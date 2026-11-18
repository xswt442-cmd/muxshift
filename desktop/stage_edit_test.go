package main

import (
	"math"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.0005 }

func TestABoxStaysGrabbable(t *testing.T) {
	box := stageBox{X: 0.5, Y: 0.5, W: 0.0001, H: -3}
	got := box.normalized()
	if !got.valid() || got.W < minStageSide || got.H < minStageSide {
		t.Fatalf("a degenerate box was not repaired: %+v", got)
	}
	// A box dragged past the right edge stops at it, keeping every pixel of
	// the picture on the stage.
	pushed := stageBox{X: 0.1, Y: 0.1, W: 0.3, H: 0.3}.movedBy(5, 5)
	if !near(pushed.X, 1-pushed.W) || !near(pushed.Y, 1-pushed.H) {
		t.Fatalf("dragging off the stage left %+v, want the inside clamp", pushed)
	}
	// And one pushed off the left stops at the left edge.
	pushed = stageBox{X: 0.1, Y: 0.1, W: 0.3, H: 0.3}.movedBy(-5, -5)
	if !near(pushed.X, 0) || !near(pushed.Y, 0) {
		t.Fatalf("dragging past the top left left %+v", pushed)
	}
}

func TestMovingABoxNeverChangesItsSize(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.3, W: 0.4, H: 0.25}
	for _, delta := range [][2]float32{{0.1, 0}, {-0.1, 0}, {0, 0.05}, {0, -0.05}} {
		got := box.movedBy(delta[0], delta[1])
		if !near(got.W, box.W) || !near(got.H, box.H) {
			t.Fatalf("moving by %v changed the size: %+v", delta, got)
		}
	}
	if got := box.movedBy(float32(math.NaN()), 0); got != box {
		t.Fatalf("a NaN delta moved the box: %+v", got)
	}
}

func TestAResizeDragsTheEdgeItWasGrabbedBy(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.4}
	// The east handle moves the right edge; the left one stays put.
	east, ok := box.resizedBy(handleE, 0.1, 0, 0, false)
	if !ok || !near(east.X, 0.2) || !near(east.W, 0.5) || !near(east.H, 0.4) {
		t.Fatalf("east resize gave %+v ok=%v", east, ok)
	}
	// The west handle moves the left edge; the right one stays put.
	west, ok := box.resizedBy(handleW, -0.1, 0, 0, false)
	if !ok || !near(west.X, 0.1) || !near(west.W, 0.5) {
		t.Fatalf("west resize gave %+v ok=%v", west, ok)
	}
	// The north handle moves the top edge; the bottom stays.
	north, ok := box.resizedBy(handleN, 0, -0.1, 0, false)
	if !ok || !near(north.Y, 0.1) || !near(north.H, 0.5) {
		t.Fatalf("north resize gave %+v ok=%v", north, ok)
	}
	// A corner moves both of its edges and keeps the opposite corner fixed.
	se, ok := box.resizedBy(handleSE, 0.1, 0.1, 0, false)
	if !ok || !near(se.X, 0.2) || !near(se.Y, 0.2) || !near(se.W, 0.5) || !near(se.H, 0.5) {
		t.Fatalf("south east resize gave %+v ok=%v", se, ok)
	}
}

func TestRatioLockedResizeKeepsThePictureShape(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.2} // 2:1
	aspect := float32(2)
	// A side drag changes both sides: width follows height at the same ratio.
	got, ok := box.resizedBy(handleE, 0.1, 0, aspect, true)
	if !ok || !near(got.W/got.H, aspect) {
		t.Fatalf("east ratio resize gave %+v ok=%v", got, ok)
	}
	// A corner keeps its opposite corner where it was.
	corner, ok := box.resizedBy(handleNW, -0.05, -0.05, aspect, true)
	if !ok || !near(corner.W/corner.H, aspect) ||
		!near(corner.X+corner.W, box.X+box.W) || !near(corner.Y+corner.H, box.Y+box.H) {
		t.Fatalf("corner ratio resize gave %+v ok=%v", corner, ok)
	}
}

func TestAResizeRefusesToShrinkOrFlip(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.4}
	if _, ok := box.resizedBy(handleE, -0.4, 0, 0, false); ok {
		t.Fatal("a resize under the minimum side was accepted")
	}
	if _, ok := box.resizedBy(handleE, -0.45, 0, 0, false); ok {
		t.Fatal("a resize that turned the box inside out was accepted")
	}
	if _, ok := box.resizedBy(handleNone, 0.1, 0.1, 0, false); ok {
		t.Fatal("a resize without a handle was accepted")
	}
	if _, ok := box.resizedBy(handleSE, float32(math.Inf(1)), 0, 0, false); ok {
		t.Fatal("an infinite delta was accepted")
	}
}

func TestHandleAtNamesTheCornerThePressAimedAt(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.4}
	cases := map[handleKind][2]float32{
		handleNW: {0.2, 0.2},
		handleNE: {0.6, 0.2},
		handleSE: {0.6, 0.6},
		handleSW: {0.2, 0.6},
		handleN:  {0.4, 0.2},
		handleS:  {0.4, 0.6},
		handleW:  {0.2, 0.4},
		handleE:  {0.6, 0.4},
	}
	for want, point := range cases {
		if got := box.handleAt(point[0], point[1]); got != want {
			t.Fatalf("press at %v named %v, want %v", point, got, want)
		}
	}
	if got := box.handleAt(0.4, 0.4); got != handleNone {
		t.Fatalf("a press in the middle named a handle: %v", got)
	}
	if got := box.handleAt(0.9, 0.9); got != handleNone {
		t.Fatalf("a press far away named a handle: %v", got)
	}
}

func TestTheSceneKnowsItsOwnRectangles(t *testing.T) {
	edits := newStageEdits()
	fallback := stageBox{X: 0, Y: 0, W: 0.5, H: 1}
	if got, ok := edits.boxFor("a", fallback); !ok || got != fallback {
		t.Fatalf("a source without a rectangle did not get the layout slot: %+v", got)
	}
	edits.setBox("a", stageBox{X: 0.1, Y: 0.2, W: 0.3, H: 0.4})
	if got, _ := edits.boxFor("a", fallback); !near(got.X, 0.1) || !near(got.H, 0.4) {
		t.Fatalf("the stored rectangle was not used: %+v", got)
	}
	edits.forget("a")
	if got, _ := edits.boxFor("a", fallback); got != fallback {
		t.Fatalf("forgetting a rectangle did not fall back to the layout: %+v", got)
	}
	if _, ok := edits.boxFor("b", stageBox{}); ok {
		t.Fatal("a source with neither an edit nor a slot was placed")
	}
}

func TestStackingOrderMovesOneStepAndToTheEnds(t *testing.T) {
	edits := newStageEdits()
	sources := []core.SourceID{"a", "b", "c"}
	got := edits.ordered(sources)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("ordered without edits gave %v", got)
	}
	edits.raise("a", sources, false)
	if got := edits.ordered(sources); got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("a step forward put %v", got)
	}
	edits.raise("a", sources, true)
	if got := edits.ordered(sources); got[len(got)-1] != "a" {
		t.Fatalf("to front put %v", got)
	}
	edits.lower("a", sources, true)
	if got := edits.ordered(sources); got[0] != "a" {
		t.Fatalf("to back put %v", got)
	}
	// The back-most source has nowhere further back to go.
	edits.lower("a", sources, false)
	if got := edits.ordered(sources); got[0] != "a" {
		t.Fatalf("a step back from the back moved it: %v", got)
	}
	edits.raise("c", sources, false)
	if got := edits.ordered(sources); got[2] != "c" || got[1] != "b" {
		t.Fatalf("a step forward put %v", got)
	}
	got = edits.ordered([]core.SourceID{"c"})
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("a source that is no longer shown leaked into the order: %v", got)
	}
}

// A picture that fits on the stage is kept wholly inside it: dragging a
// full-size picture does nothing, and a smaller one stops at the edge instead
// of sliding under the bars around the stage.
func TestABoxThatFitsStaysInsideTheStage(t *testing.T) {
	full := stageBox{X: 0, Y: 0, W: 1, H: 1}
	if moved := full.movedBy(0.4, 0.4); !near(moved.X, 0) || !near(moved.Y, 0) {
		t.Fatalf("a full-size picture moved to %.3f,%.3f", moved.X, moved.Y)
	}
	small := stageBox{X: 0.1, Y: 0.1, W: 0.4, H: 0.4}
	if moved := small.movedBy(0.9, 0.9); !near(moved.X, 0.6) || !near(moved.Y, 0.6) {
		t.Fatalf("a picture stopped at %.3f,%.3f, want 0.6,0.6", moved.X, moved.Y)
	}
	if moved := small.movedBy(-9, -9); !near(moved.X, 0) || !near(moved.Y, 0) {
		t.Fatalf("a picture stopped at %.3f,%.3f, want 0,0", moved.X, moved.Y)
	}
	// Growing by a handle cannot push the box out of the stage either.
	fitted := stageBox{X: 0, Y: 0, W: 1, H: 1}
	if out, ok := fitted.resizedBy(handleNW, 0.3, 0.3, 1, false); ok && (out.X < -0.0001 || out.Y < -0.0001 || out.X+out.W > 1.0001 || out.Y+out.H > 1.0001) {
		t.Fatalf("resize left the stage: %.3f,%.3f %.3fx%.3f", out.X, out.Y, out.W, out.H)
	}
}
