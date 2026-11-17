// P9.3: two live sources on the real stage.
//
// The isolated probe (canvas_scenario.go) proves two videos can be drawn at
// once. This one proves the product's own stage does it: the same stage(), the
// same apply(), the same swap path — with the scene showing two sources, a
// page switch, a reload of one side, and a window resize in the middle.
package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

func runCanvasLive(window *mygo.Window, a *appState, dir string) {
	go canvasLiveBody(window, a, dir)
}

func canvasLiveBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "canvaslive"
	if len(a.items) < 2 {
		logf(scenario, "CHECK FAILED: the check needs two sources, got %d", len(a.items))
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	first, second := a.items[0], a.items[1]
	if first.video == "" || second.video == "" {
		logf(scenario, "CHECK FAILED: a source has no video track (%q, %q)", first.video, second.video)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}

	var fail string
	mygoRunOnMain(func() {
		for _, it := range []*item{first, second} {
			if err := a.ensurePlayer(it); err != nil {
				fail = fmt.Sprintf("source %s does not open: %v", a.name(it), err)
				return
			}
		}
		// The scene is what says which pictures are on the stage; giving it
		// both tracks is the whole of the multi-picture request.
		if _, err := a.session.SetSceneVideo(a.scene, first.video, second.video); err != nil {
			fail = fmt.Sprintf("scene does not take two tracks: %v", err)
			return
		}
		a.apply()
		for _, it := range []*item{first, second} {
			if it.player != nil {
				it.player.Play()
			}
		}
		window.Invalidate()
	})
	if fail != "" {
		logf(scenario, "CHECK FAILED: %s", fail)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	logf(scenario, "scene shows two sources: %s and %s", a.name(first), a.name(second))

	type liveStep struct {
		at    time.Duration
		label string
		act   func()
		shot  bool
	}
	var startBounds mygo.Rectangle
	mygoRunOnMain(func() { startBounds = window.Bounds() })
	steps := []liveStep{
		{2 * time.Second, "start", nil, true},
		{10 * time.Second, "leave for sources", func() { a.workspace = workspaceSources }, false},
		{13 * time.Second, "back to stage", func() { a.workspace = workspaceStage }, false},
		{15 * time.Second, "after page return", nil, true},
		{20 * time.Second, "reload right", func() {
			a.report(a.runCommand(command{Kind: commandReload, Source: second.source.ID}))
		}, false},
		{28 * time.Second, "after reload", nil, true},
		{32 * time.Second, "resize", func() {
			window.SetBounds(mygo.Rectangle{X: startBounds.X, Y: startBounds.Y, Width: 900, Height: 620})
		}, false},
		{36 * time.Second, "after resize", nil, true},
		{62 * time.Second, "end", nil, true},
	}

	started := time.Now()
	deadline := started.Add(65 * time.Second)
	next := 0
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
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
		if step.shot {
			path := filepath.Join(dir, fmt.Sprintf("%d-%s.png", next+1, slug(step.label)))
			mygoRunOnMain(func() { window.Invalidate() })
			time.Sleep(300 * time.Millisecond)
			mygoRunOnMain(func() { _ = capture(window, path) })
			logf(scenario, "capture %s at %s: %s", path, now.Truncate(time.Second), liveReport(a))
		} else {
			logf(scenario, "step %q at %s: %s", step.label, now.Truncate(time.Second), liveReport(a))
		}
		next++
	}
	logf(scenario, "final: %s", liveReport(a))
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

// liveReport says what each source's player reports, in one line, so the log
// and the pictures can be read together.
func liveReport(a *appState) string {
	line := ""
	for i, it := range a.items {
		if it.player == nil {
			line += fmt.Sprintf("source %d: no player | ", i+1)
			continue
		}
		line += fmt.Sprintf("source %d shown=%v ready=%v playing=%v pos=%s err=%v | ",
			i+1, it.shown, it.player.Ready(), it.player.Playing(),
			it.player.Position().Truncate(time.Millisecond), it.player.Err())
	}
	return line
}
