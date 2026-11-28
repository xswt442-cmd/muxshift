package hls

import (
	"net/http"
	"net/url"
	"time"

	"github.com/xswt442-cmd/muxshift/internal/netguard"
)

const (
	// maxPlaylistSize bounds the playlist body kept in memory.
	maxPlaylistSize = 4 << 20
	// maxRedirects bounds the redirects followed for one playlist fetch.
	maxRedirects = netguard.MaxRedirects
	// defaultTimeout bounds one fetch and one probe wait.
	defaultTimeout = 15 * time.Second
)

// Options adjusts how an address is read. Its zero value uses a short timeout
// and requires public connection addresses for public media entry points.
type Options struct {
	// HTTPClient replaces the client used for both the playlist fetch and the
	// media probe. A replacement is trusted as given: it does not get the
	// local-address redirect rule.
	HTTPClient *http.Client

	// Timeout bounds one playlist fetch and one probe wait. Zero uses
	// defaultTimeout.
	Timeout time.Duration

	// StartDistance and MaxDistance choose where a live playlist starts, in
	// segments from its end, exactly as gohlslib reads them. They are ignored
	// for a playlist that ends with EXT-X-ENDLIST, which starts at its first
	// segment.
	StartDistance int
	MaxDistance   int
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return defaultTimeout
}

func (o Options) client(endpoint *url.URL) *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	client := netguard.Client(o.timeout(), isLocalHost(endpoint.Hostname()))
	client.CheckRedirect = redirectAllowed
	return client
}
