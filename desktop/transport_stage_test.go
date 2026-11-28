package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

type stageTransportPlayer struct {
	playing bool
	plays   int
	pauses  int
}

func (p *stageTransportPlayer) Ready() bool             { return true }
func (p *stageTransportPlayer) Playing() bool           { return p.playing }
func (p *stageTransportPlayer) Buffering() bool         { return false }
func (p *stageTransportPlayer) Position() time.Duration { return 0 }
func (p *stageTransportPlayer) Duration() time.Duration { return 0 }
func (p *stageTransportPlayer) Volume() float64         { return 1 }
func (p *stageTransportPlayer) Muted() bool             { return false }
func (p *stageTransportPlayer) Err() error              { return nil }
func (p *stageTransportPlayer) Play()                   { p.plays++; p.playing = true }
func (p *stageTransportPlayer) Pause()                  { p.pauses++; p.playing = false }
func (p *stageTransportPlayer) Seek(time.Duration)      {}
func (p *stageTransportPlayer) SetVolume(float64) *ui.VideoPlayer {
	return nil
}
func (p *stageTransportPlayer) SetMuted(bool) *ui.VideoPlayer { return nil }
func (p *stageTransportPlayer) Close()                        {}

func TestStageTransportUsesStageMembersAndConditionallyAddsSyncGroup(t *testing.T) {
	session := core.NewSession("desktop")
	scene, err := session.AddScene("")
	if err != nil {
		t.Fatal(err)
	}
	app := &appState{session: session, scene: scene.ID, sync: newSyncState()}
	players := make([]*stageTransportPlayer, 3)
	for i, name := range []string{"stage-group", "stage-independent", "off-stage-group"} {
		source, err := session.AddSource("https://example.test/" + name + ".m3u8")
		if err != nil {
			t.Fatal(err)
		}
		video, err := session.AddTrack(source.ID, core.MediaVideo, name)
		if err != nil {
			t.Fatal(err)
		}
		audio, err := session.AddTrack(source.ID, core.MediaAudio, name)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := session.Source(source.ID)
		if err != nil {
			t.Fatal(err)
		}
		players[i] = &stageTransportPlayer{}
		app.items = append(app.items, &item{
			persistentID: name,
			source:       stored,
			video:        video.ID,
			audio:        audio.ID,
			player:       players[i],
		})
	}
	if _, err := session.AddSyncGroup(app.items[0].video, app.items[0].video, app.items[0].audio, app.items[2].video, app.items[2].audio); err != nil {
		t.Fatal(err)
	}
	for _, group := range session.SyncGroups() {
		app.sync.group = group.ID
	}
	app.sceneMembers = map[core.SceneID]map[string]bool{
		scene.ID: {"stage-group": true, "stage-independent": true},
	}

	plan, err := app.stageTransportPlan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.syncGroup || len(plan.items) != 3 || plan.items[0] != app.items[0] || plan.items[1] != app.items[1] || plan.items[2] != app.items[2] {
		t.Fatalf("stage plan = %+v, want both stage members plus the entire sync group", plan)
	}
	app.sync.playhead.Play()
	if !app.playing() {
		t.Fatal("the active sync clock was not reflected while group players were buffering")
	}
	app.sync.playhead.Pause()
	players[1].playing = true
	if !app.playing() {
		t.Fatal("an independent playing target was hidden by the paused sync clock")
	}
	players[1].playing = false
	if result := app.runCommand(command{Kind: commandPlay}); result.Err != nil {
		t.Fatalf("stage play command: %v", result.Err)
	}
	if !app.sync.playhead.Playing() || players[0].plays != 1 || players[1].plays != 1 || players[2].plays != 1 {
		t.Fatal("the stage action did not play the stage members and intersecting sync group")
	}

	app.sceneMembers[scene.ID] = map[string]bool{"stage-independent": true}
	app.sync.playhead.Pause()
	plan, err = app.stageTransportPlan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.syncGroup || len(plan.items) != 1 || plan.items[0] != app.items[1] {
		t.Fatalf("independent stage plan = %+v, want only the independent stage member", plan)
	}
	if result := app.runCommand(command{Kind: commandPlay}); result.Err != nil {
		t.Fatalf("independent stage play command: %v", result.Err)
	}
	if app.sync.playhead.Playing() || players[0].plays != 1 || players[2].plays != 1 || players[1].plays != 2 {
		t.Fatal("an independent stage action changed the sync group or skipped its player")
	}
	if !app.playing() {
		t.Fatal("stage playback state did not read the independent member in the same plan")
	}
}
