package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

func TestAppearanceChangeDoesNotOverwriteUntrustedSettings(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	const damaged = "broken settings"
	if err := os.WriteFile(path, []byte(damaged), 0600); err != nil {
		t.Fatal(err)
	}
	app.settingsPath, app.settingsTrusted = path, false
	app.settingsDraft.Compact = !app.settings.Compact
	app.applyAppearanceNow()
	if app.settings.Compact != app.settingsDraft.Compact || app.settingsSaved.Load() {
		t.Fatal("appearance did not apply in memory or was reported saved")
	}
	app.resetAppearanceNow()
	app.settingsDraft.DefaultVolume = .25
	app.applySettingsCommand()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != damaged || app.settingsSaved.Load() {
		t.Fatalf("protected file was changed or reported saved: %v", err)
	}
}

func TestSettingsSaveFailureClearsSavedFlagAndAllowsRetry(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	app.settingsPath = t.TempDir()
	app.settingsSaved.Store(true)
	app.settingsDraft.Compact = !app.settings.Compact
	app.applyAppearanceNow()
	if app.settingsSaved.Load() || !app.settingsSaveDirty || app.settingsErr == "" {
		t.Fatal("the failed write left the settings marked saved or disabled the retry")
	}
	app.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	app.applySettingsCommand()
	loaded, note := loadSettings(app.settingsPath)
	if note != "" || loaded.Compact != app.settings.Compact || !app.settingsSaved.Load() || app.settingsSaveDirty {
		t.Fatalf("retry did not save the already-applied appearance: %s", note)
	}
}

func TestSettingsRejectsNonDecimalBudgetAndNaN(t *testing.T) {
	for _, input := range []string{"1_000", "0x1p0", "-0", "-0.0", "NaN"} {
		if _, ok := dvrFields[1].parse(input); ok {
			t.Errorf("accepted %q", input)
		}
	}
	if got, ok := dvrFields[1].parse(dvrFields[1].format(1)); got != 1 || !ok {
		t.Fatal("a one-byte limit did not round-trip")
	}
	s := defaultSettings()
	s.MasterVolume = math.NaN()
	if err := validateSettings(s); err == nil {
		t.Fatal("NaN master volume was accepted")
	}
}

func TestNavigateWorkspaceMixerWithNilDockHiddenAndHiddenSources(t *testing.T) {
	a := &appState{winW: 640}
	a.navigateWorkspace(workspaceMixer)
	if a.dockTab != 1 || !a.dockUp {
		t.Fatalf("mixer did not open with all panels available: tab %d", a.dockTab)
	}
	a.dockHidden = map[string]bool{dockSources: true, dockMixer: true}
	a.navigateWorkspace(workspaceMixer)
	if a.dockTab != 0 || !a.dockShown(dockMixer) {
		t.Fatalf("mixer did not open with Sources hidden: tab %d", a.dockTab)
	}
}

func TestNudgeRejectsNegativeRecordingPositionWithoutMutatingTimelineOrSaving(t *testing.T) {
	a, _, _ := newStoreFixture(t)
	a.sync = newSyncState()
	a.sessionPath, a.sessionWriteEnabled = filepath.Join(t.TempDir(), "session.json"), true
	it := a.items[0]
	player := &fakeSeekPlayer{ready: true, honourSeek: true}
	it.player = player
	it.replay = &dvr.Recording{StartBufferTime: 5 * time.Second}
	if _, err := a.nudge(0, time.Second); !errors.Is(err, core.ErrTimeRange) {
		t.Fatalf("negative recording position returned %v", err)
	}
	if len(a.sync.timeline.Shifts()) != 0 || a.sessionSaveSeq != 0 || player.seeks != 0 || len(a.session.SyncGroups()) != 0 {
		t.Fatal("a refused seek changed the timeline, player, group or saved session")
	}
}

func TestNudgeOnAnIndependentStageUsesItsPlayerClock(t *testing.T) {
	a, _, _ := newStoreFixture(t)
	a.sync = newSyncState()
	group, err := a.session.AddSyncGroup(a.items[0].audio, a.items[0].audio)
	if err != nil {
		t.Fatal(err)
	}
	a.sync.group = group.ID
	if err := a.sync.playhead.Seek(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	stage, err := a.session.AddScene("Independent")
	if err != nil {
		t.Fatal(err)
	}
	a.scene = stage.ID
	player := &fakeSeekPlayer{ready: true, honourSeek: true, position: 2 * time.Second}
	it := &item{uri: "https://example.test/independent.mp4", player: player, persistentID: "independent"}
	if err := a.addDirect(it); err != nil {
		t.Fatal(err)
	}
	a.items = append(a.items, it)
	a.sceneMembers = map[core.SceneID]map[string]bool{stage.ID: {"independent": true}}
	if _, err := a.nudge(1, time.Second); err != nil {
		t.Fatal(err)
	}
	if player.position != 3*time.Second || a.sync.playhead.Event() != 30*time.Second || a.sourceInExplicitGroup(it) {
		t.Fatalf("independent nudge used the group clock or membership: %s", player.position)
	}
	for _, track := range []core.TrackID{it.video, it.audio} {
		if mapped, err := a.sync.timeline.MediaTime(track, a.sync.playhead.Event()); err != nil || mapped != player.position {
			t.Fatalf("track %s maps to %s, %v, want %s", track, mapped, err, player.position)
		}
	}
}

func TestAlignCoreOffsetIncludesNonZeroRefShiftAndExpiresOnRefNudge(t *testing.T) {
	a, _, _ := newStoreFixture(t)
	a.sync = newSyncState()
	ref := a.items[0]
	ref.player = &fakeSeekPlayer{ready: true, honourSeek: true}
	tgt := &item{uri: "https://example.test/target.mp4"}
	if err := a.addDirect(tgt); err != nil {
		t.Fatal(err)
	}
	a.items = append(a.items, tgt)
	if err := a.sync.timeline.SetShift(ref.audio, 4*time.Second); err != nil {
		t.Fatal(err)
	}
	reference := alignWindow{Source: ref.source.ID, Track: ref.audio, Start: time.Second}
	target := alignWindow{Source: tgt.source.ID, Track: tgt.audio, Start: 3 * time.Second}
	snapshot := a.probeOffsets(reference, target)
	if got, err := proposedCoreOffset(snapshot, time.Second); got != 7*time.Second || err != nil {
		t.Fatalf("proposed offset = %s, error = %v", got, err)
	}
	if _, err := a.nudge(0, time.Second); err != nil {
		t.Fatal(err)
	}
	if reason := a.expiredAgainst(alignSuggestion{Reference: reference, Target: target, Snapshot: snapshot}); !strings.Contains(reason, "reference") {
		t.Fatalf("reference nudge left a fresh estimate: %q", reason)
	}
}

func TestAlignInvalidatedOnUnlinkAndImplicitRefChange(t *testing.T) {
	a, scene, _ := newStoreFixture(t)
	a.sync = newSyncState()
	first := a.items[0]
	first.player = &fakeSeekPlayer{ready: true}
	second := &item{uri: "https://example.test/another.mp4", player: &fakeSeekPlayer{ready: true}}
	if err := a.addDirect(second); err != nil {
		t.Fatal(err)
	}
	a.items = append(a.items, second)
	if _, err := a.session.SetSceneVideo(scene, first.video, second.video); err != nil {
		t.Fatal(err)
	}
	a.apply()
	a.alignRow = alignRowResult{RequestID: 1, Reference: first.source.ID, Target: second.source.ID}
	a.probeRunning = true
	a.setPrimary(second.source.ID)
	if a.probeRunning || a.alignRow.RequestID != 0 {
		t.Fatal("changing the implicit reference left the estimate running")
	}
	a.alignRow = alignRowResult{RequestID: 2, Reference: second.source.ID, Target: first.source.ID}
	a.probeRunning = true
	a.unlinkStageMember(first)
	if a.probeRunning || a.alignRow.RequestID != 0 {
		t.Fatal("unlinking the target left its estimate running")
	}
}

func TestAlignFailureRedactsSignedURL(t *testing.T) {
	reason := trimFailure("ffmpeg: https://example.test/live?token=secret#key returned HTTP 403", "")
	if strings.Contains(reason, "secret") || !strings.Contains(reason, "403") {
		t.Fatalf("unexpected probe reason %q", reason)
	}
}

func TestSnapshotEntryReadsVideoOnlyShift(t *testing.T) {
	a := &appState{sync: newSyncState()}
	it := &item{video: "track-video"}
	if err := a.sync.timeline.SetShift(it.video, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := a.snapshotEntry(it); !got.Aligned || got.Offset != 2*time.Second {
		t.Fatalf("video-only alignment = %+v", got)
	}
}

func TestViewingMuteUsesSourceMutedAndEnterCinemaLeavesFullscreen(t *testing.T) {
	a := newViewingAppState()
	w := &fakeWindow{bounds: mygo.Rectangle{Width: 1100, Height: 720}}
	it := &item{audio: "track-audio", player: &fakeSeekPlayer{}}
	if a.sourceMuted(it) {
		t.Fatal("output mute was mistaken for source mute")
	}
	a.enterViewing(viewFullscreen, w)
	a.enterViewing(viewCinema, w)
	if w.fullscreen || a.viewing != viewCinema || w.bounds.Width != 1100 {
		t.Fatal("cinema retained fullscreen or lost the workbench bounds")
	}
}

func TestOpenSiteAddressRedactsMalformedCredentialedURL(t *testing.T) {
	a := &appState{}
	result, opened := a.openSiteAddress("https://user:secret@[broken?token=secret")
	if opened || result.Err == nil || strings.Contains(result.Err.Error(), "secret") {
		t.Fatalf("malformed address returned %v", result.Err)
	}
}

func TestAboutOpenReleaseUsesContextFallback(t *testing.T) {
	a := &appState{}
	var opened string
	a.openReleasePage("https://example.test/release", func(uri string, done func(error)) {
		opened = uri
		done(errors.New("failed: https://example.test/release?token=secret"))
	})
	if opened == "" || a.lastErr == nil || strings.Contains(a.lastErr.Error(), "secret") {
		t.Fatal("About did not use the context opener or sanitize its error")
	}
}
