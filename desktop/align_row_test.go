package main

import (
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/align"
)

func TestAlignmentRowFollowsCurrentReferenceAndPlayerGeneration(t *testing.T) {
	app := &appState{scene: "stage", syncRef: "reference"}
	ref := &item{source: core.Source{ID: "reference"}, generation: 1}
	target := &item{source: core.Source{ID: "target"}, generation: 2}
	app.items = []*item{ref, target}
	app.alignRow = alignRowResult{
		Stage: app.scene, Reference: ref.source.ID, Target: target.source.ID,
		ReferenceGeneration: ref.generation, TargetGeneration: target.generation,
		Status: "busy", Text: "estimating",
	}
	if got := app.alignmentRowResult(); got.Target != target.source.ID || got.Text != "estimating" {
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

func TestAlignmentRowHidesSuggestionAfterTargetOffsetChanges(t *testing.T) {
	app := &appState{scene: "stage", syncRef: "reference", sync: newSyncState()}
	ref := &item{source: core.Source{ID: "reference"}, generation: 1}
	target := &item{source: core.Source{ID: "target"}, generation: 2}
	app.items = []*item{ref, target}
	app.alignRow = alignRowResult{
		Stage: app.scene, Reference: ref.source.ID, Target: target.source.ID,
		ReferenceGeneration: ref.generation, TargetGeneration: target.generation,
		Status: "suggested", Text: "offset",
	}
	app.lastProbe = alignSuggestion{
		Reference: alignWindow{Source: ref.source.ID, Generation: ref.generation},
		Target:    alignWindow{Source: target.source.ID, Generation: target.generation},
		Snapshot:  probeSnapshot{Track: "target-track"},
		Result:    align.Result{Accepted: true},
	}
	if got := app.alignmentRowResult(); got.Status != "suggested" {
		t.Fatalf("expected fresh suggestion, got %+v", got)
	}
	if err := app.sync.timeline.SetShift(app.lastProbe.Snapshot.Track, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := app.alignmentRowResult(); got.Status != "" {
		t.Fatalf("suggestion still visible after changing the target offset: %+v", got)
	}
}
