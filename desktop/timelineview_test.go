package main

import (
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

func timelineViewApp(t *testing.T, tracks ...core.TrackID) *appState {
	t.Helper()
	app := &appState{sync: newSyncState()}
	for index, track := range tracks {
		app.items = append(app.items, &item{
			source: core.Source{ID: core.SourceID("source-" + string(rune('1'+index)))},
			uri:    "example" + string(rune('1'+index)) + ".m3u8",
			video:  track,
		})
	}
	return app
}

// An unaligned track is not a track calibrated to zero: the two must not read
// the same in the view.
func TestAnUnalignedRowSaysUnknownRatherThanZero(t *testing.T) {
	app := timelineViewApp(t, "track-1")
	lines := app.timelineLines()
	if len(lines) != 1 {
		t.Fatalf("want one line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "not aligned") || !strings.Contains(lines[0], "media unknown") {
		t.Fatalf("an unaligned row does not say it is unknown: %q", lines[0])
	}
	if strings.Contains(lines[0], "+0.0s") || strings.Contains(lines[0], "offset") {
		t.Fatalf("an unaligned row claims an offset: %q", lines[0])
	}
}

// A real zero offset is a calibration and reads as one.
func TestAnAlignedZeroRowShowsZero(t *testing.T) {
	app := timelineViewApp(t, "track-1")
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatal(err)
	}
	app.sync.place("track-1", dvr.BasisManual, "buf-1", 1)
	line := app.timelineLines()[0]
	if !strings.Contains(line, "offset +0.0s") || !strings.Contains(line, "manual") {
		t.Fatalf("an aligned zero row does not read as one: %q", line)
	}
	if strings.Contains(line, "not aligned") {
		t.Fatalf("an aligned row still reads as unknown: %q", line)
	}
}

// The mapped media time is the timeline's answer for the shared event time, in
// the sign the rest of the app uses.
func TestTheRowSeparatesEventMediaAndPlayerPosition(t *testing.T) {
	app := timelineViewApp(t, "track-1")
	if err := app.sync.timeline.SetShift("track-1", -1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := app.sync.playhead.Seek(6 * time.Second); err != nil {
		t.Fatal(err)
	}
	line := app.timelineLines()[0]
	for _, want := range []string{"offset -1.5s", "media 4.5s", "not open"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the row is missing %q: %q", want, line)
		}
	}
}

// Reading the view must not move anything: refreshes and re-renders happen on
// every frame.
func TestReadingTheViewChangesNothing(t *testing.T) {
	app := timelineViewApp(t, "track-1", "track-2")
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := app.sync.playhead.Seek(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	before := app.sync.timeline.Shifts()
	app.timelineLines()
	app.timelineLines()
	after := app.sync.timeline.Shifts()
	if len(before) != len(after) {
		t.Fatalf("the view changed the number of shifts: %d -> %d", len(before), len(after))
	}
	for index := range before {
		if before[index] != after[index] {
			t.Fatalf("the view changed shift %d: %+v -> %+v", index, before[index], after[index])
		}
	}
	if event := app.sync.playhead.Event(); event != 3*time.Second {
		t.Fatalf("the view moved the playhead to %s", event)
	}
	if _, ok := app.sync.timeline.Shift("track-2"); ok {
		t.Fatalf("the view aligned a track nobody aligned")
	}
}

// The reference is the sync group's reference track, not whichever source
// happens to be first.
func TestTheReferenceComesFromTheSyncGroup(t *testing.T) {
	session := core.NewSession("view")
	first, err := session.AddSource("a.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.AddSource("b.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	firstTrack, err := session.AddTrack(first.ID, core.MediaVideo, "first")
	if err != nil {
		t.Fatal(err)
	}
	secondTrack, err := session.AddTrack(second.ID, core.MediaVideo, "second")
	if err != nil {
		t.Fatal(err)
	}
	// The group's reference is the second source's track: being first in the
	// window does not make a source the reference.
	group, err := session.AddSyncGroup(secondTrack.ID, secondTrack.ID, firstTrack.ID)
	if err != nil {
		t.Fatal(err)
	}
	app := &appState{session: session, sync: newSyncState()}
	app.sync.group = group.ID
	app.items = []*item{
		{source: first, uri: "a.m3u8", video: firstTrack.ID},
		{source: second, uri: "b.m3u8", video: secondTrack.ID},
	}
	if got := app.referenceTrack(); got != secondTrack.ID {
		t.Fatalf("the reference is %q, want %q", got, secondTrack.ID)
	}
	lines := app.timelineLines()
	// The second source carries the reference track, even though it is the
	// second line in the window.
	if !strings.Contains(lines[1], "track-2 ref") {
		t.Fatalf("the reference track is not marked: %q", lines[1])
	}
	if strings.Contains(lines[0], "ref") {
		t.Fatalf("a non-reference track is marked as one: %q", lines[0])
	}
}
