package main

import (
	"math"
	"testing"
)

// closeEnough compares the percentages the layouts produce.
func closeEnough(got, want float32) bool {
	return math.Abs(float64(got-want)) < 0.01
}

func inStage(r slotRect) bool {
	return r.left >= -0.01 && r.top >= -0.01 &&
		r.left+r.width <= 100.01 && r.top+r.height <= 100.01
}

func TestFocusAlwaysFillsTheStage(t *testing.T) {
	for _, n := range []int{1, 2, 4} {
		slots := layoutSlots(layoutFocus, n)
		if len(slots) != 1 {
			t.Fatalf("focus with %d shown placed %d slots, want 1", n, len(slots))
		}
		if slots[0] != (slotRect{0, 0, 100, 100, 0}) {
			t.Fatalf("focus placed %+v, want the whole stage", slots[0])
		}
	}
	if got := layoutSlots(layoutFocus, 0); got != nil {
		t.Fatalf("focus with nothing shown placed %+v, want nothing", got)
	}
}

func TestSplitGivesEqualColumns(t *testing.T) {
	slots := layoutSlots(layoutSplit, 2)
	if len(slots) != 2 {
		t.Fatalf("split placed %d slots, want 2", len(slots))
	}
	left, right := slots[0], slots[1]
	if !closeEnough(left.height, 100) || !closeEnough(right.height, 100) {
		t.Fatalf("split heights %v and %v, want the full stage", left.height, right.height)
	}
	if !closeEnough(left.width, right.width) {
		t.Fatalf("split widths differ: %v vs %v", left.width, right.width)
	}
	if left.left != 0 {
		t.Fatalf("split starts at %v, want 0", left.left)
	}
	if !closeEnough(left.left+left.width, 50-0.5) || !closeEnough(right.left, 50.5) {
		t.Fatalf("split columns are %+v and %+v", left, right)
	}
	if left.z != 0 || right.z != 0 {
		t.Fatalf("split stacked at %d and %d, want both at the bottom", left.z, right.z)
	}
}

func TestPipFloatsTheSecondPicture(t *testing.T) {
	slots := layoutSlots(layoutPip, 2)
	if len(slots) != 2 {
		t.Fatalf("pip placed %d slots, want 2", len(slots))
	}
	main, small := slots[0], slots[1]
	if main != (slotRect{0, 0, 100, 100, 0}) {
		t.Fatalf("pip main is %+v, want the whole stage below", main)
	}
	if small.z <= main.z {
		t.Fatalf("pip small is stacked at %d, main at %d", small.z, main.z)
	}
	if small.width > 40 || small.height > 40 {
		t.Fatalf("pip small is %vx%v, too big", small.width, small.height)
	}
	if !inStage(small) {
		t.Fatalf("pip small %+v leaves the stage", small)
	}
	if !closeEnough(small.left+small.width, 98) || !closeEnough(small.top+small.height, 98) {
		t.Fatalf("pip small is not in the lower right corner: %+v", small)
	}
	if got := layoutSlots(layoutPip, 1); len(got) != 1 {
		t.Fatalf("pip with one shown placed %d slots, want 1", len(got))
	}
}

func TestGridFillsCellsInReadingOrder(t *testing.T) {
	slots := layoutSlots(layoutGrid, 4)
	if len(slots) != 4 {
		t.Fatalf("grid placed %d slots, want 4", len(slots))
	}
	wantLeft := []float32{0, 50.5, 0, 50.5}
	wantTop := []float32{0, 0, 50.5, 50.5}
	for i, s := range slots {
		if !closeEnough(s.left, wantLeft[i]) || !closeEnough(s.top, wantTop[i]) {
			t.Fatalf("grid slot %d is at %v,%v; want %v,%v", i, s.left, s.top, wantLeft[i], wantTop[i])
		}
		if s.z != 0 {
			t.Fatalf("grid slot %d is stacked at %d, want 0", i, s.z)
		}
		if !inStage(s) {
			t.Fatalf("grid slot %d %+v leaves the stage", i, s)
		}
	}
	if len(layoutSlots(layoutGrid, 2)) != 2 {
		t.Fatal("grid with two sources should place two slots, not repeat them")
	}
	if len(layoutSlots(layoutGrid, 9)) != 4 {
		t.Fatal("grid cannot place more than four pictures")
	}
}

func TestHeroStacksTheSidePictures(t *testing.T) {
	slots := layoutSlots(layoutHero, 3)
	if len(slots) != 3 {
		t.Fatalf("hero placed %d slots, want 3", len(slots))
	}
	hero, a, b := slots[0], slots[1], slots[2]
	if !closeEnough(hero.width, 66) || !closeEnough(hero.height, 100) {
		t.Fatalf("hero is %vx%v, want two thirds of the stage", hero.width, hero.height)
	}
	if hero.z != 0 || a.z != 1 || b.z != 1 {
		t.Fatalf("hero stacking is %d/%d/%d", hero.z, a.z, b.z)
	}
	if !closeEnough(a.left, 67) || !closeEnough(b.left, 67) {
		t.Fatalf("side pictures start at %v and %v, want 67", a.left, b.left)
	}
	if !closeEnough(a.height, b.height) {
		t.Fatalf("side heights differ: %v vs %v", a.height, b.height)
	}
	if a.top != 0 || !closeEnough(b.top, 50.5) {
		t.Fatalf("side pictures sit at %v and %v", a.top, b.top)
	}
	for _, s := range slots {
		if !inStage(s) {
			t.Fatalf("hero slot %+v leaves the stage", s)
		}
	}
	if got := layoutSlots(layoutHero, 1); len(got) != 1 || got[0] != (slotRect{0, 0, 100, 100, 0}) {
		t.Fatalf("hero with one source placed %+v, want a full stage", got)
	}
}

func TestLayoutNamesRoundTrip(t *testing.T) {
	for _, k := range layoutKinds {
		if got := parseLayout(k.String()); got != k {
			t.Fatalf("%q parsed as %v, want %v", k.String(), got, k)
		}
		if k.label() == "" {
			t.Fatalf("%v has no button label", k)
		}
	}
	if got := parseLayout("nonsense"); got != layoutFocus {
		t.Fatalf("an unknown name parsed as %v, want focus", got)
	}
}
