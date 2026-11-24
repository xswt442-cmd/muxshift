package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

func TestCommandLineLandingChoosesFirstSuccessfulVideo(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.pendingImportOrigin = originCommandLine
	app.landImport(app.importToken, "failed.m3u8", hls.Stream{}, importProbed, false, errors.New("probe failed"))

	first := filepath.Join(t.TempDir(), "first.mp4")
	if err := os.WriteFile(first, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.pendingImportOrigin = originCommandLine
	app.landImport(app.importToken, first, hls.Stream{}, importLocal, false, nil)
	if len(app.items) != 1 || app.items[0].video == "" {
		t.Fatalf("first successful CLI source = %+v", app.items)
	}
	scene, err := app.session.Scene(app.scene)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Video) != 1 || scene.Video[0] != app.items[0].video {
		t.Fatalf("initial scene video = %v, want first successful track %q", scene.Video, app.items[0].video)
	}

	second := filepath.Join(t.TempDir(), "second.mp4")
	if err := os.WriteFile(second, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.pendingImportOrigin = originCommandLine
	app.landImport(app.importToken, second, hls.Stream{}, importLocal, false, nil)
	scene, err = app.session.Scene(app.scene)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Video) != 1 || scene.Video[0] != app.items[0].video {
		t.Fatalf("later CLI landing replaced the first successful video: %v", scene.Video)
	}
}

func TestOrdinaryImportDoesNotAutoSelectItsVideo(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "imported.mp4")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.pendingImportOrigin = originAdded
	app.landImport(app.importToken, path, hls.Stream{}, importLocal, false, nil)
	scene, err := app.session.Scene(app.scene)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Video) != 0 {
		t.Fatalf("ordinary import unexpectedly selected video track %v", scene.Video)
	}
}

func TestStageTransportControllerRequiresPlayers(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	if _, err := app.session.SetSceneVideo(scene); err != nil {
		t.Fatal(err)
	}
	app.sync = newSyncState()
	app.sceneMembers = map[core.SceneID]map[string]bool{scene: {"source": true}}
	app.items[0].persistentID = "source"
	app.items[0].player = nil
	result := app.runCommand(command{Kind: commandPlay})
	if result.Err == nil || result.Message != "no players on the current stage" {
		t.Fatalf("empty stage transport result = %+v", result)
	}
}
