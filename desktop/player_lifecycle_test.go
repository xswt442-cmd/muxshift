package main

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestLifecyclePendingBlocksReloadAndRetry(t *testing.T) {
	a, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := a.session.AddSource("closing.mp4")
	if err != nil {
		t.Fatal(err)
	}
	it := &item{source: source, uri: "closing.mp4", lifecyclePending: true}
	a.items = append(a.items, it)
	for _, kind := range []commandKind{commandReload, commandRetry} {
		result := a.runCommand(command{Kind: kind, Source: source.ID, URI: "file://invalid"})
		if result.Err == nil || !strings.Contains(result.Err.Error(), "still closing") {
			t.Errorf("%v: got %v, want a closing-player refusal", kind, result.Err)
		}
	}
	player := &ui.VideoPlayer{}
	it.player = player
	if err := a.ensurePlayer(it); err == nil || !strings.Contains(err.Error(), "still closing") {
		t.Errorf("ensurePlayer: got %v, want a closing-player refusal", err)
	}
	if it.player != player || it.generation != 0 {
		t.Fatalf("opened a replacement while closing: player=%v, generation=%d", it.player, it.generation)
	}
}

func TestShutdownWaitsForBackgroundReconcileClose(t *testing.T) {
	a, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	buffer, _ := fixtureBuffer(t)
	recording, lease, err := buffer.RecordAndLease(0)
	if err != nil {
		t.Fatal(err)
	}
	runner := &dvrRunner{buffer: buffer, stop: make(chan struct{}), done: make(chan struct{})}
	close(runner.done)
	a.trackRunner("fixture", runner)
	a.tookLease()
	it := &item{replay: &recording, replayLease: lease, lifecyclePending: true}
	a.items = append(a.items, it)
	a.retireItemOnMain(it, nil)
	if len(a.retiring) != 1 || it.replayLease != nil || it.generation != 1 {
		t.Fatal("retirement did not take ownership and invalidate the item")
	}
	if _, err := os.Stat(recording.Path); err != nil {
		t.Fatalf("the retiring player lost its recording: %v", err)
	}
	a.shutdown()
	if open, _ := a.leaseStats(); open != 0 {
		t.Fatalf("shutdown left %d recording leases open", open)
	}
	if _, err := os.Stat(recording.Path); !os.IsNotExist(err) {
		t.Fatalf("shutdown left the recording behind: %v", err)
	}
}

func TestCloseAndHandoverRollbackRace(t *testing.T) {
	a, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	previous := &ui.VideoPlayer{}
	replacement := &ui.VideoPlayer{}
	kept := &handover{previous: previous}
	it := &item{uri: "entry.mp4", player: replacement, previous: previous, handover: kept, generation: 1}
	a.closeOnMain(it)
	defer func() {
		close(a.closeDone)
		a.closeWait.Wait()
	}()
	if restored := a.rollbackHandoverOnMain(it, 1, kept); restored != "" || it.player != nil || it.generation != 2 {
		t.Fatalf("obsolete rollback reinstalled a player: restored=%q generation=%d", restored, it.generation)
	}
	if kept.take() != nil || len(a.retiring) != 1 {
		t.Fatal("ownership of the previous player was not transferred once")
	}
	for retirement := range a.retiring {
		if len(retirement.handles) != 2 || retirement.handles[0].player != previous || retirement.handles[1].player != replacement {
			t.Fatal("retirement does not own both detached players")
		}
	}
}

func TestWorkspaceRemembersWorkbenchBoundsWhenQuittingViewingMode(t *testing.T) {
	workbench := mygo.Rectangle{X: 50, Y: 80, Width: 1190, Height: 720}
	for _, mode := range []viewingMode{viewCompact, viewFullscreen} {
		a := &appState{
			viewing: mode, savedForViewing: viewingSaved{saved: true, bounds: workbench},
			dock: paneOpen, dockUp: false,
		}
		got := a.workspaceFromWindow(&mygo.Window{}, true)
		if got.X != workbench.X || got.Y != workbench.Y || got.Width != workbench.Width || got.Height != workbench.Height {
			t.Errorf("%v: remembered %+v, want original workbench bounds %+v", mode, got, workbench)
		}
		if got.Dock != paneModeName(paneClosed) {
			t.Errorf("%v: remembered dock as %q although it was closed", mode, got.Dock)
		}
	}
}
