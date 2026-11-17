package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// fakeWindow stands in for the window in the state-machine tests. The
// transitions never look at pixels, so a few fields are enough to prove what
// they asked the window to do.
type fakeWindow struct {
	bounds     mygo.Rectangle
	maximized  bool
	fullscreen bool
	onTop      bool

	boundsSet     int
	maximize      int
	unmaximize    int
	fullscreenSet []bool
	onTopSet      []bool
}

func (f *fakeWindow) Bounds() mygo.Rectangle { return f.bounds }
func (f *fakeWindow) SetBounds(r mygo.Rectangle) {
	f.bounds = r
	f.boundsSet++
}
func (f *fakeWindow) IsMaximized() bool { return f.maximized }
func (f *fakeWindow) Maximize()         { f.maximized = true; f.maximize++ }
func (f *fakeWindow) Unmaximize()       { f.maximized = false; f.unmaximize++ }
func (f *fakeWindow) SetFullScreen(v bool) {
	f.fullscreen = v
	f.fullscreenSet = append(f.fullscreenSet, v)
}
func (f *fakeWindow) IsFullScreen() bool { return f.fullscreen }
func (f *fakeWindow) SetAlwaysOnTop(v bool) {
	f.onTop = v
	f.onTopSet = append(f.onTopSet, v)
}

func newViewingAppState() *appState {
	return &appState{
		workspace: workspaceStage,
		rail:      paneAuto,
		inspector: paneAuto,
		dock:      paneAuto,
	}
}

// TestCinemaKeepsTheWorkbenchAndComesBack is W01 at the state level: entering
// cinema changes no window geometry and leaving restores the editing state the
// person left, including the page and the pane switches.
func TestCinemaKeepsTheWorkbenchAndComesBack(t *testing.T) {
	a := newViewingAppState()
	a.workspace = workspaceSources
	a.rail = paneOpen
	a.inspector = paneClosed
	workbench := mygo.Rectangle{X: 100, Y: 80, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCinema, w)
	if a.viewing != viewCinema {
		t.Fatalf("viewing = %v, want cinema", a.viewing)
	}
	if f := w.fullscreenSet; len(f) != 0 {
		t.Fatalf("cinema touched full screen: %v", f)
	}
	if w.boundsSet != 0 {
		t.Fatalf("cinema moved the window %d time(s)", w.boundsSet)
	}
	if !a.savedForViewing.saved || a.savedForViewing.bounds != workbench {
		t.Fatalf("saved = %+v, want the workbench rectangle", a.savedForViewing)
	}

	a.leaveViewing(w)
	if a.viewing != viewWorkbench {
		t.Fatalf("viewing = %v after leaving, want workbench", a.viewing)
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v after leaving, want %+v", w.bounds, workbench)
	}
	if a.workspace != workspaceSources || a.rail != paneOpen || a.inspector != paneClosed {
		t.Fatalf("editing state not restored: page=%v rail=%v inspector=%v",
			a.workspace, a.rail, a.inspector)
	}
}

// TestEnteringAnotherWatchingModeKeepsTheWorkbench is the reason the save is
// guarded: cinema -> full screen -> back must return to the workbench, not to
// the geometry full screen happened to have.
func TestEnteringAnotherWatchingModeKeepsTheWorkbench(t *testing.T) {
	a := newViewingAppState()
	workbench := mygo.Rectangle{X: 20, Y: 20, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCinema, w)
	w.bounds = mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080} // someone moved it
	a.enterViewing(viewFullscreen, w)
	if !w.fullscreen {
		t.Fatal("full screen was not asked for")
	}
	if a.savedForViewing.bounds != workbench {
		t.Fatalf("saved bounds = %+v, want the workbench %+v", a.savedForViewing.bounds, workbench)
	}

	a.leaveViewing(w)
	if w.fullscreen {
		t.Fatal("leaving full screen left the window in full screen")
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v after leaving, want %+v", w.bounds, workbench)
	}
}

// TestFullscreenComesBackToTheModeItCameFrom is W04: a full screen entered
// from cinema returns to cinema, not to the workbench, and the workbench
// rectangle saved before cinema is still the one restored at the end.
func TestFullscreenComesBackToTheModeItCameFrom(t *testing.T) {
	a := newViewingAppState()
	workbench := mygo.Rectangle{X: 30, Y: 40, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCinema, w)
	a.toggleFullscreen(w)
	if a.viewing != viewFullscreen || !w.fullscreen {
		t.Fatalf("viewing = %v fullscreen = %v, want a full screen", a.viewing, w.fullscreen)
	}
	a.toggleFullscreen(w)
	if a.viewing != viewCinema {
		t.Fatalf("viewing = %v after leaving full screen, want cinema", a.viewing)
	}
	if w.fullscreen {
		t.Fatal("leaving full screen left the window in full screen")
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v, want the cinema %+v", w.bounds, workbench)
	}

	a.leaveViewing(w)
	if a.viewing != viewWorkbench || w.bounds != workbench {
		t.Fatalf("viewing = %v bounds = %+v, want the workbench %+v", a.viewing, w.bounds, workbench)
	}
}

// TestFullscreenFromTheWorkbenchEndsTheWatchingMode: entered from the
// workbench, the way back is the workbench, with its panels.
func TestFullscreenFromTheWorkbenchEndsTheWatchingMode(t *testing.T) {
	a := newViewingAppState()
	a.workspace = workspaceMixer
	w := &fakeWindow{bounds: mygo.Rectangle{X: 1, Y: 2, Width: 900, Height: 700}}

	a.toggleFullscreen(w)
	a.toggleFullscreen(w)
	if a.viewing != viewWorkbench {
		t.Fatalf("viewing = %v, want workbench", a.viewing)
	}
	if a.workspace != workspaceMixer {
		t.Fatalf("workspace = %v, want the page the person was on", a.workspace)
	}
}

// TestCompactFromFullscreenKeepsTheSmallWindow: leaving a full screen that was
// entered from compact must give the small window back, not a maximised one.
func TestCompactFromFullscreenKeepsTheSmallWindow(t *testing.T) {
	a := newViewingAppState()
	w := &fakeWindow{bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800}}

	a.enterViewing(viewCompact, w)
	compact := w.bounds
	a.toggleFullscreen(w)
	a.toggleFullscreen(w)
	if a.viewing != viewCompact {
		t.Fatalf("viewing = %v, want compact", a.viewing)
	}
	if w.bounds != compact {
		t.Fatalf("bounds = %+v, want the compact %+v", w.bounds, compact)
	}
}

// TestCompactShrinksTheWindowAndRestoresIt covers W03: compact opens a smaller
// window, offers on-top, and undoes both when it ends.
func TestCompactShrinksTheWindowAndRestoresIt(t *testing.T) {
	a := newViewingAppState()
	workbench := mygo.Rectangle{X: 200, Y: 120, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCompact, w)
	if w.bounds.Width >= workbench.Width || w.bounds.Height >= workbench.Height {
		t.Fatalf("compact bounds = %+v, want smaller than %+v", w.bounds, workbench)
	}
	if w.bounds.Width < 420 || w.bounds.Height < 236 {
		t.Fatalf("compact bounds = %+v, below the readable floor", w.bounds)
	}

	a.toggleOnTop(w)
	if !w.onTop || !a.onTop {
		t.Fatal("on-top did not switch on")
	}
	a.leaveViewing(w)
	if w.onTop {
		t.Fatal("leaving compact left the window on top")
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v after leaving, want %+v", w.bounds, workbench)
	}
}

// TestCompactFromMaximizedRestoresMaximized is the geometry trap: setting a
// rectangle on a maximized window does nothing, so entering has to unmaximize
// first and leaving has to maximize again rather than restore a rectangle the
// maximized window never really had.
func TestCompactFromMaximizedRestoresMaximized(t *testing.T) {
	a := newViewingAppState()
	w := &fakeWindow{bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}, maximized: true}

	a.enterViewing(viewCompact, w)
	if w.maximized {
		t.Fatal("compact stayed maximized, so its small bounds could not take effect")
	}
	if w.bounds.Width >= 1920 {
		t.Fatalf("compact bounds = %+v, want a small window", w.bounds)
	}

	a.leaveViewing(w)
	if !w.maximized {
		t.Fatal("leaving did not restore the maximized window")
	}
}

// TestOverlayHidesAfterIdleAndStaysUnderThePointer is W05's rule in the small:
// a pointer move wakes the overlay, a still pointer lets it go, and a pointer
// resting on the bar itself keeps it.
func TestOverlayHidesAfterIdleAndStaysUnderThePointer(t *testing.T) {
	a := newViewingAppState()
	a.viewing = viewCinema

	if !a.notePointer(10, 10) {
		t.Fatal("the first pointer position was not treated as a move")
	}
	if !a.controlsShown {
		t.Fatal("the overlay did not show on a pointer move")
	}
	tick := a.idleTick
	if a.notePointer(11, 11) {
		t.Fatal("a one-pixel wobble counted as a move")
	}
	if a.idleTick != tick {
		t.Fatal("a still pointer armed another hide timer")
	}

	a.hideControls(tick - 1)
	if !a.controlsShown {
		t.Fatal("an older timer hid the overlay")
	}
	a.controlsHovered = false
	a.hideControls(tick)
	if a.controlsShown {
		t.Fatal("the overlay stayed after idle")
	}

	a.controlsShown = true
	a.controlsHovered = true
	a.hideControls(a.idleTick)
	if !a.controlsShown {
		t.Fatal("the overlay hid itself under the pointer")
	}
}

// TestLeavingViewingRestoresTheOnTopState proves the switch never outlives the
// mode that offered it: a window that was on top before compact stays on top,
// and one that was not goes back to not being on top.
func TestLeavingViewingRestoresTheOnTopState(t *testing.T) {
	for _, before := range []bool{false, true} {
		a := newViewingAppState()
		a.onTop = before
		w := &fakeWindow{bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1150, Height: 660}, onTop: before}

		a.enterViewing(viewCompact, w)
		a.toggleOnTop(w)
		a.leaveViewing(w)
		if w.onTop != before {
			t.Fatalf("onTop before=%v, after leaving=%v", before, w.onTop)
		}

		// The saved rectangle must survive a long stay in cinema, which is what
		// the states look like after several mode changes.
		a = newViewingAppState()
		w = &fakeWindow{bounds: mygo.Rectangle{X: 7, Y: 8, Width: 900, Height: 600}}
		a.enterViewing(viewCinema, w)
		time.Sleep(time.Millisecond)
		a.enterViewing(viewCompact, w)
		a.enterViewing(viewFullscreen, w)
		a.leaveViewing(w)
		if w.bounds != (mygo.Rectangle{X: 7, Y: 8, Width: 900, Height: 600}) {
			t.Fatalf("bounds = %+v after three mode changes", w.bounds)
		}
	}
}

// TestCompactSizeStaysSmall keeps the "small window" small whatever it was
// asked to shrink from: two fifths of a maximized screen is not compact.
func TestCompactSizeStaysSmall(t *testing.T) {
	for _, saved := range []mygo.Rectangle{
		{X: 0, Y: 0, Width: 2560, Height: 1540},
		{X: 60, Y: 60, Width: 1150, Height: 660},
		{X: 0, Y: 0, Width: 300, Height: 200},
	} {
		got := compactSize(saved)
		if got.Width < 420 || got.Width > 720 {
			t.Errorf("from %dx%d the compact width is %d, want 420..720", saved.Width, saved.Height, got.Width)
		}
		if got.Height < 236 || got.Height > 460 {
			t.Errorf("from %dx%d the compact height is %d, want 236..460", saved.Width, saved.Height, got.Height)
		}
		if got.X != saved.X+40 || got.Y != saved.Y+40 {
			t.Errorf("from %d,%d the compact window starts at %d,%d", saved.X, saved.Y, got.X, got.Y)
		}
	}
}
