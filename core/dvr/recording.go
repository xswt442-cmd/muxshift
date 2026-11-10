package dvr

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// Recording is one joined file and the map from a position inside it back to
// the source's own media clock.
//
// A caller asks for a media time and gets a file whose first frame is not
// necessarily that time: a transport stream segment cannot be cut, so the file
// begins at the segment that holds the request. The three times below make that
// difference explicit instead of leaving the caller to guess.
//
//	SourceMediaTime = RecordingPosition + ContentStart
//
// where RecordingPosition is the position in the file (zero at its first frame).
// RequestedMediaTime is only what was asked for; SegmentStartTime is where that
// request's segment begins, and ContentStart is where the file really begins.
// They are equal when the request falls on a segment boundary or inside the
// buffered window, and they differ when the request is before the window.
type Recording struct {
	// Path is the joined file. It belongs to the buffer: a later Record
	// replaces it and Close removes it.
	Path string
	// Source is the source the buffer was opened for, when one was named.
	Source core.SourceID
	// RequestedMediaTime is the media time the caller asked to start from.
	RequestedMediaTime time.Duration
	// SegmentStartTime is the media time where the requested segment starts.
	SegmentStartTime time.Duration
	// ContentStart is the media time of the file's first frame.
	ContentStart time.Duration
	// ContentEnd is the media time where the file's last segment ends, so the
	// file covers ContentStart until ContentEnd.
	ContentEnd time.Duration
	// FirstSequence and LastSequence are the media sequence numbers in the
	// file, for matching a recording against a playlist.
	FirstSequence int
	LastSequence  int
	// Ranges and Gaps describe what the buffer held when the file was written.
	Ranges []Range
	Gaps   []Gap
	// JoinedAt is when the file was written.
	JoinedAt time.Time
	// Size is the file size at that moment, used by Valid to detect that the
	// file was replaced or removed.
	Size int64
}

// Duration is how much media the file holds.
func (r Recording) Duration() time.Duration {
	if r.ContentEnd <= r.ContentStart {
		return 0
	}
	return r.ContentEnd - r.ContentStart
}

// SourceMediaTime maps a position inside the file to the source's media clock.
// The position is zero at the file's first frame and is bounded by Duration.
func (r Recording) SourceMediaTime(position time.Duration) (time.Duration, error) {
	if position < 0 || position > r.Duration() {
		return 0, fmt.Errorf("%w: position %s in %s is outside a recording of %s",
			ErrNoRange, position.Round(time.Millisecond), r.name(), r.Duration().Round(time.Millisecond))
	}
	return r.ContentStart + position, nil
}

// Position maps a media time back to a position inside the file.
func (r Recording) Position(at time.Duration) (time.Duration, error) {
	if at < r.ContentStart || at > r.ContentEnd {
		return 0, fmt.Errorf("%w: %s in %s is outside a recording of %s..%s",
			ErrNoRange, at.Round(time.Millisecond), r.name(),
			r.ContentStart.Round(time.Millisecond), r.ContentEnd.Round(time.Millisecond))
	}
	return at - r.ContentStart, nil
}

// EventTime maps a position inside the file to the shared event clock. The
// offset is the source's confirmed track offset, so the caller passes the value
// it already applied to live playback rather than a second guess.
func (r Recording) EventTime(position, offset time.Duration) (time.Duration, error) {
	at, err := r.SourceMediaTime(position)
	if err != nil {
		return 0, err
	}
	return at + offset, nil
}

// Valid reports whether the file still matches the recording. A buffer keeps one
// recording at a time and removes it on Close, so a stale Recording is a real
// case and not a defensive check.
func (r Recording) Valid() error {
	if r.Path == "" {
		return fmt.Errorf("%w: the recording has no file", ErrNoRange)
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		return fmt.Errorf("%w: %s is gone: %v", ErrNoRange, r.name(), err)
	}
	if info.Size() != r.Size {
		return fmt.Errorf("%w: %s was replaced: %d bytes on disk, %d bytes when written",
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
	return fmt.Sprintf("recording %s %s · asked %s · segment starts %s · file %s..%s",
		source, r.FileName(),
		r.RequestedMediaTime.Round(time.Millisecond),
		r.SegmentStartTime.Round(time.Millisecond),
		r.ContentStart.Round(time.Millisecond), r.ContentEnd.Round(time.Millisecond))
}

// name is what messages call the file.
func (r Recording) name() string {
	return r.FileName()
}
