package main

import "github.com/egoist/mygo/ui"

// parkedStage keeps the picture mounted while a page other than the stage is
// open. P16.0 measured the alternatives: a node that leaves the tree freezes
// the media, and a picture that stays visible squeezes the page it is drawn
// over. A hidden node does neither — layout reserves no room and nothing is
// painted, yet the player is still asked for a frame every pass — so the media
// keeps running and the stage shows the current frame when it comes back.
func (a *appState) parkedStage(c *ui.Context) {
	ui.Box(c).Hidden(true).Children(func() {
		a.stage(c)
	})
}
