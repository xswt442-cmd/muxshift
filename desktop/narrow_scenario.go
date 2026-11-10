// P15.4: the narrow window, where the status bar and its buttons are most
// likely to be squeezed, plus the feedback an import shows while it reads and
// when it fails. Each step is captured, and the state behind it is logged.
package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runNarrow exercises the narrow floor with a reading import, a cancelled
// import, and a real failure, so the status bar and its buttons are checked
// with the longest text they ever hold.
func runNarrow(window *mygo.Window, a *appState, dir string) {
	scenario := "narrow"
	go func() {
		time.Sleep(2500 * time.Millisecond)
		mygoRunOnMain(func() {
			window.SetSize(640, 560)
			a.workspace = workspaceSources
			window.Invalidate()
		})
		time.Sleep(700 * time.Millisecond)
		step(window, a, dir, scenario, "1-narrow-sources.png", "the sources page at 640x560", func(_ *appState) {
			logf(scenario, "%s", a.layoutLine(a.frameWidth, a.frameHeight))
		})

		// A read that cannot finish: the address is unroutable, so the probe
		// sits in the connect until the import's own timeout or Cancel.
		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandAdd, URI: "http://10.255.255.1/never.m3u8"}))
		})
		time.Sleep(1500 * time.Millisecond)
		step(window, a, dir, scenario, "2-reading.png", "Reading… and Cancel while the read is in flight", func(_ *appState) {
			logf(scenario, "in flight: %q, last command %q", a.importing, a.lastCommand.Message)
		})

		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandCancelImport}))
		})
		time.Sleep(700 * time.Millisecond)
		step(window, a, dir, scenario, "3-cancelled.png", "after Cancel: nothing added, the row is idle again", func(_ *appState) {
			logf(scenario, "after cancel: importing=%q, last command %q", a.importing, a.lastCommand.Message)
		})

		// A read that fails at once: connection refused, so the failure reaches
		// the status bar and the global error button together.
		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandAdd, URI: "http://127.0.0.1:9/not-there.m3u8"}))
		})
		time.Sleep(1500 * time.Millisecond)
		step(window, a, dir, scenario, "4-error.png", "a refused import: error button, status bar and Dismiss", func(_ *appState) {
			logf(scenario, "after failure: lastErr=%v, sources=%d", a.lastErr, len(a.items))
		})

		mygoRunOnMain(func() {
			a.lastErr = nil
			window.Invalidate()
		})
		time.Sleep(500 * time.Millisecond)
		step(window, a, dir, scenario, "5-dismissed.png", "after Dismiss: the status bar keeps the record, the button is gone", func(_ *appState) {
			logf(scenario, "after dismiss: lastErr=%v, last command %q", a.lastErr, a.lastCommand.Message)
		})
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
