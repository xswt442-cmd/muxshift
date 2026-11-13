//go:build windows

package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekRefuse is the regression for the execution refusal alone: a target at
// the start of the file a transaction cuts for it is authorised, and the
// replacement player cannot report the position, so the transaction says so
// and switches nothing. It deliberately asks for no second target; the retry
// after a rolled-back request is REPLAY-RETRY-AFTER-ROLLBACK and is measured
// by the seekboundary scenario, which keeps that failure visible.
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

	before := a.items[0].generation
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
	check(ok && report.Phase == replayRolledBack, "the account is %v, want a rolled back replay", report.Phase)
	check(strings.Contains(report.Reason, "could not settle at"),
		"the reason does not name the position the player could not report: %s", report.Reason)
	check(strings.Contains(report.Reason, "nothing was switched"),
		"the reason does not say nothing was switched: %s", report.Reason)
	check(strings.Contains(report.Reason, "authorised"),
		"the reason does not keep the target authorised: %s", report.Reason)
	check(a.items[0].generation == before,
		"a rolled back request changed the player (%d → %d)", before, a.items[0].generation)
	step(window, a, dir, scenario, "1-refused.png", "the rolled back request leaves the stage alone", func(a *appState) {
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
