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
		Reference: alignWindow{Source: "source-1", Index: 0, URI: reference, Track: "track-1", Start: 0, Length: 30 * time.Second},
		Target:    alignWindow{Source: "source-2", Index: 1, URI: target, Track: "track-2", Start: 4 * time.Second, Length: 26 * time.Second},
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
		"source 2 (track-2) compared with source 1 (track-1)",
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

// TestARecentFailureSetsTheInspectorTone pins the colour contract: a failed
// comparison is the news and is coloured as a failure, even while an older
// proposal is still valid enough to be shown below it.
func TestARecentFailureSetsTheInspectorTone(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	if got := app.alignState(); got != "proposed" {
		t.Fatalf("a fresh proposal reads as %q, want proposed", got)
	}

	app.lastProbeErr = "c.m3u8 has no audio track to compare"
	app.lastProbeFail = &probeFailure{
		Reference: alignWindow{Index: 0, Source: "source-1", Track: "track-2", URI: "a.m3u8"},
		Target:    alignWindow{Index: 2, Source: "source-3", URI: "c.m3u8"},
		Reason:    app.lastProbeErr,
	}
	if got := app.alignState(); got != "failed" {
		t.Fatalf("a failed comparison reads as %q, want failed", got)
	}
	if got := alignTone(app.alignState()); got != colorWarn {
		t.Fatalf("the failure is coloured %v, want the warning colour", got)
	}
	if got := app.reportOf(app.lastProbe).state(); got == "failed" {
		t.Fatalf("the older proposal's own state was rewritten as a failure")
	}

	// Clearing the failure lets the older proposal speak for itself again.
	app.lastProbeErr = ""
	app.lastProbeFail = nil
	if got := app.alignState(); got != "proposed" {
		t.Fatalf("after the failure cleared the block reads as %q, want proposed", got)
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

// TestTheReportNamesTheTrackItCompared keeps the identity honest: a proposal
// describes one track's offset, and the report has to say which.
func TestTheReportNamesTheTrackItCompared(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	first := app.alignLines()[0]
	if !strings.Contains(first, "source 2 (track-2)") || !strings.Contains(first, "source 1 (track-1)") {
		t.Fatalf("the report does not name the tracks it compared: %q", first)
	}
}

// TestAFailedComparisonSaysSoFirst covers the newest attempt failing while an
// older proposal still exists: the failure is the news, it is named after the
// sources it actually compared, and the older proposal may only be shown as what
// the failure would have replaced.
func TestAFailedComparisonSaysSoFirst(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	app.lastProbeErr = "c.m3u8 has no audio track to compare"
	app.lastProbeFail = &probeFailure{
		Reference: alignWindow{Index: 0, Source: "source-1", Track: "track-2", URI: "a.m3u8"},
		Target:    alignWindow{Index: 2, Source: "source-3", Track: "", URI: "c.m3u8"},
		Reason:    app.lastProbeErr,
	}
	lines := app.alignLines()
	if !strings.Contains(lines[0], "the last comparison did not finish:") {
		t.Fatalf("a failed comparison is not the first line: %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "c.m3u8") {
		t.Fatalf("the failure is not named after the source it compared: %q", lines[0])
	}
	if strings.Contains(lines[0], "b.m3u8") {
		t.Fatalf("the failure borrowed the older proposal's target: %q", lines[0])
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "an earlier comparison") {
		t.Fatalf("the older proposal is not marked as an earlier one:\n%s", joined)
	}
	if line := app.alignLine(); !strings.Contains(line, "c.m3u8") {
		t.Fatalf("the status bar names another source for the failure: %q", line)
	}
}

// TestAFailedComparisonAloneStillShowsSomething covers the first attempt.
func TestAFailedComparisonAloneStillShowsSomething(t *testing.T) {
	app := alignApp()
	app.lastProbeErr = "the two sources could not be decoded"
	lines := app.alignLines()
	if len(lines) != 1 || !strings.Contains(lines[0], "did not finish") {
		t.Fatalf("a single failed comparison shows %q", strings.Join(lines, "\n"))
	}
}

// TestASupersededComparisonDoesNotBecomeTheAccount pins the attempt number: a
// result that finished after a newer comparison started must be dropped, not
// written over the newest attempt.
func TestASupersededComparisonDoesNotBecomeTheAccount(t *testing.T) {
	app := alignApp()
	app.probeAttempt = 2
	app.probeDone = 1
	// Attempt 1 finishes late with a proposal; it must not be published.
	app.applyProbeResult(1, suggestionFor("a.m3u8", "b.m3u8"), nil,
		alignWindow{}, nil, alignWindow{}, nil)
	if hasProbe(app.lastProbe) {
		t.Fatal("an earlier attempt's proposal became the session's account")
	}
	if app.probeRunning {
		t.Fatal("an earlier attempt cleared the in-flight flag")
	}
	// The attempt the session is waiting for publishes normally.
	app.applyProbeResult(2, suggestionFor("a.m3u8", "b.m3u8"), nil,
		alignWindow{Index: 0, Source: "source-1", Track: "track-2", URI: "a.m3u8"}, nil,
		alignWindow{Index: 1, Source: "source-2", Track: "track-3", URI: "b.m3u8"}, nil)
	if !hasProbe(app.lastProbe) {
		t.Fatal("the newest attempt's proposal was dropped")
	}
	if app.probeDone != 2 {
		t.Fatalf("probeDone = %d, want 2", app.probeDone)
	}
}
