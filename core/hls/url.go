package hls

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/xswt442-cmd/muxshift/core"
)

// checkURI accepts an absolute http or https address without user information.
// A URI carrying credentials is refused rather than stored or logged: Session
// keeps configuration, never secrets.
func checkURI(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, core.ErrEmptyURI
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: parse URI: %v", core.ErrInvalid, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%w: scheme %q is not http or https", core.ErrInvalid, u.Scheme)
	}
	if u.User != nil {
		return nil, core.ErrCredentialsInURI
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: URI has no host", core.ErrInvalid)
	}
	return u, nil
}

// redirectAllowed follows a redirect only when it cannot turn a public address
// into an internal request. A stream that started on the local machine or the
// local network may keep redirecting inside it; a stream that started in
// public may not be sent to a loopback, private, link-local or unspecified
// address by the remote server.
func redirectAllowed(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: more than %d redirects", core.ErrInvalid, maxRedirects)
	}
	target := req.URL
	switch strings.ToLower(target.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: redirect to scheme %q", core.ErrInvalid, target.Scheme)
	}
	if target.User != nil {
		return core.ErrCredentialsInURI
	}
	if len(via) == 0 || isLocalHost(via[0].URL.Hostname()) {
		return nil
	}
	if isLocalHost(target.Hostname()) {
		return fmt.Errorf("%w: redirect from public host %s to local address %s", core.ErrInvalid, via[0].URL.Hostname(), target.Hostname())
	}
	return nil
}

// isLocalHost reports a hostname that names the local machine or a private
// network: what a public server must not redirect a request into.
func isLocalHost(host string) bool {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified()
	}
	lowered := strings.ToLower(trimmed)
	return lowered == "localhost" || strings.HasSuffix(lowered, ".localhost")
}
