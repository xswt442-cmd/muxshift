// The workspace check is two processes, because the claim is about a restart:
// one leaves the window in a shape, the next one has to come back to it.
package main

import (
	"os"
	"time"

	"github.com/egoist/mygo"
)

// runWorkspace leaves the window in a known shape and writes it down: a moved
// frame, the Sources page and a closed rail. It uses the same apply path as the
// settings page and the same save-on-exit call as the close handler.
func runWorkspace(window *mygo.Window, a *appState, dir string) {
	go workspaceBody(window, a, dir)
}

func workspaceBody(window *mygo.Window, a *appState, dir string) {
	scenario := "workspace"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	step(window, a, dir, scenario, "1-left.png",
		"the window is at a known frame, on the Sources page, with the rail closed",
		func(a *appState) {
			window.SetBounds(mygo.Rectangle{X: 180, Y: 120, Width: 1000, Height: 760})
			a.workspace = workspaceSources
			a.rail = paneClosed
			a.settingsDraft = a.settings
			a.settingsDraft.Workspace.On = true
			a.applySettingsCommand()
		})
	mygoRunOnMain(func() {
		check(a.settingsErr == "", "apply reported %q", a.settingsErr)
	})
	time.Sleep(600 * time.Millisecond)
	mygoRunOnMain(func() {
		b := window.Bounds()
		logf(scenario, "leaving on page %v at %d,%d %dx%d, rail %s, saved %v",
			a.workspace, b.X, b.Y, b.Width, b.Height, paneModeName(a.rail), a.settingsSaved)
		check(b.Width == 1000 && b.Height == 760,
			"the window was set to 1000x760 (it is %dx%d)", b.Width, b.Height)
		// What closing the window does, without closing it: a scripted run that
		// quit here would race the write it is trying to prove.
		a.saveWorkspaceOnExit(window)
		if data, err := os.ReadFile(a.settingsPath); err == nil {
			logf(scenario, "the file now holds %s", oneLine(data))
		} else {
			logf(scenario, "could not read back %s: %v", a.settingsPath, err)
		}
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// runWorkspaceRestore is the second process: it starts with the file the first
// one wrote and says what came back. Every number below is read from the window
// and the app state, not from the file.
func runWorkspaceRestore(window *mygo.Window, a *appState, dir string) {
	go workspaceRestoreBody(window, a, dir)
}

func workspaceRestoreBody(window *mygo.Window, a *appState, dir string) {
	scenario := "workspace-restore"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	time.Sleep(700 * time.Millisecond)
	mygoRunOnMain(func() {
		b := window.Bounds()
		want := a.settings.Workspace
		logf(scenario, "loaded page %q, rail %q, inspector %q, dock %q, on %v",
			want.Page, want.Rail, want.Inspector, want.Dock, want.On)
		logf(scenario, "the window is at %d,%d %dx%d, page %v, rail %s",
			b.X, b.Y, b.Width, b.Height, a.workspace, paneModeName(a.rail))
		// The claim is that the window comes back the way the file says, so the
		// check reads what came back against what the file asked for; a frame
		// hard-coded here would only pass for one run. What the file asks for is
		// not always what a window may take: a frame saved on a larger screen or
		// at another scale can be clamped to stay reachable, which is a correct
		// restore rather than a failure. So the expectation is the file's frame
		// after the same clamp the restore itself applies.
		wantFrame := clampWorkspace(mygo.Rectangle{X: want.X, Y: want.Y, Width: want.Width, Height: want.Height},
			mygo.Screen.Displays())
		check(want.On, "the file asked for the window to come back")
		check(b.X == wantFrame.X && b.Y == wantFrame.Y && b.Width == wantFrame.Width && b.Height == wantFrame.Height,
			"the frame came back as %d,%d %dx%d (the file asked for %d,%d %dx%d, or %d,%d %dx%d once clamped)",
			b.X, b.Y, b.Width, b.Height, want.X, want.Y, want.Width, want.Height,
			wantFrame.X, wantFrame.Y, wantFrame.Width, wantFrame.Height)
		wantPage, pageOK := parseWorkspaceID(want.Page)
		check(pageOK && a.workspace == wantPage,
			"the page came back as %v (the file asked for %q)", a.workspace, want.Page)
		wantRail, railOK := parsePaneMode(want.Rail)
		check(railOK && a.rail == wantRail,
			"the rail came back as %s (the file asked for %q)", paneModeName(a.rail), want.Rail)
		capture(window, dir+`\1-restored.png`)
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}
