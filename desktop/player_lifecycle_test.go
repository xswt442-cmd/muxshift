package main

import (
	"context"
	"image"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core/dvr"
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

func TestReplayBlocksReloadAndRetry(t *testing.T) {
	a, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := a.session.AddSource("replay.mp4")
	if err != nil {
		t.Fatal(err)
	}
	it := &item{source: source, uri: "replay.mp4", replay: &dvr.Recording{}}
	a.items = append(a.items, it)
	for _, kind := range []commandKind{commandReload, commandRetry} {
		result := a.runCommand(command{Kind: kind, Source: source.ID})
		if result.Err == nil || !strings.Contains(result.Err.Error(), "replaying") {
			t.Errorf("%v: got %v, want a replay refusal", kind, result.Err)
		}
	}
}

func TestShutdownDrainsQueuedRecordingRelease(t *testing.T) {
	checkShutdownRecordingRelease(t, true)
}

func TestShutdownInterruptsInFlightRetirement(t *testing.T) {
	checkShutdownRecordingRelease(t, false)
}

func TestShutdownDrainsNativeReplayCloseFromPreparingWorker(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatal(err)
	}
	app.window = &mygo.Window{}
	decoder := &lifecycleDecoder{closed: make(chan struct{}, 1)}
	player, err := ui.NewVideoPlayer(decoder)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	txn := &seekTxn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	app.seek = txn
	go func() {
		defer close(txn.done)
		<-ctx.Done()
		app.closeReplayPlayer(player, nil)
	}()
	app.shutdown()
	select {
	case <-decoder.closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown left the worker's player open")
	}
}

func checkShutdownRecordingRelease(t *testing.T, advanceFrames bool) {
	t.Helper()
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
	if advanceFrames {
		for job := range a.retiring {
			for range 3 {
				a.signalFrame()
			}
			a.finishRetirementOnMain(job)
		}
		if len(a.retiring) != 0 || len(a.mediaCloseQueue) != 1 || a.mediaCloseQueue[0].handle.lease != lease {
			t.Fatal("the close queue did not take ownership of the recording lease")
		}
	}
	if _, err := os.Stat(recording.Path); err != nil {
		t.Fatalf("the retiring player lost its recording: %v", err)
	}
	a.shutdown()
	if len(a.retiring) != 0 || len(a.mediaCloseQueue) != 0 {
		t.Fatal("shutdown left outstanding player retirements")
	}
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
	decoder := &lifecycleDecoder{closed: make(chan struct{}, 2)}
	previous, err := ui.NewVideoPlayer(decoder)
	if err != nil {
		t.Fatal(err)
	}
	replacement := &fakeSeekPlayer{}
	kept := &handover{previous: previous}
	it := &item{uri: "entry.mp4", player: replacement, previous: previous, handover: kept, generation: 1}
	// The closing thread processes callbacks individually. Hold the rollback
	// callback until after the current player and its predecessor are detached.
	type callback struct {
		run  func()
		done chan struct{}
	}
	queue := make(chan callback)
	stop := make(chan struct{})
	a.onMain = func(fn func()) {
		call := callback{run: fn, done: make(chan struct{})}
		select {
		case queue <- call:
			<-call.done
		case <-stop:
		}
	}
	savedWait := handoverWait
	handoverWait = 0
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		a.handOver(it, 1, kept)
	}()
	defer func() {
		close(stop)
		<-finished
		handoverWait = savedWait
		a.releaseOnce.Do(a.shutdown)
	}()
	next := func() callback {
		select {
		case call := <-queue:
			return call
		case <-time.After(5 * time.Second):
			t.Fatal("handover did not post its drawing-thread callback")
			return callback{}
		}
	}
	probe := next()
	probe.run()
	close(probe.done)
	rollback := next()
	a.closeOnMain(it)
	rollback.run()
	close(rollback.done)
	if it.player != nil || it.generation != 2 {
		t.Fatalf("obsolete rollback reinstalled a player: generation=%d", it.generation)
	}
	if kept.take() != nil || len(a.retiring) != 1 {
		t.Fatal("ownership of the previous player was not transferred once")
	}
	for retirement := range a.retiring {
		if len(retirement.handles) != 1 || retirement.handles[0].player != previous {
			t.Fatal("retirement does not own the detached MyGo player")
		}
		a.finishRetirementOnMain(retirement)
		if len(a.retiring) != 1 {
			t.Fatal("retirement ran before three frames were built")
		}
		for range 3 {
			a.signalFrame()
		}
		a.finishRetirementOnMain(retirement)
	}
	// With no replacement on the stage, shutdown closes the last held player.
	a.releaseOnce.Do(a.shutdown)
	select {
	case <-decoder.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the detached decoder was not closed")
	}
	if len(decoder.closed) != 0 {
		t.Fatal("the decoder was closed more than once")
	}
}

type lifecycleDecoder struct{ closed chan struct{} }

func (d *lifecycleDecoder) Next() (ui.VideoFrame, error) {
	return ui.VideoFrame{Image: image.NewRGBA(image.Rect(0, 0, 1, 1)), Duration: time.Second}, nil
}
func (d *lifecycleDecoder) Rewind() error { return nil }
func (d *lifecycleDecoder) Close() error  { d.closed <- struct{}{}; return nil }

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
