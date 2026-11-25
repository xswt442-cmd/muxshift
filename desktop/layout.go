package main

// The stage can arrange the pictures the scene shows in five named ways. The
// layout decides rectangles, stacking and which source owns the largest slot;
// it never decides what plays: sound, players, sync groups and the event clock
// stay exactly where the scene put them.
//
// The contract with the scene (P11.1):
//
//   - The scene's video tracks, in scene order, are the pictures the stage may
//     show; a source is on the stage when the scene lists its video track, which
//     is what the Show/Hide button writes through SetSceneVideo.
//   - A layout only reads that order. The first source a person marked Main
//     goes first, the rest keep scene order, and the arrangement takes as many
//     as its slots hold (1, 2, 2, 4, 3). Sources beyond that keep playing and
//     keep their audio; they simply have no rectangle.
//   - Slots are normalized percentages of the stage box and are turned into
//     pixels against the box measured a frame earlier, so a resize or an entry
//     into full screen changes rectangles, never the scene's geometry.
//   - Nothing about the arrangement is persisted: a session or a settings file
//     from before P11 opens on Focus, the single picture filling the stage.

// layoutKind names an arrangement. The zero value is the editing default, the
// one picture filling the stage.
type layoutKind int8

const (
	layoutFocus layoutKind = iota // one picture, whole stage
	layoutSplit                   // two pictures, side by side
	layoutPip                     // one picture with the second in a corner
	layoutGrid                    // up to four pictures, two by two
	layoutHero                    // one large picture, two smaller on the right
	layoutFree                    // every picture gets an independently editable place
)

var layoutKinds = []layoutKind{layoutFocus, layoutSplit, layoutPip, layoutGrid, layoutHero, layoutFree}

func (k layoutKind) String() string {
	switch k {
	case layoutSplit:
		return "split"
	case layoutPip:
		return "pip"
	case layoutGrid:
		return "grid"
	case layoutHero:
		return "hero"
	case layoutFree:
		return "free"
	default:
		return "focus"
	}
}

// label is what the stage's own buttons say.
func (k layoutKind) label() string {
	switch k {
	case layoutSplit:
		return "Split"
	case layoutPip:
		return "PiP"
	case layoutGrid:
		return "Grid"
	case layoutHero:
		return "Hero"
	case layoutFree:
		return "Free"
	default:
		return "Focus"
	}
}

// slots is how many pictures this arrangement can place. A scene that shows
// more sources than that keeps the extra ones off the stage; the stage says so
// instead of drawing one picture twice.
func (k layoutKind) slots() int {
	switch k {
	case layoutSplit, layoutPip:
		return 2
	case layoutGrid:
		return 4
	case layoutHero:
		return 3
	case layoutFree:
		return 0 // Free has no preset slot limit.
	default:
		return 1
	}
}

// parseLayout reads a stored or typed name, falling back to focus.
func parseLayout(name string) layoutKind {
	for _, k := range layoutKinds {
		if k.String() == name {
			return k
		}
	}
	return layoutFocus
}

// slotRect is one rectangle in stage coordinates: percentages of the stage's
// width and height, so the same layout holds at every window size and in every
// watching mode. z is the stacking order inside the stage, lowest first.
type slotRect struct {
	left, top, width, height float32
	z                        int
}

// layoutSlots places up to n pictures: the first n slots of the arrangement.
// n is clamped to what the arrangement can hold. The result is pure geometry:
// no source, no player, no sound.
func layoutSlots(kind layoutKind, n int) []slotRect {
	if n < 0 {
		n = 0
	}
	if n == 0 {
		return nil
	}
	if max := kind.slots(); max > 0 && n > max {
		n = max
	}
	switch kind {
	case layoutSplit:
		return splitSlots(n)
	case layoutPip:
		return pipSlots(n)
	case layoutGrid:
		return gridSlots(n)
	case layoutHero:
		return heroSlots(n)
	case layoutFree:
		return freeSlots(n)
	default:
		return []slotRect{{0, 0, 100, 100, 0}}
	}
}

// freeSlots gives every shown picture a place, regardless of how many a
// preset can hold. These are only initial positions: explicit scene edits
// take precedence and remain stored when the user switches presets.
func freeSlots(n int) []slotRect {
	if n <= 0 {
		return nil
	}
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols
	gap := min(float32(1), 20/float32(cols))
	cell := (100 - gap*float32(cols-1)) / float32(cols)
	top := (100 - float32(rows)*cell - float32(rows-1)*gap) / 2
	out := make([]slotRect, n)
	for i := range out {
		col, row := i%cols, i/cols
		out[i] = slotRect{
			left: float32(col) * (cell + gap), top: top + float32(row)*(cell+gap),
			width: cell, height: cell,
		}
	}
	return out
}

// splitSlots divides the stage into n equal columns with a hairline between
// them. Two shown sources give the plain left/right pair.
func splitSlots(n int) []slotRect {
	const gap float32 = 1
	width := (100 - gap*float32(n-1)) / float32(n)
	out := make([]slotRect, n)
	for i := range out {
		out[i] = slotRect{left: float32(i) * (width + gap), top: 0, width: width, height: 100}
	}
	return out
}

// pipSlots keeps the first picture full and floats the second in the lower
// right corner, small enough that the main picture stays readable.
func pipSlots(n int) []slotRect {
	out := []slotRect{{0, 0, 100, 100, 0}}
	if n < 2 {
		return out
	}
	const inset, width, height float32 = 2, 30, 26
	return append(out, slotRect{left: 100 - inset - width, top: 100 - inset - height, width: width, height: height, z: 1})
}

// gridSlots fills a two by two grid in reading order. Two pictures take the top
// row, three add the bottom left; the empty cells stay empty.
func gridSlots(n int) []slotRect {
	const gap float32 = 1
	width := (100 - gap) / 2
	height := (100 - gap) / 2
	positions := [4][2]float32{{0, 0}, {width + gap, 0}, {0, height + gap}, {width + gap, height + gap}}
	out := make([]slotRect, 0, n)
	for i := 0; i < n && i < len(positions); i++ {
		out = append(out, slotRect{left: positions[i][0], top: positions[i][1], width: width, height: height})
	}
	return out
}

// heroSlots gives the first picture the left two thirds and stacks the rest on
// the right. With one source it is simply a full stage.
func heroSlots(n int) []slotRect {
	if n < 2 {
		return []slotRect{{0, 0, 100, 100, 0}}
	}
	const gap float32 = 1
	heroWidth := (100 - gap) * 2 / 3
	sideWidth := (100 - gap) - heroWidth
	sideHeight := (100 - gap*float32(n-2)) / float32(n-1)
	out := []slotRect{{0, 0, heroWidth, 100, 0}}
	for i := 0; i < n-1; i++ {
		out = append(out, slotRect{
			left:   heroWidth + gap,
			top:    float32(i) * (sideHeight + gap),
			width:  sideWidth,
			height: sideHeight,
			z:      1,
		})
	}
	return out
}

// slotPixels turns one percentage slot into the pixel rectangle the stage
// draws in. A video element given a box of a different ratio letterboxes the
// picture in white, so the box itself takes the picture's shape and sits
// centered where the slot was: the space that is left shows the stage, not a
// bar of the wrong color.
func slotPixels(r slotRect, stageW, stageH float32, shapeW, shapeH int) slotRect {
	out := slotRect{
		left:   r.left / 100 * stageW,
		top:    r.top / 100 * stageH,
		width:  r.width / 100 * stageW,
		height: r.height / 100 * stageH,
		z:      r.z,
	}
	if shapeW <= 0 || shapeH <= 0 || out.width <= 0 || out.height <= 0 {
		return out
	}
	shape := float32(shapeW) / float32(shapeH)
	if out.width/out.height > shape {
		w := out.height * shape
		out.left += (out.width - w) / 2
		out.width = w
	} else {
		h := out.width / shape
		out.top += (out.height - h) / 2
		out.height = h
	}
	return out
}
