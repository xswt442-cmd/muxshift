package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/internal/netguard"
)

// addressKind is what an http(s) address answers with. The two kinds are read
// differently: a playlist is parsed by core/hls, so the session gets the tracks
// the stream declares, while a plain media file is handed to the player, which
// does not enumerate tracks.
type addressKind int

const (
	addressPlaylist addressKind = iota
	addressDirect
)

// sniffBytes is how much of an address is read to decide what it answers with.
// A playlist starts with #EXTM3U, an MP4 with its ftyp box and a transport
// stream with a sync byte, so a small head is enough; the rest is never read.
const sniffBytes = 2048

// sniffAddress asks an address what it serves before anything is handed to the
// player. The decision is made from what the answer says, never from the path:
// an .m3u8 that serves a web page is refused, and a .mp4 that serves a playlist
// is read as one.
func sniffAddress(ctx context.Context, rawURL string) (addressKind, error) {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("%s is not an address this build can read: invalid URL syntax", shortURI(rawURL))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return 0, fmt.Errorf("%s is not an http or https address", shortURI(rawURL))
	}
	if parsed.User != nil {
		return 0, fmt.Errorf("%s carries a user name; this app does not read addresses with credentials", shortURI(rawURL))
	}
	if parsed.Host == "" {
		return 0, fmt.Errorf("%s names no host", shortURI(rawURL))
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, playlistFailure("reading "+shortURI(rawURL), err)
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", sniffBytes-1))
	request.Header.Set("Accept", "*/*")

	client := netguard.Client(20*time.Second, netguard.LocalHost(parsed.Hostname()))
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("reading %s stopped: %w", shortURI(rawURL), ctx.Err())
		}
		return 0, playlistFailure("reading "+shortURI(rawURL), err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return 0, fmt.Errorf("%s answered %d %s", shortURI(rawURL), response.StatusCode, http.StatusText(response.StatusCode))
	}

	head, err := io.ReadAll(io.LimitReader(response.Body, sniffBytes))
	if err != nil {
		return 0, playlistFailure("reading the first bytes of "+shortURI(rawURL), err)
	}
	kind, err := classifyAddress(response.Header.Get("Content-Type"), head)
	if err != nil {
		return 0, playlistFailure(shortURI(rawURL), err)
	}
	return kind, nil
}

// classifyAddress decides what an address serves from its declared type and the
// first bytes of the body. Both are used: a server may send no type at all, and
// a type may be wrong, so the bytes win where they are unambiguous.
func classifyAddress(contentType string, head []byte) (addressKind, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(string(head), "\ufeff"))
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))

	if strings.HasPrefix(strings.ToUpper(trimmed), "#EXTM3U") {
		return addressPlaylist, nil
	}
	switch mediaType {
	case "application/vnd.apple.mpegurl", "application/x-mpegurl", "audio/mpegurl", "audio/x-mpegurl", "application/mpegurl":
		return addressPlaylist, nil
	}

	lower := strings.ToLower(trimmed)
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" ||
		strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return 0, errors.New("it answers with a web page, not a media stream")
	}

	if looksLikeMedia(mediaType, head) {
		return addressDirect, nil
	}
	if mediaType == "" {
		return 0, errors.New("it states no content type and does not start like a media file")
	}
	return 0, fmt.Errorf("it answers with %q, which this build does not know how to play", mediaType)
}

// looksLikeMedia recognises the containers this build plays from their first
// bytes, and accepts the types a server would normally declare for them.
func looksLikeMedia(mediaType string, head []byte) bool {
	switch {
	case len(head) >= 12 && string(head[4:8]) == "ftyp": // MP4 and its relatives
		return true
	case len(head) >= 2 && head[0] == 0x47 && (len(head) < 189 || head[188] == 0x47): // MPEG transport stream
		return true
	case len(head) >= 4 && head[0] == 0x1A && head[1] == 0x45 && head[2] == 0xDF && head[3] == 0xA3: // Matroska/WebM
		return true
	case len(head) >= 4 && string(head[0:4]) == "OggS":
		return true
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "AVI ":
		return true
	case len(head) >= 4 && string(head[0:4]) == "fLaC":
		return true
	}

	switch mediaType {
	case "video/mp4", "video/quicktime", "video/x-matroska", "video/webm", "video/mp2t",
		"video/mpeg", "audio/mp4", "audio/aac", "audio/mpeg", "application/mp4",
		"application/octet-stream", "binary/octet-stream":
		return true
	}
	return strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "audio/")
}
