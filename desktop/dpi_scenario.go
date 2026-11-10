// The display-scale walk: the P15 spec asks for a layout check at 100, 125,
// 150 and 200% scaling with the window's real pixels unchanged.
package main

import (
	"fmt"

	"github.com/egoist/mygo"
)

// runDPI walks the workbench through the display scales the P15 spec names. The
// host display runs at 125%, so 100, 150 and 200 come from ui's content zoom:
// SetZoom lays the view out in a window 1/z its size and draws it z times larger
// at the screen's resolution, which is what a larger display scale does to the
// same window pixels. The panes fold on DIP thresholds, so a scale that leaves
// the window with fewer DIPs has to fold the same panes a smaller window does.
func runDPI(window *mygo.Window, a *appState, dir string) {
	go dpiBody(window, a, dir)
}

func dpiBody(window *mygo.Window, a *appState, dir string) {
	scales := []struct {
		name string
		zoom float32
	}{
		{"100", 1},
		{"125", 1.25},
		{"150", 1.5},
		{"200", 2},
	}
	// The same window pixels at every scale: 1426×862 is the default client size
	// at 125% and the widest the host screen allows, and 900×600 is small enough
	// to fold the rail at 100%.
	sizes := []struct {
		name string
		w, h int
	}{
		{"full", 1426, 862},
		{"small", 900, 600},
	}
	for _, scale := range scales {
		for _, size := range sizes {
			step(window, a, dir, "dpi", fmt.Sprintf("%s-%s.png", scale.name, size.name),
				fmt.Sprintf("scale %s%% in a %dx%d window", scale.name, size.w, size.h),
				func(a *appState) {
					a.zoom = scale.zoom
					window.SetSize(size.w, size.h)
				})
			mygoRunOnMain(func() {
				logf("dpi", "scale %s%% at %dx%d: the content sees %.1fx%.1f DIPs (rail %v, inspector %v, dock %v)",
					scale.name, size.w, size.h, a.lastWidth, a.lastHeight,
					a.railOpen(a.lastWidth), a.inspectorOpen(a.lastWidth),
					a.dockOpen(a.lastWidth, a.lastHeight))
			})
		}
	}
	// Leave the window as the other scenarios find it.
	step(window, a, dir, "dpi", "restored.png", "back at 100%", func(a *appState) {
		a.zoom = 1
		window.SetSize(1426, 862)
	})
	logf("dpi", "done: %d sources", len(a.items))
	a.release()
	mygo.App.Quit()
}

// railOpen, inspectorOpen and dockOpen report the folds the workbench itself
// would draw, so a scale check says what the DIPs it measured should have folded
// instead of repeating the thresholds beside them.
func (a *appState) railOpen(width float32) bool { return a.rail.shown(width >= railAutoWidth) }

func (a *appState) inspectorOpen(width float32) bool {
	return a.inspector.shown(width >= inspectorAutoWidth)
}

func (a *appState) dockOpen(width, height float32) bool {
	return a.dock.shown(a.settings.DockOpen && height >= dockAutoHeight)
}
