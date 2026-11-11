// P16.4b interleaving: the single-source switch and the multi-source replay
// transaction both take recordings, players and leases from the same buffers.
// This scenario runs the two orders that matter on a real window:
//
//  1. a single-source switch commits while a transaction is preparing: the
//     transaction must be refused, and the switch must stay on the stage;
//  2. a transaction commits while a switch is still waiting for its picture:
//     the switch must give way, close what it opened and release its lease once.
//
// A third part aborts a transaction while it owns an open player, the way
// shutting the session down does, and checks the resources go back exactly once.
package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

// runInterleave is the swap/seek interleaving check.
func runInterleave(window *mygo.Window, a *appState, dir string) {
	go interleaveBody(window, a, dir)
}

func interleaveBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "interleave"
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
		name := "interleave-" + shortName(index)
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
	}
	step(window, a, dir, scenario, "", "confirm both sources and show the first", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
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
	}
	time.Sleep(time.Second)
	mygoRunOnMain(func() {
		if err := a.anchor(0); err != nil {
			logf(scenario, "anchor source 1: %v", err)
		}
		// The second source needs an origin of its own before the adapter will
		// read it: the fixtures carry no content offset, so a confirmed zero is
		// the honest statement here.
		if note, err := a.nudge(1, 0); err != nil {
			logf(scenario, "nudge source 2: %v", err)
		} else {
			logf(scenario, "source 2 placed: %s", note)
		}
	})

	first, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	start, _ := a.leaseStats()
	firstGen := runOnMainValue(func() int { return first.generation })
	logf(scenario, "starting from generation %d with %d lease(s) open", firstGen, start)
	// Let both sources play for a moment: a replacement player needs a few frames
	// before it accepts a position.
	time.Sleep(2 * time.Second)

	// 1. A single-source switch commits while the transaction is preparing. The
	// hook runs after every replacement is ready and before the commit step, so
	// the switch is committed by the time the transaction re-reads the item.
	a.beforeSeekCommit = func() {
		logf(scenario, "order 1: a single-source switch commits while the transaction waits")
		it, err := a.at(0)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[0], 2*time.Second); err != nil {
			logf(scenario, "order 1: start the switch: %v", err)
			return
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			gen := runOnMainValue(func() int { return it.generation })
			if gen != firstGen {
				logf(scenario, "order 1: the switch committed at generation %d", gen)
				return
			}
			if !time.Now().Before(deadline) {
				logf(scenario, "order 1: the switch did not commit")
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	err = a.startSeekTxn(a.items, 7*time.Second)
	check(err == nil, "order 1: the transaction did not start: %v", err)
	waitSeekGone(a, 60*time.Second)
	a.beforeSeekCommit = nil

	genNow := runOnMainValue(func() int { return first.generation })
	check(genNow == firstGen+1, "order 1: generation = %d, want only the switch's %d", genNow, firstGen+1)
	recording := runOnMainValue(func() *time.Duration {
		if first.replay == nil {
			return nil
		}
		at := first.replay.StartBufferTime
		return &at
	})
	if recording == nil {
		check(false, "order 1: the source lost its recording")
	} else {
		check(*recording == 2*time.Second, "order 1: the source shows a recording cut at %s, want the switch's 2s", sceneClock(*recording))
	}
	open, _ := a.leaseStats()
	check(open == start, "order 1: %d lease(s) open, want the %d the run started with", open, start)

	// 2. A transaction commits while a single-source switch is still waiting for
	// its picture. The switch gives way: it closes what it opened and gives its
	// lease back exactly once.
	second, err := a.at(1)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	secondGen := runOnMainValue(func() int { return second.generation })
	beforeSecond, _ := a.leaseStats()
	a.replayStarted = func(*ui.VideoPlayer) bool { return false }
	if err := a.playRecording(second, runners[1], 2*time.Second); err != nil {
		logf(scenario, "order 2: start the waiting switch: %v", err)
		return
	}
	logf(scenario, "order 2: a switch waits for a picture while the transaction runs")
	err = a.startSeekTxn(a.items, 9*time.Second)
	check(err == nil, "order 2: the transaction did not start: %v", err)
	waitSeekGone(a, 60*time.Second)
	// Give the waiting switch a moment to notice the newer generation.
	time.Sleep(2 * time.Second)
	a.replayStarted = nil

	secondNow := runOnMainValue(func() int { return second.generation })
	check(secondNow == secondGen+1, "order 2: generation = %d, want only the transaction's %d", secondNow, secondGen+1)
	secondCut := runOnMainValue(func() *time.Duration {
		if second.replay == nil {
			return nil
		}
		at := second.replay.StartBufferTime
		return &at
	})
	if secondCut == nil {
		check(false, "order 2: the source lost its recording")
	} else {
		check(*secondCut == 8*time.Second, "order 2: the source shows a recording cut at %s, want the transaction's 8s", sceneClock(*secondCut))
	}
	open, _ = a.leaseStats()
	check(open == beforeSecond, "order 2: %d lease(s) open, want the %d before the transaction", open, beforeSecond)

	// 3. Aborting a transaction that already owns an open player gives the file
	// back exactly once; this is the path shutdown takes.
	beforeThird, _ := a.leaseStats()
	thirdGen := runOnMainValue(func() int { return first.generation })
	err = a.startSeekTxn(a.items, 11*time.Second)
	check(err == nil, "order 3: the transaction did not start: %v", err)
	txn := awaitSeekTxn(a, 20*time.Second)
	if txn == nil {
		logf(scenario, "order 3: the transaction never appeared")
	} else {
		a.abortSeekTxn(txn, "the session is closing")
		waitSeekGone(a, 30*time.Second)
		open, _ = a.leaseStats()
		check(open == beforeThird, "order 3: %d lease(s) open after the abort, want %d", open, beforeThird)
		check(runOnMainValue(func() int { return first.generation }) == thirdGen, "order 3: the aborted transaction changed a generation")
	}
	step(window, a, dir, scenario, "1-after-interleaving.png", "the stage after both orders", func(a *appState) {
		a.showVideo(0)
	})
	logf(scenario, "checks failed: %d", failures)
}

// waitSeekGone waits until no transaction is in flight.
func waitSeekGone(a *appState, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running == nil {
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitSeekTxn returns the transaction in flight, waiting for one to appear.
func awaitSeekTxn(a *appState, timeout time.Duration) *seekTxn {
	deadline := time.Now().Add(timeout)
	for {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running != nil {
			return running
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}
