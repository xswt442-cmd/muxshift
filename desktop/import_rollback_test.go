package main

// P7.1: an import that the session accepted but the player could not open must
// not leave a source, its tracks, a selected source or a scene entry behind.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// errNoPlayer is the failure the seam hands back: this machine's engine refused
// the file, which is what Media Foundation does for a file it cannot decode.
var errNoPlayer = errors.New("the player refused the media")

func TestAFailedOpenLeavesNoSourceBehind(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return nil, errNoPlayer }
	app.landImport(0, file, hls.Stream{}, importLocal, false, nil)

	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("a failed open left %d source(s) in the session", got)
	}
	if got := len(app.session.Tracks()); got != 0 {
		t.Fatalf("a failed open left %d track(s) in the session", got)
	}
	if len(app.items) != 0 {
		t.Fatalf("a failed open added %d item(s)", len(app.items))
	}
	if app.lastErr == nil || !strings.Contains(app.lastErr.Error(), "refused the media") {
		t.Fatalf("the failure was not reported: %v", app.lastErr)
	}
	if !strings.Contains(app.lastCommand.Message, "could not import") {
		t.Fatalf("the message does not say the import failed: %q", app.lastCommand.Message)
	}
}

func TestAFailedOpenRollsBackTheProbedTracks(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	stream := hls.Stream{
		URI: "https://example.test/live/index.m3u8",
		Tracks: []hls.TrackInfo{
			{Index: 0, Kind: core.MediaVideo, Codec: "h264"},
			{Index: 1, Kind: core.MediaAudio, Codec: "mpeg4-audio"},
		},
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return nil, errNoPlayer }
	app.landImport(0, stream.URI, stream, importProbed, false, nil)

	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("a failed open left %d source(s)", got)
	}
	if got := len(app.session.Tracks()); got != 0 {
		t.Fatalf("a failed open left %d track(s)", got)
	}
	if len(app.items) != 0 {
		t.Fatalf("a failed open added %d item(s)", len(app.items))
	}
}

func TestAMediaThatCannotOpenIsNotKeptEither(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return nil, errNoPlayer }
	if _, err := app.addFrom(file, originCommandLine); err == nil {
		t.Fatal("addFrom accepted a file whose player could not open")
	}
	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("addFrom left %d source(s) behind", got)
	}
	if got := len(app.session.Tracks()); got != 0 {
		t.Fatalf("addFrom left %d track(s) behind", got)
	}
}

func TestALandingAfterTheWindowClosedAddsNothing(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.importing = "there.mp4"
	done := make(chan struct{})
	app.importDone = done
	app.closed = true
	app.landImport(0, file, hls.Stream{}, importLocal, false, nil)

	if len(app.items) != 0 {
		t.Fatalf("a landing after close added %d item(s)", len(app.items))
	}
	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("a landing after close left %d source(s)", got)
	}
	if app.importing != "" {
		t.Fatalf("a landing after close kept importing=%q", app.importing)
	}
	select {
	case <-done:
	default:
		t.Fatal("a landing after close left the repaint loop running")
	}
}

// The engine error that decides whether an import worked arrives after the open
// returns, so the rule is separate from the loop that reads it.
func TestAnImportIsGivenBackOnlyWhenThePlayerErroredWithoutReady(t *testing.T) {
	cases := []struct {
		name  string
		ready bool
		err   error
		want  bool
	}{
		{"no error, not ready yet", false, nil, false},
		{"ready with no error", true, nil, false},
		{"ready despite an old error", true, errNoPlayer, false},
		{"error before any frame", false, errNoPlayer, true},
	}
	for _, c := range cases {
		if got := shouldAbandonImport(c.ready, c.err); got != c.want {
			t.Errorf("%s: shouldAbandonImport(%v, %v) = %v, want %v", c.name, c.ready, c.err, got, c.want)
		}
	}
}

// A player read off the drawing thread reports live only while the item still
// owns that generation.
func TestThePlayerStateIsReadOnlyWhileTheGenerationIsOwned(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.onMain = func(fn func()) { fn() }
	it := &item{player: &ui.VideoPlayer{}, generation: 2}
	it.source.ID = "source-1"
	app.items = append(app.items, it)

	_, _, live := app.playerState(it, 2)
	if !live {
		t.Fatal("the current player was not readable")
	}
	if _, _, live := app.playerState(it, 1); live {
		t.Fatal("a player from an older generation was still readable")
	}
	app.closed = true
	if _, _, live := app.playerState(it, 2); live {
		t.Fatal("a closed window still handed out a player")
	}
}

func TestASuccessfulImportStillAddsOneSource(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.landImport(0, file, hls.Stream{}, importLocal, false, nil)

	if got := len(app.session.Sources()); got != 1 {
		t.Fatalf("a good import left %d source(s), want 1", got)
	}
	if len(app.items) != 1 {
		t.Fatalf("a good import left %d item(s), want 1", len(app.items))
	}
	if app.selected != app.items[0].source.ID {
		t.Fatalf("the imported source is not selected")
	}
}
