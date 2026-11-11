package main

// P16.4b-1: the read-only adapter from the live session to a decision. These
// tests check that the adapter reads what is really there, that a refusal comes
// with the identity that could not be proven, that a state which changed between
// the decision and its confirmation is refused rather than spliced in, and that
// deciding touches nothing a later phase has to clean up.

import (
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// replaySeekItem is one source with a real buffer, a recording cut from it and a
// measured anchor, plus the session state that places it on the shared timeline.
type replaySeekState struct {
	app    *appState
	items  []*item
	tracks []core.TrackID
	// adds push another segment into the same buffer a source is reading from,
	// which is how a test moves the state while a snapshot is being taken.
	adds []func(sequence int, discontinuity bool)
}

func replaySeekItem(t *testing.T, app *appState, source core.SourceID, track core.TrackID, media time.Duration) (*item, func(int, bool)) {
	t.Helper()
	buffer, add := fixtureBufferOptions(t, dvr.Options{Source: source})
	add(2, false)
	add(3, false)
	add(4, false)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("cut %s at 0s: %v", source, err)
	}
	state := buffer.State()
	anchor := &dvr.Anchor{
		Source:         source,
		Epoch:          state.Epoch,
		BufferInstance: state.Instance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     media,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	uri := "http://example.invalid/" + string(source) + "/index.m3u8"
	app.runners = append(app.runners, trackedRunner{
		uri:    uri,
		runner: &dvrRunner{source: source, buffer: buffer},
	})
	it := &item{
		uri:          uri,
		source:       core.Source{ID: source},
		video:        track,
		replay:       &recording,
		replayAnchor: anchor,
		generation:   2,
	}
	if err := app.sync.timeline.SetShift(track, 0); err != nil {
		t.Fatalf("place %s: %v", track, err)
	}
	app.sync.place(track, dvr.BasisManual, state.Instance, state.Epoch)
	return it, add
}

// replaySeekSession is the ordinary state: two sources of one event, both
// confirmed at offset zero, each with its own buffer and recording.
func replaySeekSession(t *testing.T) replaySeekState {
	t.Helper()
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	first, firstAdd := replaySeekItem(t, app, "source-1", "track-1", 1466667*time.Microsecond)
	second, secondAdd := replaySeekItem(t, app, "source-2", "track-2", 5466667*time.Microsecond)
	app.items = []*item{first, second}
	return replaySeekState{
		app:    app,
		items:  []*item{first, second},
		tracks: []core.TrackID{"track-1", "track-2"},
		adds:   []func(int, bool){firstAdd, secondAdd},
	}
}

func TestPlanReplaySeekReadsTheSessionState(t *testing.T) {
	session := replaySeekSession(t)
	plan, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if plan.Event() != 6*time.Second {
		t.Fatalf("target = %s, want 6s", plan.Event())
	}
	for _, it := range session.items {
		hop, ok := plan.Hop(it.source.ID)
		if !ok {
			t.Fatalf("the plan has no hop for %s", it.source.ID)
		}
		if hop.Content != 6*time.Second {
			t.Fatalf("%s: content = %s, want 6s", it.source.ID, hop.Content)
		}
		if want := 6*time.Second - it.replay.StartBufferTime; hop.Position != want {
			t.Fatalf("%s: position = %s, want %s", it.source.ID, hop.Position, want)
		}
		if hop.Instance != it.replay.BufferInstance || hop.Generation != 2 {
			t.Fatalf("%s: hop names instance %q generation %d", it.source.ID, hop.Instance, hop.Generation)
		}
	}
	if err := session.app.confirmReplaySeek(plan, session.items); err != nil {
		t.Fatalf("confirming an unchanged session: %v", err)
	}
}

func TestConfirmReplaySeekRefusesAStateThatMoved(t *testing.T) {
	session := replaySeekSession(t)
	plan, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	// The same source now reads from a different buffer: another instance, so the
	// anchor and the recording the plan named cannot describe it any more.
	rebuilt, _ := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
	session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: rebuilt}

	err = session.app.confirmReplaySeek(plan, session.items)
	if err == nil {
		t.Fatal("a rebuilt buffer confirmed a plan made from the old one")
	}
	if !containsAll(err.Error(), "source-1", "buffer") {
		t.Fatalf("the refusal %q does not name the source and its buffer", err)
	}
}

func TestPlanReplaySeekRefusesAnOffsetWithoutOrigin(t *testing.T) {
	session := replaySeekSession(t)
	// The timeline still holds a zero, but nothing remembers placing it.
	delete(session.app.sync.origins, "track-2")

	_, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("an offset of unknown origin authorised a seek")
	}
	if !containsAll(err.Error(), "source-2", "origin") {
		t.Fatalf("the refusal %q does not name the source and the missing origin", err)
	}
}

func TestPlanReplaySeekRefusesATargetOutsideTheRange(t *testing.T) {
	session := replaySeekSession(t)
	// Every fixture buffer holds 8s of media, so an event far past it has no range.
	_, err := session.app.planReplaySeek(10*time.Minute, session.items)
	if err == nil {
		t.Fatal("a target outside the buffer was accepted")
	}
}

func TestPlanningAReplaySeekTouchesNothing(t *testing.T) {
	session := replaySeekSession(t)
	before := make([]string, 0, len(session.app.runners))
	for _, tracked := range session.app.runners {
		before = append(before, tracked.runner.buffer.Instance())
	}
	plan, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if err := session.app.confirmReplaySeek(plan, session.items); err != nil {
		t.Fatalf("confirming: %v", err)
	}
	for index, it := range session.items {
		if it.player != nil {
			t.Fatalf("%s opened a player", it.source.ID)
		}
		if it.generation != 2 {
			t.Fatalf("%s generation moved to %d", it.source.ID, it.generation)
		}
		if after := session.app.runners[index].runner.buffer.Instance(); after != before[index] {
			t.Fatalf("%s buffer was replaced during a decision: %s -> %s", it.source.ID, before[index], after)
		}
	}
	if len(session.app.pending) != 0 {
		t.Fatalf("a decision queued %d players for closing", len(session.app.pending))
	}
}

func TestConfirmReplaySeekRefusesARangeThatWasEvicted(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	buffer, add := fixtureBufferOptions(t, dvr.Options{Source: "source-1", Retention: 2 * time.Second})
	add(2, false)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("cut at 0s: %v", err)
	}
	state := buffer.State()
	anchor := &dvr.Anchor{
		Source:         "source-1",
		Epoch:          state.Epoch,
		BufferInstance: state.Instance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	uri := "http://example.invalid/source-1/index.m3u8"
	app.runners = append(app.runners, trackedRunner{uri: uri, runner: &dvrRunner{source: "source-1", buffer: buffer}})
	it := &item{uri: uri, source: core.Source{ID: "source-1"}, video: "track-1", replay: &recording, replayAnchor: anchor, generation: 1}
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatalf("place the track: %v", err)
	}
	app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)

	plan, err := app.planReplaySeek(1*time.Second, []*item{it})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	// The oldest segment goes away, so the range the decision named is gone.
	if removed := buffer.Evict(nil); len(removed) == 0 {
		t.Fatal("nothing was evicted, so the range did not move")
	}
	if err := app.confirmReplaySeek(plan, []*item{it}); err == nil {
		t.Fatal("a plan whose range was evicted still held")
	}
	if it.player != nil {
		t.Fatal("a refused confirmation opened a player")
	}
}

// containsAll reports whether every part appears in text.
func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for index := 0; index+len(part) <= len(text); index++ {
			if text[index:index+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestPlanReplaySeekRefusesABufferWithoutARecording(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	buffer, add := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
	add(1, false)
	uri := "http://example.invalid/source-1/index.m3u8"
	app.runners = append(app.runners, trackedRunner{uri: uri, runner: &dvrRunner{source: "source-1", buffer: buffer}})
	it := &item{uri: uri, source: core.Source{ID: "source-1"}, video: "track-1", generation: 1}
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatalf("place the track: %v", err)
	}
	state := buffer.State()
	app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)
	if cut := len(buffer.Recordings()); cut != 0 {
		t.Fatalf("the fixture already cut %d recording(s)", cut)
	}

	// The source is buffering, but nothing has been cut from it yet. No plan may
	// be made by inventing an id or a path, and no file may be created to fill
	// the contract in.
	if _, err := app.planReplaySeek(1*time.Second, []*item{it}); err == nil {
		t.Fatal("a buffer without a recording produced a plan")
	} else if !containsAll(err.Error(), "no usable recording") {
		t.Fatalf("the refusal %q does not say there is no recording", err)
	}
	if cut := len(buffer.Recordings()); cut != 0 {
		t.Fatalf("refusing cut %d recording(s) to satisfy the contract", cut)
	}
	if it.player != nil {
		t.Fatal("refusing opened a player")
	}
	if len(app.pending) != 0 {
		t.Fatalf("refusing queued %d player(s) for closing", len(app.pending))
	}
}

func TestPlanReplaySeekRefusesASpliceOfTwoMoments(t *testing.T) {
	session := replaySeekSession(t)
	runner := session.app.runners[0].runner
	defer func() { replaySnapshotHook = nil }()
	replaySnapshotHook = func() {
		// The playlist declares a discontinuity while the snapshot is being read,
		// so the buffer the decision is being made from moves to another epoch.
		_ = runner
		session.adds[0](9, true)
	}
	_, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("a state that moved during the read produced a plan")
	}
	if !containsAll(err.Error(), "changed while its state was read") {
		t.Fatalf("the refusal %q does not say the state moved", err)
	}
}

func TestReplaySeekRefusesAnOriginFromAnotherBuffer(t *testing.T) {
	session := replaySeekSession(t)
	if _, err := session.app.planReplaySeek(6*time.Second, session.items); err != nil {
		t.Fatalf("planning on the first buffers: %v", err)
	}

	// The same source is left and joined again: the session now reads a buffer
	// built later, with its own content zero, its own recording and a measured
	// anchor that matches that buffer. The manual placement was made against the
	// old buffer's clock, so it says nothing about where this one starts.
	replacement, add := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
	add(2, false)
	add(3, false)
	add(4, false)
	recording, err := replacement.Record(0)
	if err != nil {
		t.Fatalf("cut the replacement: %v", err)
	}
	state := replacement.State()
	session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: replacement}
	session.items[0].replay = &recording
	session.items[0].replayAnchor = &dvr.Anchor{
		Source:         "source-1",
		Epoch:          state.Epoch,
		BufferInstance: state.Instance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     30 * time.Second,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	session.items[0].generation++

	_, err = session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("an offset placed against the old buffer authorised the new one")
	}
	if !containsAll(err.Error(), "source-1", "calibrated against buffer") {
		t.Fatalf("the refusal %q does not say which buffer the offset belongs to", err)
	}

	// Placing the new buffer on the timeline is what makes it usable again: the
	// old number is not re-labelled, a new relation is established.
	session.app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)
	if _, err := session.app.planReplaySeek(6*time.Second, session.items); err != nil {
		t.Fatalf("planning after placing the new buffer: %v", err)
	}
}

func TestReplaySeekRefusesARunnerReplacedDuringSnapshot(t *testing.T) {
	session := replaySeekSession(t)
	defer func() { replaySnapshotHook = nil }()
	replaySnapshotHook = func() {
		// The session swaps the runner under the item while the snapshot is
		// being taken. The buffer the adapter is reading stays perfectly stable,
		// so reading it twice cannot notice; only asking the session again can.
		replacement, _ := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
		session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: replacement}
	}
	_, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("a runner replaced during the read produced a plan")
	}
	if !containsAll(err.Error(), "source-1", "replaced while its state was read") {
		t.Fatalf("the refusal %q does not say the buffer was replaced", err)
	}
}

func TestConfirmReplaySeekRefusesEveryKindOfDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(session replaySeekState)
	}{
		{"buffer rebuilt", func(session replaySeekState) {
			// A new buffer keeps the source id but moves the instance and the
			// epoch together, which is exactly why a plan names both.
			rebuilt, _ := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
			session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: rebuilt}
		}},
		{"epoch moved", func(session replaySeekState) {
			// A discontinuity moves the clock without replacing the buffer.
			session.adds[0](9, true)
		}},
		{"anchor re-measured", func(session replaySeekState) {
			it := session.items[0]
			replaced := *it.replayAnchor
			replaced.MediaStart += 5 * time.Second
			it.replayAnchor = &replaced
		}},
		{"offset changed", func(session replaySeekState) {
			if _, err := session.app.sync.timeline.AdjustShift("track-1", 200*time.Millisecond); err != nil {
				t.Fatalf("moving the offset: %v", err)
			}
			instance, epoch := session.app.bufferClock(session.items[0])
			session.app.sync.place("track-1", dvr.BasisManual, instance, epoch)
		}},
		{"basis withdrawn", func(session replaySeekState) {
			delete(session.app.sync.origins, "track-1")
		}},
		{"player replaced", func(session replaySeekState) {
			session.items[0].generation++
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := replaySeekSession(t)
			plan, err := session.app.planReplaySeek(6*time.Second, session.items)
			if err != nil {
				t.Fatalf("planning: %v", err)
			}
			tc.mutate(session)
			if err := session.app.confirmReplaySeek(plan, session.items); err == nil {
				t.Fatalf("%s: the old plan still held", tc.name)
			}
			for _, it := range session.items {
				if it.player != nil {
					t.Fatalf("%s: a refused confirmation opened a player", it.source.ID)
				}
			}
			if len(session.app.pending) != 0 {
				t.Fatalf("%s: a refused confirmation queued a player for closing", tc.name)
			}
		})
	}
}
