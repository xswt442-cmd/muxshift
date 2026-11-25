package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

// Steps for the arrow keys, in stage fractions. Shift moves four times as far.
const (
	nudgeStep     = 0.005
	nudgeStepFast = 0.02
)

// dragState is one gesture on the stage: what was grabbed, where the box was
// when the press landed, and the stage size at that moment. Holding the size
// here means a window resize mid-drag cannot make the picture jump: the
// pointer's pixels still map through the size the gesture started in.
type dragState struct {
	active bool
	id     core.SourceID
	handle handleKind
	box    stageBox
	stageW float32
	stageH float32
	moved  bool
	// held is whether the pointer is still pressing the gesture this frame.
	held bool
	// idle counts frames with neither a press nor a move; three of them end
	// the gesture, one quiet frame does not.
	idle int
}

// boxFields holds what a person typed into the inspector's four boxes. The
// text is kept apart from the stored box so a half-typed number does not move
// the picture, and an invalid one can be refused with a reason.
type boxFields struct {
	forSource  core.SourceID
	x, y, w, h string
	loaded     bool
	note       string
}

// sceneEdits is the current scene's arrangement, created on first use. A scene
// that was never edited has no entry at all, which is what "reset" returns to.
func (a *appState) sceneEdits() *stageEdits {
	if a.edits == nil {
		a.edits = map[core.SceneID]*stageEdits{}
	}
	edits, ok := a.edits[a.scene]
	if !ok {
		fresh := newStageEdits()
		edits = &fresh
		a.edits[a.scene] = edits
	}
	return edits
}

// currentEdits is sceneEdits without creating one, for readers.
func (a *appState) currentEdits() *stageEdits {
	if a.edits == nil {
		return nil
	}
	return a.edits[a.scene]
}

// boxOf is where a picture sits: the rectangle a person set, or the layout
// preset's slot when they never moved it.
func (a *appState) boxOf(it *item, slot stageBox) (stageBox, bool) {
	return a.currentEdits().boxFor(it.source.ID, slot)
}

// pictureAspect is the shape of the picture itself, or 0 when the player has
// not reported a size yet. A resize with ratio on uses it to keep the picture
// from being stretched; without it the box keeps whatever shape it has.
func (a *appState) pictureAspect(it *item, box stageBox, stageW, stageH float32) float32 {
	if p := rawPlayer(it.player); p != nil {
		if w, h := p.Size(); w > 0 && h > 0 {
			if stageW > 0 && stageH > 0 {
				return (float32(w) / float32(h)) / (stageW / stageH)
			}
			return float32(w) / float32(h)
		}
	}
	if box.H > 0 && box.W > 0 {
		return box.W / box.H
	}
	return 0
}

// selectedForStage reports whether a picture is the one the frame draws
// handles around. It is the same selection the inspector follows: clicking a
// picture tells the inspector about it and nothing about the sound.
func (a *appState) stageSelected(it *item) bool {
	return a.selected != "" && a.selected == it.source.ID
}

// manualBox reports whether a picture has an explicit rectangle in this
// scene, as opposed to one its layout preset placed.
func (a *appState) manualBox(id core.SourceID) bool {
	edits := a.currentEdits()
	if edits == nil {
		return false
	}
	_, ok := edits.boxes[id]
	return ok
}

// stageEditable reports whether the given picture may be moved. A locked
// source still shows its frame, so the arrangement stays readable, but its
// handles do nothing and a drag on it selects it without moving it.
func (a *appState) stageLocked(id core.SourceID) bool {
	edits := a.currentEdits()
	return edits != nil && edits.locked[id]
}

// toggleStageLock flips one picture's lock without changing the stack.
func (a *appState) toggleStageLock(id core.SourceID) {
	edits := a.sceneEdits()
	edits.locked[id] = !edits.locked[id]
	a.invalidate()
	a.saveSession()
}

// stageGeometry returns the editable pictures with their boxes and the order
// they stack in, back to front. The stage and the inspector both read this, so
// what is drawn and what is listed cannot disagree.
func (a *appState) stageGeometry() []stagePlacement {
	shown := a.stagePictures()
	if len(shown) == 0 {
		return nil
	}
	rects := layoutSlots(a.layout, len(shown))
	ids := make([]core.SourceID, 0, len(shown))
	slots := make(map[core.SourceID]stageBox, len(shown))
	for i, it := range shown {
		ids = append(ids, it.source.ID)
		slot := stageBox{X: rects[i].left / 100, Y: rects[i].top / 100, W: rects[i].width / 100, H: rects[i].height / 100}
		slots[it.source.ID] = slot
	}
	order := ids
	if edits := a.currentEdits(); edits != nil {
		order = edits.ordered(ids)
	}
	out := make([]stagePlacement, 0, len(shown))
	for _, id := range order {
		for _, it := range shown {
			if it.source.ID != id {
				continue
			}
			box, _ := a.boxOf(it, slots[id])
			out = append(out, stagePlacement{item: it, box: box})
			break
		}
	}
	return out
}

// enterStageEdit selects a picture the active layout actually places before
// enabling the handles. A stale selection may refer to a hidden or overflow
// source, so it is retained only while it remains in the stage geometry.
func (a *appState) enterStageEdit() {
	geometry := a.stageGeometry()
	if len(geometry) == 0 {
		a.editMode = false
		a.editNote = a.tr("Show a source on the stage to position it.", "先在舞台上显示一路画面，再调整位置。")
		a.invalidate()
		return
	}
	a.selected = stageEditSelection(a.selected, geometry)
	a.editMode = true
	a.editNote = a.tr("Drag the picture to move it, or drag a handle to resize it.", "拖动画面调整位置，拖动把手调整大小。")
	a.invalidate()
}

func (a *appState) toggleStageEdit() {
	if a.editMode {
		a.editMode = false
		a.editNote = ""
		a.invalidate()
		return
	}
	a.enterStageEdit()
}

func stageEditSelection(selected core.SourceID, geometry []stagePlacement) core.SourceID {
	for _, spot := range geometry {
		if itemKey(spot.item) == selected {
			return selected
		}
	}
	if len(geometry) == 0 {
		return ""
	}
	return itemKey(geometry[0].item)
}

// stagePlacement is one picture where the stage will draw it.
type stagePlacement struct {
	item *item
	box  stageBox
}

// stageEditing draws the handles and frames of the edit mode above the
// pictures. The pictures themselves stay interactive-free so the stage hears
// clicks; this layer is what turns a press into a selection or a drag.
func (a *appState) stageEditing(c *ui.Context, geometry []stagePlacement, sizeW, sizeH float32) {
	if sizeW < 1 || sizeH < 1 {
		return
	}
	// A press is held by exactly one element; if none of them reports being
	// held this frame, the gesture is over. There is no release callback in
	// the frame model, so the press state itself is the signal.
	a.drag.held = false
	editing := a.editMode
	for i, spot := range geometry {
		it := spot.item
		box := spot.box
		px := boxPixels(box, sizeW, sizeH)
		selected := a.stageSelected(it)
		frame := ui.Box(c).Absolute().Left(px.X).Top(px.Y).Width(px.W).Height(px.H).
			ZIndex(100 + i)
		if editing {
			// Outside the edit mode the stage stays clean: no frame, no
			// handles, no tooltip sitting over the picture.
			frame = frame.Border(1, colorLine)
			if selected {
				frame = frame.Border(2, colorAccent)
			}
		}
		frame = frame.Children(func() {
			if it.player == nil || !it.shown {
				return
			}
			c.Key("stage-shot-" + string(it.source.ID))
		})
		if !selected || !editing {
			continue
		}
		locked := a.stageLocked(it.source.ID)
		for _, h := range handleKinds {
			kind := h
			hx, hy := box.handlePoint(kind)
			hpx := handleBox(hx*sizeW, hy*sizeH)
			handle := ui.Box(c).Absolute().Left(hpx.X).Top(hpx.Y).Width(hpx.W).Height(hpx.H).
				ZIndex(200 + i).Background(colorAccent).Radius(radiusSm)
			c.Key("stage-handle-" + string(it.source.ID) + "-" + kind.String())
			if locked {
				handle = handle.Background(colorDim)
			}
			// The handle is drawn where the press is tested; it takes no input
			// of its own, so a click that lands a pixel beside it still works.
			handle = handle.IgnoreInput(true)
		}
		if locked {
			ui.Box(c).Absolute().Left(px.X).Top(px.Y + 6).Width(px.W).Height(18).
				ZIndex(300 + i).Children(func() {
				ui.Text(c, "locked").FontSize(textMicro).TextColor(colorTextDim).Center()
			})
		}
	}
	// One transparent layer over the whole stage carries the pointer: a press
	// selects the picture under it, a drag moves or resizes that picture, and
	// the picture elements themselves stay free of input. A gesture that
	// starts inside a picture but leaves its rectangle must keep moving it,
	// which is why the layer, not the picture, owns the press.
	c.Key("stage-input")
	input := ui.Box(c).Absolute().Left(0).Top(0).Width(sizeW).Height(sizeH).ZIndex(150)
	mx, my, onStage := input.PointerPosition()
	if onStage {
		a.hoverX, a.hoverY = mx/sizeW, my/sizeH
		a.hoverKnown = true
	}
	if input.Pressed() && onStage && a.hoverKnown {
		a.drag.held = true
		// Pressed() stays true for as long as the pointer is down, so the
		// gesture is started once, not once per frame.
		if !a.drag.active {
			spot, ok := stagePressTarget(geometry, a.selected, editing && !a.stageLocked(a.selected), a.hoverX, a.hoverY)
			if ok {
				a.selectFromStage(spot.item)
				editing = a.editMode
				kind := handleNone
				if editing {
					kind = spot.box.handleAt(a.hoverX, a.hoverY)
				}
				a.drag = dragState{active: true, id: spot.item.source.ID, handle: kind,
					box: spot.box, stageW: sizeW, stageH: sizeH}
			}
		}
	}
	if input.DoubleClicked() {
		a.enterViewing(viewFullscreen, a.windowRef())
	}
	if dx, dy, dragging := input.Dragged(); dragging && editing && a.drag.active {
		// The engine reports the movement of this frame, not the total since
		// the press, so it is applied as it arrives.
		if it := a.find(a.drag.id); it != nil && (dx != 0 || dy != 0) {
			a.dragByIn(it, a.drag.handle, dx/sizeW, dy/sizeH, sizeW, sizeH)
		}
	}
	// A press that brings neither a move nor a new press is over, but one
	// quiet frame is not enough: the pointer can rest for a frame mid-drag.
	if a.drag.held {
		a.drag.idle = 0
	} else if a.drag.active {
		a.drag.idle++
		if a.drag.idle >= 3 {
			a.drag.active = false
			a.drag.idle = 0
		}
	}
	if a.editNote != "" {
		ui.Box(c).Absolute().Left(8).Top(sizeH - 26).Width(max32(200, sizeW-16)).ZIndex(400).
			Children(func() {
				ui.Text(c, a.editNote).FontSize(textMicro).TextColor(colorTextDim).SingleLine()
			})
	}
}

// stagePressTarget keeps canvas selection intuitive in overlaps: the topmost
// picture receives a normal click, while a visible handle on the selected
// picture remains directly targetable even when another picture covers it.
// The source list can therefore select a lower picture and its handles can
// still start a resize without changing the stacking order.
func stagePressTarget(geometry []stagePlacement, selected core.SourceID, editing bool, x, y float32) (stagePlacement, bool) {
	if editing && selected != "" {
		for _, spot := range geometry {
			if itemKey(spot.item) == selected && spot.box.handleAt(x, y) != handleNone {
				return spot, true
			}
		}
	}
	return topmostAt(geometry, x, y)
}

// topmostAt reports the last (topmost) picture whose box holds the point.
func topmostAt(geometry []stagePlacement, x, y float32) (stagePlacement, bool) {
	for i := len(geometry) - 1; i >= 0; i-- {
		box := geometry[i].box
		if x >= box.X && x <= box.X+box.W && y >= box.Y && y <= box.Y+box.H {
			return geometry[i], true
		}
	}
	return stagePlacement{}, false
}

// handleBox centers a 10 DIP handle on the point it grabs.
func handleBox(x, y float32) stageBox {
	const size = 10
	return stageBox{X: x - size/2, Y: y - size/2, W: size, H: size}
}

// boxPixels turns a box in stage fractions into pixels within the frame.
func boxPixels(b stageBox, w, h float32) stageBox {
	return stageBox{X: b.X * w, Y: b.Y * h, W: b.W * w, H: b.H * h}
}

// selectFromStage makes a picture the inspector's subject. It deliberately
// does not touch the scene's audio: selecting a picture is not routing sound.
func (a *appState) selectFromStage(it *item) {
	a.selected = it.source.ID
	a.boxFields = boxFields{}
	if !a.editMode {
		a.editMode = true
		a.editNote = a.tr("Drag the picture to move it, or drag a handle to resize it.", "拖动画面调整位置，拖动把手调整大小。")
	}
	a.invalidate()
}

// selectStageSource keeps list selection useful even for a source that has
// no picture to edit in the current arrangement.
func (a *appState) selectStageSource(it *item) {
	for _, spot := range a.stageGeometry() {
		if spot.item == it {
			a.selectFromStage(it)
			return
		}
	}
	a.selected = itemKey(it)
	a.boxFields = boxFields{}
	a.editNote = ""
	if it.shown {
		a.editMode = true
		a.editNote = a.tr("Choose Free layout to place this picture.", "切换到「自由布局」即可摆放这路画面。")
	}
	a.invalidate()
}

// dragBy applies one frame of movement or resize to the gesture in progress.
// The box is updated live, so the picture follows the hand; there is no
// separate commit and no second draw path.
func (a *appState) dragBy(it *item, h handleKind, dx, dy float32) {
	w, hh := a.stageFrameSize()
	a.dragByIn(it, h, dx, dy, w, hh)
}

// stageFrameSize is the stage size the last frame drew at. A gesture keeps the
// size it started at, for the reason dragState states.
func (a *appState) stageFrameSize() (float32, float32) {
	return a.stageW, a.stageH
}

// find returns the item of a source, or nil when it is gone.
func (a *appState) find(id core.SourceID) *item {
	for _, it := range a.items {
		if it.source.ID == id {
			return it
		}
	}
	return nil
}

// shownIDs are the sources the scene currently shows, in scene order. The
// stacking order is kept over this list rather than over the stage's limited
// slots: a picture that a preset cannot place still keeps its layer.
func (a *appState) shownIDs() []core.SourceID {
	ids := make([]core.SourceID, 0, len(a.items))
	for _, it := range a.items {
		if it.player != nil && it.shown {
			ids = append(ids, it.source.ID)
		}
	}
	return ids
}

// dragByIn is dragBy with an explicit stage size, so a test can drive a
// gesture without a window.
func (a *appState) dragByIn(it *item, h handleKind, dx, dy float32, stageW, stageH float32) {
	if it == nil || stageW < 1 || stageH < 1 {
		return
	}
	if a.stageLocked(it.source.ID) {
		return
	}
	if !a.drag.active || a.drag.id != it.source.ID || a.drag.handle != h {
		// A new gesture: where the picture is now, at the size the stage had
		// when the press landed.
		geometry := a.stageGeometry()
		var from stageBox
		found := false
		for _, spot := range geometry {
			if spot.item.source.ID == it.source.ID {
				from, found = spot.box, true
				break
			}
		}
		if !found {
			return
		}
		a.drag = dragState{active: true, id: it.source.ID, handle: h, box: from, stageW: stageW, stageH: stageH}
	}
	next := a.drag.box
	ok := true
	if h == handleNone {
		next = next.movedBy(dx, dy)
	} else {
		aspect := a.pictureAspect(it, next, stageW, stageH)
		next, ok = next.resizedBy(h, dx, dy, aspect, !a.freeRatio)
	}
	if !ok {
		return
	}
	a.drag.box = next
	a.drag.handle = h
	a.drag.moved = true
	// The pointer is still down: a frame that brings a move is a frame of the
	// same gesture, which is what keeps the selection's state readable.
	a.drag.held = true
	edits := a.sceneEdits()
	edits.setBox(it.source.ID, next)
	// The inspector's own fields show what was typed, so a gesture has to
	// make them read the box again rather than keep the old numbers.
	a.boxFields.loaded = false
	a.editNote = fmt.Sprintf("%s  x %.0f%%  y %.0f%%  w %.0f%%  h %.0f%%",
		a.name(it), next.X*100, next.Y*100, next.W*100, next.H*100)
	a.invalidate()
	a.saveSession()
}

// fitStage makes a picture cover the whole stage, keeping its own shape: the
// box becomes the stage and the picture fits inside it.
func (a *appState) fitStage(id core.SourceID) {
	edits := a.sceneEdits()
	edits.setBox(id, stageBox{X: 0, Y: 0, W: 1, H: 1})
	a.invalidate()
	a.saveSession()
}

// resetStage forgets every rectangle and lock in this scene, which puts the
// pictures back where the layout preset puts them.
func (a *appState) resetStage() {
	a.forgetPendingStageEdits(a.scene)
	delete(a.edits, a.scene)
	a.drag = dragState{}
	a.boxFields = boxFields{}
	a.editNote = "stage reset to the layout"
	a.invalidate()
	a.saveSession()
}

// pruneStageEdits drops rectangles and locks for sources that no longer
// exist, so a removed source cannot come back with its old place.
func (a *appState) pruneStageEdits() {
	if a.edits == nil {
		return
	}
	known := make(map[core.SourceID]bool, len(a.items))
	for _, it := range a.items {
		known[it.source.ID] = true
	}
	for scene, edits := range a.edits {
		for id := range edits.boxes {
			if !known[id] {
				edits.forget(id)
			}
		}
		for id := range edits.locked {
			if !known[id] {
				delete(edits.locked, id)
			}
		}
		kept := edits.order[:0]
		for _, id := range edits.order {
			if known[id] {
				kept = append(kept, id)
			}
		}
		edits.order = kept
		if len(edits.boxes) == 0 && len(edits.order) == 0 && len(edits.locked) == 0 {
			delete(a.edits, scene)
		}
	}
}

// nudgeSelected moves the selected picture by one step, in stage fractions.
// The arrow keys reach it from the workbench, so a picture can be placed
// without dragging; Shift makes the step larger.
func (a *appState) nudgeSelected(dx, dy float32) {
	if !a.editMode {
		return
	}
	it := a.find(a.selected)
	if it == nil {
		return
	}
	a.dragByIn(it, handleNone, dx, dy, a.stageW, a.stageH)
}

// editControls is the picture tab's stage section: four numbers, the lock,
// fit and reset commands, and the stacking order. A picture can also be moved
// and resized by dragging it on the stage; the numbers are the precise path.
func (a *appState) editControls(c *ui.Context, it *item) {
	box, ok := a.stageBoxOf(it)
	if !ok {
		ui.Text(c, a.tr("This source is off the stage.", "这个来源不在舞台上。")).
			FontSize(textSmall).TextColor(colorDim).Wrap()
		if ui.Button(c.Key("box-put"), a.tr("Put it on the stage", "放到舞台上")).
			Height(a.controlHeight()).Clicked() {
			a.selectVideo(it)
		}
		return
	}
	if a.boxFields.forSource != it.source.ID || !a.boxFields.loaded {
		a.boxFields = boxFields{forSource: it.source.ID, loaded: true,
			x: fmt.Sprintf("%.2f", box.X), y: fmt.Sprintf("%.2f", box.Y),
			w: fmt.Sprintf("%.2f", box.W), h: fmt.Sprintf("%.2f", box.H)}
	}
	dirty := a.boxFields.x != fmt.Sprintf("%.2f", box.X) ||
		a.boxFields.y != fmt.Sprintf("%.2f", box.Y) ||
		a.boxFields.w != fmt.Sprintf("%.2f", box.W) ||
		a.boxFields.h != fmt.Sprintf("%.2f", box.H)
	ui.Row(c).WidthPercent(100).AlignItems(ui.Center).Gap(spaceSm).Children(func() {
		ui.Text(c, a.tr("Position", "位置与大小")).FontSize(textMicro).TextColor(colorDim)
		ui.Box(c).Grow(1)
		if dirty {
			ui.Text(c, a.tr("not applied", "未应用")).FontSize(textMicro).TextColor(colorWarn)
		}
	})
	fields := []struct {
		key   string
		label string
		value *string
	}{
		{"box-x", "X", &a.boxFields.x},
		{"box-y", "Y", &a.boxFields.y},
		{"box-w", "W", &a.boxFields.w},
		{"box-h", "H", &a.boxFields.h},
	}
	// Two rows of two. The label sits above the field, so a narrow column
	// never squeezes the number into an ellipsis.
	for row := 0; row < len(fields); row += 2 {
		ui.Row(c).Gap(spaceSm).WidthPercent(100).Children(func() {
			for _, field := range fields[row : row+2] {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, field.label).FontSize(textMicro).TextColor(colorDim)
					c.Key(field.key)
					ui.TextInput(c, field.value).Height(a.controlHeight() - 6)
				})
			}
		})
	}
	ui.Row(c).Gap(spaceXs).WidthPercent(100).Children(func() {
		apply := ui.Button(c.Key("box-apply"), a.tr("Apply", "搴旂敤")).Height(a.controlHeight() - 6)
		if !dirty {
			apply.Disabled(true)
		}
		if apply.Clicked() {
			a.applyBoxFields(it)
		}
		if ui.Button(c.Key("box-fit"), a.tr("Fit", "閫傚簲")).Height(a.controlHeight() - 6).Clicked() {
			a.fitStage(it.source.ID)
			a.reloadBoxFields(it)
		}
		if ui.Button(c.Key("box-reset"), a.tr("Reset", "閲嶇疆")).Height(a.controlHeight() - 6).Clicked() {
			a.forgetBox(it.source.ID)
			a.reloadBoxFields(it)
		}
	})
	ui.Row(c).Wrap().Gap(spaceXs).Padding(spaceXs, 0).Children(func() {
		ratio := a.tr("Keep ratio", "锁定比例")
		if a.freeRatio {
			ratio = a.tr("Any ratio", "鑷敱姣斾緥")
		}
		if ui.Button(c.Key("box-ratio"), ratio).Height(24).Padding(0, spaceXs).
			Tooltip(a.tr("drag a corner to resize", "拖角可以改大小")).Clicked() {
			a.freeRatio = !a.freeRatio
		}
		lock := a.tr("Lock", "锁定")
		if a.stageLocked(it.source.ID) {
			lock = a.tr("Unlock", "瑙ｉ攣")
		}
		if ui.Button(c.Key("box-lock"), lock).Height(24).Padding(0, spaceXs).Clicked() {
			a.toggleStageLock(it.source.ID)
		}
		front := a.tr("Front", "置顶")
		forward := a.tr("Up", "上移")
		backward := a.tr("Down", "下移")
		back := a.tr("Back", "置底")
		if ui.Button(c.Key("layer-front"), front).Height(24).Padding(0, spaceXs).Clicked() {
			a.raiseStage(it.source.ID, true)
		}
		if ui.Button(c.Key("layer-forward"), forward).Height(24).Padding(0, spaceXs).Clicked() {
			a.raiseStage(it.source.ID, false)
		}
		if ui.Button(c.Key("layer-backward"), backward).Height(24).Padding(0, spaceXs).Clicked() {
			a.lowerStage(it.source.ID, false)
		}
		if ui.Button(c.Key("layer-back"), back).Height(24).Padding(0, spaceXs).Clicked() {
			a.lowerStage(it.source.ID, true)
		}
	})
	if a.boxFields.note != "" {
		ui.Text(c, a.boxFields.note).FontSize(textMicro).TextColor(colorBad).Wrap()
	}
}

// stageBoxOf is the box the inspector reports for a picture.
func (a *appState) stageBoxOf(it *item) (stageBox, bool) {
	for _, spot := range a.stageGeometry() {
		if spot.item.source.ID == it.source.ID {
			return spot.box, true
		}
	}
	return stageBox{}, false
}

// reloadBoxFields puts the stored box back into the four inputs, which is what
// every command that changes the box does, so the numbers never lag behind.
func (a *appState) reloadBoxFields(it *item) {
	box, ok := a.stageBoxOf(it)
	if !ok {
		return
	}
	a.boxFields = boxFields{
		forSource: it.source.ID, loaded: true,
		x: fmt.Sprintf("%.2f", box.X), y: fmt.Sprintf("%.2f", box.Y),
		w: fmt.Sprintf("%.2f", box.W), h: fmt.Sprintf("%.2f", box.H),
	}
}

// forgetBox puts one picture back where its layout puts it.
func (a *appState) forgetBox(id core.SourceID) {
	if edits := a.currentEdits(); edits != nil {
		edits.forget(id)
	}
	a.invalidate()
	a.saveSession()
}

// applyBoxFields parses the four numbers and stores them, refusing anything
// that is not a number or a rectangle a person could still grab. A refusal
// leaves the last accepted box alone and says why.
func (a *appState) applyBoxFields(it *item) {
	parse := func(text string) (float32, bool) {
		text = strings.TrimSpace(strings.TrimSuffix(text, "%"))
		if text == "" {
			return 0, false
		}
		value, err := strconv.ParseFloat(text, 32)
		if err != nil {
			return 0, false
		}
		return float32(value), true
	}
	x, okX := parse(a.boxFields.x)
	y, okY := parse(a.boxFields.y)
	w, okW := parse(a.boxFields.w)
	h, okH := parse(a.boxFields.h)
	if !okX || !okY || !okW || !okH {
		a.boxFields.note = "each of x, y, w, h has to be a number"
		return
	}
	box := stageBox{X: x, Y: y, W: w, H: h}
	if !box.valid() {
		a.boxFields.note = "a box has to have a positive width and height"
		return
	}
	if box.W < minStageSide || box.H < minStageSide {
		a.boxFields.note = fmt.Sprintf("the smallest side is %.0f%% of the stage", minStageSide*100)
		return
	}
	a.sceneEdits().setBox(it.source.ID, box)
	a.boxFields.note = ""
	a.reloadBoxFields(it)
	a.invalidate()
	a.saveSession()
}

func ratioLabel(locked bool) string {
	if locked {
		return "Keep ratio"
	}
	return "Any ratio"
}

func lockLabel(locked bool) string {
	if locked {
		return "Unlock"
	}
	return "Lock"
}

// raiseStage puts one shown source in front of the others. The Sources page
// and the inspector both call it, so the stacking order a person sets there is
// the order the stage draws.
func (a *appState) raiseStage(id core.SourceID, toFront bool) {
	a.sceneEdits().raise(id, a.shownIDs(), toFront)
	a.clearPendingStageOrder(a.scene)
	a.invalidate()
	a.saveSession()
}

// lowerStage is the other direction of raiseStage.
func (a *appState) lowerStage(id core.SourceID, toBack bool) {
	a.sceneEdits().lower(id, a.shownIDs(), toBack)
	a.clearPendingStageOrder(a.scene)
	a.invalidate()
	a.saveSession()
}
