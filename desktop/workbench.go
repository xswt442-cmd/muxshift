// The workbench is arranged the way a live mixer is: a scene and source list on
// the left, the picture in the middle, and the selected source's properties with
// the audio levels on the right. Pages (sources, diagnostics, settings, mixer)
// take the middle column instead of replacing the window, so the scene list and
// the picture stay where they are.
//
// The shell folds by width, not by a pile of toggles. At 900px and up all three
// columns are drawn; below that the scene list folds into a drawer and the
// properties open over the picture; below 747px even the drawers give way to the
// picture, a bottom bar and the page menu. Nothing here is hidden without a way
// to reach it.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/xswt442-cmd/muxshift/core"
)

const (
	// The three columns. 240 + 608 + 300 is the 1150-wide case in the review;
	// the middle column is whatever is left.
	sceneWidth      = 240
	propertiesWidth = 300
	mixerHeight     = 168
	mixerStripHi    = 34
	drawerWidth     = 300

	// Kept for the scale walk and the workspace memory: the folds they describe
	// no longer decide the layout, but a saved pane name must still parse.
	topBarHeight               = 48
	statusHeight               = 26
	dockHeight                 = 168
	inspectorWidth             = 300
	railWidth                  = 240
	railFoldWidth              = 64
	inspectorAutoWidth         = 1600
	railAutoWidth              = 1600
	dockAutoHeight             = 660
	brandAutoWidth             = 760
	statusQuietWidth   float32 = 1280
	compactButtonWidth float32 = 1200
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

// The shell's own sizes follow the type scale: a larger type gets taller rows
// and buttons, not text squeezed into the same box.
func (a *appState) barHeight() float32 {
	switch currentScale.name {
	case "compact":
		return 36
	case "large":
		return 48
	}
	return 40
}

func (a *appState) statusBarHeight() float32 {
	switch currentScale.name {
	case "compact":
		return 24
	case "large":
		return 28
	}
	return 26
}

// controlHeight is the height of an ordinary button or field.
func (a *appState) controlHeight() float32 {
	switch currentScale.name {
	case "compact":
		return 28
	case "large":
		return 36
	}
	return 32
}

// rowHeight is the height of one source row or one mixer channel.
func (a *appState) rowHeight() float32 {
	switch currentScale.name {
	case "compact":
		return 32
	case "large":
		return 40
	}
	return 36
}

// shell draws the whole window: bar, three columns, status, and whichever
// overlay is open.
func (a *appState) shell(c *ui.Context, width, height float32) {
	narrow := width < 900
	a.winW = width
	if !narrow {
		a.drawerOpen = false
	}
	// The source list lives in the console now. Anything that still asks for
	// the old page (a shortcut, a scenario, a settings file) lands on the stage
	// with that panel in front instead of a second copy of the list.
	if a.workspace == workspaceSources {
		delete(a.dockHidden, dockSources)
		a.dockUp = true
		a.workspace = workspaceStage
	}
	ui.Column(c).Fill().Background(colorBg).Children(func() {
		ui.Row(c).WidthPercent(100).Grow(1).AlignItems(ui.Stretch).Children(func() {
			// The scene column is a draggable rail the way a capture program
			// has one: it starts open, keeps its own width, and folds to icons.
			if !narrow && a.editingShell {
				layout.DraggableSideNav(c, &a.navWidth, &a.navUp, layout.DraggableSideNavProps{
					MinWidth: 64, MaxWidth: 360, NoHandle: true, Background: colorPanel,
					// Settings and Diagnostics belong under the scene list, and
					// the nav's own footer row is the place that reaches the
					// bottom: a plain Grow spacer inside the body does not.
					Footer: func(nav layout.SideNavState) { a.columnFooter(c) },
				}, func(nav layout.SideNavState) {
					a.sceneColumnBody(c, nav.Expanded)
				})
			}
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				if a.workspace == workspaceStage {
					a.preview(c, width, height)
					a.dockArea(c)
					return
				}
				// Another page keeps the picture mounted and hidden, so the
				// media keeps running and the stage comes back current.
				a.parkedStage(c)
				a.pageBody(c, width, height)
			})
			if narrow && a.drawerOpen {
				a.drawer(c, height)
			}
		})
		// In a narrow window the console already carries the mixer, so the
		// separate strip under it would be the same controls twice.
		a.statusBar(c, width)
		if a.errorOpen {
			a.errorReport(c, width, height)
		}
		if a.pageMenu {
			a.pageMenuPanel(c, width)
		}
		if a.sceneMenu {
			a.sceneMenuPanel(c, width)
		}
		if a.addOpen {
			a.addSourceDialog(c)
		}
	})
}

// pageBody draws a page in the middle column, with the padding the page's own
// rows expect.
func (a *appState) pageBody(c *ui.Context, width, height float32) {
	ui.Box(c).Fill().Padding(spaceMd).Children(func() {
		switch a.workspace {
		case workspaceDiagnostics:
			a.diagnosticsPage(c)
		case workspaceSettings:
			ui.Scroll(c).Fill().WidthPercent(100).Children(func() {
				a.settingsPage(c)
			})
		case workspaceMixer:
			a.mixerPage(c)
		}
	})
}

// topBar carries five things only: the name, the scene, what the session is
// doing, adding a source, and the way into watching and pages. Every other
// switch moved to the column it acts on.
func (a *appState) topBar(c *ui.Context, width float32) {
	a.frameWidth, a.frameHeight = width, a.lastHeight
	ui.Row(c).Height(a.barHeight()).WidthPercent(100).Padding(0, spaceLg).
		Gap(spaceSm).AlignItems(ui.Center).Background(colorBg).Children(func() {
		a.sceneChip(c)
		if width >= 900 {
			label := "‹"
			tip := a.tr("hide the scene and source list", "收起场景与来源列表")
			if a.leftCollapsed {
				label, tip = "›", a.tr("show the scene and source list", "展开场景与来源列表")
			}
			if ui.Button(c.Key("top-left"), label).Width(32).Height(a.controlHeight()).
				Tooltip(tip).Clicked() {
				a.leftCollapsed = !a.leftCollapsed
			}
		}
		ui.Box(c).Grow(1)
		if a.lastErr != nil {
			label := a.tr("Error", "错误")
			ui.Button(c.Key("top-error"), label).Height(24).Padding(0, spaceSm).
				Border(1, colorBad).TextColor(colorBad).
				Tooltip(a.tr("what failed, and what to do about it", "哪里失败了，以及该怎么做")).
				OnClick(func() { a.errorOpen = true })
		}
		if width >= 560 {
			primary := ui.Button(c.Key("top-add"), a.tr("+ Source", "＋ 来源")).
				Height(a.controlHeight()).Padding(0, spaceMd).
				Background(colorAccent).TextColor(colorAccentText)
			if primary.Clicked() {
				a.openAddSource()
			}
			if ui.Button(c.Key("top-settings"), a.tr("Settings", "设置")).Height(a.controlHeight()).Padding(0, spaceSm).
				Tooltip(a.tr("appearance, language and the other settings", "外观、语言与其他设置")).Clicked() {
				a.workspace = workspaceSettings
			}
		}
		editLabel := a.tr("Edit", "编辑")
		if a.editingShell {
			editLabel = a.tr("Watch", "观看")
		}
		// This button is a mode switch, so it shows which mode is on: filled
		// while the editing shell is mounted, plain while the stage has the
		// window to itself. The label names the mode it will switch to.
		editBackground := colorPanel
		editText := colorText
		if !a.editingShell {
			editText = colorDim
		}
		if ui.Button(c.Key("top-edit"), editLabel).Height(a.controlHeight()).Padding(0, spaceMd).
			Background(editBackground).TextColor(editText).Clicked() {
			a.editingShell = !a.editingShell
			if !a.editingShell {
				a.workspace = workspaceStage
			}
			a.invalidate()
		}
		if ui.Button(c.Key("top-pages"), a.tr("Pages", "页面")).Height(a.controlHeight()).Padding(0, spaceSm).Clicked() {
			a.pageMenu = !a.pageMenu
		}
	})
	ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
}

// sceneChip names the scene the window shows. This build has one scene, and the
// chip says so instead of pretending a menu of scenes exists.
// sceneLabelText is the core's scene name in the window's language. The name is
// a description, not data: "picture 1 · sound 1 at 0.30" reads as gibberish to a
// Chinese user, so the three words are translated while the numbers stay.
// sceneLabelText names the scene the way the sidebar lists it. The core name
// ("main", or "scene 2") describes the scene's audio, not the scene, and reads
// as gibberish in the sidebar; its position is what a person points at.
func (a *appState) sceneLabelText() string {
	for i, scene := range a.session.Scenes() {
		if scene.ID == a.scene {
			return fmt.Sprintf(a.tr("Scene %d", "场景 %d"), i+1)
		}
	}
	return a.tr("Scene", "场景")
}

func (a *appState) sceneChip(c *ui.Context) {
	chip := ui.Button(c.Key("scene-chip"), a.sceneLabelText()).Height(28).Padding(0, spaceSm).
		Tooltip(a.tr("the scene this window shows", "此窗口显示的场景"))
	if chip.Clicked() {
		a.sceneMenu = !a.sceneMenu
	}
}

// stripStatus is the one-line account of the session in the bar and the status
// bar: what is playing, the event time, and how many sources are ready.
func (a *appState) stripStatus() string {
	playing := a.tr("paused", "已暂停")
	if a.playing() {
		playing = a.tr("playing", "播放中")
	}
	ready := 0
	for _, it := range a.items {
		if it.player != nil && it.shown {
			ready++
		}
	}
	snap := a.snapshot()
	return fmt.Sprintf("%s · %s · %s", playing, formatEvent(snap.Event), itoa(len(a.items))+" "+a.tr("sources", "个来源"))
}

// sceneColumn is the left column: the scene list, the source list and the page
// shortcuts at the bottom.
// sourceDot is the colour of the state mark: red for a source that failed,
// pale for one that has no player yet, green once it is on the stage.
func (a *appState) sourceDot(it *item) ui.Color {
	switch {
	case it.openErr != nil:
		return colorBad
	case it.player == nil:
		return colorLine
	case it.shown:
		return colorGood
	}
	return colorDim
}

// sourceEntry is the only place a source is drawn. It comes in two densities —
// a row inside the scene column and a card on the Sources page — but the state
// mark, the name and the second line are the same code in both, so the two
// views cannot drift apart.
func (a *appState) sourceEntry(c *ui.Context, it *item, index int, compact bool) {
	active := a.selected == it.source.ID
	row := ui.Row(c).WidthPercent(100).Gap(spaceXs).AlignItems(ui.Center)
	if compact {
		row = row.Height(a.rowHeight()).Padding(0, 10).Radius(radiusMd).
			OnClick(func() { a.selected = it.source.ID })
		fill := colorBg
		if row.Pressed() {
			fill = colorPressed
		} else if active {
			fill = colorSelected
		} else if row.Hovered() {
			fill = colorHover
		}
		row = row.Background(fill).Border(1, colorLineSoft)
	} else {
		row = row.Wrap().Gap(spaceSm).Padding(spaceSm, spaceMd).Radius(radiusCard).
			Background(colorPanel).Border(1, colorLineSoft)
	}
	nameSize := textBody
	subSize := textMicro
	nameWeight := 500
	if compact && !active {
		nameWeight = 400
	}
	row.Children(func() {
		if !compact {
			ui.Text(c, itoa(index+1)).FontSize(textMicro).TextColor(colorDim).
				Width(16).FontFeatures("tnum")
		}
		ui.Column(c).Grow(1).Gap(0).Children(func() {
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(5).Height(5).Radius(2.5).Background(a.sourceDot(it))
				ui.Text(c, shortURI(it.uri)).FontSize(nameSize).FontWeight(nameWeight).
					TextColor(colorText).SingleLine()
				if !compact {
					a.sourceMarks(c, it)
				}
			})
			facts := ui.Text(c, a.sourceSub(it)).FontSize(subSize).FontWeight(400).
				TextColor(colorDim).SingleLine()
			if compact {
				facts = facts.PaddingStart(13)
			}
		})
		if compact {
			a.sourceEye(c, it, index)
			return
		}
		a.sourceActions(c, it, index)
	})
}

// sourceMarks says, in a card, what the source contributes to the scene.
func (a *appState) sourceMarks(c *ui.Context, it *item) {
	if it.openErr != nil {
		pill(c, a.tr("error", "错误"), colorBad, false)
		return
	}
	if it.video != "" {
		pill(c, "PICTURE", colorAccent, false)
	}
	if it.audio != "" {
		pill(c, "SOUND", colorSound, false)
	}
}

// sourceEye is the compact density's one control. It is a real switch now: the
// eye that is on takes the picture off the stage without closing the player,
// and the eye that is off puts it back.
func (a *appState) sourceEye(c *ui.Context, it *item, index int) {
	a.quietIconButton(c, "sc-eye-"+string(it.source.ID), a.eyeIcon(it), a.eyeTip(it),
		a.eyeTint(it), func() {
			a.selected = it.source.ID
			a.toggleVideo(index)
		})
}

// sourceActions is the card density's control row.
func (a *appState) sourceActions(c *ui.Context, it *item, index int) {
	ui.Row(c).Gap(spaceXs).Children(func() {
		show := a.tr("Hide", "隐藏")
		if !it.shown {
			show = a.tr("Show", "显示")
		}
		if ui.Button(c.Key("card-show-"+string(it.source.ID)), show).Height(28).Clicked() {
			a.toggleVideo(index)
		}
		if ui.Button(c.Key("card-hear-"+string(it.source.ID)), a.tr("Hear", "试听")).Height(28).Clicked() {
			a.selectAudio(it)
		}
		if it.audio != "" {
			if ui.Button(c.Key("card-allow-"+string(it.source.ID)), a.shortAllowLabel(it)).Height(28).Clicked() {
				a.runCommand(command{Kind: commandAllow, Source: it.source.ID})
			}
		}
		if it.openErr != nil {
			if ui.Button(c.Key("card-retry-"+string(it.source.ID)), a.tr("Retry", "重试")).Height(28).Clicked() {
				a.runCommand(command{Kind: commandRetry, Source: it.source.ID})
			}
		}
		if ui.Button(c.Key("card-remove-"+string(it.source.ID)), a.tr("Remove", "移除")).Height(28).Clicked() {
			a.runCommand(command{Kind: commandRemove, Source: it.source.ID})
		}
	})
}

// sceneColumnBody draws the left rail. Expanded it shows the scene, the source
// list and the way into settings; folded it becomes a strip of icons, because
// Chinese labels do not fit a 64 pixel rail (see the console prototype).
func (a *appState) sceneColumnBody(c *ui.Context, expanded bool) {
	ui.Column(c).Fill().WidthPercent(100).HeightPercent(100).AlignItems(ui.Stretch).
		Children(func() {
			if !expanded {
				ui.Column(c).WidthPercent(100).Padding(spaceXs, spaceSm).Gap(spaceXs).
					AlignItems(ui.Center).Children(func() {
					if ui.Button(c.Key("rail-add"), "＋").Width(28).Height(26).
						Tooltip(a.tr("add a source", "添加来源")).Clicked() {
						a.openAddSource()
					}
					for _, it := range a.items {
						if ui.Button(c.Key("rail-src-"+string(it.source.ID)), "▢").Width(28).Height(26).
							Tooltip(shortURI(it.uri)).Clicked() {
							a.selected = it.source.ID
							a.invalidate()
						}
						if it.shown {
							ui.Box(c).Width(6).Height(6).Radius(3).Background(colorAccent)
						}
					}
				})
				return
			}
			ui.Row(c).Padding(spaceSm, 18).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Scene", "场景")).FontSize(textCaption).FontWeight(600).
					TextColor(colorTextDim)
				ui.Box(c).Grow(1)
			})
			a.sceneList(c)
			ui.Box(c).Fill().Grow(1).MinHeight(0)
		})
}

// layoutRow is the five arrangements, kept in the scene column rather than
// floating over the picture: a strip of small buttons that says where the
// pictures go.
func (a *appState) layoutRow(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Padding(spaceSm, spaceMd).Gap(spaceXs).Children(func() {
		ui.Text(c, a.tr("Layout", "布局")).FontSize(textMicro).TextColor(colorDim)
		ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
			row := func(kinds []layoutKind) {
				ui.Row(c).WidthPercent(100).Gap(spaceXs).Children(func() {
					for _, kind := range kinds {
						on := a.layout == kind
						label := a.tr(kind.label(), map[string]string{
							"Focus": "聚焦", "Split": "分屏", "PiP": "小窗",
							"Grid": "网格", "Hero": "主次",
						}[kind.label()])
						b := ui.Button(c.Key("layout-"+kind.String()), label).Height(24).Grow(1)
						if on {
							b = b.Background(colorAccentSoft).TextColor(colorAccent)
						}
						if b.Clicked() {
							a.setLayout(kind)
						}
					}
				})
			}
			row(layoutKinds[0:3])
			row(layoutKinds[3:])
		})
	})
}

// sceneRow draws the one scene. It is a row, not a button, because there is
// nothing to switch to yet.
// sceneList draws every scene as a row: the current one carries the accent
// dot, a press switches to it, and the cross removes it when more than one is
// left. A scene is where a different arrangement of the same sources lives, so
// the list is the only place that can bring one forward.
func (a *appState) sceneList(c *ui.Context) {
	scenes := a.session.Scenes()
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Padding(0, spaceSm).Children(func() {
		for i, scene := range scenes {
			sc := scene
			current := sc.ID == a.scene
			row := ui.Row(c).WidthPercent(100).Height(36).Padding(0, 10).Gap(spaceSm).
				AlignItems(ui.Center).Radius(radiusMd).
				OnClick(func() { a.selectScene(sc.ID) })
			row = row.Background(stateFill(row.Hovered(), row.Pressed(), current))
			row.Children(func() {
				bar := ui.Box(c).Width(2).Height(14).Radius(1).Background(ui.Transparent)
				if current {
					bar = bar.Background(colorSelectedText)
				}
				label := ui.Text(c, a.tr("Scene ", "场景 ")+itoa(i+1)).FontSize(textBody).
					FontWeight(500)
				if current {
					label = label.TextColor(colorSelectedText)
				} else {
					label = label.TextColor(colorTextDim)
				}
				ui.Box(c).Grow(1)
				ui.Text(c, itoa(len(sc.Video))).FontSize(textMicro).TextColor(colorDim).
					FontFeatures("tnum")
				if len(scenes) > 1 {
					a.iconButton(c, "scene-drop-"+string(sc.ID), lucide.X(),
						a.tr("remove this scene", "删除这个场景"), false, func() {
							a.removeScene(sc.ID)
						})
				}
			})
		}
		// A new scene is a row like the others, not a filled button under
		// them: it is the same kind of thing, so it looks the same.
		add := ui.ButtonBase(c.Key("scene-add")).WidthPercent(100).Height(36).
			Padding(0, 10).Gap(spaceSm).Justify(ui.Start).Radius(radiusMd)
		add = add.Background(stateFill(add.Hovered(), add.Pressed(), false))
		ink := colorTextDim
		if add.Hovered() {
			ink = colorText
		}
		add = add.TextColor(ink)
		add.Children(func() {
			ui.Icon(c, lucide.Plus()).Size(16, 16)
			ui.Text(c, a.tr("Add scene", "添加场景")).FontSize(textBody).FontWeight(500).TextColor(ink)
		})
		if add.Clicked() {
			a.addScene()
		}
	})
}

// selectScene makes an existing scene the one the stage draws.
func (a *appState) selectScene(id core.SceneID) {
	if id == a.scene {
		return
	}
	if _, err := a.session.Scene(id); err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	a.scene = id
	a.apply()
	a.invalidate()
}

// removeScene drops a scene and stays on a scene that exists.
func (a *appState) removeScene(id core.SceneID) {
	if err := a.session.RemoveScene(id); err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	delete(a.edits, id)
	if a.scene == id {
		if scenes := a.session.Scenes(); len(scenes) > 0 {
			a.scene = scenes[0].ID
		}
	}
	a.pruneStageEdits()
	a.apply()
	a.invalidate()
}

// sceneRow is the single-line scene label used where only the current scene
// matters, such as the stage's own caption.
func (a *appState) sceneRow(c *ui.Context) {
	ui.Row(c).WidthPercent(100).Height(38).Padding(0, spaceMd).Gap(spaceXs).
		AlignItems(ui.Center).Background(colorPanel2).Children(func() {
		ui.Box(c).Width(6).Height(6).Radius(3).Background(colorAccent)
		ui.Text(c, a.sceneLabelText()).FontSize(textBody).TextColor(colorText).SingleLine()
		ui.Box(c).Grow(1)
		ui.Text(c, itoa(len(a.items))).FontSize(textMicro).TextColor(colorDim)
	})
}

// sourceSub is the second line of a source row: the shortest true account of
// what the player is doing.
func (a *appState) sourceSub(it *item) string {
	switch {
	case it.openErr != nil:
		return a.tr("failed to open", "打开失败")
	case it.player == nil:
		return a.tr("not opened yet", "尚未打开")
	case !it.player.Ready():
		return a.tr("opening", "正在打开")
	case it.player.Buffering():
		return a.tr("buffering", "缓冲中")
	case !it.shown:
		return a.tr("off the stage", "不在舞台上")
	}
	return clock(it.player.Position()) + " / " + clock(it.player.Duration())
}

// columnFooter is the page shortcut strip. The pages moved out of the top bar,
// so this is where they stay one press away while the picture is visible.
func (a *appState) columnFooter(c *ui.Context) {
	ui.Row(c).WidthPercent(100).Padding(spaceSm, spaceSm).Gap(spaceXs).Wrap().Children(func() {
		navRow := func(key string, icon *ui.SVG, label string, on bool, click func()) {
			button := ui.ButtonBase(c.Key(key)).Height(30).Padding(0, spaceSm).Gap(spaceSm).
				Radius(radiusSm).Justify(ui.Start)
			button = button.Background(stateFill(button.Hovered(), button.Pressed(), on))
			ink := colorTextDim
			if on {
				ink = colorSelectedText
			}
			button = button.TextColor(ink).Tooltip(label)
			button.Children(func() {
				ui.Icon(c, icon).Size(16, 16)
				ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink)
			})
			if button.Clicked() {
				click()
			}
		}
		// Sources are not a page any more: the console owns that list, so this
		// button brings the console forward instead of opening a second copy.
		sourcesOn := a.dockUp && !a.dockHidden[dockSources] && a.workspace == workspaceStage
		navRow("foot-sources", lucide.SquareStack(), a.pageLabel(workspaceSources), sourcesOn, func() {
			delete(a.dockHidden, dockSources)
			a.dockUp = true
			a.workspace = workspaceStage
		})
		navRow("foot-"+itoa(int(workspaceDiagnostics)), lucide.Activity(),
			a.pageLabel(workspaceDiagnostics), a.workspace == workspaceDiagnostics, func() {
				a.workspace = workspaceDiagnostics
			})
		navRow("foot-"+itoa(int(workspaceSettings)), lucide.Settings(),
			a.pageLabel(workspaceSettings), a.workspace == workspaceSettings, func() {
				a.workspace = workspaceSettings
			})
		if a.workspace != workspaceStage {
			navRow("foot-stage", lucide.Clapperboard(), a.tr("Stage", "舞台"), false, func() {
				a.workspace = workspaceStage
			})
		}
	})
}

// pageLabel names a page in the window's language.
func (a *appState) pageLabel(page workspace) string {
	switch page {
	case workspaceStage:
		return a.tr("Stage", "舞台")
	case workspaceSources:
		return a.tr("Sources", "来源")
	case workspaceDiagnostics:
		return a.tr("Diagnostics", "诊断")
	case workspaceSettings:
		return a.tr("Settings", "设置")
	case workspaceMixer:
		return a.tr("Mixer", "混音器")
	}
	return a.tr("Page", "页面")
}

// preview is the stage page in the middle column: the layout switches, the
// picture, and the transport under it.
func (a *appState) preview(c *ui.Context, width, height float32) {
	// The P16 shell has one rail on the left and no properties column on the
	// right, so the picture may use everything after the rail and its gap.
	// Subtracting a right column that is no longer drawn was what kept the
	// canvas at half the window.
	available := width
	if width >= 900 {
		rail := a.navWidth
		if rail <= 0 {
			rail = sceneWidth
		}
		if !a.navUp {
			rail = railFoldWidth
		}
		available -= rail + spaceSm
	}
	if available < 320 {
		available = 320
	}
	// The picture keeps 16:9 before anything else, and never grows taller than
	// the room left after the bar, the transport and the status line.
	pictureCap := available * 9 / 16
	if !a.editingShell {
		pictureCap = height
	}
	// The console takes its own height out of the window; the picture must not
	// be measured against the whole window or it is drawn under the dock.
	room := height
	if a.editingShell {
		room = height - a.statusBarHeight() - 44 - 12
		if a.dockUp {
			room -= a.dockHeight
		}
	}
	if room > 120 && pictureCap > room {
		pictureCap = room
	}
	// The picture is 16:9, so the box that holds it is too. Without this the
	// box takes the whole width and the video is letterboxed inside it, which
	// reads as two dead columns rather than as a picture with room around it.
	pictureWidth := pictureCap * 16 / 9
	if pictureWidth > width {
		pictureWidth = width
		pictureCap = width * 9 / 16
	}
	ui.Column(c).WidthPercent(100).Grow(1).Children(func() {
		ui.Box(c).Grow(1)
		ui.Row(c).WidthPercent(100).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1)
			ui.Box(c).Width(pictureWidth).Height(pictureCap).MaxHeight(pictureCap).
				Border(1, colorLineSoft).Children(func() {
				a.stage(c)
				a.stageCorner(c)
			})
			ui.Box(c).Grow(1)
		})
		ui.Box(c).Grow(1)
	})
	a.transportBar(c)
	if a.webView != nil {
		ui.Column(c).Height(300).Children(func() {
			ui.WebView(c, a.webView).Grow(1)
		})
	}
}

// stageCorner is the small cluster in the picture's top-right corner: the
// working controls that belong to the picture itself. They are icons because
// five words over a picture is what this window is trying not to be, and the
// tooltip carries the name for anyone who needs it.
func (a *appState) stageCorner(c *ui.Context) {
	ui.Row(c).Absolute().Top(spaceSm).Right(spaceSm).Gap(2).AlignItems(ui.Center).
		Padding(spaceXs, spaceXs).Radius(radiusLg).Background(colorPanel2).
		Border(1, colorLineSoft).Shadow(0, 8, 24, 0, shadowFloat).Children(func() {
		next := a.layout.next()
		a.iconButtonSized(c, "corner-layout", lucide.Grid2x2(),
			a.tr("layout: ", "布局：")+a.layoutLabel(a.layout)+a.tr(" → next ", "，下一个：")+a.layoutLabel(next),
			30, colorText, false, func() { a.setLayout(next) })
		editInk := colorText
		if a.editMode {
			editInk = colorAccent
		}
		a.iconButtonSized(c, "corner-edit", lucide.Move(),
			a.tr("edit the picture's place", "编辑画面位置"), 30, editInk, a.editMode, func() {
				a.editMode = !a.editMode
				a.invalidate()
			})
		ui.Box(c).Width(1).Height(18).Background(colorLineSoft)
		a.iconButtonSized(c, "corner-cinema", lucide.MonitorPlay(),
			a.tr("watch in cinema", "沉浸观看"), 30, colorText, false, func() {
				a.enterViewing(viewCinema, a.windowRef())
				a.invalidate()
			})
		a.iconButtonSized(c, "corner-full", lucide.Maximize(),
			a.tr("window full screen", "窗口全屏"), 30, colorText, false, func() {
				a.enterViewing(viewFullscreen, a.windowRef())
				a.invalidate()
			})
	})
}

// next names the arrangement a second press of the layout button reaches, so
// the button can say what it will do before it does it.
func (k layoutKind) next() layoutKind {
	for i, kind := range layoutKinds {
		if kind == k {
			return layoutKinds[(i+1)%len(layoutKinds)]
		}
	}
	return layoutFocus
}

func (a *appState) toolbar(c *ui.Context, width float32) {
	ui.Row(c).WidthPercent(100).Height(36).Padding(0, spaceSm).Gap(spaceXs).
		AlignItems(ui.Center).Children(func() {
		for _, kind := range layoutKinds {
			button := ui.Button(c.Key("layout-"+kind.String()), a.layoutLabel(kind)).
				Height(28).Padding(0, spaceSm)
			if kind == a.layout {
				button = button.Border(1, colorAccent).Background(colorPanel2)
			}
			if button.Clicked() {
				a.setLayout(kind)
			}
		}
		ui.Box(c).Grow(1)
		if note := a.layoutNote(); note != "" {
			ui.Text(c, note).FontSize(textMicro).TextColor(colorDim).SingleLine()
		}
		edit := ui.Button(c.Key("stage-edit"), a.tr("Edit", "编辑")).Height(28).Padding(0, spaceSm)
		if a.editMode {
			edit = edit.Background(colorAccentSoft).TextColor(colorAccent)
		}
		if edit.Clicked() {
			a.editMode = !a.editMode
			if a.editMode && a.selected == "" && len(a.items) > 0 {
				a.selected = a.items[0].source.ID
			}
		}
		if width < 900 {
			if ui.Button(c.Key("stage-props"), a.tr("Properties", "属性")).Height(28).Padding(0, spaceSm).Clicked() {
				a.drawerOpen = !a.drawerOpen
			}
		}
	})
}

// layoutLabel names an arrangement in the window's language.
func (a *appState) layoutLabel(kind layoutKind) string {
	switch kind {
	case layoutFocus:
		return a.tr("Focus", "聚焦")
	case layoutSplit:
		return a.tr("Split", "分屏")
	case layoutPip:
		return a.tr("PiP", "画中画")
	case layoutGrid:
		return a.tr("Grid", "网格")
	case layoutHero:
		return a.tr("Hero", "主次")
	}
	return kind.label()
}

// layoutNote explains what the arrangement cannot show, instead of leaving a
// person to wonder why a source is missing from the stage.
func (a *appState) layoutNote() string {
	shown := 0
	for _, it := range a.items {
		if it.player != nil && it.shown {
			shown++
		}
	}
	if shown > a.layout.slots() {
		return itoa(shown) + a.tr(" shown; this layout places ", " 路已显示；此布局最多 ") + itoa(a.layout.slots())
	}
	return ""
}

// transportBar is the row a person touches most often: play, mute, reload, then
// where the picture is and how long it runs, and the way into the two watching
// modes. The position is read back from the player rather than from the shared
// clock: this row is about the picture in front of you.
func (a *appState) transportBar(c *ui.Context) {
	snap := a.snapshot()
	first := a.firstShown()
	ui.Row(c).WidthPercent(100).Height(44).Padding(0, spaceSm).Gap(spaceSm).
		Background(colorBg).Border(0, colorBg).
		AlignItems(ui.Center).Children(func() {
		ui.Row(c).Grow(1).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
				playing := a.playing()
				play := a.tr("Pause", "暂停")
				if !playing {
					play = a.tr("Play", "播放")
				}
				playButton := ui.ButtonBase(c.Key("transport-play")).Width(88).Height(32).
					Padding(0, spaceMd).Radius(radiusMd).AlignItems(ui.Center).Shrink(0).AlignSelf(ui.Center)
				fill, ink := colorEnabled, colorSelectedText
				if len(a.items) == 0 {
					fill, ink = colorPanel2, colorDim
					playButton = playButton.Disabled(true)
				} else if playButton.Pressed() {
					fill, ink = colorEnabledPress, colorText
				} else if playButton.Hovered() {
					fill, ink = colorEnabledHover, colorText
				}
				playButton = playButton.Background(fill).TextColor(ink)
				playIcon := lucide.Play()
				if playing {
					playIcon = lucide.Pause()
				}
				playButton.Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, playIcon).Size(14, 14).TextColor(ink)
						ui.Text(c, play).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
					})
				})
				if playButton.Clicked() {
					if a.playing() {
						a.runCommand(command{Kind: commandPause})
					} else {
						a.runCommand(command{Kind: commandPlay})
					}
				}
				mute := a.tr("Mute", "静音")
				muteIcon := lucide.VolumeX()
				if a.masterMute {
					mute = a.tr("Unmute", "取消静音")
					muteIcon = lucide.Volume2()
				}
				a.ghostIconButton(c, "transport-mute", mute, muteIcon, 0, func() {
					a.runCommand(command{Kind: commandMasterMute})
				})
				a.ghostIconButton(c, "transport-reload", a.tr("Reload", "重载"), lucide.RotateCcw(), 0, func() {
					a.runCommand(command{Kind: commandReload})
				})
				ui.Box(c).Width(1).Height(20).Background(colorLineSoft)
				if first != nil {
					ui.Text(c, a.name(first)).FontSize(textCaption).TextColor(colorTextDim).
						MaxWidth(220).SingleLine().Padding(spaceSm, 0)
				}
				// The event clock only means something once the shared timeline
				// has been started. A window opened straight onto a file has not
				// started one, and printing 0.0s beside a player at 0:14 would
				// read as a stuck clock rather than as "there is no timeline yet".
				if snap.Event > 0 {
					ui.Text(c, formatEvent(snap.Event)).FontSize(textBody).TextColor(colorText).
						FontFeatures("tnum").Padding(spaceXs, 0)
				}
				if first != nil {
					total := first.player.Duration()
					ui.Text(c, clock(first.player.Position())+" / "+clock(total)).
						FontSize(textBody).TextColor(colorTextDim).FontFeatures("tnum").
						Padding(spaceSm, 0)
				}
			})
			// One way in to watching. Cinema is the windowed form; the
			// overlay inside it already offers full screen, so the workbench
			// does not need two buttons that both mean "stop editing".
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(1).Height(20).Background(colorLineSoft)
				a.ghostIconButton(c, "transport-cinema", a.tr("Watch", "沉浸观看"), lucide.MonitorPlay(), 104, func() {
					a.enterViewing(viewCinema, a.windowRef())
				})
			})
		})
	})
}

// propertiesColumn is the right column: the selected source's properties, then
// the audio levels. The mixer keeps its place at the bottom so a level change is
// one press from anything the properties show.
func (a *appState) propertiesColumn(c *ui.Context, height float32) {
	ui.Column(c).Width(propertiesWidth).Background(colorBgSoft).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Padding(spaceSm, spaceMd).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Properties", "属性")).FontSize(textMicro).TextColor(colorDim)
				ui.Box(c).Grow(1)
				if it, index, ok := a.selectedSource(); ok {
					ui.Text(c, itoa(index+1)+" · "+shortURI(it.uri)).FontSize(textMicro).
						TextColor(colorDim).SingleLine()
				}
			})
			a.propertiesTabs(c)
			ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
				ui.Column(c).WidthPercent(100).Padding(0, spaceMd).Gap(spaceSm).Children(func() {
					a.propertiesBody(c)
				})
			})
		})
		ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
		a.mixerPanel(c, height)
	})
}

// propertiesTabs are the three things a source has: a picture, a sound and a
// place on the timeline.
func (a *appState) propertiesTabs(c *ui.Context) {
	labels := [3]string{a.tr("Picture", "画面"), a.tr("Sound", "音频"), a.tr("Time", "时间")}
	ui.Row(c).WidthPercent(100).Padding(0, spaceSm).Gap(spaceXs).Children(func() {
		for i, label := range labels {
			tab := ui.Button(c.Key("props-tab-"+itoa(i)), label).Grow(1).Height(28).Padding(0, 4)
			if a.propsTab == i {
				tab = tab.Background(colorPanel2).Border(1, colorLine)
			}
			if tab.Clicked() {
				a.propsTab = i
			}
		}
	})
}

// propertiesBody switches on the tab. Without a selection it shows the session
// summary, which is the honest answer to "what is this window doing".
func (a *appState) propertiesBody(c *ui.Context) {
	it, index, ok := a.selectedSource()
	if !ok {
		a.sessionSummary(c)
		return
	}
	switch a.propsTab {
	case 0:
		a.pictureTab(c, it)
	case 1:
		a.soundTab(c, it, index)
	case 2:
		a.timeTab(c, it, index)
	}
}

// sessionSummary answers "what is this window doing" when nothing is selected.
func (a *appState) sessionSummary(c *ui.Context) {
	ui.Text(c, a.tr("No source selected", "未选中来源")).FontSize(textBody).TextColor(colorText)
	ui.Text(c, a.tr("Pick one in the list on the left to see its picture, sound and timing.",
		"在左侧列表选一个来源，查看它的画面、音频与时间。")).
		FontSize(textCaption).TextColor(colorDim).Wrap()
	snap := a.snapshot()
	a.detailRow(c, a.tr("Scene", "场景"), a.sceneLabelText())
	a.detailRow(c, a.tr("Event", "时间轴"), formatEvent(snap.Event))
	a.detailRow(c, a.tr("Playing", "播放"), boolLine(snap.Playing, a))
	a.detailRow(c, a.tr("Sources", "来源"), itoa(len(a.items)))
	a.detailRow(c, a.tr("Picture", "画面"), itoa(len(snap.Picture)))
	a.detailRow(c, a.tr("Sound", "声音"), itoa(len(snap.Sound)))
	if a.syncNote != "" {
		ui.Text(c, a.syncNote).FontSize(textMicro).TextColor(colorWarn).Wrap()
	}
}

// pictureTab is the source's place on the stage: the box fields, the layer and
// the placement buttons.
func (a *appState) pictureTab(c *ui.Context, it *item) {
	if _, on := a.stageBoxOf(it); !on {
		ui.Text(c, a.tr("This source is not on the stage.", "此来源不在舞台上。")).
			FontSize(textCaption).TextColor(colorDim).Wrap()
		if ui.Button(c.Key("pic-show"), a.tr("Put it on the stage", "放到舞台")).Height(30).Clicked() {
			a.selectVideo(it)
		}
		return
	}
	a.editControls(c, it)
}

// soundTab is the per-source audio: level, mute, what the scene allows, and the
// nudge buttons that move the source against the shared clock.
func (a *appState) soundTab(c *ui.Context, it *item, index int) {
	a.detailRow(c, a.tr("State", "状态"), a.soundState(a.entryOf(it)))
	ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr("Level", "音量")).FontSize(textMicro).TextColor(colorDim).Width(52)
		if a.rangeSlider(c, "snd-vol", &it.volume, 0, 1, 160) {
			a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: it.volume})
		}
		ui.Text(c, fmt.Sprintf("%.2f", it.volume)).Width(40).FontSize(textSmall).
			TextColor(colorText).FontFeatures("tnum")
	})
	ui.Row(c).Wrap().Gap(spaceXs).Children(func() {
		if ui.Button(c.Key("snd-mute"), a.muteLabel(it)).Height(26).Clicked() {
			a.runCommand(command{Kind: commandMute, Source: it.source.ID})
		}
		if it.audio != "" {
			if ui.Button(c.Key("snd-allow"), a.shortAllowLabel(it)).Height(26).Clicked() {
				a.runCommand(command{Kind: commandAllow, Source: it.source.ID})
			}
		}
		if it.openErr != nil {
			if ui.Button(c.Key("snd-retry"), a.tr("Retry", "重试")).Height(26).Clicked() {
				a.runCommand(command{Kind: commandRetry, Source: it.source.ID})
			}
		}
		if ui.Button(c.Key("snd-remove"), a.tr("Remove", "移除")).Height(26).Clicked() {
			a.runCommand(command{Kind: commandRemove, Source: it.source.ID})
		}
	})
	a.detailRow(c, a.tr("Align", "对齐"), a.alignedState(a.entryOf(it)))
	ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr("Offset", "偏移")).FontSize(textMicro).TextColor(colorDim).Width(52)
		a.offsetStepper(c, "snd-step", a.entryOf(it).Offset, a.settingsNudge(1),
			func(delta time.Duration) {
				a.report(a.runCommand(command{Kind: commandNudge, Source: it.source.ID, Delta: delta}))
			})
		if offset := a.entryOf(it).Offset; offset != 0 {
			a.iconButton(c, "snd-reset", lucide.RotateCcw(),
				a.tr("set this offset back to zero", "把这一路的偏移复位"), false, func() {
					a.report(a.runCommand(command{Kind: commandNudge, Source: it.source.ID, Delta: -offset}))
				})
		}
	})
	if it.audio != "" && a.hasCommonClock(it) {
		if ui.Button(c.Key("snd-try-align"), a.tr("Try to align", "尝试对齐")).Height(26).Clicked() {
			if index > 0 {
				a.probeAsync(0, index)
			}
		}
	}
}

// timeTab is the source's own clock and the replay seek.
func (a *appState) timeTab(c *ui.Context, it *item, index int) {
	a.detailRow(c, a.tr("Position", "位置"), a.positionLine(it))
	a.detailRow(c, a.tr("Duration", "时长"), a.durationLine(it))
	if it.openErr != nil {
		ui.Text(c, it.openErr.Error()).FontSize(textMicro).TextColor(colorBad).Wrap()
	}
	a.inspectorSeek(c)
	a.inspectorAccount(c)
	a.inspectorAlign(c)
}

// mixerPanel is the right column's audio half: the master and one channel per
// source, the way a mixer looks in a capture program.
func (a *appState) mixerPanel(c *ui.Context, height float32) {
	ui.Column(c).WidthPercent(100).Height(mixerHeight).Padding(spaceSm).Gap(spaceXs).
		Background(colorBgSoft).Children(func() {
		ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Mixer", "混音器")).FontSize(textMicro).TextColor(colorDim)
			ui.Box(c).Grow(1)
			mute := a.tr("Mute all", "全部静音")
			if a.masterMute {
				mute = a.tr("Hear all", "全部有声")
			}
			if ui.Button(c.Key("mix-master"), mute).Height(22).Padding(0, 6).Clicked() {
				a.runCommand(command{Kind: commandMasterMute})
			}
		})
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Gap(2).Children(func() {
				if len(a.items) == 0 {
					ui.Text(c, a.tr("No sources are open.", "还没有打开来源。")).
						FontSize(textCaption).TextColor(colorDim).Wrap()
					return
				}
				for _, it := range a.items {
					a.mixerChannel(c, it)
				}
			})
		})
		ui.Text(c, a.tr("Output: system default", "输出：系统默认设备")).
			FontSize(textMicro).TextColor(colorDim).SingleLine()
	})
}

// mixerChannel is one strip in the mixer: name, mute, level.
func (a *appState) mixerChannel(c *ui.Context, it *item) {
	row := a.rowSurface(c).Height(40).Padding(0, spaceSm).Gap(spaceXs).
		AlignItems(ui.Center)
	row.Children(func() {
		ui.Box(c).Width(6).Height(6).Radius(3).Background(colorSound)
		ui.Text(c, shortURI(it.uri)).FontSize(textSmall).FontWeight(400).TextColor(colorText).
			Grow(1).MinWidth(0).SingleLine()
		muted := a.sourceMuted(it)
		icon := lucide.Volume2()
		tint := colorTextDim
		if muted {
			icon = lucide.VolumeX()
			tint = colorDim
		}
		a.quietIconButton(c, "mix-mute-"+string(it.source.ID), icon,
			a.tr("mute this source", "静音这一路"), tint, func() {
				a.runCommand(command{Kind: commandMute, Source: it.source.ID})
			})
		if a.volumeSlider(c, "mix-vol-"+string(it.source.ID), &it.volume, 84) {
			a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: it.volume})
		}
		ui.Text(c, fmt.Sprintf("%.0f%%", it.volume*100)).Width(42).PaddingStart(6).
			FontSize(textSmall).FontWeight(500).TextColor(colorTextDim).TextAlign(ui.End).
			FontFeatures("tnum").SingleLine()
	})
}

// mixerStrip is the narrow window's mixer: a bottom bar that can open into a few
// channel rows, so a 747px window still has a level control.
func (a *appState) mixerStrip(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Background(colorBgSoft).Children(func() {
		ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
		ui.Row(c).WidthPercent(100).Height(mixerStripHi).Padding(0, spaceMd).Gap(spaceSm).
			AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Mixer", "混音器")).FontSize(textMicro).TextColor(colorDim)
			mute := a.tr("Mute all", "全部静音")
			if a.masterMute {
				mute = a.tr("Hear all", "全部有声")
			}
			if ui.Button(c.Key("strip-master"), mute).Height(22).Padding(0, 6).Clicked() {
				a.runCommand(command{Kind: commandMasterMute})
			}
			ui.Box(c).Grow(1)
			toggle := a.tr("Open", "展开")
			if a.mixerOpen {
				toggle = a.tr("Fold", "收起")
			}
			if ui.Button(c.Key("strip-toggle"), toggle).Height(22).Padding(0, 6).Clicked() {
				a.mixerOpen = !a.mixerOpen
			}
		})
		if !a.mixerOpen {
			return
		}
		ui.Column(c).WidthPercent(100).Padding(0, spaceMd).Gap(2).Children(func() {
			for _, it := range a.items {
				ui.Row(c).WidthPercent(100).Height(26).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
					ui.Text(c, shortURI(it.uri)).FontSize(textMicro).TextColor(colorText).Width(90).SingleLine()
					if ui.Slider(c, &it.volume, 0, 1).Grow(1).Height(16).Changed() {
						a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: it.volume})
					}
					ui.Text(c, fmt.Sprintf("%.0f%%", it.volume*100)).Width(32).FontSize(textMicro).
						TextColor(colorDim).FontFeatures("tnum")
				})
			}
		})
	})
}

// drawer is the properties column of a narrow window: it opens over the picture
// from the right and closes with its own button.
func (a *appState) drawer(c *ui.Context, height float32) {
	ui.Column(c).Absolute().Top(0).Bottom(0).Width(drawerWidth).
		LeftPercent(100).TranslatePercent(-100, 0).ZIndex(30).
		Background(colorPanel).Border(1, colorLine).Children(func() {
		ui.Row(c).Padding(spaceSm, spaceMd).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Properties", "属性")).FontSize(textMicro).TextColor(colorDim)
			ui.Box(c).Grow(1)
			if ui.Button(c.Key("drawer-close"), a.tr("Close", "关闭")).Height(24).Padding(0, spaceSm).Clicked() {
				a.drawerOpen = false
			}
		})
		a.propertiesTabs(c)
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Padding(0, spaceMd).Gap(spaceSm).Children(func() {
				a.propertiesBody(c)
			})
		})
	})
}

// statusBar is the one line that must never disappear: what the session is
// statusBar answers "is anything wrong" and nothing else when the transport
// already reads out the clock: repeating 0.0s here was the third place the same
// number appeared.
func (a *appState) statusBar(c *ui.Context, width float32) {
	line := a.stripStatus()
	if a.firstShown() != nil {
		line = a.tr("ready", "就绪") + " · " + itoa(len(a.items)) + " " + a.tr("sources", "个来源")
	}
	ui.Row(c).WidthPercent(100).Height(a.statusBarHeight()).Padding(0, spaceMd).
		Gap(spaceSm).AlignItems(ui.Center).Background(colorBg).Children(func() {
		ui.Text(c, line).FontSize(textMicro).TextColor(colorDim).
			FontFeatures("tnum").SingleLine().Grow(1)
		if a.lastErr != nil {
			if ui.Button(c.Key("status-error"), "!").Width(22).Height(18).
				Border(1, colorBad).TextColor(colorBad).
				Tooltip(a.tr("open the error report", "打开错误报告")).Clicked() {
				a.errorOpen = true
			}
		}
		if a.importing != "" {
			ui.Text(c, a.tr("reading ", "正在读取 ")+a.importing).FontSize(textMicro).
				TextColor(colorWarn).SingleLine()
		}
	})
}

// errorReport is the one place a failure is spelled out: a short sentence, the
// raw detail, and the two things a person can do about it.
func (a *appState) errorReport(c *ui.Context, width, height float32) {
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().
		Background(ui.RGBA(0, 0, 0, 0.55)).Children(func() {
		ui.Column(c).Width(560).MaxWidth(width-40).Padding(spaceXl).Gap(spaceMd).
			Radius(radiusLg).Background(colorPanel2).Border(1, colorLineSoft).
			Shadow(0, 20, 48, 0, shadowDialog).Children(func() {
			ui.Text(c, a.tr("Something went wrong", "出错了")).FontSize(textTitle).
				FontWeight(600).TextColor(colorText)
			ui.Text(c, a.errorShort()).FontSize(textBody).TextColor(colorText).Wrap()
			if a.lastErr != nil {
				ui.Scroll(c).WidthPercent(100).MaxHeight(140).Children(func() {
					ui.Text(c, a.lastErr.Error()).FontSize(textMicro).FontWeight(400).
						TextColor(colorDim).Wrap()
				})
			}
			ui.Row(c).Gap(spaceSm).Children(func() {
				if ui.Button(c.Key("err-diagnostics"), a.tr("Open diagnostics", "打开诊断")).Height(30).Clicked() {
					a.workspace = workspaceDiagnostics
					a.errorOpen = false
				}
				if a.importText != "" {
					if ui.Button(c.Key("err-retry"), a.tr("Retry", "重试")).Height(30).Clicked() {
						a.retryImport()
						a.errorOpen = false
					}
				}
				ui.Box(c).Grow(1)
				if ui.Button(c.Key("err-close"), a.tr("Close", "关闭")).Height(30).Padding(0, spaceMd).Clicked() {
					a.errorOpen = false
				}
			})
		})
	})
}

// errorShort is the sentence a person reads first: what failed, in one line,
// without the URL and without the Go error text.
func (a *appState) errorShort() string {
	if a.lastErr == nil {
		return ""
	}
	msg := a.tr("The last import did not finish.", "上一次导入没有完成。")
	if a.importText != "" {
		msg = a.tr("This address did not load: ", "这个地址没有加载成功：") + a.importText
	}
	return msg
}

// pageMenuPanel is the page list and the way out of the window, in one place.
func (a *appState) pageMenuPanel(c *ui.Context, width float32) {
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(19).
		OnClick(func() { a.pageMenu = false })
	ui.Column(c).Absolute().Top(a.barHeight()+4).Right(12).Width(220).Padding(4).Gap(2).
		Radius(radiusCard).Background(colorPanel2).Border(1, colorLineSoft).ZIndex(20).
		Shadow(0, 8, 24, 0, shadowFloat).Children(func() {
		for _, page := range []workspace{workspaceStage, workspaceSources, workspaceDiagnostics, workspaceSettings, workspaceMixer} {
			p := page
			button := ui.Button(c.Key("menu-"+itoa(int(p))), a.pageLabel(p)).Height(32).Padding(0, spaceSm)
			if p == a.workspace {
				button = button.Background(colorSelected).TextColor(colorSelectedText)
			}
			if button.Clicked() {
				a.workspace = p
				a.pageMenu = false
			}
		}
		ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
		if ui.Button(c.Key("menu-quit"), a.tr("Quit MuxShift", "退出 MuxShift")).Height(32).
			Padding(0, spaceSm).TextColor(colorBad).Clicked() {
			a.release()
			a.closes++
			mygo.App.Quit()
		}
	})
}

// sceneMenuPanel says what the scene chip can do: nothing yet, and why.
func (a *appState) sceneMenuPanel(c *ui.Context, width float32) {
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(19).
		OnClick(func() { a.sceneMenu = false })
	ui.Column(c).Absolute().Top(a.barHeight()+4).Left(sceneWidth+spaceMd).Width(220).
		Padding(4).Gap(2).Radius(radiusCard).Background(colorPanel2).Border(1, colorLineSoft).
		ZIndex(20).Shadow(0, 8, 24, 0, shadowFloat).Children(func() {
		ui.Row(c).Height(32).Padding(0, spaceSm).Gap(spaceXs).AlignItems(ui.Center).
			Background(colorSelected).Children(func() {
			ui.Text(c, a.sceneLabelText()).FontSize(textBody).TextColor(colorSelectedText)
		})
		ui.Button(c.Key("scene-new"), a.tr("New scene…", "新建场景…")).Height(32).Padding(0, spaceSm).Disabled(true).
			Tooltip(a.tr("this build has one scene", "此版本只有一个场景"))
	})
}

// sourceCard is the Sources page's row: the whole width, the file, its state and
// the actions that do not belong in the scene list.
// pill is a small label in a source card. The fill is the colour at a low alpha
// and the text is the colour itself, so the pair keeps its contrast in every
// theme pack.
func pill(c *ui.Context, label string, tint ui.Color, strong bool) {
	background := colorPanel2
	if strong {
		background = colorAccentSoft
	}
	ui.Box(c).Padding(2, 6).Radius(4).Background(background).Children(func() {
		ui.Text(c, label).FontSize(textMicro).TextColor(tint).SingleLine()
	})
}

// detailRow is one label/value line in the properties column.
func (a *appState) detailRow(c *ui.Context, label, value string) {
	ui.Row(c).WidthPercent(100).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(76).FontSize(textMicro).TextColor(colorDim)
		ui.Text(c, value).FontSize(textSmall).TextColor(colorText).FontFeatures("tnum").SingleLine().Grow(1)
	})
}

// addSourceDialog replaces the stock modal, whose panel was drawn in the page's
// own colour and whose scrim was a 40% black that a dark page swallows: here the
// backdrop is deeper and the panel has a border, so the dialog reads as a layer.
func (a *appState) addSourceDialog(c *ui.Context) {
	ui.DialogBase(c, &a.addOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		panel = panel.Background(colorPanel2).Border(1, colorLine).Radius(radiusLg).
			Padding(spaceXl).Gap(spaceMd).Shadow(0, 20, 48, 0, shadowDialog)
		panel.Children(func() { a.addSourcePanel(c) })
	})
}

// boolLine spells a boolean for a detail row.
func boolLine(v bool, a *appState) string {
	if v {
		return a.tr("yes", "是")
	}
	return a.tr("no", "否")
}
