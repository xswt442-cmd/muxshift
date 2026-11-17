package main

// The stage can arrange the pictures the scene shows in five named ways. The
// layout decides rectangles, stacking and which source owns the largest slot;
// it never decides what plays: sound, players, sync groups and the event clock
// stay exactly where the scene put them.

// layoutKind names an arrangement. The zero value is the editing default, the
// one picture filling the stage.
type layoutKind int8

const (
	layoutFocus layoutKind = iota // one picture, whole stage
	layoutSplit                   // two pictures, side by side
	layoutPip                     // one picture with the second in a corner
	layoutGrid                    // up to four pictures, two by two
	layoutHero                    // one large picture, two smaller on the right
)

var layoutKinds = []layoutKind{layoutFocus, layoutSplit, layoutPip, layoutGrid, layoutHero}

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
	if max := kind.slots(); n > max {
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
	default:
		return []slotRect{{0, 0, 100, 100, 0}}
	}
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
