package core

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

// TestShiftsPlaceTracksOnOneTimeline checks the sign convention: at a shared
// event time, a track's media time is the event time plus its offset, so a
// positive offset marks a source whose media clock is ahead.
func TestShiftsPlaceTracksOnOneTimeline(t *testing.T) {
	line := NewTimeline()
	cases := []struct {
		name   string
		track  TrackID
		offset time.Duration
		event  time.Duration
		media  time.Duration
		delay  time.Duration
	}{
		{"level", "track-level", 0, 10 * time.Second, 10 * time.Second, 0},
		{"ahead", "track-ahead", 3200 * time.Millisecond, 10 * time.Second, 13*time.Second + 200*time.Millisecond, 3200 * time.Millisecond},
		{"behind", "track-behind", -2500 * time.Millisecond, 10 * time.Second, 7500 * time.Millisecond, 0},
		{"ahead at origin", "track-ahead", 3200 * time.Millisecond, 0, 3200 * time.Millisecond, 3200 * time.Millisecond},
		{"behind at origin", "track-behind", -2500 * time.Millisecond, 0, -2500 * time.Millisecond, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := line.SetShift(tc.track, tc.offset); err != nil {
				t.Fatalf("set shift: %v", err)
			}
			shift, ok := line.Shift(tc.track)
			if !ok {
				t.Fatalf("shift of %s missing", tc.track)
			}
			if shift.Offset != tc.offset || shift.Track != tc.track {
				t.Fatalf("shift = %+v, want {%s %s}", shift, tc.track, tc.offset)
			}
			media, err := line.MediaTime(tc.track, tc.event)
			if err != nil {
				t.Fatalf("media time: %v", err)
			}
			if media != tc.media {
				t.Fatalf("media time at %s = %s, want %s", tc.event, media, tc.media)
			}
			event, err := line.EventTime(tc.track, tc.media)
			if err != nil {
				t.Fatalf("event time: %v", err)
			}
			if event != tc.event {
				t.Fatalf("event time of %s = %s, want %s", tc.media, event, tc.event)
			}
			plans := line.Plan(tc.event, tc.track)
			if len(plans) != 1 {
				t.Fatalf("plans = %d, want 1", len(plans))
			}
			plan := plans[0]
			if plan.Alignment != AlignmentKnown {
				t.Fatalf("alignment = %s, want known", plan.Alignment)
			}
			if plan.Target != tc.media {
				t.Fatalf("target = %s, want %s", plan.Target, tc.media)
			}
			if plan.Delay != tc.delay {
				t.Fatalf("delay = %s, want %s", plan.Delay, tc.delay)
			}
		})
	}
}

// TestRepeatedCalibrationDoesNotAccumulate applies the same shift twice, then
// recalibrates after a seek, and expects the mapping to depend only on the
// stored offset.
func TestRepeatedCalibrationDoesNotAccumulate(t *testing.T) {
	line := NewTimeline()
	track := TrackID("track-1")
	for i := 0; i < 2; i++ {
		if err := line.SetShift(track, 3200*time.Millisecond); err != nil {
			t.Fatalf("set shift %d: %v", i, err)
		}
	}
	if shifts := line.Shifts(); len(shifts) != 1 {
		t.Fatalf("shifts = %+v, want one", shifts)
	}

	playhead := NewPlayhead()
	playhead.Play()
	if err := playhead.Advance(10 * time.Second); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := playhead.Seek(15 * time.Second); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if err := line.SetShift(track, 3200*time.Millisecond); err != nil {
		t.Fatalf("recalibrate: %v", err)
	}

	media, err := line.MediaTime(track, playhead.Event())
	if err != nil {
		t.Fatalf("media time: %v", err)
	}
	if want := 18*time.Second + 200*time.Millisecond; media != want {
		t.Fatalf("media time after recalibration = %s, want %s", media, want)
	}

	if err := line.SetShift(track, 2500*time.Millisecond); err != nil {
		t.Fatalf("replace shift: %v", err)
	}
	media, err = line.MediaTime(track, playhead.Event())
	if err != nil {
		t.Fatalf("media time: %v", err)
	}
	if want := 17*time.Second + 500*time.Millisecond; media != want {
		t.Fatalf("media time after replacement = %s, want %s", media, want)
	}
}

// TestUnknownTrackIsNotAligned leaves a track without a shift, then forgets
// one after a gap, and expects an explicit not-aligned result instead of a
// guessed time.
func TestUnknownTrackIsNotAligned(t *testing.T) {
	line := NewTimeline()
	aligned := TrackID("track-1")
	unknown := TrackID("track-2")
	if err := line.SetShift(aligned, 3*time.Second); err != nil {
		t.Fatalf("set shift: %v", err)
	}

	if _, err := line.MediaTime(unknown, 0); !errors.Is(err, ErrNotAligned) {
		t.Fatalf("media time of unknown track = %v, want ErrNotAligned", err)
	}
	if _, err := line.EventTime(unknown, 0); !errors.Is(err, ErrNotAligned) {
		t.Fatalf("event time of unknown track = %v, want ErrNotAligned", err)
	}
	plans := line.Plan(5*time.Second, aligned, unknown)
	if len(plans) != 2 {
		t.Fatalf("plans = %d, want 2", len(plans))
	}
	if plans[1].Track != unknown || plans[1].Alignment != AlignmentUnknown {
		t.Fatalf("unknown plan = %+v", plans[1])
	}
	if plans[1].Target != 0 || plans[1].Delay != 0 {
		t.Fatalf("unknown plan carries a guess: %+v", plans[1])
	}

	line.Forget(aligned)
	if line.Aligned(aligned) {
		t.Fatal("forgotten track still aligned")
	}
	if _, err := line.MediaTime(aligned, 0); !errors.Is(err, ErrNotAligned) {
		t.Fatalf("media time after forget = %v, want ErrNotAligned", err)
	}
	if shifts := line.Shifts(); len(shifts) != 0 {
		t.Fatalf("shifts after forget = %+v", shifts)
	}
}

// TestTimeRange guards the boundaries: a sum outside time.Duration is
// reported instead of wrapping around.
func TestTimeRange(t *testing.T) {
	line := NewTimeline()
	track := TrackID("track-1")
	if err := line.SetShift(track, time.Second); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if _, err := line.MediaTime(track, time.Duration(math.MaxInt64)); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("overflow error = %v, want ErrTimeRange", err)
	}
	if err := line.SetShift(track, -time.Second); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if _, err := line.MediaTime(track, time.Duration(math.MinInt64)); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("underflow error = %v, want ErrTimeRange", err)
	}
	if _, err := line.EventTime(track, time.Duration(math.MaxInt64)); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("event time overflow error = %v, want ErrTimeRange", err)
	}
	if err := line.SetShift(track, time.Second); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if _, err := line.EventTime(track, time.Duration(math.MinInt64)); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("event time underflow error = %v, want ErrTimeRange", err)
	}

	playhead := NewPlayhead()
	playhead.Play()
	if err := playhead.Seek(time.Duration(math.MaxInt64) - time.Second); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if err := playhead.Advance(2 * time.Second); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("advance overflow error = %v, want ErrTimeRange", err)
	}
	if got := playhead.Event(); got != time.Duration(math.MaxInt64)-time.Second {
		t.Fatalf("event after failed advance = %s", got)
	}
}

// TestPlayheadPauseResume checks that the shared clock advances only while
// playing and that pause and resume affect every track at once by moving no
// target on its own.
func TestPlayheadPauseResume(t *testing.T) {
	playhead := NewPlayhead()
	if playhead.Playing() {
		t.Fatal("new playhead is playing")
	}
	if err := playhead.Advance(time.Second); err != nil {
		t.Fatalf("advance while paused: %v", err)
	}
	if got := playhead.Event(); got != 0 {
		t.Fatalf("event while paused = %s, want 0", got)
	}

	playhead.Play()
	playhead.Play()
	if err := playhead.Advance(1500 * time.Millisecond); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if got := playhead.Event(); got != 1500*time.Millisecond {
		t.Fatalf("event after play = %s, want 1.5s", got)
	}

	playhead.Pause()
	playhead.Pause()
	if err := playhead.Advance(time.Second); err != nil {
		t.Fatalf("advance after pause: %v", err)
	}
	if got := playhead.Event(); got != 1500*time.Millisecond {
		t.Fatalf("event after pause = %s, want 1.5s", got)
	}

	if err := playhead.Advance(-time.Second); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("negative advance error = %v, want ErrTimeRange", err)
	}
}

// TestSeekKeepsStateAndTargets seeks to fifteen seconds of history in both
// states and expects every target to move by exactly the seek distance, with
// no change to the delays.
func TestSeekKeepsStateAndTargets(t *testing.T) {
	line := NewTimeline()
	ahead := TrackID("track-ahead")
	behind := TrackID("track-behind")
	if err := line.SetShift(ahead, 3200*time.Millisecond); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if err := line.SetShift(behind, -2500*time.Millisecond); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	before := line.Plan(0, ahead, behind)

	playhead := NewPlayhead()
	if err := playhead.Seek(15 * time.Second); err != nil {
		t.Fatalf("seek while paused: %v", err)
	}
	if playhead.Playing() {
		t.Fatal("seek while paused started playback")
	}
	after := line.Plan(playhead.Event(), ahead, behind)
	for i := range before {
		if got := after[i].Target - before[i].Target; got != 15*time.Second {
			t.Fatalf("%s target moved by %s, want 15s", after[i].Track, got)
		}
		if after[i].Delay != before[i].Delay {
			t.Fatalf("%s delay changed by seek: %s -> %s", after[i].Track, before[i].Delay, after[i].Delay)
		}
	}

	playhead.Play()
	if err := playhead.Seek(0); err != nil {
		t.Fatalf("seek while playing: %v", err)
	}
	if !playhead.Playing() {
		t.Fatal("seek while playing stopped playback")
	}
	back := line.Plan(playhead.Event(), ahead, behind)
	if !reflect.DeepEqual(before, back) {
		t.Fatalf("plans after seeking back = %+v, want %+v", back, before)
	}

	if err := playhead.Seek(-time.Second); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("negative seek error = %v, want ErrTimeRange", err)
	}
	if got := playhead.Event(); got != 0 {
		t.Fatalf("event after refused seek = %s, want 0", got)
	}
}

// TestPlanOrderAndEmpty checks that plans come back in the order asked for and
// that an empty request is not an error.
func TestPlanOrderAndEmpty(t *testing.T) {
	line := NewTimeline()
	for _, track := range []TrackID{"track-c", "track-a", "track-b"} {
		if err := line.SetShift(track, time.Second); err != nil {
			t.Fatalf("set shift: %v", err)
		}
	}
	if plans := line.Plan(time.Second); len(plans) != 0 {
		t.Fatalf("empty plan = %+v", plans)
	}
	plans := line.Plan(time.Second, "track-b", "track-c", "track-a", "track-missing")
	want := []TrackID{"track-b", "track-c", "track-a", "track-missing"}
	for i, track := range want {
		if plans[i].Track != track {
			t.Fatalf("plan %d track = %s, want %s", i, plans[i].Track, track)
		}
	}
	if plans[3].Alignment != AlignmentUnknown {
		t.Fatalf("missing track alignment = %s, want not_aligned", plans[3].Alignment)
	}
}

// TestShiftsAreOrderedByTrack checks the snapshot order clients can rely on.
func TestShiftsAreOrderedByTrack(t *testing.T) {
	line := NewTimeline()
	for _, track := range []TrackID{"track-c", "track-a", "track-b"} {
		if err := line.SetShift(track, time.Second); err != nil {
			t.Fatalf("set shift: %v", err)
		}
	}
	shifts := line.Shifts()
	want := []TrackID{"track-a", "track-b", "track-c"}
	if len(shifts) != len(want) {
		t.Fatalf("shifts = %+v", shifts)
	}
	for i, track := range want {
		if shifts[i].Track != track {
			t.Fatalf("shift %d track = %s, want %s", i, shifts[i].Track, track)
		}
	}
	if err := line.SetShift("", time.Second); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty track shift error = %v, want ErrInvalid", err)
	}

	// The zero value is usable without a constructor.
	var zero Timeline
	if err := zero.SetShift("track-a", time.Second); err != nil {
		t.Fatalf("set shift on zero value: %v", err)
	}
	if !zero.Aligned("track-a") {
		t.Fatal("zero value lost its shift")
	}
}

// TestBufferGapForgetsAnchor covers a source that stops delivering: its old
// anchor must be dropped instead of extrapolated, and the other tracks keep
// their alignment.
func TestBufferGapForgetsAnchor(t *testing.T) {
	line := NewTimeline()
	gapped := TrackID("track-gapped")
	steady := TrackID("track-steady")
	if err := line.SetShift(gapped, 4*time.Second); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if err := line.SetShift(steady, -time.Second); err != nil {
		t.Fatalf("set shift: %v", err)
	}

	line.Forget(gapped)
	if _, err := line.MediaTime(gapped, 10*time.Second); !errors.Is(err, ErrNotAligned) {
		t.Fatalf("media time after gap error = %v, want ErrNotAligned", err)
	}
	plans := line.Plan(10*time.Second, gapped, steady)
	if plans[0].Alignment != AlignmentUnknown {
		t.Fatalf("gapped track alignment = %s, want not_aligned", plans[0].Alignment)
	}
	if plans[1].Alignment != AlignmentKnown || plans[1].Target != 9*time.Second {
		t.Fatalf("steady track plan = %+v", plans[1])
	}

	// A new calibration after the gap replaces the old anchor.
	if err := line.SetShift(gapped, 4200*time.Millisecond); err != nil {
		t.Fatalf("recalibrate: %v", err)
	}
	if got, err := line.MediaTime(gapped, 10*time.Second); err != nil || got != 14200*time.Millisecond {
		t.Fatalf("media time after recalibration = %s, %v, want 14.2s", got, err)
	}
}

// TestSeekDropsUnseekableSource covers a seek the media layer cannot perform:
// the caller drops that track rather than claiming a position it never
// reached, while tracks that did seek stay aligned.
func TestSeekDropsUnseekableSource(t *testing.T) {
	line := NewTimeline()
	seekable := TrackID("track-seekable")
	live := TrackID("track-live")
	if err := line.SetShift(seekable, 3200*time.Millisecond); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if err := line.SetShift(live, -500*time.Millisecond); err != nil {
		t.Fatalf("set shift: %v", err)
	}

	playhead := NewPlayhead()
	playhead.Play()
	if err := playhead.Seek(15 * time.Second); err != nil {
		t.Fatalf("seek: %v", err)
	}
	line.Forget(live)

	plans := line.Plan(playhead.Event(), seekable, live)
	if plans[0].Alignment != AlignmentKnown || plans[0].Target != 18200*time.Millisecond {
		t.Fatalf("seekable plan = %+v, want 18.2s", plans[0])
	}
	if plans[1].Alignment != AlignmentUnknown {
		t.Fatalf("live track alignment = %s, want not_aligned", plans[1].Alignment)
	}
	if !playhead.Playing() {
		t.Fatal("dropping a track changed the shared clock")
	}
}

// TestAdjustShiftMovesFromStoredOffset checks how a manual nudge is applied:
// each step moves the offset stored on the timeline, so a repeated nudge adds
// up exactly and does not depend on where a player happened to be.
func TestAdjustShiftMovesFromStoredOffset(t *testing.T) {
	line := NewTimeline()
	const track TrackID = "track-voice"
	steps := []struct {
		delta time.Duration
		want  time.Duration
	}{
		{-100 * time.Millisecond, -100 * time.Millisecond},
		{-100 * time.Millisecond, -200 * time.Millisecond},
		{-100 * time.Millisecond, -300 * time.Millisecond},
		{1 * time.Second, 700 * time.Millisecond},
		{-1700 * time.Millisecond, -1 * time.Second},
	}
	for i, step := range steps {
		shift, err := line.AdjustShift(track, step.delta)
		if err != nil {
			t.Fatalf("step %d: adjust shift: %v", i, err)
		}
		if shift.Offset != step.want || shift.Track != track {
			t.Fatalf("step %d: shift = %+v, want {%s %s}", i, shift, track, step.want)
		}
		stored, ok := line.Shift(track)
		if !ok || stored.Offset != step.want {
			t.Fatalf("step %d: stored shift = %+v (present %v), want %s", i, stored, ok, step.want)
		}
	}
	// The target the media layer would seek to follows the nudged offset.
	media, err := line.MediaTime(track, 10*time.Second)
	if err != nil {
		t.Fatalf("media time: %v", err)
	}
	if want := 9 * time.Second; media != want {
		t.Fatalf("media time = %s, want %s", media, want)
	}
}

// TestAdjustShiftRejectsBadNudges checks the two ways a nudge can fail: a track
// identifier nothing can be stored under, and a sum past the range a duration
// can hold.
func TestAdjustShiftRejectsBadNudges(t *testing.T) {
	line := NewTimeline()
	if _, err := line.AdjustShift("", time.Second); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty track: err = %v, want ErrInvalid", err)
	}
	if err := line.SetShift("track-edge", math.MaxInt64); err != nil {
		t.Fatalf("set shift: %v", err)
	}
	if _, err := line.AdjustShift("track-edge", time.Second); !errors.Is(err, ErrTimeRange) {
		t.Fatalf("overflow: err = %v, want ErrTimeRange", err)
	}
	stored, ok := line.Shift("track-edge")
	if !ok || stored.Offset != math.MaxInt64 {
		t.Fatalf("stored shift = %+v (present %v), want the old offset kept", stored, ok)
	}
}

// TestAlignmentNames fixes the spelling clients read.
func TestAlignmentNames(t *testing.T) {
	cases := map[Alignment]string{
		AlignmentKnown:   "known",
		AlignmentUnknown: "not_aligned",
		Alignment(99):    "Alignment(99)",
		Alignment(-1):    "Alignment(-1)",
	}
	for alignment, want := range cases {
		if got := alignment.String(); got != want {
			t.Fatalf("Alignment(%d).String() = %q, want %q", int(alignment), got, want)
		}
	}
}
