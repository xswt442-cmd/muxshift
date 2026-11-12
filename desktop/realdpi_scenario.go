package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// realDPI is the scripted half of the real-scale check (P18.1). It reads the
// scale the operating system actually reports, places the window at the
// smallest size that scale allows, checks the window stays inside the work
// area, and then watches what external pointer clicks do while it stands there.
// The clicks come from testplace/p18/realdpi: a click at the very edge of a
// control only lands on it if the hit regions and the picture agree, which is
// what a scaled display can break.
//
// The scenario changes no setting of the operating system. It measures the
// scale the machine is at and says so, so a reader can tell a run at the real
// 125% from one at 200%.
func runRealDPI(window *mygo.Window, a *appState, dir string) {
	go realDPIBody(window, a, dir)
}

func realDPIBody(window *mygo.Window, a *appState, dir string) {
	scenario := "realdpi"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	defer func() {
		if err := captureErr(window, dir+"\\realdpi.png"); err != nil {
			logf(scenario, "the screenshot could not be taken: %v", err)
		}
		logf(scenario, "done: %d check(s) failed", failures)
		mygoRunOnMain(func() { a.release() })
		mygo.App.Quit()
	}()
	time.Sleep(1200 * time.Millisecond)
	// A window that has not drawn its first frame is still hidden, and a
	// hidden window has no size worth measuring: show it and wait until the
	// bounds it reports are the ones a person would see.
	window.Show()
	visible := func() bool {
		var b mygo.Rectangle
		mygoRunOnMain(func() { b = window.Bounds() })
		return b.Width > 400 && b.Height > 300
	}
	for waited := time.Duration(0); waited < 20*time.Second && !visible(); waited += 250 * time.Millisecond {
		time.Sleep(250 * time.Millisecond)
	}
	check(visible(), "the window is on screen with a size before it is measured")

	displays := mygo.Screen.Displays()
	var home mygo.Display
	for _, d := range displays {
		logf(scenario, "display %q bounds %dx%d at %d,%d, work area %dx%d at %d,%d, scale %.2f, primary %v, internal %v",
			d.Label, d.Bounds.Width, d.Bounds.Height, d.Bounds.X, d.Bounds.Y,
			d.WorkArea.Width, d.WorkArea.Height, d.WorkArea.X, d.WorkArea.Y,
			d.ScaleFactor, d.Primary, d.Internal)
		if d.Primary {
			home = d
		}
	}
	if home.Bounds.Width == 0 && len(displays) > 0 {
		home = displays[0]
	}
	if len(displays) == 0 {
		logf(scenario, "no display was reported; nothing to measure")
		return
	}
	zoom := float64(home.ScaleFactor)
	if zoom <= 0 {
		zoom = 1
	}
	check(zoom > 0, "the system reports a scale factor: %.2f", zoom)

	var bounds mygo.Rectangle
	mygoRunOnMain(func() { bounds = window.Bounds() })
	logf(scenario, "the window reports %dx%d at %d,%d before it is placed", bounds.Width, bounds.Height, bounds.X, bounds.Y)

	// The smallest content the workbench draws is a fixed number of DIPs; the
	// frame around it is the title bar and border, which do not grow with the
	// scale, so they are added unmultiplied. They can only be measured from the
	// start-up size the window was actually opened at, which a run may have
	// overridden with -frame; when it does not match, the 15x38 measured at 100%
	// in the P18.1 windows run is used and the log says so rather than calling
	// the estimate a measurement.
	frameWidth, frameHeight, measured := 15, 38, false
	if bounds.Width > 0 && bounds.Height > 0 {
		fw := bounds.Width - int(1180*zoom)
		fh := bounds.Height - int(float64(700+70*len(a.items))*zoom)
		if fw >= 0 && fw <= 60 && fh >= 0 && fh <= 120 {
			frameWidth, frameHeight, measured = fw, fh, true
		}
	}
	if measured {
		logf(scenario, "the window frame measures %dx%d pixels at scale %.2f", frameWidth, frameHeight, zoom)
	} else {
		logf(scenario, "the window did not start at the size its frame would be measured from; using the 15x38 estimate measured at 100%% (scale %.2f)", zoom)
	}

	minWidth := int(minContentWidth*zoom) + frameWidth
	minHeight := int(minContentHeight*zoom) + frameHeight
	frame := centeredFrame(mygo.Rectangle{Width: minWidth, Height: minHeight}, home.WorkArea)
	mygoRunOnMain(func() { window.SetBounds(frame) })
	time.Sleep(700 * time.Millisecond)
	mygoRunOnMain(func() { bounds = window.Bounds() })
	logf(scenario, "asked for the smallest window at scale %.2f: %dx%d at %d,%d; it reports %dx%d at %d,%d",
		zoom, frame.Width, frame.Height, frame.X, frame.Y, bounds.Width, bounds.Height, bounds.X, bounds.Y)

	contentWidth := (float64(bounds.Width) - float64(frameWidth)) / zoom
	contentHeight := (float64(bounds.Height) - float64(frameHeight)) / zoom
	check(inside(bounds, home.WorkArea),
		"the smallest window is wholly inside the work area: %dx%d at %d,%d within %dx%d at %d,%d",
		bounds.Width, bounds.Height, bounds.X, bounds.Y,
		home.WorkArea.Width, home.WorkArea.Height, home.WorkArea.X, home.WorkArea.Y)
	check(contentWidth >= minContentWidth-6 && contentHeight >= minContentHeight-6,
		"the smallest window keeps %.1fx%.1f DIPs of content", contentWidth, contentHeight)

	// Stand still and record every change the external clicks make.
	type state struct {
		page          string
		rail          bool
		inspector     bool
		dock          bool
		railMode      string
		inspectorMode string
	}
	current := func() state {
		return state{
			page:          a.workspace.title(),
			rail:          a.railOpen(a.lastWidth),
			inspector:     a.inspectorOpen(a.lastWidth),
			dock:          a.dockOpen(a.lastWidth, a.lastHeight),
			railMode:      paneModeName(a.rail),
			inspectorMode: paneModeName(a.inspector),
		}
	}
	first := state{}
	mygoRunOnMain(func() { first = current() })
	logf(scenario, "before the clicks: page %s, rail %v (%s), inspector %v (%s), dock %v",
		first.page, first.rail, first.railMode, first.inspector, first.inspectorMode, first.dock)

	pageMoves, paneMoves := 0, 0
	last := first
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		now := state{}
		mygoRunOnMain(func() { now = current() })
		if now.page != last.page {
			pageMoves++
			logf(scenario, "the page became %s", now.page)
		}
		if now.inspector != last.inspector {
			paneMoves++
			logf(scenario, "the inspector became %v", now.inspector)
		}
		last = now
		if pageMoves > 0 && paneMoves > 0 {
			logf(scenario, "both kinds of click landed; stopping the watch early")
			break
		}
	}

	check(pageMoves > 0, "a click on a page button changed the page (%d change(s))", pageMoves)
	check(paneMoves > 0, "a click on the inspector button toggled the inspector (%d change(s))", paneMoves)
}

// captureErr takes the screenshot without the log line capture writes.
func captureErr(window *mygo.Window, path string) error {
	var err error
	mygoRunOnMain(func() { err = capture(window, path) })
	return err
}

// parseFrame reads "x,y,width,height" as a window frame. It is a check's
// starting point, not a setting: nothing writes it back to the file.
func parseFrame(s string) (mygo.Rectangle, bool) {
	var x, y, w, h int
	if _, err := fmt.Sscanf(s, "%d,%d,%d,%d", &x, &y, &w, &h); err != nil {
		return mygo.Rectangle{}, false
	}
	if w < 320 || h < 240 {
		return mygo.Rectangle{}, false
	}
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}, true
}
