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
func probeFirstMediaTime(ctx context.Context, path string) (time.Duration, error) {
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
func measureRecordingAnchor(recording dvr.Recording) (dvr.Anchor, error) {
	if err := recording.Valid(); err != nil {
		return dvr.Anchor{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

// startAnchorMeasurement measures the buffer's anchor once, in the background,
// as soon as the buffer holds a segment. Doing it early keeps the measurement
// off the drawing thread: a caller that needs the anchor then finds it ready
// instead of waiting for ffprobe.
func (r *dvrRunner) startAnchorMeasurement() {
	r.anchorOnce.Do(func() {
		r.anchorDone = make(chan struct{})
		go func() {
			defer close(r.anchorDone)
			r.anchor, r.anchorErr = r.measureAnchor()
		}()
	})
}

// measureAnchor reads the buffer's first stored segment and measures the pair
// that ties the buffer's clock to the source's media clock there.
func (r *dvrRunner) measureAnchor() (*dvr.Anchor, error) {
	ranges := r.buffer.Ranges()
	if len(ranges) == 0 || len(ranges[0].Segments) == 0 {
		return nil, fmt.Errorf("the buffer holds no segment to measure")
	}
	first := ranges[0].Segments[0]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	media, err := probeFirstMediaTime(ctx, first.Path)
	if err != nil {
		return nil, err
	}
	return &dvr.Anchor{
		Source:         r.source,
		Epoch:          r.buffer.Epoch(),
		BufferInstance: r.buffer.Instance(),
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
func (r *dvrRunner) bufferAnchor(window dvr.Range) (dvr.Anchor, error) {
	r.startAnchorMeasurement()
	<-r.anchorDone
	if r.anchorErr != nil {
		return dvr.Anchor{}, r.anchorErr
	}
	anchor := *r.anchor
	if epoch := r.buffer.Epoch(); anchor.Epoch != epoch {
		return dvr.Anchor{}, fmt.Errorf("the buffer moved to epoch %d after the anchor was measured in epoch %d",
			epoch, anchor.Epoch)
	}
	if window.Start < anchor.Interval.Start {
		return dvr.Anchor{}, fmt.Errorf("the window starts at %s, before the measured segment at %s",
			window.Start, anchor.Interval.Start)
	}
	if window.End > anchor.Interval.End {
		anchor.Interval.End = window.End
	}
	return anchor, nil
}

// replayEventTime converts a position inside the recording an item is showing to
// shared event time. It refuses when the item shows no recording, when the anchor
// was never measured, when the source has no track in the session, or when that
// track has no confirmed offset in the Core timeline: a zero offset that was
// never confirmed is not the same thing as a measured zero.
func (a *appState) replayEventTime(it *item, position time.Duration) (time.Duration, error) {
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
	return it.replay.EventTime(position, *it.replayAnchor, shift.Offset)
}
