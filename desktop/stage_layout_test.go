package main

import "testing"

// TestStageLayoutSingleFillsTheStage keeps the one-source case exactly as it
// was: the picture fills the stage instead of becoming a column in a row of
// one, which would leave a border nobody asked for.
func TestStageLayoutSingleFillsTheStage(t *testing.T) {
	got := stageLayout(1)
	if len(got) != 1 {
		t.Fatalf("stageLayout(1) has %d rects, want 1", len(got))
	}
	want := stageRect{left: 0, top: 0, width: 100, height: 100}
	if got[0] != want {
		t.Fatalf("stageLayout(1) = %+v, want %+v", got[0], want)
	}
}

// TestStageLayoutPutsEachPictureSomewhereElse is the property the whole canvas
// rests on: with more than one source, every rect is a real rectangle inside
// the stage and two pictures never land on top of each other. A layout that
// overlapped would still "show two videos" in a capture while one hid the
// other.
func TestStageLayoutPutsEachPictureSomewhereElse(t *testing.T) {
	for n := 2; n <= 5; n++ {
		rects := stageLayout(n)
		if len(rects) != n {
			t.Fatalf("stageLayout(%d) has %d rects", n, len(rects))
		}
		for i, r := range rects {
			if r.width <= 0 || r.height <= 0 {
				t.Fatalf("stageLayout(%d)[%d] has no area: %+v", n, i, r)
			}
			if r.left < 0 || r.top < 0 || r.left+r.width > 100.01 || r.top+r.height > 100.01 {
				t.Fatalf("stageLayout(%d)[%d] leaves the stage: %+v", n, i, r)
			}
			for j := 0; j < i; j++ {
				if overlap(rects[j], r) {
					t.Fatalf("stageLayout(%d)[%d] %+v overlaps [%d] %+v", n, i, r, j, rects[j])
				}
			}
		}
	}
}

func overlap(a, b stageRect) bool {
	return a.left < b.left+b.width && b.left < a.left+a.width &&
		a.top < b.top+b.height && b.top < a.top+a.height
}
