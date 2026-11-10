// P15.5: the same window at three workstation sizes and with zero, one, two and
// four sources. Every step captures the stage and the sources page, and the log
// records the client area, which panes folded, and how many sources and tracks
// the session holds, so a clipped label or a pane that hid when it should not
// have can be seen next to the numbers that explain it.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// runSizes walks the three workstation sizes from wide to small with whatever
// sources the command line gave, then restores the default size.
func runSizes(window *mygo.Window, a *appState, dir string) {
	sizesWalk(window, a, dir, "sizes", false)
}

// runSizesEmpty is the same walk after removing every source, which is the only
// way to reach the empty session: the app refuses to start without -media, so a
// zero-source window has to be emptied rather than launched.
func runSizesEmpty(window *mygo.Window, a *appState, dir string) {
	sizesWalk(window, a, dir, "sizes-empty", true)
}

func sizesWalk(window *mygo.Window, a *appState, dir, scenario string, empty bool) {
	sizes := []struct {
		name   string
		width  int
		height int
	}{
		{"wide", 1440, 900},
		{"medium", 1166, 802},
		{"small", 900, 600},
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		if empty {
			mygoRunOnMain(func() {
				for len(a.items) > 0 {
					if err := a.remove(0); err != nil {
						logf(scenario, "remove the last sources: %v", err)
						break
					}
				}
				window.Invalidate()
			})
			time.Sleep(600 * time.Millisecond)
			logf(scenario, "emptied the session: %s", a.sessionState())
		}
		for _, size := range sizes {
			mygoRunOnMain(func() {
				window.SetSize(size.width, size.height)
				a.workspace = workspaceStage
				window.Invalidate()
			})
			time.Sleep(700 * time.Millisecond)
			step(window, a, dir, scenario, fmt.Sprintf("stage-%s.png", size.name),
				fmt.Sprintf("the stage at %s with %d source(s)", size.name, len(a.items)),
				func(a *appState) {
					logf(scenario, "%s | %s", size.name, a.layoutLine(a.frameWidth, a.frameHeight))
				})
			mygoRunOnMain(func() {
				a.workspace = workspaceSources
				window.Invalidate()
			})
			time.Sleep(500 * time.Millisecond)
			step(window, a, dir, scenario, fmt.Sprintf("sources-%s.png", size.name),
				fmt.Sprintf("the sources page at %s with %d source(s)", size.name, len(a.items)), nil)
			if len(a.items) > 0 {
				step(window, a, dir, scenario, fmt.Sprintf("inspector-%s.png", size.name),
					fmt.Sprintf("the first source selected at %s", size.name),
					func(a *appState) {
						a.selected = a.items[0].source.ID
					})
			}
		}
		mygoRunOnMain(func() {
			window.SetSize(1166, 802)
			a.workspace = workspaceStage
			a.selected = ""
			window.Invalidate()
		})
		time.Sleep(600 * time.Millisecond)
		step(window, a, dir, scenario, "restored.png", "the stage at the default size again", func(a *appState) {
			logf(scenario, "restored: %s | %s", a.layoutLine(a.frameWidth, a.frameHeight), a.sessionState())
		})
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
