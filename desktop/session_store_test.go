package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

// newStoreFixture builds a session with one named scene, one named source, one
// video track, and one audio track at a known level, plus the card that mirrors
// the source. It is what the round-trip test writes and reads back.
func newStoreFixture(t *testing.T) (*appState, core.SceneID, core.SourceID) {
	t.Helper()
	session := core.NewSession("desktop")
	scene, err := session.AddScene("")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	src, err := session.AddSource("https://example.test/camera.m3u8")
	if err != nil {
		t.Fatalf("add source: %v", err)
	}
	video, err := session.AddTrack(src.ID, core.MediaVideo, "camera")
	if err != nil {
		t.Fatalf("add video: %v", err)
	}
	audio, err := session.AddTrack(src.ID, core.MediaAudio, "ambient")
	if err != nil {
		t.Fatalf("add audio: %v", err)
	}
	if _, err := session.SetSceneVideo(scene.ID, video.ID); err != nil {
		t.Fatalf("set video: %v", err)
	}
	if _, err := session.SetSceneAudio(scene.ID, audio.ID, 0.35, true); err != nil {
		t.Fatalf("set audio: %v", err)
	}
	if err := session.RenameSource(src.ID, "Camera A"); err != nil {
		t.Fatalf("rename source: %v", err)
	}
	if err := session.RenameScene(scene.ID, "Opening"); err != nil {
		t.Fatalf("rename scene: %v", err)
	}
	storedSrc, err := session.Source(src.ID)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	app := &appState{
		session: session,
		scene:   scene.ID,
		items: []*item{{
			uri:    src.URI,
			source: storedSrc,
			video:  video.ID,
			audio:  audio.ID,
			volume: 0.35,
			muted:  true,
			shown:  true,
		}},
	}
	return app, scene.ID, src.ID
}

// TestSessionStoreRoundTrip checks that a snapshot survives save and load with
// the active scene, each scene's name and contents, and each source's identity,
// name and levels intact.
func TestSessionStoreRoundTrip(t *testing.T) {
	app, sceneID, sourceID := newStoreFixture(t)
	path := filepath.Join(t.TempDir(), "settings.json.session.json")
	if err := saveSessionStore(path, app.captureSessionStore()); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, note := loadSessionStore(path)
	if note != "" {
		t.Fatalf("load note = %q, want none", note)
	}
	if got.ActiveScene != string(sceneID) {
		t.Fatalf("active scene = %q, want %q", got.ActiveScene, sceneID)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(got.Sources))
	}
	src := got.Sources[0]
	if src.ID != string(sourceID) || src.URI != "https://example.test/camera.m3u8" {
		t.Fatalf("source identity = %+v", src)
	}
	if src.Name != "Camera A" || src.Volume != 0.35 || !src.Muted || !src.Shown {
		t.Fatalf("source values = %+v", src)
	}
	if len(got.Scenes) != 1 {
		t.Fatalf("scenes = %d, want 1", len(got.Scenes))
	}
	scene := got.Scenes[0]
	if scene.ID != string(sceneID) || scene.Name != "Opening" {
		t.Fatalf("scene identity = %+v", scene)
	}
	if len(scene.Video) != 1 || scene.Video[0] != string(sourceID) {
		t.Fatalf("scene video = %v, want [%s]", scene.Video, sourceID)
	}
	if len(scene.Audio) != 1 || scene.Audio[0].Source != string(sourceID) ||
		scene.Audio[0].Volume != 0.35 || !scene.Audio[0].Muted {
		t.Fatalf("scene audio = %+v", scene.Audio)
	}
}

// TestSessionStoreCorruptTolerated checks that a corrupt file is reported and
// produces an empty snapshot instead of an error the start would die on.
func TestSessionStoreCorruptTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json.session.json")
	if err := os.WriteFile(path, []byte("{ not a session"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, note := loadSessionStore(path)
	if note == "" {
		t.Fatal("a corrupt file produced no note")
	}
	if len(got.Sources) != 0 || len(got.Scenes) != 0 || got.ActiveScene != "" {
		t.Fatalf("corrupt file produced %+v, want an empty snapshot", got)
	}
}

// TestSessionStoreIgnoresUnknownFields checks that a future field or a bad level
// does not stop the file being read: unknown fields are ignored and levels are
// clamped.
func TestSessionStoreIgnoresUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json.session.json")
	data := `{"activeScene":"scene-1","fromTheFuture":{"x":1},"sources":[{"uri":"https://example.test/a.m3u8","volume":2,"colour":"red"}],"scenes":[{"id":"scene-1","name":"A","audio":[{"source":"source-1","volume":-1}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, note := loadSessionStore(path)
	if note != "" {
		t.Fatalf("load note = %q, want none", note)
	}
	if len(got.Sources) != 1 || got.Sources[0].Volume != 1 {
		t.Fatalf("source volume = %+v, want clamped to 1", got.Sources)
	}
	if len(got.Scenes) != 1 || len(got.Scenes[0].Audio) != 1 || got.Scenes[0].Audio[0].Volume != 0 {
		t.Fatalf("scene audio = %+v, want clamped to 0", got.Scenes)
	}
}

// TestSessionStoreMissingIsFirstRun checks that a missing file is not a note.
func TestSessionStoreMissingIsFirstRun(t *testing.T) {
	got, note := loadSessionStore(filepath.Join(t.TempDir(), "absent.json"))
	if note != "" {
		t.Fatalf("missing file note = %q, want none", note)
	}
	if len(got.Sources) != 0 || len(got.Scenes) != 0 {
		t.Fatalf("missing file produced %+v, want an empty snapshot", got)
	}
}

// TestSessionStoreRebuildsScene checks the rebuild path with a scene but no
// sources: it must run without a window and restore the active scene.
func TestSessionStoreRebuildsScene(t *testing.T) {
	snap := storedSession{
		ActiveScene: "scene-2",
		Scenes: []storedScene{
			{ID: "scene-1", Name: ""},
			{ID: "scene-2", Name: "Second"},
		},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	scenes := app.session.Scenes()
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d, want 2", len(scenes))
	}
	if scenes[1].Name != "Second" {
		t.Fatalf("second scene name = %q, want Second", scenes[1].Name)
	}
	if got := app.scene; got != scenes[1].ID {
		t.Fatalf("active scene = %q, want %q", got, scenes[1].ID)
	}
}

// TestSessionStoreRebuildKeepsFailedSource checks that a source that cannot be
// reopened is kept with its open error instead of stopping the start.
func TestSessionStoreRebuildKeepsFailedSource(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.mp4")
	snap := storedSession{
		ActiveScene: "scene-1",
		Scenes:      []storedScene{{ID: "scene-1", Name: ""}},
		Sources:     []storedSource{{ID: "source-1", URI: missing, Volume: 0.5}},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if len(app.items) != 1 {
		t.Fatalf("items = %d, want 1", len(app.items))
	}
	if app.items[0].openErr == nil {
		t.Fatal("a missing source was not marked with its open error")
	}
}
