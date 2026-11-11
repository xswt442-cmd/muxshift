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
	"sync/atomic"
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
	// ErrNoSpace reports a buffer that has no room for what was asked: a segment
	// the disk refused, or a recording the directory or the recording budget
	// cannot hold.
	ErrNoSpace = errors.New("the buffer has no room for this")
	// ErrNoAnchor reports a recording that cannot be placed on a source's media
	// clock because no measured anchor ties the buffer clock to it.
	ErrNoAnchor = errors.New("buffer clock has no anchor for this source")
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
	// epoch is the media-clock epoch this segment belongs to, taken when it was
	// stored. It is older than the buffer's epoch as soon as the playlist
	// declares a discontinuity, which is why a recording has to inherit the
	// epoch of the range it was cut from rather than whatever the buffer says
	// now.
	epoch uint64
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
	// Epoch is the media-clock epoch the whole range belongs to. A range is cut
	// where the clock was rebuilt, so every segment in it shares this epoch.
	Epoch uint64
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
	// RecordingsDir is where published recordings go. Empty means Dir, which is
	// what a standalone buffer wants; a session passes a directory of its own so
	// a recording is not mixed in with the rolling segments.
	RecordingsDir string
	// Retention is how much media time to keep. Zero keeps everything the
	// byte limit allows.
	Retention time.Duration
	// MaxBytes is how many bytes of segments to keep. Zero means the byte limit
	// is not enforced.
	MaxBytes int64
	// Source names the source whose media clock the buffer's times belong to.
	// It is copied into every Recording so a saved file can be attributed
	// without a second lookup; an empty value is allowed for a bare buffer.
	Source core.SourceID
	// SourceEpoch names the run of the source's media clock this buffer
	// records. It is copied into every Recording and every Anchor is checked
	// against it, so a recording made before a discontinuity cannot be mapped
	// with an anchor measured after it. Zero means the buffer starts the first
	// epoch itself; a caller that reopens a source increments it.
	SourceEpoch uint64
	// RecordingBytes bounds the disk the published recordings may take. Zero
	// leaves them unbounded, which is a deliberate risk the caller takes: with no
	// budget nothing but Close reclaims a published file. When a new cut does not
	// fit, the oldest recordings nobody holds are removed first; if it still does
	// not fit, the cut is refused instead of deleting a file a player is reading.
	RecordingBytes int64
}

// recorded is one published recording and how many players hold it.
type recorded struct {
	Recording
	refs int
	gone bool
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
	// recordingsOwned says this buffer created RecordingsDir, so only then does
	// Close remove it. A shared recordings directory belongs to the session.
	recordingsOwned bool
	// cuts counts recordings so two cuts in the same nanosecond still get
	// different names; recordings lists the published files, which Close keeps
	// while a lease holds them.
	// cuts counts recordings so two cuts in the same nanosecond still get
	// different names; recordings lists the published files, which Close keeps
	// while a lease holds them.
	cuts           int
	recordings     []recorded
	recordingBytes int64
	pendingCleanup []string
	lastRemoveErr  error
	// instance names this buffer and never changes, so an anchor measured on
	// another buffer instance is refused even when the source identity and the
	// epoch match: two buffers for one source keep different clocks.
	instance string
	// epoch is the run of the source's media clock the buffer is storing. A
	// discontinuity in the playlist moves it forward, which makes every earlier
	// recording refuse an anchor measured after the jump.
	epoch uint64
}

// New creates the buffer's directory and returns it empty. The directory is
// removed by Close when the buffer created it, so a session that exits leaves no
// cache behind.
func New(opts Options) (*Buffer, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return nil, fmt.Errorf("%w: buffer needs a directory", core.ErrInvalid)
	}
	if opts.Retention < 0 || opts.MaxBytes < 0 || opts.RecordingBytes < 0 {
		return nil, fmt.Errorf("%w: retention, size and recording limits cannot be negative", core.ErrInvalid)
	}
	if strings.TrimSpace(opts.RecordingsDir) == "" {
		opts.RecordingsDir = opts.Dir
	}
	owned := false
	if _, err := os.Stat(opts.Dir); errors.Is(err, os.ErrNotExist) {
		owned = true
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSpace, err)
	}
	recordingsOwned := false
	if opts.RecordingsDir != opts.Dir {
		if _, err := os.Stat(opts.RecordingsDir); errors.Is(err, os.ErrNotExist) {
			recordingsOwned = true
		}
		if err := os.MkdirAll(opts.RecordingsDir, 0o755); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNoSpace, err)
		}
	}
	epoch := opts.SourceEpoch
	if epoch == 0 {
		epoch = 1
	}
	return &Buffer{
		opts:            opts,
		dirOwned:        owned,
		recordingsOwned: recordingsOwned,
		instance:        newInstanceID(),
		epoch:           epoch,
	}, nil
}

// instanceSeq keeps two buffers created in the same nanosecond apart.
var instanceSeq atomic.Uint64

// newInstanceID names one buffer. The process-wide counter is what makes the
// name unique; the creation time only helps when logs from different runs are
// compared side by side.
func newInstanceID() string {
	return fmt.Sprintf("buf-%d-%d", time.Now().UnixNano(), instanceSeq.Add(1))
}

// Instance names this buffer. It is unique for the life of the process and is
// fixed when the buffer is created.
func (b *Buffer) Instance() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.instance
}

// Epoch is the run of the source's media clock the buffer is currently storing.
// It moves forward when the playlist states a discontinuity.
func (b *Buffer) Epoch() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.epoch
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
		// previous one, so the two cannot share a replayable range. The media
		// clock itself was rebuilt, which also ends the epoch: an anchor
		// measured before this segment cannot describe a time after it.
		split = true
		b.epoch++
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
		epoch:              b.epoch,
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
	current := Range{Start: b.segments[0].Start, Epoch: b.segments[0].epoch}
	for _, segment := range b.segments {
		if len(current.Segments) > 0 && (segment.split || segment.Start != current.End) {
			out = append(out, current)
			current = Range{Start: segment.Start, Epoch: segment.epoch}
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

// ClockPoint is the oldest stored segment together with the clock identity it
// belongs to. Measuring a segment and naming its epoch are two readings of the
// same state, so a caller that wants both has to take them under one lock: a
// discontinuity between the two reads would pair a segment with an epoch that
// arrived after it.
type ClockPoint struct {
	Segment Segment
	// Epoch is the media-clock epoch the segment belongs to.
	Epoch uint64
	// BufferInstance is the buffer that stored the segment.
	BufferInstance string
}

// OldestSegment returns the oldest stored segment and the clock it belongs to.
func (b *Buffer) OldestSegment() (ClockPoint, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, segment := range b.segments {
		return ClockPoint{Segment: segment, Epoch: segment.epoch, BufferInstance: b.instance}, nil
	}
	return ClockPoint{}, fmt.Errorf("%w: the buffer holds no segment", ErrNoRange)
}

// CurrentSegment returns the first segment of the range the buffer is filling
// now, with the clock it belongs to. That is the point an anchor has to be
// measured at to describe the clock the buffer is on: after a discontinuity, the
// oldest segment belongs to the clock that was replaced.
func (b *Buffer) CurrentSegment() (ClockPoint, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ranges := b.ranges()
	if len(ranges) == 0 || len(ranges[len(ranges)-1].Segments) == 0 {
		return ClockPoint{}, fmt.Errorf("%w: the buffer holds no segment", ErrNoRange)
	}
	current := ranges[len(ranges)-1]
	segment := current.Segments[0]
	return ClockPoint{Segment: segment, Epoch: current.Epoch, BufferInstance: b.instance}, nil
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

// Join writes the continuous range that holds from into a new transport stream
// file and returns its path. It is Record without the timing metadata; a caller
// that has to place the file on the buffer clock, or on shared event time, uses
// Record instead.
//
// Each call publishes a new file: a published recording is never overwritten, so
// a player that has one open keeps reading the bytes it was given. Close removes
// every recording the buffer published.
func (b *Buffer) Join(from time.Duration) (string, error) {
	recording, err := b.Record(from)
	if err != nil {
		return "", err
	}
	return recording.Path, nil
}

// Record writes the continuous range that holds from into a new transport stream
// file and reports what the file contains and where it sits on the buffer's
// clock.
//
// A start inside a segment begins at that segment's first frame, because a
// transport stream segment cannot be cut, so the file's first frame is at the
// chosen segment's start and not at from. Recording carries both times rather
// than letting a caller mistake one for the other.
//
// Every recording gets its own name: two requests in the same second do not
// collide, and a published file is never overwritten. The bytes go to a
// temporary name first and are renamed only after they are all written and
// closed, so a failed cut leaves no half file and cannot damage an earlier one.
func (b *Buffer) Record(from time.Duration) (Recording, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	recording, _, err := b.recordLocked(from)
	return recording, err
}

// RecordAndLease writes the range that holds from and returns a lease on it.
//
// Publishing and leasing happen under one lock, so no other cut can drop the
// file between the moment it appears and the moment a player holds it: a caller
// that used Record and then Lease would leave a window in which a second cut
// under the recording budget could remove what the first one just published.
// The engine must open the file from this lease, not from a path it looked up
// separately.
func (b *Buffer) RecordAndLease(from time.Duration) (Recording, *Lease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	recording, index, err := b.recordLocked(from)
	if err != nil {
		return Recording{}, nil, err
	}
	b.recordings[index].refs++
	return recording, &Lease{buffer: b, id: recording.ID, recording: recording}, nil
}

// recordLocked is Record without the lock. It returns the index of the published
// recording so a caller can lease it before another cut runs.
func (b *Buffer) recordLocked(from time.Duration) (Recording, int, error) {
	if b.closed {
		return Recording{}, -1, core.ErrClosed
	}
	chosen, err := b.rangeAtLocked(from)
	if err != nil {
		return Recording{}, -1, err
	}
	// The cut needs room under the recording budget. Unleased older recordings
	// are dropped first; a recording a player holds is never dropped, so a cut
	// that cannot fit beside them is refused instead.
	need := int64(0)
	for _, segment := range chosen.Segments {
		if segment.End > from {
			need += segment.Bytes
		}
	}
	if b.opts.RecordingBytes > 0 && need > b.opts.RecordingBytes {
		return Recording{}, -1, fmt.Errorf("%w: one recording needs %d bytes, the budget is %d",
			ErrNoSpace, need, b.opts.RecordingBytes)
	}
	for b.opts.RecordingBytes > 0 && b.recordingBytes+need > b.opts.RecordingBytes {
		if !b.dropOldestRecordingLocked() {
			return Recording{}, -1, fmt.Errorf("%w: the recording budget of %d bytes is held by files a player is using or files the system will not let the buffer remove",
				ErrNoSpace, b.opts.RecordingBytes)
		}
	}
	b.cuts++
	id := fmt.Sprintf("rec-%d-%d", time.Now().UnixNano(), b.cuts)
	name := id + ".ts"
	path := filepath.Join(b.opts.RecordingsDir, name)
	temporary := filepath.Join(b.opts.RecordingsDir, "."+id+".part")
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Recording{}, -1, fmt.Errorf("%w: create %s: %v", ErrNoSpace, name, err)
	}
	// A failed cut removes its own temporary file; nothing else is touched.
	failed := func(err error) (Recording, int, error) {
		out.Close()
		os.Remove(temporary)
		return Recording{}, -1, err
	}
	recording := Recording{
		ID:                  id,
		Path:                path,
		Source:              b.opts.Source,
		SourceMediaEpoch:    chosen.Epoch,
		BufferInstance:      b.instance,
		RequestedBufferTime: from,
		JoinedAt:            time.Now(),
		Ranges:              b.ranges(),
		Gaps:                append([]Gap(nil), b.gaps...),
	}
	var written int64
	first := true
	for _, segment := range chosen.Segments {
		if segment.End <= from {
			continue
		}
		data, err := os.ReadFile(segment.Path)
		if err != nil {
			return failed(fmt.Errorf("%w: read %s: %v", ErrNotPlayable, filepath.Base(segment.Path), err))
		}
		if _, err := out.Write(data); err != nil {
			return failed(fmt.Errorf("%w: write %s: %v", ErrNoSpace, name, err))
		}
		written += int64(len(data))
		if first {
			// The file begins at this segment's first frame, which is why the
			// requested time alone cannot be used as the file's start.
			recording.StartBufferTime = segment.Start
			recording.FirstSequence = segment.Sequence
			first = false
		}
		recording.EndBufferTime = segment.End
		recording.LastSequence = segment.Sequence
	}
	if err := out.Close(); err != nil {
		os.Remove(temporary)
		return Recording{}, -1, fmt.Errorf("%w: close %s: %v", ErrNoSpace, name, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return Recording{}, -1, fmt.Errorf("%w: publish %s: %v", ErrNoSpace, name, err)
	}
	recording.Size = written
	b.recordings = append(b.recordings, recorded{Recording: recording})
	b.recordingBytes += written
	return recording, len(b.recordings) - 1, nil
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
// directory. A recording a player still holds stays on disk until the last lease
// is released, so closing a session cannot pull a file out from under a player;
// the directory is left in place while such a file remains. It is safe to call
// twice.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	var first error
	// Recordings are immutable and never overwritten. Each one is removed by
	// name, and a leased one is kept: its lease removes it on release.
	for i := range b.recordings {
		if b.recordings[i].refs > 0 || b.recordings[i].gone {
			continue
		}
		b.removeRecordingLocked(i)
		if b.lastRemoveErr != nil && first == nil {
			first = b.lastRemoveErr
		}
	}
	for _, segment := range b.segments {
		if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	b.segments = nil
	b.bytes = 0
	if b.dirOwned && b.emptyLocked() {
		if err := os.RemoveAll(b.opts.Dir); err != nil && first == nil {
			first = err
		}
	}
	// The recordings directory is only the buffer's to remove when the buffer
	// created it and nothing it published is still leased. A session's shared
	// recordings directory is left for the session to clear.
	if b.recordingsOwned && b.emptyLocked() {
		if err := os.RemoveAll(b.opts.RecordingsDir); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return fmt.Errorf("close buffer: %v", first)
	}
	return nil
}

// emptyLocked reports whether no file the buffer owns is still on disk, so only
// then may its directory be removed. The caller holds the lock.
func (b *Buffer) emptyLocked() bool {
	if len(b.pendingCleanup) > 0 {
		return false
	}
	for _, rec := range b.recordings {
		if !rec.gone {
			return false
		}
	}
	return true
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
