// Package dvr keeps a bounded, replayable window of an HLS source on local
// disk and reports exactly which media times can be replayed.
//
// The buffer stores whole MPEG-TS segments, because those are what the playlist
// already declares and what the player can fetch again. It never rewrites
// media, never transcodes and never hides a gap: a segment that is missing,
// short, or does not start on a transport-stream packet boundary is recorded as
// a gap with a reason, and ranges are split there. A caller that asks for a
// time inside a gap gets a refusal that names the gap, not a plausible-looking
// range that would play the wrong content.
//
// The cache is on disk under a directory the buffer owns. Segment files are
// named by media sequence number only; the source address, including any query
// string or credentials, is never part of a file name or a log line.
package dvr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// Errors reported by the buffer. They are wrapped with the segment and the
// operation, so callers match them with errors.Is.
var (
	// ErrNotPlayable reports a segment that was fetched but cannot be handed to
	// a player: empty, truncated, not a whole number of transport-stream
	// packets, or an HTML error page instead of media.
	ErrNotPlayable = errors.New("segment is not playable")
	// ErrTooLarge reports a segment over maxSegmentSize.
	ErrTooLarge = errors.New("segment exceeds the size limit")
	// ErrNoRange reports a request for a media time the buffer holds no
	// contiguous run for.
	ErrNoRange = errors.New("media time is not in a buffered range")
	// ErrNoSpace reports a buffer whose directory cannot hold another segment.
	ErrNoSpace = errors.New("buffer directory is not writable")
)

const (
	// maxSegmentSize bounds one segment download.
	maxSegmentSize = 32 << 20
	// tsPacket is the fixed MPEG-TS packet size.
	tsPacket = 188
	// syncByte starts every MPEG-TS packet.
	syncByte = 0x47
)

// Segment is one buffered segment and where it sits on the source's own clock.
type Segment struct {
	// Sequence is the media sequence number the playlist gave it.
	Sequence int
	// Start and End are media times on this source's clock, measured from the
	// first segment this buffer stored. They are contiguous with the previous
	// segment only inside one range.
	Start time.Duration
	End   time.Duration
	// Duration is the stated EXTINF duration.
	Duration time.Duration
	// Path is the local file, named by sequence number.
	Path string
	// Bytes is the stored size.
	Bytes int64
	// ProgramDateTime is the playlist's EXT-X-PROGRAM-DATE-TIME, when stated.
	ProgramDateTime time.Time
	// HasProgramDateTime reports whether the playlist stated one.
	HasProgramDateTime bool
	// split marks the first segment of a range: the segment stored before it is
	// not contiguous with it, either because media is missing or because the
	// playlist said the timestamps jump.
	split bool
}

// Gap is a stretch of media time the buffer cannot replay.
type Gap struct {
	// Start and End are media times. End equals Start when the length is
	// unknown, for example after a download that failed with no playlist
	// timestamps on either side.
	Start time.Duration
	End   time.Duration
	// KnownLength reports whether End is a measured end rather than a marker.
	KnownLength bool
	// Reason says what is missing, for example "segment 7: 404 Not Found".
	Reason string
}

// String returns the spelling used in diagnostics.
func (g Gap) String() string {
	if !g.KnownLength {
		return fmt.Sprintf("gap at %s (%s)", g.Start.Round(time.Millisecond), g.Reason)
	}
	return fmt.Sprintf("gap %s..%s (%s)", g.Start.Round(time.Millisecond), g.End.Round(time.Millisecond), g.Reason)
}

// Range is one contiguous stretch of replayable media time.
type Range struct {
	// Start is the media time of the first stored segment's start.
	Start time.Duration
	// End is the media time where the last stored segment ends.
	End time.Duration
	// Segments are the stored segments making up the range, in order.
	Segments []Segment
}

// Contains reports whether a media time falls inside the range. The end is
// exclusive, so a time exactly at the boundary belongs to the next range.
func (r Range) Contains(at time.Duration) bool {
	return at >= r.Start && at < r.End
}

// String returns the spelling used in diagnostics.
func (r Range) String() string {
	return fmt.Sprintf("%s..%s (%d segments)", r.Start.Round(time.Millisecond), r.End.Round(time.Millisecond), len(r.Segments))
}

// Options bounds what the buffer may keep.
type Options struct {
	// Dir is the directory the buffer owns. It is created when missing and
	// removed when the buffer is closed.
	Dir string
	// Retention is how much media time to keep. Zero keeps everything the
	// byte limit allows.
	Retention time.Duration
	// MaxBytes is how many bytes of segments to keep. Zero means the byte limit
	// is not enforced.
	MaxBytes int64
}

// Buffer is one source's rolling window.
type Buffer struct {
	opts Options

	mu       sync.Mutex
	segments []Segment
	gaps     []Gap
	bytes    int64
	next     time.Duration
	expected int
	seen     bool
	pending  *Gap
	closed   bool
	dirOwned bool
}

// New creates the buffer's directory and returns it empty. The directory is
// removed by Close when the buffer created it, so a session that exits leaves no
// cache behind.
func New(opts Options) (*Buffer, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return nil, fmt.Errorf("%w: buffer needs a directory", core.ErrInvalid)
	}
	if opts.Retention < 0 || opts.MaxBytes < 0 {
		return nil, fmt.Errorf("%w: retention and size limits cannot be negative", core.ErrInvalid)
	}
	owned := false
	if _, err := os.Stat(opts.Dir); errors.Is(err, os.ErrNotExist) {
		owned = true
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSpace, err)
	}
	return &Buffer{opts: opts, dirOwned: owned}, nil
}

// Add fetches one segment, checks that it can be played, stores it, and places
// it on the media clock. A segment that cannot be played is not stored: the
// buffer records a gap with the reason and returns the error, so the window
// never claims more than it can replay.
func (b *Buffer) Add(ctx context.Context, client *http.Client, segment hls.Segment) (Segment, error) {
	if client == nil {
		client = http.DefaultClient
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return Segment{}, fmt.Errorf("add segment %d: %w", segment.Sequence, core.ErrClosed)
	}
	if err := checkSegmentURI(segment.URI); err != nil {
		b.noteGap(segment, err)
		return Segment{}, err
	}

	body, err := fetchSegment(ctx, client, segment.URI)
	if err != nil {
		b.noteGap(segment, err)
		return Segment{}, err
	}
	if err := validateTransportStream(body); err != nil {
		b.noteGap(segment, err)
		return Segment{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Segment{}, fmt.Errorf("add segment %d: %w", segment.Sequence, core.ErrClosed)
	}

	path := filepath.Join(b.opts.Dir, fmt.Sprintf("seg-%08d.ts", segment.Sequence))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		b.recordGap(fmt.Errorf("write segment %d: %v", segment.Sequence, err))
		return Segment{}, fmt.Errorf("%w: %v", ErrNoSpace, err)
	}

	start := b.next
	split := false
	// A failed download left an open gap; this segment is what comes after it.
	if b.pending != nil {
		b.closePending(segment)
		start = b.next
		split = true
	}
	if b.seen && segment.Sequence != b.expected {
		// The playlist skipped forward: the media between the last stored
		// segment and this one is not in the buffer. Its length is measured from
		// the playlist timestamps when both sides state one.
		gapStart := b.next
		gapEnd := gapStart
		known := false
		if last := b.last(); last != nil && last.HasProgramDateTime && segment.HasProgramDateTime {
			if length := segment.ProgramDateTime.Sub(last.ProgramDateTime) - last.Duration; length >= 0 {
				gapEnd = gapStart + length
				known = true
			}
		}
		b.gaps = append(b.gaps, Gap{
			Start:       gapStart,
			End:         gapEnd,
			KnownLength: known,
			Reason:      fmt.Sprintf("playlist skipped from segment %d to %d", b.expected-1, segment.Sequence),
		})
		if known {
			b.next = gapEnd
		}
		start = b.next
		split = true
	}
	if b.seen && segment.Discontinuity {
		// The playlist says this segment's timestamps do not follow the
		// previous one, so the two cannot share a replayable range.
		split = true
	}

	stored := Segment{
		Sequence:           segment.Sequence,
		Start:              start,
		End:                start + segment.Duration,
		Duration:           segment.Duration,
		Path:               path,
		Bytes:              int64(len(body)),
		ProgramDateTime:    segment.ProgramDateTime,
		HasProgramDateTime: segment.HasProgramDateTime,
		split:              split,
	}
	b.segments = append(b.segments, stored)
	b.bytes += stored.Bytes
	b.next = stored.End
	b.expected = segment.Sequence + 1
	b.seen = true
	return stored, nil
}

// noteGap records why a segment could not be stored. The gap's length is closed
// when the next playable segment arrives, because only then is the media time
// after it known.
func (b *Buffer) noteGap(segment hls.Segment, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recordGap(fmt.Errorf("segment %d: %v", segment.Sequence, err))
	b.expected = segment.Sequence + 1
}

// recordGap appends a gap, merging it into an open one that has no measured end
// yet. The caller holds the lock.
func (b *Buffer) recordGap(err error) {
	if b.pending != nil {
		b.pending.Reason += "; " + err.Error()
		return
	}
	gap := Gap{Start: b.next, End: b.next, Reason: err.Error()}
	b.gaps = append(b.gaps, gap)
	b.pending = &b.gaps[len(b.gaps)-1]
}

// closePending measures the open gap from the playlist timestamps of the last
// stored segment and the incoming one, so a rejection reports a length instead
// of a point. The caller holds the lock.
func (b *Buffer) closePending(next hls.Segment) {
	if b.pending == nil {
		return
	}
	if last := b.last(); last != nil && last.HasProgramDateTime && next.HasProgramDateTime {
		if length := next.ProgramDateTime.Sub(last.ProgramDateTime) - last.Duration; length > 0 {
			b.pending.End = b.next + length
			b.pending.KnownLength = true
			b.next = b.pending.End
		}
	}
	b.pending = nil
}

// last returns the newest stored segment, or nil. The caller holds the lock.
func (b *Buffer) last() *Segment {
	if len(b.segments) == 0 {
		return nil
	}
	return &b.segments[len(b.segments)-1]
}

// Ranges returns every contiguous stretch of stored media time, oldest first.
// A gap ends one range and starts the next, so a caller never mistakes the
// window for one unbroken stretch.
func (b *Buffer) Ranges() []Range {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ranges()
}

// ranges computes the ranges. The caller holds the lock.
func (b *Buffer) ranges() []Range {
	if len(b.segments) == 0 {
		return nil
	}
	out := make([]Range, 0, 1)
	current := Range{Start: b.segments[0].Start}
	for _, segment := range b.segments {
		if len(current.Segments) > 0 && (segment.split || segment.Start != current.End) {
			out = append(out, current)
			current = Range{Start: segment.Start}
		}
		current.Segments = append(current.Segments, segment)
		current.End = segment.End
	}
	return append(out, current)
}

// Available returns the newest contiguous range and where it ends. That is the
// part of the window a player can keep replaying while the source runs on; the
// older ranges stay listed by Ranges even when a gap separates them.
func (b *Buffer) Available() (Range, bool) {
	ranges := b.Ranges()
	if len(ranges) == 0 {
		return Range{}, false
	}
	return ranges[len(ranges)-1], true
}

// Locate returns the stored segment whose media time contains at, so a replay
// can start at the segment boundary at or before it.
func (b *Buffer) Locate(at time.Duration) (Segment, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, current := range b.ranges() {
		if !current.Contains(at) {
			continue
		}
		for _, segment := range current.Segments {
			if at >= segment.Start && at < segment.End {
				return segment, nil
			}
		}
		return current.Segments[len(current.Segments)-1], nil
	}
	for _, gap := range b.gaps {
		if at >= gap.Start && at <= gap.End {
			return Segment{}, fmt.Errorf("%w: %s", ErrNoRange, gap.String())
		}
	}
	return Segment{}, fmt.Errorf("%w: %s is outside %s", ErrNoRange, at.Round(time.Millisecond), b.describeLocked())
}

// Playlist renders the stored recording that starts at from as an HLS playlist
// whose segment entries are the file names beside it. A start inside a segment
// begins at that segment's first frame, because a transport stream segment
// cannot be cut.
func (b *Buffer) Playlist(from time.Duration) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", core.ErrClosed
	}
	chosen, err := b.rangeAtLocked(from)
	if err != nil {
		return "", err
	}
	target := 1
	for _, segment := range chosen.Segments {
		if segment.End > from && int(segment.Duration.Seconds())+1 > target {
			target = int(segment.Duration.Seconds()) + 1
		}
	}
	var body strings.Builder
	body.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	fmt.Fprintf(&body, "#EXT-X-TARGETDURATION:%d\n", target)
	body.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	for _, segment := range chosen.Segments {
		if segment.End <= from {
			continue
		}
		fmt.Fprintf(&body, "#EXTINF:%.3f,\n", segment.Duration.Seconds())
		fmt.Fprintf(&body, "%s\n", filepath.Base(segment.Path))
	}
	body.WriteString("#EXT-X-ENDLIST\n")
	return body.String(), nil
}

// PlaylistPath writes Playlist(from) into the buffer directory and returns its
// path, for a player that opens local HLS files instead of a local endpoint.
func (b *Buffer) PlaylistPath(from time.Duration) (string, error) {
	list, err := b.Playlist(from)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("replay-%d.m3u8", int64(from.Seconds()))
	path := filepath.Join(b.opts.Dir, name)
	if err := os.WriteFile(path, []byte(list), 0o644); err != nil {
		return "", fmt.Errorf("%w: write playlist: %v", ErrNoSpace, err)
	}
	return path, nil
}

// rangeAtLocked returns the contiguous range that covers at, or the first one
// when at is zero and the buffer starts later. The caller holds the lock.
func (b *Buffer) rangeAtLocked(at time.Duration) (Range, error) {
	ranges := b.ranges()
	for _, current := range ranges {
		if current.Contains(at) || (at == 0 && len(ranges) > 0) {
			return current, nil
		}
	}
	for _, gap := range b.gaps {
		if at >= gap.Start && at <= gap.End {
			return Range{}, fmt.Errorf("%w: %s", ErrNoRange, gap.String())
		}
	}
	return Range{}, fmt.Errorf("%w: %s is outside %s", ErrNoRange, at.Round(time.Millisecond), b.describeLocked())
}

// describeLocked spells the buffered ranges for a refusal. The caller holds the
// lock.
func (b *Buffer) describeLocked() string {
	if len(b.segments) == 0 {
		return "an empty buffer"
	}
	parts := make([]string, 0, 2)
	for _, current := range b.ranges() {
		parts = append(parts, current.String())
	}
	return strings.Join(parts, ", ")
}

// Segments returns a copy of the stored segments in order.
func (b *Buffer) Segments() []Segment {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Segment(nil), b.segments...)
}

// Gaps returns a copy of the recorded gaps in order.
func (b *Buffer) Gaps() []Gap {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Gap(nil), b.gaps...)
}

// Bytes is how many segment bytes are stored.
func (b *Buffer) Bytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bytes
}

// Evict removes the oldest segments while the buffer is over its byte or
// retention limit. Protected segments are kept even when they are old enough to
// go, which is what keeps the part being played from disappearing underneath the
// player. It returns the sequences it removed.
func (b *Buffer) Evict(protect func(Segment) bool) []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	removed := make([]int, 0, len(b.segments))
	for len(b.segments) > 0 && b.over() {
		oldest := b.segments[0]
		if protect != nil && protect(oldest) {
			break
		}
		b.segments = b.segments[1:]
		b.bytes -= oldest.Bytes
		removed = append(removed, oldest.Sequence)
		_ = os.Remove(oldest.Path)
	}
	return removed
}

// over reports whether a limit is exceeded. The caller holds the lock.
func (b *Buffer) over() bool {
	if b.opts.MaxBytes > 0 && b.bytes > b.opts.MaxBytes {
		return true
	}
	if b.opts.Retention > 0 && len(b.segments) > 1 {
		span := b.segments[len(b.segments)-1].End - b.segments[0].Start
		return span > b.opts.Retention
	}
	return false
}

// Close removes the stored segments and, when the buffer created it, the
// directory. It is safe to call twice.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	var first error
	for _, segment := range b.segments {
		if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	b.segments = nil
	b.bytes = 0
	if b.dirOwned {
		if err := os.RemoveAll(b.opts.Dir); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return fmt.Errorf("close buffer: %v", first)
	}
	return nil
}

// checkSegmentURI keeps the download to plain HTTP(S) and refuses credentials in
// the address, the same rule the playlist reader applies.
func checkSegmentURI(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%w: empty segment address", core.ErrEmptyURI)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: segment address: %v", core.ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: segment address scheme %q", core.ErrInvalid, parsed.Scheme)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: segment address carries credentials", core.ErrCredentialsInURI)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: segment address has no host", core.ErrInvalid)
	}
	return nil
}

// fetchSegment downloads one segment within the size limit.
func fetchSegment(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", core.ErrInvalid, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch segment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch segment: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSegmentSize+1))
	if err != nil {
		return nil, fmt.Errorf("fetch segment: %v", err)
	}
	if len(body) > maxSegmentSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(body))
	}
	return body, nil
}

// validateTransportStream checks that the body is a whole number of MPEG-TS
// packets, each starting with the sync byte. A truncated download or an HTML
// error page fails here instead of being stored as if it were media.
func validateTransportStream(body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("%w: empty body", ErrNotPlayable)
	}
	if len(body)%tsPacket != 0 {
		return fmt.Errorf("%w: %d bytes is not a whole number of %d-byte packets", ErrNotPlayable, len(body), tsPacket)
	}
	for offset := 0; offset < len(body); offset += tsPacket {
		if body[offset] != syncByte {
			return fmt.Errorf("%w: packet at %d does not start with 0x47", ErrNotPlayable, offset)
		}
	}
	return nil
}

// SortSegments orders segments by media sequence, which is the order a playlist
// lists them in.
func SortSegments(segments []Segment) {
	sort.Slice(segments, func(i, j int) bool { return segments[i].Sequence < segments[j].Sequence })
}
