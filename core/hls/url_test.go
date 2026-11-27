package hls

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

func TestReadAndProbeRefusePublicToPrivateRedirect(t *testing.T) {
	var localRequests atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localRequests.Add(1)
		http.Error(w, "redirect reached local fixture", http.StatusBadRequest)
	}))
	defer local.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, local.URL+"/hidden.m3u8?fixture-signature", http.StatusFound)
	}))
	defer public.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	dialer := net.Dialer{}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "media.example.test:80" {
			address = public.Listener.Addr().String()
		}
		return dialer.DialContext(ctx, network, address)
	}
	opts := Options{HTTPClient: &http.Client{Transport: base, CheckRedirect: redirectAllowed}}
	address := "http://media.example.test/live.m3u8?fixture-signature#fixture-fragment"
	_, readErr := Read(context.Background(), address, opts)
	_, probeErr := Probe(context.Background(), address, opts)
	for _, err := range []error{readErr, probeErr} {
		if !errorsIs(err, core.ErrInvalid) || strings.Contains(err.Error(), "fixture-") {
			t.Errorf("redirect error = %v, want redacted destination refusal", err)
		}
	}
	if hits := localRequests.Load(); hits != 0 {
		t.Fatalf("the refused destination received %d requests", hits)
	}
}

// TestCheckURI fixes what an address may look like: http or https, a host, and
// no credentials.
func TestCheckURI(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{name: "https", raw: "https://example.com/live/index.m3u8"},
		{name: "http", raw: "http://example.com:8080/index.m3u8"},
		{name: "padded", raw: "  https://example.com/a.m3u8  "},
		{name: "empty", raw: "   ", want: core.ErrEmptyURI},
		{name: "credentials", raw: "https://user:secret@example.com/a.m3u8", want: core.ErrCredentialsInURI},
		{name: "user only", raw: "https://user@example.com/a.m3u8", want: core.ErrCredentialsInURI},
		{name: "file", raw: "file:///tmp/a.m3u8", want: core.ErrInvalid},
		{name: "rtsp", raw: "rtsp://example.com/a", want: core.ErrInvalid},
		{name: "no host", raw: "https:///a.m3u8", want: core.ErrInvalid},
		{name: "garbage", raw: "https://exa mple.com/a.m3u8", want: core.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkURI(tc.raw)
			if tc.want != nil {
				if !errorsIs(err, tc.want) {
					t.Fatalf("checkURI(%q) error = %v, want %v", tc.raw, err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkURI(%q): %v", tc.raw, err)
			}
			if got.Hostname() == "" {
				t.Fatalf("checkURI(%q) kept no host", tc.raw)
			}
		})
	}
}

// TestIsLocalHost fixes which hosts count as local, since a public server must
// not be able to redirect a request into them.
func TestIsLocalHost(t *testing.T) {
	local := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.9", "192.168.1.10",
		"169.254.169.254", "0.0.0.0", "localhost", "LOCALHOST", "app.localhost",
	}
	for _, host := range local {
		if !isLocalHost(host) {
			t.Fatalf("isLocalHost(%q) = false, want true", host)
		}
	}
	public := []string{"example.com", "8.8.8.8", "203.0.113.7", "cdn.example.net", "localhost.example.com"}
	for _, host := range public {
		if isLocalHost(host) {
			t.Fatalf("isLocalHost(%q) = true, want false", host)
		}
	}
}

// TestRedirectAllowed checks the redirect rule: a stream that started in public
// cannot be redirected into a local address, and everything else follows the
// usual scheme and credential checks.
func TestRedirectAllowed(t *testing.T) {
	via := func(raw string) []*http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return []*http.Request{{URL: u}}
	}
	target := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return &http.Request{URL: u}
	}

	if err := redirectAllowed(target("https://cdn.example.com/b.m3u8"), via("https://example.com/a.m3u8")); err != nil {
		t.Fatalf("public to public redirect refused: %v", err)
	}
	if err := redirectAllowed(target("http://127.0.0.1:9000/b.m3u8"), via("http://127.0.0.1:8000/a.m3u8")); err != nil {
		t.Fatalf("local to local redirect refused: %v", err)
	}
	if err := redirectAllowed(target("http://169.254.169.254/latest"), via("https://example.com/a.m3u8")); !errorsIs(err, core.ErrInvalid) {
		t.Fatalf("public to link-local redirect error = %v, want ErrInvalid", err)
	}
	if err := redirectAllowed(target("http://10.0.0.5/a.m3u8"), via("https://example.com/a.m3u8")); !errorsIs(err, core.ErrInvalid) {
		t.Fatalf("public to private redirect error = %v, want ErrInvalid", err)
	}
	if err := redirectAllowed(target("https://user:secret@example.org/a.m3u8"), via("https://example.com/a.m3u8")); !errorsIs(err, core.ErrCredentialsInURI) {
		t.Fatalf("credentialed redirect error = %v, want ErrCredentialsInURI", err)
	}
	if err := redirectAllowed(target("ftp://example.org/a.m3u8"), via("https://example.com/a.m3u8")); !errorsIs(err, core.ErrInvalid) {
		t.Fatalf("scheme redirect error = %v, want ErrInvalid", err)
	}

	tooMany := make([]*http.Request, maxRedirects)
	if err := redirectAllowed(target("https://example.org/a.m3u8"), tooMany); !errorsIs(err, core.ErrInvalid) {
		t.Fatalf("redirect chain error = %v, want ErrInvalid", err)
	}
}
