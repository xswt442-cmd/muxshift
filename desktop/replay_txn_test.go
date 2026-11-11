package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// fakeSeekPlayer stands in for an engine player in the transaction tests: the
// transaction only reads readiness, closes it, and hands it to an item.
type fakeSeekPlayer struct {
	name   string
	ready  bool
	closed bool

	// honourSeek decides whether the player reports the position it was asked
	// for. A player that does not move is what a failed seek looks like from
	// here: the transaction must notice and give it back.
	honourSeek bool
	position   time.Duration
}

func (p *fakeSeekPlayer) Ready() bool     { return p.ready }
func (p *fakeSeekPlayer) Playing() bool   { return false }
func (p *fakeSeekPlayer) Buffering() bool { return false }

func (p *fakeSeekPlayer) Position() time.Duration { return p.position }
func (p *fakeSeekPlayer) Duration() time.Duration { return 0 }
func (p *fakeSeekPlayer) Volume() float64         { return 0 }
func (p *fakeSeekPlayer) Muted() bool             { return true }
func (p *fakeSeekPlayer) Err() error              { return nil }
func (p *fakeSeekPlayer) Play()                   {}
func (p *fakeSeekPlayer) Pause()                  {}
func (p *fakeSeekPlayer) Seek(target time.Duration) {
	if p.honourSeek {
		p.position = target
	}
}
func (p *fakeSeekPlayer) SetVolume(float64) *ui.VideoPlayer { return nil }
func (p *fakeSeekPlayer) SetMuted(bool) *ui.VideoPlayer     { return nil }
func (p *fakeSeekPlayer) Close()                            { p.closed = true }

// seekTxnFixture is the two-source session the transaction tests run against,
// with the player and thread seams pointed at test doubles.
func seekTxnFixture(t *testing.T) (replaySeekState, *[]*fakeSeekPlayer) {
	t.Helper()
	session := replaySeekSession(t)
	prevProbe := probeFirstMediaTime
	probeFirstMediaTime = func(context.Context, string) (time.Duration, error) {
		return 100 * time.Second, nil
	}
	t.Cleanup(func() { probeFirstMediaTime = prevProbe })

	opened := &[]*fakeSeekPlayer{}
	session.app.seekMain = func(fn func()) { fn() }
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true, honourSeek: true}
		*opened = append(*opened, player)
		return player, nil
	}
	session.app.seekStarted = func(player desktopPlayer) bool { return player.Ready() }
	return session, opened
}

// waitSeekTxn waits for the transaction to be over, so the test reads a settled
// session rather than a race.
func waitSeekTxn(t *testing.T, app *appState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		app.seekMu.Lock()
		running := app.seek
		app.seekMu.Unlock()
		if running == nil {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the replay transaction did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSeekTxnCutsAndCommitsEverySource(t *testing.T) {
	session, opened := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != 2 {
		t.Fatalf("opened %d players, want 2", len(*opened))
	}
	for index, it := range session.items {
		if it.replay == before[index] {
			t.Fatalf("%s still shows the recording it had", it.source.ID)
		}
		if it.replay.StartBufferTime != 6*time.Second {
			t.Fatalf("%s: the new recording starts at %s, want 6s", it.source.ID, it.replay.StartBufferTime)
		}
		if it.replayAnchor == nil {
			t.Fatalf("%s: the new recording has no anchor", it.source.ID)
		}
		if it.player == nil {
			t.Fatalf("%s: the new player was not committed", it.source.ID)
		}
		if it.generation != 3 {
			t.Fatalf("%s: generation = %d, want 3", it.source.ID, it.generation)
		}
	}
	open, given := session.app.leaseStats()
	if open != 2 || given != 2 {
		t.Fatalf("leases open=%d given=%d, want 2 and 2", open, given)
	}
}

func TestSeekTxnRollsBackWhenAPlayerCannotOpen(t *testing.T) {
	session, _ := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
	}
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		return nil, fmt.Errorf("this build cannot open a file")
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for index, it := range session.items {
		if it.replay != before[index] {
			t.Fatalf("%s: the failed transaction replaced its recording", it.source.ID)
		}
		if it.player != nil || it.generation != 2 {
			t.Fatalf("%s: player=%v generation=%d after a rollback", it.source.ID, it.player, it.generation)
		}
	}
	if open, given := session.app.leaseStats(); open != 0 || given != 1 {
		t.Fatalf("leases open=%d given=%d after a rollback, want 0 and 1", open, given)
	}
}

func TestSeekTxnRollsBackWhenTheStateMovesBeforeTheCommit(t *testing.T) {
	session, opened := seekTxnFixture(t)
	session.app.beforeSeekCommit = func() {
		// A discontinuity arrives while the recordings are waiting for their
		// pictures: the buffer's clock moves, so the plan no longer describes it.
		session.adds[0](9, true)
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for _, it := range session.items {
		if it.player != nil || it.generation != 2 {
			t.Fatalf("%s: a moved buffer still committed (player=%v generation=%d)", it.source.ID, it.player, it.generation)
		}
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open by a rolled back transaction", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a rollback, want 0", open)
	}
}

func TestSeekTxnRollsBackWhenAPlayerNeverShows(t *testing.T) {
	session, opened := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 150 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })
	session.app.seekStarted = func(desktopPlayer) bool { return false }
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != 2 {
		t.Fatalf("opened %d players, want 2", len(*opened))
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open when the picture never came", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a failed handover, want 0", open)
	}
}

func TestSeekTxnSettlesAPositionInsideTheFile(t *testing.T) {
	session, opened := seekTxnFixture(t)
	// A target that is not on a segment boundary: the file is cut at 6s, so
	// 7s sits one second into it. The transaction must ask the player for that
	// offset, not for the start of the file.
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != 2 {
		t.Fatalf("opened %d players, want 2", len(*opened))
	}
	for _, player := range *opened {
		if player.position != time.Second {
			t.Fatalf("%s reports %s, want the target at 1s into its file", player.name, player.position)
		}
	}
	for _, it := range session.items {
		if it.replay.StartBufferTime != 6*time.Second {
			t.Fatalf("%s: the file starts at %s, want 6s", it.source.ID, it.replay.StartBufferTime)
		}
	}
}

func TestSeekTxnRollsBackWhenAPlayerNeverSettles(t *testing.T) {
	session, opened := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 300 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		// A player that opens and draws but never moves.
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true}
		*opened = append(*opened, player)
		return player, nil
	}
	before := make([]*dvr.Recording, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for index, it := range session.items {
		if it.replay != before[index] || it.player != nil {
			t.Fatalf("%s: a player that never settled still committed", it.source.ID)
		}
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open after it never settled", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a failed settle, want 0", open)
	}
}
