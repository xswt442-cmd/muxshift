package main

import (
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// waitForReport waits until a report of the wanted phase has been published.
func waitForReport(t *testing.T, app *appState, phase replayPhase) replayReport {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		report, ok := app.lastReport()
		if ok && report.Phase == phase {
			return report
		}
		time.Sleep(20 * time.Millisecond)
	}
	report, _ := app.lastReport()
	t.Fatalf("no %s report was published; the newest is %s", phase, report.Line())
	return replayReport{}
}

// TestCommittedReportPublishesPositionsButNotAFrame keeps the two meanings
// apart: a committed transaction may publish the positions its players
// reported, and may never publish a confirmed frame.
func TestCommittedReportPublishesPositionsButNotAFrame(t *testing.T) {
	session, _ := seekTxnFixture(t)
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	report := waitForReport(t, session.app, replayCommitted)
	if !report.Published() {
		t.Fatal("a committed report did not publish")
	}
	if report.FrameConfirmed() {
		t.Fatal("a committed report claimed a confirmed frame")
	}
	if report.Event != 7*time.Second {
		t.Fatalf("the report names event %s, want 7s", report.Event)
	}
	if len(report.Sources) != 2 {
		t.Fatalf("the report covers %d sources, want 2", len(report.Sources))
	}
	for _, source := range report.Sources {
		if !source.Settled {
			t.Fatalf("%s has no settled position in a committed report", source.Source)
		}
		if source.Residual != source.Reported-source.Requested {
			t.Fatalf("%s residual %s does not follow requested %s and reported %s",
				source.Source, source.Residual, source.Requested, source.Reported)
		}
	}
	if line := report.Line(); !strings.Contains(line, "position settled, frame not confirmed") {
		t.Fatalf("the committed line hides what it does not know: %s", line)
	}
}

// TestRefusedReportExplainsItself covers a request that never became a
// transaction: it is a refusal with a reason, not a silent no-op.
func TestRefusedReportExplainsItself(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.sync.origins = map[core.TrackID]origin{}
	err := session.app.startSeekTxn(session.items, 7*time.Second)
	if err == nil {
		t.Fatal("a request without a recorded origin was accepted")
	}
	report := waitForReport(t, session.app, replayRefused)
	if report.Published() {
		t.Fatal("a refused request published as a success")
	}
	if !strings.Contains(report.Reason, "origin") {
		t.Fatalf("the refusal does not say why: %q", report.Reason)
	}
}

// TestRolledBackReportKeepsItsMeasurementsButNotSuccess is the case the phase
// exists for: the players were measured, then the commit was refused. The
// numbers stay, the success does not.
func TestRolledBackReportKeepsItsMeasurementsButNotSuccess(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.beforeSeekCommit = func() {
		session.items[1].generation++
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	report := waitForReport(t, session.app, replayRolledBack)
	if report.Published() {
		t.Fatal("a rolled back transaction published as a success")
	}
	measured := 0
	for _, source := range report.Sources {
		if source.Settled {
			measured++
		}
	}
	if measured == 0 {
		t.Fatal("the rolled back report dropped the positions it had measured")
	}
	if line := report.Line(); strings.Contains(line, "position settled, frame not confirmed") {
		t.Fatalf("a rolled back line reads like success: %s", line)
	}
}

// TestReportIsASnapshot makes sure a caller cannot edit the session's account.
func TestReportIsASnapshot(t *testing.T) {
	session, _ := seekTxnFixture(t)
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	waitForReport(t, session.app, replayCommitted)

	first, _ := session.app.lastReport()
	if len(first.Sources) == 0 {
		t.Fatal("the committed report has no sources")
	}
	first.Sources[0].Reported = 99 * time.Second
	second, _ := session.app.lastReport()
	if second.Sources[0].Reported == 99*time.Second {
		t.Fatal("editing a returned report changed the session's account")
	}
}
