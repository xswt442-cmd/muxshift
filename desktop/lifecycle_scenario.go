// P14.4: lifecycle cases the baseline matrix does not cover — a burst of
// reloads, removing a source while the window runs, adding one at runtime,
// opening an address that fails, and closing the last player. Each case records
// what the players and the session actually did.
package main

import (
	"time"

	"github.com/egoist/mygo"
)

func runLifecycle(window *mygo.Window, a *appState, dir string) {
	scenario := "lifecycle"
	go func() {
		time.Sleep(300 * time.Millisecond)
		start := time.Now()

		// 1. Every source the command line named must be ready before the cases
		// change anything, so a later failure cannot be blamed on start-up.
		for i := range a.items {
			sample := a.sampleOnMain(i, start)
			logf(scenario, "start: %s", sample.line())
		}

		// 2. A burst of reloads: three replacements 150ms apart. Only the last
		// one may own the source, and the handovers must not close a player that
		// a later one draws.
		if len(a.items) > 0 {
			for round := 1; round <= 3; round++ {
				mygoRunOnMain(func() { a.reopenPlayer(a.items[0]) })
				logf(scenario, "reload burst: asked for replacement %d", round)
				time.Sleep(150 * time.Millisecond)
			}
			ready := waitPlayerReady("lifecycle", rawPlayer(a.items[0].player), 25*time.Second)
			sample := a.sampleOnMain(0, start)
			logf(scenario, "reload burst: ready=%t generation=%d %s", ready, a.generationOf(0), sample.line())
			step(window, a, dir, scenario, "1-after-burst.png", "after the reload burst", nil)
		}

		// 3. Remove the second source while the window runs; the first keeps
		// drawing and the session keeps exactly one source.
		if len(a.items) > 1 {
			var err error
			mygoRunOnMain(func() { err = a.remove(1) })
			if err != nil {
				logf(scenario, "remove source 2 failed: %v", err)
			} else {
				logf(scenario, "remove source 2: %d source(s) left in the window, %d in the session, shown=%s",
					len(a.items), len(a.session.Sources()), a.sceneLabel())
			}
			time.Sleep(700 * time.Millisecond)
			step(window, a, dir, scenario, "2-after-remove.png", "after removing the second source", nil)
		}

		// 4. Add a source while the window runs: the case P8 left open. The
		// result is logged whichever way it goes.
		var added *item
		var addErr error
		mygoRunOnMain(func() {
			added, addErr = a.add(a.lifecycleAddition())
			if addErr == nil {
				a.items = append(a.items, added)
				a.apply()
			}
		})
		if addErr != nil {
			logf(scenario, "adding a source at runtime failed: %v", addErr)
		} else {
			index := len(a.items) - 1
			mygoRunOnMain(func() {
				if err := a.showVideo(index); err != nil {
					logf(scenario, "showing the added source failed: %v", err)
				}
			})
			ready := waitPlayerReady("lifecycle", rawPlayer(added.player), 15*time.Second)
			sample := a.sampleOnMain(index, start)
			logf(scenario, "added a source at runtime: ready=%t %s", ready, sample.line())
			step(window, a, dir, scenario, "3-added-at-runtime.png", "a source added at runtime", nil)
			mygoRunOnMain(func() {
				if err := a.remove(index); err != nil {
					logf(scenario, "removing the added source failed: %v", err)
				}
			})
			logf(scenario, "removed the added source: %d source(s) left", len(a.items))
		}

		// 5. An address that cannot open must report an error and leave the live
		// source drawing. The open call itself is asynchronous in this engine, so
		// the error is read from the player instead of from the call's return.
		bad, openErr := openPlayerOnMain("http://127.0.0.1:9/not-there.m3u8")
		if openErr != nil {
			logf(scenario, "opening an unreachable address: open returned %v", openErr)
		} else if bad != nil {
			var state engineState
			deadline := time.Now().Add(6 * time.Second)
			for {
				state = readPlayer(bad)
				if state.Ready || state.Err != "" || time.Now().After(deadline) {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			logf(scenario, "opening an unreachable address: no error on the call; ready=%t err=%s", state.Ready, emptyWord(state.Err))
			mygoRunOnMain(func() { bad.Close() })
		}
		if len(a.items) > 0 {
			sample := a.sampleOnMain(0, start)
			logf(scenario, "after the failed open: %s", sample.line())
		}

		// 6. Close everything; the process must exit rather than hang on a
		// player it cannot release.
		mygoRunOnMain(func() {
			for len(a.items) > 0 {
				if err := a.remove(0); err != nil {
					logf(scenario, "closing source %d: %v", 1, err)
					break
				}
			}
			logf(scenario, "closed every source: %d left, %d event(s)", len(a.items), len(a.session.Events()))
			a.release()
			mygo.App.Quit()
		})
	}()
}

// generationOf reads one source's generation on the drawing thread.
func (a *appState) generationOf(index int) int {
	generation := 0
	mygoRunOnMain(func() { generation = a.items[index].generation })
	return generation
}

// lifecycleAddition is the local file the lifecycle scenario adds while the
// window runs. It falls back to the first source's address when that is itself
// local, so the run needs no extra argument.
func (a *appState) lifecycleAddition() string {
	for _, it := range a.items {
		if !isNetworkAddress(it.uri) {
			return it.uri
		}
	}
	return a.items[0].uri
}
