package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/xswt442-cmd/muxshift/core/hls"
)

// TestAFinishedImportAsksForAFrame is UI-IMPORT-INVALIDATE-01: the Sources page
// keeps showing "Reading…" and the old source count when the background read
// ends, because nothing asks for a frame. The landing must request one on both
// outcomes, and a landing from a cancelled read must not.
func TestAFinishedImportAsksForAFrame(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("the app state was not built: %v", err)
	}
	frames := 0
	app.requestFrame = func() { frames++ }
	app.importing = "reading"
	missing := filepath.Join(t.TempDir(), "not-there.mp4")

	app.landImport(0, missing, hls.Stream{}, false, errors.New("read playlist: connection refused"))

	if frames == 0 {
		t.Fatal("an import that failed left the page without a frame to draw")
	}
	if app.importing != "" {
		t.Fatalf("the import is still marked in flight as %q", app.importing)
	}
	if app.lastErr == nil {
		t.Fatal("the failed import left no error to show")
	}
	if got, want := app.lastCommand.Message, "could not import not-there.mp4"; got != want {
		t.Fatalf("the status line says %q, want %q", got, want)
	}
}

// TestACancelledReadDoesNotDrawTwice guards the early return: a landing from the
// read that Cancel stopped must not add a source or ask for a frame, because
// Cancel already cleared the busy state and said so.
func TestACancelledReadDoesNotDrawTwice(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("the app state was not built: %v", err)
	}
	frames := 0
	app.requestFrame = func() { frames++ }
	app.importToken = 3

	app.landImport(2, filepath.Join(t.TempDir(), "late.mp4"), hls.Stream{}, false, nil)

	if frames != 0 {
		t.Fatalf("a cancelled read asked for %d frame(s)", frames)
	}
	if len(app.items) != 0 {
		t.Fatalf("a cancelled read added %d source(s)", len(app.items))
	}
}
