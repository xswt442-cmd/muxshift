// The replay clock: measuring where a recording sits on its source's media
// clock, and converting a position inside it to shared event time.
//
// The buffer's clock starts at the first segment the buffer stored; the source's
// media clock is the one in the container's timestamps. The two are related by an
// anchor, and the only honest way to get an anchor is to measure one: the first
// frame of the file has both a buffer time (the recording says which) and a media
// time (ffprobe reads it out of the container). Everything else — the source id,
// the player's position, the generation — is not a measurement and must not be
// used as one.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// probeFirstMediaTime reports the media time of a file's first frame by asking
// ffprobe for the stream's start time. Video wins when the file has both, because
// that is the track whose picture a viewer compares against.
// probeFirstMediaTime is a variable so a test can hold the measurement open
// while the buffer moves on, which is exactly what the epoch check has to
// survive.
var probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, fmt.Errorf("ffprobe is not on PATH: %w", err)
	}
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-show_entries", "stream=codec_type,start_time",
		"-of", "csv=p=0",
		path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe %s: %w", shortURI(path), err)
	}
	var video, audio time.Duration
	var haveVideo, haveAudio bool
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if len(fields) != 2 {
			continue
		}
		seconds, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || seconds != seconds { // second test rejects NaN
			continue
		}
		at := time.Duration(seconds * float64(time.Second))
		switch fields[0] {
		case "video":
			if !haveVideo {
				video, haveVideo = at, true
			}
		case "audio":
			if !haveAudio {
				audio, haveAudio = at, true
			}
		}
	}
	switch {
	case haveVideo:
		return video, nil
	case haveAudio:
		return audio, nil
	}
	return 0, fmt.Errorf("%s states no stream start time", shortURI(path))
}

// measureRecordingAnchor measures the pair that ties a recording to its source's
// media clock: the file's first frame is buffer time StartBufferTime and its
// container timestamp is the media time there. The anchor names the source,
// epoch and buffer instance the recording was cut from, so a later cut, a
// reopened source or a discontinuity makes it refuse instead of drifting.
// measureRecordingAnchor measures a published recording against the content
// clock of the buffer it was cut from. The file's first frame is the recording's
// start buffer time and its container timestamp is the media time there; the
// content clock begins where the buffer stored its first segment, which the
// recording carries because the file alone cannot state it.
func measureRecordingAnchor(recording dvr.Recording) (dvr.Anchor, error) {
	return measureRecordingAnchorIn(context.Background(), recording)
}

// measureRecordingAnchorIn measures the same pair under a caller's lifetime, so
// a transaction that is being stopped can cancel a probe that is still running
// instead of making the session wait for it.
func measureRecordingAnchorIn(parent context.Context, recording dvr.Recording) (dvr.Anchor, error) {
	if err := recording.Valid(); err != nil {
		return dvr.Anchor{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	media, err := probeFirstMediaTime(ctx, recording.Path)
	if err != nil {
		return dvr.Anchor{}, err
	}
	return dvr.Anchor{
		Source:         recording.Source,
		Epoch:          recording.SourceMediaEpoch,
		BufferInstance: recording.BufferInstance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     media,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}, nil
}

// anchorAttempt is one measurement, named by the point it measured. Two
// attempts are the same work only when they measure the same segment of the same
// clock: after a discontinuity the buffer's current point moves, and a pair
// measured at the old point no longer describes the clock the buffer is on.
type anchorAttempt struct {
	point  dvr.ClockPoint
	done   chan struct{}
	anchor *dvr.Anchor
	err    error
	tries  int
}

// finished reports whether the measurement has put down its result.
func (a *anchorAttempt) finished() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

// samePoint reports whether two measurements are of the same work: the same
// segment of the same clock.
func samePoint(a, b dvr.ClockPoint) bool {
	return a.Epoch == b.Epoch && a.Segment.Start == b.Segment.Start
}

// covers reports whether a measurement at measured describes the buffer's clock
// from current onwards. It is the same segment normally; after a segment proved
// unreadable the runner measures a later one, whose buffer start still anchors
// every later position (and leaves earlier ones unprovable).
func covers(measured, current dvr.ClockPoint) bool {
	return measured.Epoch == current.Epoch && measured.Segment.Start >= current.Segment.Start
}

// maxAnchorTries bounds how often one point is measured before the runner gives
// up on that segment and measures the newest one the buffer holds instead. The
// replacement anchor names the newer segment's own buffer start, so windows
// before it are refused rather than extrapolated.
const maxAnchorTries = 2

// refreshAnchor starts measuring the buffer's current point when nothing is
// measuring it yet, and retries a measurement that failed once the buffer has
// stored another segment. It returns immediately: the measurement runs in the
// background so ffprobe never makes a drawing pass wait.
func (r *dvrRunner) refreshAnchor() {
	point, err := r.buffer.CurrentSegment()
	if err != nil {
		return
	}
	newest, err := r.buffer.NewestSegment()
	if err != nil {
		return
	}
	r.anchorMu.Lock()
	defer r.anchorMu.Unlock()

	target := point
	tries := 1
	if attempt := r.attempt; attempt != nil && covers(attempt.point, point) {
		if !attempt.finished() {
			return
		}
		// A measurement that succeeded stands: a growing range does not make it
		// stale. Only a failure is retried, and only once the buffer has stored a
		// segment after the one that could not be read — retrying on every poll
		// would hammer ffprobe for no new information.
		if attempt.err == nil || newest.Segment.Start <= attempt.point.Segment.Start {
			return
		}
		tries = attempt.tries + 1
		if tries > maxAnchorTries {
			// This segment cannot be read. Measure what the buffer has now; the
			// anchor carries that segment's own buffer start, so anything before
			// it stays unprovable.
			target = newest
			tries = 1
		}
	}

	attempt := &anchorAttempt{point: target, done: make(chan struct{}), tries: tries}
	r.attempt = attempt
	go func() {
		defer close(attempt.done)
		attempt.anchor, attempt.err = r.measureAnchor(target)
	}()
}

// currentAttempt returns the measurement of the buffer's current point when one
// has finished. It never waits; a caller on the drawing thread asks for what is
// ready and comes back after the next segment.
func (r *dvrRunner) currentAttempt() (*anchorAttempt, error) {
	point, err := r.buffer.CurrentSegment()
	if err != nil {
		return nil, err
	}
	r.anchorMu.Lock()
	attempt := r.attempt
	r.anchorMu.Unlock()
	if attempt == nil || !covers(attempt.point, point) {
		r.refreshAnchor()
		return nil, fmt.Errorf("the buffer has not measured its clock yet; the next segment starts the measurement")
	}
	select {
	case <-attempt.done:
		return attempt, nil
	default:
		return nil, fmt.Errorf("the buffer is still measuring its clock; try again in a moment")
	}
}

// waitAnchor blocks until the buffer's current point has been measured. It is
// for callers that are not the drawing thread — scripted checks and tests — and
// gives up when ctx is done.
func (r *dvrRunner) waitAnchor(ctx context.Context) (*dvr.Anchor, error) {
	for {
		r.refreshAnchor()
		if attempt, err := r.currentAttempt(); err == nil {
			return attempt.anchor, attempt.err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// measureAnchor reads the point the buffer is on and measures the pair that ties
// the buffer's clock to the source's media clock there.
//
// The segment and the clock identity were taken under one lock, before the
// measurement runs: ffprobe can take a second, and a discontinuity arriving in
// the meantime would otherwise have the anchor name an epoch that its segment
// does not belong to.
func (r *dvrRunner) measureAnchor(point dvr.ClockPoint) (*dvr.Anchor, error) {
	first := point.Segment
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	media, err := probeFirstMediaTime(ctx, first.Path)
	if err != nil {
		return nil, err
	}
	return &dvr.Anchor{
		Source:         r.source,
		Epoch:          point.Epoch,
		BufferInstance: point.BufferInstance,
		BufferStart:    first.Start,
		MediaStart:     media,
		Interval:       dvr.Range{Start: first.Start, End: first.End},
		MeasuredAt:     time.Now(),
	}, nil
}

// bufferAnchor returns the buffer's measured anchor, extended to cover the
// window a caller wants to convert. Within one epoch buffer time and media time
// move together, so the interval of the measured segment may be widened; a
// discontinuity moves the epoch, and a window that starts before the measured
// segment cannot be explained by that measurement at all, so both are refused
// rather than extrapolated.
//
// It does not wait for the measurement. A caller on the drawing thread gets an
// error saying the clock is not measured yet and asks again later; a caller that
// can afford to wait uses waitAnchor first.
func (r *dvrRunner) bufferAnchor(window dvr.Range) (dvr.Anchor, error) {
	attempt, err := r.currentAttempt()
	if err != nil {
		return dvr.Anchor{}, err
	}
	if attempt.err != nil {
		return dvr.Anchor{}, attempt.err
	}
	anchor := *attempt.anchor
	if epoch := r.buffer.Epoch(); anchor.Epoch != epoch {
		return dvr.Anchor{}, fmt.Errorf("the buffer moved to epoch %d after the anchor was measured in epoch %d",
			epoch, anchor.Epoch)
	}
	if window.Start < anchor.Interval.Start {
		return dvr.Anchor{}, fmt.Errorf("the window starts at %s, before the measured segment at %s",
			window.Start, anchor.Interval.Start)
	}
	// The anchor says which buffer time the media clock was measured at, not
	// that the buffer holds one unbroken stretch from there onwards. A skipped
	// segment inside the same epoch ends the range without moving the epoch, so
	// extending the interval over it would claim media the buffer never stored.
	if _, ok := r.buffer.RangeContaining(anchor.Interval.Start, window.End); !ok {
		return dvr.Anchor{}, fmt.Errorf("the window %s..%s is not in the same continuous range as the segment measured at %s; a gap or a skipped segment separates them",
			window.Start, window.End, anchor.Interval.Start)
	}
	if window.End > anchor.Interval.End {
		anchor.Interval.End = window.End
	}
	return anchor, nil
}

// waitBufferAnchor waits for the measurement and then converts the window, which
// is what a caller that is not the drawing thread wants: scripted checks, tests
// and the probe route. It repeats the conversion because the buffer's point can
// move while the measurement runs.
func (r *dvrRunner) waitBufferAnchor(ctx context.Context, window dvr.Range) (dvr.Anchor, error) {
	for {
		if _, err := r.waitAnchor(ctx); err != nil {
			return dvr.Anchor{}, err
		}
		anchor, err := r.bufferAnchor(window)
		if err == nil {
			return anchor, nil
		}
		if ctx.Err() != nil {
			return dvr.Anchor{}, err
		}
		select {
		case <-ctx.Done():
			return dvr.Anchor{}, err
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// mediaClock converts between the two clocks a replay file has. The buffer
// records container timestamps; a player's position starts at zero on the first
// frame of the file it opened, so seeking to a position means "container time
// minus the first frame's container time". They are not interchangeable: a file
// whose timestamps were all shifted keeps the same content at an earlier player
// position, and that shift must not become a track offset.
type mediaClock struct {
	origin time.Duration // the file's first frame, in container time
}

// Player reports the position that shows the given container time.
func (c mediaClock) Player(container time.Duration) time.Duration {
	return container - c.origin
}

// Container reports the container time shown at a player position.
func (c mediaClock) Container(player time.Duration) time.Duration {
	return player + c.origin
}

// clockFor takes the conversion from an anchor measured for the file a player
// holds: the anchor's media start is that file's first frame.
func clockFor(anchor dvr.Anchor) mediaClock {
	return mediaClock{origin: anchor.MediaStart}
}

// playerWindow reports the player positions that show a buffered window. Two
// steps sit behind the single subtraction: the buffer's clock reaches the
// container's through the anchor (`buffer = anchor.BufferStart + (container -
// anchor.MediaStart)`), and a player that opens a file reports zero at its first
// frame (`position = buffer - opened`). A file cut at the buffer start therefore
// opens at `anchor.BufferStart`, and the container origin cancels out -- which is
// the point: a timestamp shift shared by every frame must not survive into the
// player clock, let alone into a track offset.
func playerWindow(anchor dvr.Anchor, window dvr.Range) dvr.Range {
	start := window.Start - anchor.BufferStart
	return dvr.Range{Start: start, End: start + (window.End - window.Start)}
}

// replayFile is the contract between a player and the clock it runs on. Three
// facts have to agree before a position means anything: which file the player
// actually opened, which source instance that file came from, and where the
// file's own zero sits in the container clock. They are kept in one place
// because carrying them separately invites the mistake this work has to rule
// out -- a position read from one file, converted with an anchor measured for
// another, and then presented as a shared event time.
type replayFile struct {
	file     string // the recording's file name, for logs and errors
	source   core.SourceID
	instance string // the buffer that produced the file
	epoch    uint64
	clock    mediaClock
}

// replayFileFor binds a recording to the anchor measured for it, refusing any
// mismatch instead of converting with a plausible-looking value. An anchor from
// another source, another buffer instance, another epoch, or one measured for a
// different part of the buffer cannot locate this file's first frame.
func replayFileFor(recording dvr.Recording, anchor dvr.Anchor) (replayFile, error) {
	if !anchor.Measured() {
		return replayFile{}, fmt.Errorf("the anchor for %s was not measured", recording.FileName())
	}
	if anchor.Source != recording.Source {
		return replayFile{}, fmt.Errorf("the anchor names %s, but the file came from %s",
			anchor.Source, recording.Source)
	}
	if anchor.BufferInstance != recording.BufferInstance {
		return replayFile{}, fmt.Errorf("the anchor was measured on buffer %s, but the file came from buffer %s",
			bufferName(anchor.BufferInstance), bufferName(recording.BufferInstance))
	}
	if anchor.Epoch != recording.SourceMediaEpoch {
		return replayFile{}, fmt.Errorf("the anchor was measured in epoch %d, but the file was cut in epoch %d",
			anchor.Epoch, recording.SourceMediaEpoch)
	}
	if anchor.BufferStart != recording.StartBufferTime {
		return replayFile{}, fmt.Errorf("the anchor starts at buffer %s, but the file starts at buffer %s",
			anchor.BufferStart, recording.StartBufferTime)
	}
	return replayFile{
		file:     recording.FileName(),
		source:   recording.Source,
		instance: recording.BufferInstance,
		epoch:    recording.SourceMediaEpoch,
		clock:    clockFor(anchor),
	}, nil
}

// bufferName keeps an empty instance from disappearing into a sentence: a
// recording cut before this field existed still has to name what it lacks.
func bufferName(instance string) string {
	if instance == "" {
		return "(no instance recorded)"
	}
	return instance
}

// replayEventTime converts a position inside the recording an item is showing to
// shared event time. It refuses when the item shows no recording, when the anchor
// was never measured, when the source has no track in the session, or when that
// track has no confirmed offset in the Core timeline: a zero offset that was
// never confirmed is not the same thing as a measured zero.
func (a *appState) replayEventTime(it *item, position time.Duration) (time.Duration, error) {
	offset, err := a.confirmedShift(it)
	if err != nil {
		return 0, err
	}
	// The player's position is content, not container: its zero is the file's
	// first frame. Reaching shared event time means the file's position becomes
	// buffer time (the recording's start plus the position), buffer time becomes
	// content time (minus the buffer's origin), and only then does the confirmed
	// offset apply. Going through the container instead would carry each file's
	// mux delay into the event clock: two files of the same content sit four
	// seconds apart there, and every shared window would inherit the difference.
	file, err := replayFileFor(*it.replay, *it.replayAnchor)
	if err != nil {
		return 0, err
	}
	buffer := it.replay.StartBufferTime + position
	content, err := it.replayAnchor.ContentTime(buffer)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", file.file, err)
	}
	event, err := eventFromMedia(content, offset)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", file.file, err)
	}
	return event, nil
}

// confirmedShift is the one offset every replay conversion uses. A track with no
// confirmed offset is refused rather than treated as zero, and the offset always
// applies to content time -- never to the container's own timestamps.
func (a *appState) confirmedShift(it *item) (time.Duration, error) {
	if it.replay == nil {
		return 0, fmt.Errorf("%s is not showing a recording", shortURI(it.uri))
	}
	if it.replayAnchor == nil {
		if it.replayAnchorErr != nil {
			return 0, fmt.Errorf("no measured anchor for %s: %w", shortURI(it.uri), it.replayAnchorErr)
		}
		return 0, fmt.Errorf("no measured anchor for %s", shortURI(it.uri))
	}
	track := it.audio
	if track == "" {
		track = it.video
	}
	if track == "" {
		return 0, fmt.Errorf("%s has no track in the session, so no offset can be confirmed", shortURI(it.uri))
	}
	shift, ok := a.sync.timeline.Shift(track)
	if !ok {
		return 0, fmt.Errorf("%s: %w", track, core.ErrNotAligned)
	}
	return shift.Offset, nil
}

// replayPosition maps a shared event time back to a position inside the open
// recording, the reverse of replayEventTime. It says where the file would have
// to be asked to go; it does not say that the frame there is on screen, which is
// a separate state the player has to be measured for.
func (a *appState) replayPosition(it *item, event time.Duration) (time.Duration, error) {
	offset, err := a.confirmedShift(it)
	if err != nil {
		return 0, err
	}
	file, err := replayFileFor(*it.replay, *it.replayAnchor)
	if err != nil {
		return 0, err
	}
	content := event + offset
	position := content - it.replay.StartBufferTime
	length := it.replay.EndBufferTime - it.replay.StartBufferTime
	if position < 0 || position >= length {
		return 0, fmt.Errorf("%s: %s is at %s into the recording, which runs %s",
			file.file, event.Round(time.Millisecond), position.Round(time.Millisecond), length.Round(time.Millisecond))
	}
	return position, nil
}
