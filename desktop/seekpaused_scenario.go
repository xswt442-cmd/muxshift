//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekPaused is the regression for a paused stage: the replacement player
// only produces its first frame while the window draws, and a paused stage
// stops the window from drawing on its own. The transaction keeps asking for a
// frame while it prepares, so the seek settles and commits with the stage left
// paused throughout.
//
// It also records the stage every ~600ms while the transaction prepares, so a
// reader can check that the picture before the commit is the old one and that
// the target frame only appears once the commit is announced.
func runSeekPaused(window *mygo.Window, a *appState, dir string) {
	go seekPausedBody(window, a, dir, "seekpaused")
}

func seekPausedBody(window *mygo.Window, a *appState, dir, scenario string) {
	if len(a.items) == 0 {
		logf(scenario, "the scenario needs a source")
		return
	}
	flight, finish := attachMidFlight(a, dir, scenario)
	if flight == nil {
		return
	}
	defer finish()

	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	if err := a.playRecording(flight.it, flight.runner, 0); err != nil {
		logf(scenario, "play the recording cut at 0s: %v", err)
		return
	}
	if !waitRecording(a, 0, 0, 40*time.Second) {
		logf(scenario, "the recording cut at 0s never showed a picture")
		return
	}
	// The swap that shows the recording commits on its own thread; a request
	// that arrives before it does is refused for the honest reason that the
	// player changed, so wait for it here.
	swapDeadline := time.Now().Add(30 * time.Second)
	for {
		var swapping bool
		mygoRunOnMain(func() { swapping = a.items[0].swap != nil })
		if !swapping {
			break
		}
		if time.Now().After(swapDeadline) {
			logf(scenario, "the swap that shows the recording never finished")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	step(window, a, dir, scenario, "", "confirm the source on the event clock", func(a *appState) {
		for _, track := range []core.TrackID{flight.it.video, flight.it.audio} {
			if track == "" {
				continue
			}
			if err := a.sync.timeline.SetShift(track, 0); err != nil {
				logf(scenario, "align %s: %v", track, err)
			}
		}
		a.showVideo(0)
		a.apply()
	})
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor the source: %v", anchorErr)
		return
	}
	var nudgeNote string
	mygoRunOnMain(func() { nudgeNote, _ = a.nudge(0, 10*time.Millisecond) })
	logf(scenario, "the source was nudged by 10ms: %s", nudgeNote)

	// Settle the stage on a recognisable picture, then pause it there. The
	// target below is several seconds away, so the two frames cannot be
	// confused in a screenshot.
	if _, ok := flight.seekSettle(a, scenario, 3*time.Second); !ok {
		logf(scenario, "the stage did not settle on 3s")
		return
	}
	step(window, a, dir, scenario, "1-paused.png", "pause the stage on a known picture", func(a *appState) {
		if flight.it.player != nil {
			flight.it.player.Pause()
		}
		a.apply()
	})
	var paused bool
	var pausedAt time.Duration
	mygoRunOnMain(func() {
		paused = !flight.it.player.Playing()
		pausedAt = flight.it.player.Position()
	})
	logf(scenario, "the stage is paused=%v at %s", paused, pausedAt)
	check(paused, "the stage was not paused before the request")

	before := flight.it.generation
	var note string
	mygoRunOnMain(func() {
		a.seekText = "9"
		note = a.requestReplaySeek()
	})
	logf(scenario, "the request said: %s", note)

	// Keep the stage on screen while the transaction prepares. The capture is
	// taken on the drawing thread between two polls, so it shows what a viewer
	// would see at that moment rather than a frame staged for the test.
	shots := 0
	deadline := time.Now().Add(30 * time.Second)
	for a.seek != nil && time.Now().Before(deadline) {
		shots++
		name := fmt.Sprintf("during-%02d.png", shots)
		mygoRunOnMain(func() {
			_ = capture(window, filepath.Join(dir, name))
		})
		logf(scenario, "captured %s while the transaction prepares", name)
		time.Sleep(600 * time.Millisecond)
	}
	if !waitSeekDone(a, 120*time.Second) {
		logf(scenario, "the transaction did not finish within 120s")
		return
	}
	time.Sleep(1500 * time.Millisecond)
	report, ok := a.lastReport()
	logf(scenario, "the account says: %s", report.Line())
	check(ok && report.Phase == replayCommitted, "the account is %v, want a committed replay", report.Phase)
	check(flight.it.generation == before+1,
		"a committed request did not replace the player (%d → %d)", before, flight.it.generation)
	check(strings.Contains(report.Line(), "position settled, frame not confirmed"),
		"the account claims more than a settled position: %s", report.Line())
	step(window, a, dir, scenario, "after.png", "the target picture after the commit", func(a *appState) {
		a.showVideo(0)
	})

	open, given := a.leaseStats()
	logf(scenario, "leases open=%d given=%d, players=%d", open, given, openPlayers(a))
	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ = a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	logf(scenario, "checks failed: %d", failures)
}
