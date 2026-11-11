package main

import (
	"context"
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

func TestReplayFileRefusesAnAnchorForAnotherFile(t *testing.T) {
	recording := dvr.Recording{
		Source:           "fixture",
		SourceMediaEpoch: 2,
		BufferInstance:   "buf-1",
		StartBufferTime:  5 * time.Second,
		EndBufferTime:    9 * time.Second,
	}
	anchor := dvr.Anchor{
		Source:         "fixture",
		Epoch:          2,
		BufferInstance: "buf-1",
		BufferStart:    5 * time.Second,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: 5 * time.Second, End: 9 * time.Second},
		MeasuredAt:     time.Now(),
	}
	file, err := replayFileFor(recording, anchor)
	if err != nil {
		t.Fatalf("binding the anchor to its own file: %v", err)
	}
	if file.epoch != 2 || file.instance != "buf-1" || file.clock.Player(100*time.Second) != 0 {
		t.Fatalf("the contract lost what it was given: %+v", file)
	}

	cases := []struct {
		name      string
		recording dvr.Recording
		anchor    dvr.Anchor
		want      string
	}{
		{"another source", recording, func() dvr.Anchor { a := anchor; a.Source = "other"; return a }(), "names other"},
		{"another buffer", recording, func() dvr.Anchor { a := anchor; a.BufferInstance = "buf-2"; return a }(), "buf-2"},
		{"another epoch", recording, func() dvr.Anchor { a := anchor; a.Epoch = 3; return a }(), "epoch 3"},
		{"another cut point", recording, func() dvr.Anchor { a := anchor; a.BufferStart = 6 * time.Second; return a }(), "starts at buffer"},
		{"never measured", recording, func() dvr.Anchor { a := anchor; a.MeasuredAt = time.Time{}; return a }(), "was not measured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := replayFileFor(tc.recording, tc.anchor); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestBufferAnchorWidensOnlyForward(t *testing.T) {
	buffer, _ := fixtureBuffer(t)
	stubProbe(t, 100*time.Second, nil)
	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := runner.waitAnchor(ctx); err != nil {
		t.Fatalf("waiting for the measurement: %v", err)
	}

	// A window starting at the measured segment is explained, and the interval
	// widens to cover it: buffer time moves with media time within one epoch.
	got, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 12 * time.Second})
	if err != nil {
		t.Fatalf("a window at the measured segment was refused: %v", err)
	}
	if got.Interval.End != 12*time.Second {
		t.Fatalf("the returned interval ends at %s, not at the window's %s", got.Interval.End, 12*time.Second)
	}

	// A window that starts before it cannot be blamed on the measurement.
	if _, err := runner.bufferAnchor(dvr.Range{Start: -time.Second, End: 9 * time.Second}); err == nil ||
		!strings.Contains(err.Error(), "before the measured segment") {
		t.Fatalf("a window before the measured segment reports %v, want a refusal", err)
	}
}

func TestMediaClockKeepsAPtsShiftOutOfThePlayerClock(t *testing.T) {
	// The two files hold the same frames; only their container timestamps differ,
	// which is what the d-3.0 fixture did. The same content therefore has to land
	// on the same player position in both, and the shift must stay in the
	// conversion instead of turning into a track offset.
	first := dvr.Anchor{Source: "a", MeasuredAt: time.Now(), MediaStart: 1466667 * time.Microsecond}
	second := dvr.Anchor{Source: "b", MeasuredAt: time.Now(), MediaStart: 5466667 * time.Microsecond}
	content := 6 * time.Second

	if got, want := clockFor(first).Player(first.MediaStart+content), content; got != want {
		t.Fatalf("the unshifted file shows content %s at player %s, want %s", content, got, want)
	}
	if got, want := clockFor(second).Player(second.MediaStart+content), content; got != want {
		t.Fatalf("the shifted file shows content %s at player %s, want %s", content, got, want)
	}
	shift := second.MediaStart - first.MediaStart
	if shift != 4*time.Second {
		t.Fatalf("the fixture's shift is %s, want 4s", shift)
	}
	if got := clockFor(second).Player(second.MediaStart+content) - clockFor(first).Player(first.MediaStart+content); got != 0 {
		t.Fatalf("the same content sits %s apart on the two player clocks, want 0", got)
	}
	if got, want := clockFor(first).Container(content), first.MediaStart+content; got != want {
		t.Fatalf("container time for player %s is %s, want %s", content, got, want)
	}
	// The buffer window is the clock a player is handed: both sources buffered
	// 0s..30s, and a player opening a file cut at the buffer start reports 0s
	// there, so the same content lands on the same positions even though the
	// container origins differ by the fixture's shift.
	window := playerWindow(second, dvr.Range{Start: 0, End: 30 * time.Second})
	if window.Start != 0 || window.End != 30*time.Second {
		t.Fatalf("the converted window is %s..%s, want 0s..30s", window.Start, window.End)
	}
	shifted := playerWindow(second, dvr.Range{Start: second.BufferStart + 5*time.Second, End: second.BufferStart + 15*time.Second})
	if shifted.Start != 5*time.Second || shifted.End != 15*time.Second {
		t.Fatalf("a window cut later converts to %s..%s, want 5s..15s", shifted.Start, shifted.End)
	}
}
