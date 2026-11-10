package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

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
