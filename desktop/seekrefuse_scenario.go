//go:build windows

package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekRefuse is the regression for a target at the start of the file a
// transaction cuts for it. While the stage keeps drawing the replacement
// player, that target usually settles; when the first request is dropped the
// transaction must refuse it as an execution failure and leave the players
// alone. The scenario follows whichever account the transaction publishes and
// checks that the run stays consistent.
func runSeekRefuse(window *mygo.Window, a *appState, dir string) {
	go seekRefuseBody(window, a, dir, "seekrefuse")
}

func seekRefuseBody(window *mygo.Window, a *appState, dir, scenario string) {
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
	report, ok := a.lastReport()
	logf(scenario, "the account says: %s", report.Line())
	switch {
	case ok && report.Phase == replayCommitted:
		logf(scenario, "the window start settled and committed")
		check(strings.Contains(report.Line(), "requested 0s reported 0s"),
			"the account does not say the players reported position zero: %s", report.Line())
		check(flight.it.generation == before+1,
			"a committed request did not replace the player (%d → %d)", before, flight.it.generation)
		step(window, a, dir, scenario, "1-committed.png", "the target at the window start is on screen", func(a *appState) {
			a.showVideo(0)
		})
	case ok && report.Phase == replayRolledBack:
		logf(scenario, "the window start could not be reported and was refused")
		check(strings.Contains(report.Reason, "0s"),
			"the refusal does not name the target the player missed: %s", report.Reason)
		check(strings.Contains(report.Reason, "source 1"),
			"the refusal does not name the player that missed it: %s", report.Reason)
		check(flight.it.generation == before,
			"a refused request changed the player (%d → %d)", before, flight.it.generation)
		step(window, a, dir, scenario, "1-refused.png", "the refused request leaves the stage alone", func(a *appState) {
			a.showVideo(0)
		})
	default:
		check(false, "the boundary request ended as %v without an account", report.Phase)
	}

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
