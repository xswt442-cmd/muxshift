package main

// P16.4f-a: the decisions entering replay is allowed to make, and the ones it
// must refuse. These tests read no player, cut no recording and start no
// window: every case is a snapshot handed to the same function the session
// feeds.

import (
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// entryFixture builds one source's contribution to an entry decision. The
// anchor places buffer time at media time 100s, the offset moves it two seconds
// the other way, so a window [start, end) is enterable over [98s+start,
// 98s+end).
func entryFixture(name string, start, end time.Duration) entrySourceState {
	anchor := dvr.Anchor{
		Source:         core.SourceID(name),
		Epoch:          2,
		BufferInstance: "buf-" + name,
		BufferStart:    start,
		MediaStart:     100*time.Second + start,
		Interval:       dvr.Range{Start: start, End: end, Epoch: 2},
		MeasuredAt:     time.Unix(1_700_000_000, 0),
	}
	return entrySourceState{
		Source:         core.SourceID(name),
		Name:           name,
		Track:          "track-1",
		Generation:     3,
		Instance:       "buf-" + name,
		Epoch:          2,
		Window:         dvr.Range{Start: start, End: end, Epoch: 2},
		Anchor:         anchor,
		Offset:         2 * time.Second,
		OriginInstance: "buf-" + name,
		OriginEpoch:    2,
	}
}

func TestEntryPlansTheFirstWholeSecondBothSourcesCanReach(t *testing.T) {
	first := entryFixture("source-1", 0, 30*time.Second)
	second := entryFixture("source-2", 4*time.Second, 34*time.Second)

	plan, err := planEntry([]entrySourceState{first, second}, 0, false)
	if err != nil {
		t.Fatalf("a shared window was not planned: %v", err)
	}
	if plan.Event != 102*time.Second {
		t.Fatalf("the default target is %s, want 1m42s (the later window's start)", plan.Event)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("the plan has %d steps, want one per source", len(plan.Steps))
	}
	if got, want := plan.Steps[0].Cut, 4*time.Second; got != want {
		t.Fatalf("source-1 would be cut at %s, want %s", got, want)
	}
	if plan.Steps[0].InFile != 0 {
		t.Fatalf("source-1 would start %s into its file, but a recording begins at the cut", plan.Steps[0].InFile)
	}
	if plan.Steps[0].Media != 104*time.Second {
		t.Fatalf("source-1's media time is %s, want 1m44s", plan.Steps[0].Media)
	}
}

func TestEntryRefusesWhenNoInstantIsShared(t *testing.T) {
	early := entryFixture("source-1", 0, 10*time.Second)
	late := entryFixture("source-2", 12*time.Second, 22*time.Second)

	if _, err := planEntry([]entrySourceState{early, late}, 0, false); err == nil {
		t.Fatal("a plan was made for sources whose windows never overlap")
	} else if !strings.Contains(err.Error(), "no shared instant") {
		t.Fatalf("the refusal does not explain the gap: %v", err)
	}
}

func TestEntryNamesASourceWithoutARecordedOrigin(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	source.OriginInstance = ""

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "source-1") || !strings.Contains(err.Error(), "another buffer") {
		t.Fatalf("a missing origin was not refused by name: %v", err)
	}
}

func TestEntryNamesASourceWithoutAMeasuredAnchor(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	source.Anchor = dvr.Anchor{}

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "source-1") || !strings.Contains(err.Error(), "no measured anchor") {
		t.Fatalf("an unmeasured anchor was not refused by name: %v", err)
	}
}

func TestEntryRefusesAnAnchorFromAnotherEpoch(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	source.Anchor.Epoch = 3

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "another buffer or epoch") {
		t.Fatalf("an anchor from another epoch was accepted: %v", err)
	}
}

func TestEntryDoesNotCrossAGapBetweenRanges(t *testing.T) {
	// The buffer holds [0s, 10s) and would hold more after the gap, but the
	// plan's window is the one continuous range the anchor explains.
	source := entryFixture("source-1", 0, 10*time.Second)
	source.Anchor.Interval = dvr.Range{Start: 0, End: 2 * time.Second, Epoch: 2}

	// Entering at 108s would be media 110s, i.e. buffer 10s: exactly at the end
	// of the range, which belongs to whatever comes next.
	if _, err := planEntry([]entrySourceState{source}, 108*time.Second, true); err == nil {
		t.Fatal("a target at the edge of a range was accepted")
	}
	if _, err := planEntry([]entrySourceState{source}, 105*time.Second, true); err != nil {
		t.Fatalf("a target inside the range was refused: %v", err)
	}
}

func TestEntryKeepsAnExplicitTargetExactly(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)

	plan, err := planEntry([]entrySourceState{source}, 102_300*time.Millisecond, true)
	if err != nil {
		t.Fatalf("an explicit target inside the window was refused: %v", err)
	}
	if plan.Event != 102_300*time.Millisecond {
		t.Fatalf("the explicit target became %s, want 1m42.3s unchanged", plan.Event)
	}
	if !plan.Explicit {
		t.Fatal("the plan does not record that the target was explicit")
	}
}

func TestEntryRefusesAnExplicitTargetOutsideTheWindow(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)

	_, err := planEntry([]entrySourceState{source}, 140*time.Second, true)
	if err == nil || !strings.Contains(err.Error(), "source-1") || !strings.Contains(err.Error(), "cannot enter replay at") {
		t.Fatalf("a target outside the window was not refused by name: %v", err)
	}
}

func TestEntryRefusesADefaultTargetAtTheEndBoundary(t *testing.T) {
	// The latest window starts at 0.4s and ends at exactly 1s after it began,
	// so rounding the start up to a whole second lands on the end.
	source := entryFixture("source-1", 400*time.Millisecond, 1_000*time.Millisecond)

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "no shared instant") {
		t.Fatalf("a default target at the end boundary was accepted: %v", err)
	}
}

func TestRecheckEntryRefusesAChangedGeneration(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	plan, err := planEntry([]entrySourceState{source}, 0, false)
	if err != nil {
		t.Fatalf("the plan was refused: %v", err)
	}

	moved := source
	moved.Generation++
	err = recheckEntry(plan, []entrySourceState{moved})
	if err == nil || !strings.Contains(err.Error(), "player was replaced") {
		t.Fatalf("a replaced player did not stop the commit: %v", err)
	}
}

func TestRecheckEntryRefusesToRemeasuredClock(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	plan, err := planEntry([]entrySourceState{source}, 0, false)
	if err != nil {
		t.Fatalf("the plan was refused: %v", err)
	}

	remeasured := source
	remeasured.Anchor.MeasuredAt = source.Anchor.MeasuredAt.Add(time.Second)
	err = recheckEntry(plan, []entrySourceState{remeasured})
	if err == nil || !strings.Contains(err.Error(), "measured again") {
		t.Fatalf("a re-measured clock did not stop the commit: %v", err)
	}
}

func TestRecheckEntryRefusesAChangedOffset(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	plan, err := planEntry([]entrySourceState{source}, 0, false)
	if err != nil {
		t.Fatalf("the plan was refused: %v", err)
	}

	same := source
	if err := recheckEntry(plan, []entrySourceState{same}); err != nil {
		t.Fatalf("an unchanged session was refused: %v", err)
	}
	moved := source
	moved.Offset += 10 * time.Millisecond
	err = recheckEntry(plan, []entrySourceState{moved})
	if err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("a changed offset did not stop the commit: %v", err)
	}
}

func TestEntryAccountsAreLabelledAsEntering(t *testing.T) {
	enter := (&seekTxn{event: 5 * time.Second, id: 7, kind: replayKindEnter}).reportFor(replayPreparing, "")
	if line := enter.Line(); !strings.HasPrefix(line, "enter replay 7 at 5s: preparing") {
		t.Fatalf("an entry account reads %q", line)
	}
	seek := (&seekTxn{event: 5 * time.Second, id: 8}).reportFor(replayPreparing, "")
	if line := seek.Line(); !strings.HasPrefix(line, "replay 8 for event 5s: preparing") {
		t.Fatalf("a seek account reads %q", line)
	}
}
