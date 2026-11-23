package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	if src.ID != "muxshift-source-1" || src.ID == string(sourceID) || src.URI != "https://example.test/camera.m3u8" {
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
	if len(scene.Video) != 1 || scene.Video[0] != src.ID {
		t.Fatalf("scene video = %v, want [%s]", scene.Video, src.ID)
	}
	if len(scene.Audio) != 1 || scene.Audio[0].Source != src.ID ||
		scene.Audio[0].Volume != 0.35 || !scene.Audio[0].Muted {
		t.Fatalf("scene audio = %+v", scene.Audio)
	}
}

func TestLateImportFailureRemovesSourceAndSchedulesSave(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.onMain = func(run func()) { run() }
	app.sessionPath = filepath.Join(t.TempDir(), "settings.json.session.json")
	app.sessionWriteEnabled = true
	saved := make(chan storedSession, 1)
	app.sessionStoreWriter = func(_ string, snapshot storedSession) error {
		saved <- snapshot
		return nil
	}
	item := app.items[0]
	item.generation = 7

	app.abandonImport(item, 7, errors.New("late decode failure"))

	if len(app.items) != 0 {
		t.Fatalf("the late failure left %d source(s) in the window", len(app.items))
	}
	select {
	case snapshot := <-saved:
		if len(snapshot.Sources) != 0 {
			t.Fatalf("the saved snapshot kept %d failed source(s)", len(snapshot.Sources))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the late failure did not schedule a session save")
	}
}

func TestSessionStoreRedactsSensitiveAddressAndKeepsSourceSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json.session.json")
	data := `{"scenes":[{"id":"scene-1","name":"Opening","video":["legacy-source"]}],"sources":[{"id":"legacy-source","uri":"https://user:fictional-pass@example.test/watch?token=fictional-token","name":"Camera A","volume":0.4,"muted":true,"shown":true}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, note := loadSessionStore(path)
	if note != "" {
		t.Fatalf("load note = %q, want none", note)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %+v, want retained source", got.Sources)
	}
	src := got.Sources[0]
	if src.URI != "" || !src.NeedsReselect || src.ID != "legacy-source" || src.Name != "Camera A" || src.Volume != 0.4 || !src.Muted || !src.Shown {
		t.Fatalf("redacted source = %+v", src)
	}
	if got.Scenes[0].Video[0] != "legacy-source" {
		t.Fatalf("scene binding = %v, want retained source identity", got.Scenes[0].Video)
	}
}

func TestSessionStoreRejectsTrailingDataWithoutChangingOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json.session.json")
	original := []byte(`{"scenes":[]} {"sources":[]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, note := loadSessionStore(path)
	if note == "" || len(got.Sources) != 0 || len(got.Scenes) != 0 {
		t.Fatalf("load = %+v, %q; want rejected empty snapshot with a note", got, note)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(after) != string(original) {
		t.Fatal("failed session load changed the original file")
	}
}

func TestPersistentURIOmitsSensitiveHTTPMaterial(t *testing.T) {
	for _, raw := range []string{
		"https://user:fictional-pass@example.test/watch",
		"https://example.test/watch?signature=fictional-signature",
		"https://example.test/watch#temporary-fragment",
	} {
		got, reselect := persistentURI(raw)
		if got != "" || !reselect {
			t.Errorf("persistentURI(%q) = %q, %v; want omitted address and reselect", raw, got, reselect)
		}
	}
	got, reselect := persistentURI("https://example.test/watch")
	if got != "https://example.test/watch" || reselect {
		t.Fatalf("safe address = %q, %v", got, reselect)
	}
}

func TestSessionSaveCoalescesAndFlushesLatestSnapshot(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.sessionPath = filepath.Join(t.TempDir(), "session.json")
	app.sessionWriteEnabled = true
	var mu sync.Mutex
	var writes []storedSession
	app.sessionStoreWriter = func(_ string, snap storedSession) error {
		mu.Lock()
		writes = append(writes, snap)
		mu.Unlock()
		return nil
	}
	app.saveSession()
	app.items[0].volume = 0.2
	app.saveSession()
	app.items[0].volume = 0.8
	app.saveSession()
	if err := app.flushSessionSave(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want one coalesced write", len(writes))
	}
	if got := writes[0].Sources[0].Volume; got != 0.8 {
		t.Fatalf("saved volume = %v, want last value 0.8", got)
	}
}

func TestSessionSaveDoesNotWriteTemporaryRun(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.sessionPath = filepath.Join(t.TempDir(), "session.json")
	var writes int
	app.sessionStoreWriter = func(string, storedSession) error {
		writes++
		return nil
	}
	if err := app.saveSession(); err != nil {
		t.Fatalf("save temporary run: %v", err)
	}
	if err := app.flushSessionSave(); err != nil {
		t.Fatalf("flush temporary run: %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero for an isolated run", writes)
	}
}

func TestSessionRestoreRetainsOrderedBindingsForUnavailableSources(t *testing.T) {
	snap := storedSession{
		ActiveScene: "scene-main",
		Scenes: []storedScene{{
			ID: "scene-main", Name: "Mix",
			Video: []string{"source-b", "source-a"},
			Audio: []storedAudio{
				{Source: "source-b", Volume: 0.3, Muted: true},
				{Source: "source-a", Volume: 0.8},
			},
		}},
		Sources: []storedSource{
			{ID: "source-a", URI: filepath.Join(t.TempDir(), "missing-a.mp4"), Name: "A", Volume: 0.8},
			{ID: "source-b", URI: filepath.Join(t.TempDir(), "missing-b.mp4"), Name: "B", Volume: 0.3, Muted: true},
		},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := app.captureSessionStore()
	if len(got.Scenes) != 1 || len(got.Scenes[0].Video) != 2 || got.Scenes[0].Video[0] != "source-b" || got.Scenes[0].Video[1] != "source-a" {
		t.Fatalf("video bindings after restore = %+v", got.Scenes)
	}
	if len(got.Scenes[0].Audio) != 2 || got.Scenes[0].Audio[0] != snap.Scenes[0].Audio[0] || got.Scenes[0].Audio[1] != snap.Scenes[0].Audio[1] {
		t.Fatalf("audio bindings after restore = %+v", got.Scenes[0].Audio)
	}
}

func TestSessionSaveCloseFlushCannotBeOvertakenByOlderTimer(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.sessionPath = filepath.Join(t.TempDir(), "session.json")
	app.sessionWriteEnabled = true
	started := make(chan struct{})
	releaseOld := make(chan struct{})
	var mu sync.Mutex
	var saved []float64
	app.sessionStoreWriter = func(_ string, snap storedSession) error {
		mu.Lock()
		index := len(saved)
		saved = append(saved, snap.Sources[0].Volume)
		mu.Unlock()
		if index == 0 {
			close(started)
			<-releaseOld
		}
		return nil
	}
	app.items[0].volume = 0.2
	app.saveSession()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("debounced write did not start")
	}
	app.items[0].volume = 0.9
	app.saveSession()
	flushed := make(chan error, 1)
	go func() { flushed <- app.flushSessionSave() }()
	close(releaseOld)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatalf("flush: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("final flush deadlocked behind the older write")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 2 || saved[0] != 0.2 || saved[1] != 0.9 {
		t.Fatalf("write order = %v, want [0.2 0.9]", saved)
	}
}

func TestSessionSaveIgnoresStaleCompletionAndCallback(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.sessionSaveSeq = 2
	var invalidations int
	app.sessionSaveNotify = func(fn func()) { fn() }
	app.requestFrame = func() { invalidations++ }
	app.finishSessionSnapshot(2, nil)
	app.finishSessionSnapshot(1, os.ErrPermission)
	if got := app.sessionSaveError(); got != "" {
		t.Fatalf("stale failed write replaced the newest success: %q", got)
	}
	if invalidations != 1 {
		t.Fatalf("invalidations = %d, want only the current completion", invalidations)
	}
}

func TestSessionStorePreservesLayoutStageEditsAndSync(t *testing.T) {
	app, sceneID, _ := newStoreFixture(t)
	it := app.items[0]
	it.persistentID = "source-a"
	app.persistentSceneIDs = map[string]core.SceneID{string(sceneID): sceneID}
	app.layout = layoutHero
	app.layoutTaken = []core.SourceID{it.source.ID}
	app.edits = map[core.SceneID]*stageEdits{
		sceneID: {
			boxes:  map[core.SourceID]stageBox{it.source.ID: {X: .1, Y: .2, W: .6, H: .7}},
			order:  []core.SourceID{it.source.ID},
			locked: map[core.SourceID]bool{it.source.ID: true},
		},
	}
	app.sync = newSyncState()
	if _, err := app.session.AddSyncGroup(it.video, it.video, it.audio); err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	if err := app.sync.timeline.SetShift(it.video, 420*time.Millisecond); err != nil {
		t.Fatalf("set video shift: %v", err)
	}
	if err := app.sync.timeline.SetShift(it.audio, -80*time.Millisecond); err != nil {
		t.Fatalf("set audio shift: %v", err)
	}
	snap := app.captureSessionStore()
	if snap.Layout != "hero" || len(snap.LayoutOrder) != 1 || snap.LayoutOrder[0] != "source-a" {
		t.Fatalf("layout snapshot = %+v", snap)
	}
	if len(snap.StageEdits) != 1 || len(snap.StageEdits[0].Boxes) != 1 || snap.StageEdits[0].Boxes[0].Source != "source-a" || len(snap.StageEdits[0].Locked) != 1 {
		t.Fatalf("stage edits = %+v", snap.StageEdits)
	}
	if len(snap.SyncGroups) != 1 || snap.SyncGroups[0].Reference != (storedTrackRef{Source: "source-a", Kind: "video"}) || len(snap.Shifts) != 2 {
		t.Fatalf("sync snapshot = groups %+v shifts %+v", snap.SyncGroups, snap.Shifts)
	}

	session := core.NewSession("restored")
	scene, err := session.AddScene("Opening")
	if err != nil {
		t.Fatal(err)
	}
	source, err := session.AddSource("C:\\media\\camera.mp4")
	if err != nil {
		t.Fatal(err)
	}
	video, err := session.AddTrack(source.ID, core.MediaVideo, "video")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := session.AddTrack(source.ID, core.MediaAudio, "audio")
	if err != nil {
		t.Fatal(err)
	}
	restoredSource, _ := session.Source(source.ID)
	restored := &appState{
		session:            session,
		items:              []*item{{persistentID: "source-a", source: restoredSource, video: video.ID, audio: audio.ID}},
		sync:               newSyncState(),
		pendingStageEdits:  snap.StageEdits,
		pendingSyncGroups:  snap.SyncGroups,
		pendingShifts:      snap.Shifts,
		persistentSceneIDs: map[string]core.SceneID{string(sceneID): scene.ID},
	}
	restored.restorePersistentMetadata(restored.persistentSceneIDs)
	if restored.layout != layoutFocus {
		t.Fatalf("zero-value layout changed unexpectedly: %v", restored.layout)
	}
	group, ok := restored.existingGroup()
	if !ok || len(group.Tracks) != 2 || group.Reference != video.ID {
		t.Fatalf("restored group = %+v, %v", group, ok)
	}
	if shift, ok := restored.sync.timeline.Shift(video.ID); !ok || shift.Offset != 420*time.Millisecond {
		t.Fatalf("video shift = %+v, %v", shift, ok)
	}
	edit := restored.edits[scene.ID]
	if edit == nil || edit.boxes[source.ID].X != .1 || !edit.locked[source.ID] || len(edit.order) != 1 || edit.order[0] != source.ID {
		t.Fatalf("restored stage edit = %+v", edit)
	}
}

func TestUnavailableSavedSourcesKeepIndependentKeysAndRemoval(t *testing.T) {
	snap := storedSession{
		Scenes: []storedScene{{ID: "scene-main", Name: "Mix", Video: []string{"source-a", "source-b"}}},
		Sources: []storedSource{
			{ID: "source-a", Name: "A", NeedsReselect: true},
			{ID: "source-b", Name: "B", NeedsReselect: true},
		},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	first, second := itemKey(app.items[0]), itemKey(app.items[1])
	if first == "" || second == "" || first == second {
		t.Fatalf("saved source keys = %q, %q", first, second)
	}
	if _, _, err := app.itemBySource(first); err != nil {
		t.Fatalf("find first unavailable source: %v", err)
	}
	if _, _, err := app.itemBySource(second); err != nil {
		t.Fatalf("find second unavailable source: %v", err)
	}
	if err := app.remove(0); err != nil {
		t.Fatalf("remove first unavailable source: %v", err)
	}
	got := app.captureSessionStore()
	if len(got.Sources) != 1 || got.Sources[0].ID != "source-b" || len(got.Scenes[0].Video) != 1 || got.Scenes[0].Video[0] != "source-b" {
		t.Fatalf("after removing A, snapshot = %+v", got)
	}
}

func TestRecoveredSourceUpdatesSelectionAndKeepsStableLookup(t *testing.T) {
	snap := storedSession{
		Scenes:  []storedScene{{ID: "scene-main"}},
		Sources: []storedSource{{ID: "source-a", Name: "A", NeedsReselect: true}},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	it := app.items[0]
	stable := itemKey(it)
	app.selected = stable
	app.reselectSource = stable
	it.source.ID = "core-source-new"
	app.updateRecoveredSourceKey(it, stable)
	if app.selected != it.source.ID || app.reselectSource != it.source.ID {
		t.Fatalf("recovered UI keys = selected %q reselect %q, want %q", app.selected, app.reselectSource, it.source.ID)
	}
	app.selected = stable // an already queued UI reference remains resolvable
	if selected, _, ok := app.selectedSource(); !ok || selected != it {
		t.Fatalf("stale saved key did not resolve: item %p, ok %v", selected, ok)
	}
	if found, _, err := app.itemBySource(stable); err != nil || found != it {
		t.Fatalf("stable command key resolved to %p, %v", found, err)
	}
}

func TestAddressReplacementRetainsBindingsStageAndSyncMetadata(t *testing.T) {
	app, sceneID, _ := newStoreFixture(t)
	it := app.items[0]
	it.persistentID = "source-a"
	app.persistentSceneIDs = map[string]core.SceneID{string(sceneID): sceneID}
	app.sync = newSyncState()
	app.layout = layoutSplit
	app.layoutTaken = []core.SourceID{it.source.ID}
	app.edits = map[core.SceneID]*stageEdits{
		sceneID: {
			boxes:  map[core.SourceID]stageBox{it.source.ID: {X: .15, Y: .2, W: .55, H: .6}},
			order:  []core.SourceID{it.source.ID},
			locked: map[core.SourceID]bool{it.source.ID: true},
		},
	}
	if _, err := app.session.AddSyncGroup(it.video, it.video, it.audio); err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	if err := app.sync.timeline.SetShift(it.video, 350*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := app.sync.timeline.SetShift(it.audio, 350*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	app.preserveSourceBeforeReplacement(it)
	oldSourceID := it.source.ID
	if err := app.session.RemoveSource(oldSourceID); err != nil {
		t.Fatalf("remove old source: %v", err)
	}
	newSource, err := app.session.AddSource("C:\\media\\replacement.mp4")
	if err != nil {
		t.Fatal(err)
	}
	video, err := app.session.AddTrack(newSource.ID, core.MediaVideo, "video")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := app.session.AddTrack(newSource.ID, core.MediaAudio, "audio")
	if err != nil {
		t.Fatal(err)
	}
	it.uri = newSource.URI
	it.source = newSource
	it.video = video.ID
	it.audio = audio.ID
	app.pruneStageEdits()
	app.restoreRetryBindings(it)
	app.restorePersistentMetadata(app.persistentSceneIDs)
	left := app.pendingLayoutOrder[:0]
	for _, id := range app.pendingLayoutOrder {
		if id == it.persistentID {
			app.layoutTaken = append(app.layoutTaken, it.source.ID)
		} else {
			left = append(left, id)
		}
	}
	app.pendingLayoutOrder = left
	if len(app.pendingLayoutOrder) != 0 || len(app.layoutTaken) != 1 || app.layoutTaken[0] != it.source.ID {
		t.Fatalf("layout order after replacement = pending %v active %v", app.pendingLayoutOrder, app.layoutTaken)
	}
	scene, err := app.session.Scene(sceneID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Video) != 1 || scene.Video[0] != it.video || len(scene.Audio) != 1 || scene.Audio[0].Track != it.audio || scene.Audio[0].Volume != .35 || !scene.Audio[0].Muted {
		t.Fatalf("scene bindings after replacement = %+v", scene)
	}
	if group, ok := app.existingGroup(); !ok || group.Reference != it.video || len(group.Tracks) != 2 {
		t.Fatalf("sync group after replacement = %+v, %v", group, ok)
	}
	if shift, ok := app.sync.timeline.Shift(it.audio); !ok || shift.Offset != 350*time.Millisecond {
		t.Fatalf("audio shift after replacement = %+v, %v", shift, ok)
	}
	if edits := app.edits[sceneID]; edits == nil || edits.boxes[it.source.ID].X != .15 || !edits.locked[it.source.ID] {
		t.Fatalf("stage edits after replacement = %+v", edits)
	}
}

func TestReselectRestoresTheSavedSceneBinding(t *testing.T) {
	media := filepath.Join(t.TempDir(), "replacement.mp4")
	if err := os.WriteFile(media, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := storedSession{
		ActiveScene: "scene-main",
		Scenes: []storedScene{{
			ID: "scene-main", Name: "Mix",
			Video: []string{"source-a"},
			Audio: []storedAudio{{Source: "source-a", Volume: .42, Muted: true}},
		}},
		Sources: []storedSource{{ID: "source-a", Name: "Camera", NeedsReselect: true, Volume: .42, Muted: true, Shown: true}},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	it := app.itemByPersistentID("source-a")
	if it == nil || it.source.ID != "" || it.openErr == nil {
		t.Fatalf("source before reselect = %+v", it)
	}
	source, err := app.session.AddSource(media)
	if err != nil {
		t.Fatal(err)
	}
	video, err := app.session.AddTrack(source.ID, core.MediaVideo, "video")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := app.session.AddTrack(source.ID, core.MediaAudio, "audio")
	if err != nil {
		t.Fatal(err)
	}
	it.source = source
	it.video = video.ID
	it.audio = audio.ID
	it.uri = media
	it.openErr = nil
	app.restoreRetryBindings(it)
	if it.persistentID != "source-a" || it.source.ID == "" || it.video == "" || it.audio == "" || it.uri != media {
		t.Fatalf("source after reselect = %+v", it)
	}
	scene, err := app.session.Scene(app.scene)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Video) != 1 || scene.Video[0] != it.video || len(scene.Audio) != 1 || scene.Audio[0].Track != it.audio || scene.Audio[0].Volume != .42 || !scene.Audio[0].Muted {
		t.Fatalf("scene binding after reselect = %+v", scene)
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
