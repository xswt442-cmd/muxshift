package main

import (
	"errors"
	"strings"
	"testing"
)

// The ledger counts what went wrong away from the drawing thread. Dismissing
// the status bar's message must not make the run look clean.

func TestBackgroundFailuresAreCountedAndDismissKeepsThem(t *testing.T) {
	a := &appState{}

	if count, last := a.backgroundFailures(); count != 0 || last.what != "" {
		t.Fatalf("a fresh run has %d failure(s), newest %q", count, last.what)
	}
	if line := (backgroundFailure{}).line(); !strings.Contains(line, "nothing has failed") {
		t.Fatalf("an empty ledger reads %q", line)
	}

	a.noteBackgroundFailure("saving a source's remembered sound", errors.New("rename settings.json: Access is denied."))
	a.noteBackgroundFailure("measuring a recording's anchor", errors.New("ffprobe is not on PATH"))

	count, last := a.backgroundFailures()
	if count != 2 {
		t.Fatalf("counted %d failure(s), want 2", count)
	}
	if !strings.Contains(last.line(), "measuring a recording's anchor") {
		t.Fatalf("the newest failure reads %q", last.line())
	}
	if !strings.Contains(last.line(), "ffprobe is not on PATH") {
		t.Fatalf("the newest failure does not carry its error: %q", last.line())
	}

	a.lastErr = errors.New("the newest message")
	a.dismissError()
	if a.lastErr != nil {
		t.Fatalf("dismiss left the message in place: %v", a.lastErr)
	}
	if count, _ = a.backgroundFailures(); count != 2 {
		t.Fatalf("dismissing the message changed the count to %d, want 2", count)
	}
}

func TestASuccessfulWriteCountsNothing(t *testing.T) {
	a := &appState{}
	a.noteBackgroundFailure("saving a source's remembered sound", nil)
	if count, _ := a.backgroundFailures(); count != 0 {
		t.Fatalf("a nil error counted %d failure(s)", count)
	}
	a.dismissError() // nothing to dismiss
	if a.lastErr != nil {
		t.Fatalf("dismissing nothing invented an error: %v", a.lastErr)
	}
}
