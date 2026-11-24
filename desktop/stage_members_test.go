package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

func TestStageMembershipSharesSourcesWithoutSharingRoutes(t *testing.T) {
	app, first, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, first)
	source := app.items[0]
	app.addScene()
	second := app.scene
	if second == first || app.isStageMember(source) {
		t.Fatal("a new stage must start without sources")
	}
	app.linkStageMember(source)
	if !app.isStageMember(source) || !app.routed(source) || len(app.items) != 1 {
		t.Fatal("linking an existing source should reuse its item and add a scene route")
	}
	app.selectScene(first)
	if !app.isStageMember(source) {
		t.Fatal("the source lost its first stage membership")
	}
	app.unlinkStageMember(source)
	if app.isStageMember(source) || app.routed(source) || len(app.items) != 1 {
		t.Fatal("unlinking should remove only the active stage membership and route")
	}
	app.selectScene(second)
	if !app.isStageMember(source) || !app.routed(source) {
		t.Fatal("unlinking in the first stage changed the second stage")
	}
	if _, err := app.session.RemoveSceneAudio(second, source.audio); err != nil {
		t.Fatal(err)
	}
	if _, err := app.session.SetSceneVideo(second); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := saveSessionStore(path, app.captureSessionStore()); err != nil {
		t.Fatal(err)
	}
	snapshot, note := loadSessionStore(path)
	if note != "" || snapshot.Scenes[0].Members == nil || len(snapshot.Scenes[0].Members) != 0 {
		t.Fatalf("an explicitly empty stage must survive JSON: %+v (%s)", snapshot.Scenes[0], note)
	}
	restored, err := newAppStateFromStoreDeferred(defaultSettings(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.isStageMember(restored.items[0]) || len(snapshot.Scenes[1].Video) != 0 || len(snapshot.Scenes[1].Audio) != 0 {
		t.Fatal("a hidden member did not survive restore")
	}
	restored.selectScene(restored.session.Scenes()[0].ID)
	if restored.isStageMember(restored.items[0]) {
		t.Fatal("restoring an empty stage should not link shared sources")
	}
}

func TestImportLandsInTheStageWhereItStarted(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	start := app.scene
	app.importScene = start
	app.addScene()
	current := app.scene
	file := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.landImport(app.importToken, file, hls.Stream{}, importLocal, false, nil)
	if len(app.items) != 1 {
		t.Fatal("the completed import did not register its source")
	}
	imported := app.items[0]
	if app.scene != current || app.isStageMember(imported) || app.routed(imported) {
		t.Fatal("the completed import leaked into the current stage")
	}
	app.selectScene(start)
	if !app.isStageMember(imported) || !app.routed(imported) {
		t.Fatal("the stage that requested the import lost its source or audio route")
	}
}

func TestImportAfterTargetStageDeletionReportsCatalogLanding(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedTarget := app.scene
	app.addScene()
	app.importScene = deletedTarget
	app.removeScene(deletedTarget)
	file := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.landImport(app.importToken, file, hls.Stream{}, importLocal, false, nil)
	member := len(app.items) == 1 && app.isStageMember(app.items[0])
	if len(app.items) != 1 || member || app.lastCommand.Message == "" ||
		!strings.Contains(app.lastCommand.Message, "starting stage was deleted") {
		t.Fatalf("deleted-target import outcome: sources=%d member=%v result=%+v", len(app.items), member, app.lastCommand)
	}
}

func TestOfflineLinkRecordsSameDefaultBindingsAsOnlineLink(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	source := app.items[0]
	source.persistentID = "source-a"
	app.addScene()
	target := app.scene
	source.player, source.video, source.audio = nil, "", ""
	app.linkStageMember(source)
	binding := app.pendingBindings[target][source.persistentID]
	if !binding.video || binding.audio == nil || binding.audio.Source != source.persistentID || binding.audio.Volume != source.volume || binding.audio.Muted {
		t.Fatalf("offline link intent = %+v", binding)
	}
	if !app.sceneMembers[target][source.persistentID] {
		t.Fatal("offline source was not registered as a stage member")
	}
}

func TestStageCommandsRejectSharedCatalogOnlySources(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	app.items[0].persistentID = "source-a"
	app.addScene()
	result := app.runCommand(command{Kind: commandShow, Source: app.items[0].source.ID})
	if result.Err == nil || app.isStageMember(app.items[0]) {
		t.Fatalf("off-stage command result = %+v; source became a member=%v", result, app.isStageMember(app.items[0]))
	}
}

func TestUnlinkLastStageKeepsSourceUntilCatalogDeletion(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	source := app.items[0]
	source.persistentID = "source-a"
	player := source.player
	app.unlinkStageMember(source)
	if len(app.items) != 1 || app.items[0] != source || source.player != player || app.sourceHasStageMember(source.persistentID) {
		t.Fatal("unlinking the last stage closed or removed the catalog source")
	}
	app.removeCatalogSource(source)
	if len(app.items) != 0 {
		t.Fatal("explicit catalog deletion did not remove the unlinked source")
	}
}

func prepareStageMembershipFixture(t *testing.T, app *appState, scene core.SceneID) {
	t.Helper()
	app.scene = scene
	output, err := app.session.AddOutput(core.OutputDesktopWindow, scene)
	if err != nil {
		t.Fatal(err)
	}
	app.output = output.ID
}
