package main

import (
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo"
)

// runMidJoin proves the case the buffer is really for: the source was already
// running when MuxShift attached, so the buffer's clock does not start at the
// source's zero, and neither does the file cut from it. Four numbers are logged
// for every sample -- the cut's StartBufferTime, the anchor's MediaStart, the
// player's Position(), and the burned-in timecode in the captured picture -- so
// the fixed offset and the drift over play time can be told apart.
func runMidJoin(window *mygo.Window, a *appState, dir string) {
	go midJoinBody(window, a, dir, "midjoin")
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
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a live network source, got %s", shortURI(it.uri))
		return
	}
	bufferDir := filepath.Join(dir, "segments")
	recordingsDir := filepath.Join(dir, "recordings")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, recordingsDir, 120*time.Second, 0, dvrRecordingBytes, it.source.ID))
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

	windowOf, ok := waitForRange(runner, 8*time.Second, 90*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 8s of the live source within 90s")
		return
	}
	logf(scenario, "the buffer attached mid-flight and holds %s; %s", logRange(windowOf), runner.budgetLine())

	// Cut from the first segment the buffer stored -- the point where MuxShift
	// attached -- and open that file. Its own zero has to be its own first
	// frame, which is somewhere inside the source's timeline, not at zero.
	base := windowOf.Start
	if err := a.playRecording(it, runner, base); err != nil {
		logf(scenario, "open a recording of the live buffer: %v", err)
		return
	}
	if !waitRecording(a, 0, base, 20*time.Second) {
		logf(scenario, "the recording never reached the stage")
		return
	}
	recording := showing(it)
	anchor := it.replayAnchor
	if recording == nil || anchor == nil {
		logf(scenario, "the stage shows no measured recording: recording=%v anchor=%v", recording, anchor)
		return
	}
	logf(scenario, "the recording %s was cut at buffer %s; its anchor measured media %s at buffer %s",
		recording.FileName(), sceneClock(recording.StartBufferTime), sceneClock(anchor.MediaStart), sceneClock(anchor.BufferStart))
	check(anchor.MediaStart > time.Second,
		"the live source's media clock starts at %s, so this run cannot show a mid-flight attach",
		sceneClock(anchor.MediaStart))
	check(recording.StartBufferTime == base,
		"the cut starts at buffer %s, not at the first stored segment %s", sceneClock(recording.StartBufferTime), sceneClock(base))

	length := recording.EndBufferTime - recording.StartBufferTime
	logf(scenario, "the file runs %s of media", sceneClock(length))

	samples := []time.Duration{0, length / 2, length - 2*time.Second}
	for index, target := range samples {
		if target >= length {
			target = length - 500*time.Millisecond
		}
		// A seek issued while the new player is still opening is dropped, so ask
		// again until the player reports the requested position; a picture taken
		// in the meantime would claim a position the player never reached.
		var position time.Duration
		for attempt := 0; attempt < 3; attempt++ {
			mygoRunOnMain(func() {
				if it.player != nil {
					it.player.Pause()
					it.player.Seek(target)
				}
			})
			time.Sleep(1200 * time.Millisecond)
			position = positionOf(a, 0)
			if absDuration(position-target) <= 120*time.Millisecond {
				break
			}
			logf(scenario, "sample %d: attempt %d asked for %s and the player reported %s", index+1, attempt+1, sceneClock(target), sceneClock(position))
		}
		logf(scenario, "sample %d: asked for %s | StartBufferTime %s | Anchor.MediaStart %s | Position() %s | drift %s",
			index+1, sceneClock(target), sceneClock(recording.StartBufferTime), sceneClock(anchor.MediaStart),
			sceneClock(position), sceneClock(position-target))
		check(absDuration(position-target) <= 120*time.Millisecond,
			"sample %d asked for %s and the player reported %s", index+1, sceneClock(target), sceneClock(position))
		shot := "sample-" + strconv.Itoa(index+1) + ".png"
		step(window, a, dir, scenario, shot, "sample at "+sceneClock(position), nil)
		logf(scenario, "sample %d: the picture %s was captured at Position() %s", index+1, shot, sceneClock(position))
	}
	logf(scenario, "checks failed: %d", failures)
}
