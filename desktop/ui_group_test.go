package main

import (
	"errors"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests were grouped from layout_test.go, mixer_test.go, stage_edit_test.go, stage_layout_test.go, timelineview_test.go, viewing_test.go, workspace_memory_test.go.

func closeEnough(got, want float32) bool {
	return math.Abs(float64(got-want)) < 0.01
}

func inStage(r slotRect) bool {
	return r.left >= -0.01 && r.top >= -0.01 &&
		r.left+r.width <= 100.01 && r.top+r.height <= 100.01
}

func TestFocusAlwaysFillsTheStage(t *testing.T) {
	for _, n := range []int{1, 2, 4} {
		slots := layoutSlots(layoutFocus, n)
		if len(slots) != 1 {
			t.Fatalf("focus with %d shown placed %d slots, want 1", n, len(slots))
		}
		if slots[0] != (slotRect{0, 0, 100, 100, 0}) {
			t.Fatalf("focus placed %+v, want the whole stage", slots[0])
		}
	}
	if got := layoutSlots(layoutFocus, 0); got != nil {
		t.Fatalf("focus with nothing shown placed %+v, want nothing", got)
	}
}

func TestSplitGivesEqualColumns(t *testing.T) {
	slots := layoutSlots(layoutSplit, 2)
	if len(slots) != 2 {
		t.Fatalf("split placed %d slots, want 2", len(slots))
	}
	left, right := slots[0], slots[1]
	if !closeEnough(left.height, 100) || !closeEnough(right.height, 100) {
		t.Fatalf("split heights %v and %v, want the full stage", left.height, right.height)
	}
	if !closeEnough(left.width, right.width) {
		t.Fatalf("split widths differ: %v vs %v", left.width, right.width)
	}
	if left.left != 0 {
		t.Fatalf("split starts at %v, want 0", left.left)
	}
	if !closeEnough(left.left+left.width, 50-0.5) || !closeEnough(right.left, 50.5) {
		t.Fatalf("split columns are %+v and %+v", left, right)
	}
	if left.z != 0 || right.z != 0 {
		t.Fatalf("split stacked at %d and %d, want both at the bottom", left.z, right.z)
	}
}

func TestPipFloatsTheSecondPicture(t *testing.T) {
	slots := layoutSlots(layoutPip, 2)
	if len(slots) != 2 {
		t.Fatalf("pip placed %d slots, want 2", len(slots))
	}
	main, small := slots[0], slots[1]
	if main != (slotRect{0, 0, 100, 100, 0}) {
		t.Fatalf("pip main is %+v, want the whole stage below", main)
	}
	if small.z <= main.z {
		t.Fatalf("pip small is stacked at %d, main at %d", small.z, main.z)
	}
	if small.width > 40 || small.height > 40 {
		t.Fatalf("pip small is %vx%v, too big", small.width, small.height)
	}
	if !inStage(small) {
		t.Fatalf("pip small %+v leaves the stage", small)
	}
	if !closeEnough(small.left+small.width, 98) || !closeEnough(small.top+small.height, 98) {
		t.Fatalf("pip small is not in the lower right corner: %+v", small)
	}
	if got := layoutSlots(layoutPip, 1); len(got) != 1 {
		t.Fatalf("pip with one shown placed %d slots, want 1", len(got))
	}
}

func TestGridFillsCellsInReadingOrder(t *testing.T) {
	slots := layoutSlots(layoutGrid, 4)
	if len(slots) != 4 {
		t.Fatalf("grid placed %d slots, want 4", len(slots))
	}
	wantLeft := []float32{0, 50.5, 0, 50.5}
	wantTop := []float32{0, 0, 50.5, 50.5}
	for i, s := range slots {
		if !closeEnough(s.left, wantLeft[i]) || !closeEnough(s.top, wantTop[i]) {
			t.Fatalf("grid slot %d is at %v,%v; want %v,%v", i, s.left, s.top, wantLeft[i], wantTop[i])
		}
		if s.z != 0 {
			t.Fatalf("grid slot %d is stacked at %d, want 0", i, s.z)
		}
		if !inStage(s) {
			t.Fatalf("grid slot %d %+v leaves the stage", i, s)
		}
	}
	if len(layoutSlots(layoutGrid, 2)) != 2 {
		t.Fatal("grid with two sources should place two slots, not repeat them")
	}
	if len(layoutSlots(layoutGrid, 9)) != 4 {
		t.Fatal("grid cannot place more than four pictures")
	}
}

func TestHeroStacksTheSidePictures(t *testing.T) {
	slots := layoutSlots(layoutHero, 3)
	if len(slots) != 3 {
		t.Fatalf("hero placed %d slots, want 3", len(slots))
	}
	hero, a, b := slots[0], slots[1], slots[2]
	if !closeEnough(hero.width, 66) || !closeEnough(hero.height, 100) {
		t.Fatalf("hero is %vx%v, want two thirds of the stage", hero.width, hero.height)
	}
	if hero.z != 0 || a.z != 1 || b.z != 1 {
		t.Fatalf("hero stacking is %d/%d/%d", hero.z, a.z, b.z)
	}
	if !closeEnough(a.left, 67) || !closeEnough(b.left, 67) {
		t.Fatalf("side pictures start at %v and %v, want 67", a.left, b.left)
	}
	if !closeEnough(a.height, b.height) {
		t.Fatalf("side heights differ: %v vs %v", a.height, b.height)
	}
	if a.top != 0 || !closeEnough(b.top, 50.5) {
		t.Fatalf("side pictures sit at %v and %v", a.top, b.top)
	}
	for _, s := range slots {
		if !inStage(s) {
			t.Fatalf("hero slot %+v leaves the stage", s)
		}
	}
	if got := layoutSlots(layoutHero, 1); len(got) != 1 || got[0] != (slotRect{0, 0, 100, 100, 0}) {
		t.Fatalf("hero with one source placed %+v, want a full stage", got)
	}
}

func TestLayoutNamesRoundTrip(t *testing.T) {
	for _, k := range layoutKinds {
		if got := parseLayout(k.String()); got != k {
			t.Fatalf("%q parsed as %v, want %v", k.String(), got, k)
		}
		if k.label() == "" {
			t.Fatalf("%v has no button label", k)
		}
	}
	if got := parseLayout("nonsense"); got != layoutFocus {
		t.Fatalf("an unknown name parsed as %v, want focus", got)
	}
}

func TestSlotPixelsKeepsThePictureShape(t *testing.T) {
	// A half-width slot on a 800x450 stage, for a 16:9 picture: the slot is
	// 400x450, so the picture keeps 400x225 and is centered vertically.
	got := slotPixels(slotRect{left: 50, top: 0, width: 50, height: 100}, 800, 450, 1920, 1080)
	if got.width != 400 || got.height != 225 {
		t.Fatalf("got %vx%v, want 400x225", got.width, got.height)
	}
	if got.left != 400 || got.top != 112.5 {
		t.Fatalf("got at %v,%v, want 400,112.5", got.left, got.top)
	}
	// Without a picture size the slot is used as it is.
	plain := slotPixels(slotRect{left: 0, top: 0, width: 50, height: 50}, 800, 450, 0, 0)
	if plain.width != 400 || plain.height != 225 {
		t.Fatalf("got %vx%v, want the slot 400x225", plain.width, plain.height)
	}
	// A 4:3 picture in a wide slot is limited by height.
	tall := slotPixels(slotRect{left: 0, top: 0, width: 100, height: 100}, 800, 450, 640, 480)
	if tall.width != 600 || tall.height != 450 {
		t.Fatalf("got %vx%v, want 600x450", tall.width, tall.height)
	}
}

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

func TestASourceWhosePlayerDidNotOpenIsNotCalledHeard(t *testing.T) {
	a := mixerFixture(t)
	first := a.items[0]
	first.openErr = errors.New("the player never opened")

	a.apply()
	if a.heard(first) {
		t.Fatalf("a source whose player never opened still reads as heard: %q", a.mixerLine(first))
	}
	if line := a.mixerLine(first); !strings.Contains(line, "its player did not open") {
		t.Fatalf("the row does not say why nothing is audible: %q", line)
	}
	if got := heardList(a); !got[1] {
		t.Fatalf("the other source was silenced too: %v", got)
	}

	// Once the source is retried successfully the reason goes away, and the row
	// is allowed to say it is heard again.
	first.openErr = nil
	a.apply()
	if !a.heard(first) {
		t.Fatalf("a source that opened fine is still called silent: %q", a.mixerLine(first))
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
// TestSceneMuteCannotBeReportedAsHeard pins the row against the scene itself:
// when the scene's own audio level is muted, the player is muted from it, so the
// mixer must not describe that source as heard.
func TestSceneMuteCannotBeReportedAsHeard(t *testing.T) {
	a := mixerFixture(t)
	players := make([]*mutePlayer, len(a.items))
	for i, it := range a.items {
		players[i] = &mutePlayer{fakeSeekPlayer: &fakeSeekPlayer{}}
		it.player = players[i]
	}
	// Another surface can mute the scene's level without touching the item.
	if _, err := a.session.SetSceneAudio(a.scene, a.items[0].audio, 1, true); err != nil {
		t.Fatalf("mute the scene level: %v", err)
	}
	a.apply()

	if players[0].muted != true {
		t.Fatal("the scene's mute did not reach the player")
	}
	if a.heard(a.items[0]) {
		t.Fatal("the mixer calls a source heard while the scene keeps it muted")
	}
	line := a.mixerLine(a.items[0])
	if !strings.Contains(line, "the scene's own mute") {
		t.Fatalf("the row does not say why the source is silent: %q", line)
	}
	if !strings.Contains(line, "silent") {
		t.Fatalf("the row does not read as silent: %q", line)
	}
	// The mute is the scene's; reading it must not clear it.
	level, routed := a.sceneLevel(a.items[0])
	if !routed || !level.Muted {
		t.Fatal("reading the mixer changed the scene's own mute")
	}
	if !a.heard(a.items[1]) || players[1].muted {
		t.Fatal("the other source was affected by the scene's mute")
	}
}

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

// TestVolumeRefusesToRouteASourceTheSceneDoesNotHold pins the difference between
// changing a level and putting a source on the output: a slider must not make a
// source audible as a side effect.
func TestVolumeRefusesToRouteASourceTheSceneDoesNotHold(t *testing.T) {
	a := mixerFixture(t)
	it := a.items[1]
	if _, err := a.session.RemoveSceneAudio(a.scene, it.audio); err != nil {
		t.Fatalf("unroute: %v", err)
	}
	before := it.volume
	err := a.setVolume(1, 0.35)
	if err == nil {
		t.Fatal("changing the level of an unrouted source succeeded")
	}
	if !strings.Contains(err.Error(), "use Hear") {
		t.Fatalf("the refusal does not point at routing: %v", err)
	}
	if it.volume != before {
		t.Fatalf("the refused change still moved the level: %v -> %v", before, it.volume)
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio {
			t.Fatal("the refused change routed the source anyway")
		}
	}
}

// TestVolumeUpdatesTheLevelTheControlsShow keeps the scene, the item and the
// row in step: a slider that keeps drawing 100% while the scene plays 25% is the
// same defect as a wrong value.
func TestVolumeUpdatesTheLevelTheControlsShow(t *testing.T) {
	a := mixerFixture(t)
	it := a.items[0]
	if err := a.setVolume(0, 0.25); err != nil {
		t.Fatalf("volume: %v", err)
	}
	level, routed := a.sceneLevel(it)
	if !routed || level.Volume != 0.25 {
		t.Fatalf("scene level = %v (routed=%v), want 0.25", level.Volume, routed)
	}
	if it.volume != 0.25 {
		t.Fatalf("item volume = %v, want 0.25", it.volume)
	}
	if got := a.shownLevel(it); got != 0.25 {
		t.Fatalf("shown level = %v, want 0.25", got)
	}
	if line := a.mixerLine(it); !strings.Contains(line, "25%") {
		t.Fatalf("the row still shows another level: %q", line)
	}
}

// TestVolumeIsCheckedAgainstItsRange keeps an impossible level out of the scene.
func TestVolumeIsCheckedAgainstItsRange(t *testing.T) {
	a := mixerFixture(t)
	it := a.items[0]
	if err := a.setVolume(0, 1.5); err == nil || !strings.Contains(err.Error(), "outside 0..1") {
		t.Fatalf("out-of-range volume: %v", err)
	}
	if it.volume != 1 {
		t.Fatalf("the refused change moved the level: %v", it.volume)
	}
}

// mutePlayer needs the getter too, so a test can read back the level the
// adapter was handed rather than only the one it stored.
func (p *mutePlayer) Volume() float64 { return p.volume }

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.0005 }

func TestABoxStaysGrabbable(t *testing.T) {
	box := stageBox{X: 0.5, Y: 0.5, W: 0.0001, H: -3}
	got := box.normalized()
	if !got.valid() || got.W < minStageSide || got.H < minStageSide {
		t.Fatalf("a degenerate box was not repaired: %+v", got)
	}
	// A box dragged past the right edge stops at it, keeping every pixel of
	// the picture on the stage.
	pushed := stageBox{X: 0.1, Y: 0.1, W: 0.3, H: 0.3}.movedBy(5, 5)
	if !near(pushed.X, 1-pushed.W) || !near(pushed.Y, 1-pushed.H) {
		t.Fatalf("dragging off the stage left %+v, want the inside clamp", pushed)
	}
	// And one pushed off the left stops at the left edge.
	pushed = stageBox{X: 0.1, Y: 0.1, W: 0.3, H: 0.3}.movedBy(-5, -5)
	if !near(pushed.X, 0) || !near(pushed.Y, 0) {
		t.Fatalf("dragging past the top left left %+v", pushed)
	}
}

func TestMovingABoxNeverChangesItsSize(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.3, W: 0.4, H: 0.25}
	for _, delta := range [][2]float32{{0.1, 0}, {-0.1, 0}, {0, 0.05}, {0, -0.05}} {
		got := box.movedBy(delta[0], delta[1])
		if !near(got.W, box.W) || !near(got.H, box.H) {
			t.Fatalf("moving by %v changed the size: %+v", delta, got)
		}
	}
	if got := box.movedBy(float32(math.NaN()), 0); got != box {
		t.Fatalf("a NaN delta moved the box: %+v", got)
	}
}

func TestAResizeDragsTheEdgeItWasGrabbedBy(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.4}
	// The east handle moves the right edge; the left one stays put.
	east, ok := box.resizedBy(handleE, 0.1, 0, 0, false)
	if !ok || !near(east.X, 0.2) || !near(east.W, 0.5) || !near(east.H, 0.4) {
		t.Fatalf("east resize gave %+v ok=%v", east, ok)
	}
	// The west handle moves the left edge; the right one stays put.
	west, ok := box.resizedBy(handleW, -0.1, 0, 0, false)
	if !ok || !near(west.X, 0.1) || !near(west.W, 0.5) {
		t.Fatalf("west resize gave %+v ok=%v", west, ok)
	}
	// The north handle moves the top edge; the bottom stays.
	north, ok := box.resizedBy(handleN, 0, -0.1, 0, false)
	if !ok || !near(north.Y, 0.1) || !near(north.H, 0.5) {
		t.Fatalf("north resize gave %+v ok=%v", north, ok)
	}
	// A corner moves both of its edges and keeps the opposite corner fixed.
	se, ok := box.resizedBy(handleSE, 0.1, 0.1, 0, false)
	if !ok || !near(se.X, 0.2) || !near(se.Y, 0.2) || !near(se.W, 0.5) || !near(se.H, 0.5) {
		t.Fatalf("south east resize gave %+v ok=%v", se, ok)
	}
}

func TestRatioLockedResizeKeepsThePictureShape(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.2} // 2:1
	aspect := float32(2)
	// A side drag changes both sides: width follows height at the same ratio.
	got, ok := box.resizedBy(handleE, 0.1, 0, aspect, true)
	if !ok || !near(got.W/got.H, aspect) {
		t.Fatalf("east ratio resize gave %+v ok=%v", got, ok)
	}
	// A corner keeps its opposite corner where it was.
	corner, ok := box.resizedBy(handleNW, -0.05, -0.05, aspect, true)
	if !ok || !near(corner.W/corner.H, aspect) ||
		!near(corner.X+corner.W, box.X+box.W) || !near(corner.Y+corner.H, box.Y+box.H) {
		t.Fatalf("corner ratio resize gave %+v ok=%v", corner, ok)
	}
}

func TestAResizeRefusesToShrinkOrFlip(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.4}
	if _, ok := box.resizedBy(handleE, -0.4, 0, 0, false); ok {
		t.Fatal("a resize under the minimum side was accepted")
	}
	if _, ok := box.resizedBy(handleE, -0.45, 0, 0, false); ok {
		t.Fatal("a resize that turned the box inside out was accepted")
	}
	if _, ok := box.resizedBy(handleNone, 0.1, 0.1, 0, false); ok {
		t.Fatal("a resize without a handle was accepted")
	}
	if _, ok := box.resizedBy(handleSE, float32(math.Inf(1)), 0, 0, false); ok {
		t.Fatal("an infinite delta was accepted")
	}
}

func TestHandleAtNamesTheCornerThePressAimedAt(t *testing.T) {
	box := stageBox{X: 0.2, Y: 0.2, W: 0.4, H: 0.4}
	cases := map[handleKind][2]float32{
		handleNW: {0.2, 0.2},
		handleNE: {0.6, 0.2},
		handleSE: {0.6, 0.6},
		handleSW: {0.2, 0.6},
		handleN:  {0.4, 0.2},
		handleS:  {0.4, 0.6},
		handleW:  {0.2, 0.4},
		handleE:  {0.6, 0.4},
	}
	for want, point := range cases {
		if got := box.handleAt(point[0], point[1]); got != want {
			t.Fatalf("press at %v named %v, want %v", point, got, want)
		}
	}
	if got := box.handleAt(0.4, 0.4); got != handleNone {
		t.Fatalf("a press in the middle named a handle: %v", got)
	}
	if got := box.handleAt(0.9, 0.9); got != handleNone {
		t.Fatalf("a press far away named a handle: %v", got)
	}
}

func TestTheSceneKnowsItsOwnRectangles(t *testing.T) {
	edits := newStageEdits()
	fallback := stageBox{X: 0, Y: 0, W: 0.5, H: 1}
	if got, ok := edits.boxFor("a", fallback); !ok || got != fallback {
		t.Fatalf("a source without a rectangle did not get the layout slot: %+v", got)
	}
	edits.setBox("a", stageBox{X: 0.1, Y: 0.2, W: 0.3, H: 0.4})
	if got, _ := edits.boxFor("a", fallback); !near(got.X, 0.1) || !near(got.H, 0.4) {
		t.Fatalf("the stored rectangle was not used: %+v", got)
	}
	edits.forget("a")
	if got, _ := edits.boxFor("a", fallback); got != fallback {
		t.Fatalf("forgetting a rectangle did not fall back to the layout: %+v", got)
	}
	if _, ok := edits.boxFor("b", stageBox{}); ok {
		t.Fatal("a source with neither an edit nor a slot was placed")
	}
}

func TestStackingOrderMovesOneStepAndToTheEnds(t *testing.T) {
	edits := newStageEdits()
	sources := []core.SourceID{"a", "b", "c"}
	got := edits.ordered(sources)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("ordered without edits gave %v", got)
	}
	edits.raise("a", sources, false)
	if got := edits.ordered(sources); got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("a step forward put %v", got)
	}
	edits.raise("a", sources, true)
	if got := edits.ordered(sources); got[len(got)-1] != "a" {
		t.Fatalf("to front put %v", got)
	}
	edits.lower("a", sources, true)
	if got := edits.ordered(sources); got[0] != "a" {
		t.Fatalf("to back put %v", got)
	}
	// The back-most source has nowhere further back to go.
	edits.lower("a", sources, false)
	if got := edits.ordered(sources); got[0] != "a" {
		t.Fatalf("a step back from the back moved it: %v", got)
	}
	edits.raise("c", sources, false)
	if got := edits.ordered(sources); got[2] != "c" || got[1] != "b" {
		t.Fatalf("a step forward put %v", got)
	}
	got = edits.ordered([]core.SourceID{"c"})
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("a source that is no longer shown leaked into the order: %v", got)
	}
}

// A picture that fits on the stage is kept wholly inside it: dragging a
// full-size picture does nothing, and a smaller one stops at the edge instead
// of sliding under the bars around the stage.
func TestABoxThatFitsStaysInsideTheStage(t *testing.T) {
	full := stageBox{X: 0, Y: 0, W: 1, H: 1}
	if moved := full.movedBy(0.4, 0.4); !near(moved.X, 0) || !near(moved.Y, 0) {
		t.Fatalf("a full-size picture moved to %.3f,%.3f", moved.X, moved.Y)
	}
	small := stageBox{X: 0.1, Y: 0.1, W: 0.4, H: 0.4}
	if moved := small.movedBy(0.9, 0.9); !near(moved.X, 0.6) || !near(moved.Y, 0.6) {
		t.Fatalf("a picture stopped at %.3f,%.3f, want 0.6,0.6", moved.X, moved.Y)
	}
	if moved := small.movedBy(-9, -9); !near(moved.X, 0) || !near(moved.Y, 0) {
		t.Fatalf("a picture stopped at %.3f,%.3f, want 0,0", moved.X, moved.Y)
	}
	// Growing by a handle cannot push the box out of the stage either.
	fitted := stageBox{X: 0, Y: 0, W: 1, H: 1}
	if out, ok := fitted.resizedBy(handleNW, 0.3, 0.3, 1, false); ok && (out.X < -0.0001 || out.Y < -0.0001 || out.X+out.W > 1.0001 || out.Y+out.H > 1.0001) {
		t.Fatalf("resize left the stage: %.3f,%.3f %.3fx%.3f", out.X, out.Y, out.W, out.H)
	}
}

func TestEveryLayoutKeepsItsPicturesApart(t *testing.T) {
	for _, kind := range layoutKinds {
		for n := 1; n <= kind.slots(); n++ {
			rects := layoutSlots(kind, n)
			if len(rects) != n {
				t.Fatalf("%v with %d sources placed %d rects", kind, n, len(rects))
			}
			for i, r := range rects {
				if r.width <= 0 || r.height <= 0 {
					t.Fatalf("%v with %d sources: slot %d has no area: %+v", kind, n, i, r)
				}
				if !inStage(r) {
					t.Fatalf("%v with %d sources: slot %d leaves the stage: %+v", kind, n, i, r)
				}
				for j := 0; j < i; j++ {
					// Overlap is only allowed when the later slot is painted
					// above the earlier one: picture in picture floats over the
					// main picture instead of hiding under it.
					if overlap(r, rects[j]) && r.z <= rects[j].z {
						t.Fatalf("%v with %d sources: slot %d %+v overlaps slot %d %+v without being stacked above it", kind, n, i, r, j, rects[j])
					}
				}
			}
		}
	}
}

func overlap(a, b slotRect) bool {
	return a.left < b.left+b.width && b.left < a.left+a.width &&
		a.top < b.top+b.height && b.top < a.top+a.height
}

func timelineViewApp(t *testing.T, tracks ...core.TrackID) *appState {
	t.Helper()
	app := &appState{sync: newSyncState()}
	for index, track := range tracks {
		app.items = append(app.items, &item{
			source: core.Source{ID: core.SourceID("source-" + string(rune('1'+index)))},
			uri:    "example" + string(rune('1'+index)) + ".m3u8",
			video:  track,
		})
	}
	return app
}

// An unaligned track is not a track calibrated to zero: the two must not read
// the same in the view.
func TestAnUnalignedRowSaysUnknownRatherThanZero(t *testing.T) {
	app := timelineViewApp(t, "track-1")
	lines := app.timelineLines()
	if len(lines) != 1 {
		t.Fatalf("want one line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "not aligned") || !strings.Contains(lines[0], "media unknown") {
		t.Fatalf("an unaligned row does not say it is unknown: %q", lines[0])
	}
	if strings.Contains(lines[0], "+0.0s") || strings.Contains(lines[0], "offset") {
		t.Fatalf("an unaligned row claims an offset: %q", lines[0])
	}
}

// A real zero offset is a calibration and reads as one.
func TestAnAlignedZeroRowShowsZero(t *testing.T) {
	app := timelineViewApp(t, "track-1")
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatal(err)
	}
	app.sync.place("track-1", dvr.BasisManual, "buf-1", 1)
	line := app.timelineLines()[0]
	if !strings.Contains(line, "offset +0.0s") || !strings.Contains(line, "manual") {
		t.Fatalf("an aligned zero row does not read as one: %q", line)
	}
	if strings.Contains(line, "not aligned") {
		t.Fatalf("an aligned row still reads as unknown: %q", line)
	}
}

// The mapped media time is the timeline's answer for the shared event time, in
// the sign the rest of the app uses.
func TestTheRowSeparatesEventMediaAndPlayerPosition(t *testing.T) {
	app := timelineViewApp(t, "track-1")
	if err := app.sync.timeline.SetShift("track-1", -1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := app.sync.playhead.Seek(6 * time.Second); err != nil {
		t.Fatal(err)
	}
	line := app.timelineLines()[0]
	for _, want := range []string{"offset -1.5s", "media 4.5s", "not open"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the row is missing %q: %q", want, line)
		}
	}
}

// Reading the view must not move anything: refreshes and re-renders happen on
// every frame.
func TestReadingTheViewChangesNothing(t *testing.T) {
	app := timelineViewApp(t, "track-1", "track-2")
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := app.sync.playhead.Seek(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	before := app.sync.timeline.Shifts()
	app.timelineLines()
	app.timelineLines()
	after := app.sync.timeline.Shifts()
	if len(before) != len(after) {
		t.Fatalf("the view changed the number of shifts: %d -> %d", len(before), len(after))
	}
	for index := range before {
		if before[index] != after[index] {
			t.Fatalf("the view changed shift %d: %+v -> %+v", index, before[index], after[index])
		}
	}
	if event := app.sync.playhead.Event(); event != 3*time.Second {
		t.Fatalf("the view moved the playhead to %s", event)
	}
	if _, ok := app.sync.timeline.Shift("track-2"); ok {
		t.Fatalf("the view aligned a track nobody aligned")
	}
}

// The reference is the sync group's reference track, not whichever source
// happens to be first.
func TestTheReferenceComesFromTheSyncGroup(t *testing.T) {
	session := core.NewSession("view")
	first, err := session.AddSource("a.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.AddSource("b.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	firstTrack, err := session.AddTrack(first.ID, core.MediaVideo, "first")
	if err != nil {
		t.Fatal(err)
	}
	secondTrack, err := session.AddTrack(second.ID, core.MediaVideo, "second")
	if err != nil {
		t.Fatal(err)
	}
	// The group's reference is the second source's track: being first in the
	// window does not make a source the reference.
	group, err := session.AddSyncGroup(secondTrack.ID, secondTrack.ID, firstTrack.ID)
	if err != nil {
		t.Fatal(err)
	}
	app := &appState{session: session, sync: newSyncState()}
	app.sync.group = group.ID
	app.items = []*item{
		{source: first, uri: "a.m3u8", video: firstTrack.ID},
		{source: second, uri: "b.m3u8", video: secondTrack.ID},
	}
	if got := app.referenceTrack(); got != secondTrack.ID {
		t.Fatalf("the reference is %q, want %q", got, secondTrack.ID)
	}
	lines := app.timelineLines()
	// The second source carries the reference track, even though it is the
	// second line in the window.
	if !strings.Contains(lines[1], "track-2 ref") {
		t.Fatalf("the reference track is not marked: %q", lines[1])
	}
	if strings.Contains(lines[0], "ref") {
		t.Fatalf("a non-reference track is marked as one: %q", lines[0])
	}
}

// Core lets an audio track be the sync group's reference. A row that always
// prefers the video track would then hide the very track the group aligns to.
func TestAnAudioReferenceLabelsItsOwnRow(t *testing.T) {
	session := core.NewSession("view")
	// One source carries both a picture and a sound, and the group aligns to
	// its sound: a row that always prefers the picture would hide that.
	mixedSource, err := session.AddSource("a.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	picture, err := session.AddTrack(mixedSource.ID, core.MediaVideo, "picture")
	if err != nil {
		t.Fatal(err)
	}
	sound, err := session.AddTrack(mixedSource.ID, core.MediaAudio, "sound")
	if err != nil {
		t.Fatal(err)
	}
	otherSource, err := session.AddSource("b.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	other, err := session.AddTrack(otherSource.ID, core.MediaVideo, "other")
	if err != nil {
		t.Fatal(err)
	}
	group, err := session.AddSyncGroup(sound.ID, picture.ID, sound.ID, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	app := &appState{session: session, sync: newSyncState()}
	app.sync.group = group.ID
	app.items = []*item{
		{source: mixedSource, uri: "a.m3u8", video: picture.ID, audio: sound.ID},
		{source: otherSource, uri: "b.m3u8", video: other.ID},
	}
	rows := app.timelineRows()
	if len(rows) != 2 {
		t.Fatalf("want two rows, got %d", len(rows))
	}
	if rows[0].Track != sound.ID || !rows[0].Reference {
		t.Fatalf("a source whose sound is the reference describes its picture instead: %+v", rows[0])
	}
	if rows[1].Track != other.ID || rows[1].Reference {
		t.Fatalf("the other source picked up the reference: %+v", rows[1])
	}
}

type fakeWindow struct {
	bounds     mygo.Rectangle
	maximized  bool
	fullscreen bool
	onTop      bool

	boundsSet     int
	maximize      int
	unmaximize    int
	fullscreenSet []bool
	onTopSet      []bool
}

func (f *fakeWindow) Bounds() mygo.Rectangle { return f.bounds }
func (f *fakeWindow) SetBounds(r mygo.Rectangle) {
	f.bounds = r
	f.boundsSet++
}
func (f *fakeWindow) IsMaximized() bool { return f.maximized }
func (f *fakeWindow) Maximize()         { f.maximized = true; f.maximize++ }
func (f *fakeWindow) Unmaximize()       { f.maximized = false; f.unmaximize++ }
func (f *fakeWindow) SetFullScreen(v bool) {
	f.fullscreen = v
	f.fullscreenSet = append(f.fullscreenSet, v)
}
func (f *fakeWindow) IsFullScreen() bool { return f.fullscreen }
func (f *fakeWindow) SetAlwaysOnTop(v bool) {
	f.onTop = v
	f.onTopSet = append(f.onTopSet, v)
}

func newViewingAppState() *appState {
	return &appState{
		workspace: workspaceStage,
		rail:      paneAuto,
		inspector: paneAuto,
		dock:      paneAuto,
	}
}

// TestCinemaKeepsTheWorkbenchAndComesBack is W01 at the state level: entering
// cinema changes no window geometry and leaving restores the editing state the
// person left, including the page and the pane switches.
func TestCinemaKeepsTheWorkbenchAndComesBack(t *testing.T) {
	a := newViewingAppState()
	a.workspace = workspaceSources
	a.rail = paneOpen
	a.inspector = paneClosed
	workbench := mygo.Rectangle{X: 100, Y: 80, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCinema, w)
	if a.viewing != viewCinema {
		t.Fatalf("viewing = %v, want cinema", a.viewing)
	}
	if f := w.fullscreenSet; len(f) != 0 {
		t.Fatalf("cinema touched full screen: %v", f)
	}
	if w.boundsSet != 0 {
		t.Fatalf("cinema moved the window %d time(s)", w.boundsSet)
	}
	if !a.savedForViewing.saved || a.savedForViewing.bounds != workbench {
		t.Fatalf("saved = %+v, want the workbench rectangle", a.savedForViewing)
	}

	a.leaveViewing(w)
	if a.viewing != viewWorkbench {
		t.Fatalf("viewing = %v after leaving, want workbench", a.viewing)
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v after leaving, want %+v", w.bounds, workbench)
	}
	if a.workspace != workspaceSources || a.rail != paneOpen || a.inspector != paneClosed {
		t.Fatalf("editing state not restored: page=%v rail=%v inspector=%v",
			a.workspace, a.rail, a.inspector)
	}
}

// TestEnteringAnotherWatchingModeKeepsTheWorkbench is the reason the save is
// guarded: cinema -> full screen -> back must return to the workbench, not to
// the geometry full screen happened to have.
func TestEnteringAnotherWatchingModeKeepsTheWorkbench(t *testing.T) {
	a := newViewingAppState()
	workbench := mygo.Rectangle{X: 20, Y: 20, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCinema, w)
	w.bounds = mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080} // someone moved it
	a.enterViewing(viewFullscreen, w)
	if !w.fullscreen {
		t.Fatal("full screen was not asked for")
	}
	if a.savedForViewing.bounds != workbench {
		t.Fatalf("saved bounds = %+v, want the workbench %+v", a.savedForViewing.bounds, workbench)
	}

	a.leaveViewing(w)
	if w.fullscreen {
		t.Fatal("leaving full screen left the window in full screen")
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v after leaving, want %+v", w.bounds, workbench)
	}
}

// TestFullscreenComesBackToTheModeItCameFrom is W04: a full screen entered
// from cinema returns to cinema, not to the workbench, and the workbench
// rectangle saved before cinema is still the one restored at the end.
func TestFullscreenComesBackToTheModeItCameFrom(t *testing.T) {
	a := newViewingAppState()
	workbench := mygo.Rectangle{X: 30, Y: 40, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCinema, w)
	a.toggleFullscreen(w)
	if a.viewing != viewFullscreen || !w.fullscreen {
		t.Fatalf("viewing = %v fullscreen = %v, want a full screen", a.viewing, w.fullscreen)
	}
	a.toggleFullscreen(w)
	if a.viewing != viewCinema {
		t.Fatalf("viewing = %v after leaving full screen, want cinema", a.viewing)
	}
	if w.fullscreen {
		t.Fatal("leaving full screen left the window in full screen")
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v, want the cinema %+v", w.bounds, workbench)
	}

	a.leaveViewing(w)
	if a.viewing != viewWorkbench || w.bounds != workbench {
		t.Fatalf("viewing = %v bounds = %+v, want the workbench %+v", a.viewing, w.bounds, workbench)
	}
}

// TestFullscreenFromTheWorkbenchEndsTheWatchingMode: entered from the
// workbench, the way back is the workbench, with its panels.
func TestFullscreenFromTheWorkbenchEndsTheWatchingMode(t *testing.T) {
	a := newViewingAppState()
	a.workspace = workspaceMixer
	w := &fakeWindow{bounds: mygo.Rectangle{X: 1, Y: 2, Width: 900, Height: 700}}

	a.toggleFullscreen(w)
	a.toggleFullscreen(w)
	if a.viewing != viewWorkbench {
		t.Fatalf("viewing = %v, want workbench", a.viewing)
	}
	if a.workspace != workspaceMixer {
		t.Fatalf("workspace = %v, want the page the person was on", a.workspace)
	}
}

// TestCompactFromFullscreenKeepsTheSmallWindow: leaving a full screen that was
// entered from compact must give the small window back, not a maximised one.
func TestCompactFromFullscreenKeepsTheSmallWindow(t *testing.T) {
	a := newViewingAppState()
	w := &fakeWindow{bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800}}

	a.enterViewing(viewCompact, w)
	compact := w.bounds
	a.toggleFullscreen(w)
	a.toggleFullscreen(w)
	if a.viewing != viewCompact {
		t.Fatalf("viewing = %v, want compact", a.viewing)
	}
	if w.bounds != compact {
		t.Fatalf("bounds = %+v, want the compact %+v", w.bounds, compact)
	}
}

// TestCompactShrinksTheWindowAndRestoresIt covers W03: compact opens a smaller
// window, offers on-top, and undoes both when it ends.
func TestCompactShrinksTheWindowAndRestoresIt(t *testing.T) {
	a := newViewingAppState()
	workbench := mygo.Rectangle{X: 200, Y: 120, Width: 1150, Height: 660}
	w := &fakeWindow{bounds: workbench}

	a.enterViewing(viewCompact, w)
	if w.bounds.Width >= workbench.Width || w.bounds.Height >= workbench.Height {
		t.Fatalf("compact bounds = %+v, want smaller than %+v", w.bounds, workbench)
	}
	if w.bounds.Width < 420 || w.bounds.Height < 236 {
		t.Fatalf("compact bounds = %+v, below the readable floor", w.bounds)
	}

	a.toggleOnTop(w)
	if !w.onTop || !a.onTop {
		t.Fatal("on-top did not switch on")
	}
	a.leaveViewing(w)
	if w.onTop {
		t.Fatal("leaving compact left the window on top")
	}
	if w.bounds != workbench {
		t.Fatalf("bounds = %+v after leaving, want %+v", w.bounds, workbench)
	}
}

// TestCompactFromMaximizedRestoresMaximized is the geometry trap: setting a
// rectangle on a maximized window does nothing, so entering has to unmaximize
// first and leaving has to maximize again rather than restore a rectangle the
// maximized window never really had.
func TestCompactFromMaximizedRestoresMaximized(t *testing.T) {
	a := newViewingAppState()
	w := &fakeWindow{bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}, maximized: true}

	a.enterViewing(viewCompact, w)
	if w.maximized {
		t.Fatal("compact stayed maximized, so its small bounds could not take effect")
	}
	if w.bounds.Width >= 1920 {
		t.Fatalf("compact bounds = %+v, want a small window", w.bounds)
	}

	a.leaveViewing(w)
	if !w.maximized {
		t.Fatal("leaving did not restore the maximized window")
	}
}

// TestOverlayHidesAfterIdleAndStaysUnderThePointer is W05's rule in the small:
// a pointer move wakes the overlay, a still pointer lets it go, and a pointer
// resting on the bar itself keeps it.
func TestOverlayHidesAfterIdleAndStaysUnderThePointer(t *testing.T) {
	a := newViewingAppState()
	a.viewing = viewCinema

	if !a.notePointer(10, 10) {
		t.Fatal("the first pointer position was not treated as a move")
	}
	if !a.controlsShown {
		t.Fatal("the overlay did not show on a pointer move")
	}
	tick := a.idleTick
	if a.notePointer(11, 11) {
		t.Fatal("a one-pixel wobble counted as a move")
	}
	if a.idleTick != tick {
		t.Fatal("a still pointer armed another hide timer")
	}

	a.hideControls(tick - 1)
	if !a.controlsShown {
		t.Fatal("an older timer hid the overlay")
	}
	a.controlsHovered = false
	a.hideControls(tick)
	if a.controlsShown {
		t.Fatal("the overlay stayed after idle")
	}

	a.controlsShown = true
	a.controlsHovered = true
	a.hideControls(a.idleTick)
	if !a.controlsShown {
		t.Fatal("the overlay hid itself under the pointer")
	}
}

// TestLeavingViewingRestoresTheOnTopState proves the switch never outlives the
// mode that offered it: a window that was on top before compact stays on top,
// and one that was not goes back to not being on top.
func TestLeavingViewingRestoresTheOnTopState(t *testing.T) {
	for _, before := range []bool{false, true} {
		a := newViewingAppState()
		a.onTop = before
		w := &fakeWindow{bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1150, Height: 660}, onTop: before}

		a.enterViewing(viewCompact, w)
		a.toggleOnTop(w)
		a.leaveViewing(w)
		if w.onTop != before {
			t.Fatalf("onTop before=%v, after leaving=%v", before, w.onTop)
		}

		// The saved rectangle must survive a long stay in cinema, which is what
		// the states look like after several mode changes.
		a = newViewingAppState()
		w = &fakeWindow{bounds: mygo.Rectangle{X: 7, Y: 8, Width: 900, Height: 600}}
		a.enterViewing(viewCinema, w)
		time.Sleep(time.Millisecond)
		a.enterViewing(viewCompact, w)
		a.enterViewing(viewFullscreen, w)
		a.leaveViewing(w)
		if w.bounds != (mygo.Rectangle{X: 7, Y: 8, Width: 900, Height: 600}) {
			t.Fatalf("bounds = %+v after three mode changes", w.bounds)
		}
	}
}

// TestCompactSizeStaysSmall keeps the "small window" small whatever it was
// asked to shrink from: two fifths of a maximized screen is not compact.
func TestCompactSizeStaysSmall(t *testing.T) {
	for _, saved := range []mygo.Rectangle{
		{X: 0, Y: 0, Width: 2560, Height: 1540},
		{X: 60, Y: 60, Width: 1150, Height: 660},
		{X: 0, Y: 0, Width: 300, Height: 200},
	} {
		got := compactSize(saved)
		if got.Width < 420 || got.Width > 720 {
			t.Errorf("from %dx%d the compact width is %d, want 420..720", saved.Width, saved.Height, got.Width)
		}
		if got.Height < 236 || got.Height > 460 {
			t.Errorf("from %dx%d the compact height is %d, want 236..460", saved.Width, saved.Height, got.Height)
		}
		if got.X != saved.X+40 || got.Y != saved.Y+40 {
			t.Errorf("from %d,%d the compact window starts at %d,%d", saved.X, saved.Y, got.X, got.Y)
		}
	}
}

func TestValidateRejectsARememberedShapeItCannotHonour(t *testing.T) {
	base := func() workspaceMemory {
		return workspaceMemory{
			On: true, Page: "sources", X: 100, Y: 80, Width: 1180, Height: 840,
			Rail: "auto", Inspector: "auto", Dock: "auto",
		}
	}
	if err := validateWorkspaceMemory(base()); err != nil {
		t.Fatalf("a plain remembered shape does not validate: %v", err)
	}
	cases := []struct {
		name  string
		edit  func(*workspaceMemory)
		wants string
	}{
		{"page", func(w *workspaceMemory) { w.Page = "dvr" }, "workspace page"},
		{"rail", func(w *workspaceMemory) { w.Rail = "sometimes" }, "workspace rail"},
		{"dock", func(w *workspaceMemory) { w.Dock = "half" }, "workspace dock"},
		{"too small", func(w *workspaceMemory) { w.Width = 200 }, "workspace width"},
		{"too tall", func(w *workspaceMemory) { w.Height = 40000 }, "workspace height"},
		{"nowhere", func(w *workspaceMemory) { w.X = 50000 }, "workspace position"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := base()
			tc.edit(&w)
			err := validateWorkspaceMemory(w)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error %q does not mention %q", err, tc.wants)
			}
		})
	}
	off := base()
	off.On = false
	off.Page = "not-a-page"
	if err := validateWorkspaceMemory(off); err != nil {
		t.Fatalf("a memory that is off still validated its unused fields: %v", err)
	}
}

// TestPaneModeNamesRoundTrip pins the stored spellings: a name is part of the
// file format, so renaming one silently would make an old file meaningless.
func TestPaneModeNamesRoundTrip(t *testing.T) {
	for _, mode := range []paneMode{paneAuto, paneOpen, paneClosed} {
		name := paneModeName(mode)
		back, ok := parsePaneMode(name)
		if !ok || back != mode {
			t.Fatalf("%v named %q came back as %v (%v)", mode, name, back, ok)
		}
	}
	if _, ok := parsePaneMode("OPEN"); ok {
		t.Fatal("a pane name is case-sensitive and OPEN was accepted")
	}
}

// TestClampWorkspaceKeepsTheFrameReachable is the rule for a display that went
// away: keep the size when it fits, and bring the title bar back to a place a
// person can grab.
func TestClampWorkspaceKeepsTheFrameReachable(t *testing.T) {
	displays := []mygo.Display{
		{Primary: true, WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}},
		{WorkArea: mygo.Rectangle{X: 1920, Y: 0, Width: 1280, Height: 1024}},
	}
	onScreen := mygo.Rectangle{X: 100, Y: 80, Width: 1180, Height: 840}
	if got := clampWorkspace(onScreen, displays); got != onScreen {
		t.Fatalf("an on-screen frame moved: %+v", got)
	}
	onSecond := mygo.Rectangle{X: 2000, Y: 100, Width: 900, Height: 700}
	if got := clampWorkspace(onSecond, displays); got != onSecond {
		t.Fatalf("a frame on the second display moved: %+v", got)
	}
	// Just the title strip has to be reachable, so a window hanging off the
	// right edge is still fine.
	hanging := mygo.Rectangle{X: 1900, Y: 10, Width: 600, Height: 400}
	if got := clampWorkspace(hanging, displays); got != hanging {
		t.Fatalf("a deliberately overhanging frame moved: %+v", got)
	}
	gone := mygo.Rectangle{X: -4000, Y: -3000, Width: 1180, Height: 840}
	got := clampWorkspace(gone, displays)
	if got.Width != 1180 || got.Height != 840 {
		t.Fatalf("the size changed while only the position was unreachable: %+v", got)
	}
	if !intersects(mygo.Rectangle{X: got.X, Y: got.Y, Width: got.Width, Height: 40}, displays[0].WorkArea) {
		t.Fatalf("the frame was not brought back to the primary display: %+v", got)
	}
	huge := mygo.Rectangle{X: 0, Y: 0, Width: 3000, Height: 1400}
	got = clampWorkspace(huge, displays)
	if got.Width != 1920 || got.Height != 1040 {
		t.Fatalf("a frame larger than the screen was not shrunk to it: %+v", got)
	}
}

// TestWorkspaceMemorySurvivesTheSettingsFile is the whole point of the feature:
// what one start writes, the next start reads back as the same shape.
func TestWorkspaceMemorySurvivesTheSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := defaultSettings()
	want.Workspace = workspaceMemory{
		On: true, Page: "mixer", X: 220, Y: 140, Width: 1180, Height: 840,
		Rail: "closed", Inspector: "auto", Dock: "open",
	}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, note := loadSettings(path)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if loaded.Workspace != want.Workspace {
		t.Fatalf("workspace came back as %+v, want %+v", loaded.Workspace, want.Workspace)
	}
	// A file without the block is still a valid file: the memory is off.
	plain := defaultSettings()
	if err := saveSettings(path, plain); err != nil {
		t.Fatalf("save without the block: %v", err)
	}
	loaded, note = loadSettings(path)
	if note != "" || loaded.Workspace.On {
		t.Fatalf("a file without the block loaded as %+v (%q)", loaded.Workspace, note)
	}
}
