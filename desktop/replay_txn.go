// P16.4b-2: the replay transaction.
//
// An authorisation says where each source would have to be cut and which
// position in the file the target sits at (core/dvr/seek.go, P16.4a). This file
// turns such a decision into the resources a replay needs 闂?a recording that
// really contains the target, the lease that keeps that file alive, and a player
// that has shown a picture 闂?and commits them only while the decision still
// holds. Until the commit, every source keeps the player and the lease it had,
// so a failure anywhere leaves the window exactly as it was.
//
// It does not seek: after the commit the new players sit at the start of their
// files, which is the target. Asking them for a position and confirming it is
// P16.4b-3.
package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// seekTxnStep is one source's share of a replay transaction.
type seekTxnStep struct {
	item       *item
	runner     *dvrRunner
	generation int

	// content is the buffer time the transaction cuts at, the plan's hop for
	// this source. Everything else is derived from the file that cutting
	// produced, never from the file that was playing before.
	content time.Duration

	recording dvr.Recording
	anchor    dvr.Anchor
	alignment dvr.Alignment

	lease  *dvr.Lease
	player desktopPlayer

	// offset is where the target sits inside the file that cutting produced,
	// and settled is where the player reported it had arrived. They are two
	// different facts: the second says the player is at that position, not that
	// the frame there is on screen.
	offset   time.Duration
	settled  time.Duration
	reported time.Duration
	measured bool

	// wasPlaying is the state the player was committed in. Settling it means
	// stopping, asking for the position and letting it go again, so the state
	// it had before is restored afterwards rather than left paused.
	wasPlaying bool

	// finished makes the lease and the player be given back by exactly one
	// path: a rollback, a superseded run, or the commit that released them as
	// the previous resources.
	finished sync.Once
}

// seekTxn is one attempt to prepare a plan's files and players.
type seekTxn struct {
	event time.Duration
	plan  dvr.SeekPlan

	// prepared is the plan rebuilt from the recordings that were really cut.
	// The transaction commits only while that plan still matches the session.
	prepared dvr.SeekPlan

	// id ties every report about this transaction to the same attempt.
	id uint64

	// noticeOnce keeps one account per transaction: the first ending wins, and
	// a later abort of the same transaction cannot rewrite it.
	noticeOnce sync.Once

	steps []*seekTxnStep

	// done is closed when the preparing goroutine has returned. A closing
	// session waits on it so the files and players the work opened are given
	// back before the buffers they were cut from are closed.
	done chan struct{}
}

// stepFor returns the step preparing a source, or nil when the transaction does
// not cover it.
func (txn *seekTxn) stepFor(source core.SourceID) *seekTxnStep {
	for _, step := range txn.steps {
		if step.item.source.ID == source {
			return step
		}
	}
	return nil
}

// startSeekTxn decides whether the event time may be replayed, and starts
// preparing the files and players it needs. It returns as soon as the work is
// off the caller's thread; progress goes to the log, and a failure leaves the
// sources untouched.
func (a *appState) startSeekTxn(items []*item, event time.Duration) (err error) {
	// Every refusal is its own account: a request that never became a
	// transaction is still something a surface may have to explain.
	defer func() {
		if err != nil {
			a.publishReport(replayReport{ID: replaySeq.Add(1), Event: event, Phase: replayRefused, Reason: err.Error()})
		}
	}()
	if len(items) == 0 {
		return fmt.Errorf("no source was offered for the replay seek")
	}
	a.seekStart.Lock()
	defer a.seekStart.Unlock()

	a.seekMu.Lock()
	busy := a.seek != nil
	a.seekMu.Unlock()
	if busy {
		return fmt.Errorf("a replay is already being prepared; wait for it to finish")
	}

	plan, err := a.planReplaySeek(event, items)
	if err != nil {
		return err
	}
	txn := &seekTxn{event: event, plan: plan, done: make(chan struct{}), id: replaySeq.Add(1)}
	for _, hop := range plan.Hops() {
		it, _, err := a.itemBySource(hop.Source)
		if err != nil {
			return err
		}
		runner := a.runnerFor(it)
		if runner == nil {
			return fmt.Errorf("%s has no replay buffer to cut from", a.name(it))
		}
		source, err := a.replaySeekSource(it, event)
		if err != nil {
			return err
		}
		txn.steps = append(txn.steps, &seekTxnStep{
			item:       it,
			runner:     runner,
			generation: it.generation,
			content:    hop.Content,
			alignment:  source.Alignment,
		})
	}

	a.seekMu.Lock()
	a.seek = txn
	a.seekMu.Unlock()
	a.publishReport(txn.reportFor(replayPreparing, ""))
	go func() {
		defer close(txn.done)
		a.prepareSeekTxn(txn)
	}()
	return nil
}

// prepareSeekTxn cuts one recording per source, opens a player on each, and
// commits only once every player has a picture and the authorisation still
// describes the session. Every exit either commits or gives back what it took.
func (a *appState) prepareSeekTxn(txn *seekTxn) {
	for _, step := range txn.steps {
		// Ownership is re-read between sources: a newer transaction or a closing
		// session gives the work back instead of spending more files on it.
		if !a.holdsSeek(txn) {
			a.abortSeekTxn(txn, "another replay transaction took the session over")
			return
		}
		recording, lease, err := step.runner.recordAndHold(step.content)
		if err != nil {
			a.abortSeekTxn(txn, fmt.Sprintf("cut %s at %s: %v", a.name(step.item), shortDuration(step.content), err))
			return
		}
		a.tookLease()
		a.seekMu.Lock()
		step.recording = recording
		step.lease = lease
		a.seekMu.Unlock()

		anchor, anchorErr := measureRecordingAnchor(recording)
		if anchorErr != nil {
			a.abortSeekTxn(txn, fmt.Sprintf("measure %s: %v", recording.FileName(), anchorErr))
			return
		}
		a.seekMu.Lock()
		step.anchor = anchor
		a.seekMu.Unlock()

		player, err := a.openSeekPlayer(recording)
		if err != nil {
			a.abortSeekTxn(txn, fmt.Sprintf("open %s: %v", recording.FileName(), err))
			return
		}
		// The session may have closed or another transaction may have taken
		// over while this player was opening. The work stops here and gives the
		// player straight back, so a closing session never has a player appear
		// after it asked the transaction to stop.
		if !a.holdsSeek(txn) {
			a.closeSeekPlayer(player)
			a.abortSeekTxn(txn, "the session gave up while a replacement was opening")
			return
		}
		a.seekMu.Lock()
		step.player = player
		a.seekMu.Unlock()
	}

	prepared, err := dvr.PlanSeek(txn.event, seekTxnSources(txn))
	if err != nil {
		a.abortSeekTxn(txn, fmt.Sprintf("the cut recordings do not authorise the target: %v", err))
		return
	}
	// The plan is rebuilt from the files that were really cut, so it has to
	// agree with where those files start: the target sits at the cut's own
	// offset into the file, which is zero only when the cut began on the target
	// frame. A disagreement means the file and the plan describe different cuts.
	for _, hop := range prepared.Hops() {
		step := txn.stepFor(hop.Source)
		if step == nil {
			a.abortSeekTxn(txn, fmt.Sprintf("%s: the rebuilt plan names a source the transaction did not prepare", hop.Source))
			return
		}
		want := step.content - step.recording.StartBufferTime
		if hop.Position != want {
			a.abortSeekTxn(txn, fmt.Sprintf("%s: the file starts at %s, so the target sits at %s into it, not at %s",
				hop.Source, shortDuration(step.recording.StartBufferTime), shortDuration(want), shortDuration(hop.Position)))
			return
		}
		a.seekMu.Lock()
		step.offset = hop.Position
		a.seekMu.Unlock()
	}
	txn.prepared = prepared

	logf("seek", "prepared %d recording(s) for event %s; waiting for every replacement to show a picture",
		len(txn.steps), shortDuration(txn.event))
	if err := a.waitSeekPlayers(txn); err != nil {
		a.abortSeekTxn(txn, err.Error())
		return
	}
	// Position the replacements before the commit, while the players that are on
	// screen still exist: a player that never reaches its position can then be
	// given back with the rest of the transaction instead of leaving the session
	// on a file that cannot show the requested moment.
	if err := a.settleSeekTxn(txn); err != nil {
		a.abortSeekTxn(txn, err.Error())
		return
	}
	if a.beforeSeekCommit != nil {
		a.beforeSeekCommit()
	}
	a.commitSeekTxn(txn)
}

// seekTxnSources is what the transaction itself asks the contract: one source
// per cut file, with that file's own identity, range and measured anchor.
func seekTxnSources(txn *seekTxn) []dvr.SeekSource {
	sources := make([]dvr.SeekSource, 0, len(txn.steps))
	for _, step := range txn.steps {
		sources = append(sources, dvr.SeekSource{
			Source:           step.recording.Source,
			Epoch:            step.recording.SourceMediaEpoch,
			Instance:         step.recording.BufferInstance,
			Range:            dvr.Range{Start: step.recording.StartBufferTime, End: step.recording.EndBufferTime, Epoch: step.recording.SourceMediaEpoch},
			Anchor:           step.anchor,
			Alignment:        step.alignment,
			Recording:        step.recording,
			PlayerGeneration: uint64(step.generation),
		})
	}
	return sources
}

// revalidateSeekTxn asks the live session again, now that the recordings exist:
// every source must still be reading the buffer and the player the plan was made
// for, and the plan rebuilt from the files that were cut must still describe the
// session. A plan that no longer holds means some other operation got there
// first, and the only honest answer is to give the new files back.
func (a *appState) revalidateSeekTxn(txn *seekTxn) error {
	current := make([]dvr.SeekSource, 0, len(txn.steps))
	for _, step := range txn.steps {
		if step.item.generation != step.generation {
			return fmt.Errorf("%s: the player was replaced while its recording was being prepared", a.name(step.item))
		}
		if a.runnerFor(step.item) != step.runner {
			return fmt.Errorf("%s: the replay buffer was replaced while its recording was being prepared", a.name(step.item))
		}
		live, err := a.replaySeekSource(step.item, txn.event)
		if err != nil {
			return err
		}
		if live.Instance != step.recording.BufferInstance || live.Epoch != step.recording.SourceMediaEpoch {
			return fmt.Errorf("%s: the buffer moved to a new instance or epoch while its recording was being prepared", a.name(step.item))
		}
		live.Recording = step.recording
		live.Range = dvr.Range{Start: step.recording.StartBufferTime, End: step.recording.EndBufferTime, Epoch: step.recording.SourceMediaEpoch}
		live.Anchor = step.anchor
		current = append(current, live)
	}
	return txn.prepared.Matches(current)
}

// commitSeekTxn makes the prepared players the items' own, and only then gives
// the previous players and leases back. The order is what keeps the picture on
// the stage: from the main-thread step on, each item draws its new player, so
// nothing a viewer sees is ever left without one.
func (a *appState) commitSeekTxn(txn *seekTxn) {
	type released struct {
		player desktopPlayer
		lease  *dvr.Lease
	}
	var previous []released
	var refusal error
	// Everything the commit depends on is checked in the same drawing-thread
	// step that installs the new resources. Checking source by source while
	// installing them would leave the first source committed when the second
	// one turns out to have moved; checking everything first and installing
	// afterwards is what makes the commit all-or-nothing.
	a.seekOnMain(func() {
		// The decision to commit and the installation it authorises happen under
		// the same lock. Ownership cannot change between "this transaction still
		// owns the session" and the step that acts on it, and the slot is given
		// up as part of the same critical section, so a transaction that lost it
		// cannot install anything or clear whoever took its place.
		a.seekMu.Lock()
		if a.seek != txn {
			a.seekMu.Unlock()
			refusal = fmt.Errorf("this replay transaction no longer owns the session")
			return
		}
		if err := a.revalidateSeekTxn(txn); err != nil {
			a.seekMu.Unlock()
			refusal = err
			return
		}
		for _, step := range txn.steps {
			it := step.item
			previous = append(previous, released{player: it.player, lease: it.replayLease})
			recording := step.recording
			anchor := step.anchor
			it.player = step.player
			it.replayLease = step.lease
			it.replay = &recording
			it.replayAnchor = &anchor
			it.replayAnchorErr = nil
			it.generation++
		}
		a.seek = nil
		a.seekMu.Unlock()
	})
	if refusal != nil {
		a.abortSeekTxn(txn, refusal.Error())
		return
	}
	for _, old := range previous {
		a.closeSeekPlayer(old.player)
		if old.lease != nil {
			if err := old.lease.Release(); err != nil {
				logf("seek", "release the previous lease: %v", err)
			}
			a.leftLease()
		}
	}
	a.clearSeek(txn)
	logf("seek", "committed %d recording(s) for event %s; each player is on the file cut for it, and the target sits %s into that file for the seek step",
		len(txn.steps), shortDuration(txn.event), shortDuration(deepestPosition(txn.prepared)))
	txn.noticeOnce.Do(func() { a.publishReport(txn.reportFor(replayCommitted, "")) })
}

// abortSeekTxn gives back every file and player the attempt took. The items keep
// the players and leases they had, because the commit is the only step that
// replaces them.
// seekShutdownWait is how long a closing session waits for the work it stopped
// to return. The work may be inside a cut, an ffprobe or a player open; it is
// bounded so a closing window does not hang on an engine call.
const seekShutdownWait = 2 * time.Second

// shutdownReplayTxn stops a transaction that is still preparing and waits for
// the work it started to return. A closing session runs this before it closes
// the buffers those files were cut from, so a player opened at the last moment
// is given back instead of outliving the session.
func (a *appState) shutdownReplayTxn() bool {
	txn := a.seekInFlight()
	if txn == nil {
		return true
	}
	a.abortSeekTxn(txn, "the session is closing")
	if !a.awaitSeekTxn(txn, seekShutdownWait) {
		logf("seek", "the replay preparation was still running %s after the session closed", seekShutdownWait)
		return false
	}
	return true
}

// awaitSeekTxn waits for the preparing goroutine to return.
func (a *appState) awaitSeekTxn(txn *seekTxn, timeout time.Duration) bool {
	select {
	case <-txn.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (a *appState) abortSeekTxn(txn *seekTxn, why string) {
	// Take whatever the transaction still owns out of its steps under the lock,
	// then close it outside: a step reaches here at most once with resources
	// attached, whether the abort comes from the transaction itself, a newer
	// transaction taking over, or the session closing.
	type owned struct {
		player desktopPlayer
		lease  *dvr.Lease
		name   string
	}
	var give []owned
	a.seekMu.Lock()
	for _, step := range txn.steps {
		if step.player == nil && step.lease == nil {
			continue
		}
		give = append(give, owned{player: step.player, lease: step.lease, name: step.recording.FileName()})
		step.player = nil
		step.lease = nil
	}
	a.seekMu.Unlock()
	for _, item := range give {
		a.closeSeekPlayer(item.player)
		if item.lease != nil {
			if err := item.lease.Release(); err != nil {
				logf("seek", "release %s: %v", item.name, err)
			}
			a.leftLease()
		}
	}
	a.clearSeek(txn)
	logf("seek", "rolled back the replay for event %s: %s", shortDuration(txn.event), why)
	txn.noticeOnce.Do(func() { a.publishReport(txn.reportFor(replayRolledBack, why)) })
}

// seekInFlight returns the transaction the window is preparing, if any.
func (a *appState) seekInFlight() *seekTxn {
	a.seekMu.Lock()
	defer a.seekMu.Unlock()
	return a.seek
}

// holdsSeek reports whether this transaction is still the one in flight.
func (a *appState) holdsSeek(txn *seekTxn) bool {
	a.seekMu.Lock()
	defer a.seekMu.Unlock()
	return a.seek == txn
}

// clearSeek drops the transaction from the window's view once it is over, and
// only if it is still the transaction the window holds: a transaction that was
// replaced must not clear the one that replaced it.
func (a *appState) clearSeek(txn *seekTxn) bool {
	a.seekMu.Lock()
	defer a.seekMu.Unlock()
	if a.seek != txn {
		return false
	}
	a.seek = nil
	return true
}

// openSeekPlayer opens the file a recording published. The test seam stands in
// for a window; production opens the file on the drawing thread.
func (a *appState) openSeekPlayer(recording dvr.Recording) (desktopPlayer, error) {
	if a.openSeek != nil {
		return a.openSeek(recording)
	}
	player, err := a.openReplayFile(recording.Path)
	if err != nil {
		return nil, err
	}
	return player, nil
}

// seekPlayerStarted reports whether a replacement player has a picture to show.
func (a *appState) seekPlayerStarted(player desktopPlayer) bool {
	if player == nil {
		return false
	}
	if a.seekStarted != nil {
		return a.seekStarted(player)
	}
	return player.Ready() && !player.Buffering()
}

// seekSettleTolerance is how far from the requested position a player may be
// and still count as having arrived. It is the same tolerance the sampling
// checks use: roughly a frame at 30fps plus the engine's own reporting step.
const seekSettleTolerance = 40 * time.Millisecond

// settleSeekTxn asks every replacement for the target position inside its own
// file and waits until each reports a position close to it. It runs before the
// commit so that a player which never arrives is rolled back with the rest of
// the transaction.
//
// What this proves is a position, not a picture: the player reports where it
// was asked to go, and the engine has no reliable way to say that the frame at
// that position is the one on screen. The transaction therefore records the
// position each player actually reported, together with what was requested and
// what is left between them, and never claims a confirmed frame.
func (a *appState) settleSeekTxn(txn *seekTxn) error {
	for _, step := range txn.steps {
		target := step.offset
		player := step.player
		a.seekOnMain(func() {
			step.wasPlaying = player.Playing()
			player.Pause()
			player.Seek(target)
		})
	}
	// A seek issued while a player is still settling can be dropped by the
	// engine; the player then keeps the position it had. Re-issuing the request
	// a few times is what makes the wait describe "never arrived" instead of
	// "the first request was ignored", and each attempt is still held to the
	// same two consecutive samples within tolerance.
	const settleAttempts = 4
	const retryAfter = 1200 * time.Millisecond
	deadline := time.Now().Add(handoverWait)
	issued := make([]time.Time, len(txn.steps))
	attempts := make([]int, len(txn.steps))
	for i := range issued {
		issued[i] = time.Now()
		attempts[i] = 1
	}
	stable := 0
	for {
		short := ""
		frames := make([]seekFrame, len(txn.steps))
		for i, step := range txn.steps {
			frames[i] = a.seekFrameOf(step.player)
			frame := frames[i]
			if frame.err != nil {
				short = fmt.Sprintf("%s reports an engine error: %v", a.name(step.item), frame.err)
				break
			}
			if !frame.ready || frame.buffering {
				short = fmt.Sprintf("%s is not ready yet", a.name(step.item))
				break
			}
			if absDuration(frame.position-step.offset) > seekSettleTolerance {
				if attempts[i] < settleAttempts && time.Since(issued[i]) >= retryAfter {
					attempts[i]++
					issued[i] = time.Now()
					logf("seek", "%s did not take the first request; asking again (%d/%d)",
						a.name(step.item), attempts[i], settleAttempts)
					a.seekOnMain(func() {
						step.player.Pause()
						step.player.Seek(step.offset)
					})
					stable = 0
				}
				short = fmt.Sprintf("%s reports %s, %s away from the %s it was asked for",
					a.name(step.item), frame.position, absDuration(frame.position-step.offset), step.offset)
				break
			}
		}
		if short == "" {
			stable++
		} else {
			stable = 0
		}
		if short == "" && stable >= 2 {
			for _, step := range txn.steps {
				if !step.wasPlaying {
					continue
				}
				player := step.player
				a.seekOnMain(func() { player.Play() })
			}
			landings := make([]seekLanding, 0, len(txn.steps))
			for i, step := range txn.steps {
				landings = append(landings, seekLanding{
					source:    step.item.source.ID,
					requested: step.offset,
					reported:  frames[i].position,
					residual:  frames[i].position - step.offset,
				})
				step.settled = frames[i].position
				step.reported = frames[i].position
				step.measured = true
			}
			a.seekMu.Lock()
			a.settled = landings
			a.seekMu.Unlock()
			for _, landing := range landings {
				logf("seek", "%s requested %s, reported %s, residual %s",
					landing.source, landing.requested, landing.reported, landing.residual)
			}
			logf("seek", "every replacement reports a position within %s of the target; that is a position, not a confirmed frame",
				seekSettleTolerance)
			return nil
		}
		if !time.Now().Before(deadline) {
			if short == "" {
				short = "the players did not hold a position"
			}
			return fmt.Errorf("did not settle on the target within %s: %s", handoverWait, short)
		}
		time.Sleep(60 * time.Millisecond)
	}
}

// seekLanding is what one replacement reported when it settled: what it was
// asked for, where it says it is, and what is left between the two. The
// residual is reported rather than hidden, because a player within the
// tolerance has not necessarily moved at all.
type seekLanding struct {
	source    core.SourceID
	requested time.Duration
	reported  time.Duration
	residual  time.Duration
}

// seekLandings returns what the last settled transaction reported.
func (a *appState) seekLandings() []seekLanding {
	a.seekMu.Lock()
	defer a.seekMu.Unlock()
	return append([]seekLanding(nil), a.settled...)
}

// seekFrame is what one player reports on the drawing thread. The error is read
// too: a player can be ready and not buffering while the engine is holding an
// error, and that is not a settled position.
type seekFrame struct {
	position  time.Duration
	ready     bool
	buffering bool
	err       error
}

// seekFrameOf reads one player's state on the drawing thread.
func (a *appState) seekFrameOf(player desktopPlayer) seekFrame {
	var frame seekFrame
	a.seekOnMain(func() {
		frame = seekFrame{
			position:  player.Position(),
			ready:     player.Ready(),
			buffering: player.Buffering(),
			err:       player.Err(),
		}
	})
	return frame
}

// waitSeekPlayers gives every replacement the same handover budget a single
// switch has, and says which one did not make it.
func (a *appState) waitSeekPlayers(txn *seekTxn) error {
	deadline := time.Now().Add(handoverWait)
	for {
		pending := ""
		for _, step := range txn.steps {
			if !a.seekPlayerStarted(step.player) {
				pending = a.name(step.item)
				break
			}
		}
		if pending == "" {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%s did not show a picture within %s", pending, handoverWait)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// seekOnMain runs a step on the drawing thread. Tests replace it with a direct
// call, because they run without a window.
func (a *appState) seekOnMain(fn func()) {
	if a.seekMain != nil {
		a.seekMain(fn)
		return
	}
	mygoRunOnMain(fn)
}

// closeSeekPlayer closes a replacement player, engine players through the
// adapter's release path and everything else directly.
func (a *appState) closeSeekPlayer(player desktopPlayer) {
	if player == nil {
		return
	}
	if video := rawPlayer(player); video != nil {
		a.releasePlayer(video)
		return
	}
	player.Close()
}

// pendingSeekPlayer is the replacement a source is preparing, if any. The stage
// draws it under the picture the source already shows: a player that is never
// drawn never produces a frame, and this transaction waits for one.
func (a *appState) pendingSeekPlayer(it *item) desktopPlayer {
	a.seekMu.Lock()
	defer a.seekMu.Unlock()
	if a.seek == nil {
		return nil
	}
	for _, step := range a.seek.steps {
		if step.item == it {
			return step.player
		}
	}
	return nil
}

// deepestPosition is how far into its file the furthest target sits. It says how
// much work the seek step has left, and makes clear that the commit alone has
// not landed the players on the target frame.
func deepestPosition(plan dvr.SeekPlan) time.Duration {
	var deepest time.Duration
	for _, hop := range plan.Hops() {
		if hop.Position > deepest {
			deepest = hop.Position
		}
	}
	return deepest
}
