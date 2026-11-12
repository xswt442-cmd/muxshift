// The mixer page is the listening overlay's face: one row per source showing
// what the scene says about it, and the three things the overlay adds on top of
// that (a mute of its own, a master mute, and one solo). Nothing here shapes
// sound: there is no shared PCM bus in this build, so the page says what is
// unavailable instead of offering it.
package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// mixerPage draws the overlay. Every button goes through the same command entry
// as the rest of the window, so what the page shows and what the log records are
// the same action.
func (a *appState) mixerPage(c *ui.Context) {
	ui.Column(c).Fill().Grow(1).Gap(spaceSm).Padding(spaceMd).Children(func() {
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "output").Width(70).FontSize(textSmall).TextColor(colorTextDim)
			ui.Text(c, "system default (device switching unavailable)").
				FontSize(textSmall).SingleLine().TextColor(colorTextDim)
		})
		ui.Text(c, a.overlayLine()).FontSize(textSmall).TextColor(colorText).Wrap()

		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			label := "Mute everything"
			if a.masterMute {
				label = "Everything muted"
			}
			if ui.Button(c.Key("mixer-master"), label).Width(150).Height(30).Clicked() {
				a.report(a.runCommand(command{Kind: commandMasterMute}))
			}
			ui.Text(c, a.masterNote()).FontSize(textSmall).TextColor(colorTextDim).Wrap()
		})

		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Gap(spaceSm).Children(func() {
				for i, it := range a.items {
					a.mixerRow(c, i, it)
				}
				if len(a.items) == 0 {
					ui.Text(c, "no sources are open; add one on the Sources page").
						FontSize(textSmall).TextColor(colorTextDim)
				}
			})
		})

		ui.Text(c, "not in this build: a shared PCM bus, ducking, panning, per-source EQ, "+
			"choosing another output device, and a level meter. Each source is played by its own "+
			"player into the default device.").
			FontSize(textMicro).TextColor(colorDim).Wrap()
	})
}

// mixerRow is one source: what the scene says, and the overlay's two controls.
func (a *appState) mixerRow(c *ui.Context, index int, it *item) {
	soloed := a.solo == it.source.ID
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Padding(spaceSm).
		Background(colorPanel).Radius(4).Children(func() {
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			name := string(it.source.ID)
			if len(it.source.URI) > 0 {
				name = shortURI(it.source.URI)
			}
			ui.Text(c, fmt.Sprintf("%d  %s", index+1, name)).
				FontSize(textSmall).SingleLine()
			if soloed {
				pill(c, "solo", colorWarn, false)
			}
			ui.Box(c).Grow(1)
			muteLabel := "Mute"
			if it.muted {
				muteLabel = "Muted"
			}
			if ui.Button(c.Key(fmt.Sprintf("mixer-mute-%d", index)), muteLabel).
				Width(76).Height(26).FontSize(textMicro).Clicked() {
				a.report(a.runCommand(command{Kind: commandMute, Source: it.source.ID}))
			}
			soloLabel := "Solo"
			if soloed {
				soloLabel = "Solo ✓"
			}
			if ui.Button(c.Key(fmt.Sprintf("mixer-solo-%d", index)), soloLabel).
				Width(76).Height(26).FontSize(textMicro).Clicked() {
				a.report(a.runCommand(command{Kind: commandSolo, Source: it.source.ID}))
			}
			if ui.Button(c.Key(fmt.Sprintf("mixer-hear-%d", index)), "Hear").
				Width(70).Height(26).FontSize(textMicro).
				Tooltip("routes this source's sound and takes the other sources out of the scene's audio, as the Sources page does").
				Clicked() {
				a.report(a.runCommand(command{Kind: commandHear, Source: it.source.ID}))
			}
		})
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			// Show the level the scene actually holds, so a slider and its label
			// cannot disagree with what is playing after a change.
			level := it.volume
			if current, routed := a.sceneLevel(it); routed {
				level = current.Volume
			}
			if ui.Slider(c.Key(fmt.Sprintf("mixer-volume-%d", index)), &level, 0, 1).
				Grow(1).Height(20).Clicked() {
				// Slider reports the new value on click and drag; the command
				// writes it to the scene without touching the mute, and refuses
				// to route a source that is not in the scene yet.
				a.report(a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: level}))
			}
			ui.Text(c, fmt.Sprintf("%.0f%%", level*100)).Width(46).
				FontSize(textMicro).TextColor(colorTextDim).FontFeatures("tnum")
		})
		ui.Text(c, a.mixerLine(it)).FontSize(textMicro).TextColor(colorTextDim).Wrap()
	})
}

// masterNote says what the master mute would do with what is on screen.
func (a *appState) masterNote() string {
	if a.masterMute {
		return "every source is silent; each one keeps its own mute and level for when this is off"
	}
	audible := 0
	for _, it := range a.items {
		if a.heard(it) {
			audible++
		}
	}
	return fmt.Sprintf("%d of %d source(s) would be heard", audible, len(a.items))
}

// overlayLine sums up the overlay for the page header.
func (a *appState) overlayLine() string {
	heard := make([]string, 0, len(a.items))
	for _, it := range a.items {
		if a.heard(it) {
			heard = append(heard, string(it.source.ID))
		}
	}
	if a.masterMute {
		return "master mute is on; the sources below keep what they were told"
	}
	if a.solo != "" {
		return fmt.Sprintf("solo on source %s: it is the only source the overlay lets through", a.solo)
	}
	if len(heard) == 0 {
		return "no source would be heard; check each row for the reason"
	}
	return "heard: " + joinWords(heard)
}
