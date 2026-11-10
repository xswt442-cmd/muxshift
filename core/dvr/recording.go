package dvr

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// Recording is one immutable joined file and the map from a position inside it
// back to the buffer's clock.
//
// A caller asks for a time and gets a file whose first frame is not necessarily
// that time: a transport stream segment cannot be cut, so the file begins at the
// segment that holds the request. The times below make that difference explicit:
//
//	BufferTime = RecordingPosition + StartBufferTime
//
// where RecordingPosition is the position in the file (zero at its first frame).
// RequestedBufferTime is only what was asked for; StartBufferTime is where the
// file really begins.
//
// The buffer's clock is its own: it starts at the first segment the buffer
// stored, so it is not the source's media clock. Reaching the source's clock and
// then shared event time needs an Anchor that was measured for this source, and
// a track offset whose sign follows the Core timeline (event = media - offset).
type Recording struct {
	// ID names this recording and never changes: two requests in the same
	// second get different files, and a published file is never overwritten.
	ID string
	// Path is the joined file. It stays in the buffer's directory and is
	// removed when the buffer is closed.
	Path string
	// Source is the source the buffer was opened for, when one was named.
	Source core.SourceID
	// RequestedBufferTime is the time the caller asked to start from.
	RequestedBufferTime time.Duration
	// StartBufferTime is the buffer time of the file's first frame: the start
	// of the segment that holds the request.
	StartBufferTime time.Duration
	// EndBufferTime is the buffer time where the file's last segment ends.
	EndBufferTime time.Duration
	// FirstSequence and LastSequence are the media sequence numbers in the
	// file, for matching a recording against a playlist.
	FirstSequence int
	LastSequence  int
	// Ranges and Gaps describe what the buffer held when the file was written.
	Ranges []Range
	Gaps   []Gap
	// JoinedAt is when the file was published.
	JoinedAt time.Time
	// Size is the file size at that moment, used by Valid to notice a file
	// that was truncated or removed from outside.
	Size int64
}

// Anchor ties the buffer's clock to a source's media clock. It is measured for
// one source instance; after a reopen or a seek past a discontinuity the answer
// changes, so the caller must confirm the anchor still describes the source it
// is mapping.
type Anchor struct {
	// Source is the source the anchor was measured on.
	Source core.SourceID
	// BufferStart is the buffer time the anchor was measured at.
	BufferStart time.Duration
	// MediaStart is the source media time observed at BufferStart.
	MediaStart time.Duration
}

// Duration is how much media the file holds.
func (r Recording) Duration() time.Duration {
	if r.EndBufferTime <= r.StartBufferTime {
		return 0
	}
	return r.EndBufferTime - r.StartBufferTime
}

// BufferTime maps a position inside the file to the buffer's clock. This
// relation is exact: the file is the segment run itself.
func (r Recording) BufferTime(position time.Duration) (time.Duration, error) {
	if position < 0 || position > r.Duration() {
		return 0, fmt.Errorf("%w: position %s in %s is outside a recording of %s",
			ErrNoRange, position.Round(time.Millisecond), r.name(), r.Duration().Round(time.Millisecond))
	}
	return r.StartBufferTime + position, nil
}

// Position maps a buffer time back to a position inside the file.
func (r Recording) Position(at time.Duration) (time.Duration, error) {
	if at < r.StartBufferTime || at > r.EndBufferTime {
		return 0, fmt.Errorf("%w: %s in %s is outside a recording of %s..%s",
			ErrNoRange, at.Round(time.Millisecond), r.name(),
			r.StartBufferTime.Round(time.Millisecond), r.EndBufferTime.Round(time.Millisecond))
	}
	return at - r.StartBufferTime, nil
}

// SourceMediaTime maps a position inside the file to the source's media clock.
// It refuses without a measured anchor for this very source: assuming the
// buffer starts at zero would silently shift a live buffer that began mid-stream.
func (r Recording) SourceMediaTime(position time.Duration, anchor Anchor) (time.Duration, error) {
	at, err := r.BufferTime(position)
	if err != nil {
		return 0, err
	}
	if anchor.Source == "" || r.Source == "" {
		return 0, fmt.Errorf("%w: %s has no anchor that ties its buffer clock to a source",
			ErrNoAnchor, r.name())
	}
	if anchor.Source != r.Source {
		return 0, fmt.Errorf("%w: the anchor belongs to %s, not %s", ErrNoAnchor, anchor.Source, r.Source)
	}
	return anchor.MediaStart + (at - anchor.BufferStart), nil
}

// EventTime maps a position inside the file to shared event time. The Core
// timeline fixes media = event + offset, so this subtracts the confirmed track
// offset instead of inventing the other sign.
func (r Recording) EventTime(position time.Duration, anchor Anchor, offset time.Duration) (time.Duration, error) {
	at, err := r.SourceMediaTime(position, anchor)
	if err != nil {
		return 0, err
	}
	return at - offset, nil
}

// Valid reports whether the file still matches the recording. It detects a file
// that was removed or truncated from outside; it never licenses overwriting one.
func (r Recording) Valid() error {
	if r.Path == "" {
		return fmt.Errorf("%w: the recording has no file", ErrNoRange)
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		return fmt.Errorf("%w: %s is gone: %v", ErrNoRange, r.name(), err)
	}
	if info.Size() != r.Size {
		return fmt.Errorf("%w: %s changed on disk: %d bytes, %d when written",
			ErrNoRange, r.name(), info.Size(), r.Size)
	}
	return nil
}

// FileName is the recording's file name. Logs use it so a line never repeats a
// directory that the surrounding message already names.
func (r Recording) FileName() string {
	if r.Path == "" {
		return "the recording"
	}
	return filepath.Base(r.Path)
}

// String returns the spelling used in diagnostics and logs.
func (r Recording) String() string {
	source := string(r.Source)
	if source == "" {
		source = "source"
	}
	return fmt.Sprintf("recording %s %s · asked %s · file starts %s · covers %s..%s",
		source, r.FileName(),
		r.RequestedBufferTime.Round(time.Millisecond),
		r.StartBufferTime.Round(time.Millisecond),
		r.StartBufferTime.Round(time.Millisecond), r.EndBufferTime.Round(time.Millisecond))
}

// name is what messages call the file.
func (r Recording) name() string {
	return r.FileName()
}
