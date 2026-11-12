package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runCrash sets the app up the way a person might leave it -- another page,
// another frame, a live buffer filling the cache -- and then waits to be killed
// instead of closing. Nothing here saves anything: the point of the check is
// what a process that never reaches OnClose leaves behind, and what the next
// start makes of it.
func runCrash(window *mygo.Window, a *appState, dir string) {
	go crashBody(window, a, dir)
}

func crashBody(window *mygo.Window, a *appState, dir string) {
	scenario := "crash"
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source to fill the buffer, got %s", shortURI(it.uri))
		return
	}

	// A state worth missing: a different page and a different frame from the one
	// the workspace file holds. If any of this turned up after the restart, it
	// would mean the killed process had written after all.
	mygoRunOnMain(func() {
		window.SetBounds(mygo.Rectangle{X: 320, Y: 200, Width: 940, Height: 700})
		a.workspace = workspaceMixer
	})
	time.Sleep(500 * time.Millisecond)

	// The buffer writes into this run's own session directory, which is where a
	// sweep after the kill has to find it: segments, a marker and a lock the
	// process no longer holds.
	runner, err := startDVR(it.uri, dvrFromMedia(
		a.dvrDirFor("crash"), a.dvrRecordingsDirFor("crash"),
		120*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	if held, ok := waitForRange(runner, 4*time.Second, 60*time.Second); ok {
		logf(scenario, "the buffer holds %s in %s", logRange(held), a.dvrDirFor("crash"))
	} else {
		logf(scenario, "the buffer held nothing within 60s; killing this process would still leave a session directory behind")
	}

	mygoRunOnMain(func() {
		b := window.Bounds()
		logf(scenario, "the window is on %s at %d,%d %dx%d and nothing has been saved",
			a.workspace.title(), b.X, b.Y, b.Width, b.Height)
	})
	logf(scenario, "the driver can kill this process now")
	for i := 0; ; i++ {
		time.Sleep(5 * time.Second)
		logf(scenario, "still running after %d tick(s); %s", i+1, runner.state())
	}
}
