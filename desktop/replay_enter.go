package main

// P16.4f-a: the read-only half of entering replay.
//
// A replay seek starts from a stage that is already replaying: each source has
// a recording, a lease, a measured anchor and a confirmed offset, and
// PlanSeek decides where inside those files a target sits. Entering replay
// starts from the live stage, where none of that exists yet, so the question is
// different: may this target be reached from every source's buffer at all, and
// if so, what exactly would each source have to cut?
//
// This file answers that question and nothing else. It cuts no recording, opens
// no player, takes no lease and touches no item. What it does produce is an
// entryPlan: the target, and for every source the identity and the evidence the
// decision was based on. The transaction that later prepares the files re-reads
// the same fields and refuses when any of them moved, so "the plan was valid
// when it was made" can never stand in for "the plan is still valid".
//
// The identities are the ones the rest of the replay path already uses -- the
// source, the track, the player generation, the buffer instance and epoch, the
// measured anchor, and the offset together with the buffer clock it was
// measured against. Nothing here invents a second notion of time.

import (
	"context"
	"fmt"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// entrySourceState is what one source contributes to a decision about entering
// replay: the identities, the evidence, and the continuous buffer window that
// evidence explains. It is a snapshot, not a reference: the plan keeps its own
// copy so a later change to the session cannot rewrite what was decided.
type entrySourceState struct {
	Source     core.SourceID
	Name       string
	Track      core.TrackID
	Generation int

	// Instance and Epoch name the buffer the plan is about. An offset or an
	// anchor measured against another buffer says nothing about this one.
	Instance string
	Epoch    uint64

	// Window is the one continuous buffer range the anchor explains. A gap
	// between ranges is a clock the plan may not cross, even though the buffer
	// holds segments on both sides of it.
	Window dvr.Range

	Anchor dvr.Anchor
	Offset time.Duration

	// OriginInstance and OriginEpoch are the buffer clock the offset was
	// established against. A placement made against a buffer that no longer
	// exists, or before its epoch moved, cannot authorise entering replay.
	OriginInstance string
	OriginEpoch    uint64
	// OriginBasis is how the offset was established. The transaction hands it
	// back to the replay contract, which accepts only the three named origins;
	// an offset whose basis was never stated cannot be used to place a target.
	OriginBasis dvr.Basis
}

// entryStep is one source's share of an entry plan: the identities that were
// decided from, the target, and where the transaction would cut.
type entryStep struct {
	entrySourceState

	// Event is the shared event time the source would enter at, Media the
	// container media time that content carries, Cut the content time the
	// recording is asked to start at. A cut begins at the segment that holds
	// that time, so the file's own zero can sit slightly before the cut; the
	// transaction measures the real offset from the rebuilt plan rather than
	// trusting InFile.
	Event  time.Duration
	Media  time.Duration
	Cut    time.Duration
	InFile time.Duration
}

// entryPlan is an immutable decision. It is made once, read by the transaction
// that prepares the work, and compared again against the live session before
// anything is committed.
type entryPlan struct {
	Event    time.Duration
	Explicit bool
	Steps    []entryStep
}

// replayKind names the operation an account describes. A seek moves inside a
// stage that is already replaying; an entry changes the stage from live to
// replay. They share the transaction machinery and the numbering, but a failure
// must not be reported as the other kind of request.
type replayKind string

const (
	replayKindSeek  replayKind = "seek"
	replayKindEnter replayKind = "enter"
)

// enterable returns the event-time interval this state can be entered at, and
// refuses when the evidence is not there. The end is exclusive.
func (s entrySourceState) enterable() (time.Duration, time.Duration, error) {
	if s.Instance == "" {
		return 0, 0, fmt.Errorf("%s: the buffer has no identity, so it cannot enter replay", s.Name)
	}
	if s.OriginInstance == "" || s.OriginInstance != s.Instance || s.OriginEpoch != s.Epoch {
		return 0, 0, fmt.Errorf("%s: the offset %+.3fs was measured against another buffer, so it cannot enter replay",
			s.Name, s.Offset.Seconds())
	}
	if !s.Anchor.Measured() {
		return 0, 0, fmt.Errorf("%s: no measured anchor, so its clock cannot place an event time", s.Name)
	}
	if s.Anchor.BufferInstance != s.Instance || s.Anchor.Epoch != s.Epoch {
		return 0, 0, fmt.Errorf("%s: the anchor was measured in another buffer or epoch, so it cannot place an event time", s.Name)
	}
	if s.Window.Epoch != s.Epoch {
		return 0, 0, fmt.Errorf("%s: the continuous range belongs to epoch %d, not to the buffer's epoch %d",
			s.Name, s.Window.Epoch, s.Epoch)
	}
	if s.Window.End <= s.Window.Start {
		return 0, 0, fmt.Errorf("%s: the buffer has no continuous range to enter from", s.Name)
	}
	if s.Anchor.Interval.Start < s.Window.Start {
		return 0, 0, fmt.Errorf("%s: the anchor starts at %s, before the buffer's own range at %s",
			s.Name, shortDuration(s.Anchor.Interval.Start), shortDuration(s.Window.Start))
	}
	// The buffer's range is in content time, which is the timeline the event
	// clock is offset from: an entry at event e lands at content e+offset. The
	// anchor's media times are container timestamps and place no event.
	return s.Window.Start - s.Offset, s.Window.End - s.Offset, nil
}

// mediaAt converts a content time inside this state's continuous window into the
// container media time the files carry. The anchor explains the window; anything
// before it is refused rather than extended backwards. It is used as evidence
// only: the cut is made at content time, not at container time.
func (s entrySourceState) mediaAt(buffer time.Duration) (time.Duration, error) {
	if buffer < s.Window.Start || buffer >= s.Window.End {
		return 0, fmt.Errorf("%s: %s is outside the buffer range %s..%s",
			s.Name, shortDuration(buffer), shortDuration(s.Window.Start), shortDuration(s.Window.End))
	}
	if buffer < s.Anchor.Interval.Start {
		return 0, fmt.Errorf("%s: %s is before the anchor's own range at %s",
			s.Name, shortDuration(buffer), shortDuration(s.Anchor.Interval.Start))
	}
	widened := s.Anchor
	widened.Interval.End = s.Window.End
	return widened.MediaTime(buffer)
}

// cutAt is the content time a recording would start at to enter at event, and
// the container media time that content carries. The cut itself is content time:
// content = event + offset, which is the same mapping the replay contract uses.
// Deriving the cut from the anchor's container times instead would cut at a
// timestamp the buffer never called a position.
func (s entrySourceState) cutAt(event time.Duration) (time.Duration, time.Duration, error) {
	content, err := offsetAfter(event, s.Offset)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", s.Name, err)
	}
	if content < s.Window.Start || content >= s.Window.End {
		return 0, 0, fmt.Errorf("%s: the event %s converts to content %s, outside the buffer range %s..%s",
			s.Name, shortDuration(event), shortDuration(content), shortDuration(s.Window.Start), shortDuration(s.Window.End))
	}
	if content < s.Anchor.Interval.Start {
		return 0, 0, fmt.Errorf("%s: the event %s converts to content %s, before the anchor's own range at %s",
			s.Name, shortDuration(event), shortDuration(content), shortDuration(s.Anchor.Interval.Start))
	}
	media, err := s.mediaAt(content)
	if err != nil {
		return 0, 0, err
	}
	return media, content, nil
}

// planEntry decides whether event can be entered from these sources. With
// explicit false the target is chosen: the first whole second at or after the
// latest window start in event time, as long as it still lies before every
// window's end. The rounding is only what makes the default deterministic and
// explainable; it does not keep the target away from a cut boundary and it
// promises nothing about a player settling there.
func planEntry(sources []entrySourceState, event time.Duration, explicit bool) (*entryPlan, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("no source was offered for entering replay")
	}
	type span struct{ start, end time.Duration }
	spans := make([]span, 0, len(sources))
	latestStart := time.Duration(0)
	earliestEnd := time.Duration(0)
	for index, source := range sources {
		start, end, err := source.enterable()
		if err != nil {
			return nil, err
		}
		spans = append(spans, span{start: start, end: end})
		if index == 0 || start > latestStart {
			latestStart = start
		}
		if index == 0 || end < earliestEnd {
			earliestEnd = end
		}
	}
	if !explicit {
		event = latestStart
		if remainder := event % time.Second; remainder != 0 {
			event += time.Second - remainder
		}
		if event >= earliestEnd {
			return nil, fmt.Errorf("the sources have no shared instant to enter replay: the latest start is %s and the earliest end is %s",
				shortDuration(latestStart), shortDuration(earliestEnd))
		}
	}
	plan := &entryPlan{Event: event, Explicit: explicit}
	for index, source := range sources {
		if event < spans[index].start || event >= spans[index].end {
			return nil, fmt.Errorf("%s cannot enter replay at %s: its window is %s..%s",
				source.Name, shortDuration(event), shortDuration(spans[index].start), shortDuration(spans[index].end))
		}
		media, cut, err := source.cutAt(event)
		if err != nil {
			return nil, err
		}
		plan.Steps = append(plan.Steps, entryStep{
			entrySourceState: source,
			Event:            event,
			Media:            media,
			Cut:              cut,
		})
	}
	return plan, nil
}

// recheckEntry asks the live session again, now that the plan may have been
// prepared for a while. Every field the plan decided from is compared: an
// identity that moved turns the plan into a refusal instead of a commit. It
// takes fresh snapshots so the caller can decide what "fresh" means.
func recheckEntry(plan *entryPlan, fresh []entrySourceState) error {
	if plan == nil {
		return fmt.Errorf("there is no entry plan to confirm")
	}
	if len(fresh) != len(plan.Steps) {
		return fmt.Errorf("the session has %d sources but the plan was made for %d", len(fresh), len(plan.Steps))
	}
	for index, step := range plan.Steps {
		now := fresh[index]
		if now.Source != step.Source {
			return fmt.Errorf("the plan was made for %s but the session now has %s", step.Source, now.Source)
		}
		if now.Generation != step.Generation {
			return fmt.Errorf("%s: the player was replaced after the entry was planned", step.Name)
		}
		if now.Instance != step.Instance || now.Epoch != step.Epoch {
			return fmt.Errorf("%s: the buffer changed to another instance or epoch after the entry was planned", step.Name)
		}
		if now.Anchor.BufferStart != step.Anchor.BufferStart ||
			now.Anchor.MediaStart != step.Anchor.MediaStart ||
			!now.Anchor.MeasuredAt.Equal(step.Anchor.MeasuredAt) {
			return fmt.Errorf("%s: the clock was measured again after the entry was planned", step.Name)
		}
		if now.Offset != step.Offset || now.OriginInstance != step.OriginInstance || now.OriginEpoch != step.OriginEpoch {
			return fmt.Errorf("%s: the offset or the buffer it was measured against changed after the entry was planned", step.Name)
		}
		if now.OriginBasis != step.OriginBasis {
			return fmt.Errorf("%s: the origin of its offset changed after the entry was planned", step.Name)
		}
		if now.Window.Start != step.Window.Start || now.Window.End != step.Window.End {
			return fmt.Errorf("%s: the buffer range changed after the entry was planned", step.Name)
		}
		start, end, err := now.enterable()
		if err != nil {
			return err
		}
		if step.Event < start || step.Event >= end {
			return fmt.Errorf("%s: the target is no longer inside the buffer's own range", step.Name)
		}
	}
	return nil
}

// entrySources reads the live session into the snapshots planEntry decides
// from. Nothing here opens a file or changes an item: every value comes from
// the buffer's own state, the current measurement, the timeline and the
// recorded origin of its offset.
func (a *appState) entrySources(items []*item) ([]entrySourceState, error) {
	out := make([]entrySourceState, 0, len(items))
	for _, it := range items {
		name := a.name(it)
		if it.replay != nil {
			return nil, fmt.Errorf("%s is already showing a recording; it is not on the live stage", name)
		}
		runner := a.runnerFor(it)
		if runner == nil || runner.buffer == nil {
			return nil, fmt.Errorf("%s is not buffering, so it cannot enter replay", name)
		}
		track, err := seekTrack(it)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		shift, ok := a.sync.timeline.Shift(track)
		if !ok {
			return nil, fmt.Errorf("%s: %w", track, core.ErrNotAligned)
		}
		origin, ok := a.sync.originOf(track)
		if !ok {
			return nil, fmt.Errorf("%s: the offset %+.3fs has no recorded origin, so it cannot authorise an entry",
				name, shift.Offset.Seconds())
		}
		state := runner.buffer.State()
		attempt, err := runner.currentAttempt()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if attempt.err != nil {
			return nil, fmt.Errorf("%s: no measured anchor: %w", name, attempt.err)
		}
		anchor := *attempt.anchor
		window, ok := state.Containing(anchor.Interval.Start)
		if !ok {
			return nil, fmt.Errorf("%s: the measured segment is no longer in the buffer; try again in a moment", name)
		}
		out = append(out, entrySourceState{
			Source:         it.source.ID,
			Name:           name,
			Track:          track,
			Generation:     it.generation,
			Instance:       state.Instance,
			Epoch:          state.Epoch,
			Window:         window,
			Anchor:         anchor,
			Offset:         shift.Offset,
			OriginInstance: origin.instance,
			OriginEpoch:    origin.epoch,
			OriginBasis:    origin.basis,
		})
	}
	return out, nil
}

// planReplayEntry decides whether the live stage can enter replay at event. It
// is a read; the plan it returns names every source's evidence and where the
// transaction would cut, and nothing is prepared by making it.
func (a *appState) planReplayEntry(items []*item, event time.Duration, explicit bool) (*entryPlan, error) {
	sources, err := a.entrySources(items)
	if err != nil {
		return nil, err
	}
	return planEntry(sources, event, explicit)
}

// recheckReplayEntry asks the session again, now that a plan may have been
// prepared. It takes the same reading planReplayEntry took and compares the two,
// so a source that moved between planning and committing is refused rather
// than installed.
func (a *appState) recheckReplayEntry(plan *entryPlan) error {
	if plan == nil {
		return fmt.Errorf("there is no entry plan to confirm")
	}
	items := make([]*item, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		it, _, err := a.itemBySource(step.Source)
		if err != nil {
			return err
		}
		items = append(items, it)
	}
	fresh, err := a.entrySources(items)
	if err != nil {
		return err
	}
	return recheckEntry(plan, fresh)
}

// startEnterReplayTxn turns a live stage into one that replays the same moment
// at every source. It plans from the live state first, keeps that plan as the
// evidence the commit has to confirm again, and then hands the work to the same
// preparation, settlement and commit steps the seek path uses: the stage stays
// live and visible until one drawing-thread step installs every replacement.
// A request that never becomes a transaction still gets its own account, and
// that account says it was an entry rather than a seek.
func (a *appState) startEnterReplayTxn(items []*item, event time.Duration, explicit bool) (err error) {
	defer func() {
		if err != nil {
			a.publishReport(replayReport{ID: replaySeq.Add(1), Event: event, Kind: replayKindEnter, Phase: replayRefused, Reason: err.Error()})
		}
	}()
	if len(items) == 0 {
		return fmt.Errorf("no source was offered for entering replay")
	}
	a.seekStart.Lock()
	defer a.seekStart.Unlock()

	a.seekMu.Lock()
	busy := a.seek != nil
	a.seekMu.Unlock()
	if busy {
		return fmt.Errorf("a replay is already being prepared; wait for it to finish")
	}

	plan, err := a.planReplayEntry(items, event, explicit)
	if err != nil {
		return err
	}
	txn := &seekTxn{event: plan.Event, kind: replayKindEnter, entry: plan, done: make(chan struct{}), id: replaySeq.Add(1)}
	txn.ctx, txn.cancel = context.WithCancel(context.Background())
	for _, step := range plan.Steps {
		it, _, err := a.itemBySource(step.Source)
		if err != nil {
			txn.cancel()
			return err
		}
		runner := a.runnerFor(it)
		if runner == nil {
			txn.cancel()
			return fmt.Errorf("%s has no replay buffer to cut from", a.name(it))
		}
		txn.steps = append(txn.steps, &seekTxnStep{
			item:       it,
			runner:     runner,
			generation: it.generation,
			content:    step.Cut,
			alignment:  dvr.Alignment{Offset: step.Offset, Basis: step.OriginBasis},
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
