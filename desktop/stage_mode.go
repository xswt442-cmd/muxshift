package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// stageMode is the P16.0 experiment: what becomes of the picture while a page
// other than the stage is open. The names follow the ruling that asked for
// three shapes: the current one, one where the video node stays mounted and
// drawn, and one where the element exists but is not visible. The visible
// variant is split into dim, tiny and hidden because "invisible" can mean
// several different things to a compositor.
type stageMode string

const (
	// stageSwap is what the build did until P16.0: the picture leaves the tree
	// the moment another page is drawn, so nothing asks the player for a frame.
	stageSwap stageMode = "swap"
	// stageSplit keeps the picture on screen above the page. Nothing is hidden:
	// the user keeps seeing the media while adjusting settings.
	stageSplit stageMode = "split"
	// stageDim draws the picture at its usual size but fully transparent, so the
	// node is still painted every frame without being visible.
	stageDim stageMode = "dim"
	// stageTiny draws the picture in a one dip square, which is painted but not
	// readable.
	stageTiny stageMode = "tiny"
	// stageHidden mounts the picture but marks it hidden, so layout reserves no
	// room and nothing is painted.
	stageHidden stageMode = "hidden"
)

// parseStageMode reads a -stage-mode value, refusing one it does not know so a
// typo cannot silently measure the wrong shape.
func parseStageMode(s string) (stageMode, error) {
	switch mode := stageMode(s); mode {
	case stageSwap, stageSplit, stageDim, stageTiny, stageHidden:
		return mode, nil
	}
	return stageSwap, fmt.Errorf("unknown -stage-mode %q, want swap, split, dim, tiny or hidden", s)
}

// pictureWidth is the room the picture may use in the centre column, with the
// rail and the inspector subtracted exactly as the stage page does it.
func (a *appState) pictureWidth(width float32) float32 {
	available := width - 2*spaceMd
	if a.rail.shown(width >= railAutoWidth) {
		available -= railWidth + spaceMd
	}
	if a.inspector.shown(width >= inspectorAutoWidth) {
		available -= inspectorWidth + spaceMd
	}
	return available
}

// picture draws the stage into a box of a fixed height. The stage page grows it
// instead; here a fixed height keeps the page below it inside the window.
func (a *appState) picture(c *ui.Context, height float32) ui.Element {
	return ui.Box(c).WidthPercent(100).Height(height).Children(func() {
		a.stage(c)
	})
}

// parkedStage draws the picture before a page that is not the stage, in the
// shape the mode asks for. Swap mode draws nothing: it is the behaviour the
// other modes are measured against.
func (a *appState) parkedStage(c *ui.Context, width, height float32) {
	if a.stageMode == stageSwap {
		return
	}
	capped := a.pictureWidth(width) * 9 / 16
	if capped < 200 {
		capped = 200
	}
	switch a.stageMode {
	case stageSplit:
		// Half the window at most, so the page below it still has room to
		// scroll through its rows.
		if limit := height * 0.45; capped > limit {
			capped = limit
		}
		a.picture(c, capped)
	case stageDim:
		a.picture(c, capped).Opacity(0)
	case stageTiny:
		ui.Box(c).Width(1).Height(1).Children(func() {
			a.stage(c)
		})
	case stageHidden:
		ui.Box(c).Hidden(true).Children(func() {
			a.stage(c)
		})
	}
}
