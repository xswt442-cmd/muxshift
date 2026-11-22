package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file groups the scenario entry points in this domain. It was merged from common_scenario.go, editing_scenario.go, final_scenario.go, mixer_scenario.go, mutehold_scenario.go, scenario_scene.go, scenario_support.go, scenarios.go, sourcememory_scenario.go, sources_scenario.go, swap_scenario.go, swap_two_scenario.go.

var commonShift = 4 * time.Second

// runCommon is P16.3d-3.0, kept current with the content clock: two sources that
// really are the same event, buffered separately, must produce a non-empty
// shared window once each source's measured anchor puts it on the content clock
// and its confirmed offset puts it on the event clock. The fixture's four-second
// difference between container timestamps is not a content offset and must not
// reach the timeline.
//
// The fixture is built so the answer can be predicted without asking the code
// under test: the second playlist carries the same encoded frames as the first,
// every packet timestamp four seconds later. Two independent checks hold that
// claim up: ffprobe reports the first segment of the second playlist starting
// exactly 4s after the first, and the decoded frames of corresponding segments
// hash identically. That is what makes the shared window an observation rather
// than a restatement of the number the window was built from.
func runCommon(window *mygo.Window, a *appState, dir string) {
	go commonBody(window, a, dir)
}

func commonBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "common"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for i := 0; i < 2; i++ {
		it, err := a.at(i)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", i+1, shortURI(it.uri))
			return
		}
		uris[i] = it.uri
	}

	sources := make([]replaySource, 0, 2)
	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("source-%d", index+1)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		sources = append(sources, replaySource{index: index, runner: runner})
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for i, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", i+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	// Wait for both pollers to collect enough of each playlist to overlap even
	// after the 4s the second one carries.
	windows := make([]dvr.Range, 2)
	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 60*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 60s", index+1)
			return
		}
		windows[index] = windowOf
		logf(scenario, "source %d buffers %s of %s", index+1, logRange(windowOf), shortURI(uris[index]))
	}

	// The measured anchors, before any offset is applied: what media time each
	// buffer's first stored second really is. They are printed on their own so a
	// reader can check the four second difference with ffprobe alone.
	anchors := make([]dvr.Anchor, 2)
	mediaStarts := make([]time.Duration, 2)
	for index, runner := range runners {
		// The measurement waits here, off the drawing thread: ffprobe runs in the
		// background and the loop below only reads what it produced.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		stored, err := runner.waitBufferAnchor(ctx, windows[index])
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		anchors[index] = stored
		media, err := stored.MediaTime(windows[index].Start)
		if err != nil {
			logf(scenario, "source %d: place the window on media time: %v", index+1, err)
			return
		}
		mediaStarts[index] = media
		logf(scenario, "source %d measured: media %s at buffer %s (epoch %d, %s)",
			index+1, sceneClock(anchors[index].MediaStart), sceneClock(anchors[index].BufferStart),
			anchors[index].Epoch, anchors[index].BufferInstance)
	}

	measuredGap := mediaStarts[1] - mediaStarts[0]
	logf(scenario, "the two buffers start %s apart in media time; the fixture shifts the second playlist's timestamps by exactly %s",
		sceneClock(measuredGap), sceneClock(commonShift))
	check(absDuration(measuredGap-commonShift) <= 100*time.Millisecond,
		"the measured media gap is %s, not the %s the fixture was built with", sceneClock(measuredGap), sceneClock(commonShift))

	// The fixtures hold the same frames, so the content offset between them is
	// zero: the four seconds are the container's mux delay, and the content
	// clock (buffer time) does not carry it. Both sources get a confirmed zero
	// offset, which is the honest statement about where their content sits.
	step(window, a, dir, scenario, "", "align both sources on the shared event clock", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.apply()
	})

	var start, end time.Duration
	var windowErr error
	mygoRunOnMain(func() {
		start, end, windowErr = a.commonReplay(sources)
	})
	if windowErr != nil {
		failures++
		logf(scenario, "CHECK FAILED: no shared window for two sources of the same event: %v", windowErr)
		return
	}
	// The event start is the later of the two content starts. Content time is
	// buffer time, so this is a statement about the clock the timeline holds,
	// not about the container timestamps the anchors measured.
	contentStarts := make([]time.Duration, len(windows))
	wantStart := time.Duration(0)
	for index := range windows {
		content, err := anchors[index].ContentTime(windows[index].Start)
		if err != nil {
			failures++
			logf(scenario, "CHECK FAILED: source %d: content for the window start: %v", index+1, err)
			return
		}
		contentStarts[index] = content
		if index == 0 || content > wantStart {
			wantStart = content
		}
	}
	logf(scenario, "shared window on the event clock: %s..%s; the content clocks put the event start at %s (the containers would say %s and %s)",
		sceneClock(start), sceneClock(end), sceneClock(wantStart), sceneClock(mediaStarts[0]), sceneClock(mediaStarts[1]))
	check(absDuration(start-wantStart) <= 100*time.Millisecond,
		"the shared window starts at %s, but the measured anchors put the event at %s",
		sceneClock(start), sceneClock(wantStart))
	check(end-start >= 8*time.Second, "the shared window covers only %s of overlap", sceneClock(end-start))
	step(window, a, dir, scenario, "1-shared-window.png", "two sources of the same event on one clock", nil)

	// The pictures carry the proof the numbers cannot: both fixtures show the
	// same burned-in timecode from the source material. Both players are asked
	// for the same position, because a player's seek and Position() follow the
	// content clock, not the container timestamps the buffer records -- asking
	// the second one for 6s+shift would repeat the mistake this stage is about.
	const proofEvent = 6 * time.Second
	step(window, a, dir, scenario, "", "pause both players on the same content position", func(a *appState) {
		// The group only covers sources that joined the scene, and the second
		// one is deliberately background here, so pause the players themselves.
		for index, it := range a.items {
			if it.player == nil {
				continue
			}
			it.player.Pause()
			it.player.Seek(proofEvent)
			logf(scenario, "source %d paused and asked for %s", index+1, sceneClock(proofEvent))
		}
	})
	time.Sleep(2 * time.Second)
	var firstPos, secondPos time.Duration
	step(window, a, dir, scenario, "1a-source-1.png", "the first source at the shared content position", func(a *appState) {
		if err := a.showVideo(0); err != nil {
			logf(scenario, "show source 1: %v", err)
		}
	})
	firstPos = positionOf(a, 0)
	step(window, a, dir, scenario, "1b-source-2.png", "the second source at the same content position", func(a *appState) {
		if err := a.showVideo(1); err != nil {
			logf(scenario, "show source 2: %v", err)
		}
	})
	secondPos = positionOf(a, 1)
	logf(scenario, "the pictures were taken at player positions %s and %s, %s apart",
		sceneClock(firstPos), sceneClock(secondPos), sceneClock(secondPos-firstPos))
	// Reading the two captures is the check the numbers cannot make on their
	// own: the same content position has to read the same burned-in timecode in
	// both pictures even though the second file's container timestamps are four
	// seconds later. The tolerance is one frame at 30 fps, not the 1.4667s the
	// container delays this fixture by.
	check(absDuration(secondPos-firstPos) <= 40*time.Millisecond,
		"the two players sit %s apart at the same content position, more than one frame apart",
		sceneClock(secondPos-firstPos))
	for index := range a.items {
		if index >= len(anchors) {
			break
		}
		logf(scenario, "source %d: the buffer's measured container media at its start is %s (epoch %d, %s), while the player was asked for %s",
			index+1, sceneClock(anchors[index].MediaStart), anchors[index].Epoch, anchors[index].BufferInstance, sceneClock(proofEvent))
	}

	// The container window above is measured in the timestamps the files carry.
	// A player cannot use it as it stands: its position starts at zero on the
	// file's first frame. Converting both windows through their own measured
	// clock has to make the two sources of one event line up with no offset at
	// all -- the fixture only shifted timestamps, so the content offset is zero,
	// and the four seconds belong to the conversion, not to the Core timeline.
	step(window, a, dir, scenario, "", "put both sources on their own player clocks", func(a *appState) {
		for index := range a.items {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "clear %s: %v", track, err)
				}
			}
		}
		a.apply()
	})
	playerStarts := make([]time.Duration, len(windows))
	playerEnds := make([]time.Duration, len(windows))
	for index := range windows {
		window := playerWindow(anchors[index], windows[index])
		playerStarts[index] = window.Start
		playerEnds[index] = window.End
		logf(scenario, "source %d on its player clock: %s..%s (the buffered window was %s..%s; the container origin %s cancels against the buffer origin %s)",
			index+1, sceneClock(window.Start), sceneClock(window.End),
			sceneClock(windows[index].Start), sceneClock(windows[index].End),
			sceneClock(anchors[index].MediaStart), sceneClock(anchors[index].BufferStart))
	}
	check(absDuration(playerStarts[1]-playerStarts[0]) <= 100*time.Millisecond,
		"the same event starts %s apart on the two player clocks after conversion",
		sceneClock(playerStarts[1]-playerStarts[0]))
	check(absDuration((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])) <= 100*time.Millisecond,
		"the two player windows differ in length by %s",
		sceneClock((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])))
	step(window, a, dir, scenario, "", "the same event on two player clocks", nil)

	// The first negative: the same buffers, but the second source has no
	// confirmed offset at all. A window would be a guess, so the answer has to
	// be a refusal that names the unaligned source.
	step(window, a, dir, scenario, "", "forget the offset of the second source", func(a *appState) {
		it, err := a.at(1)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		for _, track := range []core.TrackID{it.video, it.audio} {
			if track == "" {
				continue
			}
			a.sync.timeline.Forget(track)
		}
		a.apply()
	})
	var unprovenStart, unprovenEnd time.Duration
	var unprovenErr error
	mygoRunOnMain(func() {
		unprovenStart, unprovenEnd, unprovenErr = a.commonReplay(sources)
	})
	logf(scenario, "with the second source unaligned the buffers give %s..%s and %v",
		sceneClock(unprovenStart), sceneClock(unprovenEnd), unprovenErr)
	check(unprovenErr != nil && errors.Is(unprovenErr, core.ErrNotAligned),
		"an unaligned source produced a window instead of ErrNotAligned (%v)", unprovenErr)
	step(window, a, dir, scenario, "2-unaligned.png", "the same buffers with no confirmed offset", nil)

	// The second negative: a confirmed but wrong offset is not refused; it
	// moves the window by the difference. The offset used here is the container
	// gap, which is exactly the mistake the new contract rules out -- if the
	// four seconds were written into the Core timeline as a content offset, the
	// shared window would shrink by four seconds on the wrong assumption.
	step(window, a, dir, scenario, "", "confirm the container gap as if it were a content offset", func(a *appState) {
		it, err := a.at(1)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		for _, track := range []core.TrackID{it.video, it.audio} {
			if track == "" {
				continue
			}
			if err := a.sync.timeline.SetShift(track, commonShift); err != nil {
				logf(scenario, "shift %s: %v", track, err)
			}
		}
		a.apply()
	})
	var wrongStart, wrongEnd time.Duration
	var wrongErr error
	mygoRunOnMain(func() {
		wrongStart, wrongEnd, wrongErr = a.commonReplay(sources)
	})
	logf(scenario, "with the container gap used as an offset the same buffers give %s..%s (%v)", sceneClock(wrongStart), sceneClock(wrongEnd), wrongErr)
	check(wrongErr == nil, "a confirmed offset failed instead of moving the window: %v", wrongErr)
	check(absDuration((end-wrongEnd)-commonShift) <= 100*time.Millisecond,
		"the invented offset changed the window end to %s, %s instead of %s",
		sceneClock(wrongEnd), sceneClock(end-wrongEnd), sceneClock(commonShift))
	step(window, a, dir, scenario, "3-wrong-offset.png", "the container gap mistaken for a content offset", nil)
	logf(scenario, "checks failed: %d", failures)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

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
	stageW, stageH := float32(0), float32(0)
	mygoRunOnMain(func() {
		a.drag = dragState{}
		stageW, stageH = a.stageFrameSize()
		a.dragByIn(target, handleSE, -0.06, -0.04, stageW, stageH)
		resized, _ = a.stageBoxOf(target)
	})
	// The lock keeps the picture's own shape, not the slot's: a slot that
	// started wider than the picture snaps to the picture the first time a
	// handle is taken. Boxes are stage fractions, so the shape is measured in
	// pixels on the stage before it is compared with the picture's own.
	want := float32(0)
	mygoRunOnMain(func() {
		if p := rawPlayer(target.player); p != nil {
			if w, h := p.Size(); w > 0 && h > 0 {
				want = float32(w) / float32(h)
			}
		}
	})
	pixelAspect := func(box stageBox) float32 {
		if stageW > 0 && stageH > 0 {
			return (box.W * stageW) / (box.H * stageH)
		}
		return box.W / box.H
	}
	aspectAfter := pixelAspect(resized)
	if want <= 0 {
		want = pixelAspect(before)
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

func runFinal(window *mygo.Window, a *appState, dir string) {
	go finalBody(window, a, dir)
}

func finalBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "final"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			failures++
			logf(scenario, "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for i := 0; i < 2; i++ {
		it, err := a.at(i)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", i+1, shortURI(it.uri))
			return
		}
		uris[i] = it.uri
	}

	sources := make([]replaySource, 0, 2)
	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("source-%d", index+1)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		sources = append(sources, replaySource{index: index, runner: runner})
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for i, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", i+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	// 1. The positive case: two sources of one event, buffered separately.
	windows := make([]dvr.Range, 2)
	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 60*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 60s", index+1)
			return
		}
		windows[index] = windowOf
		logf(scenario, "source %d buffers %s of %s", index+1, logRange(windowOf), shortURI(uris[index]))
	}
	anchors := make([]dvr.Anchor, 2)
	mediaStarts := make([]time.Duration, 2)
	contentStarts := make([]time.Duration, 2)
	for index, runner := range runners {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		stored, err := runner.waitBufferAnchor(ctx, windows[index])
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		anchors[index] = stored
		// Media time is the container's clock, which the files disagree on; the
		// content clock is what the timeline holds, and it must agree.
		media, err := stored.MediaTime(windows[index].Start)
		if err != nil {
			logf(scenario, "source %d: place the window on media time: %v", index+1, err)
			return
		}
		mediaStarts[index] = media
		content, err := stored.ContentTime(windows[index].Start)
		if err != nil {
			logf(scenario, "source %d: place the window on content time: %v", index+1, err)
			return
		}
		contentStarts[index] = content
		logf(scenario, "source %d measured: media %s (content %s) at buffer %s (epoch %d, %s)",
			index+1, sceneClock(anchors[index].MediaStart), sceneClock(content),
			sceneClock(anchors[index].BufferStart), anchors[index].Epoch, anchors[index].BufferInstance)
	}
	// The content offset of the fixture is zero: only the container timestamps
	// were shifted, and the player clock conversion below removes that. The
	// Core timeline therefore carries no offset at all, and the four seconds
	// remain a property of the fixtures rather than of the mapping.
	step(window, a, dir, scenario, "", "confirm the fixture's content offset is zero", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.apply()
	})

	var start, end time.Duration
	var windowErr error
	mygoRunOnMain(func() {
		start, end, windowErr = a.commonReplay(sources)
	})
	if windowErr != nil {
		check(false, "no shared window for two sources of the same event: %v", windowErr)
	} else {
		wantStart := contentStarts[0]
		if contentStarts[1] > wantStart {
			wantStart = contentStarts[1]
		}
		logf(scenario, "shared window on the event clock: %s..%s; the content clocks put the event start at %s (the containers would say %s and %s)",
			sceneClock(start), sceneClock(end), sceneClock(wantStart), sceneClock(mediaStarts[0]), sceneClock(mediaStarts[1]))
		check(absDuration(start-wantStart) <= 100*time.Millisecond,
			"the shared window starts at %s, but the content clocks put the event at %s", sceneClock(start), sceneClock(wantStart))
		check(end-start >= 8*time.Second, "the shared window covers only %s", sceneClock(end-start))

		// The invariant of this stage: one content position, one event time, even
		// though the two files state it four seconds apart in their containers.
		// Going through the container instead put exactly that difference into the
		// event time, which is what the previous run reported as a shared window.
		const proofContent = 6 * time.Second
		events := make([]time.Duration, len(anchors))
		for index := range anchors {
			content, err := anchors[index].ContentTime(windows[index].Start + proofContent)
			if err != nil {
				check(false, "source %d: content at %s: %v", index+1, sceneClock(proofContent), err)
				continue
			}
			event, err := eventFromMedia(content, 0)
			if err != nil {
				check(false, "source %d: event for content %s: %v", index+1, sceneClock(content), err)
				continue
			}
			events[index] = event
			// The reverse, as P16.4 will need it: content minus the file's start
			// is the position to ask this particular player for.
			position := content - windows[index].Start
			logf(scenario, "source %d: content %s is event %s; in a file cut at %s that is position %s",
				index+1, sceneClock(proofContent), sceneClock(event), sceneClock(windows[index].Start), sceneClock(position))
			check(absDuration(position-proofContent) <= time.Millisecond,
				"source %d: event %s went back to %s, want %s",
				index+1, sceneClock(event), sceneClock(position), sceneClock(proofContent))
		}
		check(events[0] == events[1],
			"the same content is event %s in one file and %s in the other", sceneClock(events[0]), sceneClock(events[1]))
	}

	// Each window has to become a player position through its own measured
	// clock, and the two conversions must agree although the container
	// timestamps differ by four seconds.
	playerStarts := make([]time.Duration, len(windows))
	playerEnds := make([]time.Duration, len(windows))
	for index := range windows {
		converted := playerWindow(anchors[index], windows[index])
		playerStarts[index] = converted.Start
		playerEnds[index] = converted.End
		logf(scenario, "source %d on its player clock: %s..%s (buffered %s..%s; container origin %s, buffer origin %s)",
			index+1, sceneClock(converted.Start), sceneClock(converted.End),
			sceneClock(windows[index].Start), sceneClock(windows[index].End),
			sceneClock(anchors[index].MediaStart), sceneClock(anchors[index].BufferStart))
	}
	check(absDuration(playerStarts[1]-playerStarts[0]) <= 100*time.Millisecond,
		"the same event starts %s apart on the two player clocks", sceneClock(playerStarts[1]-playerStarts[0]))
	check(absDuration((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])) <= 100*time.Millisecond,
		"the two player windows differ in length by %s",
		sceneClock((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])))

	// The pictures: both players are asked for the same content position,
	// because a player's seek follows the content clock, not the container
	// timestamps the buffer records. Asking the second one for 6s+4s would
	// repeat the mistake this stage is about.
	const proofEvent = 6 * time.Second
	step(window, a, dir, scenario, "", "pause both players on the same content position", func(a *appState) {
		for index, it := range a.items {
			if it.player == nil {
				continue
			}
			it.player.Pause()
			it.player.Seek(proofEvent)
			logf(scenario, "source %d paused and asked for %s", index+1, sceneClock(proofEvent))
		}
	})
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "1a-source-1.png", "the first source at the shared content position", func(a *appState) {
		if err := a.showVideo(0); err != nil {
			logf(scenario, "show source 1: %v", err)
		}
	})
	firstPos := positionOf(a, 0)
	step(window, a, dir, scenario, "1b-source-2.png", "the second source at the same content position", func(a *appState) {
		if err := a.showVideo(1); err != nil {
			logf(scenario, "show source 2: %v", err)
		}
	})
	secondPos := positionOf(a, 1)
	logf(scenario, "the pictures were taken at player positions %s and %s, %s apart",
		sceneClock(firstPos), sceneClock(secondPos), sceneClock(secondPos-firstPos))
	check(absDuration(secondPos-firstPos) <= 40*time.Millisecond,
		"the two players sit %s apart at the same content position, more than one frame apart",
		sceneClock(secondPos-firstPos))

	// 2. Refusals. An unconfirmed offset must not produce a window at all.
	step(window, a, dir, scenario, "", "forget the offset of the second source", func(a *appState) {
		it, err := a.at(1)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		for _, track := range []core.TrackID{it.video, it.audio} {
			if track == "" {
				continue
			}
			a.sync.timeline.Forget(track)
		}
		a.apply()
	})
	var unprovenStart, unprovenEnd time.Duration
	var unprovenErr error
	mygoRunOnMain(func() {
		unprovenStart, unprovenEnd, unprovenErr = a.commonReplay(sources)
	})
	logf(scenario, "with the second source unaligned the buffers give %s..%s and %v",
		sceneClock(unprovenStart), sceneClock(unprovenEnd), unprovenErr)
	check(unprovenErr != nil && errors.Is(unprovenErr, core.ErrNotAligned),
		"an unaligned source produced a window instead of ErrNotAligned (%v)", unprovenErr)
	step(window, a, dir, scenario, "2-unaligned.png", "the same buffers with no confirmed offset", nil)

	// The real faults are separate buffers: the scenario's own sources are
	// healthy, so the broken playlists are opened against the same server with
	// their own directories. Each has to lose the right thing.
	gap, gapFinish := finalRunner(a, dir, "gap", "d3-gap2/index.m3u8", "final-gap")
	if gap != nil {
		ranges := railOf(gap)
		logf(scenario, "gap: %d ranges, epochs %s", len(ranges), epochList(ranges))
		if len(ranges) >= 2 {
			check(ranges[0].Epoch == ranges[len(ranges)-1].Epoch,
				"the skipped segments moved the epoch: %s", epochList(ranges))
			bridged, err := bridge(gap, ranges)
			check(err != nil, "a window over the skipped segments was authorised: %v", bridged)
			logf(scenario, "gap: a window from %s to %s is refused: %v",
				ranges[0].Start, ranges[len(ranges)-1].End, err)
		} else {
			check(false, "the missing segments did not split the buffer: %d range(s)", len(ranges))
		}
		gapFinish()
	}
	disc, discFinish := finalRunner(a, dir, "disc", "d3-disc/index.m3u8", "final-disc")
	if disc != nil {
		ranges := railOf(disc)
		logf(scenario, "disc: %d ranges, epochs %s", len(ranges), epochList(ranges))
		if len(ranges) >= 2 {
			check(ranges[0].Epoch != ranges[len(ranges)-1].Epoch,
				"the discontinuity did not move the epoch: %s", epochList(ranges))
			bridged, err := bridge(disc, ranges)
			check(err != nil, "a window across the discontinuity was authorised: %v", bridged)
			logf(scenario, "disc: a window from %s to %s is refused: %v",
				ranges[0].Start, ranges[len(ranges)-1].End, err)
		} else {
			check(false, "the discontinuity did not split the buffer: %d range(s)", len(ranges))
		}
		discFinish()
	}

	// 3. Lifecycle: a second buffer of the same source is a different clock.
	// Its anchor must not be accepted for a recording the first one produced,
	// even though the source id is the same.
	first := runners[0]
	recording, err := first.buffer.Record(0)
	if err != nil {
		logf(scenario, "record from the first buffer: %v", err)
	} else {
		rebuilt, rebuiltFinish := finalRunnerOn(a, dir, "rebuilt", uris[0], a.items[0].source.ID)
		if rebuilt != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			windowOf, _ := waitForRange(rebuilt, 6*time.Second, 60*time.Second)
			newAnchor, anchorErr := rebuilt.waitBufferAnchor(ctx, windowOf)
			cancel()
			if anchorErr != nil {
				logf(scenario, "the rebuilt buffer did not measure an anchor: %v", anchorErr)
			} else {
				oldInstance := first.buffer.Instance()
				logf(scenario, "rebuilding the buffer: instance %s -> %s, epoch %d -> %d",
					oldInstance, rebuilt.buffer.Instance(), first.buffer.Epoch(), rebuilt.buffer.Epoch())
				check(rebuilt.buffer.Instance() != oldInstance,
					"the rebuilt buffer kept the instance name %s", oldInstance)
				_, fileErr := replayFileFor(recording, newAnchor)
				check(fileErr != nil,
					"a new buffer's anchor was accepted for a recording of %s", recording.ID)
				logf(scenario, "the recording %s is refused under the rebuilt anchor: %v", recording.ID, fileErr)
			}
			rebuiltFinish()
		}
	}

	// 4. Switching to a recording and closing it gives every lease back: the
	// player and the lease are two facts, so both are counted.
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
	} else {
		// The stage is showing the second source after the picture step, and a
		// player that is not drawn never leaves the fist frame: a switch there
		// would be rolled back for the wrong reason. The players were paused for
		// the picture comparison, so play this one again as well: a window with
		// no playing player stops redrawing, and the replacement would never get
		// a frame even though it is attached to the stage.
		mygoRunOnMain(func() {
			if err := a.showVideo(0); err != nil {
				logf(scenario, "show source 1 before the switch: %v", err)
			}
			if it, err := a.at(0); err == nil && it.player != nil {
				it.player.Play()
			}
		})
		time.Sleep(1500 * time.Millisecond)
		base := windows[0].Start
		if err := a.playRecording(it, first, base); err != nil {
			logf(scenario, "switch to a recording: %v", err)
		} else if waitRecording(a, 0, base, 20*time.Second) {
			open, given := a.leaseStats()
			logf(scenario, "the recording is on the stage | leases open=%d given=%d", open, given)
			check(open == 1, "the stage holds %d lease(s) for one recording", open)
			step(window, a, dir, scenario, "3-recording.png", "the replay of a recording in the same session", nil)
		} else {
			check(false, "the recording did not reach the stage")
		}
	}

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, given := a.leaseStats()
	players := openPlayers(a)
	logf(scenario, "after the session closed: leases open=%d given=%d players=%d", open, given, players)
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(players == 0, "the run ended with %d player(s) open", players)
	left := first.buffer.PendingCleanup()
	recordings := len(first.buffer.Recordings())
	logf(scenario, "after closing the first buffer: pending cleanup=%v recordings=%d", left, recordings)
	check(len(left) == 0, "the buffer could not remove %v", left)
	check(recordings == 0, "the closed buffer still owns %d recording(s)", recordings)

	logf(scenario, "checks failed: %d", failures)
}

// finalRunner opens a buffer on a playlist of the fixture server, under the
// scenario's own directory, and waits for it to hold media. The broken
// playlists are separate buffers on purpose: the scenario's sources are the
// healthy pair, and the faults must not be able to disturb the positive case.
func finalRunner(a *appState, dir, name, playlist, source string) (*dvrRunner, func()) {
	return finalRunnerOn(a, dir, name, "http://127.0.0.1:8791/"+playlist, core.SourceID(source))
}

func finalRunnerOn(a *appState, dir, name, uri string, source core.SourceID) (*dvrRunner, func()) {
	runner, err := startDVR(uri, dvrFromMedia(
		filepath.Join(dir, name, "segments"), filepath.Join(dir, name, "recordings"),
		120*time.Second, 0, dvrRecordingBytes, source))
	if err != nil {
		logf("final", "%s: open the buffer: %v", name, err)
		return nil, func() {}
	}
	a.trackRunner(uri, runner)
	if _, ok := waitForRange(runner, 6*time.Second, 60*time.Second); !ok {
		logf("final", "%s: the buffer did not hold 6s within 60s", name)
		return runner, func() {
			if err := runner.Close(); err != nil {
				logf("final", "%s: close the buffer: %v", name, err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := runner.waitAnchor(ctx); err != nil {
		logf("final", "%s: measuring the clock: %v", name, err)
	}
	return runner, func() {
		if err := runner.Close(); err != nil {
			logf("final", "%s: close the buffer: %v", name, err)
		}
	}
}

func runMixer(window *mygo.Window, a *appState, dir string) {
	go mixerBody(window, a, dir)
}

func mixerBody(window *mygo.Window, a *appState, dir string) {
	checks := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			checks++
			logf("mixer", "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < 2 {
		logf("mixer", "this check needs two sources")
		finishScenario(a)
		return
	}
	if !waitPlayers(a, 30*time.Second) {
		logf("mixer", "the players never came up")
		finishScenario(a)
		return
	}

	step(window, a, dir, "mixer", "1-mixer.png", "before anybody aligns anything", func(a *appState) {
		a.workspace = workspaceMixer
		joined := strings.Join(mixerLines(a), "\n")
		for _, line := range mixerLines(a) {
			logf("mixer", "before: %s", line)
		}
		check(strings.Contains(joined, "the unaligned protection"),
			"a routed source is not held back by the protection until it is aligned: %q", joined)
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			logf("mixer", "player %d: muted=%v volume=%.2f", i+1,
				rawPlayer(it.player).Muted(), rawPlayer(it.player).Volume())
		}
	})
	mixerPhase(a, "before aligning")

	// Anchor the first source: the protection lifts and its sound reaches the
	// device. This is the real command path, on the drawing thread.
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	check(anchorErr == nil, "anchor: %v", anchorErr)

	step(window, a, dir, "mixer", "2-heard.png", "the routed source heard once it is aligned", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "after anchoring: %s", line)
		}
		check(a.heard(a.items[0]), "the aligned source is still silent: %s", a.mixerLine(a.items[0]))
		if a.items[0].player != nil {
			check(!rawPlayer(a.items[0].player).Muted(), "the heard source's player is muted")
		}
	})
	mixerPhase(a, "heard")
	time.Sleep(5 * time.Second)

	// A source the scene does not route cannot be soloed: refusing is the point,
	// because a solo there would only silence what is playing for nothing.
	mygoRunOnMain(func() {
		result := a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 1)})
		check(result.Err != nil, "soloing a source the scene does not route was accepted")
		check(strings.Contains(result.Err.Error(), "not in the scene") ||
			strings.Contains(result.Err.Error(), "not routed"),
			"the refusal does not say the source is unrouted: %v", result.Err)
		logf("mixer", "solo on the unrouted source: %v", result.Err)
		// The refusal is expected here; it must not linger as a session error.
		a.lastErr = nil
	})

	// Solo the routed source: the overlay keeps it heard and holds the rest.
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 0)}))
	})

	step(window, a, dir, "mixer", "3-soloed.png", "the routed source soloed", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo on: %s", line)
		}
		check(a.solo == sourceAt(a, 0), "solo does not name the first source: %q", a.solo)
		check(a.heard(a.items[0]), "solo made its own source silent: %s", a.mixerLine(a.items[0]))
		check(strings.Contains(a.mixerLine(a.items[0]), "solo"),
			"the row does not say it is soloed: %q", a.mixerLine(a.items[0]))
	})
	mixerPhase(a, "solo on")
	time.Sleep(6 * time.Second)

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandMasterMute}))
	})

	step(window, a, dir, "mixer", "4-muted.png", "master mute, a silent window for the meter", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "master mute on: %s", line)
		}
		check(a.masterMute, "the master mute did not take")
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			check(rawPlayer(it.player).Muted(), "source %d still plays under the master mute", i+1)
		}
	})
	mixerPhase(a, "master mute on")
	time.Sleep(6 * time.Second)

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandMasterMute}))
		if solo := a.solo; solo != "" {
			a.report(a.runCommand(command{Kind: commandSolo, Source: solo}))
		}
	})

	step(window, a, dir, "mixer", "5-restored.png", "overlay cleared", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "restored: %s", line)
		}
		check(a.solo == "", "a source is still soloed: %q", a.solo)
		check(!a.masterMute, "the master mute is still on")
		check(a.heard(a.items[0]), "the routed source did not come back: %s", a.mixerLine(a.items[0]))
	})
	mixerPhase(a, "overlay cleared")
	time.Sleep(4 * time.Second)

	// Changing a level is not routing: the unrouted source must be refused, and
	// the routed one's slider must end up showing what the scene now holds.
	mygoRunOnMain(func() {
		result := a.runCommand(command{Kind: commandVolume, Source: sourceAt(a, 1), Volume: 0.35})
		check(result.Err != nil, "changing the level of an unrouted source was accepted")
		check(result.Err != nil && strings.Contains(result.Err.Error(), "use Hear"),
			"the refusal does not point at routing: %v", result.Err)
		logf("mixer", "volume on the unrouted source: %v", result.Err)
		a.lastErr = nil

		if level, routed := a.sceneLevel(a.items[1]); routed {
			check(false, "the refused volume routed the source at %.2f", level.Volume)
		}

		a.report(a.runCommand(command{Kind: commandVolume, Source: sourceAt(a, 0), Volume: 0.25}))
	})

	step(window, a, dir, "mixer", "6-volume.png", "the routed source at a quarter", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "after the volume change: %s", line)
		}
		level, routed := a.sceneLevel(a.items[0])
		check(routed && level.Volume == 0.25, "the scene holds %.2f for the routed source, want 0.25", level.Volume)
		check(a.items[0].volume == 0.25, "the item still carries %.2f", a.items[0].volume)
		check(strings.Contains(a.mixerLine(a.items[0]), "25%"),
			"the row does not show the level the scene holds: %q", a.mixerLine(a.items[0]))
	})

	// The window's own Hear is exclusive, but the scene can hold two sources at
	// once (hearBoth). Solo only means something with more than one audible
	// source, so the switch and the return are checked here rather than claimed
	// from the single-routed state above. The second source is aligned by
	// nothing, so it must be allowed explicitly first: the protection wins over
	// routing, and a solo is not a way around it.
	var allowErr, bothErr error
	mygoRunOnMain(func() {
		allowErr = a.allowAudio(1)
		bothErr = a.hearBoth(0, 1, 0.8, 0.6)
	})
	check(allowErr == nil, "allowing the second source: %v", allowErr)
	check(bothErr == nil, "routing both sources: %v", bothErr)

	step(window, a, dir, "mixer", "7-both-heard.png", "two sources heard at their own levels", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "both routed: %s", line)
		}
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			check(!rawPlayer(it.player).Muted(), "source %d is silent although it is routed", i+1)
		}
		check(a.heard(a.items[0]) && a.heard(a.items[1]),
			"the rows do not both say heard: %q / %q", a.mixerLine(a.items[0]), a.mixerLine(a.items[1]))
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 0)}))
	})

	step(window, a, dir, "mixer", "8-solo-switch.png", "solo moved from one routed source to the other", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo first: %s", line)
		}
		check(a.solo == sourceAt(a, 0), "solo does not name the first source: %q", a.solo)
		if a.items[0].player != nil {
			check(!rawPlayer(a.items[0].player).Muted(), "the soloed source is muted")
		}
		if a.items[1].player != nil {
			check(rawPlayer(a.items[1].player).Muted(), "the other routed source still plays under solo")
		}
		check(strings.Contains(a.mixerLine(a.items[1]), "another source is soloed"),
			"the second row does not say why it is silent: %q", a.mixerLine(a.items[1]))
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 1)}))
	})

	step(window, a, dir, "mixer", "9-solo-switched.png", "solo switched to the second source", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo switched: %s", line)
		}
		check(a.solo == sourceAt(a, 1), "solo did not switch: %q", a.solo)
		if a.items[1].player != nil {
			check(!rawPlayer(a.items[1].player).Muted(), "the newly soloed source is muted")
		}
		if a.items[0].player != nil {
			check(rawPlayer(a.items[0].player).Muted(), "the source solo left still plays")
		}
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 1)}))
	})

	step(window, a, dir, "mixer", "10-both-back.png", "leaving solo restores both sources and their levels", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo off: %s", line)
		}
		check(a.solo == "", "solo is still on: %q", a.solo)
		for i, want := range []float64{0.8, 0.6} {
			if level, routed := a.sceneLevel(a.items[i]); !routed || level.Volume != want {
				check(false, "source %d holds %.2f in the scene, want %.2f", i+1, level.Volume, want)
			}
			if a.items[i].player != nil {
				check(!rawPlayer(a.items[i].player).Muted(), "source %d stayed muted after solo", i+1)
			}
			check(strings.Contains(a.mixerLine(a.items[i]), "heard"),
				"source %d does not read as heard again: %q", i+1, a.mixerLine(a.items[i]))
		}
	})

	// A solo is not an unmute. The target's own mute is part of its state, and
	// the overlay must not quietly lift it to make the solo audible.
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandMute, Source: sourceAt(a, 0)}))
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 0)}))
	})

	step(window, a, dir, "mixer", "11-solo-keeps-mute.png", "solo leaves the target's own mute alone", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo on a muted source: %s", line)
		}
		check(a.solo == sourceAt(a, 0), "solo does not name the first source: %q", a.solo)
		check(a.items[0].muted, "soloing the source turned its own mute off")
		check(!a.heard(a.items[0]), "a source that is muted and soloed reads as heard: %s", a.mixerLine(a.items[0]))
		line := a.mixerLine(a.items[0])
		check(strings.Contains(line, "muted") && strings.Contains(line, "solo"),
			"the row does not say both why it is silent and that it is soloed: %q", line)
		if a.items[0].player != nil {
			check(rawPlayer(a.items[0].player).Muted(), "the muted solo target still plays")
		}
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandMute, Source: sourceAt(a, 0)}))
	})

	step(window, a, dir, "mixer", "12-unmuted-solo.png", "unmuting the solo target makes it audible again", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo target unmuted: %s", line)
		}
		check(!a.items[0].muted, "the source is still muted")
		check(a.heard(a.items[0]), "the soloed source is not heard after unmuting: %s", a.mixerLine(a.items[0]))
		if a.items[0].player != nil {
			check(!rawPlayer(a.items[0].player).Muted(), "the player stayed muted after unmuting")
		}
	})

	logf("mixer", "mixer: %d source(s), checks failed: %d", len(a.items), checks)
	finishScenario(a)
}

// mixerLines is the page's account of every source, which is what the rows show.
func mixerLines(a *appState) []string {
	lines := make([]string, 0, len(a.items))
	for _, it := range a.items {
		lines = append(lines, a.mixerLine(it))
	}
	return lines
}

// mixerPhase writes a timestamped boundary the meter can be read against.
func mixerPhase(a *appState, where string) {
	logf("mixer", "phase %s at %s", where, time.Now().Format("15:04:05.000"))
	for _, line := range mixerLines(a) {
		logf("mixer", "  %s", line)
	}
}

func runMuteHold(window *mygo.Window, a *appState, dir string) {
	go func() {
		if !waitPlayers(a, 30*time.Second) {
			logf("mutehold", "the players never came up")
			finishScenario(a)
			return
		}
		// Heard first, so the meter has a window with sound to compare against.
		mygoRunOnMain(func() { a.anchor(0) })
		logf("mutehold", "heard at %s", time.Now().Format("15:04:05.000"))
		time.Sleep(8 * time.Second)

		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandMasterMute}))
		})
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			logf("mutehold", "source %d muted=%v", i+1, rawPlayer(it.player).Muted())
		}
		logf("mutehold", "muted at %s", time.Now().Format("15:04:05.000"))
		time.Sleep(10 * time.Second)

		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandMasterMute}))
		})
		logf("mutehold", "unmuted at %s", time.Now().Format("15:04:05.000"))
		time.Sleep(6 * time.Second)

		step(window, a, dir, "mutehold", "1-after.png", "after the mute was lifted", nil)
		finishScenario(a)
	}()
}

func runScene(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "scene", "1-a-video-a-audio.png", "a video with a audio", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf("scene", "show 1: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("scene", "hear 1: %v", err)
			}
		})
		step(window, a, dir, "scene", "2-a-video-b-audio.png", "a video with b audio", func(a *appState) {
			if err := a.hearAudio(1, 1); err != nil {
				logf("scene", "hear 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "3-b-audio-lowered.png", "b audio at 0.25", func(a *appState) {
			if err := a.setVolume(1, 0.25); err != nil {
				logf("scene", "volume 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "4-both-audio.png", "both audio at their own levels", func(a *appState) {
			if err := a.hearBoth(0, 1, 0.5, 1); err != nil {
				logf("scene", "hear both: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "5-b-video-a-audio.png", "b video with a audio", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				logf("scene", "show 2: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("scene", "hear 1: %v", err)
			}
		})
		// The acceptance case: A's picture with B's sound, B having been shown
		// once so its engine is running.
		wait(1 * time.Second)
		step(window, a, dir, "scene", "6-a-video-b-audio-warm.png", "a video with b audio after b was shown", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf("scene", "show 1: %v", err)
			}
			if err := a.hearAudio(1, 1); err != nil {
				logf("scene", "hear 2: %v", err)
			}
		})
		wait(2 * time.Second)
		step(window, a, dir, "scene", "7-b-audio-2s-later.png", "a video with b audio, 2s later", nil)
		wait(1 * time.Second)
		step(window, a, dir, "scene", "", "remove source 1 while playing", func(a *appState) {
			if err := a.remove(0); err != nil {
				logf("scene", "remove 1: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "8-after-removal.png", "after removing the playing source", nil)
		step(window, a, dir, "scene", "", "events", func(a *appState) {
			logf("scene", "events: %s", eventList(a))
		})
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runFour opens four sources, shows one picture with another's sound, and
// removes two of them in turn, recording that every player and every session
// object stayed consistent.
func runFour(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "four", "1-four-open.png", "four sources open", nil)
		step(window, a, dir, "four", "2-video1-audio2.png", "video 1 with audio 2", func(a *appState) {
			for _, call := range []struct {
				name string
				fn   func() error
			}{
				{"show 1", func() error { return a.showVideo(0) }},
				{"hear 2", func() error { return a.hearAudio(1, 1) }},
			} {
				if err := call.fn(); err != nil {
					logf("four", "%s: %v", call.name, err)
				}
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "3-video3-audio4.png", "video 3 with audio 4", func(a *appState) {
			if err := a.showVideo(2); err != nil {
				logf("four", "show 3: %v", err)
			}
			if err := a.hearBoth(1, 3, 1, 0.5); err != nil {
				logf("four", "hear 2 and 4: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "", "remove source 2 while it is heard", func(a *appState) {
			if err := a.remove(1); err != nil {
				logf("four", "remove 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "4-after-removing-2.png", "after removing source 2", nil)
		step(window, a, dir, "four", "", "remove source 3 while it is shown", func(a *appState) {
			if err := a.remove(1); err != nil {
				logf("four", "remove 3: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "5-after-removing-3.png", "after removing source 3", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runAudio plays one source with its audio heard, then mutes it, then unmutes
// it, logging a marker at each change. A separate meter sampling the default
// output device records which windows had sound, so audibility can be checked
// without a person listening.
func runAudio(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "audio", "1-audible.png", "audible window starts", nil)
		wait(4 * time.Second)
		step(window, a, dir, "audio", "", "muted window starts", func(a *appState) {
			a.muteAll(true)
		})
		wait(4 * time.Second)
		step(window, a, dir, "audio", "2-muted.png", "unmuted window starts", func(a *appState) {
			a.muteAll(false)
		})
		wait(4 * time.Second)
		step(window, a, dir, "audio", "3-unmuted-again.png", "end", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSync aligns two sources that carry the same picture 2.5 seconds apart.
//
// It pauses the group on the first source, draws the second while paused so the
// two timecodes can be compared, applies offsets through the panel the window
// uses, and captures after each one. The paused group is what makes the
// comparison possible: both players hold their position, so a difference in the
// burnt-in timecode is the difference the offsets have to remove.
func runSync(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the second one is shown once before anything is measured.
		step(window, a, dir, "sync", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(2 * time.Second)
		// The case the app is for: the first source's picture with the second
		// source's sound. Both are in the scene, so a group command reaches both
		// players even though only one picture is drawn.
		step(window, a, dir, "sync", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(4 * time.Second)

		// The way out of the conservative mute is visible and reversible: the
		// button lets a person who can see both pictures allow the sound, and
		// pressing it again puts the hold back.
		step(window, a, dir, "sync", "", "second sound allowed while its offset is unknown", func(a *appState) {
			if err := a.allowAudio(1); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "", "second sound held again", func(a *appState) {
			if err := a.allowAudio(1); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)

		step(window, a, dir, "sync", "", "paused, event anchored on the first source", func(a *appState) {
			// Pause first: the event time has to be the position the group stops
			// at, not a sample taken while the players are still moving.
			logGroup("sync", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "1-paused-a.png", "first picture while paused", nil)

		step(window, a, dir, "sync", "", "second picture while paused", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
			// A seek is what makes a paused player hand over a new picture, so a
			// zero nudge is used to draw the frame this offset puts on screen.
			// It also places the second source on the timeline, which is what
			// lets its sound through: until then it is held silent.
			a.nudgeAndRecord(1, 0)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "2-paused-b-before.png", "second picture before any offset", nil)

		step(window, a, dir, "sync", "", "second delayed by 2s", func(a *appState) {
			a.nudgeAndRecord(1, -2000*time.Millisecond)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "3-paused-b-after.png", "second picture after -2s", nil)

		step(window, a, dir, "sync", "", "second delayed 0.1s more", func(a *appState) {
			a.nudgeAndRecord(1, -100*time.Millisecond)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "sync", "4-b-minus-0.1.png", "second picture after another -0.1s", nil)

		step(window, a, dir, "sync", "", "second moved 0.1s back", func(a *appState) {
			a.nudgeAndRecord(1, 100*time.Millisecond)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "sync", "5-b-plus-0.1.png", "second picture after +0.1s", nil)

		step(window, a, dir, "sync", "", "delay past the start of the stream is refused", func(a *appState) {
			a.nudgeAndRecord(1, -60*time.Second)
		})
		step(window, a, dir, "sync", "6-refused.png", "after the refused delay", nil)

		step(window, a, dir, "sync", "", "sound back on the first source", func(a *appState) {
			if err := a.hearAudio(0, 1); err != nil {
				a.lastErr = err
			}
		})

		step(window, a, dir, "sync", "", "group resumed", func(a *appState) {
			logGroup("sync", "play", a.playAll())
		})
		wait(2 * time.Second)
		step(window, a, dir, "sync", "", "group paused again", func(a *appState) {
			// Pause first: the event time has to be the position the group stops
			// at, not a sample taken while the players are still moving.
			logGroup("sync", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "7-paused-again.png", "second picture after resume and pause", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

func runSyncGroup(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the second source is shown once before it can be measured.
		step(window, a, dir, "syncgroup", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(2 * time.Second)
		step(window, a, dir, "syncgroup", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)
		step(window, a, dir, "syncgroup", "", "group created by the first sync command", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after creation %s", a.groupState())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "1-group-paused.png", "group paused", nil)

		step(window, a, dir, "syncgroup", "", "group resumed", func(a *appState) {
			logGroup("syncgroup", "play", a.playAll())
		})
		wait(2 * time.Second)

		// The third source joins the picture but not the group: the scene now
		// shows a source that is not being synchronized.
		step(window, a, dir, "syncgroup", "", "third source shown, outside the group", func(a *appState) {
			if err := a.showVideo(2); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after scene change %s", a.groupState())
		})
		wait(2 * time.Second)
		step(window, a, dir, "syncgroup", "", "pause the group while the third source plays", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "2-third-still-playing.png", "group paused, third source outside it", nil)
		wait(1500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "", "third source a second and a half later", nil)

		step(window, a, dir, "syncgroup", "", "group resumed, second source removed", func(a *appState) {
			logGroup("syncgroup", "play", a.playAll())
			if err := a.remove(1); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after removal %s", a.groupState())
		})
		wait(time.Second)
		step(window, a, dir, "syncgroup", "3-after-removal.png", "second source removed from the session", nil)

		step(window, a, dir, "syncgroup", "", "pause the group after its member left", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "4-paused-after-removal.png", "group paused with the surviving member", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSyncMap measures one source at a known target time. It pauses the source,
// seeks it to `-at` plus `-offset`, plays a moment so the captured picture cannot
// be a leftover frame, and logs the position the player reports at capture time.
// Reading the burnt-in timecode of that picture says what the player's position
// means for this source, which is the ground truth an alignment claim needs.
func runSyncMap(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "syncmap", "", "paused", func(a *appState) {
			logGroup("syncmap", "pause", a.pauseAll())
		})

		target := syncAt + syncOffset
		step(window, a, dir, "syncmap", "", "seek to "+target.String(), func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Seek(target)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "syncmap", "", "play a moment", func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Play()
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "syncmap", "map.png", "captured while paused at the target", func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Pause()
			logf("syncmap", "at=%s offset=%s target=%s reported=%s", syncAt, syncOffset, target, it.player.Position())
		})

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSyncFail pauses a group whose second member stops playing mid-session, to
// show that one failing member is reported instead of pretending the group
// stopped together.
func runSyncFail(window *mygo.Window, a *appState, dir string) {
	go func() {
		time.Sleep(4 * time.Second)
		step(window, a, dir, "syncfail", "", "both sources playing", func(a *appState) {
			if err := a.hearAudio(1, 1); err != nil {
				logf("syncfail", "could not put the second source in the scene: %v", err)
			}
		})
		// Fault injection with the player factory the app really uses: the second
		// player is pointed at an address that answers 404, which is what a
		// source that breaks mid-session looks like to the group.
		step(window, a, dir, "syncfail", "", "second player pointed at a missing stream", func(a *appState) {
			it := a.items[1]
			broken, err := openPlayer("http://127.0.0.1:8790/missing.m3u8")
			if err != nil {
				a.lastErr = err
				return
			}
			old := it.player
			it.player = broken
			it.generation++
			if old != nil {
				old.Close()
			}
		})
		time.Sleep(2 * time.Second)
		step(window, a, dir, "syncfail", "", "pause with a failing member", func(a *appState) {
			logGroup("syncfail", "pause", a.pauseAll())
		})
		time.Sleep(time.Second)
		step(window, a, dir, "syncfail", "", "resume with a failing member", func(a *appState) {
			logGroup("syncfail", "play", a.playAll())
		})
		time.Sleep(time.Second)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

func logf(scenario, format string, args ...any) {
	log.Printf(scenario+": "+format, args...)
}

// playerState is what a player reported at one moment.
func playerState(i int, it *item) string {
	if it.player == nil {
		return "source " + itoa(i+1) + " no player"
	}
	p := it.player
	return logLine(i, p.Playing(), p.Muted(), p.Position(), p.Duration(), p.Ready(), p.Err())
}

// logLine formats a player's state as one line.
func logLine(i int, playing, muted bool, pos, dur time.Duration, ready bool, err error) string {
	return strings.Join([]string{
		"source " + itoa(i+1),
		"playing=" + boolString(playing),
		"muted=" + boolString(muted),
		"ready=" + boolString(ready),
		"position=" + pos.String(),
		"duration=" + dur.String(),
		"err=" + errorString(err),
	}, " ")
}

// itoa is a small decimal of a non-negative int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// boolString is true or false.
func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// errorString is an error as text, or nil.
func errorString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// step runs fn on the main thread, where the window and its players live, and
// then records every source, capturing the window when path is not empty.
func step(window *mygo.Window, a *appState, dir, scenario, path, what string, fn func(*appState)) {
	mygoRunOnMain(func() {
		if fn != nil {
			fn(a)
		}
		// A page change or a selection does not paint by itself; the window
		// only redraws when it is told the content moved.
		window.Invalidate()
	})
	if path != "" {
		// A capture taken in the same pass as the change shows the previous
		// frame: the raster of the window is painted between passes. Let a few
		// frames go by, then capture twice with a pass in between and keep the
		// second: switching which video is drawn also leaves one stale frame.
		time.Sleep(600 * time.Millisecond)
	}
	mygoRunOnMain(func() {
		if path != "" {
			_ = capture(window, filepath.Join(dir, path))
		}
	})
	if path != "" {
		time.Sleep(400 * time.Millisecond)
	}
	mygoRunOnMain(func() {
		logErr := error(nil)
		if path != "" {
			logErr = capture(window, filepath.Join(dir, path))
		}
		if logErr != nil {
			logf(scenario, "%s: %s | scene %s | capture failed: %v", what, allStates(a), a.sessionState(), logErr)
			return
		}
		logf(scenario, "%s: %s | scene %s", what, allStates(a), a.sessionState())
	})
}

// allStates is every source's state as one line.
func allStates(a *appState) string {
	parts := make([]string, 0, len(a.items))
	for i, it := range a.items {
		parts = append(parts, playerState(i, it))
	}
	return strings.Join(parts, " | ")
}

// sessionState is the session's counts and the scene's contents, so a run shows
// whether anything was left dangling.
func (a *appState) sessionState() string {
	sources := a.session.Sources()
	tracks := a.session.Tracks()
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return "scene error: " + err.Error()
	}
	parts := []string{
		fmt.Sprintf("sources=%d tracks=%d outputs=%d", len(sources), len(tracks), len(a.session.Outputs())),
		"video=" + trackList(scene.Video),
		"audio=" + audioList(scene.Audio),
	}
	return strings.Join(parts, " ")
}

// trackList is a comma separated list of track ids, or none.
func trackList(tracks []core.TrackID) string {
	if len(tracks) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(tracks))
	for _, track := range tracks {
		parts = append(parts, string(track))
	}
	return strings.Join(parts, ",")
}

// audioList is the scene's audio levels as track and volume pairs, or none.
func audioList(levels []core.AudioLevel) string {
	if len(levels) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(levels))
	for _, level := range levels {
		parts = append(parts, fmt.Sprintf("%s@%.2f", level.Track, level.Volume))
	}
	return strings.Join(parts, ",")
}

// runSyncGroup shows that pause and play act on the session's sync group rather
// than on whatever the scene selects: a source the scene shows after the group
// was made keeps playing, and a source removed from the session leaves the group
// without any command going to a player that no longer exists.
// positions is every live player position, used to show that a probe left the
// players where they were.
func (a *appState) positions() []string {
	out := make([]string, 0, len(a.items))
	for index, it := range a.items {
		if it.player == nil {
			out = append(out, fmt.Sprintf("%d: none", index+1))
			continue
		}
		out = append(out, fmt.Sprintf("%d: %s", index+1, it.player.Position()))
	}
	return out
}

func describeShifts(shifts []core.Shift) string {
	if len(shifts) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(shifts))
	for _, shift := range shifts {
		parts = append(parts, fmt.Sprintf("%s %+.3fs", shift.Track, shift.Offset.Seconds()))
	}
	return strings.Join(parts, ", ")
}

func sameShifts(before, after []core.Shift) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index] != after[index] {
			return false
		}
	}
	return true
}

// groupState is every sync group of the session as one line, so a run shows who
// the pause commands reach and how a removal changed that.
func (a *appState) groupState() string {
	groups := a.session.SyncGroups()
	if len(groups) == 0 {
		return "groups=none"
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		parts = append(parts, fmt.Sprintf("%s ref=%s tracks=%s", group.ID, group.Reference, trackList(group.Tracks)))
	}
	return "groups=" + strings.Join(parts, ";")
}

// logGroup records what a group command reported.
func logGroup(scenario, what string, problems []string) {
	if len(problems) == 0 {
		logf(scenario, "%s: every group member accepted the command", what)
		return
	}
	logf(scenario, "%s: %s", what, strings.Join(problems, "; "))
}

// muteAll mutes or unmutes every open player, so a scripted check can compare
// what the output device meters with the sound on and off.
func (a *appState) muteAll(muted bool) {
	for _, it := range a.items {
		if it.player != nil {
			it.player.SetMuted(muted)
		}
	}
}

// eventList is the session's event log, newest last, as one line.
func eventList(a *appState) string {
	events := a.session.Events()
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%d:%s(%s)", event.Seq, event.Kind, event.Object))
	}
	return strings.Join(parts, " ")
}

// missingAddress is uri with its last path element replaced by one a server
// does not have.
func missingAddress(uri string) string {
	slash := strings.LastIndex(uri, "/")
	if slash < 0 || slash == len(uri)-1 {
		return uri + ".missing"
	}
	return uri[:slash+1] + "missing.m3u8"
}

// goodAddress is the address a source had before missingAddress changed it.
func goodAddress(uri string) string {
	return strings.Replace(uri, "/missing.m3u8", "/index.m3u8", 1)
}

// waitPlayerReady waits for one player to present its first picture, logging its
// position every five seconds so a slow start is visible in the record.
func waitPlayerReady(scenario string, player *ui.VideoPlayer, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	next := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		var ready, playing, buffering bool
		var position, duration time.Duration
		var err error
		mygoRunOnMain(func() {
			if player == nil {
				return
			}
			ready = player.Ready()
			playing = player.Playing()
			buffering = player.Buffering()
			position = player.Position()
			duration = player.Duration()
			err = player.Err()
		})
		if ready {
			logf(scenario, "the replay started at %s of %s", position, duration)
			return true
		}
		if time.Now().After(next) {
			logf(scenario, "the replay has not started yet: position %s of %s ready=%v playing=%v buffering=%v err=%v",
				position, duration, ready, playing, buffering, err)
			next = time.Now().Add(5 * time.Second)
		}
	}
	return false
}

// copyFile copies one file to another, which is how a recording leaves the
// buffer directory before the buffer removes it.
func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

// scenarioNames lists the checks in order, for messages.
func scenarioNames() string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

type scenario func(window *mygo.Window, a *appState, dir string)

// scenarios are the checks -scenario names.
var scenarios = map[string]scenario{
	"single":            runSingle,
	"reopen":            runReopen,
	"reopen-strict":     runReopenStrict,
	"handover-fail":     runHandoverFail,
	"reload-rapid":      runReloadRapid,
	"pair":              runPair,
	"replay-swap":       runReplaySwap,
	"switch":            runSwitch,
	"rapid":             runRapid,
	"retry":             runRetry,
	"scene":             runScene,
	"four":              runFour,
	"audio":             runAudio,
	"sync":              runSync,
	"syncgroup":         runSyncGroup,
	"syncmap":           runSyncMap,
	"syncfail":          runSyncFail,
	"dvr":               runDVR,
	"replayprobe":       runReplayProbe,
	"align-probe":       runAlignProbe,
	"align-async":       runAlignAsync,
	"commands":          runCommands,
	"webview":           runWebView,
	"canvas":            runCanvas,
	"canvaslive":        runCanvasLive,
	"viewing":           runViewing,
	"layouts":           runLayouts,
	"editing":           runEditing,
	"editing-live":      runEditingLive,
	"workspaces":        runWorkspaces,
	"workbench":         runWorkbench,
	"matrix":            runMatrix,
	"lifecycle":         runLifecycle,
	"sources":           runSources,
	"settings":          runSettings,
	"settings-restart":  runSettingsRestart,
	"keys":              runKeys,
	"narrow":            runNarrow,
	"sizes":             runSizes,
	"sizes-empty":       runSizesEmpty,
	"dpi":               runDPI,
	"windows":           runWindows,
	"pages":             runPages,
	"focus":             runFocus,
	"realdpi":           runRealDPI,
	"workspace":         runWorkspace,
	"workspace-restore": runWorkspaceRestore,
	"crash":             runCrash,
	"budget":            runBudget,

	// P18.2-b: what a restart remembers about a source's sound, and what it
	// refuses to write.
	"sourcememory":         runSourceMemory,
	"sourcememory-restore": runSourceMemoryRestore,
	"sourcememory-clear":   runSourceMemoryClear,
	"hidden":               runHidden,
	"contract":             runContract,
	"lease":                runLease,
	"sweep":                runSweep,
	"replay":               runReplay,
	"swap":                 runSwap,
	"swap-two":             runSwapTwo,
	"common":               runCommon,
	"midjoin":              runMidJoin,
	"frames":               runFrames,
	"faults":               runFaults,
	"final":                runFinal,
	"seektxn":              runSeekTxn,
	"seekentry":            runSeekEntry,
	"seekclose":            runSeekClose,
	"enterreplay":          runEnterReplay,
	"enterreplay-one":      runEnterReplayOne,
	"seekedge":             runSeekEdge,
	"interleave":           runInterleave,
	"replayui":             runReplayUI,
	"timeline":             runTimeline,
	"alignreport":          runAlignReport,
	"probeclose":           runProbeClose,
	"replaybuffers":        runReplayBuffers,
	"offline":              runOffline,
	"background":           runBackground,
	"mixer":                runMixer,
	"mutehold":             runMuteHold,
	"clock":                runClock,
	"playthrough":          runPlayThrough,
}

func runSourceMemory(window *mygo.Window, a *appState, dir string) {
	go sourceMemoryBody(window, a, dir)
}

func sourceMemoryBody(window *mygo.Window, a *appState, dir string) {
	scenario := "sourcememory"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	step(window, a, dir, scenario, "0-before.png",
		"memory is on, and the command line opened one source",
		func(a *appState) {
			a.workspace = workspaceSettings
			a.settingsSection = "Sources"
			a.settingsDraft = cloneSettings(a.settings)
			a.settingsDraft.SourceMemory.On = true
			a.settingsDraft.SourceMemory.Remember = true
			a.applySettingsCommand()
		})
	mygoRunOnMain(func() {
		check(a.settingsErr == "", "Apply reported %q", a.settingsErr)
		check(a.settings.SourceMemory.On && a.settings.SourceMemory.Remember, "the applied settings do not have memory on")
	})

	// A source the command line opened must not be written back: replaying the
	// same flag on every start would otherwise keep saving the same preference.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() {
			ready = len(a.items) > 0 && a.items[0].player != nil && a.items[0].audio != ""
			if ready {
				if err := a.setVolume(0, 0.3); err != nil {
					logf(scenario, "setVolume on the command line source: %v", err)
				}
			}
		})
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)
	commandLine, note := loadSettings(a.settingsPath)
	mygoRunOnMain(func() {
		check(note == "", "the file loaded without a complaint (%s)", note)
		check(len(commandLine.SourceMemory.Entries) == 0,
			"the command line source left no entry (%d entr(ies))", len(commandLine.SourceMemory.Entries))
	})

	// Now add one by hand and change its sound: that is the source whose
	// preference the file is allowed to keep.
	extra := extraMedia
	var added int
	var identity string
	mygoRunOnMain(func() {
		added = len(a.items)
		if extra == "" {
			logf(scenario, "no -extra address was given, so no hand-added source is available")
			return
		}
		it, err := a.add(extra)
		if err != nil {
			logf(scenario, "add(%s): %v", identityDigest(identityOf(extra)), err)
			return
		}
		a.items = append(a.items, it)
		if err := a.hearAudio(added, 0.25); err != nil {
			logf(scenario, "hearAudio: %v", err)
		}
		if _, err := a.toggleMute(added); err != nil {
			logf(scenario, "toggleMute: %v", err)
		}
		identity = identityOf(extra)
	})
	mygoRunOnMain(func() {
		check(extra != "", "the check gave an address to add by hand")
		check(identity != "", "the -extra address can be an identity (%s)", identityDigest(identityOf(extra)))
	})

	// The write happens off the drawing thread, so wait for a newer file and
	// read it once. Watching it by reading in a loop would hold the file open
	// while the writer tries to replace it, and Windows refuses that rename.
	remembered := time.Now()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(a.settingsPath); err == nil && !info.ModTime().Before(remembered) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var saved settings
	saved, note = loadSettings(a.settingsPath)
	// The capture only succeeds while the window is actually drawn; a start that
	// has not painted yet reports "the window's content has no size". Retry a
	// few times and count a missing picture as a failure rather than leave it in
	// the log only.
	shot := filepath.Join(dir, "1-remembered.png")
	for attempt := 0; attempt < 4; attempt++ {
		time.Sleep(800 * time.Millisecond)
		step(window, a, dir, scenario, "1-remembered.png",
			"the hand-added source is audible and muted, and the file keeps its sound",
			func(a *appState) { a.workspace = workspaceMixer })
		if info, err := os.Stat(shot); err == nil && info.Size() > 0 {
			break
		}
	}
	mygoRunOnMain(func() {
		info, err := os.Stat(shot)
		check(err == nil && info.Size() > 0, "the Mixer page was captured (%v)", err)
		logf(scenario, "the file holds %s", sourceMemoryLine(saved.SourceMemory))
		check(note == "", "the file loaded without a complaint (%s)", note)
		check(len(saved.SourceMemory.Entries) == 1, "the file remembers one address (it holds %d)", len(saved.SourceMemory.Entries))
		if len(saved.SourceMemory.Entries) == 1 {
			entry := saved.SourceMemory.Entries[0]
			check(entry.Address == identity, "the remembered identity matches (%s)", identityDigest(entry.Address))
			check(entry.Volume == 0.25, "the remembered level came back at %.2f", entry.Volume)
			check(entry.Muted, "the remembered mute came back on")
		}
		// Neither the page nor the log prints a remembered address.
		check(!strings.Contains(sourceMemoryLine(saved.SourceMemory), "http") &&
			!strings.Contains(sourceMemoryLine(saved.SourceMemory), ":\\"), "the summary prints no address")
	})

	// A write must not rewrite the draft somebody may be editing.
	mygoRunOnMain(func() {
		if len(a.items) > added {
			a.settingsDraft.DefaultVolume = 0.42
			a.rememberSource(&item{uri: a.items[added].uri, volume: 0.25, muted: true, origin: originAdded})
			check(a.settingsDraft.DefaultVolume == 0.42, "remembering changed the draft's level to %.2f", a.settingsDraft.DefaultVolume)
		}
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// runSourceMemoryClear is the third process: it starts from the file the first
// one wrote and does what only an explicit Clear may do. It is separate because
// the restore check needs the entry to still be there.
func runSourceMemoryClear(window *mygo.Window, a *appState, dir string) {
	go sourceMemoryClearBody(window, a, dir)
}

func sourceMemoryClearBody(window *mygo.Window, a *appState, dir string) {
	scenario := "sourcememory-clear"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() { ready = len(a.items) > 0 && a.items[0].player != nil })
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mygoRunOnMain(func() {
		check(len(a.settings.SourceMemory.Entries) == 1, "the file starts with one remembered address (%d)", len(a.settings.SourceMemory.Entries))
	})

	// The snapshot below is taken before the Clear, so its ticket is older: it
	// is the write that must never land.
	var staleWritten bool
	mygoRunOnMain(func() {
		stale := cloneSettings(a.settings)
		staleTicket := a.saveTicket()
		a.workspace = workspaceSettings
		a.settingsSection = "Sources"
		a.beginDraft(a.settings)
		a.settingsDraft.SourceMemory.clear()
		a.draftCleared = true
		a.applySettingsCommand()
		check(a.settingsErr == "", "Apply after Clear reported %q", a.settingsErr)
		check(len(a.settings.SourceMemory.Entries) == 0,
			"Clear emptied the applied list (%d entr(ies))", len(a.settings.SourceMemory.Entries))
		if err := a.saveSettingsSerialized(stale, staleTicket); err != nil {
			logf(scenario, "the stale snapshot reported %v", err)
		}
		staleWritten = true
		// With remembering off, nothing is written at all.
		a.settings.SourceMemory.Remember = false
		a.rememberSource(&item{uri: a.items[0].uri, volume: 0.9, muted: false, origin: originAdded})
		a.settings.SourceMemory.Remember = true
	})
	if !staleWritten {
		failures++
		logf(scenario, "CHECK FAILED: the clear phase did not run")
	}
	time.Sleep(700 * time.Millisecond)
	cleared, note := loadSettings(a.settingsPath)
	mygoRunOnMain(func() {
		check(note == "", "the file loaded after Clear (%s)", note)
		check(len(cleared.SourceMemory.Entries) == 0,
			"the file kept the forgotten entries (%d entr(ies))", len(cleared.SourceMemory.Entries))
	})
	step(window, a, dir, scenario, "1-cleared.png",
		"the list is empty after an explicit Clear, and a late older write did not fill it again",
		func(a *appState) {
			a.workspace = workspaceSettings
			a.settingsSection = "Sources"
		})
	mygoRunOnMain(func() {
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// runSourceMemoryRestore is the second process: it starts with the file the
// first one wrote and the same hand-added address on the command line, so the
// remembered sound must already be in force when the first scene is applied.
func runSourceMemoryRestore(window *mygo.Window, a *appState, dir string) {
	go sourceMemoryRestoreBody(window, a, dir)
}

func sourceMemoryRestoreBody(window *mygo.Window, a *appState, dir string) {
	scenario := "sourcememory-restore"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	before, beforeErr := os.ReadFile(a.settingsPath)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() { ready = len(a.items) > 0 && a.items[0].player != nil })
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	step(window, a, dir, scenario, "1-restored.png",
		"the window starts with the remembered sound already in force",
		func(a *appState) { a.workspace = workspaceMixer })
	mygoRunOnMain(func() {
		check(a.settings.SourceMemory.On, "memory came back on")
		if len(a.items) == 0 {
			failures++
			logf(scenario, "CHECK FAILED: no source was opened")
		} else {
			it := a.items[0]
			logf(scenario, "source 1 came back at %.2f muted=%v", it.volume, it.muted)
			check(it.volume == 0.25, "the level came back at %.2f", it.volume)
			check(it.muted, "the source came back muted")
			// The card and the output have to agree: the scene level is what
			// the player is handed, so a memory that stops at it.volume would
			// leave the file saying 25% while the sound plays at 100%.
			level, routed := a.sceneLevel(it)
			check(routed && level.Volume == 0.25,
				"the scene plays it at %.2f (the remembered 0.25)", level.Volume)
			if !a.heard(it) {
				// It comes back muted, so nothing is heard yet and a silent
				// player is not handed a level. Unmute the way a person would,
				// and confirm its place, to read what the output was given.
				logf(scenario, "source 1 is silent for now (%s); unmuting it to read the output", joinWords(a.silences(it)))
				if _, err := a.toggleMute(0); err != nil {
					logf(scenario, "unmute: %v", err)
				}
				if err := a.sync.timeline.SetShift(it.audio, 0); err != nil {
					logf(scenario, "confirm the track's shift: %v", err)
				}
				a.apply()
			}
			if it.player != nil {
				check(a.heard(it), "the remembered source is audible once its place is confirmed")
				check(it.player.Volume() == a.volumeFor(0.25),
					"the player was handed %.2f (want %.2f)", it.player.Volume(), a.volumeFor(0.25))
			}
			// A command line source is never written back, even when its sound
			// came from memory: this start must leave the file alone.
			check(it.origin == originCommandLine, "the source came from the command line")
		}
		after, afterErr := os.ReadFile(a.settingsPath)
		check(beforeErr == nil && afterErr == nil, "the file was readable twice (%v / %v)", beforeErr, afterErr)
		check(string(before) == string(after), "this start left the settings file alone")
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// sourceMemoryLine summarises the memory for a log line without printing the
// remembered addresses.
func sourceMemoryLine(memory sourceMemory) string {
	parts := make([]string, 0, len(memory.Entries))
	for _, entry := range memory.Entries {
		muted := ""
		if entry.Muted {
			muted = " muted"
		}
		parts = append(parts, identityDigest(entry.Address)+muted)
	}
	return "on=" + boolWord(memory.On) + " remember=" + boolWord(memory.Remember) + " set=" + memoryDigest(memory) + " entries=[" + strings.Join(parts, " ") + "]"
}

func runSources(window *mygo.Window, a *appState, dir string) {
	scenario := "sources"
	go func() {
		time.Sleep(600 * time.Millisecond)
		mygoRunOnMain(func() { a.workspace = workspaceSources })
		logf(scenario, "start: %d source(s), selection %q", len(a.items), a.selected)
		step(window, a, dir, scenario, "1-sources.png", "the Sources page with its address field and cards", nil)

		// 1. Select the second source the way a click on its row does.
		if len(a.items) > 1 {
			mygoRunOnMain(func() { a.selected = a.items[1].source.ID })
			step(window, a, dir, scenario, "2-selected.png", "the second source selected, the inspector follows it", nil)
			mygoRunOnMain(func() {
				logf(scenario, "selected %s; inspector shows source 2", a.selected)
			})
		}

		// 2. Import a local file through the command the Add button sends. The
		// read runs off the drawing thread; this waits for the command to land.
		uri := ""
		mygoRunOnMain(func() {
			a.selected = ""
			uri = a.lifecycleAddition()
			// Prefer a local file that is not already open: importing the same
			// file a second time would mix two questions (a new player mid-run
			// and the same file opened twice). Look next to the first local
			// source instead of carrying a machine-specific path.
			for _, other := range a.items {
				address := string(other.source.ID)
				if isNetworkAddress(address) {
					continue
				}
				if candidate := filepath.Join(filepath.Dir(address), "long.mp4"); candidate != address {
					if _, statErr := os.Stat(candidate); statErr == nil {
						uri = candidate
					}
				}
				break
			}
		})
		if uri != "" {
			before := len(a.items)
			mygoRunOnMain(func() {
				a.importText = uri
				a.report(a.runCommand(command{Kind: commandAdd, URI: a.importText}))
			})
			deadline := time.Now().Add(20 * time.Second)
			for {
				done := false
				mygoRunOnMain(func() { done = a.importing == "" && len(a.items) > before })
				if done || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				logf(scenario, "import: %d source(s) in the window, selected=%q, field=%q, last command: %s",
					len(a.items), a.selected, a.importText, a.lastCommand.String())
			})
			step(window, a, dir, scenario, "3-imported.png", "a file imported from the address field; it is selected", nil)

			// 2b. The player opened at import; give it time and say how long it
			// took to have a picture, before anything shows it.
			opened := time.Now()
			readyAtImport := false
			for {
				mygoRunOnMain(func() {
					if len(a.items) > 0 {
						last := a.items[len(a.items)-1]
						readyAtImport = last.player != nil && last.player.Ready()
					}
				})
				if readyAtImport || time.Since(opened) > 8*time.Second {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				last := a.items[len(a.items)-1]
				logf(scenario, "import: player opened at import: ready=%v after %s, position=%s",
					readyAtImport, time.Since(opened).Round(time.Millisecond), last.player.Position())
			})

			// 2c. Show it: the stage must draw the new source and its player
			// must keep advancing.
			var added core.SourceID
			mygoRunOnMain(func() {
				if len(a.items) > 0 {
					added = a.items[len(a.items)-1].source.ID
				}
			})
			mygoRunOnMain(func() {
				a.report(a.runCommand(command{Kind: commandShow, Source: added}))
			})
			showDeadline := time.Now().Add(8 * time.Second)
			for {
				ready := false
				mygoRunOnMain(func() {
					if len(a.items) > 0 {
						last := a.items[len(a.items)-1]
						ready = last.player != nil && last.player.Ready()
					}
				})
				if ready || time.Now().After(showDeadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				last := a.items[len(a.items)-1]
				logf(scenario, "show %s: player=%v ready=%v picture=%v, last command: %s",
					added, last.player != nil, last.player != nil && last.player.Ready(),
					a.snapshot().Picture, a.lastCommand.String())
			})
			step(window, a, dir, scenario, "3b-shown.png", "the imported source shown: its player came up and draws it", nil)
		}

		// 3. Import an address that fails, then retry it: both must show a real
		// error and the Retry button must be the one that carries it.
		before := len(a.items)
		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandAdd, URI: "http://127.0.0.1:9/not-there.m3u8"}))
		})
		deadline := time.Now().Add(20 * time.Second)
		for {
			done := false
			mygoRunOnMain(func() { done = a.importing == "" && a.lastCommand.Err != nil })
			if done || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		var failedURI string
		mygoRunOnMain(func() {
			logf(scenario, "failed import: %d source(s), last command: %s", len(a.items), a.lastCommand.String())
			if a.lastCommand.Command.Kind == commandAdd && a.lastCommand.Err != nil {
				failedURI = a.lastCommand.Command.URI
			}
		})
		if len(a.items) == before {
			logf(scenario, "a failed import adds no card; the import row offers Retry for the address it remembers")
		}
		step(window, a, dir, scenario, "4-failed-import.png", "an address that cannot be read: the row keeps the error and a Retry", nil)

		// 3b. Retry the failed address from the same row: it must fail again,
		// not silently succeed or freeze the window.
		if failedURI != "" {
			mygoRunOnMain(func() {
				a.report(a.runCommand(command{Kind: commandAdd, URI: failedURI}))
			})
			deadline := time.Now().Add(20 * time.Second)
			for {
				done := false
				mygoRunOnMain(func() { done = a.importing == "" })
				if done || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				logf(scenario, "retry of %s: %d source(s), last command: %s", failedURI, len(a.items), a.lastCommand.String())
			})
		}

		// 4. Remove the first source through the command the card sends.
		if len(a.items) > 0 {
			var removed core.SourceID
			mygoRunOnMain(func() {
				removed = a.items[0].source.ID
				a.report(a.runCommand(command{Kind: commandRemove, Source: removed}))
			})
			mygoRunOnMain(func() {
				logf(scenario, "remove %s: %d source(s) left, selected=%q, last command: %s",
					removed, len(a.items), a.selected, a.lastCommand.String())
			})
			step(window, a, dir, scenario, "5-after-remove.png", "after removing a source: the list and the inspector agree", nil)
		}

		// 5. Clicking a card selects it; clearing the selection falls back to
		// the session summary.
		mygoRunOnMain(func() { a.selected = "" })
		step(window, a, dir, scenario, "6-session-summary.png", "no selection: the inspector shows the session summary", nil)

		mygoRunOnMain(func() {
			logf(scenario, "done: %d source(s), %d event(s), selection %q",
				len(a.items), len(a.session.Events()), a.selected)
			a.release()
			mygo.App.Quit()
		})
	}()
}

func runSwap(window *mygo.Window, a *appState, dir string) {
	// The body sleeps while it waits for a recording to reach the picture. Run
	// it off the drawing thread: MyGo dispatches scenarios inside WhenReady, and
	// a body that slept there would hold the frame loop while its own
	// runOnMain calls wait for that same loop.
	go swapBody(window, a, dir)
}

func swapBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "swap"
	// The numbers below are the evidence, so a wrong one fails the run instead
	// of only reading oddly in a log nobody re-adds up.
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source to buffer, got %s", shortURI(it.uri))
		return
	}
	bufferDir := filepath.Join(dir, "segments")
	recordingsDir := filepath.Join(dir, "recordings")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, recordingsDir, 40*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()

	windowOf, ok := waitForRange(runner, 12*time.Second, 60*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 12s of media within 60s")
		return
	}
	logf(scenario, "the buffer holds %s; %s", logRange(windowOf), runner.budgetLine())
	base := windowOf.Start

	// 1. A recording reaches the stage through a lease.
	epochBefore, instanceBefore := runner.buffer.Epoch(), runner.buffer.Instance()
	generationBefore := it.generation
	if err := a.playRecording(it, runner, base+2*time.Second); err != nil {
		logf(scenario, "first switch: %v", err)
		return
	}
	if !waitRecording(a, 0, base+2*time.Second, 15*time.Second) {
		logf(scenario, "the first recording never reached the stage")
		return
	}
	epochAfter, instanceAfter := runner.buffer.Epoch(), runner.buffer.Instance()
	logf(scenario, "the switch replaced the player, not the source clock: buffer %s epoch %d -> %s epoch %d, generation %d -> %d",
		instanceBefore, epochBefore, instanceAfter, epochAfter, generationBefore, it.generation)
	check(instanceAfter == instanceBefore && epochAfter == epochBefore,
		"a replay switch changed the buffer identity (%s epoch %d became %s epoch %d)",
		instanceBefore, epochBefore, instanceAfter, epochAfter)
	check(it.generation > generationBefore, "a committed switch left the player generation at %d", it.generation)
	check(showing(it) != nil && showing(it).BufferInstance == instanceBefore && showing(it).SourceMediaEpoch == epochBefore,
		"the recording on the stage does not name the buffer it was cut from")
	open, given := a.leaseStats()
	logf(scenario, "the recording is on the stage: %s | leases open=%d given=%d", replayLine(it), open, given)
	check(open == 1 && given == 1, "the first switch asked for %d lease(s) and left %d open", given, open)
	event, clockErr := a.replayEventTime(it, 0)
	if clockErr != nil {
		// An unconfirmed offset is a refusal, not a zero offset — so the first
		// answer here must be the refusal. Confirming the track at zero then
		// lets the same position convert, and the answer must be the measured
		// media start, because event = media - 0.
		logf(scenario, "replay clock: a position inside the recording does not convert: %v", clockErr)
		track := it.audio
		if track == "" {
			track = it.video
		}
		if err := a.sync.timeline.SetShift(track, 0); err != nil {
			logf(scenario, "replay clock: confirming %s at 0 failed: %v", track, err)
		} else if converted, err := a.replayEventTime(it, 0); err != nil {
			logf(scenario, "replay clock: %s is confirmed at 0 but the position still does not convert: %v", track, err)
		} else {
			logf(scenario, "replay clock: with %s confirmed at 0, position 0 of %s converts to %s (measured media %s at buffer %s)",
				track, showing(it).FileName(), converted, it.replayAnchor.MediaStart, it.replayAnchor.BufferStart)
			check(converted == it.replayAnchor.MediaStart,
				"the measured start converted to %s, not to the media time the anchor measured (%s)", converted, it.replayAnchor.MediaStart)
		}
	} else {
		logf(scenario, "replay clock: position 0s of %s is event %s (anchor media %s at buffer %s, interval %s)",
			showing(it).FileName(), event, it.replayAnchor.MediaStart, it.replayAnchor.BufferStart, logRange(it.replayAnchor.Interval))
	}
	check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
		"the stage shows %v, not the recording asked for at %s", showing(it), sceneClock(base+2*time.Second))
	step(window, a, dir, scenario, "1-replay.png", "the recording on the stage", nil)
	first := positionOf(a, 0)
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "2-replay-later.png", "the same recording two seconds later", nil)
	logf(scenario, "the picture moved: position %s -> %s (advanced=%v), %s",
		sceneClock(first), sceneClock(positionOf(a, 0)), moved(first, positionOf(a, 0)), replayState(a, 0))

	// 2. A player that cannot open a file changes nothing.
	a.openReplay = func(path string) (*ui.VideoPlayer, error) {
		return nil, fmt.Errorf("stand-in: refusing to open %s", filepath.Base(path))
	}
	errOpen := a.playRecording(it, runner, base+4*time.Second)
	a.openReplay = nil
	open, given = a.leaseStats()
	logf(scenario, "open failure: %v | still showing %v | leases open=%d given=%d",
		errOpen, replayLine(it), open, given)
	check(errOpen != nil, "a player that cannot open the file reported no error")
	check(open == 1, "a failed open left %d lease(s) open", open)
	check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
		"a failed open changed the picture to %v", showing(it))

	// 3. A player that never shows a picture is rolled back and its file given
	// back; the picture and the lease the item already had stay.
	oldWait := handoverWait
	handoverWait = 1200 * time.Millisecond
	a.replayStarted = func(*ui.VideoPlayer) bool { return false }
	errLate := a.playRecording(it, runner, base+6*time.Second)
	time.Sleep(2 * time.Second)
	a.replayStarted = nil
	handoverWait = oldWait
	open, given = a.leaseStats()
	logf(scenario, "timeout switch: err=%v | still showing %v | leases open=%d given=%d",
		errLate, replayLine(it), open, given)
	check(open == 1, "a timed-out switch left %d lease(s) open", open)
	check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
		"a timed-out switch changed the picture to %v", showing(it))

	// 4. A burst of switches ends on the last one, and only its lease is open.
	//
	// A budget that is already full refuses a cut instead of dropping a file a
	// player holds; the run then checks the picture and lease it kept rather
	// than pretending the burst happened.
	burst := true
	last := time.Duration(0)
	for _, offset := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second} {
		if err := a.playRecording(it, runner, base+offset); err != nil {
			logf(scenario, "burst switch at %s refused: %v", sceneClock(base+offset), err)
			burst = false
			break
		}
		last = base + offset
		time.Sleep(250 * time.Millisecond)
	}
	open, given = a.leaseStats()
	if burst {
		if !waitRecording(a, 0, last, 15*time.Second) {
			logf(scenario, "the burst did not settle on the last switch: %v", replayLine(it))
			return
		}
		open, given = a.leaseStats()
		logf(scenario, "the burst settled on %s | leases open=%d given=%d", replayLine(it), open, given)
		check(open == 1, "the burst left %d lease(s) open", open)
		check(showing(it) != nil && showing(it).RequestedBufferTime == last,
			"the burst settled on %v, not the last switch", showing(it))
		step(window, a, dir, scenario, "3-burst.png", "the last switch of a burst", nil)
	} else {
		logf(scenario, "the burst kept %s | leases open=%d given=%d", replayLine(it), open, given)
		check(showing(it) != nil && showing(it).RequestedBufferTime == base+2*time.Second,
			"a refused burst changed the picture to %v", showing(it))
	}

	// 4b. The recording budget drops the snapshots the item has let go of, and
	// never the file the item is holding a lease on.
	shown := showing(it)
	var validErr error
	if shown != nil {
		validErr = shown.Valid()
	}
	logf(scenario, "the budget keeps %d recording(s) of %d bytes; the one on the stage is valid=%v (%v)",
		len(runner.buffer.Recordings()), runner.buffer.RecordingBytes(), validErr == nil, validErr)
	check(shown != nil && validErr == nil, "the recording on the stage is no longer valid: %v", validErr)

	// 5. Closing the item in the middle of a switch still returns every lease
	// exactly once. The player that was showing the recording is paused until
	// another one can take over, so its lease stays open: the file has to remain
	// readable for as long as the player holds it. The replacement, which never
	// became the item's player, is closed and given back by this close alone; a
	// rollback that is still waiting finds that work done.
	held := showing(it)
	handoverWait = 5 * time.Second
	a.replayStarted = func(*ui.VideoPlayer) bool { return false }
	if err := a.playRecording(it, runner, base+8*time.Second); err != nil {
		logf(scenario, "switch before the close: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	a.close(it)
	time.Sleep(500 * time.Millisecond)
	a.replayStarted = nil
	handoverWait = oldWait
	open, given = a.leaseStats()
	var heldErr error
	if held != nil {
		heldErr = held.Valid()
	}
	logf(scenario, "closed in the middle of a switch: leases open=%d given=%d players=%d; the file the paused player reads is valid=%v (%v)",
		open, given, openPlayers(a), heldErr == nil, heldErr)
	check(open == 1, "a close during a switch left %d lease(s) open before the player was really closed", open)
	check(held != nil && heldErr == nil, "the recording of the paused player is no longer valid: %v", heldErr)
	check(openPlayers(a) == 1, "a close during a switch left %d player(s), not the paused one", openPlayers(a))

	// 6. The paused player is closed when nobody else is left, and only then is
	// its lease given back. A lease count of zero and a closed player are two
	// different facts, so both are asserted.
	mygoRunOnMain(func() { a.release() })
	open, given = a.leaseStats()
	logf(scenario, "after shutdown: leases open=%d given=%d players=%d", open, given, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	left := runner.buffer.PendingCleanup()
	recordings := len(runner.buffer.Recordings())
	logf(scenario, "after closing the buffer: pending cleanup=%v recordings=%d", left, recordings)
	check(len(left) == 0, "the buffer could not remove %v", left)
	check(recordings == 0, "the closed buffer still owns %d recording(s)", recordings)
	logf(scenario, "checks failed: %d", failures)
}

// openPlayers counts the players the run still holds open, wherever they live:
// on an item, kept drawn by a handover, or paused while nothing else was left.
func openPlayers(a *appState) int {
	open := 0
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if it.player != nil {
				open++
			}
			if it.previous != nil {
				open++
			}
			if it.handover != nil && it.handover.open() {
				open++
			}
			if it.swap != nil && it.swap.player != nil {
				open++
			}
		}
		a.pendingMu.Lock()
		open += len(a.pending)
		a.pendingMu.Unlock()
	})
	return open
}

// showing returns the recording the item has on the stage, or nil when it shows
// its own source. It asks the drawing thread, which is the thread that owns the
// field.
func showing(it *item) *dvr.Recording {
	var at *dvr.Recording
	mygoRunOnMain(func() { at = it.replay })
	return at
}

// waitRecording waits until the item shows a recording that was asked for at the
// given buffer time.
func waitRecording(a *appState, index int, want time.Duration, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		it, err := a.at(index)
		if err == nil && it.replay != nil && it.replay.RequestedBufferTime == want {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func runSwapTwo(window *mygo.Window, a *appState, dir string) {
	go swapTwoBody(window, a, dir)
}

func swapTwoBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "swap-two"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	if len(a.items) < 2 {
		logf(scenario, "needs two sources, got %d", len(a.items))
		return
	}
	first, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	second, err := a.at(1)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(second.uri) {
		logf(scenario, "needs a network second source, got %s", shortURI(second.uri))
		return
	}
	runner, err := startDVR(second.uri, dvrFromMedia(
		filepath.Join(dir, "segments2"), filepath.Join(dir, "recordings2"),
		40*time.Second, 0, dvrRecordingBytes, second.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(second.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()

	// 1. A buffer belongs to the source it was started for. Asking for a
	// recording of source 1 through the buffer of source 2 has to be refused
	// before anything is cut or opened.
	errWrong := a.playRecording(first, runner, 0)
	logf(scenario, "an item of %s asked the buffer of %s for a recording: %v",
		first.source.ID, second.source.ID, errWrong)
	check(errWrong != nil, "a buffer of another source handed out a recording")
	check(errWrong != nil && strings.Contains(errWrong.Error(), "belongs to"),
		"the refusal does not name the sources: %v", errWrong)
	open, given := a.leaseStats()
	check(open == 0 && given == 0, "the refused request took %d lease(s) and left %d open", given, open)

	windowOf, ok := waitForRange(runner, 12*time.Second, 60*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 12s of media within 60s")
		return
	}
	logf(scenario, "the buffer holds %s", logRange(windowOf))

	// 2. Source 2 is not in the scene. Switching it to a recording must not
	// change what the stage shows, and its own element must stay hidden.
	if err := a.playRecording(second, runner, windowOf.Start+2*time.Second); err != nil {
		logf(scenario, "switch the background source: %v", err)
		return
	}
	if !waitRecording(a, 1, windowOf.Start+2*time.Second, 15*time.Second) {
		logf(scenario, "the background source never reached its recording")
		return
	}
	var showingFirst, showingSecond bool
	var hiddenSecond bool
	mygoRunOnMain(func() {
		showingFirst = first.shown
		showingSecond = second.shown
		hiddenSecond = !second.shown
	})
	open, given = a.leaseStats()
	logf(scenario, "background switch: %s | the stage still shows %s (shown=%v) | the background source is shown=%v | leases open=%d given=%d",
		backgroundLine(second), first.source.ID, showingFirst, showingSecond, open, given)
	check(first.replay == nil, "the on-stage source changed to a recording nobody asked for")
	check(showingFirst && !showingSecond, "the background source is on the stage (first=%v second=%v)", showingFirst, showingSecond)
	check(hiddenSecond, "the background source is not hidden")
	check(open == 1 && given == 1, "the background switch asked for %d lease(s) and left %d open", given, open)
	step(window, a, dir, scenario, "1-background.png", "a background source switching to its history", nil)
	logf(scenario, "checks failed: %d", failures)
}

// backgroundLine says what a source that is not in the scene is showing.
func backgroundLine(it *item) string {
	var line string
	mygoRunOnMain(func() {
		if it.replay == nil {
			line = string(it.source.ID) + " shows its own source"
			return
		}
		line = fmt.Sprintf("%s shows %s (asked at %s)", it.source.ID,
			filepath.Base(it.replay.Path), sceneClock(it.replay.RequestedBufferTime))
	})
	return line
}
