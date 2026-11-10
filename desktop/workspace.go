// The workspace rail is the thinnest navigation the window can have: three
// pages that each show real state, and no button for anything that does not
// work yet.
package main

import (
	"fmt"

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
	default:
		return "Stage"
	}
}

// workspaceRow draws the rail. The page names are the only buttons, and every
// one of them opens a page that reads the running session.
func (a *appState) workspaceRow(c *ui.Context) {
	ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		for w := workspace(0); w < workspaceCount; w++ {
			page := w
			button := ui.Button(c.Key(fmt.Sprintf("workspace-%d", int(page))), page.title()).
				Width(112).
				Height(30)
			if page == a.workspace {
				button = button.Background(colorAccentSoft).Border(1, colorAccent)
			}
			if button.Clicked() {
				a.workspace = page
			}
		}
	})
}

// sourcesPage lists every source with the state of the player behind it and the
// commands that act on that one source.
func (a *appState) sourcesPage(c *ui.Context) {
	ui.Column(c).Fill().Grow(1).Gap(spaceSm).Children(func() {
		ui.Text(c, "every source in the session").FontSize(textSmall).TextColor(colorTextDim)
		for i, it := range a.items {
			a.sourceCard(c, i, it)
		}
		if len(a.items) == 0 {
			ui.Text(c, "no sources yet").FontSize(textBody).TextColor(colorTextDim)
		}
	})
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
