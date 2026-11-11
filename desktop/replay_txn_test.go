package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

	// ignoreSeeks is the number of requests the player drops on the floor, as
	// the engine does when a seek arrives before the player is settled.
	ignoreSeeks int
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
	if p.ignoreSeeks > 0 {
		p.ignoreSeeks--
		return
	}
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

func TestSeekTxnCommitsNothingWhenTheLastSourceMoves(t *testing.T) {
	session, opened := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	players := make([]desktopPlayer, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
		players[index] = it.player
	}
	session.app.beforeSeekCommit = func() {
		// The second source is replaced after the first one has already been
		// validated. A commit that validated and installed source by source
		// would leave the first one changed; this one must change neither.
		session.items[1].generation++
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for index, it := range session.items {
		if it.replay != before[index] {
			t.Fatalf("%s: its recording changed although the transaction was refused", it.source.ID)
		}
		if it.player != players[index] {
			t.Fatalf("%s: its player changed although the transaction was refused", it.source.ID)
		}
		// Only the hook's own change is allowed through: the refused commit
		// must not have bumped anything itself.
		want := 2
		if index == 1 {
			want = 3
		}
		if it.generation != want {
			t.Fatalf("%s: generation=%d after a refusal, want %d", it.source.ID, it.generation, want)
		}
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open by a refused commit", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a refused commit, want 0", open)
	}
}

func TestSeekTxnRecordsThePositionThePlayerReported(t *testing.T) {
	session, opened := seekTxnFixture(t)
	// A player that opens and draws but never moves. Asked for 10ms it reports
	// 0s: inside the tolerance, so the commit is allowed, but the transaction
	// must record 0s and the 10ms it is short rather than claiming the target.
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true}
		*opened = append(*opened, player)
		return player, nil
	}
	if err := session.app.startSeekTxn(session.items, 2*time.Second+10*time.Millisecond); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	landings := session.app.seekLandings()
	if len(landings) != 2 {
		t.Fatalf("recorded %d landings, want 2", len(landings))
	}
	for _, landing := range landings {
		if landing.requested != 10*time.Millisecond {
			t.Fatalf("%s requested %s, want 10ms", landing.source, landing.requested)
		}
		if landing.reported != 0 {
			t.Fatalf("%s reported %s, want the position the player really gave (0s)", landing.source, landing.reported)
		}
		if landing.residual != -10*time.Millisecond {
			t.Fatalf("%s residual %s, want -10ms", landing.source, landing.residual)
		}
	}
	for _, it := range session.items {
		if it.replay == nil || it.replay.StartBufferTime != 2*time.Second {
			t.Fatalf("%s: the file start is %v, want 2s", it.source.ID, it.replay)
		}
	}
}

func TestStartSeekTxnRefusesASecondRequest(t *testing.T) {
	session, _ := seekTxnFixture(t)
	release := make(chan struct{})
	opened := make(chan struct{}, 4)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened <- struct{}{}
		<-release
		return inner(recording)
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the first transaction did not start: %v", err)
	}
	<-opened // the first one is inside its preparation
	err := session.app.startSeekTxn(session.items, 6*time.Second)
	if err == nil {
		t.Fatal("a second transaction started while the first was still preparing")
	}
	if !strings.Contains(err.Error(), "already being prepared") {
		t.Fatalf("the refusal did not explain itself: %v", err)
	}
	close(release)
	waitSeekTxn(t, session.app)
}

// Two requests that arrive together must not both find the transaction field
// empty and both plan; the start decision is serialised.
func TestStartSeekTxnHandlesRequestsThatArriveTogether(t *testing.T) {
	session, _ := seekTxnFixture(t)
	release := make(chan struct{})
	opened := make(chan struct{}, 8)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened <- struct{}{}
		<-release
		return inner(recording)
	}

	var started, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
				refused.Add(1)
				return
			}
			started.Add(1)
		}()
	}
	<-opened
	close(release)
	wg.Wait()
	if started.Load() != 1 {
		t.Fatalf("%d transactions started, want exactly one", started.Load())
	}
	if refused.Load() != 3 {
		t.Fatalf("%d requests were refused, want three", refused.Load())
	}
	waitSeekTxn(t, session.app)
}

// A runner replaced between the plan and the commit is a different buffer
// underneath the same item; the commit must refuse and change nothing.
func TestSeekTxnRefusesWhenTheRunnerIsReplacedBeforeTheCommit(t *testing.T) {
	session, opened := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	beforePlayers := make([]desktopPlayer, len(session.items))
	for i, it := range session.items {
		before[i] = it.replay
		beforePlayers[i] = it.player
	}
	// The buffer is created outside the hook: the hook runs on the transaction's
	// goroutine, where t.Fatalf would end that goroutine without failing the test.
	second, err := dvr.New(dvr.Options{Dir: t.TempDir(), Source: session.items[0].source.ID})
	if err != nil {
		t.Fatalf("a second buffer: %v", err)
	}
	session.app.beforeSeekCommit = func() {
		session.app.runners[0].runner = &dvrRunner{source: session.items[0].source.ID, buffer: second}
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	waitSeekTxn(t, session.app)
	for i, it := range session.items {
		if it.replay != before[i] {
			t.Fatalf("source %d: its recording changed although the runner was replaced", i+1)
		}
		if it.player != beforePlayers[i] {
			t.Fatalf("source %d: its player changed although the runner was replaced", i+1)
		}
	}
	// openPlayers counts through the window, which a unit test does not have;
	// the players the transaction opened are all this test needs to look at.
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s is still open after the refusal", player.name)
		}
	}
}

// A transaction that was replaced while it was preparing must not commit, must
// give its files and players back, and must not clear the transaction that took
// the session over.
func TestSeekTxnGivesWayWhenAnotherTakesOver(t *testing.T) {
	session, opened := seekTxnFixture(t)
	release := make(chan struct{})
	openedOnce := make(chan struct{}, 4)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		openedOnce <- struct{}{}
		<-release
		return inner(recording)
	}
	before := make([]*dvr.Recording, len(session.items))
	for i, it := range session.items {
		before[i] = it.replay
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the first transaction did not start: %v", err)
	}
	<-openedOnce

	newer := &seekTxn{event: 9 * time.Second}
	session.app.seekMu.Lock()
	session.app.seek = newer
	session.app.seekMu.Unlock()
	close(release)

	deadline := time.Now().Add(10 * time.Second)
	for {
		allClosed := true
		for _, player := range *opened {
			if !player.closed {
				allClosed = false
			}
		}
		if allClosed && len(*opened) > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the replaced transaction kept its players")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, it := range session.items {
		if it.replay != before[i] {
			t.Fatalf("source %d: the replaced transaction committed its recording", i+1)
		}
	}
	session.app.seekMu.Lock()
	holder := session.app.seek
	session.app.seekMu.Unlock()
	if holder != newer {
		t.Fatal("the replaced transaction cleared the one that took over")
	}
}

// A transaction that has lost the session may not install anything, even when
// it is already inside the commit step.
func TestSeekTxnCommitsNothingOnceOwnershipIsGone(t *testing.T) {
	session, opened := seekTxnFixture(t)
	newer := &seekTxn{event: 9 * time.Second}
	session.app.beforeSeekCommit = func() {
		session.app.seekMu.Lock()
		session.app.seek = newer
		session.app.seekMu.Unlock()
	}
	before := make([]*dvr.Recording, len(session.items))
	for i, it := range session.items {
		before[i] = it.replay
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(*opened) == 0 || !allClosed(*opened) {
		if !time.Now().Before(deadline) {
			t.Fatal("the transaction that lost the session kept its players")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, it := range session.items {
		if it.replay != before[i] || it.generation != 2 {
			t.Fatalf("source %d: a transaction that lost the session still installed itself", i+1)
		}
	}
	session.app.seekMu.Lock()
	holder := session.app.seek
	session.app.seekMu.Unlock()
	if holder != newer {
		t.Fatal("the transaction cleared the one that took over")
	}
}

// Rolling an old transaction back must not clear the transaction that replaced
// it, even when the abort arrives after the replacement.
func TestSeekTxnAbortLeavesANewerTransactionAlone(t *testing.T) {
	session, _ := seekTxnFixture(t)
	old := &seekTxn{event: 6 * time.Second}
	newer := &seekTxn{event: 9 * time.Second}
	session.app.seekMu.Lock()
	session.app.seek = newer
	session.app.seekMu.Unlock()

	session.app.abortSeekTxn(old, "test abort")

	session.app.seekMu.Lock()
	holder := session.app.seek
	session.app.seekMu.Unlock()
	if holder != newer {
		t.Fatal("aborting an old transaction cleared the newer one")
	}
}
func allClosed(players []*fakeSeekPlayer) bool {
	for _, player := range players {
		if !player.closed {
			return false
		}
	}
	return len(players) > 0
}

// settleTxnFor is a transaction with one prepared step: the settle step only
// needs the player it landed, the item it names and the offset it wants.
func settleTxnFor(session replaySeekState, player desktopPlayer, offset time.Duration) *seekTxn {
	return &seekTxn{
		event: 6 * time.Second,
		steps: []*seekTxnStep{{item: session.items[0], player: player, offset: offset}},
	}
}

// TestSettleSeekRetriesAnIgnoredRequest covers the engine behaviour the retry
// exists for: the first request is dropped, the second one takes.
func TestSettleSeekRetriesAnIgnoredRequest(t *testing.T) {
	session, _ := seekTxnFixture(t)
	player := &fakeSeekPlayer{ready: true, honourSeek: true, ignoreSeeks: 1}
	start := time.Now()
	if err := session.app.settleSeekTxn(settleTxnFor(session, player, time.Second)); err != nil {
		t.Fatalf("a dropped request that was re-issued did not settle: %v", err)
	}
	if player.position != time.Second {
		t.Fatalf("the player ended at %s, want the requested 1s", player.position)
	}
	if elapsed := time.Since(start); elapsed < 1200*time.Millisecond {
		t.Fatalf("the request was honoured after %s, so no re-issue happened", elapsed)
	}
}

// TestSettleSeekGivesUpAfterTheBudget keeps the re-issue bounded: a player
// that drops every request is a failed landing, not a longer wait.
func TestSettleSeekGivesUpAfterTheBudget(t *testing.T) {
	session, _ := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 300 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })

	player := &fakeSeekPlayer{ready: true, honourSeek: true, ignoreSeeks: 100}
	err := session.app.settleSeekTxn(settleTxnFor(session, player, time.Second))
	if err == nil || !strings.Contains(err.Error(), "did not settle") {
		t.Fatalf("a player that never arrived returned %v, want a settle failure", err)
	}
	if player.position != 0 {
		t.Fatalf("the player moved to %s although every request was dropped", player.position)
	}
}

// TestShutdownWaitsForAnOpeningPlayer is the case the interleaving scenario
// did not cover: the session closes while a replacement player is still being
// opened. The player must not outlive the session, and shutdown must not
// return before the work it stopped has given its resources back.
func TestShutdownWaitsForAnOpeningPlayer(t *testing.T) {
	session, opened := seekTxnFixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		entered <- struct{}{}
		<-release
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true, honourSeek: true}
		*opened = append(*opened, player)
		return player, nil
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	<-entered

	closed := make(chan struct{})
	go func() {
		if !session.app.shutdownReplayTxn() {
			t.Error("the closing session gave up waiting for the preparation to return")
		}
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("the session closed while a player was still being opened")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not close after the player opened")
	}

	if !allClosed(*opened) {
		t.Fatal("a player opened after the session asked to close was left open")
	}
	if owner := session.app.seekInFlight(); owner != nil {
		t.Fatalf("a transaction is still registered after the session closed: %s", owner.event)
	}
	if pending := session.app.releaseLeases(); len(pending) > 0 {
		t.Fatalf("leases were left open after the session closed: %v", pending)
	}
}

// TestShutdownWaitsPastTheBudgetForAnOpeningPlayer pins the closing rule: the
// budget is when the wait gets reported, not when the buffers may be destroyed.
// A preparation that is still inside an engine call keeps running, and the
// session waits for it to return instead of closing the buffer it is reading.
func TestShutdownWaitsPastTheBudgetForAnOpeningPlayer(t *testing.T) {
	session, opened := seekTxnFixture(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		close(entered)
		<-release
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true}
		*opened = append(*opened, player)
		return player, nil
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	<-entered

	closed := make(chan struct{})
	go func() {
		session.app.shutdownReplayTxn()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("the close returned while a player was still opening")
	case <-time.After(seekShutdownWait + 400*time.Millisecond):
	}

	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the close did not return after the opening finished")
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatal("a player opened after the close began was left open")
		}
	}
}

// TestAClosedSessionDoesNotWaitForADrawingThreadItIsBlocking is the deadlock
// case: a window closes while a transaction is preparing, so the drawing thread
// stops the transaction and waits for it, while the transaction is waiting for
// the drawing thread to run its next step. Bounded dispatch breaks the cycle:
// the step gives up, the preparation ends, and the close can finish.
func TestAClosedSessionDoesNotWaitForADrawingThreadItIsBlocking(t *testing.T) {
	session, opened := seekTxnFixture(t)
	blocked := make(chan struct{})
	defer close(blocked)
	session.app.seekMain = func(func()) {
		// This is what mygoRunOnMain does: it waits for the drawing thread to
		// run the callback. Here the drawing thread is inside the close that
		// waits for this transaction, so the post itself never returns.
		<-blocked
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(*opened) == 0 {
		if !time.Now().Before(deadline) {
			t.Fatal("no replacement player was opened")
		}
		time.Sleep(20 * time.Millisecond)
	}

	start := time.Now()
	if !session.app.shutdownReplayTxn() {
		t.Fatal("the close reported that it could not stop the transaction")
	}
	if elapsed := time.Since(start); elapsed > 3*seekDispatchWait {
		t.Fatalf("the close waited %s for a step the drawing thread could not run", elapsed)
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatal("a player opened by the stopped transaction stayed open")
		}
	}
	if report, ok := session.app.lastReport(); !ok || report.Phase != replayRolledBack {
		t.Fatalf("the stopped transaction ended as %q, want rolled back", report.Phase)
	}
}
