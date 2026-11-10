package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// runSwap checks the replay transaction of desktop/replay_swap.go: a recording
// reaches the stage from a lease, a failed or timed-out switch leaves the picture
// and the old lease alone, a burst of switches ends on the last one, and a close
// during a switch gives every lease back.
//
// The run records a real buffer and logs, for every case, what the item shows,
// how many leases are open, and what the buffer believes it still owns. The
// player seams stand in for a player that cannot open a file and for one that
// never gets a picture; the successful path uses the real player and captures the
// stage twice so the burnt-in timecode says the picture is the recording's.
func runSwap(window *mygo.Window, a *appState, dir string) {
	// The body sleeps while it waits for a recording to reach the picture. Run
	// it off the drawing thread: MyGo dispatches scenarios inside WhenReady, and
	// a body that slept there would hold the frame loop while its own
	// runOnMain calls wait for that same loop.
	go swapBody(window, a, dir)
}

func swapBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "swap"
	// The numbers below are the evidence, so a wrong one fails the run instead
	// of only reading oddly in a log nobody re-adds up.
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source to buffer, got %s", shortURI(it.uri))
		return
	}
	bufferDir := filepath.Join(dir, "segments")
	recordingsDir := filepath.Join(dir, "recordings")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, recordingsDir, 40*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()

	windowOf, ok := waitForRange(runner, 12*time.Second, 60*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 12s of media within 60s")
		return
	}
	logf(scenario, "the buffer holds %s; %s", logRange(windowOf), runner.budgetLine())
	base := windowOf.Start

	// 1. A recording reaches the stage through a lease.
	if err := a.playRecording(it, runner, base+2*time.Second); err != nil {
		logf(scenario, "first switch: %v", err)
		return
	}
	if !waitRecording(a, 0, base+2*time.Second, 15*time.Second) {
		logf(scenario, "the first recording never reached the stage")
		return
	}
	open, given := a.leaseStats()
	logf(scenario, "the recording is on the stage: %s | leases open=%d given=%d", replayLine(it), open, given)
	check(open == 1 && given == 1, "the first switch asked for %d lease(s) and left %d open", given, open)
	check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
		"the stage shows %v, not the recording asked for at %s", showing(it), sceneClock(base+2*time.Second))
	step(window, a, dir, scenario, "1-replay.png", "the recording on the stage", nil)
	first := positionOf(a, 0)
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "2-replay-later.png", "the same recording two seconds later", nil)
	logf(scenario, "the picture moved: position %s -> %s (advanced=%v), %s",
		sceneClock(first), sceneClock(positionOf(a, 0)), moved(first, positionOf(a, 0)), replayState(a, 0))

	// 2. A player that cannot open a file changes nothing.
	a.openReplay = func(path string) (*ui.VideoPlayer, error) {
		return nil, fmt.Errorf("stand-in: refusing to open %s", filepath.Base(path))
	}
	errOpen := a.playRecording(it, runner, base+4*time.Second)
	a.openReplay = nil
	open, given = a.leaseStats()
	logf(scenario, "open failure: %v | still showing %v | leases open=%d given=%d",
		errOpen, replayLine(it), open, given)
	check(errOpen != nil, "a player that cannot open the file reported no error")
	check(open == 1, "a failed open left %d lease(s) open", open)
	check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
		"a failed open changed the picture to %v", showing(it))

	// 3. A player that never shows a picture is rolled back and its file given
	// back; the picture and the lease the item already had stay.
	oldWait := handoverWait
	handoverWait = 1200 * time.Millisecond
	a.replayStarted = func(*ui.VideoPlayer) bool { return false }
	errLate := a.playRecording(it, runner, base+6*time.Second)
	time.Sleep(2 * time.Second)
	a.replayStarted = nil
	handoverWait = oldWait
	open, given = a.leaseStats()
	logf(scenario, "timeout switch: err=%v | still showing %v | leases open=%d given=%d",
		errLate, replayLine(it), open, given)
	check(open == 1, "a timed-out switch left %d lease(s) open", open)
	check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
		"a timed-out switch changed the picture to %v", showing(it))

	// 4. A burst of switches ends on the last one, and only its lease is open.
	//
	// A budget that is already full refuses a cut instead of dropping a file a
	// player holds; the run then checks the picture and lease it kept rather
	// than pretending the burst happened.
	burst := true
	last := time.Duration(0)
	for _, offset := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second} {
		if err := a.playRecording(it, runner, base+offset); err != nil {
			logf(scenario, "burst switch at %s refused: %v", sceneClock(base+offset), err)
			burst = false
			break
		}
		last = base + offset
		time.Sleep(250 * time.Millisecond)
	}
	open, given = a.leaseStats()
	if burst {
		if !waitRecording(a, 0, last, 15*time.Second) {
			logf(scenario, "the burst did not settle on the last switch: %v", replayLine(it))
			return
		}
		open, given = a.leaseStats()
		logf(scenario, "the burst settled on %s | leases open=%d given=%d", replayLine(it), open, given)
		check(open == 1, "the burst left %d lease(s) open", open)
		check(showing(it) != nil && showing(it).RequestedBufferTime == last,
			"the burst settled on %v, not the last switch", showing(it))
		step(window, a, dir, scenario, "3-burst.png", "the last switch of a burst", nil)
	} else {
		logf(scenario, "the burst kept %s | leases open=%d given=%d", replayLine(it), open, given)
		check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
			"a refused burst changed the picture to %v", showing(it))
	}

	// 4b. The recording budget drops the snapshots the item has let go of, and
	// never the file the item is holding a lease on.
	shown := showing(it)
	var validErr error
	if shown != nil {
		validErr = shown.Valid()
	}
	logf(scenario, "the budget keeps %d recording(s) of %d bytes; the one on the stage is valid=%v (%v)",
		len(runner.buffer.Recordings()), runner.buffer.RecordingBytes(), validErr == nil, validErr)
	check(shown != nil && validErr == nil, "the recording on the stage is no longer valid: %v", validErr)

	// 5. Closing the item in the middle of a switch still returns every lease
	// exactly once. The player that was showing the recording is paused until
	// another one can take over, so its lease stays open: the file has to remain
	// readable for as long as the player holds it. The replacement, which never
	// became the item's player, is closed and given back by this close alone; a
	// rollback that is still waiting finds that work done.
	held := showing(it)
	handoverWait = 5 * time.Second
	a.replayStarted = func(*ui.VideoPlayer) bool { return false }
	if err := a.playRecording(it, runner, base+8*time.Second); err != nil {
		logf(scenario, "switch before the close: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	a.close(it)
	time.Sleep(500 * time.Millisecond)
	a.replayStarted = nil
	handoverWait = oldWait
	open, given = a.leaseStats()
	var heldErr error
	if held != nil {
		heldErr = held.Valid()
	}
	logf(scenario, "closed in the middle of a switch: leases open=%d given=%d players=%d; the file the paused player reads is valid=%v (%v)",
		open, given, openPlayers(a), heldErr == nil, heldErr)
	check(open == 1, "a close during a switch left %d lease(s) open before the player was really closed", open)
	check(held != nil && heldErr == nil, "the recording of the paused player is no longer valid: %v", heldErr)
	check(openPlayers(a) == 1, "a close during a switch left %d player(s), not the paused one", openPlayers(a))

	// 6. The paused player is closed when nobody else is left, and only then is
	// its lease given back. A lease count of zero and a closed player are two
	// different facts, so both are asserted.
	mygoRunOnMain(func() { a.release() })
	open, given = a.leaseStats()
	logf(scenario, "after shutdown: leases open=%d given=%d players=%d", open, given, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	left := runner.buffer.PendingCleanup()
	recordings := len(runner.buffer.Recordings())
	logf(scenario, "after closing the buffer: pending cleanup=%v recordings=%d", left, recordings)
	check(len(left) == 0, "the buffer could not remove %v", left)
	check(recordings == 0, "the closed buffer still owns %d recording(s)", recordings)
	logf(scenario, "checks failed: %d", failures)
}

// openPlayers counts the players the run still holds open, wherever they live:
// on an item, kept drawn by a handover, or paused while nothing else was left.
func openPlayers(a *appState) int {
	open := 0
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if it.player != nil {
				open++
			}
			if it.previous != nil {
				open++
			}
			if it.handover != nil && it.handover.open() {
				open++
			}
			if it.swap != nil && it.swap.player != nil {
				open++
			}
		}
		a.pendingMu.Lock()
		open += len(a.pending)
		a.pendingMu.Unlock()
	})
	return open
}

// showing returns the recording the item has on the stage, or nil when it shows
// its own source. It asks the drawing thread, which is the thread that owns the
// field.
func showing(it *item) *dvr.Recording {
	var at *dvr.Recording
	mygoRunOnMain(func() { at = it.replay })
	return at
}

// waitRecording waits until the item shows a recording that was asked for at the
// given buffer time.
func waitRecording(a *appState, index int, want time.Duration, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		it, err := a.at(index)
		if err == nil && it.replay != nil && it.replay.RequestedBufferTime == want {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
