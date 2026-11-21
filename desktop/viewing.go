// P10: the four viewing states. A person edits in the workbench and watches in
// cinema, full screen or a small compact window. The states are orthogonal to
// the scene layout and to the pages: nothing here changes which source the
// scene shows, which sounds it plays, or how the players are timed.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

type viewingMode int8

const (
	viewWorkbench viewingMode = iota
	viewCinema
	viewFullscreen
	viewCompact
)

// viewingModes is the order the page reports, so a log line reads the same way
// twice.
var viewingModes = []viewingMode{viewWorkbench, viewCinema, viewFullscreen, viewCompact}

func (m viewingMode) String() string {
	switch m {
	case viewCinema:
		return "cinema"
	case viewFullscreen:
		return "fullscreen"
	case viewCompact:
		return "compact"
	default:
		return "workbench"
	}
}

// editing says whether the mode lays the workbench out. Only the workbench does:
// the other three are for watching, so their carriers are a picture and a small
// overlay instead of a page.
func (m viewingMode) editing() bool { return m == viewWorkbench }

// viewingWindow is the part of the window the state machine touches. It is an
// interface so the transitions can be tested without opening a window; mygo's
// *Window already satisfies it.
type viewingWindow interface {
	Bounds() mygo.Rectangle
	SetBounds(mygo.Rectangle)
	IsMaximized() bool
	Maximize()
	Unmaximize()
	SetFullScreen(bool)
	IsFullScreen() bool
	SetAlwaysOnTop(bool)
}

// viewingSaved is everything a viewing mode overwrites, remembered so leaving
// puts the window back where the person left it. The bool says a save exists;
// entering from another viewing mode keeps the original one.
type viewingSaved struct {
	saved      bool
	bounds     mygo.Rectangle
	maximized  bool
	fullscreen bool
	onTop      bool
	workspace  workspace
	rail       paneMode
	inspector  paneMode
	dock       paneMode
}

// compactSize is the window size compact mode opens at, in DIP. It is a
// fraction of the workbench, with a floor: a smaller window would letterbox a
// 16:9 picture into a stripe.
func compactSize(saved mygo.Rectangle) mygo.Rectangle {
	// Two fifths of the workbench, within limits: a maximized window would
	// otherwise give a "small" window a metre across, and a nearly square one
	// would give a strip too small to hold the overlay.
	width := clampInt(saved.Width*2/5, 420, 720)
	height := clampInt(saved.Height*2/5, 236, 460)
	return mygo.Rectangle{X: saved.X + 40, Y: saved.Y + 40, Width: width, Height: height}
}

// enterViewing switches to a watching mode, saving the workbench state the first
// time. Entering the mode already in use does nothing, so a repeated keypress or
// double click cannot save the viewing geometry as if it were the workbench.
func (a *appState) enterViewing(mode viewingMode, w viewingWindow) {
	if mode == viewWorkbench || a.viewing == mode {
		return
	}
	if a.viewing == viewWorkbench {
		a.savedForViewing = viewingSaved{
			saved:      true,
			bounds:     w.Bounds(),
			maximized:  w.IsMaximized(),
			fullscreen: w.IsFullScreen(),
			onTop:      a.onTop,
			workspace:  a.workspace,
			rail:       a.rail,
			inspector:  a.inspector,
			dock:       a.dock,
		}
	}
	if mode == viewFullscreen && a.viewing != viewFullscreen {
		a.viewingReturn = a.viewing
	}
	a.viewing = mode
	// Entering a mode shows the overlay once, then it must fade on its own.
	// Setting the flag without arming the timer leaves the bar on screen
	// forever for anyone who clicks a button and then stops moving the mouse.
	a.showControls()
	switch mode {
	case viewFullscreen:
		w.SetFullScreen(true)
	case viewCompact:
		if w.IsFullScreen() {
			w.SetFullScreen(false)
		}
		if w.IsMaximized() {
			w.Unmaximize()
		}
		w.SetBounds(compactSize(a.savedForViewing.bounds))
	}
	a.invalidate()
}

// leaveViewing returns to the workbench and undoes everything a viewing mode
// did to the window. It is safe to call from the workbench: nothing is restored
// that was never saved.
func (a *appState) leaveViewing(w viewingWindow) {
	if !a.savedForViewing.saved {
		a.viewing = viewWorkbench
		return
	}
	saved := a.savedForViewing
	a.viewing = viewWorkbench
	if w.IsFullScreen() {
		w.SetFullScreen(false)
	}
	w.SetAlwaysOnTop(saved.onTop)
	a.onTop = saved.onTop
	if saved.maximized {
		// The window was maximized when the person started watching, so the
		// way back is the maximize state, not the rectangle it happened to
		// report while maximized.
		w.Maximize()
	} else {
		w.SetBounds(saved.bounds)
	}
	a.workspace = saved.workspace
	a.rail = saved.rail
	a.inspector = saved.inspector
	a.dock = saved.dock
	a.savedForViewing = viewingSaved{}
	a.invalidate()
}

// toggleFullscreen is the F11 key and the double click: from any watching mode
// it goes to full screen, and from full screen it goes back to the mode the
// person was watching in (cinema, or the workbench).
// toggleFullscreen goes full screen from wherever the viewer is, and comes
// back to that same mode. The spec asks for "the previous non-full-screen
// mode, geometry and panels", so Esc in a full screen entered from cinema
// must not dump the viewer into the workbench.
func (a *appState) toggleFullscreen(w viewingWindow) {
	if a.viewing == viewFullscreen {
		a.leaveFullscreen(w)
		return
	}
	a.enterViewing(viewFullscreen, w)
}

// leaveFullscreen returns to the mode that was showing before the full
// screen. From the workbench that is the way out of watching altogether.
func (a *appState) leaveFullscreen(w viewingWindow) {
	back := a.viewingReturn
	a.viewingReturn = viewWorkbench
	if back == viewWorkbench || back == viewFullscreen {
		a.leaveViewing(w)
		return
	}
	a.viewing = back
	if w.IsFullScreen() {
		w.SetFullScreen(false)
	}
	a.showControls()
	a.invalidate()
}

// toggleOnTop is compact mode's own switch: the small window is easy to lose
// behind the browser the stream came from. Leaving compact restores whatever
// the window had before, so the switch never outlives the mode that offered it.
func (a *appState) toggleOnTop(w viewingWindow) {
	a.onTop = !a.onTop
	w.SetAlwaysOnTop(a.onTop)
	a.invalidate()
}

// controlsIdle is how long the overlay stays after the pointer's last move.
const controlsIdle = 2500 * time.Millisecond

// notePointer records a pointer position and keeps the overlay awake when the
// pointer moved. It reports whether this was a move, so the caller can avoid
// scheduling a second hide timer.
func (a *appState) notePointer(x, y float32) bool {
	moved := !a.pointerKnown ||
		x < a.pointerX-2 || x > a.pointerX+2 ||
		y < a.pointerY-2 || y > a.pointerY+2
	a.pointerX, a.pointerY, a.pointerKnown = x, y, true
	if !moved {
		return false
	}
	a.showControls()
	return true
}

// showControls wakes the overlay and arms one hide timer. The token makes an
// older timer harmless when the pointer moved again in the meantime.
func (a *appState) showControls() {
	a.controlsShown = true
	a.controlsAt = time.Now()
	a.idleTick++
	tick := a.idleTick
	time.AfterFunc(controlsIdle, func() {
		mygoRunOnMain(func() {
			if a.viewing.editing() || a.idleTick != tick {
				return
			}
			// A pointer resting on the bar is not idle: hiding the button
			// under the cursor would be a trap, not a tidy screen.
			if a.controlsHovered || a.controlsFocused {
				time.AfterFunc(controlsIdle, func() {
					mygoRunOnMain(func() { a.hideControls(tick) })
				})
				return
			}
			a.hideControls(tick)
		})
	})
}

// hideControls takes the overlay away unless a newer wake-up exists.
func (a *appState) hideControls(tick uint64) {
	if a.idleTick != tick || a.viewing.editing() {
		return
	}
	if a.controlsHovered || a.controlsFocused {
		return
	}
	a.controlsShown = false
	a.invalidate()
}

// viewingView draws a watching mode: the picture filling the window, and the
// overlay of controls that the workbench would otherwise carry.
func (a *appState) viewingView(c *ui.Context) {
	width, height := c.Size()
	a.lastWidth, a.lastHeight = width, height
	outer := ui.Box(c).Fill().Background(colorBg)
	outer.Children(func() {
		ui.Box(c).Fill().Padding(a.space(0, 0)).Children(func() {
			a.stage(c)
		})
		// Cinema keeps the window frame and the title bar; it only takes the
		// workbench chrome away, which is why it draws nothing extra here.
		//
		// The status bar is part of that chrome, so a failure that arrives
		// while watching would otherwise be invisible until the person went
		// back to editing. The line stays up whatever the overlay's timer
		// decides: an error is not a control.
		if a.lastErr != nil {
			a.viewingError(c)
		}
		if a.controlsShown || a.controlsHovered || a.controlsFocused {
			bar := a.viewingControls(c, width, height)
			a.controlsHovered = bar.Hovered()
			// A keyboard user who tabbed onto a button owns the overlay just
			// as a pointer resting on it does: hiding the control that has
			// focus would take the focus ring away mid-use.
			a.controlsFocused = bar.FocusWithin()
		} else {
			a.controlsHovered, a.controlsFocused = false, false
		}
	})
	if x, y, inside := outer.PointerPosition(); inside {
		a.notePointer(x, y)
	}
	if outer.DoubleClicked() {
		a.toggleFullscreen(a.windowRef())
	}
}

// windowRef is the window the state machine drives. It is nil in tests that only
// exercise the view, which is why every transition above takes the interface.
func (a *appState) windowRef() viewingWindow {
	if a.window == nil {
		return nil
	}
	return a.window
}

// viewingError keeps the newest error readable while the workbench is away. It
// repeats what statusBar says, with the same dismiss, because a viewer who
// cannot see the failure cannot decide whether to keep watching.
func (a *appState) viewingError(c *ui.Context) {
	ui.Row(c).Absolute().Top(a.space(10, 10)).LeftPercent(2).RightPercent(2).
		Padding(a.space(8, 12)).Radius(radiusMd).
		Background(colorPanel2).AlignItems(ui.Center).Gap(spaceSm).
		Children(func() {
			ui.Icon(c, lucide.AlertTriangle()).Size(16, 16).TextColor(colorBad)
			ui.Text(c, a.errorShort()).FontSize(textSmall).
				TextColor(colorText).SingleLine().Grow(1)
			dismiss := ui.ButtonBase(c.Key("view-dismiss")).Height(24).
				Padding(0, spaceSm).Radius(radiusMd).AlignItems(ui.Center)
			fill, ink := ui.Transparent, colorTextDim
			if dismiss.Pressed() {
				fill, ink = colorPressed, colorText
			} else if dismiss.Hovered() {
				fill, ink = colorHover, colorText
			}
			dismiss = dismiss.Background(fill).TextColor(ink)
			dismiss.Children(func() {
				ui.Text(c, a.tr("Dismiss", "关闭")).FontSize(textSmall).FontWeight(500).
					TextColor(ink).SingleLine()
			})
			if dismiss.Tooltip(a.tr("hides the message; the log keeps it", "只隐藏这条消息，日志里仍然保留")).
				Clicked() {
				a.dismissError()
				a.showControls()
			}
		})
}

// viewingControls is the overlay: the few actions a viewer needs, laid on top of
// the picture instead of in a row that permanently costs height.
func (a *appState) viewingControls(c *ui.Context, width, height float32) ui.Element {
	// A row cannot centre itself, so a wide bar is placed at the middle and
	// pulled back by half its own width. A compact window is narrower than the
	// bar, so there the bar stretches between the edges and wraps instead of
	// pushing its last buttons out of sight.
	narrow := width < 700
	bar := ui.Row(c).Absolute().Bottom(a.space(18, 18)).
		Padding(a.space(6, 6), a.space(10, 14)).Gap(spaceSm).
		AlignItems(ui.Center).Radius(radiusLg).Border(1, colorLine).
		Background(colorPanel2).ZIndex(10)
	if narrow {
		bar = bar.LeftPercent(2).RightPercent(2).Wrap().Gap(a.space(4, 4))
	} else {
		bar = bar.LeftPercent(50).TranslatePercent(-50, 0)
	}
	bar.Children(func() {
		// Tab brought the bar back, so the first button takes the focus ring
		// that the timer promise keeps in place.
		first := a.focusControls
		a.focusControls = false
		playing := a.sync.playhead.Playing()
		label, buttonWidth := a.tr("Pause", "暂停"), float32(88)
		if !playing {
			label = a.tr("Play", "播放")
		}
		play := ui.Button(c.Key("view-play"), label).Width(buttonWidth).Height(a.controlHeight() - 2)
		if first {
			play = play.AutoFocus()
		}
		if play.Clicked() {
			kind := commandPlay
			if playing {
				kind = commandPause
			}
			a.report(a.runCommand(command{Kind: kind}))
			a.showControls()
		}
		if main := a.first(); main != nil && main.player != nil {
			muted := main.player.Muted()
			muteText := a.tr("Mute", "静音")
			if muted {
				muteText = a.tr("Unmute", "取消静音")
			}
			if ui.Button(c.Key("view-mute"), muteText).Width(88).
				Height(a.controlHeight() - 2).Clicked() {
				a.report(a.runCommand(command{Kind: commandMute, Source: main.source.ID}))
				a.showControls()
			}
		}
		// Group two: where the picture is. A rule and a mode word, so the
		// transport keys are not mixed in with the window switches.
		ui.Box(c).Width(1).Height(20).Background(colorLine).Padding(0, a.space(2, 6))
		if main := a.firstShown(); main != nil {
			if entry := a.entryOf(main); entry.Player {
				ui.Text(c, formatEvent(entry.Position)).FontSize(textSmall).
					TextColor(colorTextDim).FontFeatures("tnum")
			}
		}
		ui.Box(c).Width(1).Height(20).Background(colorLine).Padding(0, a.space(2, 6))
		if a.viewing == viewCompact {
			onTop := a.tr("On top", "置顶")
			if a.onTop {
				onTop = a.tr("Pinned", "已置顶")
			}
			if ui.Button(c.Key("view-ontop"), onTop).Width(96).
				Height(a.controlHeight() - 2).Clicked() {
				a.toggleOnTop(a.windowRef())
				a.showControls()
			}
		}
		if a.viewing == viewCinema || a.viewing == viewFullscreen {
			if ui.Button(c.Key("view-small"), a.tr("Small", "小窗")).Width(72).
				Height(a.controlHeight() - 2).
				Tooltip(a.tr("a small window that can stay on top", "一个可以置顶的小窗口")).
				Clicked() {
				a.enterViewing(viewCompact, a.windowRef())
			}
		}
		fullLabel := a.tr("Window full screen", "窗口全屏")
		backLabel := a.tr("Back to editing", "回到编辑")
		fullWidth, backWidth := float32(104), float32(124)
		if narrow {
			fullLabel, fullWidth = a.tr("Full", "全屏"), 64
			backLabel, backWidth = a.tr("Back", "返回"), 64
		}
		if ui.Button(c.Key("view-full"), fullLabel).Width(fullWidth).
			Height(a.controlHeight() - 2).
			Tooltip(a.tr("F11 or a double click does the same", "F11 或双击画面也可以")).
			Clicked() {
			a.toggleFullscreen(a.windowRef())
			a.showControls()
		}
		if ui.Button(c.Key("view-back"), backLabel).Width(backWidth).
			Height(a.controlHeight() - 2).
			Tooltip(a.tr("Esc returns to the workbench", "Esc 回到工作台")).
			Clicked() {
			a.leaveViewing(a.windowRef())
		}
	})
	return bar
}

// viewingShortcuts are the keys that work while a picture fills the window. The
// wheel and the buttons cover the rest; Esc always has a way back, so nobody is
// stuck in full screen.
func (a *appState) viewingShortcuts(c *ui.Context) {
	if c.FocusTakesText() {
		return
	}
	c.OnWindowShortcut(0, ui.KeyF11, func() { a.toggleFullscreen(a.windowRef()) })
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		// Esc leaves the full screen for the mode it came from; from any other
		// watching mode it is the way back to editing, and it never closes
		// the application.
		if a.viewing == viewFullscreen {
			a.leaveFullscreen(a.windowRef())
			return
		}
		a.leaveViewing(a.windowRef())
	})
	c.OnWindowShortcut(0, ui.KeySpace, func() {
		kind := commandPlay
		if a.sync.playhead.Playing() {
			kind = commandPause
		}
		a.report(a.runCommand(command{Kind: kind}))
		a.showControls()
	})
	// Tab is how a keyboard user reaches the buttons at all: the bar is not
	// drawn while hidden, so the first Tab has to bring it back and put the
	// focus on its first button.
	c.OnWindowShortcut(0, ui.KeyTab, func() {
		if !a.controlsShown {
			a.showControls()
			a.focusControls = true
		}
	})
	c.OnWindowShortcut(0, ui.KeyM, func() {
		it := a.firstShown()
		if it == nil || it.player == nil {
			return
		}
		a.report(a.runCommand(command{Kind: commandMute, Source: it.source.ID}))
		a.showControls()
	})
	c.OnWindowShortcut(0, ui.KeyUp, func() { a.stepVolume(0.05) })
	c.OnWindowShortcut(0, ui.KeyDown, func() { a.stepVolume(-0.05) })
}

// stepVolume nudges the volume of the picture being watched. The workbench has
// sliders for this; a watching mode has no room for them, so the arrow keys
// are the equivalent.
func (a *appState) stepVolume(delta float32) {
	it := a.firstShown()
	if it == nil || it.player == nil {
		return
	}
	level := it.volume + float64(delta)
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	a.report(a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: level}))
	a.showControls()
}

// firstShown is the source the watching keys act on: the first one the scene
// shows, because a watching mode draws whoever the scene shows.
func (a *appState) firstShown() *item {
	for _, it := range a.items {
		if it.shown && it.player != nil {
			return it
		}
	}
	return nil
}

// viewingLabel is the top bar's word for the current mode.
func (a *appState) viewingLabel() string {
	if a.viewing == viewWorkbench {
		return "watch"
	}
	return fmt.Sprintf("%s · back", a.viewing.String())
}
