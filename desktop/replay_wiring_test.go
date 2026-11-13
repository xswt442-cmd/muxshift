package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P16.4g closed the phase with an explicit scope: a stage that is already
// replaying can be seeked, and entering replay from a live stage is not a
// product capability. The internal transaction still exists so the recorded
// checks can exercise the planning and the rollback, which means the boundary
// has to be guarded rather than remembered.
//
// This test walks the package's shipping files and fails if any of them calls
// the entry transaction. Scenario files are excluded because they only run
// under -scenario, and tests are excluded because they are the recorded checks
// themselves.
func TestEnteringReplayIsNotWiredIntoTheProduct(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files were found, so the guard checked nothing")
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || path == "enter_scenario.go" {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		// A call is spelled through the receiver; the declaration in
		// replay_enter.go is the function itself and must stay.
		if strings.Contains(string(source), "a.startEnterReplayTxn(") {
			t.Errorf("%s calls startEnterReplayTxn; entering replay from live is not a product capability (P16.4g)", path)
		}
	}
}

// The same closure says there is no default lag: a refused entry must not leave
// a transaction behind. This pins the observable half of that rule — the refusal
// is a sentence, and nothing started.
func TestARefusedSeekStartsNothing(t *testing.T) {
	a := &appState{items: []*item{{}}, seekText: "0"}
	note := a.requestReplaySeek()
	if note == "" {
		t.Fatal("a refused request produced no sentence")
	}
	if !strings.Contains(note, "is not buffering") {
		t.Fatalf("the refusal should name what is missing: %q", note)
	}
	a.seekMu.Lock()
	started := a.seek != nil
	a.seekMu.Unlock()
	if started {
		t.Fatal("a refused request left a transaction behind")
	}
}

// A zero offset is a claim like any other, so nothing may substitute one when
// the evidence is absent: an entry with no buffer, and an entry with no sources,
// must both be refused rather than planned against a default target.
func TestZeroIsNotADefaultTarget(t *testing.T) {
	if _, err := (&appState{}).planReplayEntry(nil, 0, true); err == nil {
		t.Fatal("planning an entry with no sources succeeded")
	}
	if _, err := (&appState{items: []*item{{}}}).planReplayEntry([]*item{{}}, 0, false); err == nil {
		t.Fatal("planning an entry with no buffer succeeded")
	}
}
