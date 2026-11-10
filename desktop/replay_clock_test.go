package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// fixtureRecording is a file the repository tracks, described the way the buffer
// describes a cut: which source, epoch and instance it came from, and where its
// first frame sits on the buffer's clock.
func fixtureRecording(t *testing.T) dvr.Recording {
	t.Helper()
	path := filepath.Join("..", "core", "hls", "testdata", "vod", "seg0.ts")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the test fixture is gone: %v", err)
	}
	return dvr.Recording{
		Path:             path,
		Size:             info.Size(),
		Source:           "fixture",
		SourceMediaEpoch: 3,
		BufferInstance:   "buf-fixture",
		StartBufferTime:  5 * time.Second,
		EndBufferTime:    9 * time.Second,
	}
}

func TestMeasureRecordingAnchorReadsTheFilesFirstFrame(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed, so the first frame cannot be measured")
	}
	recording := fixtureRecording(t)
	anchor, err := measureRecordingAnchor(recording)
	if err != nil {
		t.Fatalf("measuring %s: %v", recording.FileName(), err)
	}
	if !anchor.Measured() {
		t.Fatal("a measured anchor says it was not measured")
	}
	if anchor.Source != recording.Source || anchor.Epoch != recording.SourceMediaEpoch || anchor.BufferInstance != recording.BufferInstance {
		t.Fatalf("the anchor names %s epoch %d instance %q, not the recording's %s epoch %d instance %q",
			anchor.Source, anchor.Epoch, anchor.BufferInstance,
			recording.Source, recording.SourceMediaEpoch, recording.BufferInstance)
	}
	if anchor.BufferStart != recording.StartBufferTime {
		t.Fatalf("the anchor puts buffer time %s at the file's first frame, not %s",
			anchor.BufferStart, recording.StartBufferTime)
	}
	if anchor.Interval.Start != recording.StartBufferTime || anchor.Interval.End != recording.EndBufferTime {
		t.Fatalf("the anchor explains %s, not the recording's %s",
			logRange(anchor.Interval), logRange(dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime}))
	}
	// The measured pair must reproduce itself: the first frame the recording
	// starts at converts to exactly the media time the container states.
	at, err := recording.SourceMediaTime(0, anchor)
	if err != nil {
		t.Fatalf("converting the measured position: %v", err)
	}
	if at != anchor.MediaStart {
		t.Fatalf("the measured start converts to %s, not back to the measured %s", at, anchor.MediaStart)
	}
}

func TestReplayEventTimeRefusesWhatItCannotProve(t *testing.T) {
	recording := dvr.Recording{
		Source:           "fixture",
		SourceMediaEpoch: 1,
		BufferInstance:   "buf-fixture",
		StartBufferTime:  5 * time.Second,
		EndBufferTime:    9 * time.Second,
	}
	anchor := dvr.Anchor{
		Source:         "fixture",
		Epoch:          1,
		BufferInstance: "buf-fixture",
		BufferStart:    5 * time.Second,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: 5 * time.Second, End: 9 * time.Second},
		MeasuredAt:     time.Now(),
	}

	cases := []struct {
		name  string
		it    item
		track core.TrackID
		want  string
	}{
		{
			name: "no recording",
			it:   item{uri: "http://example.invalid/live.m3u8"},
			want: "not showing a recording",
		},
		{
			name: "anchor never measured",
			it:   item{uri: "http://example.invalid/live.m3u8", replay: &recording, replayAnchorErr: errors.New("ffprobe is not on PATH")},
			want: "no measured anchor",
		},
		{
			name: "no track",
			it:   item{uri: "http://example.invalid/live.m3u8", replay: &recording, replayAnchor: &anchor},
			want: "has no track in the session",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &appState{sync: newSyncState()}
			_, err := a.replayEventTime(&tc.it, 0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}

	// A track that exists but has no confirmed offset is refused, not treated as
	// an offset of zero.
	it := item{uri: "http://example.invalid/live.m3u8", replay: &recording, replayAnchor: &anchor, video: "track-1"}
	a := &appState{sync: newSyncState()}
	if _, err := a.replayEventTime(&it, time.Second); !errors.Is(err, core.ErrNotAligned) {
		t.Fatalf("an unaligned track gave %v, want ErrNotAligned", err)
	}

	// With a confirmed offset the conversion runs through the measured anchor and
	// Core's sign: event = media - offset.
	if err := a.sync.timeline.SetShift("track-1", 400*time.Millisecond); err != nil {
		t.Fatalf("aligning the track: %v", err)
	}
	event, err := a.replayEventTime(&it, time.Second)
	if err != nil {
		t.Fatalf("converting a measured position: %v", err)
	}
	// position 1s inside a recording that starts at buffer 5s is media 101s;
	// minus the 400ms offset that is 100.6s.
	if want := 100*time.Second + 600*time.Millisecond; event != want {
		t.Fatalf("position 1s converted to event %s, want %s", event, want)
	}
}

func TestBufferAnchorRefusesAWindowBeforeTheMeasuredSegment(t *testing.T) {
	anchor := dvr.Anchor{
		Source:         "fixture",
		Epoch:          1,
		BufferInstance: "buf-fixture",
		BufferStart:    5 * time.Second,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: 5 * time.Second, End: 9 * time.Second},
		MeasuredAt:     time.Now(),
	}
	buffer, err := dvr.New(dvr.Options{Dir: t.TempDir(), Source: "fixture", SourceEpoch: 1})
	if err != nil {
		t.Fatalf("starting a buffer for the runner: %v", err)
	}
	t.Cleanup(func() { buffer.Close() })
	runner := &dvrRunner{buffer: buffer}
	// Mark the one-shot as spent: this test supplies the measurement instead of
	// running ffprobe against a buffer it never filled.
	runner.anchorOnce.Do(func() {})
	runner.anchor = &anchor
	runner.anchorDone = make(chan struct{})
	close(runner.anchorDone)

	// A window starting at the measured segment is explained, and the interval
	// widens to cover it: buffer time moves with media time within one epoch.
	got, err := runner.bufferAnchor(dvr.Range{Start: 5 * time.Second, End: 12 * time.Second})
	if err != nil {
		t.Fatalf("a window at the measured segment was refused: %v", err)
	}
	if got.Interval.End != 12*time.Second {
		t.Fatalf("the returned interval ends at %s, not at the window's %s", got.Interval.End, 12*time.Second)
	}

	// A window that starts before it cannot be blamed on the measurement.
	if _, err := runner.bufferAnchor(dvr.Range{Start: 2 * time.Second, End: 9 * time.Second}); err == nil {
		t.Fatal("a window before the measured segment was accepted")
	}
}
