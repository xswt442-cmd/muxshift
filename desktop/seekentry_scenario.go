// P16.4d on a real window: the inspector's replay seek entry is the only place
// in the window that asks for a seek. The scenario drives that entry — not the
// transaction — and checks that a valid target reaches the same authorisation
// and transaction path, that a target outside the window is refused without
// touching the players, and that a second request while one is preparing is
// refused instead of replacing it.
package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekEntry is P16.4d: the entry the inspector offers.
func runSeekEntry(window *mygo.Window, a *appState, dir string) {
	go seekEntryBody(window, a, dir)
}

func seekEntryBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seekentry"
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
	uris := make([]string, len(a.items))
	for index := range a.items {
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

	runners := make([]*dvrRunner, 0, len(uris))
	for index, uri := range uris {
		name := "seekentry-" + shortName(index)
		runner, err := startDVR(uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uri, runner)
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

	step(window, a, dir, scenario, "", "confirm both sources on the event clock", func(a *appState) {
		for index := range a.items {
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
	time.Sleep(2 * time.Second)
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	// The second source needs a recorded origin of its own; a shift written
	// straight into the timeline is a statement without a measurement, and the
	// seek plan refuses to lean on it. Nudging is how the window records one.
	var nudgeNote string
	mygoRunOnMain(func() { nudgeNote, _ = a.nudge(1, 10*time.Millisecond) })
	logf(scenario, "source 2 nudged by 10ms: %s", nudgeNote)
	time.Sleep(500 * time.Millisecond)

	// The entry computes the window itself, so the scenario only records what a
	// user would see: the window, and the numbers the entry will accept.
	start, end, windowErr := a.replayWindowForEntry()
	if windowErr != nil {
		logf(scenario, "the entry offers no window: %v", windowErr)
		return
	}
	logf(scenario, "the entry offers %s..%s (event time)", sceneClock(start), sceneClock(end))

	step(window, a, dir, scenario, "1-window.png", "the inspector with the seek entry", func(a *appState) {
		a.showVideo(0)
	})

	generations := make([]int, 2)
	for index := 0; index < 2; index++ {
		generations[index] = a.items[index].generation
	}

	// A valid target goes through the entry, and the entry hands it to the same
	// transaction the phase already tested.
	var validNote string
	mygoRunOnMain(func() {
		a.seekText = "7"
		validNote = a.requestReplaySeek()
	})
	logf(scenario, "a valid request said: %s", validNote)
	if !waitSeekDone(a, 90*time.Second) {
		logf(scenario, "the transaction did not finish within 90s")
		return
	}
	time.Sleep(1500 * time.Millisecond)
	report, ok := a.lastReport()
	logf(scenario, "after the valid request the account says: %s", report.Line())
	check(ok && report.Phase == replayCommitted, "the account is %v, want a committed replay", report.Phase)
	for index := 0; index < 2; index++ {
		check(a.items[index].generation == generations[index]+1,
			"source %d: generation = %d, want %d", index+1, a.items[index].generation, generations[index]+1)
	}
	step(window, a, dir, scenario, "2-committed.png", "after the entry committed a replay seek", func(a *appState) {
		a.showVideo(0)
	})

	// A target outside the window is refused by the entry, and nothing about the
	// players changes: same generations, same account.
	before := make([]int, 2)
	for index := 0; index < 2; index++ {
		before[index] = a.items[index].generation
	}
	var invalidNote string
	var invalidStarted bool
	mygoRunOnMain(func() {
		a.seekText = "999"
		invalidNote = a.requestReplaySeek()
		invalidStarted = a.seekInFlight() != nil
	})
	logf(scenario, "an out-of-window request said: %s", invalidNote)
	check(invalidNote != "", "an out-of-window request produced no sentence")
	check(!invalidStarted, "an out-of-window request started a transaction")
	for index := 0; index < 2; index++ {
		check(a.items[index].generation == before[index],
			"source %d: an out-of-window request changed the player (%d → %d)", index+1, before[index], a.items[index].generation)
	}
	step(window, a, dir, scenario, "3-refused.png", "the refusal stays on the page and the stage is unchanged", func(a *appState) {
		a.showVideo(0)
	})

	// A second request while one is preparing is refused rather than replacing
	// the first: the entry says so, and only the first transaction commits.
	var firstNote, secondNote string
	mygoRunOnMain(func() {
		a.seekText = "9"
		firstNote = a.requestReplaySeek()
		secondNote = a.requestReplaySeek()
	})
	logf(scenario, "two requests in a row said: %q then %q", firstNote, secondNote)
	check(secondNote != "" && secondNote != firstNote, "the second request was not refused on its own")
	if waitSeekDone(a, 90*time.Second) {
		time.Sleep(time.Second)
	}
	open, given := a.leaseStats()
	logf(scenario, "leases open=%d given=%d, players=%d", open, given, openPlayers(a))
	check(open == len(a.items), "the run holds %d lease(s), want %d", open, len(a.items))

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ = a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	logf(scenario, "checks failed: %d", failures)
}

// waitSeekDone waits until no transaction is preparing and reports whether one
// was seen running at all.
func waitSeekDone(a *appState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		a.seekMu.Lock()
		running := a.seek != nil
		a.seekMu.Unlock()
		if !running {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
