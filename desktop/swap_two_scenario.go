package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runSwapTwo checks the parts of the replay transaction that need a second
// source: a buffer may only be cut for the source it was started for, and a
// source that is not in the scene must not take the picture over while its
// replacement is getting ready.
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
