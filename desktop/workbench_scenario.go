// P15.1: the workbench at five window sizes, captured in a real window. Each
// size is set through the window API, so the frame reports the new size to the
// layout instead of the scenario pretending which panes should show.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// workbenchSizes are the five widths the acceptance asks for: a large desktop,
// the inspector threshold, the rail threshold, a small laptop, and the narrow
// floor where every pane is folded.
var workbenchSizes = []struct {
	width, height int
	note          string
}{
	{1440, 900, "large desktop: rail, inspector and dock all present"},
	{1180, 760, "inspector threshold: all three panes just fit"},
	{1000, 720, "rail threshold: inspector folded, rail and dock present"},
	{820, 620, "small window: only the global bar and the stage remain"},
	{640, 560, "floor: panes folded, the picture is not squeezed under them"},
}

// runWorkbench walks the five sizes and captures the frame at each.
func runWorkbench(window *mygo.Window, a *appState, dir string) {
	scenario := "workbench"
	go func() {
		time.Sleep(3 * time.Second)
		for i, size := range workbenchSizes {
			width, height := size.width, size.height
			mygoRunOnMain(func() {
				window.SetSize(width, height)
				window.Invalidate()
			})
			time.Sleep(500 * time.Millisecond)
			mygoRunOnMain(func() {
				logf(scenario, "%s | %s", a.layoutLine(a.frameWidth, a.frameHeight), size.note)
			})
			name := fmt.Sprintf("%d-%dx%d.png", i+1, width, height)
			step(window, a, dir, scenario, name, size.note, nil)
		}
		// A person can reopen what the size rule folded; prove the switch
		// survives one more resize instead of snapping back.
		mygoRunOnMain(func() {
			a.inspector = paneOpen
			a.rail = paneOpen
			window.SetSize(640, 560)
			window.Invalidate()
		})
		time.Sleep(500 * time.Millisecond)
		mygoRunOnMain(func() {
			logf(scenario, "manual panes at 640x560 | %s | %s", a.layoutLine(a.frameWidth, a.frameHeight), fmt.Sprintf("frame %.0fx%.0f", a.frameWidth, a.frameHeight))
		})
		step(window, a, dir, scenario, "6-manual-at-640x560.png",
			"inspector and rail reopened by hand at the smallest size", nil)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
