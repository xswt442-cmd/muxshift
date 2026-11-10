// P15.1: the workbench layout — a top bar, a collapsible rail, a native stage
// with its transport, a collapsible inspector, a timeline dock and a status
// bar that stays visible when the window gets small.
//
// The rules are simple and measured from the window size the frame reports:
// below 900px the rail folds away, below 1160px the inspector does, and below
// 660px of content height the dock does. The numbers are client area, which is
// a little smaller than the size a window is asked for. A person can override any of the three with
// its button, and that choice keeps; the automatic rule only applies while a
// pane is left on auto.
//
// Nothing here invents a control: Mixer and DVR are drawn disabled because
// this build has neither, and every disabled button says why in its tooltip.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	topBarHeight   = 56
	railWidth      = 188
	inspectorWidth = 292
	dockHeight     = 132
	statusHeight   = 30

	// The sizes below which a pane folds itself away. They are thresholds for
	// the whole window, not for the pane, because a pane cannot measure the
	// room it was given in this layout engine.
	inspectorAutoWidth = 1160
	railAutoWidth      = 900
	dockAutoHeight     = 660
)

// paneMode says whether a pane follows the window-size rule or a person's
// explicit choice. Auto is the zero value, so a state built before P15 keeps
// the automatic behaviour.
type paneMode int8

const (
	paneAuto paneMode = iota
	paneOpen
	paneClosed
)

// shown reports whether a pane is visible at this window size.
func (m paneMode) shown(autoOpen bool) bool {
	switch m {
	case paneOpen:
		return true
	case paneClosed:
		return false
	}
	return autoOpen
}

// toggled turns a press into the opposite of what is on screen.
func (m paneMode) toggled(open bool) paneMode {
	if open {
		return paneClosed
	}
	return paneOpen
}

// topBar is the global bar: what session is open, what it is doing, and the
// three layout switches. Open and Settings are drawn disabled because this
// build has no file picker and no settings page yet; their tooltips say so
// instead of leaving a button that does nothing.
func (a *appState) topBar(c *ui.Context, width, height float32) {
	a.frameWidth, a.frameHeight = width, height
	ui.Row(c).Height(topBarHeight).WidthPercent(100).Padding(spaceSm, spaceLg).
		Gap(spaceSm).AlignItems(ui.Center).Background(colorBgSoft).Children(func() {
		ui.Text(c, "MuxShift").FontSize(textTitle).TextColor(colorText).SingleLine()
		// Below the rail threshold the chips would wrap the bar, so only the
		// source count stays: it is the one number the stage needs.
		if width >= railAutoWidth {
			pill(c, a.sceneLabel(), colorAccent, true)
		}
		pill(c, fmt.Sprintf("%d source%s", len(a.items), plural(len(a.items))), colorTextDim, false)
		if a.lastErr != nil {
			pill(c, "error", colorBad, true)
		}
		ui.Box(c).Grow(1)
		if width >= 1100 {
			ui.Text(c, a.globalStatus()).FontSize(textSmall).TextColor(colorTextDim).
				FontFeatures("tnum")
			ui.Button(c.Key("top-open"), "Open…").Width(68).Height(28).
				Disabled(true).
				Tooltip("importing at runtime is not implemented; pass -media on the command line").
				Description("Open or import a source (not available in this build)")
			ui.Button(c.Key("top-settings"), "Settings").Width(80).Height(28).
				Disabled(true).
				Tooltip("the settings page arrives with P15.3; nothing is saved here yet").
				Description("Global settings (not available in this build)")
		}
		a.paneButton(c, paneRail, "Rail", a.rail.shown(width >= railAutoWidth))
		a.paneButton(c, paneInspector, "Inspect", a.inspector.shown(width >= inspectorAutoWidth))
		a.paneButton(c, paneDock, "Dock", a.dock.shown(height >= dockAutoHeight))
	})
}

// globalStatus is the one line the top bar keeps current: shared event time,
// whether the group plays, and how many sources are ready.
func (a *appState) globalStatus() string {
	ready := 0
	for _, it := range a.items {
		if it.player != nil && it.player.Ready() {
			ready++
		}
	}
	state := "paused"
	if a.sync.playhead.Playing() {
		state = "playing"
	}
	return fmt.Sprintf("%s · %s · %d/%d ready", formatEvent(a.sync.playhead.Event()), state, ready, len(a.items))
}

// paneButton draws a layout switch. It is a real command: pressing flips the
// pane now, and that choice keeps whatever the window does next.
func (a *appState) paneButton(c *ui.Context, which paneKind, label string, open bool) {
	text := label
	if open {
		text += " ✓"
	}
	if ui.Button(c.Key(fmt.Sprintf("pane-%d", int(which))), text).Width(100).Height(28).Clicked() {
		switch which {
		case paneRail:
			a.rail = a.rail.toggled(open)
		case paneInspector:
			a.inspector = a.inspector.toggled(open)
		default:
			a.dock = a.dock.toggled(open)
		}
	}
}

// paneKind names which pane a button controls; the three modes are separate
// fields because they fold independently.
type paneKind int

const (
	paneRail paneKind = iota
	paneInspector
	paneDock
)

// rail is the left navigation. The three real pages are buttons; Mixer and DVR
// are visible but disabled with a tooltip, because neither exists yet.
func (a *appState) railPane(c *ui.Context, width, height float32) {
	if !a.rail.shown(width >= railAutoWidth) {
		return
	}
	ui.Column(c).Width(railWidth).Gap(spaceXs).Padding(spaceSm).
		Background(colorBgSoft).Children(func() {
		ui.Text(c, "workspace").FontSize(textMicro).TextColor(colorDim)
		for w := workspace(0); w < workspaceCount; w++ {
			page := w
			button := ui.Button(c.Key(fmt.Sprintf("rail-%d", int(page))), page.title()).
				Width(railWidth - 2*spaceSm).Height(34)
			if page == a.workspace {
				button.Background(colorAccentSoft).TextColor(colorAccent)
			}
			if button.Clicked() {
				a.workspace = page
			}
		}
		ui.Button(c.Key("rail-mixer"), "Mixer").Width(railWidth - 2*spaceSm).Height(34).
			Disabled(true).
			Tooltip("a mixer needs per-track routing the player does not expose; this build does not fake one").TextColor(colorDim)
		ui.Button(c.Key("rail-dvr"), "DVR").Width(railWidth - 2*spaceSm).Height(34).
			Disabled(true).
			Tooltip("the replay buffer is read-only until history playback is verified").TextColor(colorDim)
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("rail-fold"), "Hide rail").Width(railWidth - 2*spaceSm).Height(30).Clicked() {
			a.rail = paneClosed
		}
	})
}

// center is the stage page: the native picture, the transport under it, and
// the P13.3 page only when a scenario asked for one.
func (a *appState) center(c *ui.Context, width, height float32) {
	available := width - 2*spaceMd
	if a.rail.shown(width >= railAutoWidth) {
		available -= railWidth + spaceMd
	}
	if a.inspector.shown(width >= inspectorAutoWidth) {
		available -= inspectorWidth + spaceMd
	}
	// The picture keeps 16:9 before anything else: cap its height at what the
	// width allows instead of letting the stage fill a tall narrow column.
	pictureCap := available * 9 / 16
	if pictureCap < 200 {
		pictureCap = 200
	}
	ui.Box(c).WidthPercent(100).Grow(1).MaxHeight(pictureCap).Children(func() {
		a.stage(c)
	})
	a.transportRow(c)
	if a.webView != nil {
		ui.Column(c).Height(300).Children(func() {
			ui.WebView(c, a.webView).Grow(1)
		})
	}
}

// inspector is the right pane. Without a selection it shows the session, which
// is true today; P15.2 adds selecting a source and shows its properties here.
func (a *appState) inspectorPane(c *ui.Context, width, height float32) {
	if !a.inspector.shown(width >= inspectorAutoWidth) {
		return
	}
	ui.Column(c).Width(inspectorWidth).Gap(spaceSm).Padding(spaceMd).
		Background(colorBgSoft).Children(func() {
		ui.Text(c, "inspector").FontSize(textMicro).TextColor(colorDim)
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
				if it, index, ok := a.selectedSource(); ok {
					a.inspectorSource(c, it, index)
				} else {
					a.inspectorSession(c)
				}
			})
		})
	})
}

// inspectorSource follows the selected source: its Core identity, the player
// behind it, and the actions too detailed for a row in the Sources list.
func (a *appState) inspectorSource(c *ui.Context, it *item, index int) {
	snapshot := a.snapshot()
	var entry sourceSnapshot
	for _, source := range snapshot.Sources {
		if source.ID == it.source.ID {
			entry = source
			break
		}
	}
	ui.Row(c).WidthPercent(100).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("source %d", index+1)).FontSize(textMicro).TextColor(colorAccent)
		if ui.Button(c.Key("inspector-clear"), "clear").Width(64).Height(22).Clicked() {
			a.selected = ""
		}
	})
	a.detailRow(c, "address", shortURI(it.uri))
	a.detailRow(c, "id", string(it.source.ID))
	a.detailRow(c, "video", string(it.video))
	a.detailRow(c, "audio", string(it.audio))
	a.detailRow(c, "generation", itoa(entry.Generation))
	a.detailRow(c, "player", fmt.Sprintf("%v", entry.Player))
	a.detailRow(c, "ready", fmt.Sprintf("%v", entry.Ready))
	a.detailRow(c, "playing", fmt.Sprintf("%v", entry.Playing))
	a.detailRow(c, "position", formatEvent(entry.Position))
	a.detailRow(c, "duration", formatEvent(entry.Duration))
	a.detailRow(c, "volume", fmt.Sprintf("%.2f", entry.Volume))
	a.detailRow(c, "muted", fmt.Sprintf("%v", entry.Muted))
	if it.audio != "" {
		a.detailRow(c, "offset", fmt.Sprintf("%+.1fs", entry.Offset.Seconds()))
		a.detailRow(c, "aligned", fmt.Sprintf("%v", entry.Aligned))
	}
	if entry.OpenErr != "" && entry.OpenErr != "nil" {
		a.detailRow(c, "error", entry.OpenErr)
	}
	ui.Row(c).Wrap().Gap(spaceXs).Padding(spaceSm, 0).Children(func() {
		if it.video != "" && ui.Button(c.Key("inspector-show"), "Show").Width(72).Height(26).Clicked() {
			a.report(a.runCommand(command{Kind: commandShow, Source: it.source.ID}))
		}
		if it.audio != "" {
			if ui.Button(c.Key("inspector-hear"), "Hear").Width(72).Height(26).Clicked() {
				a.report(a.runCommand(command{Kind: commandHear, Source: it.source.ID}))
			}
			if ui.Button(c.Key("inspector-allow"), shortAllowLabel(a, it)).Width(84).Height(26).Clicked() {
				a.report(a.runCommand(command{Kind: commandAllow, Source: it.source.ID}))
			}
			if ui.Button(c.Key("inspector-vol-down"), "Vol -").Width(72).Height(26).Clicked() {
				a.report(a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: max(0, it.volume-0.25)}))
			}
			if ui.Button(c.Key("inspector-vol-up"), "Vol +").Width(72).Height(26).Clicked() {
				a.report(a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: min(1, it.volume+0.25)}))
			}
		}
		if it.video != "" || it.audio != "" {
			if ui.Button(c.Key("inspector-back"), "-0.1s").Width(64).Height(26).Clicked() {
				a.report(a.runCommand(command{Kind: commandNudge, Source: it.source.ID, Delta: -100 * time.Millisecond}))
			}
			if ui.Button(c.Key("inspector-fwd"), "+0.1s").Width(64).Height(26).Clicked() {
				a.report(a.runCommand(command{Kind: commandNudge, Source: it.source.ID, Delta: 100 * time.Millisecond}))
			}
			if index > 0 && ui.Button(c.Key("inspector-probe"), "Try align").Width(92).Height(26).Clicked() {
				a.probeAsync(0, index)
			}
		}
		if entry.OpenErr != "" && entry.OpenErr != "nil" && ui.Button(c.Key("inspector-retry"), "Retry").Width(72).Height(26).Clicked() {
			a.report(a.runCommand(command{Kind: commandRetry, Source: it.source.ID}))
		}
		if ui.Button(c.Key("inspector-remove"), "Remove").Width(84).Height(26).Clicked() {
			a.report(a.runCommand(command{Kind: commandRemove, Source: it.source.ID}))
		}
	})
	ui.Text(c, "no waveform is drawn: this build does not decode audio samples, so volume and mute come from the player itself.").
		FontSize(textMicro).TextColor(colorDim).Padding(spaceSm, 0)
}

// inspectorSession is the read-only session summary.
func (a *appState) inspectorSession(c *ui.Context) {
	snapshot := a.snapshot()
	a.detailRow(c, "session", snapshot.Name)
	a.detailRow(c, "scene", string(snapshot.Scene))
	a.detailRow(c, "picture", pictureLine(snapshot.Picture))
	a.detailRow(c, "sound", soundLine(snapshot.Sound))
	a.detailRow(c, "sources", fmt.Sprintf("%d", len(snapshot.Sources)))
	a.detailRow(c, "event", formatEvent(snapshot.Event))
	a.detailRow(c, "playing", fmt.Sprintf("%v", snapshot.Playing))
	a.detailRow(c, "group", snapshot.Group)
	if snapshot.SeekWindow != "" && snapshot.SeekWindow != "unknown" {
		a.detailRow(c, "seek", snapshot.SeekWindow)
	}
	ui.Text(c, "click a source in the Sources page to follow it here").
		FontSize(textMicro).TextColor(colorDim).Padding(spaceSm, 0)
}

// shortAllowLabel names the audio-hold action in the narrow inspector.
func shortAllowLabel(a *appState, it *item) string {
	if it.audio == "" {
		return "No audio"
	}
	if a.audible[it.audio] {
		return "Mute"
	}
	return "Allow"
}

// detailRow is one "name  value" line in the inspector.
func (a *appState) detailRow(c *ui.Context, name, value string) {
	if value == "" {
		return
	}
	ui.Row(c).WidthPercent(100).Gap(spaceSm).Children(func() {
		ui.Text(c, name).FontSize(textMicro).TextColor(colorDim).Width(64)
		ui.Text(c, value).FontSize(textSmall).TextColor(colorText).Grow(1).FontFeatures("tnum")
	})
}

// dockPane is the timeline dock. It is read-only in this build: it shows the
// shared event time and each source's offset. It never draws a clickable
// buffer that it cannot honour.
func (a *appState) dockPane(c *ui.Context) {
	ui.Column(c).Height(dockHeight).WidthPercent(100).Padding(spaceSm, spaceLg).
		Gap(spaceXs).Background(colorBgSoft).Children(func() {
		ui.Row(c).WidthPercent(100).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "timeline · event time").FontSize(textMicro).TextColor(colorDim)
			ui.Text(c, formatEvent(a.sync.playhead.Event())).FontSize(textSmall).
				TextColor(colorText).FontFeatures("tnum")
			ui.Box(c).Grow(1)
			if ui.Button(c.Key("dock-fold"), "Hide dock").Width(104).Height(24).Clicked() {
				a.dock = paneClosed
			}
		})
		for i, it := range a.items {
			offset := time.Duration(0)
			if shift, ok := a.sync.timeline.Shift(it.video); ok {
				offset = shift.Offset
			} else if it.audio != "" {
				if shift, ok := a.sync.timeline.Shift(it.audio); ok {
					offset = shift.Offset
				}
			}
			ui.Text(c, fmt.Sprintf("%d %s · offset %+.1fs · %s", i+1, shortURI(it.uri),
				offset.Seconds(), dockWindow(it))).FontSize(textMicro).
				TextColor(colorTextDim).FontFeatures("tnum")
		}
		if len(a.items) == 0 {
			ui.Text(c, "no sources").FontSize(textMicro).TextColor(colorDim)
		}
		ui.Text(c, "read-only: seeking and buffer editing arrive with the DVR work; nudges live in the inspector").
			FontSize(textMicro).TextColor(colorDim)
	})
}

// dockWindow reports the known window a source reports, or says that it has
// none. It never guesses a range.
func dockWindow(it *item) string {
	if it.player == nil {
		return "no player"
	}
	dur := it.player.Duration()
	if dur <= 0 {
		return "live · window unknown"
	}
	return "0:00–" + clock(dur)
}

// statusBar is the always-visible bottom line, separate from the panes so a
// small window still shows errors and positions.
func (a *appState) statusBar(c *ui.Context) {
	ui.Row(c).Height(statusHeight).WidthPercent(100).Padding(spaceXs, spaceLg).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.status()).FontSize(textSmall).TextColor(colorTextDim).
			FontFeatures("tnum").SingleLine()
	})
}

// plural is the suffix a count needs.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// formatEvent writes a shared event time as seconds with one decimal, the way
// the seek reports do.
func formatEvent(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Round(100*time.Millisecond).Seconds())
}

// layoutLine describes which panes the last frame drew, so a scenario's log can
// be tied to its capture.
func (a *appState) layoutLine(width, height float32) string {
	return fmt.Sprintf("layout %.0fx%.0f rail=%t inspector=%t dock=%t",
		width, height,
		a.rail.shown(width >= railAutoWidth),
		a.inspector.shown(width >= inspectorAutoWidth),
		a.dock.shown(height >= dockAutoHeight))
}
