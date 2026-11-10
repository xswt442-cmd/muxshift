package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runSweep is the lifecycle check: the process opens its own marked session,
// keeps it locked while it lives, and a second process started at the same time
// must neither collect it nor be collected by it. The boot log carries the sweep
// result; this scenario only keeps the window alive long enough for a second
// process to start, then closes normally so the directory goes away.
func runSweep(window *mygo.Window, a *appState, dir string) {
	const scenario = "sweep"
	if a.dvrSession == nil {
		logf(scenario, "no session was opened")
		return
	}
	logf(scenario, "session %s lives at %s", a.dvrSession.ID, a.dvrSession.Dir)
	logf(scenario, "segments %s, recordings %s", a.dvrSession.SegmentsDir(), a.dvrSession.RecordingsDir())
	step(window, a, dir, scenario, "sweep.png", "the window while the session is live", nil)
	time.Sleep(9 * time.Second)
	logf(scenario, "still live: %s", a.dvrSession.Dir)
	a.release()
	mygo.App.Quit()
}
