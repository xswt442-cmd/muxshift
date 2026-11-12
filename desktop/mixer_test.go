package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

// mixerFixture builds a session with two sources, each with one video and one
// audio track, and both audio tracks routed into the scene.
func mixerFixture(t *testing.T) *appState {
	t.Helper()
	session := core.NewSession("mixer test")
	scene, err := session.AddScene("main")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	app := &appState{
		session:  session,
		scene:    scene.ID,
		settings: defaultSettings(),
		audible:  map[core.TrackID]bool{},
		sync:     newSyncState(),
	}
	app.settings.MuteUnaligned = false
	for i, uri := range []string{"a.m3u8", "b.m3u8"} {
		source, err := session.AddSource(uri)
		if err != nil {
			t.Fatalf("source %d: %v", i, err)
		}
		video, err := session.AddTrack(source.ID, core.MediaVideo, "picture")
		if err != nil {
			t.Fatalf("video %d: %v", i, err)
		}
		sound, err := session.AddTrack(source.ID, core.MediaAudio, "sound")
		if err != nil {
			t.Fatalf("sound %d: %v", i, err)
		}
		if _, err := session.SetSceneAudio(scene.ID, sound.ID, 1, false); err != nil {
			t.Fatalf("route %d: %v", i, err)
		}
		app.items = append(app.items, &item{
			uri:    uri,
			source: source,
			video:  video.ID,
			audio:  sound.ID,
			volume: 1,
		})
	}
	return app
}

// heard reports whether each source would reach the output right now.
func heardList(a *appState) []bool {
	out := make([]bool, len(a.items))
	for i, it := range a.items {
		out[i] = a.heard(it)
	}
	return out
}

func TestTheMixerOverlaysKeepEverySourceOwnState(t *testing.T) {
	a := mixerFixture(t)
	first, second := a.items[0], a.items[1]
	first.volume, second.volume = 0.8, 0.4

	if got := heardList(a); got[0] != true || got[1] != true {
		t.Fatalf("both routed sources start heard, got %v", got)
	}

	if err := a.setVolume(1, 0.4); err != nil {
		t.Fatalf("volume: %v", err)
	}

	a.masterMute = true
	a.apply()
	if got := heardList(a); got[0] || got[1] {
		t.Fatalf("master mute left a source heard: %v", got)
	}
	if !first.muted == false || first.volume != 0.8 {
		t.Fatalf("master mute changed the first source's own state: muted=%v volume=%v", first.muted, first.volume)
	}

	a.masterMute = false
	a.apply()
	if got := heardList(a); !got[0] || !got[1] {
		t.Fatalf("turning master mute off did not bring both sources back: %v", got)
	}

	if _, err := a.toggleMute(0); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if got := heardList(a); got[0] || !got[1] {
		t.Fatalf("a source's own mute did not only silence that source: %v", got)
	}
	if got := a.volumeFor(first.volume); got == 0 {
		t.Fatalf("muting a source changed its volume: %v", got)
	}
}

func TestSoloIsAnOverlayThatEndsWhereItStarted(t *testing.T) {
	a := mixerFixture(t)
	first, second := a.items[0], a.items[1]
	if _, err := a.toggleMute(1); err != nil {
		t.Fatalf("mute the second source: %v", err)
	}
	if _, err := a.toggleSolo(0); err != nil {
		t.Fatalf("solo: %v", err)
	}
	if got := heardList(a); !got[0] || got[1] {
		t.Fatalf("solo did not leave exactly the soloed source heard: %v", got)
	}
	// The muted source is silent for its own reason; unmuting it while another
	// source is soloed must not make it heard, and must not change its state.
	if _, err := a.toggleMute(1); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if second.muted {
		t.Fatal("the second source is still muted by its owner")
	}
	if got := heardList(a); !got[0] || got[1] {
		t.Fatalf("unmuting the other source broke the solo: %v", got)
	}

	// Clicking the same source again leaves solo, and the other source comes
	// back because its own state says so, not because solo remembered anything.
	if _, err := a.toggleSolo(0); err != nil {
		t.Fatalf("solo off: %v", err)
	}
	if a.solo != "" {
		t.Fatalf("solo survived its own toggle: %q", a.solo)
	}
	if got := heardList(a); !got[0] || !got[1] {
		t.Fatalf("after solo the mix is not back to what each source says: %v", got)
	}
	_ = first

	// A second solo switches the target instead of stacking.
	if _, err := a.toggleSolo(0); err != nil {
		t.Fatalf("solo again: %v", err)
	}
	if _, err := a.toggleSolo(1); err != nil {
		t.Fatalf("switch solo: %v", err)
	}
	if a.solo != second.source.ID {
		t.Fatalf("solo did not move to the second source: %q", a.solo)
	}
}
func TestSoloRefusesASourceTheSceneDoesNotRoute(t *testing.T) {
	a := mixerFixture(t)
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	for _, level := range scene.Audio {
		if _, err := a.session.RemoveSceneAudio(a.scene, level.Track); err != nil {
			t.Fatalf("unroute: %v", err)
		}
	}
	if _, err := a.toggleSolo(0); err == nil {
		t.Fatal("soloing a source outside the scene's routing was accepted")
	}
	if a.solo != "" {
		t.Fatalf("a refused solo stayed set: %q", a.solo)
	}
}

func TestSoloRespectsTheUnalignedProtection(t *testing.T) {
	a := mixerFixture(t)
	first := a.items[0]
	a.settings.MuteUnaligned = true
	// The fixture tracks carry no confirmed shift, so both are protected.
	if _, err := a.toggleSolo(0); err != nil {
		t.Fatalf("solo: %v", err)
	}
	if a.heard(first) {
		t.Fatal("solo quietly took the unaligned protection off")
	}
	a.audible[first.audio] = true
	if !a.heard(first) {
		t.Fatal("Allow did not let the soloed source be heard")
	}
}

func TestSoloIsDroppedWhenItsSourceLeaves(t *testing.T) {
	a := mixerFixture(t)
	gone := a.items[1]
	if _, err := a.toggleSolo(1); err != nil {
		t.Fatalf("solo: %v", err)
	}
	a.items = a.items[:1]
	a.dropSoloIfGone()
	if a.solo != "" {
		t.Fatalf("solo outlived its source: %q", a.solo)
	}
	if got := a.heard(a.items[0]); !got {
		t.Fatal("dropping the solo did not bring the remaining source back")
	}
	_ = gone
}

// TestVolumeDoesNotChangeMute pins the rule the mixer UI depends on: moving a
// slider adjusts a level and nothing else.
func TestVolumeDoesNotChangeMute(t *testing.T) {
	a := mixerFixture(t)
	it := a.items[0]
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, 1, true); err != nil {
		t.Fatalf("mute the scene level: %v", err)
	}
	if err := a.setVolume(0, 0.6); err != nil {
		t.Fatalf("volume: %v", err)
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	for _, level := range scene.Audio {
		if level.Track != it.audio {
			continue
		}
		if !level.Muted {
			t.Fatal("changing the volume turned the track back on")
		}
		if level.Volume != 0.6 {
			t.Fatalf("volume = %v, want 0.6", level.Volume)
		}
	}
}

// mutePlayer records what apply() actually tells the player, which is the only
// account of the mixer that reaches the output.
type mutePlayer struct {
	*fakeSeekPlayer
	muted  bool
	volume float64
}

func (p *mutePlayer) SetMuted(muted bool) *ui.VideoPlayer { p.muted = muted; return nil }
func (p *mutePlayer) Muted() bool                         { return p.muted }
func (p *mutePlayer) SetVolume(volume float64) *ui.VideoPlayer {
	p.volume = volume
	return nil
}

// TestApplySilencesTheOtherSourceWhileOneIsSoloed pins the overlay where it
// matters: what the players are actually told.
func TestApplySilencesTheOtherSourceWhileOneIsSoloed(t *testing.T) {
	a := mixerFixture(t)
	players := make([]*mutePlayer, len(a.items))
	for i, it := range a.items {
		players[i] = &mutePlayer{fakeSeekPlayer: &fakeSeekPlayer{}}
		it.player = players[i]
	}
	a.apply()
	if players[0].muted || players[1].muted {
		t.Fatalf("both sources should start heard: %v %v", players[0].muted, players[1].muted)
	}
	if _, err := a.toggleSolo(0); err != nil {
		t.Fatalf("solo: %v", err)
	}
	if players[0].muted {
		t.Fatal("solo muted the soloed source")
	}
	if !players[1].muted {
		t.Fatal("solo left the other source playing")
	}
	if _, err := a.toggleSolo(0); err != nil {
		t.Fatalf("solo off: %v", err)
	}
	if players[1].muted {
		t.Fatal("leaving solo left the other source muted")
	}
}
