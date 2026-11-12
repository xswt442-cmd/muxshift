package main

import (
	"strings"
	"testing"
)

// TestByteLineMovesUnitsWhereTheyChange keeps the diagnostics numbers readable at
// the sizes a buffer actually reaches.
func TestByteLineMovesUnitsWhereTheyChange(t *testing.T) {
	for _, c := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 bytes"},
		{512, "512 bytes"},
		{2048, "2.0 KiB"},
		{3 << 20, "3.0 MiB"},
		{2 << 30, "2.0 GiB"},
	} {
		if got := byteLine(c.bytes); got != c.want {
			t.Errorf("byteLine(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

// TestACleanupThatKeptAFileSaysWhy is the honesty requirement of the cleanup
// action: a pass that left a file on disk says which file and why, instead of
// reporting a clean sweep.
func TestACleanupThatKeptAFileSaysWhy(t *testing.T) {
	cleanup := dvrCleanup{kept: []string{"rec-1.ts: a player still holds it"}}
	line := cleanup.line()
	if !strings.Contains(line, "removed 0 recording(s)") {
		t.Fatalf("line = %q, want the removal count", line)
	}
	if !strings.Contains(line, "kept 1 (rec-1.ts: a player still holds it)") {
		t.Fatalf("line = %q, want the file and its reason", line)
	}
	if strings.Contains(line, "freed 3") {
		t.Fatalf("line = %q claims bytes it did not free", line)
	}
}

// TestTheBudgetLineIsStatedAsALimit keeps the page from presenting a limit as if
// it were the current usage.
func TestTheBudgetLineIsStatedAsALimit(t *testing.T) {
	a := &appState{budget: builtinDVRBudget(), budgetFrom: "the built-in defaults"}
	line := a.budgetLine()
	if !strings.Contains(line, "keeps 60s") {
		t.Fatalf("line = %q, want the retention it states", line)
	}
	if !strings.Contains(line, "a limit, not what is in use") {
		t.Fatalf("line = %q, want the caveat", line)
	}
	if !strings.Contains(line, "the built-in defaults") {
		t.Fatalf("line = %q, want where the limit came from", line)
	}
}

// TestTwoBuffersMadeInTheSameSecondReadApart keeps the page from claiming two
// buffers are one: instance ids carry a nanosecond stamp, and a short name that
// only keeps the head would cut them down to the same string.
func TestTwoBuffersMadeInTheSameSecondReadApart(t *testing.T) {
	// The counterexample the review named: the same prefix, two counters. The id
	// ends in the Core's per-process counter, so the name keeps it verbatim
	// instead of hoping a digest of the whole id never collides.
	first := "buf-1794510388264000000-265"
	second := "buf-1794510388491000000-492"
	if got, want := instanceName(first), "buf-1794510388-265"; got != want {
		t.Fatalf("the first instance reads %q, want %q", got, want)
	}
	if got, want := instanceName(second), "buf-1794510388-492"; got != want {
		t.Fatalf("the second instance reads %q, want %q", got, want)
	}
	if instanceName(first) == instanceName(second) {
		t.Fatalf("two instances read as one name: %q", instanceName(first))
	}
	// A buffer made in the same nanosecond differs only by that counter, and the
	// two must still be told apart.
	next := "buf-1794510388264000000-266"
	if instanceName(first) == instanceName(next) {
		t.Fatalf("two buffers made in the same nanosecond read as one name: %q", instanceName(first))
	}
	for _, instance := range []string{first, second, next} {
		if name := instanceName(instance); len(name) > 24 {
			t.Fatalf("the name is not short enough for a row: %q (%d characters)", name, len(name))
		}
	}
	if instanceName("") != "buffer" {
		t.Fatalf("an empty instance reads %q", instanceName(""))
	}
	if instanceName("noseparator") != "noseparator" {
		t.Fatalf("an id without a counter reads %q", instanceName("noseparator"))
	}
}
