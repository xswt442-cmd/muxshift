// The workspace rail is the thinnest navigation the window can have: three
// pages that each show real state, and no button for anything that does not
// work yet.
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
	// workspaceSettings edits the global settings; the page keeps its own draft
	// until Apply, so Cancel is free.
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
			field := ui.TextInput(c, &a.importText).Grow(1).Height(a.controlHeight()).
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
			add := ui.Button(c.Key("import-add"), label).Width(96).Height(a.controlHeight()).
				Tooltip(a.tr("add a file, a media address or a website", "添加文件、媒体地址或网页"))
			if a.importing != "" {
				add.Disabled(true).Tooltip(a.tr("the read is in flight; Cancel stops it", "正在读取；按取消可以停下"))
			}
			if add.Clicked() {
				a.openAddSource()
			}
			if a.importing != "" {
				if ui.Button(c.Key("import-cancel"), a.tr("Cancel", "取消")).Width(76).
					Height(a.controlHeight()).
					Tooltip(a.tr("stop reading; nothing is added", "停止读取，不会添加") + " " + shortURI(a.importing)).
					Clicked() {
					a.report(a.runCommand(command{Kind: commandCancelImport}))
				}
			}
		})
		failed := a.lastCommand.Command.Kind == commandAdd && a.lastCommand.Err != nil
		if failed {
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).WidthPercent(100).Children(func() {
				ui.Text(c, a.errorShort()).FontSize(textMicro).TextColor(colorBad).
					Grow(1).SingleLine().Tooltip(a.lastCommand.Err.Error())
				if ui.Button(c.Key("import-retry"), a.tr("Retry", "重试")).Width(76).
					Height(26).Clicked() {
					a.report(a.runCommand(command{Kind: commandAdd, URI: a.lastCommand.Command.URI}))
				}
				if ui.Button(c.Key("import-error-open"), a.tr("Details", "详情")).Width(76).
					Height(26).Clicked() {
					a.errorOpen = true
				}
			})
		}
	})
}

// selectedSource is the source the inspector follows, or nil.
func (a *appState) selectedSource() (*item, int, bool) {
	for i, it := range a.items {
		if it.source.ID == a.selected {
			return it, i, true
		}
	}
	return nil, 0, false
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
	ui.Column(c).Fill().Grow(1).Gap(spaceXs).Children(func() {
		snapshot := a.snapshot()
		ui.Text(c, fmt.Sprintf("session %s · scene %s · video %s · %s",
			snapshot.Name, snapshot.Scene, pictureLine(snapshot.Picture), soundLine(snapshot.Sound))).
			FontSize(textSmall).TextColor(colorTextDim).FontFeatures("tnum")
		ui.Text(c, fmt.Sprintf("event %s · playing %v · last %s",
			snapshot.Event.Round(100*1e6), snapshot.Playing, snapshot.LastCommand)).
			FontSize(textSmall).TextColor(colorTextDim)
		for i, source := range snapshot.Sources {
			ui.Text(c, fmt.Sprintf("%d · %s · %s · gen %d · %s · ready %v · player %v · playing %v · %s / %s · offset %s",
				i+1, source.ID, shortURI(source.URI), source.Generation,
				tracksLine([]core.TrackID{source.Video, source.Audio}), source.Ready,
				source.Player, source.Playing,
				source.Position.Round(100*1e6), source.Duration.Round(100*1e6), source.Offset)).
				FontSize(textSmall).TextColor(colorText).FontFeatures("tnum")
			// What the picture and sound actually are, as far as the player can
			// tell. Frame rate and codec are not exposed by the engine, so they
			// are named as unknown rather than invented.
			ui.Text(c, a.sourceFacts(a.find(source.ID))).FontSize(textMicro).
				TextColor(colorTextDim).FontFeatures("tnum")
		}
		if snapshot.SeekWallClock != "" {
			ui.Text(c, fmt.Sprintf("seek %s · requested %s · actual %s · residual %s · window %s · at %s (wall clock)",
				snapshot.SeekSource, snapshot.SeekRequested.Round(100*1e6), snapshot.SeekActual.Round(100*1e6),
				snapshot.SeekResidual.Round(100*1e6), snapshot.SeekWindow, snapshot.SeekWallClock)).
				FontSize(textSmall).TextColor(colorTextDim).FontFeatures("tnum")
		}
		if a.syncNote != "" {
			ui.Text(c, a.syncNote).FontSize(textSmall).TextColor(colorTextDim)
		}
		// P5: the browser's way in. The code is short-lived on purpose; making a
		// new one never closes the listener a paired browser already uses.
		ui.Text(c, a.tr("browser handoff", "浏览器接入")).FontSize(textMicro).TextColor(colorTextDim)
		switch {
		case a.handoffErr != "":
			ui.Text(c, a.handoffErr).FontSize(textSmall).TextColor(colorBad).SingleLine()
		default:
			ui.Row(c).Gap(spaceXs).Children(func() {
				ui.Text(c, a.handoff.codeLine()).FontSize(textSmall).TextColor(colorText).FontFeatures("tnum")
				ui.Button(c.Key("handoff-new-code"), a.tr("New code", "换一个码")).Height(28).Width(104).
					Tooltip(a.tr("the browser pairs with this code instead of the expired one", "浏览器用这个新码配对，旧码随即失效")).
					OnClick(func() {
						if a.handoff != nil {
							a.handoff.rotate()
							log.Printf("handoff: made a new pairing code")
						}
					})
			})
			ui.Text(c, a.tr("load the unpacked extension in Edge or Chrome, open its popup and pair once with the code above", "在 Edge 或 Chrome 里加载解包扩展，打开弹窗，用上面的码配对一次")).
				FontSize(textMicro).TextColor(colorTextDim)
		}
		if rows := a.dvrRows(); len(rows) > 0 {
			ui.Text(c, a.tr("replay buffers (live)", "回放缓存（实时）")).FontSize(textMicro).TextColor(colorTextDim)
			for _, row := range rows {
				ui.Text(c, row).FontSize(textSmall).TextColor(colorText).FontFeatures("tnum")
			}
			ui.Text(c, a.budgetLine()).FontSize(textMicro).TextColor(colorTextDim)
			ui.Button(c.Key("dvr-cleanup"), a.tr("Forget recordings nothing plays", "清掉没有在播放的录制")).Height(28).Width(268).
				OnClick(func() { a.cleanupDVR() })
			if a.dvrNote != "" {
				ui.Text(c, a.tr("last cleanup: ", "上次清理：")+a.dvrNote).FontSize(textMicro).TextColor(colorTextDim)
			}
		}
		ui.Text(c, a.tr("background work", "后台任务")).FontSize(textMicro).TextColor(colorTextDim)
		{
			count, last := a.backgroundFailures()
			line := fmt.Sprintf("%d failure(s) this run · %s", count, last.line())
			if count == 0 {
				line = a.tr("nothing has failed in the background this run", "这次运行没有后台失败")
			}
			ui.Text(c, line).FontSize(textSmall).TextColor(colorText).Wrap()
			ui.Text(c, a.tr("the status bar notice can be dismissed; this count keeps the record", "状态条上的提示可以关掉，这里的计数会保留")).
				FontSize(textMicro).TextColor(colorTextDim)
		}
		ui.Text(c, a.tr("session events", "会话事件")).FontSize(textMicro).TextColor(colorTextDim)
		events := a.session.Events()
		start := 0
		if len(events) > 12 {
			start = len(events) - 12
		}
		for _, event := range events[start:] {
			ui.Text(c, fmt.Sprintf("%3d · %s · %s", event.Seq, event.Kind, event.Object)).
				FontSize(textMicro).TextColor(colorTextDim).FontFeatures("tnum")
		}
	})
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
