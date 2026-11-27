package netguard

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveAndDialPinsValidatedIP(t *testing.T) {
	public := netip.MustParseAddr("9.9.9.9")
	private := netip.MustParseAddr("127.0.0.1")
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint("mixed=", mixed), func(t *testing.T) {
			var lookups, dials atomic.Int32
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				if address != "9.9.9.9:8443" {
					t.Errorf("dial received %q, want the validated literal IP", address)
				}
				client, server := net.Pipe()
				t.Cleanup(func() { server.Close() })
				return client, nil
			}
			transport := newMediaTransport(base, func(_ context.Context, network, host string) ([]netip.Addr, error) {
				if network != "ip" || host != "media.example.test" {
					t.Errorf("unexpected lookup of %s/%s", network, host)
				}
				if lookups.Add(1) > 1 {
					return []netip.Addr{private}, nil
				}
				if mixed {
					return []netip.Addr{public, private}, nil
				}
				return []netip.Addr{public}, nil
			}, nil)
			conn, err := transport.direct.DialContext(context.Background(), "tcp", "media.example.test:8443")
			if mixed {
				if !errors.Is(err, ErrDestination) || dials.Load() != 0 {
					t.Fatalf("mixed addresses returned %v and dialed %d times", err, dials.Load())
				}
			} else {
				if err != nil || dials.Load() != 1 {
					t.Fatalf("validated address returned %v and dialed %d times", err, dials.Load())
				}
				conn.Close()
			}
			if lookups.Load() != 1 {
				t.Fatalf("resolved the destination %d times", lookups.Load())
			}
		})
	}
}

func TestProxyPinningAndHTTPForwardProxyRejected(t *testing.T) {
	resolve := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("9.9.9.9")}, nil
	}
	t.Run("forward proxy refused", func(t *testing.T) {
		var dials atomic.Int32
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.DialContext = func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		}
		for _, tc := range []struct{ scheme, reason string }{
			{"http", "direct connection or a SOCKS proxy"},
			{"https", "direct connection or a SOCKS proxy"},
			{"ftp", "unsupported media proxy protocol"},
		} {
			transport := newMediaTransport(base, resolve, func(*http.Request) (*url.URL, error) {
				return &url.URL{Scheme: tc.scheme, Host: "127.0.0.1:1"}, nil
			})
			_, err := transport.client(time.Second, false).Get("http://media.example.test/index.m3u8")
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("%s proxy returned %v, want %q", tc.scheme, err, tc.reason)
			}
		}
		if dials.Load() != 0 {
			t.Fatalf("refused proxy attempts dialed %d times", dials.Load())
		}
	})
	t.Run("HTTP CONNECT", func(t *testing.T) {
		type authority struct{ host, sni string }
		observed := make(chan authority, 1)
		origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observed <- authority{r.Host, r.TLS.ServerName}
			fmt.Fprint(w, "#EXTM3U")
		}))
		defer origin.Close()
		target := make(chan string, 1)
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				t.Errorf("proxy received %s, want CONNECT", r.Method)
				http.Error(w, "CONNECT required", http.StatusBadRequest)
				return
			}
			target <- r.Host
			backend, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Error(err)
				http.Error(w, "fixture connection", http.StatusBadGateway)
				return
			}
			defer backend.Close()
			frontend, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer frontend.Close()
			frontend.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprint(buffer, "HTTP/1.1 200 Connection Established\r\n\r\n")
			if err := buffer.Flush(); err != nil {
				t.Error(err)
				return
			}
			copied := make(chan struct{})
			go func() {
				defer close(copied)
				_, _ = io.Copy(backend, buffer)
			}()
			_, _ = io.Copy(frontend, backend)
			frontend.Close()
			<-copied
		}))
		defer proxy.Close()
		base := http.DefaultTransport.(*http.Transport).Clone()
		roots := x509.NewCertPool()
		roots.AddCert(origin.Certificate())
		base.TLSClientConfig = &tls.Config{RootCAs: roots}
		proxyURL, err := url.Parse(proxy.URL)
		if err != nil {
			t.Fatal(err)
		}
		client := newMediaTransport(base, resolve, http.ProxyURL(proxyURL)).client(3*time.Second, false)
		resp, err := client.Get("https://example.com:8443/index.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || string(body) != "#EXTM3U" {
			t.Fatalf("proxied response = %q, %v", body, err)
		}
		if got := <-target; got != "9.9.9.9:8443" {
			t.Errorf("CONNECT authority = %q", got)
		}
		if got := <-observed; got != (authority{"example.com:8443", "example.com"}) {
			t.Errorf("origin authority and TLS SNI = %+v", got)
		}
	})
	t.Run("SOCKS5", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		finished := make(chan error, 1)
		go func() { finished <- serveSOCKSFixture(listener) }()
		base := http.DefaultTransport.(*http.Transport).Clone()
		proxy := &url.URL{Scheme: "socks5", Host: listener.Addr().String()}
		client := newMediaTransport(base, resolve, http.ProxyURL(proxy)).client(3*time.Second, false)
		resp, err := client.Get("http://media.example.test/index.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if body, err := io.ReadAll(resp.Body); err != nil || string(body) != "#EXTM3U" {
			t.Fatalf("proxied response = %q, %v", body, err)
		}
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	})
}

func serveSOCKSFixture(listener net.Listener) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return err
	}
	if string(greeting) != "\x05\x01\x00" {
		return fmt.Errorf("SOCKS5 greeting = %x", greeting)
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return err
	}
	destination := make([]byte, 10)
	if _, err := io.ReadFull(conn, destination); err != nil {
		return err
	}
	if got, want := string(destination), "\x05\x01\x00\x01\x09\x09\x09\x09\x00\x50"; got != want {
		return fmt.Errorf("SOCKS5 CONNECT = %x, want literal IPv4 and port 80", destination)
	}
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return err
	}
	defer req.Body.Close()
	if req.Host != "media.example.test" || req.URL.Path != "/index.m3u8" {
		return fmt.Errorf("SOCKS5 origin saw %s %s", req.Host, req.URL.Path)
	}
	_, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 7\r\nConnection: close\r\n\r\n#EXTM3U")
	return err
}

func TestCheckRedirectAndLocalHostEdgeCases(t *testing.T) {
	var protectedHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/local":
			http.Redirect(w, r, "http://media.example.test/public", http.StatusFound)
		case "/public":
			local := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
			http.Redirect(w, r, "http://"+local.String()+"/protected", http.StatusFound)
		case "/protected":
			protectedHits.Add(1)
		}
	}))
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	dialer := net.Dialer{}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "9.9.9.9:80" && address != server.Listener.Addr().String() {
			return nil, fmt.Errorf("unexpected fixture address %q", address)
		}
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	resolver := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("9.9.9.9")}, nil
	}
	transport := newMediaTransport(base, resolver, func(*http.Request) (*url.URL, error) { return nil, nil })
	for _, tc := range []struct {
		address    string
		allowLocal bool
	}{
		{"http://media.example.test/public", false},
		{server.URL + "/local", true},
	} {
		_, err := transport.client(time.Second, tc.allowLocal).Get(tc.address)
		if !errors.Is(err, ErrDestination) {
			t.Errorf("redirect from %s returned %v, want a destination refusal", tc.address, err)
		}
	}
	if hits := protectedHits.Load(); hits != 0 {
		t.Errorf("refused redirects reached the local destination %d times", hits)
	}
	for _, host := range []string{"::", "0.0.0.1"} {
		if !LocalHost(host) {
			t.Errorf("%s was classified as public", host)
		}
	}
	for _, address := range []string{"::ffff:9.9.9.9%eth0", "2606:4700::1111%eth0"} {
		if PublicIP(netip.MustParseAddr(address)) {
			t.Errorf("zoned address %s was classified as public", address)
		}
	}
}

func TestPublicIP(t *testing.T) {
	for _, address := range []string{"9.9.9.9", "2606:4700::1111", "::ffff:9.9.9.9"} {
		if !PublicIP(netip.MustParseAddr(address)) {
			t.Errorf("ordinary public address %s was refused", address)
		}
	}
	for _, address := range []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.0.1", "172.16.0.1",
		"192.0.0.1", "192.0.2.1", "192.168.0.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"::1", "::2", "::ffff:127.0.0.1", "64:ff9b::1", "64:ff9b:1::1", "100::1",
		"2001::1", "2001:db8::1", "2002::1", "3fff::1", "fc00::1", "fe80::1", "fec0::1",
	} {
		if PublicIP(netip.MustParseAddr(address)) {
			t.Errorf("special address %s was classified as public", address)
		}
	}
}

func TestClientRequiresExplicitLocalAddress(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "#EXTM3U")
	}))
	defer server.Close()

	if response, err := Client(time.Second, false).Get(server.URL); !errors.Is(err, ErrDestination) {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("public media client returned %v, want a destination refusal", err)
	}
	if requests.Load() != 0 {
		t.Fatal("the denied connection reached the local media server")
	}
	response, err := Client(time.Second, true).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || requests.Load() != 1 {
		t.Fatalf("explicit local request returned HTTP %d with %d requests", response.StatusCode, requests.Load())
	}
}

func TestValidatePublicAddressesRequiresEveryAddressToBePublic(t *testing.T) {
	public := netip.MustParseAddr("9.9.9.9")
	reserved := netip.MustParseAddr("192.0.2.1")
	if err := validatePublicAddresses([]netip.Addr{public}); err != nil {
		t.Fatalf("public addresses refused: %v", err)
	}
	if err := validatePublicAddresses([]netip.Addr{public, reserved}); !errors.Is(err, ErrDestination) {
		t.Fatalf("mixed addresses returned %v, want destination refusal", err)
	}
}

func TestPinRequestKeepsOriginalHTTPAuthority(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://media.example.test:8443/master.m3u8?profile=low", nil)
	if err != nil {
		t.Fatal(err)
	}
	pinned := pinRequest(req, netip.MustParseAddr("2606:4700::1111"), "8443")
	if pinned.URL.Host != "[2606:4700::1111]:8443" || pinned.Host != "media.example.test:8443" ||
		req.URL.Host != "media.example.test:8443" || pinned.URL.RequestURI() != req.URL.RequestURI() {
		t.Fatal("pinning changed the original HTTP authority or the playlist path")
	}
}

func TestHTTPSProxyHandshakeClosesAfterRequestTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = io.Copy(io.Discard, conn)
		closed <- err
	}()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSHandshakeTimeout = 500 * time.Millisecond
	proxy := &url.URL{Scheme: "https", Host: listener.Addr().String()}
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("9.9.9.9")}, nil
	}
	client := newMediaTransport(base, lookup, http.ProxyURL(proxy)).client(250*time.Millisecond, false)
	if _, err := client.Get("https://media.example.test/playlist.m3u8"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request returned %v, want a deadline error", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("proxy did not observe the client close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy connection remained open after the handshake deadline")
	}
}
