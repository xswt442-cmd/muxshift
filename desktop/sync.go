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
	// group is the Core sync group the pause and play commands act on. It is
	// set the first time a synchronised command runs and is not derived from
	// the scene again, so changing what is shown cannot change who pauses.
	group core.SyncGroupID
}

// newSyncState starts paused at the origin with no track aligned, which is what
// Core's zero values mean.
func newSyncState() *syncState {
	return &syncState{timeline: core.NewTimeline(), playhead: core.NewPlayhead()}
}

// replaySource is one source's replay buffer, paired with the index of its item
// so a window can be reported back in the same terms the window shows.
type replaySource struct {
	index  int
	runner *dvrRunner
}

// commonReplay is the part of the buffered history that every listed source can
// play after its own offset moves it onto the shared event clock. The answer is
// an intersection, not the union: a window only one source holds is not a moment
// both can show. An unaligned source, or one whose buffered range has not
// reached the others, makes an honest answer impossible, so the error says which
// source refused instead of guessing a number.
func (a *appState) commonReplay(sources []replaySource) (time.Duration, time.Duration, error) {
	if len(sources) == 0 {
		return 0, 0, fmt.Errorf("no buffered source")
	}
	var start, end time.Duration
	first := true
	for _, source := range sources {
		it, err := a.at(source.index)
		if err != nil {
			return 0, 0, err
		}
		track := it.audio
		if track == "" {
			track = it.video
		}
		if track == "" {
			return 0, 0, fmt.Errorf("source %d (%s) has no track in the session", source.index+1, shortURI(it.uri))
		}
		shift, ok := a.sync.timeline.Shift(track)
		if !ok {
			return 0, 0, fmt.Errorf("source %d (%s): %w", source.index+1, shortURI(it.uri), core.ErrNotAligned)
		}
		window, ok := source.runner.buffer.Available()
		if !ok {
			return 0, 0, fmt.Errorf("source %d (%s) has no playable buffered range", source.index+1, shortURI(it.uri))
		}
		// media = event + offset, so the same range on the event clock is
		// media - offset.
		eventStart := window.Start - shift.Offset
		eventEnd := window.End - shift.Offset
		if first {
			start, end = eventStart, eventEnd
			first = false
			continue
		}
		if eventStart > start {
			start = eventStart
		}
		if eventEnd < end {
			end = eventEnd
		}
	}
	if end <= start {
		return 0, 0, fmt.Errorf("the buffered ranges do not overlap on shared event time: %s..%s", start, end)
	}
	return start, end, nil
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

// group is the sources that pause and play together: the tracks of the one Core
// sync group this session uses, not whatever the scene happens to select. The
// scene decides what is shown and heard; a scene that later shows a second,
// unrelated match must not make one pause stop both.
func (a *appState) group() ([]*item, error) {
	members, err := a.syncMembers()
	if err != nil {
		return nil, err
	}
	inGroup := make(map[core.TrackID]bool, len(members.Tracks))
	for _, track := range members.Tracks {
		inGroup[track] = true
	}
	out := make([]*item, 0, len(a.items))
	for _, it := range a.items {
		if it.player == nil {
			continue
		}
		if inGroup[it.video] || inGroup[it.audio] {
			out = append(out, it)
		}
	}
	return out, nil
}

// syncMembers returns the session's sync group, creating it from the tracks the
// scene selects the first time a synchronised command runs. Membership is fixed
// at that point, which is what keeps a later scene change from silently pulling
// unrelated sources into the same pause.
func (a *appState) syncMembers() (core.SyncGroup, error) {
	if a.sync.group != "" {
		for _, group := range a.session.SyncGroups() {
			if group.ID == a.sync.group {
				return group, nil
			}
		}
		// Core drops a group whose last track left with its source. Build a
		// new one from what is left rather than acting on a stale id.
		a.sync.group = ""
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return core.SyncGroup{}, err
	}
	var reference core.TrackID
	tracks := make([]core.TrackID, 0, 2*len(a.items))
	add := func(track core.TrackID) {
		if track == "" || containsTrack(tracks, track) {
			return
		}
		tracks = append(tracks, track)
		if reference == "" {
			reference = track
		}
	}
	for _, it := range a.items {
		if it.player == nil {
			continue
		}
		heard := false
		for _, level := range scene.Audio {
			if level.Track == it.audio {
				heard = true
				break
			}
		}
		if !heard && !containsTrack(scene.Video, it.video) {
			continue
		}
		add(it.video)
		add(it.audio)
	}
	if len(tracks) == 0 {
		return core.SyncGroup{}, fmt.Errorf("no source in the scene to synchronize")
	}
	group, err := a.session.AddSyncGroup(reference, tracks...)
	if err != nil {
		return core.SyncGroup{}, err
	}
	a.sync.group = group.ID
	log.Printf("sync: created %s with %d tracks", group.ID, len(group.Tracks))
	return group, nil
}

// existingGroup reports the session sync group without creating one. The
// alignment probe reads through this: showing a suggestion must not change the
// session, and building the group belongs to the first sync command instead.
func (a *appState) existingGroup() (core.SyncGroup, bool) {
	if a.sync.group == "" {
		return core.SyncGroup{}, false
	}
	for _, group := range a.session.SyncGroups() {
		if group.ID == a.sync.group {
			return group, true
		}
	}
	return core.SyncGroup{}, false
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
	members, err := a.group()
	if err != nil {
		return []string{err.Error()}
	}
	problems := make([]string, 0, 1)
	for _, it := range members {
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
	members, err := a.group()
	if err != nil {
		return []string{err.Error()}
	}
	problems := make([]string, 0, 1)
	for _, it := range members {
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
	// Anchoring is the start of manual alignment, so it is also where the one
	// sync group of this session comes into being.
	if _, err := a.syncMembers(); err != nil {
		return fmt.Errorf("sync group: %w", err)
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
	if _, err := a.syncMembers(); err != nil {
		return "", fmt.Errorf("sync group: %w", err)
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
	group := "group none"
	if a.sync.group != "" {
		for _, candidate := range a.session.SyncGroups() {
			if candidate.ID == a.sync.group {
				group = fmt.Sprintf("group %s (%d tracks)", candidate.ID, len(candidate.Tracks))
				break
			}
		}
	}
	shifts := a.sync.timeline.Shifts()
	if len(shifts) == 0 {
		return fmt.Sprintf("event %s (%s) · %s · sync 0", event.Round(time.Millisecond), state, group)
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
	return fmt.Sprintf("event %s (%s) · %s · %s", event.Round(time.Millisecond), state, group, strings.Join(parts, " "))
}
