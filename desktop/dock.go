package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// The console under the picture is a set of panels that sit side by side, the
// way a capture program arranges its docks: sources, the mixer and the sync
// controls are visible at the same time instead of hiding behind tabs.
const (
	dockSources = "sources"
	dockMixer   = "mixer"
	dockSync    = "sync"
)

// dockOrder is the left-to-right order of the panels.
var dockOrder = []string{dockSources, dockMixer, dockSync}

func (a *appState) dockTitle(id string) string {
	switch id {
	case dockSources:
		return a.tr("Sources", "来源")
	case dockMixer:
		return a.tr("Mixer", "混音")
	default:
		return a.tr("Sync", "同步")
	}
}

func (a *appState) dockShown(id string) bool {
	return a.dockHidden == nil || !a.dockHidden[id]
}

// narrowDock says whether the console has to fold into tabs. The width is the
// one the shell last drew at, so a resize is picked up on the next frame.
func (a *appState) narrowDock() bool {
	return a.winW > 0 && a.winW < 900
}

func (a *appState) toggleDockPanel(id string) {
	if a.dockHidden == nil {
		a.dockHidden = make(map[string]bool)
	}
	a.dockHidden[id] = !a.dockHidden[id]
	if !a.dockHidden[id] {
		a.dockUp = true
	}
	a.invalidate()
}

// dockArea draws the console. Its height is dragged at the top edge; the whole
// thing folds into a bar when the picture needs the room.
// eyeIcon and eyeTip are the one eye the window draws: dock rows, the compact
// source rows and the row layout all ask for the same pair.
func (a *appState) eyeIcon(it *item) *ui.SVG {
	if it.shown {
		return lucide.Eye()
	}
	return lucide.EyeOff()
}

func (a *appState) eyeTip(it *item) string {
	if it.shown {
		return a.tr("take it off the stage", "从舞台上取下")
	}
	return a.tr("put it on the stage", "放到舞台上")
}

func (a *appState) eyeTint(it *item) ui.Color {
	if it.shown {
		return colorTextDim
	}
	return colorDim
}

// rowFacts is the quiet second line of a console row: what the file is and how
// far it has got. The numbers the engine does not know are left out instead of
// being guessed.
func (a *appState) rowFacts(it *item) string {
	parts := make([]string, 0, 4)
	parts = append(parts, a.audioRouteLabel(it))
	if a.sourceMuted(it) {
		parts = append(parts, a.tr("source muted", "本路静音"))
	} else {
		parts = append(parts, a.tr("source unmuted", "本路未静音"))
	}
	if facts := a.sourceFacts(it); facts != "" {
		parts = append(parts, facts)
	}
	if p := rawPlayer(it.player); p != nil && p.Ready() {
		parts = append(parts, formatEvent(p.Position())+" / "+formatEvent(p.Duration()))
	}
	return strings.Join(parts, " · ")
}

func (a *appState) dockArea(c *ui.Context) {
	if a.dockHidden == nil {
		a.dockHidden = make(map[string]bool)
	}
	layout.DraggablePanel(c, &a.dockHeight, &a.dockUp, layout.DraggablePanelProps{
		Placement:         layout.Bottom,
		MinSize:           140,
		MaxSize:           520,
		CollapseThreshold: 110,
	}, func() {
		a.dockBody(c)
	})
}

func (a *appState) dockBody(c *ui.Context) {
	ui.Column(c).Fill().WidthPercent(100).Background(colorBg).Children(func() {
		if normalizeConsoleLayout(a.settings.ConsoleLayout) == consoleRows {
			// The row layout is the setting's alternative to the panels: the
			// same sources with their level, stack place and offset on one line
			// each, instead of three separate panels.
			ui.Row(c).WidthPercent(100).Height(30).Padding(spaceSm, spaceXs).Gap(spaceXs).
				AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Console", "控制台")).FontSize(textMicro).TextColor(colorDim)
				ui.Box(c).Grow(1)
				if ui.Button(c.Key("dock-fold"), "▾").Width(24).Height(22).
					Tooltip(a.tr("fold the console", "收起控制台")).Clicked() {
					a.dockUp = false
					a.invalidate()
				}
			})
			ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
			ui.Scroll(c).WidthPercent(100).Grow(1).Children(func() { a.dockRowsBody(c) })
			return
		}
		if a.narrowDock() {
			// A narrow console is tabs and nothing else: one row of labels with
			// the fold button on its right, then the panel those tabs chose.
			ids := make([]string, 0, len(dockOrder))
			labels := make([]string, 0, len(dockOrder))
			for _, id := range dockOrder {
				if !a.dockShown(id) {
					continue
				}
				ids = append(ids, id)
				labels = append(labels, a.dockTitle(id))
			}
			ui.Row(c).WidthPercent(100).Height(34).Padding(spaceSm, spaceXs).Gap(spaceXs).
				AlignItems(ui.Center).Children(func() {
				if len(ids) == 0 {
					ui.Text(c, a.tr("Every panel is hidden.", "所有面板都被隐藏了。")).
						FontSize(textCaption).TextColor(colorDim)
					ui.Box(c).Grow(1)
				} else {
					if a.dockTab < 0 || a.dockTab >= len(ids) {
						a.dockTab = 0
					}
					track := a.segmentTrack(c)
					track.Children(func() {
						for i, label := range labels {
							idx := i
							a.segmentedOption(c, "dock-tab-"+ids[idx], label, a.dockTab == idx, false, func() {
								a.dockTab = idx
							})
						}
					})
					ui.Box(c).Grow(1)
				}
				if ui.Button(c.Key("dock-fold"), "▾").Width(24).Height(22).
					Tooltip(a.tr("fold the console", "收起控制台")).Clicked() {
					a.dockUp = false
					a.invalidate()
				}
			})
			if len(ids) == 0 {
				return
			}
			ui.Row(c).WidthPercent(100).Grow(1).AlignItems(ui.Stretch).Children(func() {
				a.dockPanel(c, ids[a.dockTab])
			})
			return
		}
		ui.Row(c).WidthPercent(100).Height(30).Padding(0, spaceSm).Gap(spaceXs).
			AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Console", "控制台")).FontSize(textMicro).TextColor(colorDim)
			ui.Box(c).Grow(1)
			// In a narrow window the tabs take this row's place below, so only
			// the fold button stays here.
			// Panel switches are chip-shaped and quiet: they sit on the same
			// surface as the row, and only a shown panel carries the accent.
			if !a.narrowDock() {
				track := a.segmentTrack(c)
				track.Children(func() {
					for _, id := range dockOrder {
						pid := id
						a.segmentedOption(c, "dock-toggle-"+pid, a.dockTitle(pid), a.dockShown(pid), false, func() {
							a.toggleDockPanel(pid)
						})
					}
				})
			}
			a.iconButtonSized(c, "dock-fold", lucide.ChevronDown(),
				a.tr("fold the console", "收起控制台"), 24, colorTextDim, false, func() {
					a.dockUp = false
					a.invalidate()
				})
		})
		ui.Box(c).WidthPercent(100).Height(1).Background(colorLine)
		if a.narrowDock() {
			// In a narrow window three columns become unreadable, so the same
			// panels turn into tabs: one at a time, same body functions.
			ids := make([]string, 0, len(dockOrder))
			labels := make([]string, 0, len(dockOrder))
			for _, id := range dockOrder {
				if !a.dockShown(id) {
					continue
				}
				ids = append(ids, id)
				labels = append(labels, a.dockTitle(id))
			}
			if len(ids) == 0 {
				ui.Text(c, a.tr("Every panel is hidden.", "所有面板都被隐藏了。")).FontSize(textCaption).
					TextColor(colorDim).Padding(spaceSm, spaceSm)
				return
			}
			if a.dockTab < 0 || a.dockTab >= len(ids) {
				a.dockTab = 0
			}
			ui.Tabs(c, &a.dockTab, labels...)
			ui.Row(c).WidthPercent(100).Grow(1).AlignItems(ui.Stretch).Children(func() {
				a.dockPanel(c, ids[a.dockTab])
			})
			return
		}
		row := ui.Row(c).WidthPercent(100).Grow(1).AlignItems(ui.Stretch)
		rowWidth := row.Bounds().W
		if rowWidth > 0 {
			a.dockW = rowWidth
		}
		row.Children(func() {
			shown := make([]string, 0, len(dockOrder))
			for _, id := range dockOrder {
				if a.dockShown(id) {
					shown = append(shown, id)
				}
			}
			a.normalizeDockWidths(shown)
			first := true
			for i, id := range shown {
				if !first {
					a.dockSplitter(c, shown[i-1], id)
				}
				first = false
				a.dockPanel(c, id)
			}
		})
	})
}

// dockPanel is one panel: a sticky title and a body that scrolls. Panels keep
// the share the person gave them by dragging the divider beside them.
func (a *appState) dockPanel(c *ui.Context, id string) {
	share := a.dockWidths[id]
	if share <= 0 {
		share = 1 / float32(len(dockOrder))
	}
	// In a narrow window the tabs show one panel, so it takes the whole width:
	// keeping its third-panel share would leave two thirds of the console as a
	// dark empty strip.
	widthPercent := share * 100
	if a.narrowDock() {
		widthPercent = 100
	}
	ui.Box(c).WidthPercent(widthPercent).MinWidth(120).HeightPercent(100).Padding(spaceSm, spaceSm).
		Children(func() {
			ui.Column(c).WidthPercent(100).Grow(1).Padding(spaceMd, spaceMd, spaceSm, spaceMd).Gap(spaceXs).
				Radius(radiusCard).Background(colorPanel).Border(1, colorLineSoft).
				Shadow(0, 2, 8, 0, shadowCard).Children(func() {
				ui.Row(c).WidthPercent(100).Height(22).Gap(spaceXs).
					AlignItems(ui.Center).Children(func() {
					a.sectionTitle(c, a.dockTitle(id))
					ui.Box(c).Grow(1)
				})
				ui.Box(c).WidthPercent(100).Height(1).Background(colorLineSoft)
				ui.Scroll(c).WidthPercent(100).Grow(1).Children(func() {
					ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
						switch id {
						case dockSources:
							a.dockSourcesBody(c)
						case dockMixer:
							a.dockMixerBody(c)
						default:
							a.dockSyncBody(c)
						}
					})
				})
			})
		})
}

// normalizeDockWidths gives every shown panel a share and rescales the shares
// so they fill the console after a panel was hidden or shown.
func (a *appState) normalizeDockWidths(ids []string) {
	if a.dockWidths == nil {
		a.dockWidths = make(map[string]float32)
	}
	var total float32
	for _, id := range ids {
		total += a.dockWidths[id]
	}
	if total <= 0 {
		for _, id := range ids {
			a.dockWidths[id] = 1 / float32(len(ids))
		}
		return
	}
	for _, id := range ids {
		a.dockWidths[id] /= total
	}
}

// dockSplitter is the 8px seam between two panels. Dragging it moves width from
// one neighbour to the other; both keep at least 12% so neither can vanish.
func (a *appState) dockSplitter(c *ui.Context, left, right string) {
	ui.Box(c).Width(8).HeightPercent(100).Children(func() {
		seam := ui.Box(c).WidthPercent(100).HeightPercent(100).Background(colorBg).
			Tooltip(a.tr("drag to give one side more room", "拖动以调整两侧宽度"))
		key := left + "|" + right
		if dx, _, dragging := seam.Dragged(); dragging && a.dockW > 0 {
			// Dragged reports the whole travel since the press, so only the part
			// that is new this frame moves the panels. Without this the widths
			// would keep running after the pointer stopped.
			if a.dockSeam == nil {
				a.dockSeam = make(map[string]float32)
			}
			step := dx - a.dockSeam[key]
			a.dockSeam[key] = dx
			delta := step / a.dockW
			const floor float32 = 0.12
			if delta != 0 && a.dockWidths[left]-delta >= floor && a.dockWidths[right]+delta >= floor {
				a.dockWidths[left] -= delta
				a.dockWidths[right] += delta
			}
		} else if !dragging {
			delete(a.dockSeam, key)
		}
	})
}

func (a *appState) dockSourcesBody(c *ui.Context) {
	placed := make(map[*item]bool)
	for _, it := range a.stagePictures() {
		placed[it] = true
	}
	ui.Row(c).WidthPercent(100).Height(28).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		a.iconButton(c, "dock-src-add", lucide.Plus(), a.tr("add a source", "添加来源"), false,
			a.openAddSource)
		a.iconButton(c, "dock-src-remove", lucide.Minus(), a.tr("remove from this stage", "从当前舞台移除"), false, func() {
			if it := a.find(a.selected); it != nil {
				a.unlinkStageMember(it)
			}
		})
		a.iconButton(c, "dock-src-up", lucide.ChevronUp(), a.tr("toward the front", "往前一层"), false, func() {
			if it := a.find(a.selected); it != nil {
				a.raiseStage(it.source.ID, false)
			}
		})
		a.iconButton(c, "dock-src-down", lucide.ChevronDown(), a.tr("toward the back", "往后一层"), false, func() {
			if it := a.find(a.selected); it != nil {
				a.lowerStage(it.source.ID, false)
			}
		})
		ui.Box(c).Grow(1)
		if it := a.find(a.selected); it != nil {
			ui.Text(c, a.layerNote(it)).FontSize(textMicro).TextColor(colorDim).SingleLine()
		}
	})
	a.importFailureNotice(c)
	a.stageCatalog(c)
	memberCount := 0
	for i, it := range a.items {
		if !a.isStageMember(it) {
			continue
		}
		memberCount++
		row := it
		ui.Row(c).WidthPercent(100).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			a.sourceEntry(c, row, i, true, placed)
			a.iconButton(c, "dock-unlink-"+string(itemKey(row)), lucide.X(),
				a.tr("remove from this stage", "从当前舞台移除"), false, func() {
					a.unlinkStageMember(row)
				})
		})
	}
	if memberCount == 0 {
		message := a.tr("No sources yet.", "还没有来源。")
		if len(a.items) > 0 {
			message = a.tr("No sources on this stage.", "当前舞台没有来源。")
		}
		ui.Text(c, message).FontSize(textCaption).TextColor(colorDim).Wrap()
	}
}

// stageCatalog exposes session sources that are not linked to the active
// stage. Adding one links that shared source; it never creates another player.
func (a *appState) stageCatalog(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
		label := a.tr("Shared source catalog", "共享来源目录")
		if a.stageCatalogOpen {
			label = a.tr("Hide source catalog", "收起来源目录")
		}
		if ui.Button(c.Key("stage-catalog-toggle"), label).Height(26).Clicked() {
			a.stageCatalogOpen = !a.stageCatalogOpen
		}
		if !a.stageCatalogOpen {
			return
		}
		ui.Text(c, a.tr("Unlinking keeps the source and player here. Delete it only from this catalog.", "从舞台移除会保留目录和播放器；仅在此目录中彻底删除。")).
			FontSize(textMicro).TextColor(colorDim).Wrap()
		for _, it := range a.items {
			if a.isStageMember(it) {
				continue
			}
			row := it
			ui.Row(c).WidthPercent(100).Height(28).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
				ui.Text(c, sourceDisplayName(row)).FontSize(textSmall).TextColor(colorText).Grow(1).MinWidth(0).SingleLine()
				if ui.Button(c.Key("stage-link-"+string(itemKey(row))), a.tr("Add Existing", "添加现有来源")).Height(24).Clicked() {
					a.linkStageMember(row)
					a.stageCatalogOpen = false
				}
				if !a.sourceHasStageMember(row.persistentID) && ui.Button(c.Key("catalog-delete-"+string(itemKey(row))), a.tr("Delete", "删除")).Height(24).Clicked() {
					a.removeCatalogSource(row)
				}
			})
		}
	})
}

// syncBadge says, in two words, whether the source shares a clock with the
// reference. The full sentence belongs in the inspector: a dock column is too
// narrow to carry it, and a clipped sentence reads as a layout bug.
func (a *appState) syncAligned(it *item) bool {
	state := a.alignedState(a.entryOf(it))
	return !strings.Contains(state, a.tr("not yet", "还没有")) && !strings.Contains(state, "no common clock")
}

func (a *appState) syncBadge(it *item) string {
	if !a.syncAligned(it) {
		return a.tr("no clock", "未对齐")
	}
	return a.tr("aligned", "已对齐")
}

// layerNote names where the selected source sits in the stack, so the arrows
// have a visible effect even when two pictures do not overlap.
func (a *appState) layerNote(it *item) string {
	order := a.sceneEdits().ordered(a.shownIDs())
	for i, id := range order {
		if id == it.source.ID {
			return fmt.Sprintf("%d/%d", i+1, len(order))
		}
	}
	return ""
}

const (
	consolePanels = "panels"
	consoleRows   = "rows"
)

// normalizeConsoleLayout reads the stored choice, treating anything unknown as
// the three-panel default.
func normalizeConsoleLayout(v string) string {
	if v == consoleRows {
		return consoleRows
	}
	return consolePanels
}

// dockRowsBody is the same console as one row per source: the row carries the
// name, the level, the mute, its place in the stack and its offset. It reads
// exactly the state the panels read; only the arrangement differs.
func (a *appState) dockRowsBody(c *ui.Context) {
	a.importFailureNotice(c)
	placed := make(map[*item]bool)
	for _, picture := range a.stagePictures() {
		placed[picture] = true
	}
	ui.Column(c).Fill().WidthPercent(100).Padding(spaceSm, spaceSm).Gap(spaceXs).Children(func() {
		ui.Row(c).WidthPercent(100).Height(22).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Source", "来源")).FontSize(textMicro).TextColor(colorDim).Grow(1).MinWidth(0)
			ui.Text(c, a.tr("Level", "音量")).FontSize(textMicro).TextColor(colorDim).Width(220)
			ui.Text(c, a.tr("Offset", "偏移")).FontSize(textMicro).TextColor(colorDim).Width(190)
		})
		for i, it := range a.items {
			if !a.isStageMember(it) {
				continue
			}
			row, index := it, i
			ui.Box(c).WidthPercent(100).Height(48).Radius(radiusCard).Background(colorPanel).
				Border(1, colorLineSoft).Padding(spaceXs, spaceSm).Children(func() {
				ui.Row(c).WidthPercent(100).HeightPercent(100).Gap(spaceXs).
					AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(6).Height(6).Radius(3).Background(a.sourceDot(row))
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, sourceDisplayName(row)).FontSize(textBody).FontWeight(500).
							TextColor(colorText).MinWidth(0).SingleLine()
						state := "hidden"
						if row.shown {
							state = "shown"
							if !placed[row] {
								state = "shown · outside layout"
							}
						}
						if row.player == nil {
							state += " · opening"
						} else if row.player.Ready() {
							state += " · ready"
						} else {
							state += " · not ready"
						}
						if row.player != nil && row.player.Playing() {
							state += " · playing"
						} else {
							state += " · paused"
						}
						status := a.mixerChannelRouteLabel(row)
						if status != "" {
							status += " · "
						}
						status += state
						ui.Text(c, status).FontSize(textMicro).TextColor(colorDim).
							MinWidth(0).SingleLine().Tooltip(a.localizedMixerLine(row))
					})
					key := string(itemKey(row))
					visibleLabel := a.tr("Hide", "隐藏")
					if !row.shown {
						visibleLabel = a.tr("Show", "显示")
					}
					if ui.Button(c.Key("source-visible-"+key), visibleLabel).Height(26).Clicked() {
						a.toggleVideo(index)
					}
					if row.shown && ui.Button(c.Key("source-primary-"+key), a.tr("Primary", "主画面")).Height(26).Clicked() {
						a.runCommand(command{Kind: commandPrimary, Source: itemKey(row)})
					}
					if row.player != nil {
						kind, label := commandSourcePause, a.tr("Pause", "暂停")
						if !row.player.Playing() {
							kind, label = commandSourcePlay, a.tr("Play", "播放")
						}
						playButton := ui.Button(c.Key("source-play-"+key), label).Height(26)
						if a.sourceInExplicitGroup(row) {
							playButton = playButton.Disabled(true).Tooltip(a.tr("Use the sync group transport", "请使用同步组传输控制"))
						}
						if playButton.Clicked() {
							a.runCommand(command{Kind: kind, Source: itemKey(row)})
						}
					} else {
						ui.Button(c.Key("source-play-"+key), a.tr("Opening", "打开中")).Height(26).Disabled(true)
					}
					a.iconButton(c, "row-up-"+string(row.source.ID), lucide.ChevronUp(),
						a.tr("toward the front", "往前一层"), false, func() {
							a.raiseStage(row.source.ID, false)
						})
					a.iconButton(c, "row-down-"+string(row.source.ID), lucide.ChevronDown(),
						a.tr("toward the back", "往后一层"), false, func() {
							a.lowerStage(row.source.ID, false)
						})
					if row.audio != "" {
						ui.Row(c).Width(220).Gap(2).AlignItems(ui.Center).Children(func() {
							a.mixerActions(c, row, "row-mix")
							level := a.mixerLevel(row)
							if a.volumeSlider(c, "row-vol-"+string(row.source.ID), &level, 72) {
								a.runCommand(command{Kind: commandVolume, Source: row.source.ID, Volume: level})
							}
							a.numericReadout(c, fmt.Sprintf("%.0f%%", level*100), 38)
						})
					}
					ui.Row(c).Width(190).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
						a.offsetStepper(c, "row-step-"+string(row.source.ID), a.entryOf(row).Offset,
							a.settingsNudge(1), func(delta time.Duration) {
								a.runCommand(command{Kind: commandNudge, Source: row.source.ID, Delta: delta})
							})
						if offset := a.entryOf(row).Offset; offset != 0 {
							a.iconButton(c, "row-reset-"+string(row.source.ID), lucide.RotateCcw(),
								a.tr("set this offset back to zero", "把这一路的偏移复位"), false, func() {
									a.report(a.runCommand(command{Kind: commandNudge, Source: row.source.ID, Delta: -offset}))
								})
						}
					})
					a.quietIconButton(c, "row-rename-"+string(row.source.ID), lucide.Pencil(),
						a.tr("rename this source", "重命名这个来源"), colorDim, func() {
							a.openRenameSource(row.source.ID)
						})
					a.quietIconButton(c, "row-show-"+string(row.source.ID), a.eyeIcon(row),
						a.eyeTip(row), a.eyeTint(row), func() {
							a.toggleVideo(index)
						})
					a.iconButton(c, "row-unlink-"+string(itemKey(row)), lucide.X(),
						a.tr("remove from this stage", "从当前舞台移除"), false, func() {
							a.unlinkStageMember(row)
						})
				})
			})
		}
		if len(a.items) == 0 {
			ui.Text(c, a.tr("Nothing here yet. Add a file or an address with +.",
				"这里还是空的。用 ＋ 添加文件或地址。")).
				FontSize(textCaption).TextColor(colorDim).Padding(spaceSm, spaceSm)
		}
		a.stageCatalog(c)
	})
}

func (a *appState) dockMixerBody(c *ui.Context) {
	ui.Row(c).WidthPercent(100).Height(28).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		mute := a.tr("Mute all", "全部静音")
		if a.masterMute {
			mute = a.tr("Unmute all", "取消全部静音")
		}
		master := ui.ButtonBase(c.Key("dock-master")).Height(24).Padding(0, spaceSm).
			Radius(radiusSm).AlignItems(ui.Center)
		fill, ink := ui.Transparent, colorTextDim
		if a.masterMute {
			ink = colorWarn
		}
		if master.Pressed() {
			fill, ink = colorPressed, colorText
		} else if master.Hovered() {
			fill, ink = colorHover, colorText
		}
		master = master.Background(fill).TextColor(ink)
		master.Children(func() {
			ui.Text(c, mute).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
		})
		if master.Clicked() {
			a.runCommand(command{Kind: commandMasterMute})
		}
	})
	memberCount := 0
	for _, it := range a.items {
		if a.isStageMember(it) {
			memberCount++
		}
	}
	if memberCount == 0 {
		ui.Text(c, a.tr("Nothing to mix yet.", "还没有可调的声音。")).FontSize(textCaption).
			TextColor(colorDim).Wrap()
		return
	}
	for _, it := range a.items {
		if a.isStageMember(it) {
			a.mixerChannel(c, it)
		}
	}
}

// syncReference is the source the others are measured against: the one picked
// in this panel, or the first picture on the stage when nobody picked one.
func (a *appState) syncReference() *item {
	if it := a.find(a.syncRef); it != nil && a.isStageMember(it) {
		return it
	}
	return a.firstShown()
}

func (a *appState) dockSyncBody(c *ui.Context) {
	memberCount := 0
	for _, it := range a.items {
		if a.isStageMember(it) {
			memberCount++
		}
	}
	if memberCount == 0 {
		ui.Text(c, a.tr("Nothing to line up yet.", "还没有可对齐的来源。")).FontSize(textCaption).
			TextColor(colorDim).Wrap()
		return
	}
	reference := a.syncReference()
	a.rowSurface(c).Height(28).Padding(0, spaceSm).Gap(spaceXs).
		AlignItems(ui.Center).Children(func() {
		label := a.tr("Reference: none", "参考：无")
		if reference != nil {
			label = a.tr("Reference: ", "参考：") + sourceDisplayName(reference)
		}
		ui.Text(c, label).FontSize(textMicro).TextColor(colorTextDim).Grow(1).MinWidth(0).SingleLine()
		if a.syncRef != "" {
			a.iconButton(c, "sync-ref-clear", lucide.X(),
				a.tr("go back to the first picture on the stage", "改回以舞台上第一幅画面为参考"), false, func() {
					a.syncRef = ""
				})
		}
	})
	for i, it := range a.items {
		if !a.isStageMember(it) {
			continue
		}
		isRef := reference != nil && it.source.ID == reference.source.ID
		row := it
		edge := colorLineSoft
		if isRef {
			edge = colorRowEdge
		}
		ui.Column(c).WidthPercent(100).Gap(0).Padding(spaceXs, spaceSm).
			Background(colorRow).Border(1, edge).Radius(radiusMd).Children(func() {
			// The reference is one line: it has no offset to step, and the
			// second line would only repeat the same word.
			lineHeight := float32(20)
			if isRef {
				lineHeight = 26
			}
			ui.Row(c).WidthPercent(100).Height(lineHeight).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d  %s", i+1, sourceDisplayName(row))).FontSize(textBody).FontWeight(400).
					TextColor(colorText).Grow(1).MinWidth(0).SingleLine().Tooltip(sourceDisplayName(row))
				if !isRef {
					a.iconButton(c, "sync-ref-"+string(row.source.ID), lucide.Crosshair(),
						a.tr("make this source the reference", "把这一路设为参考"), false, func() {
							a.syncRef = row.source.ID
						})
				}
				if isRef {
					ui.Text(c, a.tr("reference", "参考")).FontSize(textMicro).
						TextColor(colorAccent).SingleLine()
					return
				}
				badge := ui.Text(c, a.syncBadge(row)).FontSize(textMicro).SingleLine()
				if a.syncAligned(row) {
					badge = badge.TextColor(colorGood)
				} else {
					badge = badge.TextColor(colorDim)
				}
			})
			if isRef {
				return
			}
			ui.Row(c).WidthPercent(100).Height(28).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
				a.offsetStepper(c, "sync-step-"+string(row.source.ID), a.entryOf(row).Offset,
					a.settingsNudge(1), func(delta time.Duration) {
						a.report(a.runCommand(command{Kind: commandNudge, Source: row.source.ID, Delta: delta}))
					})
				if offset := a.entryOf(row).Offset; offset != 0 {
					a.iconButton(c, "sync-reset-"+string(row.source.ID), lucide.RotateCcw(),
						a.tr("set this offset back to zero", "把这一路的偏移复位"), false, func() {
							a.report(a.runCommand(command{Kind: commandNudge, Source: row.source.ID, Delta: -offset}))
						})
				}
				a.iconButton(c, "sync-align-"+string(row.source.ID), lucide.ScanSearch(),
					a.tr("measure a suggestion against the reference (read only)",
						"与参考比对，给一条只读建议"), false, func() {
						if reference != nil {
							if refIndex := a.indexOf(reference); refIndex >= 0 {
								a.probeAsync(refIndex, i)
							}
						}
					})
				ui.Box(c).Grow(1)
			})
		})
	}
	// A suggestion is a paragraph, not a label: it is written where there is
	// room for it (Diagnostics) instead of being clipped by this panel.
	if a.syncNote != "" {
		ui.Text(c, a.tr("the last suggestion is in Diagnostics", "最近一次建议见诊断页")).
			FontSize(textMicro).TextColor(colorTextDim).SingleLine()
	}
}

// indexOf is the position of an item in the running list, which is what the
// alignment probe takes.
func (a *appState) indexOf(want *item) int {
	for i, it := range a.items {
		if it == want {
			return i
		}
	}
	return -1
}

// dockStatus is one line for the log: which panels are open and how tall the
// console is, so a capture can prove the state it was taken in.
func (a *appState) dockStatus() string {
	shown := ""
	for _, id := range dockOrder {
		if !a.dockShown(id) {
			continue
		}
		if shown != "" {
			shown += "+"
		}
		shown += id
	}
	return fmt.Sprintf("console open=%v height=%.0f panels=%s", a.dockUp, a.dockHeight, shown)
}
