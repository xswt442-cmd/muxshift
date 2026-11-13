//go:build windows

package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekBoundary drives two committed seeks through the inspector entry: one
// at the start of the file the transaction cuts for it, and one after it. Both
// used to be out of reach while the stage was not drawing; with the draw
// schedule in place the boundary target settles and the player is replaced, so
// the scenario records that the entry can cross the cut.
func runSeekBoundary(window *mygo.Window, a *appState, dir string) {
	go seekBoundaryBody(window, a, dir, "seekboundary")
}

func seekBoundaryBody(window *mygo.Window, a *appState, dir, scenario string) {
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
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "0-window.png", "confirm the source on the event clock", func(a *appState) {
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
	time.Sleep(time.Second)

	before := flight.it.generation
	var note string
	mygoRunOnMain(func() {
		a.seekText = "0"
		note = a.requestReplaySeek()
	})
	logf(scenario, "a request at the window start said: %s", note)
	if !waitSeekDone(a, 120*time.Second) {
		logf(scenario, "the boundary transaction did not finish within 120s")
		return
	}
	time.Sleep(1500 * time.Millisecond)
	boundary, ok := a.lastReport()
	logf(scenario, "the boundary account says: %s", boundary.Line())
	// A target at the cut settles while the transaction keeps drawing the
	// replacement player; if its first request is dropped the transaction
	// refuses it. Both are consistent states, and the next request has to work
	// either way.
	switch {
	case ok && boundary.Phase == replayCommitted:
		logf(scenario, "the seek at the cut committed")
		check(strings.Contains(boundary.Line(), "requested 0s reported 0s"),
			"the boundary account does not say the player reported position zero: %s", boundary.Line())
		check(flight.it.generation == before+1,
			"the boundary left the player at generation %d, want %d", flight.it.generation, before+1)
	case ok && boundary.Phase == replayRolledBack:
		logf(scenario, "the seek at the cut was refused without switching")
		check(flight.it.generation == before,
			"a refused boundary request changed the player (%d → %d)", before, flight.it.generation)
	default:
		check(false, "the boundary request ended as %v without an account", boundary.Phase)
	}
	base := flight.it.generation
	step(window, a, dir, scenario, "1-boundary.png", "the seek at the cut shows the replay", func(a *appState) {
		a.showVideo(0)
	})

	var nextNote string
	mygoRunOnMain(func() {
		a.seekText = "5"
		nextNote = a.requestReplaySeek()
	})
	logf(scenario, "the request after the boundary said: %s", nextNote)
	if !waitSeekDone(a, 90*time.Second) {
		logf(scenario, "the second transaction did not finish within 90s")
		return
	}
	time.Sleep(time.Second)
	next, ok := a.lastReport()
	logf(scenario, "the second account says: %s", next.Line())
	check(ok && next.Phase == replayCommitted, "the second account is %v, want a committed replay", next.Phase)
	check(flight.it.generation == base+1,
		"the second seek left the player at generation %d, want %d", flight.it.generation, base+1)
	step(window, a, dir, scenario, "2-next.png", "the second target commits and shows the replay", func(a *appState) {
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
