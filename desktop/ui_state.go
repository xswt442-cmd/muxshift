package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// This file keeps the small state-to-words helpers the workbench draws. They
// used to live in workbench.go; the three-column shell made that file long
// enough that they were easier to keep beside the snapshot they read.

// formatEvent is the one duration format the window uses: a tenth of a second,
// an "s" after it. Diagnostics and scenarios read the same string, so a bug
// report and a log line agree.
func formatEvent(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// layoutLine is the diagnostic line the scenario runners print. It names the
// panes the current window size opens, not what the person toggled by hand: the
// line is about the size rule, and toggles have their own records.
func (a *appState) layoutLine(width, height float32) string {
	return fmt.Sprintf("layout %.0fx%.0f rail=%t inspector=%t dock=%t",
		width, height,
		a.rail.shown(width >= railAutoWidth),
		a.inspector.shown(width >= inspectorAutoWidth),
		a.dock.shown(a.settings.DockOpen && height >= dockAutoHeight))
}

// entryOf is the snapshot row for one source, or the zero row when the source
// has no player yet. Callers that need "is there a player" check Player.
func (a *appState) entryOf(it *item) sourceSnapshot {
	for _, source := range a.snapshot().Sources {
		if source.ID == it.source.ID {
			return source
		}
	}
	return sourceSnapshot{}
}

func (a *appState) playing() bool {
	return a.snapshot().Playing
}

// sourceMuted reports whether the audio overlay is keeping this source quiet.
// It is the state the mixer's M button shows, not the scene's routing.
func (a *appState) sourceMuted(it *item) bool {
	if it.audio == "" {
		return false
	}
	return !a.audible[it.audio]
}

// muteLabel names the button that flips the player's own mute.
func (a *appState) muteLabel(it *item) string {
	if it.player == nil {
		return a.tr("Mute", "静音")
	}
	if it.player.Muted() {
		return a.tr("Unmute", "取消静音")
	}
	return a.tr("Mute", "静音")
}

// shortAllowLabel names the audio-hold action in a narrow row.
func (a *appState) shortAllowLabel(it *item) string {
	if it.audio == "" {
		return a.tr("No audio", "无音频")
	}
	if a.audible[it.audio] {
		return a.tr("Mute", "静音")
	}
	return a.tr("Allow", "允许")
}

// positionLine is the source's media time, and durationLine its length. Both
// return an empty string when the player has not reported one, so a row is
// omitted rather than shown as "0:00".
func (a *appState) positionLine(it *item) string {
	entry := a.entryOf(it)
	if !entry.Player || entry.Duration <= 0 {
		return ""
	}
	return formatEvent(entry.Position)
}

func (a *appState) durationLine(it *item) string {
	entry := a.entryOf(it)
	if !entry.Player || entry.Duration <= 0 {
		return ""
	}
	return formatEvent(entry.Duration)
}

// hasCommonClock says whether asking the aligner for a suggestion can mean
// anything yet: it needs more than one source and a picture that is running.
func (a *appState) hasCommonClock(it *item) bool {
	entry := a.entryOf(it)
	return entry.Player && entry.Playing && len(a.items) > 1
}

// retryImport sends the address that failed again. An address that is still in
// the import field is what a person just typed; a failed row keeps it there, so
// the retry is the same read rather than a remembered one.
func (a *appState) retryImport() {
	if a.importing != "" {
		return
	}
	if text := a.importText; text != "" {
		a.report(a.runCommand(command{Kind: commandAdd, URI: text}))
		return
	}
	if a.selected != "" && a.itemOf(a.selected) != nil {
		a.report(a.runCommand(command{Kind: commandRetry, Source: a.selected}))
	}
}

// pictureState says what the stage is doing with this source in words, not in
// booleans.
func (a *appState) pictureState(entry sourceSnapshot) string {
	switch {
	case !entry.Player:
		return a.tr("no player yet", "还没有播放器")
	case !entry.Ready:
		return a.tr("opening", "正在打开")
	case !entry.Playing:
		return a.tr("held at ", "停在 ") + formatEvent(entry.Position)
	default:
		return a.tr("playing", "播放中")
	}
}

// soundState is the same for the audio side.
func (a *appState) soundState(entry sourceSnapshot) string {
	switch {
	case !entry.Player:
		return a.tr("silent: no player", "无声：没有播放器")
	case entry.Muted:
		return a.tr("muted", "已静音")
	case entry.Volume <= 0:
		return a.tr("silent: volume is zero", "无声：音量为零")
	default:
		return a.tr("heard", "可听")
	}
}

// volumeState shows the number only when the player reports one.
func volumeState(entry sourceSnapshot) string {
	if !entry.Player {
		return ""
	}
	return fmt.Sprintf("%.2f", entry.Volume)
}

// alignedState turns the alignment flag into the sentence a person needs.
func (a *appState) alignedState(entry sourceSnapshot) string {
	if entry.Aligned {
		return a.tr("yes", "是")
	}
	return a.tr("not yet: no common clock found", "还没有：没找到共同时钟")
}

// clamp01 keeps a volume inside the one range the player accepts.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// inspectorSeek is the replay-seek block in the time tab. It keeps the window
// caption and the input; the three paragraphs that used to sit under it moved
// to the help page.
func (a *appState) inspectorSeek(c *ui.Context) {
	start, end, err := a.replayWindowForEntry()
	if err != nil {
		ui.Box(c).Height(1).WidthPercent(100).Background(colorLine)
		a.detailRow(c, a.tr("Replay seek", "回放定位"), err.Error())
		return
	}
	a.seekMu.Lock()
	busy := a.seek != nil
	a.seekMu.Unlock()
	ui.Box(c).Height(1).WidthPercent(100).Background(colorLine)
	ui.Text(c, a.tr("Window ", "窗口 ")+formatEvent(start)+".."+formatEvent(end)).FontSize(textMicro).TextColor(colorDim).Wrap().SingleLine()
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).WidthPercent(100).Children(func() {
		c.Key("seek-seconds")
		ui.TextInput(c, &a.seekText).Grow(1).Height(a.controlHeight() - 4).
			Placeholder(a.tr("seconds into this window", "窗口内的秒数"))
		label := a.tr("Seek", "定位")
		if busy {
			label = a.tr("Preparing…", "准备中…")
		}
		button := ui.Button(c.Key("seek-start"), label).Width(96).Height(a.controlHeight() - 4)
		if busy {
			button.Disabled(true).Tooltip(a.tr("a replay seek is already being prepared", "已经在准备一次回放定位"))
		}
		if button.Clicked() {
			a.seekNote = a.requestReplaySeek()
		}
	})
	if a.seekNote != "" {
		ui.Text(c, a.seekNote).FontSize(textMicro).TextColor(colorDim).Wrap()
	}
}

// inspectorAccount shows the replay notice lines, which are data with a tone.
func (a *appState) inspectorAccount(c *ui.Context) {
	lines := a.noticeLines()
	if len(lines) == 0 {
		return
	}
	tone := noticeColor(a.lastPhase())
	ui.Box(c).Height(1).WidthPercent(100).Background(colorLine)
	ui.Text(c, a.tr("Replay", "回放")).FontSize(textMicro).TextColor(colorDim)
	for index, line := range lines {
		text := ui.Text(c, line).FontSize(textSmall).TextColor(tone).Wrap()
		if index == 0 {
			text.FontFeatures("tnum")
		}
	}
}

// inspectorAlign shows the aligner's last answer.
func (a *appState) inspectorAlign(c *ui.Context) {
	lines := a.alignLines()
	if len(lines) == 0 {
		return
	}
	tone := alignTone(a.alignState())
	ui.Box(c).Height(1).WidthPercent(100).Background(colorLine)
	ui.Text(c, a.tr("Align", "对齐")).FontSize(textMicro).TextColor(colorDim)
	for index, line := range lines {
		text := ui.Text(c, line).FontSize(textSmall).TextColor(tone).Wrap()
		if index < 3 {
			text.FontFeatures("tnum")
		}
	}
}

// seekWindowCaption is the diagnostic name of the window a replay seek may
// land in. The window draws its own shorter line; the scenario logs keep this
// one so a report and a log agree.
func seekWindowCaption(start, end time.Duration) string {
	return "Candidate common replay window: " + formatEvent(start) + ".." + formatEvent(end)
}

// seekAuthorizationNote is the standing limit on a replay seek. It lives here
// because a test names it; the interface shows it on the help page rather than
// under every seek field.
const seekAuthorizationNote = "A target inside this window still requires per-source anchor authorization. " +
	"Cross-source content correspondence is not authorized. Some targets may be refused."
