// P16.4d, closing during preparation: the entry starts a replay seek and the
// user closes the window while the transaction is still cutting files and
// opening players. The scenario leaves the window alone after starting the
// transaction; the close comes from outside, through the window itself.
package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

func runSeekClose(window *mygo.Window, a *appState, dir string) {
	go seekCloseBody(window, a, dir)
}

func seekCloseBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seekclose"
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	runners := make([]*dvrRunner, 0, len(a.items))
	for index := range a.items {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address", index+1)
			return
		}
		name := "seekclose-" + shortName(index)
		runner, err := startDVR(it.uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, it.source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(it.uri, runner)
		runners = append(runners, runner)
	}

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer in time", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
	}

	step(window, a, dir, scenario, "", "put both sources on the event clock", func(a *appState) {
		for index := range a.items {
			it, err := a.at(index)
			if err != nil {
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				_ = a.sync.timeline.SetShift(track, 0)
			}
		}
		a.apply()
	})
	for index, runner := range runners {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runner, 0); err != nil {
			logf(scenario, "source %d: replay: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording", index+1)
			return
		}
	}
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	mygoRunOnMain(func() { _, _ = a.nudge(1, 10*time.Millisecond) })

	var note string
	mygoRunOnMain(func() {
		a.seekText = "7"
		note = a.requestReplaySeek()
	})
	logf(scenario, "started a replay seek through the entry: %s", note)
	logf(scenario, "closing now: the transaction is still preparing; the window is left to the user")

	// The window must stay interactive while the transaction runs; this goroutine
	// only waits for the process to end.
	time.Sleep(120 * time.Second)
	logf(scenario, "the window was not closed within 120s")
}
