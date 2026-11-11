// P16.4b-2 on a real window: two sources already replaying a recording, a
// confirmed calibration, and one transaction that replaces both recordings and
// both players for a later event time. It shows the resources are prepared
// before anything is committed, and that the commit leaves the session holding
// exactly the new recordings.
package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekTxn is P16.4b-2: the replay transaction, on a window.
func runSeekTxn(window *mygo.Window, a *appState, dir string) {
	go seekTxnBody(window, a, dir)
}

func seekTxnBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seektxn"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", index+1, shortURI(it.uri))
			return
		}
		uris[index] = it.uri
	}

	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := "seektxn-" + shortName(index)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for index, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", index+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 90s", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		logf(scenario, "source %d buffers %s", index+1, logRange(windowOf))
	}

	// Put both sources on the event clock before a recording exists: the
	// content offset between the fixtures is zero, so a confirmed zero is the
	// honest statement.
	step(window, a, dir, scenario, "", "confirm both sources on the event clock", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.showVideo(0)
		a.apply()
	})

	// Both sources must already be replaying: the transaction plans against the
	// recording an item shows, and a live item is refused.
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[index], 0); err != nil {
			logf(scenario, "source %d: start its first recording: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording cut at 0s", index+1)
			return
		}
		logf(scenario, "source %d replays a recording cut at 0s", index+1)
	}
	time.Sleep(2 * time.Second)

	// Calibrate against the buffer the player is really reading, which is what
	// a seek plan may lean on.
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	logf(scenario, "source 1 is the reference; its tracks are placed on its buffer clock")
	var nudgeNote string
	mygoRunOnMain(func() {
		nudgeNote, _ = a.nudge(1, 10*time.Millisecond)
	})
	logf(scenario, "source 2 nudged by 10ms: %s", nudgeNote)
	time.Sleep(500 * time.Millisecond)

	_, givenBefore := a.leaseStats()
	before := make([]int, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		before[index] = it.generation
	}

	// The target is 7s while the fixtures' segments are 2s long, so the
	// recordings are cut at 6s and the target sits 1s into each file. A run
	// that only asked for the start of a file would land one second short.
	target := 7 * time.Second
	wantOffset := time.Second
	if err := a.startSeekTxn(a.items, target); err != nil {
		logf(scenario, "start the replay transaction: %v", err)
		return
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running == nil {
			break
		}
		if !time.Now().Before(deadline) {
			logf(scenario, "the transaction did not finish within 90s")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)

	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		check(it.generation == before[index]+1, "source %d: generation = %d, want %d", index+1, it.generation, before[index]+1)
		check(it.replay != nil, "source %d: no recording after the transaction", index+1)
		if it.replay != nil {
			logf(scenario, "source %d now shows a recording cut at %s", index+1, sceneClock(it.replay.StartBufferTime))
		}
		check(it.player != nil, "source %d: no player after the transaction", index+1)
		if it.player == nil {
			continue
		}
		snapshot := runOnMainValue(func() seekFrame { return seekFrame{position: it.player.Position(), ready: it.player.Ready()} })
		logf(scenario, "source %d reports %s with the target at %s into its file (a position, not a confirmed frame)",
			index+1, sceneClock(snapshot.position), sceneClock(wantOffset))
		check(snapshot.position >= wantOffset, "source %d: the player reports %s, before the target at %s", index+1, sceneClock(snapshot.position), sceneClock(wantOffset))
		check(snapshot.position < wantOffset+5*time.Second, "source %d: the player ran to %s, well past the target at %s", index+1, sceneClock(snapshot.position), sceneClock(wantOffset))
		check(snapshot.ready, "source %d: the player is not ready after the transaction", index+1)
	}
	open, given := a.leaseStats()
	logf(scenario, "leases open=%d given=%d, players=%d", open, given, openPlayers(a))
	check(open == 2, "the transaction left %d lease(s) open, want 2", open)
	check(given == givenBefore+2, "the transaction took %d lease(s), want %d more than the %d it started with", given, 2, givenBefore)

	step(window, a, dir, scenario, "1-committed.png", "the stage after the transaction committed", func(a *appState) {
		a.showVideo(0)
	})

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ = a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	logf(scenario, "checks failed: %d", failures)
}

// shortName keeps the fixture directory names distinct without repeating the
// index arithmetic at each call.
func shortName(index int) string {
	if index == 0 {
		return "one"
	}
	return "two"
}
