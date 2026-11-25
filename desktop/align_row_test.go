package main

import (
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

func TestAlignmentRowFollowsCurrentReferenceAndPlayerGeneration(t *testing.T) {
	app := &appState{scene: "stage", syncRef: "reference"}
	ref := &item{source: core.Source{ID: "reference"}, generation: 1}
	target := &item{source: core.Source{ID: "target"}, generation: 2}
	app.items = []*item{ref, target}
	app.alignRow = alignRowResult{
		Stage: app.scene, Reference: ref.source.ID, Target: target.source.ID,
		ReferenceGeneration: ref.generation, TargetGeneration: target.generation,
		Status: "suggested", Text: "offset",
	}
	if got := app.alignmentRowResult(); got.Target != target.source.ID || got.Text != "offset" {
		t.Fatalf("current row result = %+v", got)
	}
	target.generation++
	if got := app.alignmentRowResult(); got.Status != "" {
		t.Fatalf("result from an older player generation = %+v", got)
	}
	target.generation--
	app.syncRef = target.source.ID
	if got := app.alignmentRowResult(); got.Status != "" {
		t.Fatalf("result after changing reference = %+v", got)
	}
}
