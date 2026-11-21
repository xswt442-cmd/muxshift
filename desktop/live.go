package main

// A browser can hand over a live HLS address. Such a playlist is not a
// recording: a master playlist names variants instead of a stream, and a media
// playlist that never ends keeps growing. The import path therefore asks two
// questions before it opens a player: which address carries this stream, and
// does it end? A playlist that is protected with a key is refused here, because
// this build has nothing to fetch the key with.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// playlistReadLimit bounds one handed-over playlist kept in memory. A media
	// playlist is a few kilobytes; a master playlist with every audio group is
	// larger but still far below this.
	playlistReadLimit = 1 << 20
	// playlistTimeout bounds one playlist fetch.
	playlistTimeout = 20 * time.Second
)

// errProtectedPlaylist marks a stream this build will not open. It is separated
// from the other failures so the import can say why instead of describing a
// read that failed.
var errProtectedPlaylist = errors.New("the stream is protected")

// playlistRole is what a handed-over playlist turned out to be.
type playlistRole struct {
	// address is the media playlist a player should open: the handed-over one
	// when it is already a media playlist, or the chosen variant of a master.
	address string
	// live says the chosen playlist has no EXT-X-ENDLIST, so it goes on
	// growing instead of ending.
	live bool
}

// inspectPlaylist reads a handed-over playlist and decides what to open. It
// returns the handed-over address unchanged for a media playlist, and refuses
// a protected stream outright.
func inspectPlaylist(ctx context.Context, rawURL string) (playlistRole, error) {
	text, playlistURL, err := fetchPlaylist(ctx, rawURL)
	if err != nil {
		return playlistRole{}, err
	}
	if method := protectedMethod(text); method != "" {
		return playlistRole{}, protected(method)
	}
	if hasSeparateAudioRendition(text) {
		return playlistRole{}, fmt.Errorf("the master uses a separate audio rendition; this build opens streams with muxed audio only")
	}
	if !strings.Contains(text, "#EXT-X-STREAM-INF:") {
		return playlistRole{address: playlistURL, live: !hasEnd(text)}, nil
	}
	variant, err := pickVariant(playlistURL, text)
	if err != nil {
		return playlistRole{}, err
	}
	child, variantURL, err := fetchPlaylist(ctx, variant)
	if err != nil {
		return playlistRole{}, err
	}
	if method := protectedMethod(child); method != "" {
		return playlistRole{}, protected(method)
	}
	// A master playlist may itself end with EXT-X-ENDLIST while its variants do
	// not, so only the chosen variant answers whether the stream ends.
	return playlistRole{address: variantURL, live: !hasEnd(child)}, nil
}

func protected(method string) error {
	return fmt.Errorf("%w with %s; this build does not fetch the key that would read it", errProtectedPlaylist, method)
}

func hasEnd(text string) bool {
	return strings.Contains(text, "#EXT-X-ENDLIST")
}

// hasSeparateAudioRendition reports a master that points at an audio playlist
// through EXT-X-MEDIA. The native player opens one address, so a video variant
// plus a separate audio rendition would silently lose the sound.
func hasSeparateAudioRendition(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") &&
			strings.Contains(line, "TYPE=AUDIO") &&
			strings.Contains(line, "URI=") {
			return true
		}
	}
	return false
}

// fetchPlaylist reads one playlist body and refuses anything that is not one,
// so a page that answers with an error page cannot be mistaken for a stream.
func fetchPlaylist(ctx context.Context, rawURL string) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Accept", "application/vnd.apple.mpegurl, application/x-mpegURL, */*")
	client := &http.Client{Timeout: playlistTimeout}
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("%s could not be read: %w", shortURI(rawURL), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", "", fmt.Errorf("%s answered %s", shortURI(rawURL), response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, playlistReadLimit))
	if err != nil {
		return "", "", fmt.Errorf("%s stopped while being read: %w", shortURI(rawURL), err)
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	if !strings.HasPrefix(strings.TrimSpace(text), "#EXTM3U") {
		return "", "", fmt.Errorf("%s answered with something that is not a playlist", shortURI(rawURL))
	}
	finalURL := rawURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return text, finalURL, nil
}

// pickVariant chooses the media playlist to open from a master playlist. A
// variant that declares a picture is preferred over one that is sound only, and
// the largest of those wins; a master that declares no resolutions at all falls
// back to its widest bandwidth. Nothing is guessed from the address itself.
func pickVariant(baseURL, text string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("%s is not an address this build can read: %w", shortURI(baseURL), err)
	}
	var (
		chosen        string
		chosenHeight  int
		chosenBandwit int
	)
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		line = strings.TrimSpace(line)
		const tag = "#EXT-X-STREAM-INF:"
		if !strings.HasPrefix(line, tag) {
			continue
		}
		bandwidth, _ := strconv.Atoi(attribute(line[len(tag):], "BANDWIDTH"))
		height := resolutionHeight(attribute(line[len(tag):], "RESOLUTION"))
		uri := ""
		for next := index + 1; next < len(lines); next++ {
			candidate := strings.TrimSpace(lines[next])
			if candidate == "" || strings.HasPrefix(candidate, "#") {
				continue
			}
			uri = candidate
			break
		}
		if uri == "" {
			continue
		}
		better := chosen == ""
		if !better {
			switch {
			case height != chosenHeight:
				better = height > chosenHeight
			case bandwidth != chosenBandwit:
				better = bandwidth > chosenBandwit
			}
		}
		if !better {
			continue
		}
		reference, err := url.Parse(uri)
		if err != nil {
			continue
		}
		chosen = base.ResolveReference(reference).String()
		chosenHeight, chosenBandwit = height, bandwidth
	}
	if chosen == "" {
		return "", fmt.Errorf("%s is a master playlist that names no variant this build can open", shortURI(baseURL))
	}
	return chosen, nil
}

// resolutionHeight reads the picture height out of a RESOLUTION attribute. A
// variant without one has no height, which is how sound-only renditions look.
func resolutionHeight(resolution string) int {
	_, height, ok := strings.Cut(resolution, "x")
	if !ok {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(height))
	if err != nil {
		return 0
	}
	return value
}

// protectedMethod names the key method a playlist asks for, or an empty string
// when it asks for none. Only a playlist with no keys is read by this build.
func protectedMethod(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXT-X-KEY:") && !strings.HasPrefix(line, "#EXT-X-SESSION-KEY:") {
			continue
		}
		_, attributes, _ := strings.Cut(line, ":")
		switch method := attribute(attributes, "METHOD"); method {
		case "", "NONE":
			continue
		default:
			return method
		}
	}
	return ""
}

// attribute reads one attribute out of an HLS attribute list, honouring the
// quotes a value may carry.
func attribute(list, name string) string {
	for _, field := range splitAttributes(list) {
		key, value, ok := strings.Cut(field, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"`)
	}
	return ""
}

// splitAttributes splits an attribute list on the commas that are not inside a
// quoted value, because a RESOLUTION or CODECS value may contain one.
func splitAttributes(list string) []string {
	var (
		fields  []string
		current strings.Builder
		quoted  bool
	)
	for _, character := range list {
		switch {
		case character == '"':
			quoted = !quoted
			current.WriteRune(character)
		case character == ',' && !quoted:
			fields = append(fields, current.String())
			current.Reset()
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}
