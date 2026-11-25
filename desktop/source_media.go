package main

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *appState) sourceMediaControls(c *ui.Context, it *item) {
	if a.selected != itemKey(it) || it.player == nil || !a.isStageMember(it) {
		return
	}
	key := string(itemKey(it))
	ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		available := !it.lifecyclePending && it.handover == nil && it.replay == nil
		refresh := ui.Button(c.Key("media-refresh-"+key), a.tr("Refresh source", "刷新此来源")).Height(24)
		if !available {
			refresh = refresh.Disabled(true)
		}
		if refresh.Clicked() {
			a.replaceSourceMedia(it, it.uri)
		}
		if it.playlistMaster != "" {
			quality := ui.Button(c.Key("media-quality-"+key), a.tr("Quality", "清晰度")).Height(24)
			if quality.Clicked() {
				it.mediaMenu = !it.mediaMenu
				if it.mediaMenu {
					a.loadSourceQualities(it)
				}
			}
			ui.PopoverBase(c, quality, &it.mediaMenu, func(panel ui.Element) {
				panel.Width(200).MaxHeight(220).Padding(spaceSm).Gap(spaceXs).
					Background(colorPanel2).Border(1, colorLine).Radius(radiusMd).
					Children(func() {
						ui.Scroll(c).WidthPercent(100).MaxHeight(200).Children(func() {
							a.sourceQualityOptions(c, it, key)
						})
					})
			})
		}
	})
	if it.handover != nil && it.handover.retainState {
		ui.Text(c, a.tr("Opening this source…", "正在打开这一路…")).FontSize(textMicro).TextColor(colorTextDim)
	}
}

func (a *appState) sourceQualityOptions(c *ui.Context, it *item, key string) {
	if it.mediaLoading {
		ui.Text(c, a.tr("Reading available quality levels…", "正在读取清晰度…")).FontSize(textMicro)
		return
	}
	if it.mediaError != "" {
		ui.Text(c, it.mediaError).FontSize(textMicro).TextColor(colorTextDim).Wrap()
	}
	for i, variant := range it.mediaChoices {
		label := fmt.Sprintf("%.2f Mbps", float64(variant.bandwidth)/1e6)
		if variant.height > 0 {
			label = fmt.Sprintf("%dp · %s", variant.height, label)
		}
		active := variant.address == it.uri
		button := ui.Button(c.Key("media-variant-"+key+"-"+itoa(i)), label).Height(24)
		if active {
			button = button.Background(colorSelected).TextColor(colorSelectedText)
		}
		if active || it.lifecyclePending || it.handover != nil || it.replay != nil {
			button = button.Disabled(true)
		}
		if button.Clicked() {
			a.replaceSourceMedia(it, variant.address)
		}
	}
}

func (a *appState) replaceSourceMedia(it *item, uri string) {
	if it.player == nil || !a.isStageMember(it) || it.lifecyclePending || it.handover != nil || it.replay != nil {
		return
	}
	a.openReplacement(it, uri, true)
	if it.openErr != nil {
		a.lastErr = it.openErr
	}
	a.invalidate()
}

func (a *appState) loadSourceQualities(it *item) {
	if it.mediaLoading {
		return
	}
	master, stage, generation := it.playlistMaster, a.stageGeneration, it.generation
	ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
	it.mediaCancel = cancel
	it.mediaLoading = true
	it.mediaError = ""
	it.mediaChoices = nil
	go func() {
		defer cancel()
		list, err := inspectLivePlaylist(ctx, master)
		a.mainDo(func() {
			if a.closed || a.stageGeneration != stage || a.itemOf(it.source.ID) != it {
				return
			}
			it.mediaLoading = false
			it.mediaCancel = nil
			if it.generation != generation {
				a.invalidate()
				return
			}
			if err != nil {
				it.mediaError = a.tr("Could not read quality levels. Reopen this menu to retry.", "清晰度读取失败，重新打开菜单可重试。")
			} else {
				for _, variant := range list.variants {
					if variant.live {
						it.mediaChoices = append(it.mediaChoices, variant)
					}
				}
				if len(it.mediaChoices) < 2 {
					it.mediaChoices = nil
					it.mediaError = a.tr("No other playable quality levels.", "没有其它可播放的清晰度。")
				}
			}
			a.invalidate()
		})
	}()
}
