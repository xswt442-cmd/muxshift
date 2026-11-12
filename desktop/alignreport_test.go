package main

import (
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/align"
)

// suggestionFor builds a finished suggestion the way suggestAlign leaves one:
// both windows, the snapshot it was computed from, its result, and the numbers
// the caller compares against the session.
func suggestionFor(reference, target string) alignSuggestion {
	return alignSuggestion{
		Reference: alignWindow{Source: "source-1", Index: 0, URI: reference, Start: 0, Length: 30 * time.Second},
		Target:    alignWindow{Source: "source-2", Index: 1, URI: target, Start: 4 * time.Second, Length: 26 * time.Second},
		Snapshot: probeSnapshot{
			Track:   "track-2",
			Offset:  time.Second,
			Aligned: true,
		},
		Result: align.Result{
			Offset:       0,
			Peak:         0.918,
			RunnerUp:     0.152,
			RunnerOffset: 2400 * time.Millisecond,
			Confidence:   0.861,
			Overlap:      25900 * time.Millisecond,
			Accepted:     true,
		},
		CoreOffset: 2 * time.Second,
		Current:    time.Second,
		Delta:      time.Second,
		Elapsed:    387 * time.Millisecond,
	}
}

func TestTheReportShowsEveryReasonToTrustOrDoubtIt(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	lines := strings.Join(app.alignLines(), "\n")
	for _, want := range []string{
		"source 2 compared with source 1",
		"windows 4s..30s · 0s..30s · matched 25.9s of 26s",
		"peak 0.918 · next 0.152 at +2.400s · confidence 0.861",
		"suggested +2.000s · now +1.000s · difference +1.000s · took 387ms",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("the report does not say %q:\n%s", want, lines)
		}
	}
	if state := app.reportOf(app.lastProbe).state(); state != "proposed" {
		t.Fatalf("a fresh accepted report is in state %q, want proposed", state)
	}
}

func TestARefusedReportSaysWhyAndProposesNothing(t *testing.T) {
	app := alignApp()
	suggestion := suggestionFor("a.m3u8", "b.m3u8")
	suggestion.Result.Accepted = false
	suggestion.Result.Reason = "peak 0.092 is below the 0.250 a match needs"
	app.lastProbe = suggestion
	lines := strings.Join(app.alignLines(), "\n")
	if !strings.Contains(lines, "no suggestion: peak 0.092 is below the 0.250 a match needs") {
		t.Fatalf("a refused report does not say why:\n%s", lines)
	}
	if strings.Contains(lines, "suggested ") {
		t.Fatalf("a refused report proposes something:\n%s", lines)
	}
	if state := app.reportOf(app.lastProbe).state(); state != "refused" {
		t.Fatalf("state %q, want refused", state)
	}
}

// A probe that finishes after somebody nudged the target no longer describes
// the session; the report has to say so rather than show the old proposal as
// if it were current.
func TestAReportGoesStaleWhenTheSessionMoves(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	if expired := app.reportOf(app.lastProbe).Expired; expired != "" {
		t.Fatalf("a report read from a matching session is already expired: %s", expired)
	}

	app.sync.timeline.SetShift("track-2", 750*time.Millisecond)
	report := app.reportOf(app.lastProbe)
	if report.Expired != "the offset of the target changed since the probe started" {
		t.Fatalf("expiry reads %q", report.Expired)
	}
	if state := report.state(); state != "expired" {
		t.Fatalf("state %q, want expired", state)
	}
	if !strings.Contains(strings.Join(report.lines(), "\n"), "expired: the offset of the target changed") {
		t.Fatalf("the expired report does not say it expired:\n%s", strings.Join(report.lines(), "\n"))
	}
}

func TestShowingTheReportChangesNothing(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	before, ok := app.sync.timeline.Shift("track-2")
	_ = app.alignLines()
	_ = app.alignLine()
	_ = app.reportOf(app.lastProbe).state()
	after, still := app.sync.timeline.Shift("track-2")
	if ok != still || before != after {
		t.Fatalf("showing the report changed the target's shift: %+v/%t -> %+v/%t", before, ok, after, still)
	}
	if _, has := app.sync.timeline.Shift("track-1"); has {
		t.Fatalf("showing the report aligned a track nobody aligned")
	}
}

// alignApp is a session as a finished probe saw it: two sources present with the
// generation the probe recorded, and the target's offset where it was.
func alignApp() *appState {
	app := &appState{sync: newSyncState()}
	app.items = []*item{
		{source: core.Source{ID: "source-1"}},
		{source: core.Source{ID: "source-2"}},
	}
	app.sync.timeline.SetShift("track-2", time.Second)
	return app
}
