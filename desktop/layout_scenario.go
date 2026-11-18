// P11.5: the five layouts on the real window.
//
// The geometry has unit tests, but the question P11 asks is a window question:
// while a scene plays, does switching the arrangement move pictures without
// opening a player, resetting the clock, changing what is heard, or leaving a
// black stage? This run answers it with a capture per arrangement and a log a
// reader can check against them.
package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func runLayouts(window *mygo.Window, a *appState, dir string) {
	go layoutsBody(window, a, dir)
}

func layoutsBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "layouts"
	if len(a.items) < 2 {
		layoutsQuit(scenario, "CHECK FAILED: a layout check needs two sources, this run has %d", len(a.items))
		return
	}

	players := map[core.SourceID]*ui.VideoPlayer{}
	heard := map[core.TrackID]bool{}
	scene := ""
	bad := ""
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandPlay}))
		// Every source with a video takes part, so the grid has four pictures
		// to place and the smaller arrangements have something to leave out.
		for i, it := range a.items {
			if it.player != nil && it.video != "" && !it.shown {
				if err := a.showVideo(i); err != nil {
					bad = err.Error()
				}
			}
		}
		for _, it := range a.items {
			if it.player != nil {
				players[it.source.ID] = rawPlayer(it.player)
			}
		}
		heard = heardTracks(a)
		scene = a.sceneLabel()
	})
	logf(scenario, "started with %d source(s) as %s; heard %s", len(players), scene, tracksOf(heard))

	positions := map[core.SourceID]time.Duration{}
	lastShot := map[core.SourceID]time.Duration{}
	shot := func(name string) {
		path := filepath.Join(dir, name+".png")
		mygoRunOnMain(func() {
			window.Invalidate()
			_ = capture(window, path)
		})
		logf(scenario, "capture %s: %s", path, layoutReport(a))
		mygoRunOnMain(func() {
			for _, it := range a.items {
				if it.player == nil {
					continue
				}
				pos := it.player.Position()
				if pos > positions[it.source.ID] {
					positions[it.source.ID] = pos
				}
				lastShot[it.source.ID] = pos
			}
		})
	}

	type step struct {
		at   time.Duration
		act  func()
		shot string
	}
	var steps []step
	at := 2 * time.Second
	// The first frames are not there yet for every source; the stage has to
	// show a placeholder for the ones still opening instead of whatever a
	// player with no frame draws.
	steps = append(steps, step{at: 400 * time.Millisecond, act: func() { a.setLayout(layoutGrid) }})
	steps = append(steps, step{at: 900 * time.Millisecond, shot: "at-once"})
	for _, kind := range layoutKinds {
		kind := kind
		steps = append(steps, step{at: at, act: func() { a.setLayout(kind) }})
		steps = append(steps, step{at: at + 1200*time.Millisecond, shot: kind.String()})
		at += 2600 * time.Millisecond
	}
	// The source a person puts first takes the big slot. The order a layout
	// reads is the one a Main click writes, not the scene's own order.
	var second core.SourceID
	mygoRunOnMain(func() {
		if len(a.items) > 1 {
			second = a.items[1].source.ID
		}
	})
	if second != "" {
		steps = append(steps, step{at: at, act: func() { a.setLayout(layoutPip); a.setPrimary(second) }})
		steps = append(steps, step{at: at + 1200*time.Millisecond, shot: "pip-with-second-first"})
		at += 2400 * time.Millisecond
		steps = append(steps, step{at: at, act: func() { a.setLayout(layoutGrid) }})
	}

	// A watching mode keeps the arrangement: the stage is the same stage. Each
	// arrangement is walked through the two modes that leave room to look at
	// it, and the grid also in full screen, where the window itself changes.
	for _, mode := range []viewingMode{viewCinema, viewCompact} {
		mode := mode
		steps = append(steps, step{at: at, act: func() { a.enterViewing(mode, window) }})
		at += 900 * time.Millisecond
		for _, kind := range layoutKinds {
			kind := kind
			steps = append(steps, step{at: at, act: func() { a.setLayout(kind) }})
			steps = append(steps, step{at: at + 1100*time.Millisecond, shot: mode.String() + "-" + kind.String()})
			at += 2300 * time.Millisecond
		}
		steps = append(steps, step{at: at, act: func() { a.leaveViewing(window) }})
		at += 900 * time.Millisecond
	}
	steps = append(steps, step{at: at, act: func() { a.setLayout(layoutGrid); a.enterViewing(viewFullscreen, window) }})
	steps = append(steps, step{at: at + 1300*time.Millisecond, shot: "fullscreen-grid"})
	steps = append(steps, step{at: at + 2500*time.Millisecond, act: func() { a.leaveFullscreen(window) }})
	at += 3400 * time.Millisecond

	// A window that changes size while a four-picture grid is up keeps its
	// pictures: the slots are worked out from the box on every frame.
	steps = append(steps, step{at: at, act: func() { a.setLayout(layoutGrid) }})
	steps = append(steps, step{at: at + 1100*time.Millisecond, shot: "grid-before-resize"})
	steps = append(steps, step{at: at + 1600*time.Millisecond, act: func() {
		window.SetBounds(mygo.Rectangle{X: 120, Y: 90, Width: 820, Height: 620})
	}})
	steps = append(steps, step{at: at + 2800*time.Millisecond, shot: "grid-resized"})
	at += 3600 * time.Millisecond

	// Removing the source a grid slot holds shrinks the grid; nothing is left
	// drawing in the slot it used to occupy.
	if len(a.items) > 0 {
		var gone core.SourceID
		mygoRunOnMain(func() { gone = a.items[len(a.items)-1].source.ID })
		steps = append(steps, step{at: at, act: func() {
			window.SetBounds(mygo.Rectangle{X: 60, Y: 60, Width: 1150, Height: 660})
		}})
		steps = append(steps, step{at: at + 900*time.Millisecond, act: func() {
			a.report(a.runCommand(command{Kind: commandRemove, Source: gone}))
		}})
		steps = append(steps, step{at: at + 2100*time.Millisecond, shot: "grid-after-removal"})
		at += 3000 * time.Millisecond
	}

	started := time.Now()
	deadline := started.Add(at + 4*time.Second)
	next := 0
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
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
			shot(step.shot)
		}
		next++
	}

	var after map[core.TrackID]bool
	var afterScene string
	mygoRunOnMain(func() {
		after = heardTracks(a)
		afterScene = a.sceneLabel()
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			if players[it.source.ID] != rawPlayer(it.player) {
				bad = fmt.Sprintf("source %s has a new player after the layout changes", it.source.ID)
				return
			}
			// The picture that is not on the stage is still the picture that is
			// playing: a layout may hide it, it may not stop it.
			if positions[it.source.ID] <= 0 {
				bad = fmt.Sprintf("source %s never advanced", it.source.ID)
				return
			}
			if lastShot[it.source.ID] < positions[it.source.ID] {
				bad = fmt.Sprintf("source %s went backwards", it.source.ID)
				return
			}
		}
		if afterScene != scene {
			bad = fmt.Sprintf("the scene changed from %q to %q", scene, afterScene)
			return
		}
		if !sameTracks(heard, after) {
			bad = fmt.Sprintf("what is heard changed from %s to %s", tracksOf(heard), tracksOf(after))
		}
	})
	if bad != "" {
		logf(scenario, "CHECK FAILED: %s", bad)
	} else {
		logf(scenario, "CHECK PASSED: five layouts, same players, same scene, same sound, pictures advanced")
	}
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

// layoutReport says where the stage put each picture right now, which is what a
// capture has to agree with.
func layoutReport(a *appState) string {
	pictures := a.stagePictures()
	slots := layoutSlots(a.layout, len(pictures))
	line := fmt.Sprintf("layout=%s shown=%d placed=%d note=%q event=%s audio=%s scene=%s",
		a.layout, len(pictures), len(slots), a.layoutNote(),
		a.sync.playhead.Event().Round(10*time.Millisecond), tracksOf(heardTracks(a)), sceneAudio(a))
	for i, it := range pictures {
		line += fmt.Sprintf(" | %s rect=%.0f,%.0f %.0fx%.0f", it.source.ID,
			slots[i].left, slots[i].top, slots[i].width, slots[i].height)
	}
	for _, it := range a.items {
		if it.player == nil || it.shown {
			continue
		}
		line += fmt.Sprintf(" | %s off-stage", it.source.ID)
	}
	return line
}

// heardTracks is the set of audio tracks the scene is actually playing.
func heardTracks(a *appState) map[core.TrackID]bool {
	out := map[core.TrackID]bool{}
	for _, it := range a.items {
		if it.audio != "" && it.player != nil && !it.player.Muted() {
			out[it.audio] = true
		}
	}
	return out
}

func sameTracks(a, b map[core.TrackID]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func tracksOf(set map[core.TrackID]bool) string {
	if len(set) == 0 {
		return "nothing"
	}
	out := ""
	for id := range set {
		if out != "" {
			out += ","
		}
		out += string(id)
	}
	return out
}

func layoutsQuit(scenario, format string, args ...any) {
	logf(scenario, format, args...)
	mygoRunOnMain(func() {
		mygo.App.Quit()
	})
}

// sceneAudio writes the routing the scene holds — track and level — which is
// what a layout must leave alone. It is not what the speakers do: a source can
// be silent for reasons of its own (not aligned yet, muted, no player).
func sceneAudio(a *appState) string {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	if len(scene.Audio) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(scene.Audio))
	for _, level := range scene.Audio {
		parts = append(parts, fmt.Sprintf("%s@%.2f", level.Track, level.Volume))
	}
	return strings.Join(parts, ",")
}
