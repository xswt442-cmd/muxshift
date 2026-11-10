// Tests for the command and snapshot boundary. They deliberately open no
// player: the point of the boundary is that a command names its source and a
// snapshot copies what the session holds, and both can be checked without a
// window on a machine without media.
package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

// bareState builds a session with one source and two tracks, mapped onto an
// appState that has not opened any player yet.
func bareState(t *testing.T) (*appState, *item) {
	t.Helper()
	session := core.NewSession("test")
	scene, err := session.AddScene("main")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := session.AddOutput(core.OutputDesktopWindow, scene.ID); err != nil {
		t.Fatalf("add output: %v", err)
	}
	source, err := session.AddSource("http://127.0.0.1:1/video.m3u8")
	if err != nil {
		t.Fatalf("add source: %v", err)
	}
	video, err := session.AddTrack(source.ID, core.MediaVideo, "video")
	if err != nil {
		t.Fatalf("add video track: %v", err)
	}
	audio, err := session.AddTrack(source.ID, core.MediaAudio, "audio")
	if err != nil {
		t.Fatalf("add audio track: %v", err)
	}
	it := &item{
		uri:    source.URI,
		source: source,
		video:  video.ID,
		audio:  audio.ID,
		volume: 1,
	}
	a := &appState{
		session: session,
		scene:   scene.ID,
		items:   []*item{it},
		audible: make(map[core.TrackID]bool),
		sync:    newSyncState(),
		frames:  make(chan struct{}, 1),
	}
	return a, it
}

func TestRunCommandRejectsAnUnknownSource(t *testing.T) {
	a, _ := bareState(t)
	result := a.runCommand(command{Kind: commandShow, Source: "source-99"})

	if !errors.Is(result.Err, UnknownSourceError) {
		t.Fatalf("error = %v, want UnknownSourceError", result.Err)
	}
	if result.Command.Kind != commandShow || result.Command.Source != "source-99" {
		t.Fatalf("result kept the wrong command: %+v", result.Command)
	}
	if a.lastCommand.Err == nil {
		t.Fatal("the failed command was not recorded on the app state")
	}
	if got := result.String(); !strings.Contains(got, "failed") || !strings.Contains(got, "source-99") {
		t.Fatalf("String() = %q, want a failed line naming the source", got)
	}
}

func TestRunCommandReportsACommandWithoutAPlayer(t *testing.T) {
	a, it := bareState(t)
	result := a.runCommand(command{Kind: commandReload, Source: it.source.ID})

	if result.Err == nil {
		t.Fatal("reloading a source without a player should fail")
	}
	if !strings.Contains(result.Err.Error(), "no player") {
		t.Fatalf("error = %v, want it to say there is no player", result.Err)
	}
	if result.Generation != it.generation {
		t.Fatalf("generation = %d, want %d", result.Generation, it.generation)
	}
}

func TestSnapshotCopiesTheSceneWithoutTouchingPlayers(t *testing.T) {
	a, it := bareState(t)
	if _, err := a.session.SetSceneVideo(a.scene, it.video); err != nil {
		t.Fatalf("set scene video: %v", err)
	}
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, 0.5, true); err != nil {
		t.Fatalf("set scene audio: %v", err)
	}

	snapshot := a.snapshot()

	if len(snapshot.Picture) != 1 || snapshot.Picture[0] != it.video {
		t.Fatalf("picture = %v, want %q", snapshot.Picture, it.video)
	}
	if len(snapshot.Sound) != 1 {
		t.Fatalf("sound = %v, want one track", snapshot.Sound)
	}
	if got := snapshot.Sound[0]; got.Track != it.audio || got.Volume != 0.5 || !got.Muted {
		t.Fatalf("sound = %+v, want the scene's track at 0.5 and muted", got)
	}
	if got := snapshot.Sound[0].Source; got != it.source.ID {
		t.Fatalf("sound source = %q, want %q", got, it.source.ID)
	}
	if len(snapshot.Sources) != 1 {
		t.Fatalf("sources = %v, want one", snapshot.Sources)
	}
	entry := snapshot.Sources[0]
	if entry.Player {
		t.Fatal("the snapshot claims a player that was never opened")
	}
	if entry.ID != it.source.ID || entry.URI != it.uri || entry.Video != it.video || entry.Audio != it.audio {
		t.Fatalf("source entry = %+v, want the item's identifiers", entry)
	}
	if entry.Shown {
		t.Fatal("a source that is not the picture is reported as shown")
	}
	if snapshot.Group != "" {
		t.Fatalf("group = %q, want none before a sync command", snapshot.Group)
	}
	if snapshot.LastCommand != "none yet" {
		t.Fatalf("last command = %q, want the empty marker", snapshot.LastCommand)
	}
}

func TestSnapshotReflectsTheLastCommand(t *testing.T) {
	a, it := bareState(t)
	a.report(a.runCommand(command{Kind: commandReload, Source: it.source.ID}))

	snapshot := a.snapshot()
	if !strings.Contains(snapshot.LastCommand, "failed") || !strings.Contains(snapshot.LastCommand, string(it.source.ID)) {
		t.Fatalf("last command = %q, want the failed reload", snapshot.LastCommand)
	}
}

func TestCommandResultString(t *testing.T) {
	ok := commandResult{
		Command:    command{Kind: commandVolume, Source: "source-1"},
		Generation: 3,
		Message:    "set to 0.5",
	}
	if got := ok.String(); got != "volume source=source-1 generation=3 ok · set to 0.5" {
		t.Fatalf("ok line = %q", got)
	}
	failed := commandResult{
		Command:    command{Kind: commandNudge, Source: "source-2"},
		Generation: 0,
		Err:        core.ErrNotAligned,
	}
	if got := failed.String(); !strings.HasPrefix(got, "nudge source=source-2 generation=0 failed: ") {
		t.Fatalf("failed line = %q", got)
	}
}
