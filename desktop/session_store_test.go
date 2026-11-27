package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/internal/filelock"
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
	if got.ActiveScene != "stage-1" {
		t.Fatalf("active scene ID = %q, want stable persisted ID stage-1 (runtime %q)", got.ActiveScene, sceneID)
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
	if scene.ID != got.ActiveScene || scene.Name != "Opening" {
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
	item.handover = &handover{}
	app.abandonImport(item, 7, errors.New("replacement failed"), false)
	if len(app.items) != 1 || item.handover == nil {
		t.Fatal("the first-open watcher removed a source during player handover")
	}
	item.handover = nil

	app.abandonImport(item, 7, errors.New("late decode failure"), false)

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

func TestSessionStoreAllowsOnlyOneWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json.session.json")
	lock, err := lockSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := lockSessionStore(path); !errors.Is(err, filelock.ErrLocked) {
		second.Close()
		t.Fatalf("second writer: %v, want occupied session", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := lockSessionStore(path)
	if err != nil {
		t.Fatalf("reacquire released session: %v", err)
	}
	next.Close()
}

func TestDeletingPendingSourceKeepsRemainingSyncTracks(t *testing.T) {
	a := &appState{pendingSyncGroups: []storedSyncGroup{{
		Reference: storedTrackRef{Source: "a", Kind: "audio"},
		Tracks: []storedTrackRef{
			{Source: "a", Kind: "audio"}, {Source: "b", Kind: "audio"}, {Source: "c", Kind: "video"},
		},
	}}}
	a.forgetPendingSource("a")
	if len(a.pendingSyncGroups) != 1 {
		t.Fatalf("pending sync groups: %+v", a.pendingSyncGroups)
	}
	group := a.pendingSyncGroups[0]
	if len(group.Tracks) != 2 || group.Reference != group.Tracks[0] || group.Reference.Source != "b" {
		t.Fatalf("remaining sync group: %+v", group)
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

func TestSessionSaveOverlappingDebounceTimers(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.sessionPath = filepath.Join(t.TempDir(), "session.json")
	app.sessionWriteEnabled = true
	writes := make(chan float64, 2)
	releaseFirst := make(chan struct{})
	app.sessionStoreWriter = func(_ string, snapshot storedSession) error {
		level := snapshot.Sources[0].Volume
		writes <- level
		if level == 0.2 {
			<-releaseFirst
		}
		return nil
	}
	app.items[0].volume = 0.2
	app.saveSession()
	select {
	case <-writes:
	case <-time.After(3 * time.Second):
		close(releaseFirst)
		t.Fatal("first timer did not start")
	}
	app.items[0].volume = 0.9
	app.saveSession()
	time.Sleep(300 * time.Millisecond)
	close(releaseFirst)
	select {
	case level := <-writes:
		if level != 0.9 {
			t.Fatalf("second write level = %v, want 0.9", level)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second timer could not flush while the first writer completed")
	}
	if err := app.flushSessionSave(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRestoreProjectsRoutesInSavedOrder(t *testing.T) {
	app, err := newAppStateFromStore(defaultSettings(), storedSession{
		Scenes: []storedScene{{ID: "scene", Video: []string{"a", "b", "c"}, Audio: []storedAudio{
			{Source: "a", Volume: 0.2}, {Source: "b", Volume: 0.4}, {Source: "c", Volume: 0.8, Muted: true},
		}}},
		Sources: []storedSource{{ID: "a", NeedsReselect: true}, {ID: "b", NeedsReselect: true}, {ID: "c", NeedsReselect: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c", "b", "a"} {
		it := app.itemByPersistentID(id)
		if it == nil {
			t.Fatalf("missing source %s", id)
		}
		it.uri = "https://example.test/" + id + ".mp4"
		if err := app.addDirect(it); err != nil {
			t.Fatal(err)
		}
		app.restoreRetryBindings(it)
	}
	got := app.captureSessionStore().Scenes[0]
	if !slices.Equal(got.Video, []string{"a", "b", "c"}) || len(got.Audio) != 3 ||
		got.Audio[0].Source != "a" || got.Audio[1].Source != "b" || got.Audio[2] != (storedAudio{Source: "c", Volume: 0.8, Muted: true}) {
		t.Fatalf("unexpected routes: %+v", got)
	}
}

func TestRestoredSyncGroupKeepsSurvivingGroupIdentity(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	original := app.items[0]
	replacement := &item{uri: "https://example.test/replacement.mp4"}
	if err := app.addDirect(replacement); err != nil {
		t.Fatal(err)
	}
	group, err := app.session.AddSyncGroup(original.audio, original.audio)
	if err != nil {
		t.Fatal(err)
	}
	got, err := app.restoreSyncGroup(replacement.audio, []core.TrackID{original.audio, replacement.audio})
	if err != nil || got.ID != group.ID || got.Reference != replacement.audio ||
		!slices.Equal(got.Tracks, []core.TrackID{original.audio, replacement.audio}) {
		t.Fatalf("restored group = %+v, err = %v", got, err)
	}
}

func TestStageLayoutsMigrateAndSaveIndependently(t *testing.T) {
	legacy := storedSession{
		ActiveScene: "one", Layout: "pip", LayoutOrder: []string{"b", "a"},
		Scenes:  []storedScene{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}},
		Sources: []storedSource{{ID: "a", NeedsReselect: true}, {ID: "b", NeedsReselect: true}},
	}
	app, err := newAppStateFromStore(defaultSettings(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	scenes := app.session.Scenes()
	app.setLayout(layoutGrid)
	app.selectScene(scenes[1].ID)
	if app.layout != layoutPip || len(app.pendingLayoutOrder) != 0 {
		t.Fatalf("second stage should inherit the preset without the active stage's order: %s %v", app.layout, app.pendingLayoutOrder)
	}
	app.setLayout(layoutSplit)
	app.selectScene(scenes[0].ID)
	if app.layout != layoutGrid || app.session.Outputs()[0].Scene != scenes[0].ID || !slices.Equal(app.pendingLayoutOrder, legacy.LayoutOrder) {
		t.Fatalf("first stage did not recover its preset or output: %s %+v", app.layout, app.session.Outputs())
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := saveSessionStore(path, app.captureSessionStore()); err != nil {
		t.Fatal(err)
	}
	snap, note := loadSessionStore(path)
	if note != "" || snap.Scenes[0].Layout != "grid" || snap.Scenes[1].Layout != "split" {
		t.Fatalf("stage presets did not round trip: %+v (%s)", snap.Scenes, note)
	}
	app.removeScene(scenes[0].ID)
	if len(app.session.Scenes()) != 1 || app.layout != layoutSplit || app.session.Outputs()[0].Scene != scenes[1].ID {
		t.Fatalf("removing active stage did not move layout and output: %s %+v", app.layout, app.session.Outputs())
	}
}

func TestLegacyGlobalLayoutOrderBelongsOnlyToActiveStage(t *testing.T) {
	snap := storedSession{
		ActiveScene: "second", Layout: "pip", LayoutOrder: []string{"source-b"},
		Scenes:  []storedScene{{ID: "first"}, {ID: "second"}},
		Sources: []storedSource{{ID: "source-a", NeedsReselect: true}, {ID: "source-b", NeedsReselect: true}},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatal(err)
	}
	scenes := app.session.Scenes()
	a, b := app.itemByPersistentID("source-a"), app.itemByPersistentID("source-b")
	app.selectScene(scenes[0].ID)
	if app.isStageMember(b) {
		t.Fatal("legacy global order leaked active-stage membership into another stage")
	}
	app.selectScene(scenes[1].ID)
	if !app.isStageMember(b) || app.isStageMember(a) {
		t.Fatalf("active-stage migration membership: b=%v a=%v", app.isStageMember(b), app.isStageMember(a))
	}
}

func TestPersistentStageIDsSurviveDeletingMiddleStageAndRestore(t *testing.T) {
	app, first, _ := newStoreFixture(t)
	prepareStageMembershipFixture(t, app, first)
	source := app.items[0]
	source.persistentID = "source-a"
	app.addScene()
	middle := app.scene
	app.addScene()
	last := app.scene
	if middle == first || last == middle {
		t.Fatal("could not create and select the middle and last stages")
	}
	app.linkStageMember(source)
	lastPersistent := app.persistentSceneID(last)
	app.pendingStageEdits = []storedStageEdits{{
		Scene: lastPersistent, Order: []string{source.persistentID}, Locked: []string{source.persistentID},
	}}
	app.removeScene(middle)
	firstSnap := app.captureSessionStore()
	if len(firstSnap.Scenes) != 2 || firstSnap.ActiveScene != lastPersistent {
		t.Fatalf("snapshot stage IDs = %+v, active=%q, want two stages and active=%q", firstSnap.Scenes, firstSnap.ActiveScene, lastPersistent)
	}
	snapshot := firstSnap
	for reopen := 1; reopen <= 2; reopen++ {
		restored, err := newAppStateFromStoreDeferred(defaultSettings(), snapshot)
		if err != nil {
			t.Fatal(err)
		}
		snapshot = restored.captureSessionStore()
		if snapshot.ActiveScene != lastPersistent || snapshot.Scenes[1].ID != lastPersistent ||
			!slices.Equal(snapshot.Scenes[1].Members, []string{source.persistentID}) ||
			!slices.Equal(snapshot.Scenes[1].Video, []string{source.persistentID}) ||
			len(snapshot.Scenes[1].Audio) != 1 || snapshot.Scenes[1].Audio[0].Source != source.persistentID {
			t.Fatalf("reopen %d changed stage identity or routes: %+v active=%q", reopen, snapshot.Scenes, snapshot.ActiveScene)
		}
		if len(snapshot.StageEdits) != 1 || snapshot.StageEdits[0].Scene != lastPersistent ||
			!slices.Equal(snapshot.StageEdits[0].Order, []string{source.persistentID}) ||
			!slices.Equal(snapshot.StageEdits[0].Locked, []string{source.persistentID}) {
			t.Fatalf("reopen %d changed pending placement: %+v", reopen, snapshot.StageEdits)
		}
	}
}

func TestMissingStageIDDoesNotTakeLaterValidID(t *testing.T) {
	snap := storedSession{
		ActiveScene: "stage-1",
		Scenes:      []storedScene{{ID: ""}, {ID: "stage-1"}},
	}
	app, err := newAppStateFromStore(defaultSettings(), snap)
	if err != nil {
		t.Fatal(err)
	}
	stored := app.captureSessionStore()
	if stored.Scenes[0].ID == "stage-1" || stored.Scenes[1].ID != "stage-1" || stored.ActiveScene != "stage-1" {
		t.Fatalf("ID allocation stole a valid later ID: %+v active=%q", stored.Scenes, stored.ActiveScene)
	}
}

func TestSessionStoreCaptureOwnsNestedSlices(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	other, err := app.session.AddScene("Inactive stage")
	if err != nil {
		t.Fatal(err)
	}
	source := app.items[0]
	source.persistentID = "saved-source"
	order := []string{source.persistentID}
	otherID := app.persistentSceneID(other.ID)
	app.sceneLayouts = map[core.SceneID]storedSceneLayout{other.ID: {Layout: "grid", LayoutOrder: order}}
	app.pendingStageEdits = []storedStageEdits{{Scene: otherID, Order: []string{source.persistentID}}}
	app.pendingSyncGroups = []storedSyncGroup{{Tracks: []storedTrackRef{{Source: source.persistentID}}}}

	snapshot := app.captureSessionStore()
	order[0] = "changed"
	app.pendingStageEdits[0].Order[0] = "changed"
	app.pendingSyncGroups[0].Tracks[0].Source = "changed"

	if got := snapshot.Scenes[1].LayoutOrder; !slices.Equal(got, []string{source.persistentID}) {
		t.Fatalf("captured layout changed: %v", got)
	}
	if got := snapshot.StageEdits[0].Order; !slices.Equal(got, []string{source.persistentID}) {
		t.Fatalf("captured stage edits changed: %v", got)
	}
	if got := snapshot.SyncGroups[0].Tracks[0].Source; got != source.persistentID {
		t.Fatalf("captured sync group changed: %q", got)
	}
}

func TestRestoringLegacySourceIDDoesNotAliasRegisteredSource(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.items[0].persistentID = "source-a"
	app.items[0].source.ID = "source-1"
	placeholder := &item{persistentID: "source-1", uri: "", source: core.Source{ID: ""}}
	app.items = append(app.items, placeholder)
	got, _, err := app.itemBySource("saved:source-1")
	if err != nil || got != placeholder {
		t.Fatalf("namespaced lookup = %p, %v; want placeholder %p", got, err, placeholder)
	}
	got, _, err = app.itemBySource("source-1")
	if err != nil || got != app.items[0] {
		t.Fatalf("legacy Core lookup = %p, %v; want registered source %p", got, err, app.items[0])
	}
}

func TestExclusiveAudioRouteDropsOnlyPendingAudio(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	app.pendingBindings = map[core.SceneID]map[string]pendingSceneBinding{
		scene: {
			"late-audio": {audio: &storedAudio{Source: "late-audio"}, audioIndex: 0},
			"late-both":  {video: true, videoIndex: 1, audio: &storedAudio{Source: "late-both"}, audioIndex: 1},
		},
	}
	app.clearPendingSceneAudio(scene)
	if _, ok := app.pendingBindings[scene]["late-audio"]; ok {
		t.Fatal("audio-only pending route survived exclusive routing")
	}
	binding := app.pendingBindings[scene]["late-both"]
	if !binding.video || binding.audio != nil {
		t.Fatalf("pending video/audio after exclusive route = %+v", binding)
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
	for _, id := range app.pendingLayoutOrder {
		if id == it.persistentID {
			app.layoutTaken = append(app.layoutTaken, it.source.ID)
		}
	}
	if len(app.pendingLayoutOrder) != 1 || app.pendingLayoutOrder[0] != it.persistentID || len(app.layoutTaken) != 1 || app.layoutTaken[0] != it.source.ID {
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

func TestLoadSessionStoreRejectsTopLevelNullAndDeduplicatesSourceIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(" \n null \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, note := loadSessionStore(path); note == "" {
		t.Error("top-level null was accepted as a writable snapshot")
	}
	data := `{"sources":[{"id":"first","uri":" https://example.test/one.mp4 "},
		{"id":"first","uri":"https://example.test/two.mp4"},
		{"uri":"https://example.test/three.mp4"},
		{"id":"muxshift-source-1","uri":"https://example.test/four.mp4"}],
		"scenes":[{"video":["first","muxshift-source-1"]}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, note := loadSessionStore(path)
	if note != "" || len(snap.Sources) != 4 {
		t.Fatalf("load = %+v, note = %q", snap, note)
	}
	seen := map[string]bool{}
	for _, source := range snap.Sources {
		if source.ID == "" || seen[source.ID] {
			t.Errorf("missing or repeated source ID %q", source.ID)
		}
		seen[source.ID] = true
	}
	if snap.Sources[0].ID != "first" || snap.Sources[3].ID != "muxshift-source-1" || snap.Sources[0].URI != "https://example.test/one.mp4" {
		t.Errorf("the first IDs or trimmed address were not retained: %+v", snap.Sources)
	}
}

func TestUnlinkCancelsUnavailableRetry(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	it := app.items[0]
	it.persistentID = "camera"
	originalURI := it.uri
	replacement := filepath.Join(t.TempDir(), "replacement.mp4")
	app.retryScene = scene
	app.retryTargetID = it.persistentID
	app.importing = replacement
	app.importDone = make(chan struct{})
	done := app.importDone
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) {
		t.Fatal("the stale retry opened a player")
		return nil, nil
	}

	token := app.importToken
	app.unlinkStageMember(it)
	if app.isStageMember(it) || app.importing != "" || app.retryTargetID != "" {
		t.Fatal("unlink did not clear the pending retry")
	}
	select {
	case <-done:
	default:
		t.Fatal("unlink left the retry ticker running")
	}
	if err := app.retryUnavailableSource(it, replacement); err == nil {
		t.Fatal("a source outside the active stage could start a retry")
	}
	app.linkStageMember(it)
	if !app.isStageMember(it) {
		t.Fatal("the source was not linked back")
	}
	app.landUnavailableRetry(token, it.persistentID, replacement, importPlan{uri: replacement, mode: importDirect}, nil)
	if it.uri != originalURI {
		t.Fatalf("the stale retry changed the source address to %q", it.uri)
	}
}

func TestPlaceholderLayerPreservesPendingOrder(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	it := app.items[0]
	it.source = core.Source{}
	it.persistentID = "offline"
	app.persistentSceneIDs = map[string]core.SceneID{"stage": scene}
	app.pendingStageEdits = []storedStageEdits{{Scene: "stage", Order: []string{"offline", "other"}}}
	app.raiseStage(it.source.ID, false)
	app.lowerStage(itemKey(it), false)
	if note := app.layerNote(it); note != "" || len(app.edits) != 0 {
		t.Fatalf("placeholder layer note = %q, edits = %+v", note, app.edits)
	}
	if len(app.pendingStageEdits) != 1 || !slices.Equal(app.pendingStageEdits[0].Order, []string{"offline", "other"}) {
		t.Fatalf("placeholder changed the pending layer order: %+v", app.pendingStageEdits)
	}
}

func TestMovingLoadedLayerPreservesOfflineSlot(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	back := app.items[0]
	back.player, back.persistentID = &ui.VideoPlayer{}, "back"
	front := &item{uri: "https://example.test/front", persistentID: "front", player: &ui.VideoPlayer{}, shown: true}
	if err := app.addDirect(front); err != nil {
		t.Fatal(err)
	}
	app.items = append(app.items, &item{persistentID: "offline"}, front)
	app.persistentSceneIDs = map[string]core.SceneID{"stage": scene}
	app.pendingStageEdits = []storedStageEdits{{Scene: "stage", Order: []string{"back", "offline", "front"}}}
	app.raiseStage(back.source.ID, false)
	want := []string{"front", "offline", "back"}
	if got := app.pendingStageEdits[0].Order; !slices.Equal(got, want) {
		t.Fatalf("pending order = %v, want %v", got, want)
	}
	saved := app.captureSessionStore()
	if len(saved.StageEdits) != 1 || !slices.Equal(saved.StageEdits[0].Order, want) {
		t.Fatalf("saved layer edits = %+v", saved.StageEdits)
	}
}

func TestLandUnavailableRetryKeepsSourceOnLateDecodeFailure(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	app.sync = newSyncState()
	it := app.items[0]
	it.persistentID = "camera"
	app.retryScene = scene
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) {
		return &ui.VideoPlayer{}, nil
	}
	uri := filepath.Join(t.TempDir(), "replacement.mp4")
	app.landUnavailableRetry(0, it.persistentID, uri, importPlan{uri: uri, mode: importDirect}, nil)
	if app.lastCommand.Err != nil || !it.restored || it.player == nil {
		t.Fatalf("reselect did not preserve the saved identity: %v", app.lastCommand.Err)
	}
	app.onMain = func(fn func()) { fn() }
	app.abandonImport(it, it.generation, errors.New("late decode failure"), false)
	t.Cleanup(func() {
		close(app.closeDone)
		app.closeWait.Wait()
	})
	if len(app.items) != 1 || app.items[0] != it || it.openErr == nil || it.shown {
		t.Fatalf("decode failure changed the saved source or left it showing: %+v", it)
	}
	snap := app.captureSessionStore()
	if !slices.Equal(snap.Scenes[0].Video, []string{"camera"}) ||
		len(snap.Scenes[0].Audio) != 1 || snap.Scenes[0].Audio[0].Source != "camera" {
		t.Fatalf("decode failure lost routes: %+v", snap.Scenes[0])
	}
}

func TestPreserveUnrelatedSourceKeepsActiveSyncGroup(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	app.sync = newSyncState()
	first := app.items[0]
	group, err := app.session.AddSyncGroup(first.audio, first.audio)
	if err != nil {
		t.Fatal(err)
	}
	app.sync.group = group.ID
	unrelated := &item{uri: "https://example.test/other.mp4", persistentID: "other"}
	if err := app.addDirect(unrelated); err != nil {
		t.Fatal(err)
	}
	app.items = append(app.items, unrelated)
	app.preserveSourceBeforeReplacement(unrelated)
	if err := app.session.RemoveSource(unrelated.source.ID); err != nil {
		t.Fatal(err)
	}
	app.keepValidActiveSyncGroup()
	if app.sync.group != group.ID {
		t.Fatalf("replacing an unrelated source cleared active group %s", group.ID)
	}
}

func TestPreserveRepeatedReplacementDoesNotDuplicatePending(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	app.sync = newSyncState()
	it := app.items[0]
	it.persistentID = "camera"
	if _, err := app.session.AddSyncGroup(it.audio, it.audio); err != nil {
		t.Fatal(err)
	}
	if err := app.sync.timeline.SetShift(it.audio, time.Second); err != nil {
		t.Fatal(err)
	}
	app.sceneEdits().locked[it.source.ID] = true
	savedAudio := storedTrackRef{Source: "camera", Kind: "audio"}
	offlineAudio := storedTrackRef{Source: "offline", Kind: "audio"}
	app.pendingSyncGroups = []storedSyncGroup{{Reference: offlineAudio, Tracks: []storedTrackRef{savedAudio, offlineAudio}}}
	app.preserveSourceBeforeReplacement(it)
	app.preserveSourceBeforeReplacement(it)
	if len(app.pendingSyncGroups) != 1 || len(app.pendingShifts) != 1 || len(app.pendingStageEdits) != 1 {
		t.Fatalf("pending metadata grew: groups=%v shifts=%v edits=%v", app.pendingSyncGroups, app.pendingShifts, app.pendingStageEdits)
	}
	if app.pendingSyncGroups[0].Reference != offlineAudio || len(app.pendingSyncGroups[0].Tracks) != 2 {
		t.Fatalf("replacing a live subset lost the complete pending group: %+v", app.pendingSyncGroups)
	}
	if binding := app.pendingBindings[scene]["camera"]; binding.videoIndex != 0 || binding.audioIndex != 0 {
		t.Fatalf("repeated replacement moved the pending routes: %+v", binding)
	}
}

func TestDeferredHiddenMemberAndCancelledRestoreRemainOpenable(t *testing.T) {
	app, err := newAppStateFromStoreDeferred(defaultSettings(), storedSession{
		ActiveScene: "active",
		Scenes: []storedScene{
			{ID: "active", Members: []string{"hidden"}},
			{ID: "other", Members: []string{"hidden"}},
		},
		Sources: []storedSource{{ID: "hidden", URI: "https://example.test/clip.mp4"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	it := app.first()
	app.selectVideo(it)
	if app.lastCommand.Err != nil || !app.pendingBindings[app.scene]["hidden"].video {
		t.Fatalf("show did not bind the hidden source: %v", app.lastCommand.Err)
	}
	it.restoring = true
	app.startupRestoreCancel = func() {}
	if err := app.retryUnavailableSource(it, it.uri); err == nil {
		t.Fatal("manual retry started while this source was already recovering")
	}
	if result := app.runCommand(command{Kind: commandCancelRestore}); result.Err != nil || it.openErr == nil || it.restoring {
		t.Fatalf("cancel did not leave a retryable source: %+v", result)
	}
	app.selectVideo(it)
	if app.lastCommand.Err != nil || app.pictureRequested(it) {
		t.Fatalf("hide after cancellation did not remove the video binding: %v", app.lastCommand.Err)
	}
	app.selectVideo(it)
	if app.lastCommand.Err != nil || it.openErr != nil || !app.pendingBindings[app.scene]["hidden"].video {
		t.Fatalf("show after cancellation did not keep the binding: %v", app.lastCommand.Err)
	}
	app.selectAudio(it)
	snap := app.captureSessionStore()
	if app.lastCommand.Err != nil || len(snap.Scenes[0].Audio) != 1 || len(snap.Scenes[1].Audio) != 0 {
		t.Fatalf("audio recovery altered routes outside the active stage: %+v", snap.Scenes)
	}
}

func TestPlaceholderCannotCorruptLayoutTakenOrStageLocks(t *testing.T) {
	app, scene, _ := newStoreFixture(t)
	placeholder := &item{persistentID: "hidden", shown: true}
	app.items = append(app.items, placeholder)
	if result := app.runCommand(command{Kind: commandPrimary, Source: itemKey(placeholder)}); result.Err == nil {
		t.Fatal("placeholder was accepted as the primary picture")
	}
	app.toggleStageLock("")
	app.apply()
	if placeholder.shown || len(app.layoutTaken) != 0 || app.edits[scene] != nil {
		t.Fatal("placeholder left a picture, primary position or lock behind")
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
