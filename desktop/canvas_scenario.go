// P9: the canvas probe.
//
// This file is the smallest experiment that can answer the hard question the
// viewing workbench depends on: can two different players put two different
// pictures on screen, in two different rectangles, at the same time, and keep
// advancing while the window is resized, one side is paused, and the window is
// minimized and restored? It draws nothing else — no rail, no inspector, no
// dock — so a black rectangle or a frozen picture cannot be blamed on the
// workbench around it.
//
// Run: desktop-live.exe -scenario canvas -dir <dir> -media a.mp4 -media b.mp4
package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// canvas rects are percentages of the content area. They are deliberately
// different shapes: a second video drawn in exactly the same place as the
// first would pass a naive check while proving nothing.
type canvasRect struct {
	left, top, width, height float32
	name                     string
}

var canvasRects = []canvasRect{
	{left: 1, top: 2, width: 55, height: 96, name: "left"},
	{left: 58, top: 2, width: 41, height: 96, name: "right"},
}

// canvasProbe is the state of the experiment: the players and where each is
// drawn.
type canvasProbe struct {
	players []*ui.VideoPlayer
	uris    []string
}

// runCanvas starts the probe after the window exists.
func runCanvas(window *mygo.Window, a *appState, dir string) {
	go canvasBody(window, a, dir)
}

func canvasBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "canvas"
	probe := &canvasProbe{}

	// Two sources, opened the way the application opens any source: on the
	// drawing thread, through the same helper the session uses. A player made
	// off that thread never produced a picture in this build.
	for i, it := range a.items {
		if i >= len(canvasRects) {
			break
		}
		player := rawPlayer(it.player)
		if player == nil {
			opened, err := openPlayerOnMain(it.uri)
			if err != nil {
				logf(scenario, "CHECK FAILED: source %d does not open: %v", i+1, err)
				continue
			}
			player = opened
		}
		probe.players = append(probe.players, player)
		probe.uris = append(probe.uris, it.uri)
		logf(scenario, "rect %q: source %d %s", canvasRects[i].name, i+1, shortURI(it.uri))
	}
	if len(probe.players) < 2 {
		logf(scenario, "CHECK FAILED: the probe needs two sources, got %d", len(probe.players))
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	mygoRunOnMain(func() {
		a.probe = probe
		window.Invalidate()
	})

	left, right := probe.players[0], probe.players[1]
	var startBounds mygo.Rectangle
	mygoRunOnMain(func() { startBounds = window.Bounds() })
	logf(scenario, "window starts at %dx%d", startBounds.Width, startBounds.Height)

	// The script: each step either acts on the running window or takes a
	// picture. A step that acts says what it did, so the log can be read next
	// to the picture that follows it.
	type canvasStep struct {
		at    time.Duration
		label string
		act   func()
		shot  bool
	}
	steps := []canvasStep{
		{2 * time.Second, "start", nil, true},
		{10 * time.Second, "resize", func() {
			window.SetBounds(mygo.Rectangle{X: startBounds.X, Y: startBounds.Y, Width: 900, Height: 620})
		}, false},
		{13 * time.Second, "after resize", nil, true},
		{16 * time.Second, "restore size", func() { window.SetBounds(startBounds) }, false},
		{19 * time.Second, "after size restore", nil, true},
		{22 * time.Second, "pause right", func() { right.Pause() }, false},
		{25 * time.Second, "paused right", nil, true},
		{27 * time.Second, "resume right", func() { right.Play() }, false},
		{30 * time.Second, "minimize", func() { window.Minimize() }, false},
		{34 * time.Second, "restore after minimize", func() { window.Restore() }, false},
		{38 * time.Second, "after window restore", nil, true},
		{62 * time.Second, "end", nil, true},
	}

	started := time.Now()
	mygoRunOnMain(func() {
		left.Play()
		right.Play()
	})
	last := map[int]time.Duration{}
	stalled := map[int]int{}
	next := 0
	deadline := started.Add(65 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		now := time.Since(started)
		for i, p := range probe.players {
			pos := p.Position()
			if pos == last[i] {
				stalled[i]++
			} else {
				stalled[i] = 0
			}
			if now >= 5*time.Second && stalled[i] > 6 && !expectPaused(i, now) {
				logf(scenario, "STALLED: rect %q has not moved for %s at %s",
					canvasRects[i].name, pos, now.Truncate(time.Second))
				stalled[i] = 0
			}
			last[i] = pos
		}
		if next >= len(steps) || now < steps[next].at {
			continue
		}
		step := steps[next]
		if step.act != nil {
			mygoRunOnMain(step.act)
		}
		if step.shot {
			path := filepath.Join(dir, fmt.Sprintf("%d-%s.png", next+1, slug(step.label)))
			// The raster is painted between passes: capture after asking for a
			// pass, or the picture written belongs to the frame before.
			mygoRunOnMain(func() { window.Invalidate() })
			time.Sleep(300 * time.Millisecond)
			mygoRunOnMain(func() { _ = capture(window, path) })
			logf(scenario, "capture %s at %s: %s", path, now.Truncate(time.Second), probe.report())
		} else {
			logf(scenario, "step %q at %s: %s", step.label, now.Truncate(time.Second), probe.report())
		}
		next++
	}
	logf(scenario, "final: %s", probe.report())
	mygoRunOnMain(func() {
		a.probe = nil
		a.release()
		mygo.App.Quit()
	})
}

// expectPaused reports whether a still position at this moment is what the
// script asked for rather than a failure: the right-hand video is paused from
// 22s to 27s, and the window is minimized from 30s to 34s.
func expectPaused(rect int, now time.Duration) bool {
	if rect == 1 && now >= 22*time.Second && now < 29*time.Second {
		return true
	}
	return now >= 30*time.Second && now < 36*time.Second
}

// slug turns a step's words into a file name.
func slug(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// report is what each player says about itself, in one line.
func (p *canvasProbe) report() string {
	line := ""
	for i, player := range p.players {
		line += fmt.Sprintf("rect %q ready=%v playing=%v pos=%s dur=%s err=%v | ",
			canvasRects[i].name, player.Ready(), player.Playing(),
			player.Position().Truncate(time.Millisecond), player.Duration().Truncate(time.Millisecond), player.Err())
	}
	return line
}

// view draws nothing but the two rectangles.
func (p *canvasProbe) view(c *ui.Context) {
	c.SetTheme(muxshiftTheme())
	ui.Column(c).Fill().Background(colorBg).Children(func() {
		for i, player := range p.players {
			if player == nil || i >= len(canvasRects) {
				continue
			}
			rect := canvasRects[i]
			ui.Video(c, player).Absolute().
				LeftPercent(rect.left).TopPercent(rect.top).
				WidthPercent(rect.width).HeightPercent(rect.height).
				Fit(ui.Contain)
		}
	})
}
