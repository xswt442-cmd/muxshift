package main

import (
	"context"
	"fmt"
	"net/url"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

func (a *appState) sourceMediaControls(c *ui.Context, it *item) {
	if a.selected != itemKey(it) || it.player == nil || !a.isStageMember(it) {
		if it.mediaMenu || it.mediaLoading {
			it.closeMediaMenu()
		}
		return
	}
	key := string(itemKey(it))
	ui.Row(c).Gap(spaceXs).AlignItems(ui.Center).Children(func() {
		available := a.canReplaceSourceMedia(it)
		refresh := ui.Button(c.Key("media-refresh-"+key), a.tr("Refresh source", "刷新此来源")).Height(24)
		if !available {
			refresh = refresh.Disabled(true)
		}
		if refresh.Clicked() {
			a.replaceSourceMedia(it, it.uri)
		}
		if it.playlistMaster != "" {
			quality := ui.Button(c.Key("media-quality-"+key), a.tr("Quality", "清晰度")).Height(24)
			if !available {
				quality = quality.Disabled(true)
			}
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
			if !it.mediaMenu && it.mediaLoading {
				it.closeMediaMenu()
			}
		}
	})
	if it.handover != nil && it.handover.retainState {
		ui.Text(c, a.tr("Opening this source…", "正在打开这一路…")).FontSize(textMicro).TextColor(colorTextDim)
	}
	if it.mediaReplaceErr != nil {
		ui.Text(c, a.tr("Could not replace this source. Try refreshing it again.", "无法替换此来源，可再次刷新。")).
			FontSize(textMicro).TextColor(colorWarn).Wrap().Tooltip(it.mediaReplaceErr.Error())
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
	current := currentQuality(it.uri, it.mediaChoices)
	for i, variant := range it.mediaChoices {
		label := fmt.Sprintf("%.2f Mbps", float64(variant.bandwidth)/1e6)
		if variant.height > 0 {
			label = fmt.Sprintf("%dp · %s", variant.height, label)
		}
		active := i == current
		button := ui.Button(c.Key("media-variant-"+key+"-"+itoa(i)), label).Height(24)
		if active {
			button = button.Background(colorSelected).TextColor(colorSelectedText)
		}
		if active || !a.canReplaceSourceMedia(it) {
			button = button.Disabled(true)
		}
		if button.Clicked() {
			a.replaceSourceMedia(it, variant.address)
		}
	}
}

func currentQuality(address string, variants []liveVariant) int {
	match, matches := -1, 0
	identity := qualityAddress(address)
	for i, variant := range variants {
		if variant.address == address {
			return i
		}
		if identity != "" && qualityAddress(variant.address) == identity {
			match, matches = i, matches+1
		}
	}
	if matches == 1 {
		return match
	}
	return -1
}

// Keep variant selectors when comparing addresses with rotating tokens.
func qualityAddress(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ""
	}
	query.Del("token")
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func (a *appState) canReplaceSourceMedia(it *item) bool {
	return it.player != nil && a.isStageMember(it) &&
		!it.lifecyclePending && it.handover == nil && it.replay == nil && it.swap == nil
}

func (a *appState) replaceSourceMedia(it *item, uri string) {
	if !a.canReplaceSourceMedia(it) {
		return
	}
	it.mediaReplaceErr = nil
	a.openReplacement(it, uri, true)
	a.invalidate()
}

func (it *item) closeMediaMenu() {
	it.mediaMenu = false
	if it.mediaCancel != nil {
		it.mediaCancel()
		it.mediaCancel = nil
		it.mediaLoadSeq++
	}
	it.mediaLoading = false
}

func (a *appState) closeMediaMenus() {
	for _, it := range a.items {
		it.closeMediaMenu()
	}
}

func (a *appState) setSelectedSource(id core.SourceID) {
	if id != a.selected {
		if it := a.find(a.selected); it != nil {
			it.closeMediaMenu()
		}
		a.selected = id
	}
}

func (a *appState) loadSourceQualities(it *item) {
	if it.mediaLoading {
		return
	}
	master, stage, generation := it.playlistMaster, a.stageGeneration, it.generation
	key := itemKey(it)
	ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
	it.mediaLoadSeq++
	seq := it.mediaLoadSeq
	it.mediaCancel = cancel
	it.mediaLoading = true
	it.mediaError = ""
	it.mediaChoices = nil
	go func() {
		defer cancel()
		list, err := inspectLivePlaylist(ctx, master)
		a.mainDo(func() {
			if it.mediaLoadSeq != seq {
				return
			}
			it.mediaLoading = false
			it.mediaCancel = nil
			if a.closed || a.stageGeneration != stage {
				return
			}
			current, _, lookupErr := a.itemBySource(key)
			if lookupErr != nil || current != it || !it.mediaMenu {
				return
			}
			if err != nil || it.generation != generation {
				it.mediaError = a.tr("Could not read quality levels. Reopen this menu to retry.", "清晰度读取失败，重新打开菜单可重试。")
			} else {
				for _, variant := range list.variants {
					if variant.live {
						it.mediaChoices = append(it.mediaChoices, variant)
					}
				}
				if len(it.mediaChoices) == 0 || (len(it.mediaChoices) == 1 && currentQuality(it.uri, it.mediaChoices) == 0) {
					it.mediaChoices = nil
					it.mediaError = a.tr("No other playable quality levels.", "没有其他可播放的清晰度。")
				}
			}
			a.invalidate()
		})
	}()
}
