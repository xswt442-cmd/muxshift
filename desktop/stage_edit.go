package main

import (
	"math"

	"github.com/xswt442-cmd/muxshift/core"
)

// stageBox is one picture's rectangle on the stage, in fractions of the
// stage: (0,0,1,1) covers it exactly. Fractions rather than pixels because
// the stage is measured every frame and the window can be resized or run on
// another display scale; a stored layout stays the same shape either way.
//
// The box belongs to the picture a scene shows, not to the source: the same
// source placed in another scene keeps its own rectangle there.
type stageBox struct {
	X float32
	Y float32
	W float32
	H float32
}

const (
	// minStageSide is the smallest side a picture may be resized to, as a
	// fraction of the stage. Smaller than this and the handles overlap the
	// picture so much that it can no longer be found again.
	minStageSide = 0.05
	// keepOnCanvas is how much of a picture has to stay on the stage. A
	// picture dragged past an edge keeps this sliver visible, so it can be
	// dragged back instead of being lost outside the canvas.
	keepOnCanvas = 0.04
	// handleReach is how far from a handle, in stage fractions, a press still
	// counts as that handle. It is generous on purpose: the handles are small
	// and a person aims at a corner, not at a pixel.
	handleReach = 0.05
)

// handles are the eight directions a picture can be resized from.
type handleKind int8

const (
	handleNone handleKind = iota
	handleNW
	handleN
	handleNE
	handleE
	handleSE
	handleS
	handleSW
	handleW
)

var handleKinds = []handleKind{
	handleNW, handleN, handleNE, handleE, handleSE, handleS, handleSW, handleW,
}

func (h handleKind) String() string {
	switch h {
	case handleNW:
		return "nw"
	case handleN:
		return "n"
	case handleNE:
		return "ne"
	case handleE:
		return "e"
	case handleSE:
		return "se"
	case handleS:
		return "s"
	case handleSW:
		return "sw"
	case handleW:
		return "w"
	}
	return "none"
}

// movesX reports whether the handle drags a side, not only a corner.
func (h handleKind) movesLeft() bool  { return h == handleNW || h == handleW || h == handleSW }
func (h handleKind) movesRight() bool { return h == handleNE || h == handleE || h == handleSE }
func (h handleKind) movesTop() bool   { return h == handleNW || h == handleN || h == handleNE }
func (h handleKind) movesBottom() bool {
	return h == handleSW || h == handleS || h == handleSE
}

// handlePoint is where a handle sits on the box, in the same fractions.
func (b stageBox) handlePoint(h handleKind) (float32, float32) {
	x, y := b.X+b.W/2, b.Y+b.H/2
	if h.movesLeft() {
		x = b.X
	}
	if h.movesRight() {
		x = b.X + b.W
	}
	if h.movesTop() {
		y = b.Y
	}
	if h.movesBottom() {
		y = b.Y + b.H
	}
	return x, y
}

// valid reports whether the box is a rectangle a person could see. A drag
// that would divide by zero, or a resize that would turn the picture inside
// out, is refused instead of stored.
func (b stageBox) valid() bool {
	for _, v := range []float32{b.X, b.Y, b.W, b.H} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return false
		}
	}
	return b.W > 0 && b.H > 0
}

// normalized clamps a box to something a person can still grab.
func (b stageBox) normalized() stageBox {
	if !b.valid() {
		return stageBox{X: 0, Y: 0, W: 1, H: 1}
	}
	if b.W < minStageSide {
		b.W = minStageSide
	}
	if b.H < minStageSide {
		b.H = minStageSide
	}
	if b.W > 1 {
		b.W = 1
	}
	if b.H > 1 {
		b.H = 1
	}
	// A picture stays inside the stage: a box that fits must be wholly
	// visible, so it can never cover the bars around the stage. Only a box
	// wider or taller than the stage keeps a sliver on the canvas.
	if b.W <= 1 {
		b.X = clampFloat(b.X, 0, 1-b.W)
	} else if b.X < -b.W+keepOnCanvas {
		b.X = -b.W + keepOnCanvas
	}
	if b.H <= 1 {
		b.Y = clampFloat(b.Y, 0, 1-b.H)
	} else if b.Y < -b.H+keepOnCanvas {
		b.Y = -b.H + keepOnCanvas
	}
	return b
}

// hit reports whether the point, in stage fractions, is inside the box.
func (b stageBox) hit(x, y float32) bool {
	return x >= b.X && x <= b.X+b.W && y >= b.Y && y <= b.Y+b.H
}

// movedBy returns the box moved by a delta in stage fractions. The result
// stays normalized: a picture can be pushed past an edge but not lost.
func (b stageBox) movedBy(dx, dy float32) stageBox {
	if math.IsNaN(float64(dx)) || math.IsNaN(float64(dy)) {
		return b
	}
	out := b
	out.X = b.X + dx
	out.Y = b.Y + dy
	return out.normalized()
}

// resizedBy drags one handle by a delta in stage fractions. With ratio set,
// the other side follows the picture's own aspect so the picture is not
// stretched. A resize that would take a side under minStageSide is refused
// (ok false) rather than clamped, so a drag past the limit stops moving
// instead of jumping.
func (b stageBox) resizedBy(h handleKind, dx, dy float32, aspect float32, ratio bool) (stageBox, bool) {
	if h == handleNone {
		return b, false
	}
	if math.IsNaN(float64(dx)) || math.IsNaN(float64(dy)) ||
		math.IsInf(float64(dx), 0) || math.IsInf(float64(dy), 0) {
		return b, false
	}
	left, top := b.X, b.Y
	right, bottom := b.X+b.W, b.Y+b.H
	if h.movesLeft() {
		left += dx
	}
	if h.movesRight() {
		right += dx
	}
	if h.movesTop() {
		top += dy
	}
	if h.movesBottom() {
		bottom += dy
	}
	// A drag that carried an edge past its opposite is refused, not flipped:
	// a picture turned inside out is not what the hand meant.
	if h.movesLeft() && left > right {
		return b, false
	}
	if h.movesRight() && right < left {
		return b, false
	}
	if h.movesTop() && top > bottom {
		return b, false
	}
	if h.movesBottom() && bottom < top {
		return b, false
	}
	w, hh := abs32(right-left), abs32(bottom-top)
	if ratio && aspect > 0 {
		// The picture keeps its own shape: the side that moved decides and
		// the other follows. The edge or corner opposite the grabbed one
		// stays where it is, so the picture grows away from the hand.
		movedX := h.movesLeft() || h.movesRight()
		movedY := h.movesTop() || h.movesBottom()
		switch {
		case movedX && movedY:
			// Corner: the larger candidate wins, so neither axis feels stuck.
			if w/aspect >= hh {
				hh = w / aspect
			} else {
				w = hh * aspect
			}
		case movedX:
			hh = w / aspect
		case movedY:
			w = hh * aspect
		}
	}
	if w < minStageSide || hh < minStageSide {
		return b, false
	}
	// A picture cannot grow past the stage: when a drag asks for one, both
	// sides shrink together so the shape the hand asked for is kept.
	if w > 1 || hh > 1 {
		scale := min32(1/w, 1/hh)
		w *= scale
		hh *= scale
	}
	var x float32
	switch {
	case h.movesLeft():
		x = right - w
	case h.movesRight():
		x = left
	default:
		x = b.X + (b.W-w)/2
	}
	var y float32
	switch {
	case h.movesTop():
		y = bottom - hh
	case h.movesBottom():
		y = top
	default:
		y = b.Y + (b.H-hh)/2
	}
	out := stageBox{X: x, Y: y, W: w, H: hh}
	if !out.valid() {
		return b, false
	}
	return out.normalized(), true
}

// handleAt names the handle a press at (x,y) grabs, or handleNone when the
// press is on the picture itself.
func (b stageBox) handleAt(x, y float32) handleKind {
	best := handleNone
	bestDist := float32(handleReach)
	for _, h := range handleKinds {
		hx, hy := b.handlePoint(h)
		dx, dy := x-hx, y-hy
		if d := max32(abs32(dx), abs32(dy)); d <= bestDist {
			best, bestDist = h, d
		}
	}
	return best
}

// stageEdits is one scene's own arrangement: the rectangles a person set, and
// the stacking order they chose. A source with no rectangle keeps the place
// its layout preset gives it; the two mechanisms do not fight, the explicit
// one simply wins for the sources it names.
type stageEdits struct {
	boxes  map[core.SourceID]stageBox
	order  []core.SourceID
	locked map[core.SourceID]bool
}

func newStageEdits() stageEdits {
	return stageEdits{
		boxes:  map[core.SourceID]stageBox{},
		locked: map[core.SourceID]bool{},
	}
}

// boxFor is the rectangle a picture occupies: the one a person set, or the
// layout preset's slot. ok is false when the layout does not place it at
// all, which the stage draws as "not on stage".
func (e *stageEdits) boxFor(id core.SourceID, fallback stageBox) (stageBox, bool) {
	if e != nil {
		if box, ok := e.boxes[id]; ok {
			return box.normalized(), true
		}
	}
	if fallback.W <= 0 || fallback.H <= 0 {
		return stageBox{}, false
	}
	return fallback.normalized(), true
}

// setBox stores the rectangle a person dragged out, and remembers the source
// in the stacking order so the order stays complete.
func (e *stageEdits) setBox(id core.SourceID, box stageBox) {
	if !box.valid() {
		return
	}
	box = box.normalized()
	if e.boxes == nil {
		e.boxes = map[core.SourceID]stageBox{}
	}
	if existing, ok := e.boxes[id]; ok && almostEqual(existing, box) {
		return
	}
	e.boxes[id] = box
	e.remember(id)
}

// forget drops a source's rectangle, putting it back where the layout puts
// it, and takes it out of the order.
func (e *stageEdits) forget(id core.SourceID) {
	if e == nil {
		return
	}
	delete(e.boxes, id)
	delete(e.locked, id)
	for i, other := range e.order {
		if other == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			return
		}
	}
}

// remember puts a source at the front of the order when it is not in it yet.
func (e *stageEdits) remember(id core.SourceID) {
	for _, other := range e.order {
		if other == id {
			return
		}
	}
	e.order = append(e.order, id)
}

// ordered returns the given sources back to front, using the explicit order
// first and the caller's order for the rest. Front is the last entry.
func (e *stageEdits) ordered(sources []core.SourceID) []core.SourceID {
	if e == nil || len(e.order) == 0 {
		return append([]core.SourceID(nil), sources...)
	}
	present := make(map[core.SourceID]bool, len(sources))
	for _, id := range sources {
		present[id] = true
	}
	out := make([]core.SourceID, 0, len(sources))
	used := map[core.SourceID]bool{}
	for _, id := range e.order {
		if present[id] && !used[id] {
			out = append(out, id)
			used[id] = true
		}
	}
	for _, id := range sources {
		if !used[id] {
			out = append(out, id)
		}
	}
	return out
}

// raise moves a source one step towards the front; front sends it all the way.
// The list of sources currently shown is the base, so a source that was never
// moved before starts from the order the caller sees.
func (e *stageEdits) raise(id core.SourceID, sources []core.SourceID, toFront bool) {
	e.move(id, sources, true, toFront)
}

// lower moves a source one step towards the back; toBack sends it all the way.
func (e *stageEdits) lower(id core.SourceID, sources []core.SourceID, toBack bool) {
	e.move(id, sources, false, toBack)
}

func (e *stageEdits) move(id core.SourceID, sources []core.SourceID, forward, allTheWay bool) {
	if e == nil {
		return
	}
	order := e.ordered(sources)
	at := -1
	for i, other := range order {
		if other == id {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	if allTheWay {
		order = append(order[:at], order[at+1:]...)
		if forward {
			order = append(order, id)
		} else {
			order = append([]core.SourceID{id}, order...)
		}
		e.order = order
		return
	}
	swap := at + 1
	if !forward {
		swap = at - 1
	}
	if swap < 0 || swap >= len(order) {
		e.order = order
		return
	}
	order[at], order[swap] = order[swap], order[at]
	e.order = order
}

func almostEqual(a, b stageBox) bool {
	const eps = 0.0005
	return abs32(a.X-b.X) < eps && abs32(a.Y-b.Y) < eps &&
		abs32(a.W-b.W) < eps && abs32(a.H-b.H) < eps
}

func clampFloat(v, low, high float32) float32 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
