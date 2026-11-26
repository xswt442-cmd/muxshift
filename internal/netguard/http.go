// Package netguard restricts HTTP requests made for media metadata.
package netguard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

var excludedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	sharedAddressSpace,
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::ffff:0:0:0/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/63"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

// PublicIP reports whether a media reader may connect to an internet address.
// Special-purpose and transition networks are excluded conservatively.
func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsPrivate() &&
		!slices.ContainsFunc(excludedPrefixes, func(prefix netip.Prefix) bool { return prefix.Contains(ip) })
}

// LocalHost recognises explicitly local entry points. DNS names other than
// localhost still require public address validation.
func LocalHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap().WithZone("")
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || excludedPrefixes[0].Contains(ip) || sharedAddressSpace.Contains(ip)
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

var (
	ErrInvalidURL  = errors.New("not an http or https address with a host")
	ErrCredentials = errors.New("media address must not contain credentials")
	ErrDestination = errors.New("a public media request requires a public destination")
	ErrRedirects   = errors.New("too many media redirects")
)

const MaxRedirects = 10

// ValidateURL checks the address independently of its network destination.
func ValidateURL(u *url.URL) error {
	if u == nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrInvalidURL
	}
	if u.User != nil {
		return ErrCredentials
	}
	return nil
}

// CheckRedirect keeps a public hop from turning into a local request.
func CheckRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return ErrRedirects
	}
	if err := ValidateURL(request.URL); err != nil {
		return err
	}
	if LocalHost(request.URL.Hostname()) {
		for _, previous := range via {
			if !LocalHost(previous.URL.Hostname()) {
				return ErrDestination
			}
		}
	}
	return nil
}

// Client permits local connections only when the caller has an explicit local
// entry point. Every request is checked, including playlist subresources.
func Client(timeout time.Duration, allowLocal bool) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: CheckRedirect, Transport: transport{allowLocal}}
}

func resolvePublicIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, networkError(err, "could not resolve the media host")
	}
	if err := validatePublicAddresses(addrs); err != nil {
		return nil, err
	}
	return addrs, nil
}

func validatePublicAddresses(addrs []netip.Addr) error {
	if len(addrs) == 0 {
		return errors.New("the media host resolved to no addresses")
	}
	for _, ip := range addrs {
		if !PublicIP(ip) {
			return ErrDestination
		}
	}
	return nil
}

var networkDialer = net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

var publicTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := resolvePublicIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range addrs {
			conn, dialErr := networkDialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if dialErr == nil {
				return conn, nil
			}
			err = dialErr
		}
		return nil, networkError(err, "could not connect to the media host")
	}
	return transport
}()

type transport struct{ allowLocal bool }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ValidateURL(req.URL); err != nil {
		return nil, err
	}
	host := req.URL.Hostname()
	if LocalHost(host) {
		if !t.allowLocal {
			return nil, ErrDestination
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		return resp, networkError(err, "could not read the local media address")
	}
	proxy, err := http.ProxyFromEnvironment(req)
	if err != nil {
		return nil, networkError(err, "invalid media proxy configuration")
	}
	if proxy == nil {
		resp, err := publicTransport.RoundTrip(req)
		if errors.Is(err, ErrDestination) {
			return nil, ErrDestination
		}
		return resp, networkError(err, "could not connect to the media host")
	}
	// HTTPS CONNECT and SOCKS name a literal destination while preserving
	// the original authority for the HTTP request and TLS verification.
	// Forward HTTP proxies would receive the original host as the destination.
	switch proxy.Scheme {
	case "socks5", "socks5h":
	case "http", "https":
		if req.URL.Scheme != "https" {
			return nil, errors.New("public HTTP media requires a direct connection or a SOCKS proxy")
		}
	default:
		return nil, errors.New("unsupported media proxy protocol")
	}
	addrs, err := resolvePublicIPs(req.Context(), host)
	if err != nil {
		return nil, err
	}
	port := req.URL.Port()
	if port == "" {
		port = "443"
		if req.URL.Scheme == "http" {
			port = "80"
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL := *proxy
	if proxy.Scheme == "https" {
		proxyURL.Scheme = "http"
		if proxy.Port() == "" {
			proxyURL.Host = net.JoinHostPort(proxy.Hostname(), "443")
		}
		dialer := tls.Dialer{NetDialer: &networkDialer, Config: &tls.Config{ServerName: proxy.Hostname()}}
		transport.DialContext = dialer.DialContext
	}
	transport.Proxy = http.ProxyURL(&proxyURL)
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{ServerName: host}
	for _, ip := range addrs {
		pinned := pinRequest(req, ip, port)
		response, requestErr := transport.RoundTrip(pinned)
		if response != nil {
			response.Request = req
			return response, networkError(requestErr, "the media proxy request failed")
		}
		err = requestErr
		if req.Context().Err() != nil {
			break
		}
	}
	return nil, networkError(err, "could not connect through the media proxy")
}

func pinRequest(req *http.Request, ip netip.Addr, port string) *http.Request {
	pinned := req.Clone(req.Context())
	pinned.URL.Host = net.JoinHostPort(ip.Unmap().String(), port)
	pinned.Host = req.URL.Host
	return pinned
}

func networkError(err error, message string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return errors.New(message)
	}
}
