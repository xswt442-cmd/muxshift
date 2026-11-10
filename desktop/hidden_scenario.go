// P16.0 regression: the picture has to open and keep running while a page other
// than the stage is displayed. The ruling asked for three checks, and this
// scenario performs them in one run: the first frame arrives while the Sources
// page is up, a source imported from that page reaches the stage, and walking
// Stage -> Settings -> Sources -> Stage neither stops nor resets the media.
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"github.com/xswt442-cmd/muxshift/core"
)

// runHidden walks the three checks. It is deliberately pessimistic: a player
// that never becomes ready is reported as a failure and the run stops there,
// because a picture stuck at "opening" must not read as a pass.
func runHidden(window *mygo.Window, a *appState, dir string) {
	go func() {
		// 1. The picture opens for the first time while another page is on
		// screen. The stage has not been drawn yet, so this is the case the
		// P16.0 experiment left open: a player opened while hidden.
		mygoRunOnMain(func() {
			a.workspace = workspaceSources
			window.Invalidate()
		})
		logf("hidden", "the Sources page is up before the first frame")
		if !waitForFirstPicture(a, 20*time.Second) {
			logf("hidden", "no first frame while hidden within 20s: %s", allStates(a))
			step(window, a, dir, "hidden", "1-hidden-open.png", "the Sources page after the picture failed to open", nil)
			mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
			return
		}
		opening := positionOf(a, 0)
		step(window, a, dir, "hidden", "1-hidden-open.png", "the Sources page holding the opening picture", nil)

		// 2. It has to keep running while nothing draws it.
		time.Sleep(3 * time.Second)
		hidden := positionOf(a, 0)
		logf("hidden", "three seconds hidden: position %s -> %s, advanced=%v",
			sceneClock(opening), sceneClock(hidden), moved(opening, hidden))

		// 3. Back on the stage the arriving frame is the current one.
		step(window, a, dir, "hidden", "2-stage.png", "the stage after the picture opened hidden", func(a *appState) {
			a.workspace = workspaceStage
		})

		// 4. Import a second local file from the Sources page and let it open
		// while that page is up.
		uri := a.hiddenAddition()
		before := sourceCount(a)
		mygoRunOnMain(func() {
			a.workspace = workspaceSources
			a.report(a.runCommand(command{Kind: commandAdd, URI: uri}))
		})
		waitUntil(25*time.Second, func() bool {
			done := false
			mygoRunOnMain(func() { done = a.importing == "" && len(a.items) > before })
			return done
		})
		added := sourceCount(a)
		index := added - 1
		imported := added > before && waitForReady(a, index, 20*time.Second)
		logf("hidden", "import of %s while hidden: %d source(s), ready=%v", uri, added, imported)
		step(window, a, dir, "hidden", "3-imported.png", "the Sources page after the import", nil)
		if imported {
			first := positionOf(a, index)
			time.Sleep(2 * time.Second)
			second := positionOf(a, index)
			logf("hidden", "imported source hidden for 2s: position %s -> %s, advanced=%v",
				sceneClock(first), sceneClock(second), moved(first, second))
			step(window, a, dir, "hidden", "4-import-stage.png", "the stage showing the imported source", func(a *appState) {
				a.workspace = workspaceStage
				a.report(a.runCommand(command{Kind: commandShow, Source: sourceAt(a, index)}))
			})
		} else {
			logf("hidden", "the imported source did not open while hidden; it is not shown on the stage")
		}

		// 5. Stage -> Settings -> Sources -> Stage: no stop, no reset, and the
		// pages must not be covered by the parked picture.
		walk := make([]time.Duration, 0, 3)
		step(window, a, dir, "hidden", "5-settings.png", "the settings page with the picture parked", func(a *appState) {
			a.workspace = workspaceSettings
		})
		walk = append(walk, positionOf(a, index))
		step(window, a, dir, "hidden", "6-sources.png", "the sources page with the picture parked", func(a *appState) {
			a.workspace = workspaceSources
		})
		walk = append(walk, positionOf(a, index))
		step(window, a, dir, "hidden", "7-final-stage.png", "the stage after the walk", func(a *appState) {
			a.workspace = workspaceStage
		})
		walk = append(walk, positionOf(a, index))
		reset := false
		for i := 1; i < len(walk); i++ {
			if !moved(walk[i-1], walk[i]) {
				reset = true
			}
		}
		logf("hidden", "walk readings %s, %s, %s | stopped or reset=%v",
			sceneClock(walk[0]), sceneClock(walk[1]), sceneClock(walk[2]), reset)

		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

// waitReady polls until the first item has a ready player, so a slow file cannot
// be mistaken for a stalled one.
func waitForFirstPicture(a *appState, limit time.Duration) bool {
	return waitForReady(a, 0, limit)
}

// sceneClock prints a duration without its nanoseconds, which is noise here.
func sceneClock(d time.Duration) string {
	return d.Round(10 * time.Millisecond).String()
}

// positionOf reads one source's position on the drawing thread, where the
// players live.
func positionOf(a *appState, index int) time.Duration {
	var pos time.Duration
	mygoRunOnMain(func() {
		if index >= 0 && index < len(a.items) && a.items[index].player != nil {
			pos = a.items[index].player.Position()
		}
	})
	return pos
}

// sourceAt reads one source's ID on the drawing thread.
func sourceAt(a *appState, index int) core.SourceID {
	var id core.SourceID
	mygoRunOnMain(func() {
		if index >= 0 && index < len(a.items) {
			id = a.items[index].source.ID
		}
	})
	return id
}

// sourceCount reads the number of sources on the drawing thread.
func sourceCount(a *appState) int {
	count := 0
	mygoRunOnMain(func() { count = len(a.items) })
	return count
}

// waitForReady waits for one source's player to produce a frame.
func waitForReady(a *appState, index int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() {
			if index >= 0 && index < len(a.items) && a.items[index].player != nil {
				ready = a.items[index].player.Ready()
			}
		})
		if ready {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// waitUntil polls cond until it holds or the limit passes.
func waitUntil(limit time.Duration, cond func() bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// moved says whether the clock advanced enough to be more than sampling noise.
func moved(before, after time.Duration) bool {
	return after > before+500*time.Millisecond
}

// hiddenAddition picks a local file that is not already in the session, so the
// import opens a new player rather than a second handle on the first source.
func (a *appState) hiddenAddition() string {
	var candidates []string
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if isNetworkAddress(it.uri) {
				continue
			}
			dir := filepath.Dir(it.uri)
			for _, name := range []string{"long.mp4", "second.mp4"} {
				candidate := filepath.Join(dir, name)
				if candidate == it.uri {
					continue
				}
				if _, err := os.Stat(candidate); err == nil {
					candidates = append(candidates, candidate)
					return
				}
			}
		}
	})
	if len(candidates) > 0 {
		return candidates[0]
	}
	log.Printf("hidden: no second local file next to the media, importing the first source again")
	return a.lifecycleAddition()
}
