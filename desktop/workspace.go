// The workspace rail is the thinnest navigation the window can have: three
// pages that each show real state, and no button for anything that does not
// work yet.
package main

import (
	"fmt"
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
	default:
		return "Stage"
	}
}

// sourcesPage lists every source with the state of the player behind it and the
// commands that act on that one source.
func (a *appState) sourcesPage(c *ui.Context) {
	ui.Column(c).Fill().Grow(1).Gap(spaceSm).Children(func() {
		a.importRow(c)
		ui.Text(c, fmt.Sprintf("%d source(s); click a row to inspect it", len(a.items))).
			FontSize(textSmall).TextColor(colorTextDim)
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Gap(spaceSm).Children(func() {
				for i, it := range a.items {
					a.sourceCard(c, i, it)
				}
				if len(a.items) == 0 {
					ui.Text(c, "no sources yet: type an address above and press Add").
						FontSize(textBody).TextColor(colorTextDim).Padding(spaceMd, 0)
				}
			})
		})
	})
}

// importRow is the address field and the Add button. Both are real: Add sends
// an add-source command, the read runs off the drawing thread, and the outcome
// lands in the status line and the log. A failed read keeps the address here
// with a Retry, because the source it never added has no card to retry from.
func (a *appState) importRow(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Children(func() {
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).WidthPercent(100).Children(func() {
			ui.TextInput(c, &a.importText).Grow(1).Height(34).
				Placeholder("https://… .m3u8  or  C:\\path\\file.mp4")
			label := "Add"
			if a.importing != "" {
				// Loading feedback with a real clock, not an animation: the
				// seconds come from importSince and the ticker repaints.
				label = fmt.Sprintf("Reading… %.0fs", time.Since(a.importSince).Seconds())
			}
			add := ui.Button(c.Key("import-add"), label).Width(96).Height(34)
			if a.importing != "" {
				add.Disabled(true).Tooltip("the read is in flight; Cancel stops it")
			}
			if add.Clicked() {
				a.report(a.runCommand(command{Kind: commandAdd, URI: a.importText}))
			}
			if a.importing != "" {
				if ui.Button(c.Key("import-cancel"), "Cancel").Width(76).Height(34).
					Tooltip("stop reading " + shortURI(a.importing) + "; nothing is added").
					Clicked() {
					a.report(a.runCommand(command{Kind: commandCancelImport}))
				}
			}
		})
		failed := a.lastCommand.Command.Kind == commandAdd && a.lastCommand.Err != nil
		if failed {
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).WidthPercent(100).Children(func() {
				// One line, with the whole error on the tooltip: wrapped over
				// three lines in a narrow window it pushed Retry onto its own
				// row, and a failure message must not move the button that
				// retries it.
				message := fmt.Sprintf("last import failed: %v", a.lastCommand.Err)
				ui.Text(c, message).FontSize(textMicro).TextColor(colorBad).
					Grow(1).SingleLine().Tooltip(message)
				if ui.Button(c.Key("import-retry"), "Retry").Width(76).Height(26).Clicked() {
					a.report(a.runCommand(command{Kind: commandAdd, URI: a.lastCommand.Command.URI}))
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
			ui.Text(c, fmt.Sprintf("%d · %s · %s · gen %d · %s · ready %v · %s / %s · offset %s",
				i+1, source.ID, shortURI(source.URI), source.Generation,
				tracksLine([]core.TrackID{source.Video, source.Audio}), source.Ready,
				source.Position.Round(100*1e6), source.Duration.Round(100*1e6), source.Offset)).
				FontSize(textSmall).TextColor(colorText).FontFeatures("tnum")
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
		ui.Text(c, "session events").FontSize(textMicro).TextColor(colorTextDim)
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
