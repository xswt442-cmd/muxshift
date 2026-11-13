package main

// P16.4f-b: the prepared half of entering replay. These tests run the real
// transaction the seek path uses against a live stage: one plan, one set of
// cuts, one settlement, one commit. They check that every source moves in the
// same step, that a failure leaves the live stage exactly as it was, that the
// commit confirms the frozen entry plan rather than a replay seat that does not
// exist yet, and that an entry cannot run while another transaction is in
// flight.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// enterReplaySession is the seek fixture turned back into a live stage: each
// source still has its buffer and its measured clock, but no recording and no
// replay seat. The anchor the runner would have measured is installed by hand,
// because the measurement itself is exercised elsewhere.
func enterReplaySession(t *testing.T) (replaySeekState, *[]*fakeSeekPlayer) {
	t.Helper()
	session, opened := seekTxnFixture(t)
	for _, it := range session.items {
		runner := session.app.runnerFor(it)
		if runner == nil {
			t.Fatalf("%s has no runner to take a clock from", it.source.ID)
		}
		point, err := runner.buffer.CurrentSegment()
		if err != nil {
			t.Fatalf("%s: the buffer has no current segment: %v", it.source.ID, err)
		}
		anchor := *it.replayAnchor
		done := make(chan struct{})
		close(done)
		runner.anchorMu.Lock()
		runner.attempt = &anchorAttempt{point: point, done: done, anchor: &anchor}
		runner.anchorMu.Unlock()

		it.replay = nil
		it.replayAnchor = nil
		it.replayAnchorErr = nil
		it.replayLease = nil
	}
	return session, opened
}

func TestEnterReplayTxnCommitsEverySource(t *testing.T) {
	session, opened := enterReplaySession(t)

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != len(session.items) {
		t.Fatalf("opened %d players, want %d", len(*opened), len(session.items))
	}
	for _, it := range session.items {
		if it.replay == nil {
			t.Fatalf("%s still has no recording after the commit", it.source.ID)
		}
		if it.replayLease == nil {
			t.Fatalf("%s has no lease on the file it plays", it.source.ID)
		}
		if it.replayAnchor == nil {
			t.Fatalf("%s has no measured anchor after the commit", it.source.ID)
		}
		if it.generation != 3 {
			t.Fatalf("%s: generation = %d, want 3", it.source.ID, it.generation)
		}
	}
	line := session.app.notice.Line()
	if !strings.Contains(line, "enter replay") || !strings.Contains(line, "committed") {
		t.Fatalf("the account does not describe an entry that committed: %s", line)
	}
	if !strings.Contains(line, "position settled, frame not confirmed") {
		t.Fatalf("the account claims more than a settled position: %s", line)
	}
}

func TestEnterReplayTxnLeavesTheStageAloneWhenAPlayerCannotOpen(t *testing.T) {
	session, _ := enterReplaySession(t)
	inner := session.app.openSeek
	opens := 0
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opens++
		if opens == 2 {
			return nil, fmt.Errorf("the engine refused the file")
		}
		return inner(recording)
	}

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused before it started: %v", err)
	}
	waitSeekTxn(t, session.app)

	for _, it := range session.items {
		if it.replay != nil || it.replayLease != nil {
			t.Fatalf("%s moved to a replay file even though the entry failed", it.source.ID)
		}
		if it.generation != 2 {
			t.Fatalf("%s: generation = %d, want the unchanged 2", it.source.ID, it.generation)
		}
	}
	line := session.app.notice.Line()
	if !strings.Contains(line, "rolled back") {
		t.Fatalf("the account does not say the entry rolled back: %s", line)
	}
	if !strings.Contains(line, "the engine refused the file") {
		t.Fatalf("the account does not name the failure: %s", line)
	}
}

func TestEnterReplayTxnConfirmsThePlanRatherThanAReplaySeat(t *testing.T) {
	session, _ := enterReplaySession(t)
	// Move the source after settlement but before the commit: the frozen plan
	// no longer describes the stage, so the commit has to refuse. A seek would
	// say the player was replaced while its recording was prepared; an entry
	// has to say the player was replaced after the entry was planned.
	session.app.beforeSeekCommit = func() {
		session.items[0].generation++
	}

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused before it started: %v", err)
	}
	waitSeekTxn(t, session.app)

	line := session.app.notice.Line()
	if !strings.Contains(line, "rolled back") {
		t.Fatalf("the account does not say the entry rolled back: %s", line)
	}
	if !strings.Contains(line, "after the entry was planned") {
		t.Fatalf("the commit did not confirm the entry plan: %s", line)
	}
	if session.items[1].replay != nil {
		t.Fatal("the second source was installed even though the first one moved")
	}
}

func TestEnterReplayTxnRefusesASecondRequest(t *testing.T) {
	session, _ := enterReplaySession(t)
	release := make(chan struct{})
	opened := make(chan struct{}, 4)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened <- struct{}{}
		<-release
		return inner(recording)
	}

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("the first entry did not start: %v", err)
	}
	<-opened
	err := session.app.startEnterReplayTxn(session.items, 0, false)
	if err == nil {
		t.Fatal("a second entry started while the first was still preparing")
	}
	if !strings.Contains(err.Error(), "already being prepared") {
		t.Fatalf("the refusal did not explain itself: %v", err)
	}
	close(release)
	waitSeekTxn(t, session.app)
}

func TestEnterReplayTxnUsesTheSharedWindowStart(t *testing.T) {
	session, _ := enterReplaySession(t)

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused: %v", err)
	}
	waitSeekTxn(t, session.app)

	line := session.app.notice.Line()
	// Both fixtures buffer from content zero, so the first whole second both
	// windows hold is zero; the plan must take that instead of inventing a
	// target further in.
	if got := session.app.notice.Event; got != 0 {
		t.Fatalf("entry target = %s, want the shared window start 0s", got)
	}
	if !strings.Contains(line, "committed") {
		t.Fatalf("the account does not describe an entry that committed: %s", line)
	}
}
