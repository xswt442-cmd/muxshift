package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

func TestUnlinkSharedSourceStopsPlayerWhenNoLongerConsumed(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	app.window = &mygo.Window{}
	it := app.items[0]
	decoder := &lifecycleDecoder{closed: make(chan struct{}, 1)}
	player, err := ui.NewVideoPlayer(decoder)
	if err != nil {
		t.Fatal(err)
	}
	it.player = player
	generation := it.generation
	app.unlinkStageMember(it)
	if len(app.items) != 1 || app.items[0] != it || app.isStageMember(it) || it.player != nil || it.shown {
		t.Fatal("unlink should detach the player while keeping the shared source")
	}
	if it.generation != generation+1 || len(app.retiring) != 1 {
		t.Fatal("unlink did not schedule exactly one retirement")
	}
	for retirement := range app.retiring {
		if len(retirement.handles) != 1 || retirement.handles[0].player != player {
			t.Fatal("retirement did not acquire the detached player")
		}
	}
	app.shutdown()
	select {
	case <-decoder.closed:
	case <-time.After(time.Second):
		t.Fatal("the detached player's decoder is still open")
	}
}

func TestAlignInvalidatedWhenLinkChangesImplicitReference(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	first := app.items[0]
	first.persistentID = "first"
	first.player = &ui.VideoPlayer{}
	first.shown = false
	if _, err := app.session.SetSceneVideo(scene); err != nil {
		t.Fatal(err)
	}
	second := &item{source: core.Source{ID: "current-ref"}, persistentID: "second", player: &ui.VideoPlayer{}, shown: true}
	app.items = append(app.items, second)
	app.sceneMembers = map[core.SceneID]map[string]bool{scene: {second.persistentID: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.probeRunning, app.probeCancel = true, cancel
	app.alignRow = alignRowResult{RequestID: 1, Reference: second.source.ID}
	app.linkStageMember(first)
	if app.syncReference() != first || app.probeRunning || ctx.Err() == nil || app.alignRow.RequestID != 0 {
		t.Fatal("linking the new implicit reference did not cancel the alignment request")
	}
}

func TestAlignInvalidatedWhenReplacingSource(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	it := app.items[0]
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.probeRunning, app.probeCancel = true, cancel
	app.alignRow = alignRowResult{RequestID: 1, Target: it.source.ID}
	app.openReplacement(it, "replacement.mp4", true)
	if it.generation != 1 || app.probeRunning || ctx.Err() == nil || app.alignRow.RequestID != 0 {
		t.Fatal("replacing the target did not cancel the alignment request")
	}
}

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

func TestScenePlayerActivityFollowsVideoOrAudioConsumption(t *testing.T) {
	app, first, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, first)
	app.sceneMembers = map[core.SceneID]map[string]bool{first: {}}
	it := app.items[0]
	if _, err := app.session.SetSceneVideo(first); err != nil {
		t.Fatal(err)
	}
	if _, err := app.session.SetSceneAudio(first, it.audio, it.volume, false); err != nil {
		t.Fatal(err)
	}
	second, err := app.session.AddScene("unused")
	if err != nil {
		t.Fatal(err)
	}
	app.sceneMembers[second.ID] = map[string]bool{it.persistentID: true}
	if !app.sceneConsumesItem(first, it) {
		t.Fatal("a scene using the source's video or audio must keep its player active")
	}
	if app.sceneConsumesItem(second.ID, it) {
		t.Fatal("membership by itself must not keep an unused source player active")
	}
	if _, err := app.session.SetSceneAudio(second.ID, it.audio, it.volume, false); err != nil {
		t.Fatal(err)
	}
	if !app.sceneConsumesItem(second.ID, it) {
		t.Fatal("audio-only consumption must keep the source player active")
	}
	if _, err := app.session.RemoveSceneAudio(second.ID, it.audio); err != nil {
		t.Fatal(err)
	}
	if _, err := app.session.SetSceneVideo(second.ID, it.video); err != nil {
		t.Fatal(err)
	}
	if !app.sceneConsumesItem(second.ID, it) {
		t.Fatal("a shared source with video consumption must be active in both scenes")
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
	openCalls := 0
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) {
		openCalls++
		return &ui.VideoPlayer{}, nil
	}
	app.landImport(app.importToken, file, hls.Stream{}, importLocal, false, nil)
	if len(app.items) != 1 {
		t.Fatal("the completed import did not register its source")
	}
	imported := app.items[0]
	if imported.player != nil || openCalls != 0 {
		t.Fatal("an import completed for an inactive stage opened a background player")
	}
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

func TestRemovingOfflineStageMemberPreservesRemainingOrder(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	app.sceneMembers = map[core.SceneID]map[string]bool{scene: {}}
	first := app.items[0]
	if _, err := app.session.SetSceneVideo(scene); err != nil {
		t.Fatal(err)
	}
	if _, err := app.session.RemoveSceneAudio(scene, first.audio); err != nil {
		t.Fatal(err)
	}
	first.persistentID = "source-a"
	second := &item{persistentID: "source-b", volume: .7}
	third := &item{persistentID: "source-c", volume: .8}
	app.items = append(app.items, second, third)
	for _, it := range []*item{first, second, third} {
		it.player, it.video, it.audio = nil, "", ""
		app.linkStageMember(it)
	}
	bindings := app.pendingBindings[scene]
	if bindings["source-a"].videoIndex != 0 || bindings["source-b"].videoIndex != 1 || bindings["source-c"].videoIndex != 2 {
		t.Fatalf("video insertion indexes before remove = %+v", bindings)
	}
	app.unlinkStageMember(second)
	bindings = app.pendingBindings[scene]
	if bindings["source-a"].videoIndex != 0 || bindings["source-c"].videoIndex != 1 {
		t.Fatalf("remaining video indexes = %+v, want source-a then source-c", bindings)
	}
	snapshot := app.captureSessionStore()
	if got := snapshot.Scenes[0].Video; len(got) != 2 || got[0] != "source-a" || got[1] != "source-c" {
		t.Fatalf("persisted offline video order = %v", got)
	}
	if got := snapshot.Scenes[0].Audio; len(got) != 2 || got[0].Source != "source-a" || got[1].Source != "source-c" {
		t.Fatalf("persisted offline audio order = %+v", got)
	}
}

func TestRemovingLiveStageMemberKeepsOfflineSlotsOrdered(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	live := app.items[0]
	live.persistentID = "source-live"
	app.sceneMembers = map[core.SceneID]map[string]bool{scene: {live.persistentID: true}}
	offlineB := &item{persistentID: "source-b", volume: .7}
	offlineC := &item{persistentID: "source-c", volume: .8}
	app.items = append(app.items, offlineB, offlineC)
	app.linkStageMember(offlineB)
	app.linkStageMember(offlineC)
	app.unlinkStageMember(live)
	bindings := app.pendingBindings[scene]
	if bindings["source-b"].videoIndex != 0 || bindings["source-c"].videoIndex != 1 ||
		bindings["source-b"].audioIndex != 0 || bindings["source-c"].audioIndex != 1 {
		t.Fatalf("offline indexes after live removal = %+v", bindings)
	}
	snapshot := app.captureSessionStore()
	if got := snapshot.Scenes[0].Video; len(got) != 2 || got[0] != "source-b" || got[1] != "source-c" {
		t.Fatalf("persisted video order = %v", got)
	}
}

func TestPendingIndexCompactionOnRemoveHideAndOfflineAppend(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, scene)
	first := app.items[0]
	first.persistentID = "live-a"
	second := &item{uri: "https://example.test/second.mp4", persistentID: "live-b"}
	if err := app.addDirect(second); err != nil {
		t.Fatal(err)
	}
	app.items = append(app.items, second)
	if _, err := app.session.SetSceneVideo(scene, first.video, second.video); err != nil {
		t.Fatal(err)
	}
	if _, err := app.session.SetSceneAudio(scene, second.audio, 1, false); err != nil {
		t.Fatal(err)
	}
	app.sceneMembers = map[core.SceneID]map[string]bool{scene: {"live-a": true, "live-b": true}}
	for _, pending := range []struct {
		id    string
		index int
	}{{"offline-a", 0}, {"offline-b", 2}} {
		app.items = append(app.items, &item{persistentID: pending.id})
		app.sceneMembers[scene][pending.id] = true
		app.setPendingVideo(scene, pending.id, pending.index)
		app.setPendingAudio(scene, pending.id, storedAudio{Source: pending.id}, pending.index)
	}
	if err := app.showVideo(0); err != nil {
		t.Fatal(err)
	}
	appended := &item{persistentID: "offline-c"}
	app.items = append(app.items, appended)
	app.linkStageMember(appended)
	if err := app.remove(1); err != nil {
		t.Fatal(err)
	}
	stage := app.captureSessionStore().Scenes[0]
	if !slices.Equal(stage.Video, []string{"offline-a", "offline-b", "offline-c"}) {
		t.Fatalf("video order = %v", stage.Video)
	}
	var audio []string
	for _, route := range stage.Audio {
		audio = append(audio, route.Source)
	}
	if !slices.Equal(audio, []string{"offline-a", "live-a", "offline-b", "offline-c"}) {
		t.Fatalf("audio order = %v", audio)
	}
}

func TestSessionStoreReadMigratesScenesWithoutIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-session.json")
	if err := os.WriteFile(path, []byte(`{"scenes":[{"name":"First","members":[]},{"name":"Second","members":[]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, note := loadSessionStore(path)
	if note != "" || len(snapshot.Scenes) != 2 || snapshot.Scenes[0].ID == "" || snapshot.Scenes[1].ID == "" || snapshot.Scenes[0].ID == snapshot.Scenes[1].ID {
		t.Fatalf("migrated scenes = %+v (note %q)", snapshot.Scenes, note)
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
