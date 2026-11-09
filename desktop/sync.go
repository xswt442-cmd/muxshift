// The sync panel: one shared event clock, one offset per track, and the
// commands that move the real players to the targets those offsets give.
//
// The sign convention is Core's, not a second one invented here:
//
//	targetMediaTime = sharedEventTime + mappedOffset
//
// A positive offset asks a track for media that belongs after the shared event,
// so its content is late at that event time and following the event means
// holding it back. A negative offset asks for media before the event, so the
// track runs ahead and is pulled back. Every message prints the offset, the
// target it produced and where the player actually landed, so the same number
// is never read as "delay" in one place and "advance" in another.
package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// syncState is the shared event clock and the per-track offsets that map it to
// media time.
type syncState struct {
	timeline *core.Timeline
	playhead *core.Playhead
	last     time.Time
}

// newSyncState starts paused at the origin with no track aligned, which is what
// Core's zero values mean.
func newSyncState() *syncState {
	return &syncState{timeline: core.NewTimeline(), playhead: core.NewPlayhead()}
}

// tick moves the shared event clock by the wall time that has passed since the
// previous tick. A paused timeline keeps its event where it is: Core's Advance
// ignores elapsed time while paused. It runs on the main thread, from the
// ticker main starts.
func (s *syncState) tick(now time.Time) error {
	if s.last.IsZero() {
		s.last = now
		return nil
	}
	elapsed := now.Sub(s.last)
	s.last = now
	if elapsed <= 0 {
		return nil
	}
	return s.playhead.Advance(elapsed)
}

// group is the sources whose tracks the scene currently selects: the picture it
// shows and the audio it hears. A synchronised command goes to these, and the
// other sources keep doing whatever they were doing.
func (a *appState) group() []*item {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return nil
	}
	selected := make(map[core.TrackID]bool, len(scene.Video)+len(scene.Audio))
	for _, track := range scene.Video {
		selected[track] = true
	}
	for _, level := range scene.Audio {
		selected[level.Track] = true
	}
	out := make([]*item, 0, len(a.items))
	for _, it := range a.items {
		if it.player == nil {
			continue
		}
		if selected[it.video] || selected[it.audio] {
			out = append(out, it)
		}
	}
	return out
}

// name labels a source in group messages so a failure names the source it came
// from instead of only the error text.
func (a *appState) name(it *item) string {
	for i, other := range a.items {
		if other == it {
			return fmt.Sprintf("source %d (%s)", i+1, shortURI(it.uri))
		}
	}
	return "source"
}

// pauseAll stops the shared clock and every player of the group. It returns the
// members that could not be told, so a half-pause is reported rather than
// looking like a working one.
func (a *appState) pauseAll() []string {
	a.sync.playhead.Pause()
	problems := make([]string, 0, 1)
	for _, it := range a.group() {
		if it.player == nil {
			problems = append(problems, a.name(it)+" has no player")
			continue
		}
		it.player.Pause()
		if err := it.player.Err(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", a.name(it), err))
		}
	}
	return problems
}

// playAll starts the shared clock and every player of the group, reporting the
// members that failed the same way pauseAll does.
func (a *appState) playAll() []string {
	a.sync.playhead.Play()
	problems := make([]string, 0, 1)
	for _, it := range a.group() {
		if it.player == nil {
			problems = append(problems, a.name(it)+" has no player")
			continue
		}
		it.player.Play()
		if err := it.player.Err(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", a.name(it), err))
		}
	}
	return problems
}

// anchor adopts a player's current media position as the shared event time.
// Manual alignment starts from where the reference player actually is, not from
// an assumed clock, and doing it again after a seek cannot make the target drift
// away from the picture.
func (a *appState) anchor(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.player == nil {
		return fmt.Errorf("source %d: no player to anchor on", index+1)
	}
	if !it.player.Ready() {
		return fmt.Errorf("source %d: not ready, so its position is not a usable anchor", index+1)
	}
	if err := it.player.Err(); err != nil {
		return fmt.Errorf("source %d: %w", index+1, err)
	}
	position := it.player.Position()
	if err := a.sync.playhead.Seek(position); err != nil {
		return err
	}
	// The anchored source is the reference, so its tracks are on the timeline by
	// definition; without this its own sound would count as unverified.
	for _, track := range []core.TrackID{it.video, it.audio} {
		if track == "" {
			continue
		}
		if err := a.sync.timeline.SetShift(track, 0); err != nil {
			return err
		}
	}
	a.apply()
	return nil
}

// aligned reports whether a source has a known place on the shared timeline.
// A source that has never been anchored and never had an offset set is where a
// spoiler comes from, so it is treated as unverified rather than assumed equal.
func (a *appState) aligned(it *item) bool {
	track := it.audio
	if track == "" {
		track = it.video
	}
	if track == "" || a.sync == nil || a.sync.timeline == nil {
		return false
	}
	return a.sync.timeline.Aligned(track)
}

// allowAudio makes one source audible even though its place on the shared
// timeline is unknown. It is the visible, reversible way out of the conservative
// mute, and calling it again puts the mute back.
func (a *appState) allowAudio(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.audio == "" {
		return fmt.Errorf("source %d has no audio track", index+1)
	}
	if a.audible[it.audio] {
		delete(a.audible, it.audio)
		a.syncNote = fmt.Sprintf("source %d muted again: its offset is still unknown", index+1)
	} else {
		a.audible[it.audio] = true
		a.syncNote = fmt.Sprintf("source %d heard without a known offset", index+1)
	}
	a.apply()
	return nil
}

// nudge moves one source's offset by delta and seeks its player to the media
// time the new offset gives.
//
// The offset moves from what the timeline already holds, so two presses of a
// tenth of a second move it by two tenths whatever the player did in between.
// The seek is refused when the target lies before the start of the stream: that
// is the case where the player has no history to go back to, and pretending
// otherwise by slowing the other source down would move one of them off the
// event.
func (a *appState) nudge(index int, delta time.Duration) (string, error) {
	it, err := a.at(index)
	if err != nil {
		return "", err
	}
	if it.player == nil {
		return "", fmt.Errorf("source %d: no player", index+1)
	}
	if !it.player.Ready() {
		return "", fmt.Errorf("source %d: not ready, so there is nothing to seek yet", index+1)
	}
	if err := it.player.Err(); err != nil {
		return "", fmt.Errorf("source %d: %w", index+1, err)
	}
	track := it.audio
	if track == "" {
		track = it.video
	}
	if track == "" {
		return "", fmt.Errorf("source %d: no track to place on the timeline", index+1)
	}

	// A player takes a source as a whole, so the picture and the sound of one
	// source move together. Keeping their offsets equal is what makes the
	// target of either one describe the same content.
	current, _ := a.sync.timeline.Shift(track)
	projected, err := offsetAfter(current.Offset, delta)
	if err != nil {
		return "", fmt.Errorf("source %d: %w", index+1, err)
	}
	target, err := addDuration(a.sync.playhead.Event(), projected)
	if err != nil {
		return "", fmt.Errorf("source %d: %w", index+1, err)
	}
	if target < 0 {
		return "", fmt.Errorf("source %d: offset %+.1fs puts the target at %s, before the start of the stream; this player has no history that far back",
			index+1, projected.Seconds(), target)
	}

	// The offset is only stored once the player accepted the seek's target, so
	// a refused command leaves the timeline where it was.
	shift, err := a.sync.timeline.AdjustShift(track, delta)
	if err != nil {
		return "", err
	}
	for _, other := range []core.TrackID{it.video, it.audio} {
		if other == "" || other == track {
			continue
		}
		if _, err := a.sync.timeline.AdjustShift(other, delta); err != nil {
			return "", err
		}
	}

	before := it.player.Position()
	it.player.Seek(target)
	// The source now has a known offset, so the conservative mute can lift.
	a.apply()
	after := it.player.Position()
	return fmt.Sprintf("source %d offset=%+.3fs target=%s before=%s after=%s residual=%s",
		index+1, shift.Offset.Seconds(), target.Round(time.Millisecond), before.Round(time.Millisecond),
		after.Round(time.Millisecond), (after - target).Round(time.Millisecond)), nil
}

// offsetAfter adds delta to an offset, reporting a sum a duration cannot hold.
func offsetAfter(current, delta time.Duration) (time.Duration, error) {
	next := current + delta
	if (delta > 0 && next < current) || (delta < 0 && next > current) {
		return 0, fmt.Errorf("%w: offset overflow", core.ErrTimeRange)
	}
	return next, nil
}

// addDuration adds two durations, reporting a sum a duration cannot hold.
func addDuration(a, b time.Duration) (time.Duration, error) {
	next := a + b
	if (b > 0 && next < a) || (b < 0 && next > a) {
		return 0, fmt.Errorf("%w: time overflow", core.ErrTimeRange)
	}
	return next, nil
}

// nudgeAndRecord applies a nudge from a button and keeps the outcome visible in
// the window and the log.
func (a *appState) nudgeAndRecord(index int, delta time.Duration) {
	report, err := a.nudge(index, delta)
	if err != nil {
		a.syncNote = "sync: " + err.Error()
		log.Printf("nudge source %d: %v", index+1, err)
		return
	}
	a.syncNote = "sync: " + report
	log.Printf("nudge source %d: %s", index+1, report)
}

// syncLabel is the shared clock and every offset as one line, for the window.
func (a *appState) syncLabel() string {
	state := "paused"
	if a.sync.playhead.Playing() {
		state = "playing"
	}
	event := a.sync.playhead.Event()
	shifts := a.sync.timeline.Shifts()
	if len(shifts) == 0 {
		return fmt.Sprintf("event %s (%s) · sync 0", event.Round(time.Millisecond), state)
	}
	parts := make([]string, 0, len(shifts))
	for _, shift := range shifts {
		when := "content " + shift.Offset.Abs().Round(100*time.Millisecond).String() + " ahead"
		if shift.Offset < 0 {
			when = "content " + shift.Offset.Abs().Round(100*time.Millisecond).String() + " behind"
		} else if shift.Offset == 0 {
			when = "on the event"
		}
		parts = append(parts, fmt.Sprintf("%s %s", shift.Track, when))
	}
	return fmt.Sprintf("event %s (%s) · %s", event.Round(time.Millisecond), state, strings.Join(parts, " "))
}
