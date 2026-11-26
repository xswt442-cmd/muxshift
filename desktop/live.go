package main

// A browser can hand over a live HLS address. Such a playlist is not a
// recording: a master playlist names variants instead of a stream, and a media
// playlist that never ends keeps growing. The import path therefore asks two
// questions before it opens a player: which address carries this stream, and
// does it end? A playlist that is protected with a key is refused here, because
// this build has nothing to fetch the key with.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/internal/netguard"
)

const (
	// playlistReadLimit bounds one handed-over playlist kept in memory. A media
	// playlist is a few kilobytes; a master playlist with every audio group is
	// larger but still far below this.
	playlistReadLimit = 1 << 20
	// playlistTimeout bounds one playlist fetch.
	playlistTimeout = 20 * time.Second
	// maxVariantChecks bounds menu enumeration against oversized masters.
	maxVariantChecks = 16
)

// errProtectedPlaylist marks a stream this build will not open. It is separated
// from the other failures so the import can say why instead of describing a
// read that failed.
var errProtectedPlaylist = errors.New("the stream is protected")

type playlistReadError struct {
	message string
	cause   error
}

func (e playlistReadError) Error() string { return e.message }

func (e playlistReadError) Unwrap() error { return e.cause }

func playlistFailure(message string, cause error) error {
	var addressErr *url.Error
	if errors.As(cause, &addressErr) {
		cause = addressErr.Err
	}
	var safeCause error
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, errProtectedPlaylist, netguard.ErrDestination} {
		if errors.Is(cause, known) {
			safeCause = known
			break
		}
	}
	return playlistReadError{message: redactSecrets(message + ": " + cause.Error()), cause: safeCause}
}

// playlistRole is what a handed-over playlist turned out to be.
type playlistRole struct {
	// address is the media playlist a player should open: the handed-over one
	// when it is already a media playlist, or the chosen variant of a master.
	address string
	master  string
	// live says the chosen playlist has no EXT-X-ENDLIST, so it goes on
	// growing instead of ending.
	live bool
}

// liveVariant is a declared master-playlist variant whose child playlist was
// read and accepted. address is kept private because it may contain a signed
// query needed by the player; callers must not display or log it.
type liveVariant struct {
	address   string
	width     int
	height    int
	bandwidth int
	live      bool
}

func (v liveVariant) String() string {
	return fmt.Sprintf("liveVariant{width:%d height:%d bandwidth:%d}", v.width, v.height, v.bandwidth)
}

type livePlaylistKind uint8

const (
	liveMediaPlaylist livePlaylistKind = iota + 1
	liveMasterPlaylist
)

// livePlaylistInspection describes selectable variants for a master, or the
// single handed-over media playlist. A media playlist has no variants.
type livePlaylistInspection struct {
	kind     livePlaylistKind
	role     playlistRole
	variants []liveVariant
}

// inspectPlaylist reads a handed-over playlist and decides what to open. It
// returns the handed-over address unchanged for a media playlist, and refuses
// a protected stream outright.
func inspectPlaylist(ctx context.Context, rawURL string) (playlistRole, error) {
	text, playlistURL, err := fetchPlaylist(ctx, rawURL, true)
	if err != nil {
		return playlistRole{}, err
	}
	if err := validateRootPlaylist(text); err != nil {
		return playlistRole{}, err
	}
	if !playlistHasTag(text, "#EXT-X-STREAM-INF:") {
		return playlistRole{address: playlistURL, live: !hasEnd(text)}, nil
	}
	variants, err := parseVariants(playlistURL, text)
	if err != nil {
		return playlistRole{}, err
	}
	chosen, err := inspectDeclaredVariant(ctx, chooseVariant(variants), isLocalPlaylistURL(playlistURL))
	if err != nil {
		return playlistRole{}, err
	}
	return playlistRole{address: chosen.address, master: playlistURL, live: chosen.live}, nil
}

// inspectLivePlaylist returns the playable variants explicitly declared by a
// master. A child counts as usable only after its playlist has been read and
// checked for protection and nested-master content.
func inspectLivePlaylist(ctx context.Context, rawURL string) (livePlaylistInspection, error) {
	text, playlistURL, err := fetchPlaylist(ctx, rawURL, true)
	if err != nil {
		return livePlaylistInspection{}, err
	}
	if err := validateRootPlaylist(text); err != nil {
		return livePlaylistInspection{}, err
	}
	if !playlistHasTag(text, "#EXT-X-STREAM-INF:") {
		return livePlaylistInspection{
			kind: liveMediaPlaylist,
			role: playlistRole{address: playlistURL, live: !hasEnd(text)},
		}, nil
	}
	declared, err := parseVariants(playlistURL, text)
	if err != nil {
		return livePlaylistInspection{}, err
	}
	usable := make([]liveVariant, 0, min(maxVariantChecks, len(declared)))
	seen := make(map[string]bool)
	allowLocal := isLocalPlaylistURL(playlistURL)
	var protectedErr error
	checks := orderedVariantChecks(declared, maxVariantChecks)
	for _, variant := range checks {
		if err := ctx.Err(); err != nil {
			break
		}
		checked, fetchErr := inspectDeclaredVariant(ctx, variant, allowLocal)
		if fetchErr != nil {
			if errors.Is(fetchErr, context.DeadlineExceeded) {
				return livePlaylistInspection{}, fetchErr
			}
			if err := ctx.Err(); err != nil {
				break
			}
			if errors.Is(fetchErr, errProtectedPlaylist) && protectedErr == nil {
				protectedErr = fetchErr
			}
			continue
		}
		if !seen[checked.address] {
			seen[checked.address] = true
			usable = append(usable, checked)
		}
	}
	if ctx.Err() != nil {
		return livePlaylistInspection{}, ctx.Err()
	}
	if len(usable) == 0 {
		if protectedErr != nil {
			return livePlaylistInspection{}, protectedErr
		}
		return livePlaylistInspection{}, fmt.Errorf("%s is a master playlist that names no readable, unprotected media variant", shortURI(playlistURL))
	}
	chosen := chooseVariant(usable)
	return livePlaylistInspection{
		kind:     liveMasterPlaylist,
		role:     playlistRole{address: chosen.address, master: playlistURL, live: chosen.live},
		variants: usable,
	}, nil
}

func validateRootPlaylist(text string) error {
	if method := protectedMethod(text); method != "" {
		return protected(method)
	}
	if hasSeparateAudioRendition(text) {
		return fmt.Errorf("the master uses a separate audio rendition; this build opens streams with muxed audio only")
	}
	if !playlistHasTag(text, "#EXT-X-STREAM-INF:") && !isMediaPlaylist(text) {
		return errors.New("the playlist contains no playable media")
	}
	return nil
}

func inspectDeclaredVariant(ctx context.Context, variant liveVariant, allowLocal bool) (liveVariant, error) {
	child, childURL, err := fetchPlaylist(ctx, variant.address, allowLocal)
	if err != nil {
		return liveVariant{}, err
	}
	if method := protectedMethod(child); method != "" {
		return liveVariant{}, protected(method)
	}
	if playlistHasTag(child, "#EXT-X-STREAM-INF:") || hasSeparateAudioRendition(child) || !isMediaPlaylist(child) {
		return liveVariant{}, fmt.Errorf("declared variant is not a muxed media playlist")
	}
	variant.address = childURL
	variant.live = !hasEnd(child)
	return variant, nil
}

func orderedVariantChecks(variants []liveVariant, limit int) []liveVariant {
	ordered := slices.Clone(variants)
	slices.SortStableFunc(ordered, func(a, b liveVariant) int {
		return cmp.Or(cmp.Compare(b.height, a.height), cmp.Compare(b.bandwidth, a.bandwidth))
	})
	return ordered[:min(limit, len(ordered))]
}

func protected(method string) error {
	return fmt.Errorf("%w with %s", errProtectedPlaylist, redactSecrets(method))
}

func hasEnd(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) == "#EXT-X-ENDLIST" {
			return true
		}
	}
	return false
}

func playlistHasTag(text, tag string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), tag) {
			return true
		}
	}
	return false
}

func isMediaPlaylist(text string) bool {
	return !playlistHasTag(text, "#EXT-X-I-FRAME-STREAM-INF:") &&
		(playlistHasTag(text, "#EXTINF:") || playlistHasTag(text, "#EXT-X-PART:"))
}

// hasSeparateAudioRendition reports a master that points at an audio playlist
// through EXT-X-MEDIA. The native player opens one address, so a video variant
// plus a separate audio rendition would silently lose the sound.
func hasSeparateAudioRendition(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		attrs, ok := strings.CutPrefix(strings.TrimSpace(line), "#EXT-X-MEDIA:")
		if ok && attribute(attrs, "TYPE") == "AUDIO" && attribute(attrs, "URI") != "" {
			return true
		}
	}
	return false
}

// fetchPlaylist reads one playlist body and refuses anything that is not one,
// so a page that answers with an error page cannot be mistaken for a stream.
func fetchPlaylist(ctx context.Context, rawURL string, allowLocal bool) (string, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validatePlaylistURL(rawURL); err != nil {
		return "", "", fmt.Errorf("%s is not an address this build can read: %w", shortURI(rawURL), err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", playlistFailure("playlist request could not be created", err)
	}
	request.Header.Set("Accept", "application/vnd.apple.mpegurl, application/x-mpegURL, */*")
	client := netguard.Client(playlistTimeout, allowLocal && isLocalPlaylistURL(rawURL))
	response, err := client.Do(request)
	if err != nil {
		return "", "", playlistFailure(shortURI(rawURL)+" could not be read", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", "", fmt.Errorf("%s answered %d %s", shortURI(rawURL), response.StatusCode, http.StatusText(response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, playlistReadLimit+1))
	if err != nil {
		return "", "", playlistFailure(shortURI(rawURL)+" stopped while being read", err)
	}
	if len(body) > playlistReadLimit {
		return "", "", fmt.Errorf("%s exceeded the %d-byte playlist limit", shortURI(rawURL), playlistReadLimit)
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	header, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if strings.TrimSpace(header) != "#EXTM3U" {
		return "", "", fmt.Errorf("%s answered with something that is not a playlist", shortURI(rawURL))
	}
	finalURL := rawURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	if err := validatePlaylistURL(finalURL); err != nil {
		return "", "", fmt.Errorf("%s redirected to an address this build cannot read", shortURI(rawURL))
	}
	return text, finalURL, nil
}

func validatePlaylistURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("address has invalid URL syntax")
	}
	return netguard.ValidateURL(u)
}

func isLocalPlaylistURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && netguard.LocalHost(u.Hostname())
}

// pickVariant chooses the media playlist to open from a master playlist. A
// variant that declares a picture is preferred over one that is sound only, and
// the largest of those wins; a master that declares no resolutions at all falls
// back to its widest bandwidth. Nothing is guessed from the address itself.
func pickVariant(baseURL, text string) (string, error) {
	variants, err := parseVariants(baseURL, text)
	if err != nil {
		return "", err
	}
	return chooseVariant(variants).address, nil
}

func parseVariants(baseURL, text string) ([]liveVariant, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%s is not an address this build can read", shortURI(baseURL))
	}
	var variants []liveVariant
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		line = strings.TrimSpace(line)
		const tag = "#EXT-X-STREAM-INF:"
		if !strings.HasPrefix(line, tag) {
			continue
		}
		attributes := line[len(tag):]
		bandwidth, _ := strconv.Atoi(attribute(attributes, "BANDWIDTH"))
		width, height := resolution(attribute(attributes, "RESOLUTION"))
		uri := ""
		for next := index + 1; next < len(lines); next++ {
			candidate := strings.TrimSpace(lines[next])
			if strings.HasPrefix(candidate, "#EXT") {
				break
			}
			if candidate == "" || strings.HasPrefix(candidate, "#") {
				continue
			}
			uri = candidate
			break
		}
		if uri == "" {
			continue
		}
		reference, err := url.Parse(uri)
		if err != nil || reference.User != nil {
			continue
		}
		resolved := base.ResolveReference(reference).String()
		if validatePlaylistURL(resolved) != nil {
			continue
		}
		variants = append(variants, liveVariant{address: resolved, width: width, height: height, bandwidth: bandwidth})
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("%s is a master playlist that names no variant this build can open", shortURI(baseURL))
	}
	return variants, nil
}

func chooseVariant(variants []liveVariant) liveVariant {
	chosen := variants[0]
	for _, candidate := range variants[1:] {
		if candidate.height > chosen.height || (candidate.height == chosen.height && candidate.bandwidth > chosen.bandwidth) {
			chosen = candidate
		}
	}
	return chosen
}

// resolutionHeight reads the picture height out of a RESOLUTION attribute. A
// variant without one has no height, which is how sound-only renditions look.
func resolutionHeight(value string) int {
	_, height := resolution(value)
	return height
}

func resolution(value string) (int, int) {
	width, height, ok := strings.Cut(strings.ToLower(value), "x")
	if !ok {
		return 0, 0
	}
	w, widthErr := strconv.Atoi(strings.TrimSpace(width))
	h, heightErr := strconv.Atoi(strings.TrimSpace(height))
	if widthErr != nil || heightErr != nil || w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
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
