package main

import (
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
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

// TestReportKeepsAZeroPositionAsEvidence covers the case where the player
// really reports zero: a small target inside the tolerance is a measurement,
// and the report must keep it as one instead of reading the zero as "nothing
// was measured".
func TestReportKeepsAZeroPositionAsEvidence(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		return &fakeSeekPlayer{name: recording.FileName(), ready: true}, nil
	}
	if err := session.app.startSeekTxn(session.items, 10*time.Millisecond); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	report := waitForReport(t, session.app, replayCommitted)
	if !report.Published() || report.FrameConfirmed() {
		t.Fatalf("a committed report published=%v frame=%v", report.Published(), report.FrameConfirmed())
	}
	for _, source := range report.Sources {
		if !source.Settled {
			t.Fatalf("%s reported a real zero position and the report dropped it", source.Source)
		}
		if source.Requested != 10*time.Millisecond || source.Reported != 0 || source.Residual != -10*time.Millisecond {
			t.Fatalf("%s requested %s reported %s residual %s, want 10ms/0s/-10ms",
				source.Source, source.Requested, source.Reported, source.Residual)
		}
	}
	if line := report.Line(); !strings.Contains(line, "reported 0s (residual -10ms)") {
		t.Fatalf("the line lost the millisecond evidence: %s", line)
	}
}

// TestLastReportDoesNotRegressToAnOlderAttempt holds the newest attempt even
// when an older transaction publishes after it.
func TestLastReportDoesNotRegressToAnOlderAttempt(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.publishReport(replayReport{ID: 2, Event: 7 * time.Second, Phase: replayRefused, Reason: "outside the buffer"})
	session.app.publishReport(replayReport{ID: 1, Event: 7 * time.Second, Phase: replayCommitted})

	report, ok := session.app.lastReport()
	if !ok {
		t.Fatal("no report was kept")
	}
	if report.ID != 2 || report.Phase != replayRefused {
		t.Fatalf("the account went back to attempt %d (%s), want 2 (refused)", report.ID, report.Phase)
	}
}

func TestTheDockSaysOnlyWhatTheAccountKnows(t *testing.T) {
	app := &appState{}
	app.publishReport(replayReport{ID: 1, Event: time.Second, Phase: replayRefused, Reason: "no origin"})
	lines := strings.Join(app.noticeLines(), "\n")
	if !strings.Contains(lines, "refused") || !strings.Contains(lines, "no origin") {
		t.Fatalf("a refused account does not say why: %q", lines)
	}
	if strings.Contains(lines, "position settled") {
		t.Fatalf("a refused account claims a settled position: %q", lines)
	}

	app.publishReport(replayReport{ID: 2, Event: time.Second, Phase: replayCommitted, Sources: []replayReportSource{
		{Source: "source-1", Requested: 10 * time.Millisecond, Reported: 0, Residual: -10 * time.Millisecond, Settled: true},
	}})
	lines = strings.Join(app.noticeLines(), "\n")
	if !strings.Contains(lines, "reported 0s (residual -10ms)") {
		t.Fatalf("the dock does not keep a real zero position: %q", lines)
	}
	if !strings.Contains(lines, "the frame is not confirmed") {
		t.Fatalf("a committed account does not say the frame is unconfirmed: %q", lines)
	}

	app.publishReport(replayReport{ID: 3, Event: time.Second, Phase: replayRolledBack, Reason: "the player moved", Sources: []replayReportSource{
		{Source: "source-1", Requested: time.Second, Reported: time.Second, Settled: true},
	}})
	lines = strings.Join(app.noticeLines(), "\n")
	if strings.Contains(lines, "position settled") || !strings.Contains(lines, "rolled back") {
		t.Fatalf("a rolled-back account reads as a success: %q", lines)
	}
	if !strings.Contains(lines, "source-1: asked 1s, reported 1s") {
		t.Fatalf("a rolled-back account lost its measurements: %q", lines)
	}
}
