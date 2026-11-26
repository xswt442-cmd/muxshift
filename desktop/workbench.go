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
	"strings"
	"time"

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
	topBarHeight                = 48
	statusHeight                = 26
	dockHeight                  = 168
	inspectorWidth              = 300
	railWidth                   = 240
	railFoldWidth               = 64
	inspectorAutoWidth          = 1600
	railAutoWidth               = 1600
	dockAutoHeight              = 660
	brandAutoWidth              = 760
	transportLabelWidth         = 760
	statusQuietWidth    float32 = 1280
	compactButtonWidth  float32 = 1200
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
	a.frameWidth, a.frameHeight = width, height
	if !narrow {
		a.drawerOpen = false
	}
	// The source list lives in the console now. Anything that still asks for
	// the old page (a shortcut, a scenario, a settings file) lands on the stage
	// with that panel in front instead of a second copy of the list.
	if a.workspace == workspaceSources {
		a.showSourcesPanel()
	}
	ui.Column(c).Fill().Background(colorBg).Children(func() {
		a.workspaceNavigation(c, width)
		ui.Row(c).WidthPercent(100).Grow(1).AlignItems(ui.Stretch).Children(func() {
			// The scene column is a draggable rail the way a capture program
			// has one: it starts open, keeps its own width, and folds to icons.
			if !narrow && a.editingShell && a.workspace == workspaceStage {
				layout.DraggableSideNav(c, &a.navWidth, &a.navUp, layout.DraggableSideNavProps{
					MinWidth: 64, MaxWidth: 360, ShowHandleWhenCollapsed: true, Background: colorPanel,
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
		if a.renameOpen {
			a.renameDialog(c)
		}
	})
}

func (a *appState) workspaceNavigation(c *ui.Context, width float32) {
	ui.Row(c).WidthPercent(100).Height(a.barHeight()).Padding(0, spaceMd).
		Gap(spaceXs).AlignItems(ui.Center).Background(colorPanel).Children(func() {
		if width < 900 {
			label := a.pageLabel(a.topWorkspacePage())
			if ui.Button(c.Key("corner-pages"), a.tr("Pages", "页面")+" · "+label).
				Height(a.controlHeight()).Padding(0, spaceSm).Clicked() {
				a.pageMenu = !a.pageMenu
			}
			if a.workspace == workspaceStage && a.editingShell {
				name := a.sceneLabelText()
				if ui.Button(c.Key("stage-scenes-menu"), name+" ▾").Height(a.controlHeight()).
					MaxWidth(200).Tooltip(a.tr("Switch scenes", "切换场景")).Clicked() {
					a.sceneMenu = !a.sceneMenu
				}
			}
			return
		}
		for _, page := range []workspace{workspaceStage, workspaceDiagnostics, workspaceSettings} {
			p := page
			button := ui.Button(c.Key("workspace-nav-"+p.title()), a.pageLabel(p)).
				Height(a.controlHeight()).Padding(0, spaceSm)
			if a.workspacePageActive(p) {
				button = button.Background(colorSelected).TextColor(colorSelectedText)
			}
			if button.Clicked() {
				if p == workspaceSettings {
					a.navigateToSettings("")
				} else {
					a.navigateWorkspace(p)
				}
			}
		}
		ui.Box(c).Grow(1)
		if a.workspace == workspaceStage && a.editingShell {
			label := a.tr("Collapse scenes", "收起场景栏")
			if !a.navUp {
				label = a.tr("Expand scenes", "展开场景栏")
			}
			a.secondaryButton(c, "stage-rail-toggle", label, 128, true, func() {
				a.navUp = !a.navUp
				a.invalidate()
			})
		}
	})
}

func (a *appState) topWorkspacePage() workspace {
	if a.currentWorkspacePage() == workspaceSources {
		return workspaceStage
	}
	return a.currentWorkspacePage()
}

func (a *appState) currentWorkspacePage() workspace {
	if a.workspace == workspaceStage && a.dockUp && !a.dockHidden[dockSources] && a.dockTab == 0 {
		return workspaceSources
	}
	return a.workspace
}

func (a *appState) workspacePageActive(page workspace) bool {
	if page == workspaceSources {
		return a.currentWorkspacePage() == workspaceSources
	}
	return a.workspace == page
}

func (a *appState) navigateWorkspace(page workspace) {
	a.pageMenu = false
	a.drawerOpen = false
	if page == workspaceSources {
		a.showSourcesPanel()
		return
	}
	// Old menu, shortcut and saved-workspace routes now land on the stage
	// console, where source and mixer controls remain available.
	if page == workspaceMixer {
		a.workspace = workspaceStage
		a.dockUp = true
		a.dockHidden[dockMixer] = false
		a.dockTab = 1
		a.invalidate()
		return
	}
	a.workspace = page
	a.invalidate()
}

func (a *appState) navigateToSettings(section string) {
	if section != "" {
		for _, available := range settingsSections {
			if available == section {
				a.settingsSection = section
				break
			}
		}
	}
	a.navigateWorkspace(workspaceSettings)
}

// pageBody draws a page in the middle column, with the padding the page's own
// rows expect.
func (a *appState) pageBody(c *ui.Context, width, height float32) {
	ui.Box(c).Fill().Padding(spaceMd).Children(func() {
		switch a.workspace {
		case workspaceDiagnostics:
			ui.Scroll(c).Fill().WidthPercent(100).Children(func() {
				a.diagnosticsPage(c)
			})
		case workspaceSettings:
			ui.Scroll(c).Fill().WidthPercent(100).Children(func() {
				a.settingsPage(c)
			})
		case workspaceMixer:
			a.mixerPage(c)
		}
	})
}

// sceneLabelText is the core's scene name in the window's language. The name is
// a description, not data: "picture 1 · sound 1 at 0.30" reads as gibberish to a
// Chinese user, so the three words are translated while the numbers stay.
// sceneLabelText names the scene the way the sidebar lists it. The core name
// ("main", or "scene 2") describes the scene's audio, not the scene, and reads
// as gibberish in the sidebar; its position is what a person points at.
func (a *appState) sceneLabelText() string {
	for i, scene := range a.session.Scenes() {
		if scene.ID == a.scene {
			return a.sceneDisplayName(scene, i)
		}
	}
	return a.tr("Scene", "场景")
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
	snap := a.eventTime()
	return fmt.Sprintf("%s · %s · %s", playing, formatEvent(snap), itoa(len(a.items))+" "+a.tr("sources", "个来源"))
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
func (a *appState) sourceEntry(c *ui.Context, it *item, index int, compact bool, placement ...map[*item]bool) {
	active := a.selected == itemKey(it)
	c.Key("source-row-" + string(itemKey(it)))
	row := ui.Row(c).WidthPercent(100).Gap(spaceXs).AlignItems(ui.Center)
	if compact {
		row = row.Height(a.rowHeight()).Padding(0, 10).Radius(radiusMd).
			OnClick(func() { a.selectStageSource(it) })
		fill := colorRow
		if row.Pressed() {
			fill = colorPressed
		} else if active {
			fill = colorSelected
		} else if row.Hovered() {
			fill = colorHover
		}
		row = row.Background(fill).Border(1, colorRowEdge)
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
		ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(5).Height(5).Radius(2.5).Background(a.sourceDot(it))
				ui.Text(c, sourceDisplayName(it)).FontSize(nameSize).FontWeight(nameWeight).
					TextColor(colorText).SingleLine()
				if !compact {
					a.sourceMarks(c, it)
				}
			})
			factsText := a.sourceSub(it)
			if compact {
				factsText = a.sourceRowStatus(it, placement)
				c.Key("source-state-" + string(itemKey(it)))
			}
			facts := ui.Text(c, factsText).FontSize(subSize).FontWeight(400).
				TextColor(colorDim).SingleLine()
			if compact {
				facts.PaddingStart(13).Tooltip(factsText)
			}
		})
		if compact {
			a.sourceCompactActions(c, it, index)
			return
		}
		a.sourceActions(c, it, index)
	})
	if compact && it.openErr != nil {
		ui.Row(c).WidthPercent(100).Wrap().Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("compact-retry-"+string(itemKey(it))), a.tr("Retry", "重试")).
				Height(24).Disabled(a.importing != "").Clicked() {
				a.selected = itemKey(it)
				a.runCommand(command{Kind: commandRetry, Source: itemKey(it)})
			}
			if ui.Button(c.Key("compact-reselect-"+string(itemKey(it))), a.tr("Choose address", "重新选择地址")).
				Height(24).Clicked() {
				a.selected = itemKey(it)
				a.reselectSource = itemKey(it)
				a.reselectText = ""
				a.reselectProblem = ""
			}
			if ui.Button(c.Key("compact-remove-"+string(itemKey(it))), a.tr("Remove", "移除")).
				Height(24).Clicked() {
				a.unlinkStageMember(it)
			}
		})
	}
	if compact && a.reselectSource == itemKey(it) {
		ui.Row(c).WidthPercent(100).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			c.Key("compact-reselect-input-" + string(itemKey(it)))
			field := a.field(c, &a.reselectText).Grow(1).
				Placeholder(`https://… .m3u8  or  C:\path\file.mp4`).
				Description(a.tr("Choose an address for ", "为此来源选择地址：") + sourceDisplayName(it))
			submit := field.Submitted()
			if ui.Button(c.Key("compact-reselect-cancel"), a.tr("Cancel", "取消")).Height(26).Clicked() {
				a.reselectSource = ""
				a.reselectText = ""
			}
			if ui.Button(c.Key("compact-reselect-apply"), a.tr("Use address", "使用地址")).Height(26).Clicked() {
				submit = true
			}
			if submit && strings.TrimSpace(a.reselectText) != "" && a.importing == "" {
				a.submitReselect(itemKey(it))
			}
		})
		if a.reselectSource == itemKey(it) && a.reselectProblem != "" {
			ui.Text(c, a.reselectProblem).FontSize(textMicro).TextColor(colorBad).Wrap()
		}
	}
}

func (a *appState) sourceRowStatus(it *item, placement []map[*item]bool) string {
	player := it.outputPlayer()
	if it.openErr != nil || player == nil || !player.Ready() || player.Buffering() {
		return a.sourceSub(it)
	}
	status := a.tr("Picture hidden", "画面已隐藏")
	if it.shown {
		switch {
		case len(placement) > 0 && !placement[0][it]:
			status = a.tr("Outside this layout", "当前布局未显示")
		case a.firstShown() == it && (a.layout == layoutFocus || a.layout == layoutPip || a.layout == layoutHero):
			status = a.tr("Primary picture", "主画面")
		default:
			status = a.tr("Picture shown", "画面已显示")
		}
	}
	if player.Playing() {
		return status + a.tr(" · Playing", " · 播放中")
	}
	return status + a.tr(" · Paused", " · 已暂停")
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

func (a *appState) sourceCompactActions(c *ui.Context, it *item, index int) {
	key := string(itemKey(it))
	if player := it.outputPlayer(); player != nil {
		kind, icon, label := commandSourcePlay, lucide.Play(), a.tr("Play", "播放")
		if player.Playing() {
			kind, icon, label = commandSourcePause, lucide.Pause(), a.tr("Pause", "暂停")
		}
		if a.sourceInExplicitGroup(it) {
			ui.Icon(c, icon).Size(16, 16).TextColor(colorDim).
				Tooltip(a.tr("Play or pause the whole sync group", "这一路随同步组播放或暂停"))
		} else {
			a.quietIconButton(c, "source-play-"+key, icon, label, colorTextDim, func() {
				a.runCommand(command{Kind: kind, Source: itemKey(it)})
			})
		}
	}
	if it.shown {
		a.quietIconButton(c, "source-primary-"+key, lucide.Crosshair(),
			a.tr("Make this the primary picture", "设为主画面"), colorTextDim, func() {
				a.runCommand(command{Kind: commandPrimary, Source: itemKey(it)})
			})
		if a.selected == itemKey(it) {
			label, tint := a.tr("Lock position", "锁定摆位"), colorTextDim
			if a.stageLocked(it.source.ID) {
				label, tint = a.tr("Unlock position", "解锁摆位"), colorAccent
			}
			a.quietIconButton(c, "source-lock-"+key, lucide.Lock(), label, tint, func() {
				a.toggleStageLock(it.source.ID)
			})
		}
	}
	a.sourceRenameButton(c, it)
	if it.video != "" {
		a.quietIconButton(c, "source-visible-"+key, a.eyeIcon(it), a.eyeTip(it), a.eyeTint(it), func() {
			a.selected = itemKey(it)
			a.toggleVideo(index)
		})
	}
}

// sourceRenameButton is the pencil each source row carries, so a person can give
// a source their own name without leaving the list.
func (a *appState) sourceRenameButton(c *ui.Context, it *item) {
	if it.source.ID == "" {
		return
	}
	a.quietIconButton(c, "sc-rename-"+string(itemKey(it)), lucide.Pencil(),
		a.tr("Rename", "重命名"), colorDim, func() {
			a.openRenameSource(it.source.ID)
		})
}

// sourceActions is the card density's control row.
func (a *appState) sourceActions(c *ui.Context, it *item, index int) {
	ui.Row(c).Gap(spaceXs).Children(func() {
		if it.source.ID != "" && ui.Button(c.Key("card-rename-"+string(itemKey(it))), a.tr("Rename", "重命名")).Height(28).Clicked() {
			a.openRenameSource(it.source.ID)
		}
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
			if ui.Button(c.Key("card-retry-"+string(itemKey(it))), a.tr("Retry", "重试")).Height(28).Clicked() {
				a.runCommand(command{Kind: commandRetry, Source: itemKey(it)})
			}
			if ui.Button(c.Key("card-reselect-"+string(itemKey(it))), a.tr("Choose address", "重新选择地址")).Height(28).Clicked() {
				a.selected = itemKey(it)
				a.reselectSource = itemKey(it)
				a.reselectText = ""
				a.reselectProblem = ""
				a.invalidate()
			}
		}
		if ui.Button(c.Key("card-remove-"+string(itemKey(it))), a.tr("Remove", "移除")).Height(28).Clicked() {
			a.unlinkStageMember(it)
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
						if !a.isStageMember(it) {
							continue
						}
						if ui.Button(c.Key("rail-src-"+string(itemKey(it))), "▢").Width(28).Height(26).
							Tooltip(sourceDisplayName(it)).Clicked() {
							a.selected = itemKey(it)
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
				label := ui.Text(c, a.sceneDisplayName(sc, i)).FontSize(textBody).
					FontWeight(500)
				if current {
					label = label.TextColor(colorSelectedText)
				} else {
					label = label.TextColor(colorTextDim)
				}
				ui.Box(c).Grow(1)
				ui.Text(c, itoa(len(sc.Video))).FontSize(textMicro).TextColor(colorDim).
					FontFeatures("tnum")
				a.iconButton(c, "scene-rename-"+string(sc.ID), lucide.Pencil(),
					a.tr("rename this scene", "重命名这个场景"), false, func() {
						a.openRenameScene(sc.ID)
					})
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
	a.sceneMenu = false
	if id == a.scene {
		return
	}
	if _, err := a.session.Scene(id); err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	if _, err := a.session.SetOutputScene(a.output, id); err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	a.invalidateAlignmentRow()
	a.advanceStageGeneration()
	a.activateSceneLayout(id)
	a.reconcileStageSelection()
	a.apply()
	a.reconcileScenePlayers(id, a.stageGeneration)
	a.invalidate()
	a.saveSession()
}

// removeScene drops a scene and stays on a scene that exists.
func (a *appState) removeScene(id core.SceneID) {
	active := id == a.scene
	if active {
		scenes := a.session.Scenes()
		var next core.SceneID
		for _, scene := range scenes {
			if scene.ID != id {
				next = scene.ID
				break
			}
		}
		if next == "" {
			a.lastErr = safeOpenError(core.ErrInvalid)
			a.invalidate()
			return
		}
		if _, err := a.session.SetOutputScene(a.output, next); err != nil {
			a.lastErr = safeOpenError(err)
			a.invalidate()
			return
		}
		a.invalidateAlignmentRow()
		a.advanceStageGeneration()
		a.activateSceneLayout(next)
		a.reconcileStageSelection()
	}
	if err := a.session.RemoveScene(id); err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	delete(a.edits, id)
	delete(a.sceneLayouts, id)
	delete(a.sceneMembers, id)
	a.forgetPendingStageEdits(id)
	if persistent := a.scenePersistentIDs[id]; persistent != "" {
		delete(a.persistentSceneIDs, persistent)
		delete(a.scenePersistentIDs, id)
	}
	delete(a.pendingBindings, id)
	for i := len(a.items) - 1; i >= 0; i-- {
		it := a.items[i]
		if it.stageOnly && !a.sourceHasStageMember(it.persistentID) {
			a.removeCatalogSource(it)
		}
	}
	a.pruneStageEdits()
	a.apply()
	if active {
		a.reconcileScenePlayers(a.scene, a.stageGeneration)
	}
	a.invalidate()
	a.saveSession()
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
	player := it.outputPlayer()
	switch {
	case it.openErr != nil:
		return a.tr("failed to open", "打开失败")
	case it.restoring:
		return a.tr("opening", "正在打开")
	case player == nil:
		return a.tr("not opened yet", "尚未打开")
	case !player.Ready():
		return a.tr("opening", "正在打开")
	case player.Buffering():
		return a.tr("buffering", "缓冲中")
	case !it.shown:
		return a.tr("off the stage", "不在舞台上")
	}
	return clock(player.Position()) + " / " + clock(player.Duration())
}

func (a *appState) showSourcesPanel() {
	delete(a.dockHidden, dockSources)
	a.dockUp = true
	a.dockTab = 0
	a.workspace = workspaceStage
	a.invalidate()
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
		room = height - a.barHeight() - a.statusBarHeight() - 44 - 12
		if a.dockUp {
			room -= a.dockHeight
		}
	}
	if room > 120 && pictureCap > room {
		pictureCap = room
	}
	if room <= 120 {
		if room < 0 {
			room = 0
		}
		if pictureCap > room {
			pictureCap = room
		}
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
				if a.firstShown() != nil {
					a.stageCorner(c)
				}
			})
			ui.Box(c).Grow(1)
		})
		ui.Box(c).Grow(1)
	})
	a.transportBar(c, available)
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
			a.tr("edit the picture's place", "编辑画面位置"), 30, editInk, a.editMode, a.toggleStageEdit)
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
		if a.winW < 900 {
			a.iconButtonSized(c, "corner-props", lucide.PanelRight(),
				a.tr("properties", "属性"), 30, colorText, false, func() {
					a.drawerOpen = !a.drawerOpen
				})
		}
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
		edit := ui.Button(c.Key("stage-edit"), a.tr("Edit", "编辑")).Height(a.controlHeight()).Padding(0, spaceSm)
		if a.editMode {
			edit = edit.Background(colorAccentSoft).TextColor(colorAccent)
		}
		if edit.Clicked() {
			a.toggleStageEdit()
		}
		if width < 900 {
			if ui.Button(c.Key("stage-props"), a.tr("Properties", "属性")).Height(a.controlHeight()).Padding(0, spaceSm).Clicked() {
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
	case layoutFree:
		return a.tr("Free", "自由布局")
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
	if slots := a.layout.slots(); slots > 0 && shown > slots {
		return a.tr("Use Free layout to place all shown sources.", "切换到「自由布局」可摆放所有已显示的画面。")
	}
	return ""
}

// transportBar is the row a person touches most often: play, mute, reload, then
// where the picture is and how long it runs, and the way into the two watching
// modes. The position is read back from the player rather than from the shared
// clock: this row is about the picture in front of you.
func (a *appState) transportBar(c *ui.Context, width float32) {
	compact := width < transportLabelWidth
	event := a.eventTime()
	first := a.firstShown()
	plan, planErr := a.stageTransportPlan()
	if planErr != nil {
		plan = transportPlan{}
	}
	playing := a.playingFor(plan)
	transportLabel := a.transportActionLabel(plan, playing)
	transportTip := a.transportActionTooltip(plan)
	if compact && len(plan.items) != 0 {
		transportTip = transportLabel + " · " + transportTip
	}
	ui.Row(c).WidthPercent(100).Height(44).Padding(0, spaceSm).Gap(spaceSm).
		Background(colorBg).Border(0, colorBg).
		AlignItems(ui.Center).Children(func() {
		ui.Row(c).Grow(1).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
				playButton := ui.ButtonBase(c.Key("transport-play")).Width(180).Height(a.controlHeight()).
					Padding(0, controlPadX).Radius(radiusMd).AlignItems(ui.Center).Shrink(0).
					AlignSelf(ui.Center).Label(transportLabel)
				if compact {
					playButton = playButton.Width(a.controlHeight()).Padding(0).Justify(ui.Center)
				}
				fill, ink := colorEnabled, colorSelectedText
				if len(plan.items) == 0 {
					fill, ink = colorPanel2, colorDim
					playButton = playButton.Disabled(true).Tooltip(transportTip)
				} else if playButton.Pressed() {
					fill, ink = colorEnabledPress, colorText
				} else if playButton.Hovered() {
					fill, ink = colorEnabledHover, colorText
				}
				if len(plan.items) != 0 {
					playButton = playButton.Tooltip(transportTip)
				}
				playButton = playButton.Background(fill).TextColor(ink)
				playIcon := lucide.Play()
				if playing {
					playIcon = lucide.Pause()
				}
				playButton.Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, playIcon).Size(14, 14).TextColor(ink)
						if !compact {
							ui.Text(c, transportLabel).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
						}
					})
				})
				if playButton.Clicked() {
					if playing {
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
				a.ghostIconButton(c, "transport-mute", mute, muteIcon, 0, compact, func() {
					a.runCommand(command{Kind: commandMasterMute})
				})
				a.ghostIconButton(c, "transport-reload", a.tr("Reload", "重载"), lucide.RotateCcw(), 0, compact, func() {
					if first != nil {
						a.runCommand(command{Kind: commandReload, Source: itemKey(first)})
					}
				})
				ui.Box(c).Width(1).Height(20).Background(colorLineSoft)
				if first != nil {
					name := sourceDisplayName(first)
					ui.Text(c, name).FontSize(textCaption).TextColor(colorTextDim).
						MinWidth(0).MaxWidth(220).SingleLine().Tooltip(name)
				}
				// The event clock only means something once the shared timeline
				// has been started. A window opened straight onto a file has not
				// started one, and printing 0.0s beside a player at 0:14 would
				// read as a stuck clock rather than as "there is no timeline yet".
				if event > 0 {
					label := formatEvent(event)
					ui.Text(c, label).FontSize(textBody).TextColor(colorText).
						FontFeatures("tnum").MinWidth(0).SingleLine().Tooltip(label)
				}
				if first != nil {
					total := first.player.Duration()
					ui.Text(c.Key("transport-position"), clock(first.player.Position())+" / "+clock(total)).
						FontSize(textBody).TextColor(colorTextDim).FontFeatures("tnum").
						NoWrap().Shrink(0).Padding(spaceSm, 0)
				}
			})
			// One way in to watching. Cinema is the windowed form; the
			// overlay inside it already offers full screen, so the workbench
			// does not need two buttons that both mean "stop editing".
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Box(c).Width(1).Height(20).Background(colorLineSoft)
				a.ghostIconButton(c, "transport-cinema", a.tr("Watch", "沉浸观看"), lucide.MonitorPlay(), 104, compact, func() {
					a.enterViewing(viewCinema, a.windowRef())
				})
			})
		})
	})
}

// propertiesTabs are the three things a source has: a picture, a sound and a
// place on the timeline.
func (a *appState) propertiesTabs(c *ui.Context) {
	labels := [3]string{a.tr("Picture", "画面"), a.tr("Sound", "音频"), a.tr("Time", "时间")}
	track := a.segmentTrack(c)
	track.WidthPercent(100).Children(func() {
		for i, label := range labels {
			idx := i
			a.segmentedOption(c, "props-tab-"+itoa(idx), label, a.propsTab == idx, true, func() {
				a.propsTab = idx
			})
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
		if ui.Button(c.Key("pic-show"), a.tr("Show picture", "放上舞台")).Height(30).Clicked() {
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
			a.unlinkStageMember(it)
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
				mute = a.tr("Unmute all", "取消全部静音")
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
	ui.Column(c).WidthPercent(100).Padding(spaceXs, spaceSm).Radius(radiusMd).
		Gap(spaceXs).Background(colorRow).Border(1, colorRowEdge).Children(func() {
		ui.Row(c).WidthPercent(100).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(6).Height(6).Radius(3).Background(colorSound)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, sourceDisplayName(it)).FontSize(textSmall).FontWeight(400).
					TextColor(colorText).SingleLine()
				ui.Text(c, a.mixerChannelRouteLabel(it)).FontSize(textMicro).TextColor(colorTextDim).
					SingleLine().Tooltip(a.localizedMixerLine(it))
			})
			if it.audio == "" {
				return
			}
			a.mixerActions(c, it, "mix")
		})
		if it.audio == "" {
			return
		}
		a.mixerLevelControl(c, it, "mix-vol-", 118, 42)
	})
}

func (a *appState) mixerActions(c *ui.Context, it *item, keyPrefix string) {
	if it == nil || it.audio == "" {
		return
	}
	muted := a.sourceMuted(it)
	icon, tint := lucide.Volume2(), colorTextDim
	if muted {
		icon, tint = lucide.VolumeX(), colorDim
	}
	a.quietIconButton(c, keyPrefix+"-mute-"+string(it.source.ID), icon,
		a.muteLabel(it), tint, func() {
			a.runCommand(command{Kind: commandMute, Source: it.source.ID})
		})
	soloTint := colorTextDim
	soloTip := a.tr("Solo", "独听")
	if a.solo != "" && a.solo == it.source.ID {
		soloTint = colorAccent
		soloTip = a.tr("Stop solo", "取消独听")
	}
	routed := a.routed(it)
	if !routed {
		soloTip = a.tr("Add this source to the mix before soloing", "接入混音后才能独听")
	}
	solo := ui.ButtonBase(c.Key(keyPrefix + "-solo-" + string(it.source.ID))).
		Width(28).Height(28).Radius(radiusMd).Padding(0).Tooltip(soloTip)
	if !routed {
		solo = solo.Disabled(true)
	}
	solo = solo.Background(stateFill(solo.Hovered(), solo.Pressed(), false)).TextColor(soloTint)
	solo.Children(func() { ui.Icon(c, lucide.Headphones()).Size(16, 16) })
	if solo.Clicked() {
		a.runCommand(command{Kind: commandSolo, Source: it.source.ID})
	}
	a.quietIconButton(c, keyPrefix+"-route-"+string(it.source.ID), lucide.ListMusic(),
		a.tr("Use only this source for stage audio", "舞台只保留这路声音"),
		colorTextDim, func() { a.runCommand(command{Kind: commandHear, Source: it.source.ID}) })
}

func (a *appState) mixerLevel(it *item) float64 {
	if level, ok := a.sceneLevel(it); ok {
		return level.Volume
	}
	return it.volume
}

func (a *appState) mixerLevelControl(c *ui.Context, it *item, keyPrefix string, width, readoutWidth float32) {
	if it == nil || it.audio == "" {
		return
	}
	level := a.mixerLevel(it)
	ui.Row(c).WidthPercent(100).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Grow(1)
		if a.volumeSlider(c, keyPrefix+string(it.source.ID), &level, width) {
			a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: level})
		}
		a.numericReadout(c, fmt.Sprintf("%.0f%%", level*100), readoutWidth)
	})
}

func (a *appState) mixerChannelRouteLabel(it *item) string {
	label := a.audioRouteLabel(it)
	if solo := a.mixerChannelSoloLabel(it); solo != "" {
		label += " · " + solo
	}
	if reasons := a.silences(it); len(reasons) > 0 && reasons[0] != "not in the scene" {
		label += " · " + a.shortMixerReason(reasons[0])
	}
	return label
}

func (a *appState) shortMixerReason(reason string) string {
	switch reason {
	case "its player did not open":
		return a.tr("Could not open media", "媒体打开失败")
	case "muted":
		return a.tr("source muted", "本路静音")
	case "the scene's own mute":
		return a.tr("scene muted", "场景静音")
	case "master mute":
		return a.tr("master muted", "总静音")
	case "another source is soloed":
		return a.tr("another source soloed", "另一来源独听")
	case "the unaligned protection":
		return a.tr("unaligned", "尚未对齐")
	case "volume is zero":
		return a.tr("zero volume", "音量为零")
	default:
		return a.localizedSilenceReason(reason)
	}
}

func (a *appState) mixerChannelSoloLabel(it *item) string {
	if it != nil && a.solo != "" && a.solo == it.source.ID {
		return a.tr("Solo active", "独听中")
	}
	return ""
}

// mixerStrip is the narrow window's mixer: a bottom bar that can open into a few
// channel rows, so a 747px window still has a level control.
func (a *appState) mixerStrip(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Background(colorBgSoft).Children(func() {
		ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
		ui.Row(c).WidthPercent(100).Height(mixerStripHi).Padding(0, spaceMd).Gap(spaceSm).
			AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Mixer", "混音")).FontSize(textMicro).TextColor(colorDim)
			mute := a.tr("Mute all", "全部静音")
			if a.masterMute {
				mute = a.tr("Unmute all", "取消全部静音")
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
					ui.Text(c, sourceDisplayName(it)).FontSize(textMicro).TextColor(colorText).Width(90).SingleLine()
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
		if saveErr := a.sessionSaveError(); saveErr != "" {
			ui.Text(c, a.tr("session save failed", "会话保存失败")).
				FontSize(textMicro).TextColor(colorBad).SingleLine().Tooltip(saveErr)
		}
		if a.lastErr != nil {
			if ui.Button(c.Key("status-error"), "!").Width(22).Height(18).
				Border(1, colorBad).TextColor(colorBad).
				Tooltip(a.tr("open the error report", "打开错误报告")).Clicked() {
				a.errorOpen = true
			}
		}
		if a.importing != "" {
			ui.Text(c, a.tr("reading ", "正在读取 ")+stripSecrets(a.importing)).FontSize(textMicro).
				TextColor(colorWarn).SingleLine()
			if ui.Button(c.Key("status-cancel"), a.tr("Cancel", "取消")).Height(18).
				Padding(0, spaceXs).FontSize(textMicro).Clicked() {
				a.runCommand(command{Kind: commandCancelImport})
			}
		}
		if a.startupRestoreCancel != nil && ui.Button(c.Key("restore-cancel"), a.tr("Cancel recovery", "取消恢复")).Height(18).
			Padding(0, spaceXs).FontSize(textMicro).Clicked() {
			a.runCommand(command{Kind: commandCancelRestore})
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
				a.secondaryButton(c, "err-diagnostics", a.tr("Open diagnostics", "打开诊断"), 0, true, func() {
					a.navigateWorkspace(workspaceDiagnostics)
					a.errorOpen = false
				})
				if a.lastErr != nil && a.importFailureErr != nil &&
					a.lastErr.Error() == a.importFailureErr.Error() && a.importRetryTarget() != "" {
					a.secondaryButton(c, "err-retry", a.tr("Retry", "重试"), 0, true, func() {
						a.retryImport()
						a.errorOpen = false
					})
				}
				ui.Box(c).Grow(1)
				a.primaryButton(c, "err-close", a.tr("Close", "关闭"), 0, true, func() {
					a.errorOpen = false
				})
			})
		})
	})
}

// errorShort is the sentence a person reads first: what failed, in one line,
// without the URL and without the Go error text.
func (a *appState) errorShort() string {
	if a.lastErr != nil && a.importFailureErr != nil && a.importFailureURI != "" && a.lastErr.Error() == a.importFailureErr.Error() {
		return a.importFailureCause(a.importFailureErr)
	}
	if a.lastErr == nil {
		return ""
	}
	msg := a.tr("The last import did not finish.", "上一次导入没有完成。")
	if a.lastCommand.Command.Kind == commandAdd && a.lastCommand.Err != nil {
		msg = a.importFailureCause(a.lastCommand.Err)
	} else {
		msg = redactSecrets(a.lastErr.Error())
	}
	return msg
}

// importFailureCause gives the report a short, localized cause. The complete
// redacted error remains visible below it for diagnosis.
func (a *appState) importFailureCause(err error) string {
	if err == nil {
		return a.tr("The source could not be opened.", "无法打开此来源。")
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "404 not found"), strings.Contains(text, "not found"), strings.Contains(text, "no such file"):
		return a.tr("The file or address was not found.", "找不到此文件或地址。")
	case strings.Contains(text, "unsupported"), strings.Contains(text, "not supported"):
		return a.tr("This media format is not supported.", "不支持此媒体格式。")
	case strings.Contains(text, "timeout"), strings.Contains(text, "connection"), strings.Contains(text, "network"):
		return a.tr("The source could not be reached.", "无法连接到此来源。")
	case strings.Contains(text, "invalid"), strings.Contains(text, "malformed"):
		return a.tr("The address or media is invalid.", "地址或媒体无效。")
	default:
		return a.tr("The source could not be opened.", "无法打开此来源。")
	}
}

// pageMenuPanel is the page list and the way out of the window, in one place.
func (a *appState) pageMenuPanel(c *ui.Context, width float32) {
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(19).
		OnClick(func() { a.pageMenu = false })
	ui.Column(c).Absolute().Top(a.barHeight()+4).Right(12).Width(220).Padding(4).Gap(2).
		Radius(radiusCard).Background(colorPanel2).Border(1, colorLineSoft).ZIndex(20).
		Shadow(0, 8, 24, 0, shadowFloat).Children(func() {
		for _, page := range []workspace{workspaceStage, workspaceDiagnostics, workspaceSettings} {
			p := page
			button := ui.Button(c.Key("menu-"+itoa(int(p))), a.pageLabel(p)).Height(a.controlHeight()).Padding(0, spaceSm)
			if a.workspacePageActive(p) {
				button = button.Background(colorSelected).TextColor(colorSelectedText)
			}
			if button.Clicked() {
				if p == workspaceSettings {
					a.navigateToSettings("")
				} else {
					a.navigateWorkspace(p)
				}
			}
		}
		ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
		if ui.Button(c.Key("menu-quit"), a.tr("Quit MuxShift", "退出 MuxShift")).Height(a.controlHeight()).
			Padding(0, spaceSm).TextColor(colorBad).Clicked() {
			a.pageMenu = false
			a.window.Update(a.window.Close)
		}
	})
}

// sceneMenuPanel gives a narrow window access to the scene list.
func (a *appState) sceneMenuPanel(c *ui.Context, width float32) {
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(19).
		OnClick(func() { a.sceneMenu = false })
	ui.Column(c).Absolute().Top(a.barHeight()+4).Left(spaceMd).Width(290).MaxWidth(width-2*spaceMd).
		Padding(4).Gap(2).Radius(radiusCard).Background(colorPanel2).Border(1, colorLineSoft).
		ZIndex(20).Shadow(0, 8, 24, 0, shadowFloat).Children(func() {
		ui.Scroll(c).WidthPercent(100).MaxHeight(320).Children(func() {
			a.sceneList(c)
		})
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
