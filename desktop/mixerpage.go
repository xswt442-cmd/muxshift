// The mixer page is the listening overlay's face: one row per source showing
// what the scene says about it, and the three things the overlay adds on top of
// that (a mute of its own, a master mute, and one solo). Nothing here shapes
// sound: there is no shared PCM bus in this build, so the page says what is
// unavailable instead of offering it.
package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// mixerPage draws the overlay. Every button goes through the same command entry
// as the rest of the window, so what the page shows and what the log records are
// the same action.
func (a *appState) mixerPage(c *ui.Context) {
	ui.Column(c).Fill().Grow(1).Gap(spaceSm).Padding(spaceMd).Children(func() {
		ui.Column(c).WidthPercent(100).Padding(spaceSm, spaceMd).Gap(spaceXs).
			Radius(radiusCard).Background(colorPanel).Border(1, colorLineSoft).
			Children(func() {
				ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.tr("Output", "输出")).Width(70).FontSize(textSmall).TextColor(colorTextDim)
					ui.Text(c, a.tr("system default", "系统默认设备")).
						FontSize(textSmall).SingleLine().TextColor(colorTextDim)
				})
				ui.Text(c, a.overlayLine()).FontSize(textSmall).TextColor(colorText).Wrap()

				ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
					label := a.tr("Mute all", "全部静音")
					if a.masterMute {
						label = a.tr("Unmute all", "取消全部静音")
					}
					a.mixerAction(c, "mixer-master", label, 150, 30, a.masterMute,
						ui.RGBA(228, 182, 109, 0.14), colorWarn, func() {
							a.report(a.runCommand(command{Kind: commandMasterMute}))
						})
					ui.Text(c, a.masterNote()).FontSize(textSmall).TextColor(colorTextDim).Wrap()
				})
			})

		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).WidthPercent(100).Gap(spaceSm).Children(func() {
				for i, it := range a.items {
					a.mixerRow(c, i, it)
				}
				if len(a.items) == 0 {
					ui.Text(c, a.tr("No sources are open.", "还没有打开的来源。")).
						FontSize(textSmall).TextColor(colorTextDim)
				}
			})
		})

		ui.Text(c, a.tr("Each source plays through its own player into the system default device.",
			"每个来源用自己的播放器输出到系统默认设备。")).
			FontSize(textMicro).TextColor(colorDim).Wrap()
	})
}

// mixerAction is the overlay's one button shape: quiet until hover, a soft
// state while it applies, and the same pressed step as every other control.
func (a *appState) mixerAction(c *ui.Context, key, label string, width, height float32, active bool, activeFill, activeInk ui.Color, onClick func()) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Width(width).Height(height).
		Padding(0, spaceSm).Radius(radiusSm).AlignItems(ui.Center)
	fill, ink := ui.Transparent, colorTextDim
	if active {
		fill, ink = activeFill, activeInk
	}
	if button.Pressed() {
		fill, ink = colorPressed, colorText
	} else if button.Hovered() {
		ink = colorText
		if !active {
			fill = colorHover
		}
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Text(c, label).FontSize(textMicro).FontWeight(500).TextColor(ink).SingleLine()
	})
	if button.Clicked() && onClick != nil {
		onClick()
	}
	return button
}

// mixerRow is one source: what the scene says, and the overlay's two controls.
func (a *appState) mixerRow(c *ui.Context, index int, it *item) {
	soloed := a.solo == it.source.ID
	ui.Column(c).WidthPercent(100).Gap(spaceXs).Padding(spaceSm).
		Background(colorPanel).Border(1, colorLineSoft).Radius(radiusMd).Children(func() {
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			name := sourceDisplayName(it)
			ui.Text(c, fmt.Sprintf("%d  %s", index+1, name)).
				FontSize(textSmall).SingleLine()
			routeColor := colorDim
			if a.routed(it) {
				routeColor = colorAccent
			}
			pill(c, a.audioRouteLabel(it), routeColor, false)
			if soloed {
				pill(c, "solo", colorWarn, false)
			}
			ui.Box(c).Grow(1)
			muteLabel := a.tr("Mute source", "静音本路")
			if it.muted {
				muteLabel = a.tr("Unmute source", "取消本路静音")
			}
			a.mixerAction(c, fmt.Sprintf("mixer-mute-%d", index), muteLabel, 76, 26,
				it.muted, ui.RGBA(228, 182, 109, 0.14), colorWarn, func() {
					a.report(a.runCommand(command{Kind: commandMute, Source: it.source.ID}))
				})
			a.mixerAction(c, fmt.Sprintf("mixer-solo-%d", index), a.tr("Solo", "独听"), 76, 26,
				soloed, colorAccentSoft, colorAccent, func() {
					a.report(a.runCommand(command{Kind: commandSolo, Source: it.source.ID}))
				})
			hear := a.mixerAction(c, fmt.Sprintf("mixer-hear-%d", index), a.tr("Route only", "仅路由此路"), 112, 26,
				false, ui.Transparent, colorTextDim, func() {
					a.report(a.runCommand(command{Kind: commandHear, Source: it.source.ID}))
				})
			hear.Tooltip(a.tr("Replaces this scene's audio routing with this source. This changes the scene; Solo is temporary.",
				"用这一路替换场景的音频路由。这会修改场景；独听是临时操作。"))
		})
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			// Show the level the scene actually holds, so a slider and its label
			// cannot disagree with what is playing after a change.
			level := it.volume
			if current, routed := a.sceneLevel(it); routed {
				level = current.Volume
			}
			if a.rangeSlider(c, fmt.Sprintf("mixer-volume-%d", index), &level, 0, 1, 320) {
				// The slider reports the new value while dragging; the command
				// writes it to the scene without touching the mute, and refuses
				// to route a source that is not in the scene yet.
				a.report(a.runCommand(command{Kind: commandVolume, Source: it.source.ID, Volume: level}))
			}
			a.numericReadout(c, fmt.Sprintf("%.0f%%", level*100), 46)
		})
		if sceneLevel, routed := a.sceneLevel(it); routed && sceneLevel.Muted {
			ui.Row(c).WidthPercent(100).Children(func() {
				a.mixerAction(c, fmt.Sprintf("mixer-scene-unmute-%d", index),
					a.tr("Clear scene mute", "取消场景静音"), 142, 24, false,
					ui.Transparent, colorTextDim, func() {
						if err := a.clearSceneMute(it); err != nil {
							a.lastErr = safeOpenError(err)
							a.invalidate()
						}
					})
			})
		}
		ui.Text(c, a.localizedMixerLine(it)).FontSize(textMicro).TextColor(colorTextDim).Wrap()
	})
}

// masterNote says what the master mute would do with what is on screen.
func (a *appState) masterNote() string {
	if a.masterMute {
		return a.tr("Every source is silenced. Each keeps its source mute, scene route and level.",
			"所有来源均已静音；每一路仍保留本路静音、场景路由和音量。")
	}
	audible := 0
	for _, it := range a.items {
		if a.heard(it) {
			audible++
		}
	}
	return fmt.Sprintf(a.tr("%d of %d source(s) would be audible", "%d/%d 路可听"), audible, len(a.items))
}

// overlayLine sums up the overlay for the page header.
func (a *appState) overlayLine() string {
	heard := make([]string, 0, len(a.items))
	for _, it := range a.items {
		if a.heard(it) {
			heard = append(heard, sourceDisplayName(it))
		}
	}
	if a.masterMute {
		return a.tr("Master mute is on; each source keeps its own state.", "总静音已开启；各路仍保留自身状态。")
	}
	if a.solo != "" {
		if it := a.itemOf(a.solo); it != nil {
			return fmt.Sprintf(a.tr("Solo on %s: other routed sources are temporarily silenced.", "%s 正在独听：其他已路由来源暂时静音。"), sourceDisplayName(it))
		}
		return a.tr("Solo target is no longer available.", "独听目标已不可用。")
	}
	if len(heard) == 0 {
		return a.tr("No source is currently audible. Check each row for its reason.", "当前没有可听来源，请查看各行显示的原因。")
	}
	return a.tr("Audible: ", "可听：") + strings.Join(heard, a.tr(", ", "、"))
}
