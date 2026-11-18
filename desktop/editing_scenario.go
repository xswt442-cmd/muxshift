// P12: the free canvas on the real window.
//
// The geometry is covered by unit tests, but P12 asks a window question: does
// a drag on the real stage move the picture and nothing else? Two runs answer
// it. "editing" drives the same code a pointer drives and captures each step,
// so the frames, handles, stacking and reset can be read against the log.
// "editing-live" opens the window and waits for a person — or a mouse script —
// to drag the picture, logging every rectangle it sees.
package main

import (
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/xswt442-cmd/muxshift/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// runEditing is the scripted walk through the edit mode.
func runEditing(window *mygo.Window, a *appState, dir string) {
	go editingBody(window, a, dir)
}

func editingBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "editing"
	if len(a.items) < 2 {
		editingQuit(scenario, "CHECK FAILED: the edit check needs two sources, this run has %d", len(a.items))
		return
	}
	players := map[core.SourceID]*ui.VideoPlayer{}
	heard := map[core.TrackID]bool{}
	scene := ""
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandPlay}))
		for i, it := range a.items {
			if it.player != nil && it.video != "" && !it.shown {
				if err := a.showVideo(i); err != nil {
					a.lastErr = err
				}
			}
		}
		a.setLayout(layoutSplit)
		for _, it := range a.items {
			if it.player != nil {
				players[it.source.ID] = rawPlayer(it.player)
			}
		}
		heard = heardTracks(a)
		scene = a.sceneLabel()
		if first := a.stageGeometry(); len(first) > 0 {
			a.selectFromStage(first[0].item)
		}
	})
	logf(scenario, "started with %d source(s) as %s; heard %s", len(players), scene, tracksOf(heard))

	shot := func(name string) {
		path := filepath.Join(dir, name+".png")
		mygoRunOnMain(func() {
			window.Invalidate()
			_ = capture(window, path)
		})
		logf(scenario, "capture %s: %s", path, editingReport(a))
	}

	// The first shot is the arrangement before any gesture: frames around both
	// pictures, handles on the selected one, and the inspector open on it so
	// the pane's wording is in the evidence too.
	mygoRunOnMain(func() {
		a.inspector = paneOpen
		window.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	shot("1-frames")

	var target *item
	mygoRunOnMain(func() {
		if geometry := a.stageGeometry(); len(geometry) > 0 {
			target = geometry[0].item
		}
	})
	if target == nil {
		editingQuit(scenario, "CHECK FAILED: nothing is on the stage to edit")
		return
	}

	// One drag, in the same steps a pointer produces. dragByIn is what the
	// gesture calls; driving it here proves the picture follows the hand and
	// the scene is untouched, with no pointer timing to make flaky.
	before := stageBox{}
	mygoRunOnMain(func() {
		before, _ = a.stageBoxOf(target)
		a.drag = dragState{}
		w, h := a.stageFrameSize()
		for i := 0; i < 10; i++ {
			a.dragByIn(target, handleNone, 0.01, 0.005, w, h)
		}
	})
	moved := stageBox{}
	mygoRunOnMain(func() {
		moved, _ = a.stageBoxOf(target)
	})
	// The picture may not leave the stage: a full-height box starts at its
	// top edge, so the drag can only move it sideways.
	if !(moved.X > before.X+0.05) || moved.Y < 0 || moved.Y > before.Y+0.5 {
		editingQuit(scenario, "CHECK FAILED: a drag did not move the picture: %+v -> %+v", before, moved)
		return
	}
	shot("2-moved")

	// A corner resize with the ratio lock on: the picture keeps its shape and
	// the opposite corner stays put.
	resized := stageBox{}
	mygoRunOnMain(func() {
		a.drag = dragState{}
		w, h := a.stageFrameSize()
		a.dragByIn(target, handleSE, -0.06, -0.04, w, h)
		resized, _ = a.stageBoxOf(target)
	})
	// The lock keeps the picture's own shape, not the slot's: a slot that
	// started wider than the picture snaps to the picture the first time a
	// handle is taken.
	want := float32(0)
	mygoRunOnMain(func() {
		if p := rawPlayer(target.player); p != nil {
			if w, h := p.Size(); w > 0 && h > 0 {
				want = float32(w) / float32(h)
			}
		}
	})
	aspectAfter := resized.W / resized.H
	if want <= 0 {
		want = before.W / before.H
	}
	if math.Abs(float64(aspectAfter-want)) > 0.02 {
		editingQuit(scenario, "CHECK FAILED: a ratio-locked resize changed the shape: %.3f, want %.3f", aspectAfter, want)
		return
	}
	shot("3-resized")

	// Stacking: sending the edited picture to the front has to change which
	// picture covers which, not only a list index.
	mygoRunOnMain(func() {
		ids := a.shownIDs()
		a.sceneEdits().raise(target.source.ID, ids, true)
		a.invalidate()
	})
	shot("4-front")

	// The inspector's numbers: a refused value leaves the box alone, an
	// accepted one moves it.
	refused := false
	mygoRunOnMain(func() {
		a.selectionTo(target)
		a.boxFields.w = "0"
		a.boxFields.h = "0.4"
		a.applyBoxFields(target)
		refused = a.boxFields.note != ""
		box, _ := a.stageBoxOf(target)
		if box != resized {
			a.editNote = "a refused number moved the picture"
		}
	})
	if !refused {
		editingQuit(scenario, "CHECK FAILED: a box with no width was accepted")
		return
	}
	mygoRunOnMain(func() {
		a.boxFields.x = "0.10"
		a.boxFields.y = "0.10"
		a.boxFields.w = "0.40"
		a.boxFields.h = "0.40"
		a.applyBoxFields(target)
	})
	shot("5-typed")

	// Reset puts the picture back on the layout's slot, and the picture it
	// covers is the one the arrangement names.
	mygoRunOnMain(func() {
		a.forgetBox(target.source.ID)
		a.reloadBoxFields(target)
	})
	shot("6-reset")

	// A watching mode has no frames and no handles at all.
	mygoRunOnMain(func() { a.enterViewing(viewCinema, a.windowRef()) })
	time.Sleep(1500 * time.Millisecond)
	shot("7-cinema")
	mygoRunOnMain(func() { a.leaveViewing(a.windowRef()) })
	time.Sleep(800 * time.Millisecond)

	// Nothing above was allowed to open a player, change the sound or reset the
	// scene. The clock has to have moved through all of it.
	failed := ""
	mygoRunOnMain(func() {
		if got := a.sceneLabel(); got != scene {
			failed = fmt.Sprintf("the scene changed: %s -> %s", scene, got)
			return
		}
		if got := tracksOf(heardTracks(a)); got != tracksOf(heard) {
			failed = fmt.Sprintf("the sound changed: %s -> %s", tracksOf(heard), got)
		}
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			if want := players[it.source.ID]; want != nil && rawPlayer(it.player) != want {
				failed = fmt.Sprintf("source %s got a new player", it.source.ID)
			}
		}
		if a.drag.moved {
			// left over from the script, not a person: a real gesture is the
			// live run's business
			a.drag = dragState{}
		}
	})
	if failed != "" {
		editingQuit(scenario, "CHECK FAILED: %s", failed)
		return
	}
	logf(scenario, "CHECK PASSED: frames, drag, resize, stacking, typed numbers, reset, watching mode")
	time.Sleep(600 * time.Millisecond)
	mygoRunOnMain(func() { window.Close() })
}

// runEditingLive opens the edit mode and waits for a pointer gesture. It
// writes down the rectangles it sees so a capture can be read against them;
// the drag itself comes from a mouse script, which is the point of the run.
func runEditingLive(window *mygo.Window, a *appState, dir string) {
	go editingLiveBody(window, a, dir)
}

func editingLiveBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "editing-live"
	if len(a.items) < 1 {
		editingQuit(scenario, "CHECK FAILED: the live edit check needs a source")
		return
	}
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandPlay}))
		for i, it := range a.items {
			if it.player != nil && it.video != "" && !it.shown {
				if err := a.showVideo(i); err != nil {
					a.lastErr = err
				}
			}
		}
		if first := a.stageGeometry(); len(first) > 0 {
			a.selectFromStage(first[0].item)
		}
	})

	last := ""
	lastAt := time.Now()
	beatAt := time.Now()
	startedAt := time.Now()
	for time.Since(startedAt) < 40*time.Second {
		line := ""
		box := stageBox{}
		dragging := ""
		var stageX, stageY, stageW, stageH, windowW, windowH float32
		mygoRunOnMain(func() {
			geometry := a.stageGeometry()
			if len(geometry) == 0 {
				return
			}
			box = geometry[0].box
			if stage := a.stageFrame(); stage != nil {
				stageX, stageY, stageW, stageH = stage.X, stage.Y, stage.W, stage.H
			}
			if a.window != nil {
				bounds := a.window.Bounds()
				windowW, windowH = float32(bounds.Width), float32(bounds.Height)
			}
			if a.drag.active {
				dragging = fmt.Sprintf("dragging %s (%s)", a.drag.id, a.drag.handle)
			} else {
				dragging = "up"
			}
			line = fmt.Sprintf("stage dip=%.0f,%.0f %.0fx%.0f window dip=%.0fx%.0f picture box=%.3f,%.3f,%.3f,%.3f center=%.0f,%.0f",
				stageX, stageY, stageW, stageH, windowW, windowH, box.X, box.Y, box.W, box.H,
				stageX+(box.X+box.W/2)*stageW, stageY+(box.Y+box.H/2)*stageH)
		})
		if line != "" && (line != last || time.Since(lastAt) > 400*time.Millisecond) {
			logf(scenario, "%s | pointer %s", line, dragging)
			last = line
			lastAt = time.Now()
		}
		if time.Since(beatAt) > 500*time.Millisecond {
			beatAt = time.Now()
			pos := time.Duration(0)
			mygoRunOnMain(func() { pos = a.sync.playhead.Event() })
			logf(scenario, "beat %s box=%.3f,%.3f", pos.Round(10*time.Millisecond), box.X, box.Y)
		}
		time.Sleep(250 * time.Millisecond)
	}
	path := filepath.Join(dir, "live-end.png")
	mygoRunOnMain(func() {
		window.Invalidate()
		_ = capture(window, path)
	})
	logf(scenario, "capture %s: %s", path, editingReport(a))
	mygoRunOnMain(func() { window.Close() })
}

// stageFrame is the stage's own rectangle, for the live run's coordinate
// conversion. It is the box the stage drew in the frame before, in window DIP.
func (a *appState) stageFrame() *ui.Rect {
	return &ui.Rect{X: a.stageAt.X, Y: a.stageAt.Y, W: a.stageW, H: a.stageH}
}

// selectionTo focuses a source without changing the sound.
func (a *appState) selectionTo(it *item) {
	if it != nil {
		a.selected = it.source.ID
	}
}

// editingPointerNote says where the pointer is, so a drag in the log can be
// told apart from no drag at all.
func editingPointerNote(a *appState) string {
	if a.drag.active {
		return fmt.Sprintf("dragging %s (%s)", a.drag.id, a.drag.handle)
	}
	return "up"
}

// editingReport is the one-line state that goes beside each capture.
func editingReport(a *appState) string {
	parts := []string{"layout=" + a.layout.String()}
	if it := a.find(a.selected); it != nil {
		parts = append(parts, "selected="+string(it.source.ID))
	}
	if box, ok := a.stageBoxOf(a.find(a.selected)); ok {
		parts = append(parts, fmt.Sprintf("box=%.3f,%.3f,%.3f,%.3f", box.X, box.Y, box.W, box.H))
	}
	parts = append(parts, "ratio="+boolWord(!a.freeRatio), "note="+quote(a.editNote))
	for _, spot := range a.stageGeometry() {
		parts = append(parts, fmt.Sprintf("%s=%.2f,%.2f %.2fx%.2f",
			spot.item.source.ID, spot.box.X, spot.box.Y, spot.box.W, spot.box.H))
	}
	return joinParts(parts)
}

func editingQuit(scenario, format string, args ...any) {
	logf(scenario, format, args...)
	mygoRunOnMain(func() {
		mygo.App.Quit()
	})
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + s + `"`
}

func joinParts(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += " | "
		}
		out += part
	}
	return out
}
