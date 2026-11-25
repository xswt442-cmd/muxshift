package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func publicPlaylistIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !sharedAddressSpace.Contains(ip)
}

func resolvePlaylistIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range addrs {
		if !publicPlaylistIP(ip) {
			return nil, fmt.Errorf("playlist destination must resolve to public addresses")
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("playlist destination resolved to no addresses")
	}
	return addrs, nil
}

var publicPlaylistTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := resolvePlaylistIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		for _, ip := range addrs {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			err = dialErr
		}
		return nil, err
	}
	return transport
}()

type playlistTransport struct{ allowLocal bool }

func (t playlistTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if localPlaylistHost(host) {
		if !t.allowLocal {
			return nil, fmt.Errorf("a public playlist cannot open a local address")
		}
		return http.DefaultTransport.RoundTrip(req)
	}
	proxy, err := http.ProxyFromEnvironment(req)
	if err != nil {
		return nil, err
	}
	if proxy == nil {
		return publicPlaylistTransport.RoundTrip(req)
	}
	// HTTPS CONNECT and SOCKS name a literal destination while preserving
	// the original authority for the HTTP request and TLS verification.
	// Forward HTTP proxies would receive the original host as the destination.
	if req.URL.Scheme != "https" && !strings.HasPrefix(proxy.Scheme, "socks5") {
		return nil, fmt.Errorf("public HTTP playlists require a direct connection or a SOCKS proxy")
	}
	addrs, err := resolvePlaylistIPs(req.Context(), host)
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
		dialer := tls.Dialer{Config: &tls.Config{ServerName: proxy.Hostname()}}
		transport.DialContext = dialer.DialContext
	}
	transport.Proxy = http.ProxyURL(&proxyURL)
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{ServerName: host}
	for _, ip := range addrs {
		pinned := req.Clone(req.Context())
		pinned.URL.Host = net.JoinHostPort(ip.String(), port)
		pinned.Host = req.URL.Host
		response, requestErr := transport.RoundTrip(pinned)
		if response != nil {
			response.Request = req
			return response, requestErr
		}
		err = requestErr
		if req.Context().Err() != nil {
			break
		}
	}
	return nil, err
}
