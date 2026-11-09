package hls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bluenviron/gohlslib/v2/pkg/playlist"

	"github.com/xswt442-cmd/muxshift/core"
)

// Errors reported by reading an address. They are wrapped with the address and
// the operation, so callers match them with errors.Is.
var (
	// ErrTimeout reports a read that did not finish inside the timeout.
	ErrTimeout = errors.New("reading media timed out")
	// ErrResponse reports a playlist address that answered with a status other
	// than 200 OK.
	ErrResponse = errors.New("unexpected playlist response")
	// ErrPlaylistTooLarge reports a playlist body over maxPlaylistSize.
	ErrPlaylistTooLarge = errors.New("playlist exceeds the size limit")
	// ErrNoSegments reports a media playlist that lists no segment.
	ErrNoSegments = errors.New("media playlist has no segments")
	// ErrMasterPlaylist reports a multivariant playlist where a media playlist
	// is required.
	ErrMasterPlaylist = errors.New("playlist is a multivariant list")
	// ErrNoTracks reports a stream that announced no usable track.
	ErrNoTracks = errors.New("stream announced no usable track")
)

// Kind distinguishes the two playlist shapes this package reads.
type Kind int

const (
	// KindMedia is a media playlist: segments and tracks.
	KindMedia Kind = iota
	// KindMaster is a multivariant playlist: it points at other playlists.
	KindMaster
)

// String returns the spelling used in diagnostics.
func (k Kind) String() string {
	switch k {
	case KindMedia:
		return "media"
	case KindMaster:
		return "master"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Segment describes one media segment as the playlist states it.
type Segment struct {
	// Sequence is the media sequence number of this segment.
	Sequence int
	// Duration is the stated EXTINF duration.
	Duration time.Duration
	// URI is the segment address resolved against the playlist address.
	URI string
	// Discontinuity reports an EXT-X-DISCONTINUITY before this segment: its
	// timestamps do not follow the previous segment's.
	Discontinuity bool
	// Gap reports an EXT-X-GAP: the segment is intentionally absent.
	Gap bool
	// ProgramDateTime is the EXT-X-PROGRAM-DATE-TIME of this segment.
	ProgramDateTime time.Time
	// HasProgramDateTime reports whether the playlist stated one. Without it
	// the segment has no common time with another stream.
	HasProgramDateTime bool
}

// Info describes a playlist as read.
type Info struct {
	// URI is the address that was read, without any credentials.
	URI string
	// Kind is media or master.
	Kind Kind
	// Version is EXT-X-VERSION, zero when absent.
	Version int
	// TargetDuration is EXT-X-TARGETDURATION.
	TargetDuration time.Duration
	// MediaSequence is EXT-X-MEDIA-SEQUENCE.
	MediaSequence int
	// Duration is the sum of the segment durations.
	Duration time.Duration
	// EndList reports EXT-X-ENDLIST: the playlist will not grow.
	EndList bool
	// Segments are the listed segments in playlist order. A master playlist
	// has none.
	Segments []Segment
}

// Read fetches the media playlist at rawURL and describes it. It fetches one
// document, follows redirects under the local-address rule, and never reads a
// segment.
func Read(ctx context.Context, rawURL string, opts Options) (Info, error) {
	endpoint, err := checkURI(rawURL)
	if err != nil {
		return Info{}, err
	}

	body, finalURL, err := fetchPlaylist(ctx, endpoint, opts)
	if err != nil {
		return Info{}, err
	}

	parsed, err := playlist.Unmarshal(body)
	if err != nil {
		return Info{}, fmt.Errorf("parse playlist %s: %w", finalURL.Redacted(), core.ErrInvalid)
	}

	media, ok := parsed.(*playlist.Media)
	if !ok {
		return Info{URI: finalURL.Redacted(), Kind: KindMaster}, fmt.Errorf("%w: %s", ErrMasterPlaylist, finalURL.Redacted())
	}

	return describe(finalURL, media)
}

// fetchPlaylist returns the playlist body and the address that answered after
// redirects.
func fetchPlaylist(ctx context.Context, endpoint *url.URL, opts Options) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: build request: %v", core.ErrInvalid, err)
	}
	req.Header.Set("Accept", "application/vnd.apple.mpegurl, application/x-mpegURL, */*")

	resp, err := opts.client().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("read playlist %s: %w", endpoint.Redacted(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%w: %s answered %s", ErrResponse, endpoint.Redacted(), resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistSize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read playlist %s: %w", endpoint.Redacted(), err)
	}
	if len(body) > maxPlaylistSize {
		return nil, nil, fmt.Errorf("%w: %s", ErrPlaylistTooLarge, endpoint.Redacted())
	}
	return body, resp.Request.URL, nil
}

// describe turns the decoded playlist into the Core-facing description.
func describe(base *url.URL, media *playlist.Media) (Info, error) {
	info := Info{
		URI:            base.Redacted(),
		Kind:           KindMedia,
		Version:        media.Version,
		TargetDuration: time.Duration(media.TargetDuration) * time.Second,
		MediaSequence:  media.MediaSequence,
		EndList:        media.Endlist,
	}
	if len(media.Segments) == 0 {
		return info, fmt.Errorf("%w: %s", ErrNoSegments, base.Redacted())
	}

	for i, seg := range media.Segments {
		resolved := seg.URI
		if rel, err := url.Parse(seg.URI); err == nil {
			resolved = base.ResolveReference(rel).String()
		}
		item := Segment{
			Sequence:      media.MediaSequence + i,
			Duration:      seg.Duration,
			URI:           resolved,
			Discontinuity: seg.Discontinuity,
			Gap:           seg.Gap,
		}
		if seg.DateTime != nil {
			item.ProgramDateTime = *seg.DateTime
			item.HasProgramDateTime = true
		}
		info.Duration += item.Duration
		info.Segments = append(info.Segments, item)
	}
	return info, nil
}
