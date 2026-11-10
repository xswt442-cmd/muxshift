package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runAlignAsync proves two things at once: the probe runs off the UI thread, so
// the window keeps drawing while it decodes, and a change made to the session
// while it runs comes back as an expired suggestion instead of being folded in.
func runAlignAsync(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "async", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)

		step(window, a, dir, "async", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
			logGroup("async", "play", a.playAll())
		})
		wait(2 * time.Second)

		// The group keeps playing on purpose: a paused window does not repaint in
		// this MyGo build, and the point here is that the window keeps drawing
		// while the comparison runs.
		step(window, a, dir, "async", "", "anchor the event clock", func(a *appState) {
			logGroup("async", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
			logGroup("async", "play", a.playAll())
		})
		wait(700 * time.Millisecond)

		var framesBefore int
		step(window, a, dir, "async", "", "start the probe off the UI thread", func(a *appState) {
			framesBefore = a.framesBuilt()
			a.probeAsync(0, 1)
			logf("async", "probe started, frames so far %d", framesBefore)
		})

		// The decode takes a few hundred milliseconds, so this lands inside it.
		wait(150 * time.Millisecond)
		step(window, a, dir, "async", "", "the user moves the target while the probe runs", func(a *appState) {
			logf("async", "frames drawn while the probe ran: %d", a.framesBuilt()-framesBefore)
			logf("async", "positions while the probe ran [%s]", strings.Join(a.positions(), " | "))
			a.nudgeAndRecord(1, -100*time.Millisecond)
		})

		// The result arrives on the UI thread; the running flag says when.
		for done := time.Now().Add(30 * time.Second); time.Now().Before(done); {
			var running bool
			mygoRunOnMain(func() { running = a.probeRunning })
			if !running {
				break
			}
			wait(100 * time.Millisecond)
		}

		step(window, a, dir, "async", "1-async-expired.png", "what the window kept doing", func(a *appState) {
			logf("async", "frames drawn in total while the probe ran: %d", a.framesBuilt()-framesBefore)
			logf("async", "positions after the probe [%s]", strings.Join(a.positions(), " | "))
			logf("async", "the finished probe says: %s", a.suggestionLine(a.lastProbe))
			if a.lastProbe.Expired == "" {
				logf("async", "the result did not report the change made after the probe started")
			}
		})

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runAlignProbe shows what the alignment probe proposes and what it refuses,
// then proves that neither answer touched the timeline or the players.
func runAlignProbe(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the target is shown once before it is measured.
		step(window, a, dir, "align", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)

		// The probe is read-only, so this one runs before any sync command has
		// created a group: a suggestion may not build one either.
		step(window, a, dir, "align", "", "probe before any sync command", func(a *appState) {
			logf("align", "groups before [%s]", a.groupState())
			before := a.sync.timeline.Shifts()
			a.probeAndReport(0, 1)
			logf("align", "groups after [%s] shifts [%s]", a.groupState(), describeShifts(a.sync.timeline.Shifts()))
			if !sameShifts(before, a.sync.timeline.Shifts()) {
				logf("align", "the probe changed the timeline offsets")
			}
		})
		wait(500 * time.Millisecond)

		step(window, a, dir, "align", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
			logGroup("align", "play", a.playAll())
		})
		wait(3 * time.Second)

		// Pausing first keeps the players still, so the positions captured
		// before and after the probe can actually be compared.
		step(window, a, dir, "align", "", "pause before probing", func(a *appState) {
			logGroup("align", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)

		var suggestion alignSuggestion
		var before []core.Shift
		var positionsBefore []string
		step(window, a, dir, "align", "", "probe source 2 against source 1", func(a *appState) {
			before = a.sync.timeline.Shifts()
			positionsBefore = a.positions()
			suggestion = a.probeAndReport(0, 1)
		})

		step(window, a, dir, "align", "", "state after the probe", func(a *appState) {
			logf("align", "shifts before [%s] after [%s]", describeShifts(before), describeShifts(a.sync.timeline.Shifts()))
			logf("align", "positions before [%s] after [%s]", strings.Join(positionsBefore, " | "), strings.Join(a.positions(), " | "))
			if !sameShifts(before, a.sync.timeline.Shifts()) {
				logf("align", "the probe changed the timeline offsets")
			}
		})

		// A paused window does not repaint in this build, so the picture of the
		// proposal is taken while the group plays again.
		step(window, a, dir, "align", "", "resume so the window redraws", func(a *appState) {
			logGroup("align", "play", a.playAll())
		})
		wait(1500 * time.Millisecond)
		step(window, a, dir, "align", "1-suggestion.png", "the proposal in the status bar", nil)
		step(window, a, dir, "align", "", "pause again for the manual change", func(a *appState) {
			logGroup("align", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(300 * time.Millisecond)

		// A manual change after the probe must invalidate the proposal.
		step(window, a, dir, "align", "", "user moves the target by hand", func(a *appState) {
			a.nudgeAndRecord(1, -100*time.Millisecond)
			a.logStale(suggestion)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "align", "2-after-manual-nudge.png", "after the manual nudge", nil)

		// A source with unrelated content must be refused, not guessed. The
		// scenario only runs this step when a third source was given.
		step(window, a, dir, "align", "3-refused.png", "probe a source with different content", func(a *appState) {
			if len(a.items) < 3 {
				logf("align", "no third source was given, so the refusal case is not run")
				return
			}
			a.probeAndReport(0, 2)
		})
		wait(time.Second)
		step(window, a, dir, "align", "", "state after the refused probe", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}
