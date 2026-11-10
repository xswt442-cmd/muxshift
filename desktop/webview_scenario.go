// P13.3: one window, one session, two interfaces.
//
// The scenario proves three things a screenshot alone cannot: the page reads
// the same session the native side shows, a command the page sends moves that
// session, and the native snapshot sees the result of that command.
package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runWebView drives the web view experiment.
func runWebView(window *mygo.Window, a *appState, dir string) {
	scenario := "webview"
	_ = window
	// Scenarios start on the UI goroutine: sleeping there stops the window from
	// drawing, so the whole check moves to its own goroutine.
	go func() {
		// The native players need a moment; the page loads in parallel.
		time.Sleep(3 * time.Second)
		logf(scenario, "native snapshot before the page acts: %s", snapshotLine(a.snapshot()))

		// The page clicks the second source's Show button 2.5 seconds after load;
		// by now the command should have gone through the boundary.
		waitForCommand(scenario, a, commandShow, 10*time.Second)
		logf(scenario, "native snapshot after the page acted: %s", snapshotLine(a.snapshot()))

		// Push a fresh view to the page and give it a moment to render.
		mygo.RunOnMain(a.broadcastPanel)
		time.Sleep(800 * time.Millisecond)
		step(window, a, dir, scenario, "1-both-interfaces.png",
			"the native stage and the page below it read one session", nil)

		logf(scenario, "last command: %s", a.lastCommand.String())
		mygo.RunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

// waitForCommand waits until the session records a command of the given kind.
func waitForCommand(scenario string, a *appState, kind commandKind, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var last commandResult
		mygo.RunOnMain(func() { last = a.lastCommand })
		if last.Command.Kind == kind && last.Err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logf(scenario, "the page never sent %s through the boundary", kind)
}
