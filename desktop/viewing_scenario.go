// P10.6: the watching modes on the real window.
//
// The state machine has unit tests, but the question P10 asks is a window
// question: while a scene plays, do the four modes come and go without a new
// player, without the clock jumping, and with the picture still moving? This
// run answers it with captures at every step and a log a reader can check
// against them.
package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/xswt442-cmd/muxshift/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func runViewing(window *mygo.Window, a *appState, dir string) {
	go viewingBody(window, a, dir)
}

func viewingBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "viewing"
	if len(a.items) == 0 {
		logf(scenario, "CHECK FAILED: no source")
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}

	players := map[core.SourceID]*ui.VideoPlayer{}
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandPlay}))
		for _, it := range a.items {
			if it.player != nil {
				players[it.source.ID] = rawPlayer(it.player)
			}
		}
	})
	logf(scenario, "started with %d source(s) in the scene as %s", len(players), a.sceneLabel())

	shot := func(name string) {
		path := filepath.Join(dir, name+".png")
		mygoRunOnMain(func() {
			window.Invalidate()
			_ = capture(window, path)
		})
		logf(scenario, "capture %s: %s", path, viewingReport(a, players))
	}

	type step struct {
		at    time.Duration
		label string
		act   func()
		shot  string
	}
	steps := []step{
		{2 * time.Second, "workbench", nil, "1-workbench"},
		{4 * time.Second, "enter cinema", func() { a.enterViewing(viewCinema, window) }, ""},
		{6 * time.Second, "cinema", nil, "2-cinema"},
		{8 * time.Second, "enter full screen", func() { a.enterViewing(viewFullscreen, window) }, ""},
		{10 * time.Second, "full screen", nil, "3-fullscreen"},
		{12 * time.Second, "out of the full screen, back in cinema", func() { a.leaveFullscreen(window) }, ""},
		{14 * time.Second, "cinema again", nil, "4-cinema-again"},
		{16 * time.Second, "leave to workbench", func() { a.leaveViewing(window) }, ""},
		{18 * time.Second, "back at the workbench", nil, "5-back-workbench"},
		{20 * time.Second, "enter compact", func() { a.enterViewing(viewCompact, window) }, ""},
		{22 * time.Second, "compact", nil, "6-compact"},
		{24 * time.Second, "compact on top", func() {
			a.toggleOnTop(window)
			if a.window != nil {
				a.window.Invalidate()
			}
		}, "7-compact-on-top"},
		{27 * time.Second, "leave to workbench again", func() { a.leaveViewing(window) }, ""},
		{29 * time.Second, "back at the workbench again", nil, "8-back-again"},
		{33 * time.Second, "final", nil, "9-final"},
	}

	started := time.Now()
	deadline := started.Add(36 * time.Second)
	next := 0
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		now := time.Since(started)
		if next >= len(steps) || now < steps[next].at {
			continue
		}
		step := steps[next]
		if step.act != nil {
			mygoRunOnMain(func() {
				step.act()
				window.Invalidate()
			})
		}
		if step.shot != "" {
			// The overlay hides itself after 2.5s without a pointer move, and
			// this run never moves the real pointer. Stopping the timer here is
			// what keeps a capture from depending on whether the picture
			// happened to keep the window repainting.
			mygoRunOnMain(func() {
				a.controlsShown = true
				a.idleTick++
			})
			time.Sleep(150 * time.Millisecond)
			shot(step.shot)
		} else {
			logf(scenario, "step %q at %s: %s", step.label, now.Truncate(time.Second), viewingReport(a, players))
		}
		next++
	}

	// The mode is part of the window, not of the player: after all of that, the
	// same players must still be the ones drawing, and their positions must
	// have moved.
	bad := ""
	mygoRunOnMain(func() {
		if a.viewing != viewWorkbench {
			bad = fmt.Sprintf("ended in %s, want the workbench", a.viewing)
			return
		}
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			if players[it.source.ID] != rawPlayer(it.player) {
				bad = fmt.Sprintf("source %s has a new player after the mode changes", it.source.ID)
				return
			}
			if it.player.Position() <= 0 {
				bad = fmt.Sprintf("source %s did not advance", it.source.ID)
				return
			}
		}
	})
	if bad != "" {
		logf(scenario, "CHECK FAILED: %s", bad)
	} else {
		logf(scenario, "CHECK PASSED: four modes, same players, pictures advanced")
	}
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

// viewingReport says what the window and the players are doing right now, so a
// capture can be read against the numbers that produced it.
func viewingReport(a *appState, players map[core.SourceID]*ui.VideoPlayer) string {
	line := fmt.Sprintf("mode=%s chrome=%v", a.viewing, a.viewing.editing())
	if a.window != nil {
		b := a.window.Bounds()
		line += fmt.Sprintf(" bounds=%dx%d@%d,%d full=%v max=%v onTop=%v",
			b.Width, b.Height, b.X, b.Y, a.window.IsFullScreen(), a.window.IsMaximized(), a.onTop)
	}
	for _, it := range a.items {
		if it.player == nil {
			line += fmt.Sprintf(" | %s no player", it.source.ID)
			continue
		}
		same := players[it.source.ID] == rawPlayer(it.player)
		line += fmt.Sprintf(" | %s shown=%v pos=%s same-player=%v muted=%v",
			it.source.ID, it.shown, it.player.Position().Truncate(time.Millisecond), same, it.player.Muted())
	}
	return line
}
