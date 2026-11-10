// P15.2: the Sources experience. The scenario drives the same commands the
// Sources page's buttons send — an import, a retry, a removal, a selection —
// and captures what the page and the inspector show afterwards. It does not
// click through the UI (this build has no synthetic clicks); the commands and
// the state they change are the same ones the buttons call.
package main

import (
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

func runSources(window *mygo.Window, a *appState, dir string) {
	scenario := "sources"
	go func() {
		time.Sleep(600 * time.Millisecond)
		mygoRunOnMain(func() { a.workspace = workspaceSources })
		logf(scenario, "start: %d source(s), selection %q", len(a.items), a.selected)
		step(window, a, dir, scenario, "1-sources.png", "the Sources page with its address field and cards", nil)

		// 1. Select the second source the way a click on its row does.
		if len(a.items) > 1 {
			mygoRunOnMain(func() { a.selected = a.items[1].source.ID })
			step(window, a, dir, scenario, "2-selected.png", "the second source selected, the inspector follows it", nil)
			mygoRunOnMain(func() {
				logf(scenario, "selected %s; inspector shows source 2", a.selected)
			})
		}

		// 2. Import a local file through the command the Add button sends. The
		// read runs off the drawing thread; this waits for the command to land.
		uri := ""
		mygoRunOnMain(func() {
			a.selected = ""
			uri = a.lifecycleAddition()
			// Prefer a local file that is not already open: importing the same
			// file a second time would mix two questions (a new player mid-run
			// and the same file opened twice).
			other := `E:\\.codes\\muxshift\\testplace\\media\\local\\long.mp4`
			if _, statErr := os.Stat(other); statErr == nil {
				uri = other
			}
		})
		if uri != "" {
			before := len(a.items)
			mygoRunOnMain(func() {
				a.importText = uri
				a.report(a.runCommand(command{Kind: commandAdd, URI: a.importText}))
			})
			deadline := time.Now().Add(20 * time.Second)
			for {
				done := false
				mygoRunOnMain(func() { done = a.importing == "" && len(a.items) > before })
				if done || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				logf(scenario, "import: %d source(s) in the window, selected=%q, field=%q, last command: %s",
					len(a.items), a.selected, a.importText, a.lastCommand.String())
			})
			step(window, a, dir, scenario, "3-imported.png", "a file imported from the address field; it is selected", nil)

			// 2b. The player opened at import; give it time and say how long it
			// took to have a picture, before anything shows it.
			opened := time.Now()
			readyAtImport := false
			for {
				mygoRunOnMain(func() {
					if len(a.items) > 0 {
						last := a.items[len(a.items)-1]
						readyAtImport = last.player != nil && last.player.Ready()
					}
				})
				if readyAtImport || time.Since(opened) > 8*time.Second {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				last := a.items[len(a.items)-1]
				logf(scenario, "import: player opened at import: ready=%v after %s, position=%s",
					readyAtImport, time.Since(opened).Round(time.Millisecond), last.player.Position())
			})

			// 2c. Show it: the stage must draw the new source and its player
			// must keep advancing.
			var added core.SourceID
			mygoRunOnMain(func() {
				if len(a.items) > 0 {
					added = a.items[len(a.items)-1].source.ID
				}
			})
			mygoRunOnMain(func() {
				a.report(a.runCommand(command{Kind: commandShow, Source: added}))
			})
			showDeadline := time.Now().Add(8 * time.Second)
			for {
				ready := false
				mygoRunOnMain(func() {
					if len(a.items) > 0 {
						last := a.items[len(a.items)-1]
						ready = last.player != nil && last.player.Ready()
					}
				})
				if ready || time.Now().After(showDeadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				last := a.items[len(a.items)-1]
				logf(scenario, "show %s: player=%v ready=%v picture=%v, last command: %s",
					added, last.player != nil, last.player != nil && last.player.Ready(),
					a.snapshot().Picture, a.lastCommand.String())
			})
			step(window, a, dir, scenario, "3b-shown.png", "the imported source shown: its player came up and draws it", nil)
		}

		// 3. Import an address that fails, then retry it: both must show a real
		// error and the Retry button must be the one that carries it.
		before := len(a.items)
		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandAdd, URI: "http://127.0.0.1:9/not-there.m3u8"}))
		})
		deadline := time.Now().Add(20 * time.Second)
		for {
			done := false
			mygoRunOnMain(func() { done = a.importing == "" && a.lastCommand.Err != nil })
			if done || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		var failedURI string
		mygoRunOnMain(func() {
			logf(scenario, "failed import: %d source(s), last command: %s", len(a.items), a.lastCommand.String())
			if a.lastCommand.Command.Kind == commandAdd && a.lastCommand.Err != nil {
				failedURI = a.lastCommand.Command.URI
			}
		})
		if len(a.items) == before {
			logf(scenario, "a failed import adds no card; the import row offers Retry for the address it remembers")
		}
		step(window, a, dir, scenario, "4-failed-import.png", "an address that cannot be read: the row keeps the error and a Retry", nil)

		// 3b. Retry the failed address from the same row: it must fail again,
		// not silently succeed or freeze the window.
		if failedURI != "" {
			mygoRunOnMain(func() {
				a.report(a.runCommand(command{Kind: commandAdd, URI: failedURI}))
			})
			deadline := time.Now().Add(20 * time.Second)
			for {
				done := false
				mygoRunOnMain(func() { done = a.importing == "" })
				if done || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			mygoRunOnMain(func() {
				logf(scenario, "retry of %s: %d source(s), last command: %s", failedURI, len(a.items), a.lastCommand.String())
			})
		}

		// 4. Remove the first source through the command the card sends.
		if len(a.items) > 0 {
			var removed core.SourceID
			mygoRunOnMain(func() {
				removed = a.items[0].source.ID
				a.report(a.runCommand(command{Kind: commandRemove, Source: removed}))
			})
			mygoRunOnMain(func() {
				logf(scenario, "remove %s: %d source(s) left, selected=%q, last command: %s",
					removed, len(a.items), a.selected, a.lastCommand.String())
			})
			step(window, a, dir, scenario, "5-after-remove.png", "after removing a source: the list and the inspector agree", nil)
		}

		// 5. Clicking a card selects it; clearing the selection falls back to
		// the session summary.
		mygoRunOnMain(func() { a.selected = "" })
		step(window, a, dir, scenario, "6-session-summary.png", "no selection: the inspector shows the session summary", nil)

		mygoRunOnMain(func() {
			logf(scenario, "done: %d source(s), %d event(s), selection %q",
				len(a.items), len(a.session.Events()), a.selected)
			a.release()
			mygo.App.Quit()
		})
	}()
}
