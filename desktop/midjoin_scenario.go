package main

import (
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo"

	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// midFlight is one recording cut from a live buffer that MuxShift attached to
// mid-stream: the buffer's clock does not start at the source's zero, and
// neither does the file.
type midFlight struct {
	it        *item
	runner    *dvrRunner
	recording *dvr.Recording
	anchor    *dvr.Anchor
	length    time.Duration
}

// runMidJoin proves the case the buffer is really for: the source was already
// running when MuxShift attached, so the buffer's clock does not start at the
// source's zero, and neither does the file cut from it. Four numbers are logged
// for every sample -- the cut's StartBufferTime, the anchor's MediaStart, the
// player's Position(), and the burned-in timecode in the captured picture -- so
// the fixed offset and the drift over play time can be told apart.
func runMidJoin(window *mygo.Window, a *appState, dir string) {
	go midJoinBody(window, a, dir, "midjoin")
}

// attachMidFlight starts a buffer on the source at index 0, waits until it holds
// a window, cuts a recording from the first stored segment and opens it on the
// stage of that source. finish must run before the caller returns.
func attachMidFlight(a *appState, dir, scenario string) (flight *midFlight, finish func()) {
	finish = func() {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return nil, finish
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a live network source, got %s", shortURI(it.uri))
		return nil, finish
	}
	runner, err := startDVR(it.uri, dvrFromMedia(
		filepath.Join(dir, "segments"), filepath.Join(dir, "recordings"),
		120*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return nil, finish
	}
	a.trackRunner(it.uri, runner)

	windowOf, ok := waitForRange(runner, 8*time.Second, 90*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 8s of the live source within 90s")
		return nil, finish
	}
	logf(scenario, "the buffer attached mid-flight and holds %s; %s", logRange(windowOf), runner.budgetLine())

	// Cut from the first segment the buffer stored -- the point where MuxShift
	// attached -- and open that file. Its own zero has to be its own first
	// frame, which is somewhere inside the source's timeline, not at zero.
	base := windowOf.Start
	if err := a.playRecording(it, runner, base); err != nil {
		logf(scenario, "open a recording of the live buffer: %v", err)
		return nil, finish
	}
	if !waitRecording(a, 0, base, 20*time.Second) {
		logf(scenario, "the recording never reached the stage")
		return nil, finish
	}
	recording := showing(it)
	anchor := it.replayAnchor
	if recording == nil || anchor == nil {
		logf(scenario, "the stage shows no measured recording: recording=%v anchor=%v", recording, anchor)
		return nil, finish
	}
	logf(scenario, "the recording %s was cut at buffer %s; its anchor measured media %s at buffer %s",
		recording.FileName(), sceneClock(recording.StartBufferTime), sceneClock(anchor.MediaStart), sceneClock(anchor.BufferStart))
	if anchor.MediaStart <= time.Second {
		logf(scenario, "the live source's media clock starts at %s, so this run cannot show a mid-flight attach", sceneClock(anchor.MediaStart))
		return nil, finish
	}
	if recording.StartBufferTime != base {
		logf(scenario, "the cut starts at buffer %s, not at the first stored segment %s",
			sceneClock(recording.StartBufferTime), sceneClock(base))
		return nil, finish
	}
	return &midFlight{
		it:        it,
		runner:    runner,
		recording: recording,
		anchor:    anchor,
		length:    recording.EndBufferTime - recording.StartBufferTime,
	}, finish
}

// seekSettle pauses the player, asks for target and waits until the player
// reports that position while ready and not buffering. A seek issued while a
// player is still opening is dropped, so it asks again a few times.
func (m *midFlight) seekSettle(a *appState, scenario string, target time.Duration) (time.Duration, bool) {
	target = m.clamp(target)
	for attempt := 0; attempt < 4; attempt++ {
		mygoRunOnMain(func() {
			if m.it.player != nil {
				m.it.player.Pause()
				m.it.player.Seek(target)
			}
		})
		deadline := time.Now().Add(4 * time.Second)
		settled := 0
		for time.Now().Before(deadline) {
			time.Sleep(150 * time.Millisecond)
			position, ready, buffering := playerSnapshot(a, m.it)
			if ready && !buffering && absDuration(position-target) <= 40*time.Millisecond {
				settled++
				if settled >= 2 {
					return position, true
				}
				continue
			}
			settled = 0
		}
		logf(scenario, "target %s: attempt %d did not settle", sceneClock(target), attempt+1)
	}
	position, _, _ := playerSnapshot(a, m.it)
	return position, false
}

// clamp keeps a sample inside the file, away from the very end where the
// player may have nothing left to show.
func (m *midFlight) clamp(target time.Duration) time.Duration {
	if target < 0 {
		return 0
	}
	if target > m.length-500*time.Millisecond {
		return m.length - 500*time.Millisecond
	}
	return target
}

// captureSample reads the position before the capture, takes the picture, then
// reads the position again -- all in one drawing-thread pass -- so the two
// numbers bracket the moment the raster was asked for.
func (m *midFlight) captureSample(window *mygo.Window, a *appState, dir, scenario, name string) (before, after time.Duration, ready, buffering bool) {
	mygoRunOnMain(func() {
		before, ready, buffering = playerSnapshot(a, m.it)
		_ = capture(window, filepath.Join(dir, name))
		after, _, _ = playerSnapshot(a, m.it)
	})
	logf(scenario, "%s: captured between Position() %s and %s (ready=%t buffering=%t)",
		name, sceneClock(before), sceneClock(after), ready, buffering)
	return before, after, ready, buffering
}

// playerSnapshot reads one player's position and state on the drawing thread.
func playerSnapshot(a *appState, it *item) (position time.Duration, ready, buffering bool) {
	if it.player == nil {
		return 0, false, false
	}
	return it.player.Position(), it.player.Ready(), it.player.Buffering()
}

func midJoinBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	m, finish := attachMidFlight(a, dir, scenario)
	defer finish()
	if m == nil {
		return
	}
	logf(scenario, "the file runs %s of media", sceneClock(m.length))

	samples := []time.Duration{0, m.length / 2, m.length - 2*time.Second}
	for index, target := range samples {
		position, ok := m.seekSettle(a, scenario, target)
		logf(scenario, "sample %d: asked for %s | StartBufferTime %s | Anchor.MediaStart %s | Position() %s | drift %s",
			index+1, sceneClock(target), sceneClock(m.recording.StartBufferTime), sceneClock(m.anchor.MediaStart),
			sceneClock(position), sceneClock(position-target))
		check(ok, "sample %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		shot := "sample-" + strconv.Itoa(index+1) + ".png"
		m.captureSample(window, a, dir, scenario, shot)
	}
	logf(scenario, "checks failed: %d", failures)
}
