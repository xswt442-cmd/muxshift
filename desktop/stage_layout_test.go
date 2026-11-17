package main

import "testing"

// TestEveryLayoutKeepsItsPicturesApart is the property the whole canvas rests
// on: at the capacity a layout advertises, every picture is a real rectangle
// inside the stage and no two pictures land on top of each other. A layout that
// overlapped would still "show two videos" in a capture while one hid the
// other — except a floating slot, which must be painted above what it covers;
// a layout that poked out of the stage would lose the edges instead.
func TestEveryLayoutKeepsItsPicturesApart(t *testing.T) {
	for _, kind := range layoutKinds {
		for n := 1; n <= kind.slots(); n++ {
			rects := layoutSlots(kind, n)
			if len(rects) != n {
				t.Fatalf("%v with %d sources placed %d rects", kind, n, len(rects))
			}
			for i, r := range rects {
				if r.width <= 0 || r.height <= 0 {
					t.Fatalf("%v with %d sources: slot %d has no area: %+v", kind, n, i, r)
				}
				if !inStage(r) {
					t.Fatalf("%v with %d sources: slot %d leaves the stage: %+v", kind, n, i, r)
				}
				for j := 0; j < i; j++ {
					// Overlap is only allowed when the later slot is painted
					// above the earlier one: picture in picture floats over the
					// main picture instead of hiding under it.
					if overlap(r, rects[j]) && r.z <= rects[j].z {
						t.Fatalf("%v with %d sources: slot %d %+v overlaps slot %d %+v without being stacked above it", kind, n, i, r, j, rects[j])
					}
				}
			}
		}
	}
}

func overlap(a, b slotRect) bool {
	return a.left < b.left+b.width && b.left < a.left+a.width &&
		a.top < b.top+b.height && b.top < a.top+a.height
}
