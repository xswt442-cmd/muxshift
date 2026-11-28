// The workspace rail is the thinnest navigation the window can have: pages that
// each show real state, and no button for anything that does not work yet.
package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

// workspace is the page the window shows below the rail.
type workspace int

const (
	// workspaceStage is the player, the transport row and any web panel: what
	// the window was before there was a rail.
	workspaceStage workspace = iota
	// workspaceSources lists every source with the player behind it. It takes
	// the place of the cards the stage page used to carry.
	workspaceSources
	// workspaceDiagnostics shows the session, the synchronisation state and the
	// session's own event list.
	workspaceDiagnostics
	// workspaceSettings edits the global settings. Draft values wait for Apply;
	// appearance has its own immediate save path.
	workspaceSettings
	// workspaceMixer edits the listening overlay: each source's mute, its level
	// and one solo, plus a master mute. It shapes listening, not sound.
	workspaceMixer
	// workspaceCount is the number of pages, so a scenario can walk all of them.
	workspaceCount
)

// title is the page's name on the rail.
func (w workspace) title() string {
	switch w {
	case workspaceSources:
		return "Sources"
	case workspaceDiagnostics:
		return "Diagnostics"
	case workspaceSettings:
		return "Settings"
	case workspaceMixer:
		return "Mixer"
	default:
		return "Stage"
	}
}

// sourcesPage lists every source with the state of the player behind it and the
// commands that act on that one source.
func (a *appState) sourcesPage(c *ui.Context) {
	ui.Column(c).Fill().Grow(1).Gap(spaceSm).Children(func() {
		a.importRow(c)
		ui.Text(c, a.tr("Sources", "来源")).FontSize(textSubtitle).TextColor(colorText)
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
				for i, it := range a.items {
					a.sourceEntry(c, it, i, false)
				}
				if len(a.items) == 0 {
					ui.Column(c).Padding(spaceXl, 0).Gap(spaceSm).Children(func() {
						ui.Text(c, a.tr("No sources yet", "还没有来源")).
							FontSize(textSubtitle).TextColor(colorTextDim)
						if ui.Button(c.Key("empty-add"), a.tr("Add a source", "添加来源")).
							Width(140).Height(a.controlHeight()).Clicked() {
							a.openAddSource()
						}
					})
				}
			})
		})
	})
}

// importRow is the address field and the Add button. Enter reads the address it
// holds; the button opens the three-way chooser, because a person who has not
// typed anything yet is choosing what kind of thing they have. A failed read
// keeps the address here with a Retry: the source it never added has no card to
// retry from.
func (a *appState) importRow(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).WidthPercent(100).Children(func() {
			c.Key("import-url")
			field := a.field(c, &a.importText).Grow(1).
				Placeholder(`https://… .m3u8  or  C:\path\file.mp4`).
				Description(a.tr("a media address or an absolute file path", "媒体地址或绝对文件路径"))
			if field.Submitted() && a.importing == "" {
				result, _ := a.addMediaAddress(a.importText)
				a.report(result)
			}
			label := a.tr("Add…", "添加…")
			if a.importing != "" {
				label = fmt.Sprintf(a.tr("Reading… %.0fs", "读取中… %.0f 秒"), time.Since(a.importSince).Seconds())
			}
			add := a.primaryButton(c, "import-add", label, 96, a.importing == "", func() {
				a.openAddSource()
			})
			if a.importing != "" {
				add.Tooltip(a.tr("the read is in flight; Cancel stops it", "正在读取；按取消可以停下"))
			} else {
				add.Tooltip(a.tr("add a file, a media address or a website", "添加文件、媒体地址或网页"))
			}
			if a.importing != "" {
				cancel := a.secondaryButton(c, "import-cancel", a.tr("Cancel", "取消"), 76, true, func() {
					a.report(a.runCommand(command{Kind: commandCancelImport}))
				})
				cancel.Tooltip(a.tr("stop reading; nothing is added", "停止读取，不会添加") + " " + shortURI(a.importing))
			}
		})
		a.importFailureNotice(c)
		if a.reselectSource != "" {
			if target, _, err := a.itemBySource(a.reselectSource); err == nil {
				ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).WidthPercent(100).Children(func() {
					c.Key("reselect-source-" + string(a.reselectSource))
					field := a.field(c, &a.reselectText).Grow(1).
						Placeholder(`https://… .m3u8  or  C:\path\file.mp4`).
						Description(a.tr("Choose the address for ", "为此来源选择地址：") + sourceDisplayName(target))
					submit := field.Submitted()
					if ui.Button(c.Key("reselect-cancel"), a.tr("Cancel", "取消")).Height(28).Clicked() {
						a.reselectSource = ""
						a.reselectText = ""
						a.reselectProblem = ""
					}
					if ui.Button(c.Key("reselect-apply"), a.tr("Use address", "使用地址")).Height(28).Clicked() {
						submit = true
					}
					if submit && strings.TrimSpace(a.reselectText) != "" && a.importing == "" {
						a.submitReselect(a.reselectSource)
					}
				})
				if a.reselectSource != "" && a.reselectProblem != "" {
					ui.Text(c, a.reselectProblem).FontSize(textMicro).TextColor(colorBad).Wrap()
				}
			}
		}
	})
}

func (a *appState) submitReselect(target core.SourceID) {
	result := a.runCommand(command{Kind: commandRetry, Source: target, URI: a.reselectText})
	if result.Err != nil {
		a.reselectProblem = result.Err.Error()
		return
	}
	a.reselectSource = ""
	a.reselectText = ""
	a.reselectProblem = ""
}

func (a *appState) importFailureNotice(c *ui.Context) {
	if a.importRetryTarget() == "" {
		return
	}
	title := a.tr("Could not add ", "无法添加 ") + shortURI(a.importFailureURI)
	detail := a.importFailureErr.Error()
	ui.Column(c).WidthPercent(100).Padding(spaceXs, spaceSm).Gap(spaceXs).
		Border(1, colorBad).Radius(radiusMd).Children(func() {
		ui.Text(c, title).FontSize(textSmall).TextColor(colorBad).SingleLine().Tooltip(title)
		ui.Text(c, a.importFailureCause(a.importFailureErr)).FontSize(textMicro).
			TextColor(colorTextDim).SingleLine().Tooltip(detail)
		ui.Row(c).Gap(spaceXs).Wrap().Children(func() {
			a.secondaryButton(c, "import-retry", a.tr("Retry", "重试"), 62, true, a.retryImport)
			a.secondaryButton(c, "import-error-open", a.tr("Details", "详情"), 66, true, func() {
				a.lastErr = a.importFailureErr
				a.errorOpen = true
			})
			a.secondaryButton(c, "import-dismiss", a.tr("Dismiss", "关闭提示"), 76, true, func() {
				if a.lastErr != nil && a.lastErr.Error() == detail {
					a.lastErr = nil
				}
				a.importFailureURI = ""
				a.importFailureErr = nil
			})
		})
	})
}

// selectedSource is the source the inspector follows, or nil.
func (a *appState) selectedSource() (*item, int, bool) {
	it, index, err := a.itemBySource(a.selected)
	return it, index, err == nil
}

// diagnosticsPage shows what the session and the players actually report. It is
// read-only: nothing on this page changes playback.
// sourceFacts says what the player can actually tell about a source, and names
// the things it cannot. The engine reports a decoded picture size but no frame
// rate and no codec, so those stay unknown rather than guessed.
func (a *appState) sourceFacts(it *item) string {
	if it == nil {
		return a.tr("no player", "没有播放器")
	}
	parts := []string{}
	if p := rawPlayer(it.player); p != nil {
		if w, h := p.Size(); w > 0 && h > 0 {
			parts = append(parts, fmt.Sprintf("%d×%d", w, h))
		}
	}
	if it.video == "" {
		parts = append(parts, a.tr("sound only", "仅声音"))
	} else {
		parts = append(parts, a.tr("fps unknown", "帧率未知"), a.tr("codec unknown", "编码未知"))
	}
	if it.source.ID == "" {
		return a.tr("not loaded", "未载入")
	}
	return strings.Join(parts, " · ")
}

func (a *appState) diagnosticsPage(c *ui.Context) {
	snapshot := a.snapshot()
	ui.Column(c).WidthPercent(100).Padding(spaceSm, spaceMd).Radius(radiusCard).
		Background(colorPanel).Border(1, colorLineSoft).Gap(0).Children(func() {
		a.diagSection(c, a.tr("Session", "会话"), func() {
			ui.Text(c, fmt.Sprintf(a.tr("Session %s · Scene %s · Video %s · %s", "会话 %s · 场景 %s · 视频轨 %s · %s"),
				snapshot.Name, snapshot.Scene, pictureLine(snapshot.Picture), soundLine(snapshot.Sound))).
				FontSize(textSmall).TextColor(colorText).FontFeatures("tnum").Wrap()
			ui.Text(c, fmt.Sprintf(a.tr("Event time %s · Playing %t · Last command %s", "事件时间 %s · 正在播放 %t · 最近命令 %s"),
				snapshot.Event.Round(100*1e6), snapshot.Playing, snapshot.LastCommand)).
				FontSize(textSmall).TextColor(colorTextDim)
		})
		items := make(map[core.SourceID]*item, len(a.items))
		for _, it := range a.items {
			items[it.source.ID] = it
		}
		for i, source := range snapshot.Sources {
			a.diagSection(c, fmt.Sprintf("%d · %s", i+1, shortURI(source.URI)), func() {
				ui.Text(c, fmt.Sprintf(a.tr("%s · Generation %d · Tracks %s · Ready %t · Player %t · Playing %t · %s / %s · Offset %s",
					"%s · 播放器代数 %d · 轨道 %s · 已就绪 %t · 有播放器 %t · 正在播放 %t · %s / %s · 偏移 %s"),
					source.ID, source.Generation,
					tracksLine([]core.TrackID{source.Video, source.Audio}), source.Ready,
					source.Player, source.Playing,
					source.Position.Round(100*1e6), source.Duration.Round(100*1e6), source.Offset)).
					FontSize(textSmall).TextColor(colorTextDim).FontFeatures("tnum").Wrap()
				ui.Text(c, a.sourceFacts(items[source.ID])).FontSize(textMicro).
					TextColor(colorTextDim).FontFeatures("tnum")
			})
		}
		if snapshot.SeekWallClock != "" {
			a.diagSection(c, a.tr("Seek", "定位"), func() {
				ui.Text(c, fmt.Sprintf("seek %s · requested %s · actual %s · residual %s · window %s · at %s (wall clock)",
					snapshot.SeekSource, snapshot.SeekRequested.Round(100*1e6), snapshot.SeekActual.Round(100*1e6),
					snapshot.SeekResidual.Round(100*1e6), snapshot.SeekWindow, snapshot.SeekWallClock)).
					FontSize(textSmall).TextColor(colorTextDim).FontFeatures("tnum").Wrap()
			})
		}
		if a.syncNote != "" {
			a.diagSection(c, a.tr("Sync", "同步"), func() {
				ui.Text(c, a.syncNote).FontSize(textSmall).TextColor(colorTextDim).Wrap()
			})
		}
		// P5: the browser's way in. The code is short-lived on purpose; making a
		// new one never closes the listener a paired browser already uses.
		a.diagSection(c, a.tr("Browser handoff", "浏览器接入"), func() {
			switch {
			case a.handoffErr != "":
				ui.Text(c, a.handoffErr).FontSize(textSmall).TextColor(colorBad).SingleLine()
			default:
				ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.handoff.codeLine()).FontSize(textSmall).TextColor(colorText).
						FontFeatures("tnum")
					a.diagButton(c, "handoff-new-code", a.tr("New code", "换一个码"), 104, false, func() {
						if a.handoff != nil {
							a.handoff.rotate()
							log.Printf("handoff: made a new pairing code")
						}
					})
				})
				ui.Text(c, a.tr("load the unpacked extension in Edge or Chrome, open its popup and pair once with the code above", "在 Edge 或 Chrome 里加载解包扩展，打开弹窗，用上面的码配对一次")).
					FontSize(textMicro).TextColor(colorTextDim).Wrap()
			}
		})
		if rows := a.dvrRows(); len(rows) > 0 {
			a.diagSection(c, a.tr("Replay buffers (live)", "回放缓存（实时）"), func() {
				for _, row := range rows {
					ui.Text(c, row).FontSize(textSmall).TextColor(colorText).FontFeatures("tnum")
				}
				ui.Text(c, a.budgetLine()).FontSize(textMicro).TextColor(colorTextDim).Wrap()
				a.diagButton(c, "dvr-cleanup", a.tr("Forget recordings nothing plays", "清掉没有在播放的录制"), 268, true,
					func() { a.cleanupDVR() })
				if a.dvrNote != "" {
					ui.Text(c, a.tr("last cleanup: ", "上次清理：")+a.dvrNote).FontSize(textMicro).TextColor(colorTextDim)
				}
			})
		}
		a.diagSection(c, a.tr("Background work", "后台任务"), func() {
			count, last := a.backgroundFailures()
			line := fmt.Sprintf("%d failure(s) this run · %s", count, last.line())
			if count == 0 {
				line = a.tr("nothing has failed in the background this run", "这次运行没有后台失败")
			}
			ui.Text(c, line).FontSize(textSmall).TextColor(colorText).Wrap()
			ui.Text(c, a.tr("the status bar notice can be dismissed; this count keeps the record", "状态条上的提示可以关掉，这里的计数会保留")).
				FontSize(textMicro).TextColor(colorTextDim).Wrap()
		})
		a.diagSection(c, a.tr("Session events", "会话事件"), func() {
			events := a.session.Events()
			start := 0
			if len(events) > 12 {
				start = len(events) - 12
			}
			ui.Column(c).WidthPercent(100).Padding(spaceSm, spaceMd).Radius(radiusSm).
				Background(colorBg).Children(func() {
				for _, event := range events[start:] {
					ui.Text(c, fmt.Sprintf("%3d · %s · %s", event.Seq, event.Kind, event.Object)).
						FontSize(textMicro).TextColor(colorTextDim).FontFeatures("tnum")
				}
			})
		})
	})
}

// diagButton is the diagnostics page's one secondary action: raised, quiet,
// and warning-tinted only while the pointer is on a destructive one.
func (a *appState) diagButton(c *ui.Context, key, label string, width float32, warning bool, onClick func()) ui.Element {
	button := a.controlFrame(c, key, width)
	fill, ink := colorPanel2, colorText
	if button.Pressed() {
		fill, ink = colorPressed, colorText
	} else if button.Hovered() {
		fill = colorHover
		if warning {
			ink = colorWarn
		}
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
	})
	if button.Clicked() && onClick != nil {
		onClick()
	}
	return button
}

// diagSection is one named block inside the diagnostics surface. The page is
// one continuous Inspector, not a stack of cards: a title, its facts and a
// single quiet rule.
func (a *appState) diagSection(c *ui.Context, title string, body func()) {
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Padding(spaceSm, 0).Children(func() {
		a.sectionTitle(c, title)
		body()
	})
	ui.Box(c).WidthPercent(100).Height(1).Background(colorLineSoft)
}

// pictureLine renders the scene's video tracks for the diagnostics page.
func pictureLine(tracks []core.TrackID) string {
	if len(tracks) == 0 {
		return "no picture"
	}
	line := ""
	for i, track := range tracks {
		if i > 0 {
			line += " + "
		}
		line += string(track)
	}
	return line
}

// soundLine renders the scene's audio tracks for the diagnostics page.
func soundLine(levels []trackSnapshot) string {
	if len(levels) == 0 {
		return "silent"
	}
	line := ""
	for i, level := range levels {
		if i > 0 {
			line += " + "
		}
		line += fmt.Sprintf("%s@%.2f", level.Track, level.Volume)
		if level.Muted {
			line += " muted"
		}
	}
	return line
}

// eventsKind is the event list's element count guard: Core keeps a bounded
// history, so the page shows its tail and nothing more.
var _ = core.EventKind("")
