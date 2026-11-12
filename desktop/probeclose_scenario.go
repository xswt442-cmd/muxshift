package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// runProbeClose shows what a session close does to a comparison that is still
// being prepared: the decoder's context is cancelled, the attempt counter moves
// on so the late result cannot become the session's account, and the close path
// itself returns without waiting for the drawing thread. It needs two sources,
// because a comparison names two.
func runProbeClose(window *mygo.Window, a *appState, dir string) {
	go probeCloseBody(window, a, dir)
}

func probeCloseBody(window *mygo.Window, a *appState, dir string) {
	var started, closed bool
	var attempt, after uint64
	var failed int
	check := func(ok bool, format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if ok {
			logf("probeclose", "ok: %s", line)
			return
		}
		failed++
		logf("probeclose", "FAIL: %s", line)
	}

	mygoRunOnMain(func() {
		if len(a.items) < 2 {
			return
		}
		started = true
		attempt = a.startProbe(0, 1)
		a.release()
		closed = true
		after = a.probeAttempt
	})
	if !started {
		logf("probeclose", "the session needs two sources; it has %d", runOnMainValue(func() int { return len(a.items) }))
		mygo.App.Quit()
		return
	}
	time.Sleep(1500 * time.Millisecond)

	var running bool
	mygoRunOnMain(func() { running = a.probeRunning })
	check(!running, "the comparison is not marked as running after the close")
	check(after > attempt, "the close moved the attempt counter on (%d -> %d), so a late result is stale", attempt, after)
	check(closed, "the close returned without waiting for the decoder")

	logf("probeclose", "done: %d check(s) failed", failed)
	mygoRunOnMain(func() { mygo.App.Quit() })
}
