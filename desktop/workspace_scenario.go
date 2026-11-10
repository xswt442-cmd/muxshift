// P13.5: the rail's three pages, captured one at a time in a real window.
package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runWorkspaces walks the rail and captures each page.
func runWorkspaces(window *mygo.Window, a *appState, dir string) {
	scenario := "workspaces"
	go func() {
		time.Sleep(3 * time.Second)
		step(window, a, dir, scenario, "1-stage.png", "the stage page on open", nil)

		mygoRunOnMain(func() { a.workspace = workspaceSources; window.Invalidate() })
		time.Sleep(600 * time.Millisecond)
		step(window, a, dir, scenario, "2-sources.png", "the sources page", nil)

		mygoRunOnMain(func() { a.workspace = workspaceDiagnostics; window.Invalidate() })
		time.Sleep(600 * time.Millisecond)
		step(window, a, dir, scenario, "3-diagnostics.png", "the diagnostics page", nil)

		var page workspace
		mygoRunOnMain(func() { page = a.workspace })
		logf(scenario, "the rail ends on %q with %d sources and %d events",
			page.title(), len(a.items), len(a.session.Events()))

		mygoRunOnMain(func() { a.workspace = workspaceStage; window.Invalidate() })
		time.Sleep(400 * time.Millisecond)
		step(window, a, dir, scenario, "4-back-to-stage.png", "back on the stage", nil)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
