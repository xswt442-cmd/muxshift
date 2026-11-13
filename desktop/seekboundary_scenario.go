//go:build windows

package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runSeekBoundary drives the replay seek entry against a buffer that joined a
// live stream mid-flight. Unlike the two-source fixture, the buffer's first
// segment and the anchor's measured interval start at the same moment, so a
// target at the window start reaches the transaction and asks the replacement
// player for position zero inside its own file.
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
	// A shift written straight into the timeline is a statement without a
	// measurement; the plan refuses to lean on it, so record one.
	var nudgeNote string
	mygoRunOnMain(func() { nudgeNote, _ = a.nudge(0, 10*time.Millisecond) })
	logf(scenario, "the source was nudged by 10ms: %s", nudgeNote)
	time.Sleep(time.Second)

	var windowNote string
	var start, end time.Duration
	mygoRunOnMain(func() {
		var err error
		start, end, err = a.replayWindowForEntry()
		if err != nil {
			windowNote = err.Error()
		}
	})
	logf(scenario, "the entry offers a window of %s..%s", start, end)
	if windowNote != "" {
		logf(scenario, "the entry has no window: %s", windowNote)
		return
	}

	// A target at the window start is authorised, and it asks the replacement
	// player for position zero inside the file it cuts at that moment. The
	// player cannot report that position, so the transaction has to say the
	// target was authorised but could not be reported, and switch nothing.
	before := a.items[0].generation
	var boundaryNote string
	mygoRunOnMain(func() {
		a.seekText = "0"
		boundaryNote = a.requestReplaySeek()
	})
	logf(scenario, "a request at the window start said: %s", boundaryNote)
	if !waitSeekDone(a, 120*time.Second) {
		logf(scenario, "the boundary transaction did not finish within 120s")
		return
	}
	time.Sleep(1500 * time.Millisecond)
	boundary, ok := a.lastReport()
	logf(scenario, "the boundary account says: %s", boundary.Line())
	check(ok && boundary.Phase == replayRolledBack, "the boundary account is %v, want a rolled back replay", boundary.Phase)
	check(strings.Contains(boundary.Reason, "could not settle at"),
		"the boundary reason does not name the position the player could not report: %s", boundary.Reason)
	check(strings.Contains(boundary.Reason, "nothing was switched"),
		"the boundary reason does not say nothing was switched: %s", boundary.Reason)
	check(strings.Contains(boundary.Reason, "authorised"),
		"the boundary reason does not keep the target authorised: %s", boundary.Reason)
	check(a.items[0].generation == before,
		"a rolled back boundary request changed the player (%d → %d)", before, a.items[0].generation)
	step(window, a, dir, scenario, "1-boundary.png", "the rolled back boundary request leaves the stage alone", func(a *appState) {
		a.showVideo(0)
	})

	// The same entry, after that failure, still commits a target the players can
	// report: the refusal is about this position, not about the window.
	var recoveryNote string
	mygoRunOnMain(func() {
		a.seekText = "5"
		recoveryNote = a.requestReplaySeek()
	})
	logf(scenario, "after the boundary failure a valid request said: %s", recoveryNote)
	if !waitSeekDone(a, 90*time.Second) {
		logf(scenario, "the recovery transaction did not finish within 90s")
		return
	}
	time.Sleep(time.Second)
	recovery, ok := a.lastReport()
	logf(scenario, "the recovery account says: %s", recovery.Line())
	check(ok && recovery.Phase == replayCommitted, "the recovery account is %v, want a committed replay", recovery.Phase)
	check(a.items[0].generation == before+1,
		"the recovery left the player at generation %d, want %d", a.items[0].generation, before+1)
	step(window, a, dir, scenario, "2-recovery.png", "the recovery commits and shows the replay", func(a *appState) {
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
