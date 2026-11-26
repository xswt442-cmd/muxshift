package hls

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/internal/netguard"
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
		return nil, fmt.Errorf("%w: address has invalid URL syntax", core.ErrInvalid)
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
	err := netguard.CheckRedirect(req, via)
	if errors.Is(err, netguard.ErrCredentials) {
		return core.ErrCredentialsInURI
	}
	if err != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalid, err)
	}
	return nil
}

// isLocalHost reports a hostname that names the local machine or a private
// network: what a public server must not redirect a request into.
func isLocalHost(host string) bool {
	return netguard.LocalHost(host)
}
