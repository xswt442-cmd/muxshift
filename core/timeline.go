package core

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Alignment says whether a track's media time is known to correspond to the
// shared event timeline.
type Alignment int

// Alignment states a track can be in on the shared event timeline.
const (
	// AlignmentUnknown means no anchor ties this track's media time to the
	// shared event timeline. Callers must not guess: the track is reported as
	// not aligned until a shift is set.
	AlignmentUnknown Alignment = iota
	// AlignmentKnown means a shift maps this track's media time onto the
	// shared event timeline.
	AlignmentKnown
)

// String returns the alignment's name.
func (a Alignment) String() string {
	switch a {
	case AlignmentKnown:
		return "known"
	case AlignmentUnknown:
		return "not_aligned"
	}
	return fmt.Sprintf("Alignment(%d)", int(a))
}

// Shift is one track's placement on the shared event timeline. For a shared
// event time t, the track's media time is t + Offset:
//
//	targetMediaTime = sharedEventTime + mappedOffset
//
// A positive Offset means the track's media clock runs ahead of the shared
// timeline by that much, so following it live means holding it back by that
// much. A negative Offset means the track is behind the shared timeline: at
// the current event time its media has not reached that content yet.
type Shift struct {
	Track  TrackID
	Offset time.Duration
}

// Timeline maps the shared event time of a Session to each track's media time.
// It holds no decoder and no player: a media layer asks it what to present and
// reports back what it actually has.
//
// A track without a Shift is not aligned. Setting a Shift replaces the
// previous one, so applying the same calibration twice does not accumulate:
// the mapping depends only on the offset currently stored.
type Timeline struct {
	shifts map[TrackID]time.Duration
}

// NewTimeline starts an empty Timeline: every track is not aligned yet.
func NewTimeline() *Timeline {
	return &Timeline{shifts: make(map[TrackID]time.Duration)}
}

// SetShift places a track on the shared event timeline. For a track already
// placed it replaces the offset, so repeating a calibration is harmless.
func (t *Timeline) SetShift(track TrackID, offset time.Duration) error {
	if track == "" {
		return fmt.Errorf("set shift: %w", ErrInvalid)
	}
	if t.shifts == nil {
		t.shifts = make(map[TrackID]time.Duration)
	}
	t.shifts[track] = offset
	return nil
}

// AdjustShift moves a track's offset by delta and returns the new placement.
//
// This is how a manual nudge is applied: the offset moves from what is stored,
// so pressing a button three times moves the track by three times delta from
// where it was mapped, not from where its player happened to be. A track that
// was never aligned is placed at delta. The sum is checked like any other
// offset, so a nudge past the range time.Duration can hold fails with
// ErrTimeRange instead of wrapping around.
func (t *Timeline) AdjustShift(track TrackID, delta time.Duration) (Shift, error) {
	if track == "" {
		return Shift{}, fmt.Errorf("adjust shift: %w", ErrInvalid)
	}
	current := t.shifts[track]
	next, err := addDuration(current, delta)
	if err != nil {
		return Shift{}, fmt.Errorf("adjust shift: %w", err)
	}
	if err := t.SetShift(track, next); err != nil {
		return Shift{}, err
	}
	return Shift{Track: track, Offset: next}, nil
}

// Shift returns a track's placement. It reports false when the track is not
// aligned.
func (t *Timeline) Shift(track TrackID) (Shift, bool) {
	offset, ok := t.shifts[track]
	return Shift{Track: track, Offset: offset}, ok
}

// Aligned reports whether a track has a shift.
func (t *Timeline) Aligned(track TrackID) bool {
	_, ok := t.shifts[track]
	return ok
}

// Forget drops a track's shift, for example after a buffering gap or a
// reconnection makes the old anchor unreliable. The track becomes not aligned
// instead of keeping a stale guess.
func (t *Timeline) Forget(track TrackID) {
	delete(t.shifts, track)
}

// Shifts returns every placement, ordered by track identifier.
func (t *Timeline) Shifts() []Shift {
	out := make([]Shift, 0, len(t.shifts))
	for track, offset := range t.shifts {
		out = append(out, Shift{Track: track, Offset: offset})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Track < out[j].Track })
	return out
}

// MediaTime returns the media time a track must present at a shared event
// time. It fails with ErrNotAligned when the track has no shift, and with
// ErrTimeRange when the sum leaves the range time.Duration can hold.
func (t *Timeline) MediaTime(track TrackID, event time.Duration) (time.Duration, error) {
	offset, ok := t.shifts[track]
	if !ok {
		return 0, fmt.Errorf("media time of %s at event %s: %w", track, event, ErrNotAligned)
	}
	media, err := addDuration(event, offset)
	if err != nil {
		return 0, fmt.Errorf("media time of %s at event %s: %w", track, event, err)
	}
	return media, nil
}

// EventTime returns the shared event time a track's media time belongs to. It
// is the inverse of MediaTime.
func (t *Timeline) EventTime(track TrackID, media time.Duration) (time.Duration, error) {
	offset, ok := t.shifts[track]
	if !ok {
		return 0, fmt.Errorf("event time of %s at media %s: %w", track, media, ErrNotAligned)
	}
	event, err := subDuration(media, offset)
	if err != nil {
		return 0, fmt.Errorf("event time of %s at media %s: %w", track, media, err)
	}
	return event, nil
}

// TrackPlan is what a media layer should do with one track at one shared
// event time.
type TrackPlan struct {
	Track     TrackID
	Alignment Alignment
	// Target is the media time to present. It is meaningful only when
	// Alignment is AlignmentKnown. A negative target means the track has not
	// reached that content yet.
	Target time.Duration
	// Delay is how long to hold the track back when following it live, so its
	// live edge matches the shared timeline. It is zero for a track that is
	// level with or behind the timeline.
	Delay time.Duration
}

// Plan reports what each track should present at a shared event time, in the
// order the tracks were given. A track without a shift is reported as not
// aligned rather than placed at a guessed time.
func (t *Timeline) Plan(event time.Duration, tracks ...TrackID) []TrackPlan {
	out := make([]TrackPlan, len(tracks))
	for i, track := range tracks {
		out[i].Track = track
		offset, ok := t.shifts[track]
		if !ok {
			out[i].Alignment = AlignmentUnknown
			continue
		}
		out[i].Alignment = AlignmentKnown
		if media, err := addDuration(event, offset); err == nil {
			out[i].Target = media
		}
		if offset > 0 {
			out[i].Delay = offset
		}
	}
	return out
}

// Playhead is the shared event clock of a Session: which event time is
// playing, and whether playback advances it. It replaces no media clock; the
// media layer reports positions and buffering, and pauses or seeks when Core
// says so.
//
// The clock starts paused at event time zero. Playing advances it; seeking
// moves it and keeps the paused or playing state. A seek or an advance never
// changes a track's shift, so repeated seeks and calibrations do not
// accumulate offset.
type Playhead struct {
	event   time.Duration
	playing bool
}

// NewPlayhead starts a paused clock at event time zero.
func NewPlayhead() *Playhead {
	return &Playhead{}
}

// Play starts advancing the clock. Calling it while already playing does
// nothing.
func (p *Playhead) Play() { p.playing = true }

// Pause stops advancing the clock. Calling it while already paused does
// nothing.
func (p *Playhead) Pause() { p.playing = false }

// Playing reports whether the clock is advancing.
func (p *Playhead) Playing() bool { return p.playing }

// Event returns the current shared event time.
func (p *Playhead) Event() time.Duration { return p.event }

// Advance moves the clock by one elapsed slice. It ignores the slice while
// paused, and fails with ErrTimeRange for a negative slice or a sum outside
// the range time.Duration can hold.
func (p *Playhead) Advance(elapsed time.Duration) error {
	if elapsed < 0 {
		return fmt.Errorf("advance %s: %w", elapsed, ErrTimeRange)
	}
	if !p.playing {
		return nil
	}
	next, err := addDuration(p.event, elapsed)
	if err != nil {
		return fmt.Errorf("advance %s from %s: %w", elapsed, p.event, err)
	}
	p.event = next
	return nil
}

// Seek moves the clock to a shared event time and keeps the current playing
// state. It fails with ErrTimeRange for a negative event time.
func (p *Playhead) Seek(event time.Duration) error {
	if event < 0 {
		return fmt.Errorf("seek to %s: %w", event, ErrTimeRange)
	}
	p.event = event
	return nil
}

// addDuration adds two durations, reporting when the sum does not fit.
func addDuration(a, b time.Duration) (time.Duration, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrTimeRange
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, ErrTimeRange
	}
	return a + b, nil
}

// subDuration subtracts b from a, reporting when the difference does not fit.
func subDuration(a, b time.Duration) (time.Duration, error) {
	if b < 0 && a > math.MaxInt64+b {
		return 0, ErrTimeRange
	}
	if b > 0 && a < math.MinInt64+b {
		return 0, ErrTimeRange
	}
	return a - b, nil
}
