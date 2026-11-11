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
	offset  time.Duration
	settled time.Duration

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

	steps []*seekTxnStep
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
func (a *appState) startSeekTxn(items []*item, event time.Duration) error {
	if len(items) == 0 {
		return fmt.Errorf("no source was offered for the replay seek")
	}
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
	txn := &seekTxn{event: event, plan: plan}
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
	go a.prepareSeekTxn(txn)
	return nil
}

// prepareSeekTxn cuts one recording per source, opens a player on each, and
// commits only once every player has a picture and the authorisation still
// describes the session. Every exit either commits or gives back what it took.
func (a *appState) prepareSeekTxn(txn *seekTxn) {
	for _, step := range txn.steps {
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
		if err := a.revalidateSeekTxn(txn); err != nil {
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
	a.clearSeek()
	logf("seek", "committed %d recording(s) for event %s; each player is on the file cut for it, and the target sits %s into that file for the seek step",
		len(txn.steps), shortDuration(txn.event), shortDuration(deepestPosition(txn.prepared)))
}

// abortSeekTxn gives back every file and player the attempt took. The items keep
// the players and leases they had, because the commit is the only step that
// replaces them.
func (a *appState) abortSeekTxn(txn *seekTxn, why string) {
	for _, step := range txn.steps {
		finished := false
		step.finished.Do(func() {
			finished = true
			a.closeSeekPlayer(step.player)
			if step.lease != nil {
				if err := step.lease.Release(); err != nil {
					logf("seek", "release %s: %v", step.recording.FileName(), err)
				}
				a.leftLease()
			}
		})
		_ = finished
	}
	a.clearSeek()
	logf("seek", "rolled back the replay for event %s: %s", shortDuration(txn.event), why)
}

// clearSeek drops the transaction from the window's view once it is over.
func (a *appState) clearSeek() {
	a.seekMu.Lock()
	a.seek = nil
	a.seekMu.Unlock()
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
	deadline := time.Now().Add(handoverWait)
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
