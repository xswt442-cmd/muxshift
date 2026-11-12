// The Windows-side acceptance walk for P18.1: the display the window is really
// on, the content it really gets at 100/150/200%, the smallest window the
// workbench claims to support, and the window states a person switches between.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// windowsScales are the display scales P18.1 names. Unlike the P15 walk these
// are run inside the real work area, so a scale that cannot fit the widest
// window is measured at what the screen really allows.
var windowsScales = []struct {
	name string
	zoom float32
}{
	{"100", 1},
	{"150", 1.5},
	{"200", 2},
}

// minContentWidth and minContentHeight are the smallest content size the
// workbench still draws every core control at (P15.5). The window pixels needed
// for it grow with the scale.
const (
	minContentWidth  = 626
	minContentHeight = 522
)

func runWindows(window *mygo.Window, a *appState, dir string) {
	go windowsBody(window, a, dir)
}

func windowsBody(window *mygo.Window, a *appState, dir string) {
	scenario := "windows"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	displays := mygo.Screen.Displays()
	for index, display := range displays {
		logf(scenario, "display %d %q: bounds %dx%d at %d,%d · work area %dx%d at %d,%d · scale %.2f · primary %v · internal %v",
			index, display.Label, display.Bounds.Width, display.Bounds.Height, display.Bounds.X, display.Bounds.Y,
			display.WorkArea.Width, display.WorkArea.Height, display.WorkArea.X, display.WorkArea.Y,
			display.ScaleFactor, display.Primary, display.Internal)
	}
	check(len(displays) > 0, "at least one display was reported")

	original := window.Bounds()
	home := displayHolding(original, displays)
	logf(scenario, "the window starts at %dx%d at %d,%d, on the %q display (scale %.2f, work area %dx%d)",
		original.Width, original.Height, original.X, original.Y, home.Label, home.ScaleFactor,
		home.WorkArea.Width, home.WorkArea.Height)
	check(intersects(original, home.WorkArea), "the window starts inside a work area: %dx%d at %d,%d",
		original.Width, original.Height, original.X, original.Y)

	// SetSize sizes the frame, not the content. Measure the frame once, at
	// 100%, so every scale can be compared with the content it should get
	// instead of with the pixels asked for.
	step(window, a, dir, scenario, "", "measure the frame", func(a *appState) {
		a.zoom = 1
		window.SetSize(900, 700)
	})
	var frameWidth, frameHeight int
	mygoRunOnMain(func() {
		frameWidth = 900 - int(a.lastWidth)
		frameHeight = 700 - int(a.lastHeight)
	})
	logf(scenario, "the frame takes %dx%d pixels at 100%%: 900x700 pixels leave %.1fx%.1f DIPs",
		frameWidth, frameHeight, a.lastWidth, a.lastHeight)
	check(frameWidth >= 0 && frameWidth < 120 && frameHeight >= 0 && frameHeight < 120,
		"the frame is a plausible %dx%d pixels", frameWidth, frameHeight)

	// The scale walk. "fit" is the widest window this screen allows at the
	// scale; "min" is the smallest content the workbench promises to support.
	for _, scale := range windowsScales {
		fitWidth := int(float32(home.WorkArea.Width)) - 32
		fitHeight := int(float32(home.WorkArea.Height)) - 32
		fitWidth = minInt(fitWidth, int(1426*scale.zoom))
		fitHeight = minInt(fitHeight, int(862*scale.zoom))
		sizes := []struct {
			name string
			w, h int
		}{
			{"fit", fitWidth, fitHeight},
			{"min", int(minContentWidth*scale.zoom) + frameWidth, int(minContentHeight*scale.zoom) + frameHeight},
		}
		for _, size := range sizes {
			name := fmt.Sprintf("scale-%s-%s.png", scale.name, size.name)
			caption := fmt.Sprintf("scale %s%% in a %dx%d window", scale.name, size.w, size.h)
			zoom := scale.zoom
			width, height := size.w, size.h
			step(window, a, dir, scenario, name, caption, func(a *appState) {
				a.zoom = zoom
				window.SetSize(width, height)
			})
			var dips float32
			var rail, inspector, dock bool
			mygoRunOnMain(func() {
				dips = a.lastWidth
				rail, inspector, dock = a.railOpen(a.lastWidth), a.inspectorOpen(a.lastWidth), a.dockOpen(a.lastWidth, a.lastHeight)
				logf(scenario, "scale %s%% at %dx%d: the content sees %.1fx%.1f DIPs (rail %v, inspector %v, dock %v)",
					scale.name, width, height, a.lastWidth, a.lastHeight, rail, inspector, dock)
			})
			wantWidth := float32(width-frameWidth) / zoom
			check(abs32(dips-wantWidth) <= 2, "scale %s%%: the content got %.1f DIPs from %dx%d pixels (about %.1f)",
				scale.name, dips, width, height, wantWidth)
			check(rail == (dips >= railAutoWidth), "scale %s%%: the rail fold (%v) follows the %.1f DIPs it got",
				scale.name, rail, dips)
			check(inspector == (dips >= inspectorAutoWidth), "scale %s%%: the inspector fold (%v) follows the %.1f DIPs it got",
				scale.name, inspector, dips)
			if size.name == "min" {
				check(abs32(dips-float32(minContentWidth)) <= 1.5,
					"scale %s%%: the smallest window laid out at %.1f DIPs (%d)", scale.name, dips, minContentWidth)
			}
			bounds := window.Bounds()
			check(intersects(bounds, home.WorkArea), "scale %s%% at %dx%d stayed in the work area: %dx%d at %d,%d",
				scale.name, width, height, bounds.Width, bounds.Height, bounds.X, bounds.Y)
		}
	}

	// Back to a normal scale before the state walk, so the states are measured
	// at the scale a person would run the app at.
	step(window, a, dir, scenario, "", "back at 100%", func(a *appState) {
		a.zoom = 1
		window.SetSize(original.Width, original.Height)
	})

	// Maximize and restore: a person expects the un-maximized size to come back.
	step(window, a, dir, scenario, "1-maximized.png", "maximized", func(*appState) { window.Maximize() })
	time.Sleep(700 * time.Millisecond)
	maximized := window.Bounds()
	logf(scenario, "maximized: %dx%d at %d,%d, the work area is %dx%d at %d,%d (IsMaximized %v)",
		maximized.Width, maximized.Height, maximized.X, maximized.Y,
		home.WorkArea.Width, home.WorkArea.Height, home.WorkArea.X, home.WorkArea.Y, window.IsMaximized())
	check(window.IsMaximized(), "the window reported itself maximized")
	// A maximized window reports its frame, which reaches a few pixels beyond
	// the work area on each side.
	check(absInt(maximized.Width, home.WorkArea.Width) <= 20 && absInt(maximized.Height, home.WorkArea.Height) <= 20,
		"a maximized window is %dx%d for a %dx%d work area", maximized.Width, maximized.Height, home.WorkArea.Width, home.WorkArea.Height)

	step(window, a, dir, scenario, "2-restored.png", "unmaximized", func(*appState) { window.Unmaximize() })
	time.Sleep(500 * time.Millisecond)
	restored := window.Bounds()
	logf(scenario, "unmaximized: %dx%d at %d,%d (IsMaximized %v)", restored.Width, restored.Height, restored.X, restored.Y, window.IsMaximized())
	check(!window.IsMaximized(), "the window reported itself unmaximized")
	check(absInt(restored.Width, original.Width) <= 8 && absInt(restored.Height, original.Height) <= 8,
		"unmaximizing gave %dx%d, from %dx%d before maximizing", restored.Width, restored.Height, original.Width, original.Height)

	// Full screen covers the display, and leaving it gives the window back.
	step(window, a, dir, scenario, "3-fullscreen.png", "full screen", func(*appState) { window.SetFullScreen(true) })
	time.Sleep(700 * time.Millisecond)
	full := window.Bounds()
	logf(scenario, "full screen: %dx%d at %d,%d (IsFullScreen %v)", full.Width, full.Height, full.X, full.Y, window.IsFullScreen())
	check(window.IsFullScreen(), "the window reported itself full screen")
	check(full.Width >= home.Bounds.Width && full.Height >= home.Bounds.Height,
		"full screen is %dx%d for a %dx%d display", full.Width, full.Height, home.Bounds.Width, home.Bounds.Height)
	step(window, a, dir, scenario, "", "left full screen", func(*appState) { window.SetFullScreen(false) })
	time.Sleep(500 * time.Millisecond)

	// Minimize and bring the window back: nothing should be resized by it.
	before := window.Bounds()
	step(window, a, dir, scenario, "", "minimized", func(*appState) { window.Minimize() })
	time.Sleep(500 * time.Millisecond)
	step(window, a, dir, scenario, "4-restored-after-minimize.png", "restored from minimized", func(*appState) { window.Restore() })
	time.Sleep(400 * time.Millisecond)
	after := window.Bounds()
	logf(scenario, "minimize round trip: %dx%d at %d,%d -> %dx%d at %d,%d",
		before.Width, before.Height, before.X, before.Y, after.Width, after.Height, after.X, after.Y)
	check(absInt(after.Width, before.Width) <= 8 && absInt(after.Height, before.Height) <= 8,
		"minimizing kept the window size: %dx%d -> %dx%d", before.Width, before.Height, after.Width, after.Height)

	// A second display, when the machine has one.
	if len(displays) < 2 {
		logf(scenario, "one display: the second-monitor placement was not run")
	} else {
		other := displays[1]
		if other.Primary {
			other = displays[0]
		}
		target := other.WorkArea
		width := minInt(target.Width-64, int(1426*1.0))
		height := minInt(target.Height-64, 862)
		x := target.X + (target.Width-width)/2
		y := target.Y + (target.Height-height)/2
		step(window, a, dir, scenario, "5-second-monitor.png", "moved to "+other.Label, func(*appState) {
			window.SetBounds(mygo.Rectangle{X: x, Y: y, Width: width, Height: height})
		})
		time.Sleep(700 * time.Millisecond)
		moved := window.Bounds()
		logf(scenario, "second monitor %q: asked for %dx%d at %d,%d, the window reports %dx%d at %d,%d",
			other.Label, width, height, x, y, moved.Width, moved.Height, moved.X, moved.Y)
		check(intersects(moved, other.WorkArea), "the window landed on the %q display", other.Label)
		step(window, a, dir, scenario, "", "back on the first display", func(*appState) {
			window.SetBounds(original)
		})
	}

	logf(scenario, "checks failed: %d", failures)
	a.release()
	mygo.App.Quit()
}

// displayHolding picks the display the rectangle sits on, and the primary when
// it sits on none.
func displayHolding(r mygo.Rectangle, displays []mygo.Display) mygo.Display {
	for _, display := range displays {
		if intersects(r, display.WorkArea) {
			return display
		}
	}
	for _, display := range displays {
		if display.Primary {
			return display
		}
	}
	if len(displays) > 0 {
		return displays[0]
	}
	return mygo.Display{}
}

// intersects reports whether two rectangles share any area.
func intersects(a, b mygo.Rectangle) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
