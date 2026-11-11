package main

// The read-only half of P16.4b: turning the session as it stands right now into
// the state a replay seek would be decided from, and asking again at the moment
// of commitment whether that decision still holds.
//
// Nothing here opens a file, cuts a recording, creates a lease or touches a
// player. It reads the buffer's own state, the measured anchor, the recording
// that was really published, the offset the timeline holds and the player
// generation, then hands them to core's PlanSeek. The second entry point,
// confirmReplaySeek, takes the same reading again and compares it with the
// decision: an identity that changed in between makes the plan stale instead of
// being spliced into it.

import (
	"fmt"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// replaySeekSource reads one source's contribution to a decision about event.
// Every value comes from the live session: no field is defaulted, and a source
// that cannot prove where it is refuses instead of contributing a guess.
func (a *appState) replaySeekSource(it *item, event time.Duration) (dvr.SeekSource, error) {
	name := shortURI(it.uri)
	if it.replay == nil {
		// A buffer alone is not a plan: there is no file to open and no range to
		// name, and cutting one here to satisfy the contract would decide the
		// user's request by side effect.
		return dvr.SeekSource{}, fmt.Errorf("%s has no usable recording to plan around; the buffer is still live", name)
	}
	if it.replayAnchor == nil {
		if it.replayAnchorErr != nil {
			return dvr.SeekSource{}, fmt.Errorf("no measured anchor for %s: %w", name, it.replayAnchorErr)
		}
		return dvr.SeekSource{}, fmt.Errorf("no measured anchor for %s", name)
	}
	runner := a.runnerFor(it)
	if runner == nil || runner.buffer == nil {
		return dvr.SeekSource{}, fmt.Errorf("%s has no replay buffer to read", name)
	}
	track, err := seekTrack(it)
	if err != nil {
		return dvr.SeekSource{}, fmt.Errorf("%s: %w", name, err)
	}
	offset, err := a.confirmedShift(it)
	if err != nil {
		return dvr.SeekSource{}, err
	}
	basis, ok := a.sync.originOf(track)
	if !ok {
		return dvr.SeekSource{}, fmt.Errorf("%s: the offset %+.3fs has no recorded origin, so it cannot authorise a seek",
			name, offset.Seconds())
	}
	// The event and the offset are read together, so a target that a duration
	// cannot hold is refused here instead of wrapping into a negative content
	// time that looks valid.
	content, err := offsetAfter(event, offset)
	if err != nil {
		return dvr.SeekSource{}, fmt.Errorf("%s: %w", name, err)
	}
	if content < 0 {
		return dvr.SeekSource{}, fmt.Errorf("%s: event %s with offset %+.3fs is before the buffer's own clock",
			name, event.Round(time.Millisecond), offset.Seconds())
	}
	state := runner.buffer.State()
	if state.Instance == "" {
		return dvr.SeekSource{}, fmt.Errorf("%s: the buffer has no identity, so a plan cannot name it", name)
	}
	// Read the buffer once more and require the same state. The identity, the
	// epoch and the ranges are read under one lock, but the anchor and the
	// recording come from the item, so a state that moved while this snapshot
	// was being taken would splice two moments into one decision. Refusing is
	// how that becomes impossible rather than improbable.
	if before := replaySnapshotHook; before != nil {
		before()
	}
	again := runner.buffer.State()
	if again.Instance != state.Instance || again.Epoch != state.Epoch || !sameRanges(again.Ranges, state.Ranges) {
		return dvr.SeekSource{}, fmt.Errorf("%s: the buffer changed while its state was read", name)
	}
	span, ok := again.Containing(content)
	if !ok {
		return dvr.SeekSource{}, fmt.Errorf("%s: the buffer no longer holds a single range around %s",
			name, content.Round(time.Millisecond))
	}
	state = again
	return dvr.SeekSource{
		Source:           it.source.ID,
		Epoch:            state.Epoch,
		Instance:         state.Instance,
		Range:            span,
		Anchor:           *it.replayAnchor,
		Alignment:        dvr.Alignment{Offset: offset, Basis: basis},
		Recording:        *it.replay,
		PlayerGeneration: uint64(it.generation),
	}, nil
}

// replaySnapshotHook runs between the two readings of one snapshot. Tests use it
// to move the buffer and prove the adapter refuses instead of splicing two
// moments into one decision. It is nil in production.
var replaySnapshotHook func()

// sameRanges reports whether two range lists describe the same stretch of media.
func sameRanges(one, two []dvr.Range) bool {
	if len(one) != len(two) {
		return false
	}
	for index := range one {
		if one[index].Start != two[index].Start || one[index].End != two[index].End || one[index].Epoch != two[index].Epoch {
			return false
		}
	}
	return true
}

// runnerFor is the replay buffer this session opened for an item, if it opened
// one. The buffer is owned by the run rather than the item because two replays of
// the same source keep two clocks even though they name one source.
func (a *appState) runnerFor(it *item) *dvrRunner {
	if a == nil || it == nil {
		return nil
	}
	for _, tracked := range a.runners {
		if tracked.uri == it.uri {
			return tracked.runner
		}
	}
	return nil
}

// seekTrack is the track whose offset describes this source on the shared
// timeline. A player takes a source as a whole, so its picture and its sound are
// placed together and either one names the same offset.
func seekTrack(it *item) (core.TrackID, error) {
	if it.audio != "" {
		return it.audio, nil
	}
	if it.video != "" {
		return it.video, nil
	}
	return "", fmt.Errorf("the source has no track in the session, so no offset can be confirmed")
}

// planReplaySeek decides whether every listed source could be asked for one
// event time. It is a read: the sources are read once, in the order given, and
// core either produces an immutable plan or names the source that refused.
func (a *appState) planReplaySeek(event time.Duration, items []*item) (dvr.SeekPlan, error) {
	if len(items) == 0 {
		return dvr.SeekPlan{}, fmt.Errorf("%w: no source was offered", dvr.ErrNotAuthorized)
	}
	sources := make([]dvr.SeekSource, 0, len(items))
	for _, it := range items {
		source, err := a.replaySeekSource(it, event)
		if err != nil {
			return dvr.SeekPlan{}, err
		}
		sources = append(sources, source)
	}
	return dvr.PlanSeek(event, sources)
}

// confirmReplaySeek takes the reading again, at the moment a decision is about
// to be used, and asks whether the decision still describes the session. The
// reading is not merged with the earlier one: a buffer that was rebuilt, an
// epoch that moved, a player that was replaced or a recording that was published
// again makes the answer no, and the plan is left unused.
func (a *appState) confirmReplaySeek(plan dvr.SeekPlan, items []*item) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: no source was offered", dvr.ErrNotAuthorized)
	}
	sources := make([]dvr.SeekSource, 0, len(items))
	for _, it := range items {
		source, err := a.replaySeekSource(it, plan.Event())
		if err != nil {
			return fmt.Errorf("the plan no longer holds: %w", err)
		}
		sources = append(sources, source)
	}
	return plan.Matches(sources)
}
