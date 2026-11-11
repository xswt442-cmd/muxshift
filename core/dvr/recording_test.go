package dvr

import (
	"context"
	"errors"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// recBuffer stores the fixture's two segments and returns the buffer.
func recBuffer(t *testing.T, source core.SourceID) (*Buffer, *httptest.Server, hls.Info) {
	t.Helper()
	server := vodServer(t)
	buffer, err := New(Options{Dir: t.TempDir(), Source: source})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	info := readPlaylist(t, server.URL+"/vod/index.m3u8")
	for _, segment := range info.Segments {
		if _, err := buffer.Add(context.Background(), server.Client(), segment); err != nil {
			t.Fatalf("add segment %d: %v", segment.Sequence, err)
		}
	}
	t.Cleanup(func() { _ = buffer.Close() })
	return buffer, server, info
}

func TestRecordSeparatesTheRequestFromTheFileStart(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-7")

	// 3s falls inside the second segment, which covers 2s..4s.
	recording, err := buffer.Record(3 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if recording.RequestedBufferTime != 3*time.Second {
		t.Fatalf("requested = %s, want 3s", recording.RequestedBufferTime)
	}
	if recording.StartBufferTime != 2*time.Second {
		t.Fatalf("file start = %s, want 2s", recording.StartBufferTime)
	}
	if recording.EndBufferTime != 4*time.Second {
		t.Fatalf("file end = %s, want 4s", recording.EndBufferTime)
	}
	if recording.StartBufferTime == recording.RequestedBufferTime {
		t.Fatalf("the file start equals the request, which is the mistake this type exists to prevent")
	}
	if recording.FirstSequence != 1 || recording.LastSequence != 1 {
		t.Fatalf("sequences = %d..%d, want 1..1", recording.FirstSequence, recording.LastSequence)
	}
	if recording.Source != "source-7" {
		t.Fatalf("source = %q, want source-7", recording.Source)
	}
	if recording.Size != int64(len(readFile(t, recording.Path))) {
		t.Fatalf("size = %d, want the file size", recording.Size)
	}
	if got := recording.Duration(); got != 2*time.Second {
		t.Fatalf("duration = %s, want 2s", got)
	}
	if err := recording.Valid(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if !strings.HasSuffix(recording.FileName(), ".ts") || recording.ID+".ts" != recording.FileName() {
		t.Fatalf("file name %q does not carry the id %q", recording.FileName(), recording.ID)
	}
}

func TestRecordAtASegmentBoundaryStartsThere(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if recording.StartBufferTime != 2*time.Second || recording.RequestedBufferTime != 2*time.Second {
		t.Fatalf("start %s and request %s, want both 2s",
			recording.StartBufferTime, recording.RequestedBufferTime)
	}
	if recording.EndBufferTime != 4*time.Second {
		t.Fatalf("file end = %s, want 4s", recording.EndBufferTime)
	}
}

func TestRecordingMapsPositionsBothWays(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(3 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// Invariant: BufferTime = RecordingPosition + StartBufferTime.
	for _, position := range []time.Duration{0, 250 * time.Millisecond, time.Second, 2 * time.Second} {
		at, err := recording.BufferTime(position)
		if err != nil {
			t.Fatalf("buffer time at %s: %v", position, err)
		}
		if want := recording.StartBufferTime + position; at != want {
			t.Fatalf("buffer time at %s = %s, want %s", position, at, want)
		}
		back, err := recording.Position(at)
		if err != nil {
			t.Fatalf("position of %s: %v", at, err)
		}
		if back != position {
			t.Fatalf("position of %s = %s, want %s", at, back, position)
		}
	}

	// The requested time is inside the file, at a known position.
	offset, err := recording.Position(recording.RequestedBufferTime)
	if err != nil {
		t.Fatalf("position of the request: %v", err)
	}
	if offset != time.Second {
		t.Fatalf("the request sits at %s in the file, want 1s", offset)
	}

	if _, err := recording.BufferTime(2*time.Second + time.Millisecond); !errors.Is(err, ErrNoRange) {
		t.Fatalf("buffer time past the end = %v, want ErrNoRange", err)
	}
	if _, err := recording.Position(time.Second); !errors.Is(err, ErrNoRange) {
		t.Fatalf("position before the file = %v, want ErrNoRange", err)
	}
}

func TestSourceMediaTimeNeedsAMeasuredAnchor(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-2")
	recording, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// Without an anchor the buffer clock is not the source clock, so the answer
	// is refused rather than assumed to start at zero.
	if _, err := recording.SourceMediaTime(0, Anchor{}); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("no anchor = %v, want ErrNoAnchor", err)
	}
	// An anchor measured on another source does not describe this one.
	other := anchorFor(recording, 2*time.Second, 100*time.Second)
	other.Source = "source-9"
	if _, err := recording.SourceMediaTime(0, other); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("foreign anchor = %v, want ErrNoAnchor", err)
	}
	// A constructed anchor states the mapping without measuring it.
	guessed := anchorFor(recording, 2*time.Second, 100*time.Second)
	guessed.MeasuredAt = time.Time{}
	if _, err := recording.SourceMediaTime(0, guessed); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("unmeasured anchor = %v, want ErrNoAnchor", err)
	}
	// An anchor from another buffer instance, even for the same source, is a
	// different clock.
	elsewhere := anchorFor(recording, 2*time.Second, 100*time.Second)
	elsewhere.BufferInstance = "buf-elsewhere"
	if _, err := recording.SourceMediaTime(0, elsewhere); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("anchor from another buffer = %v, want ErrNoAnchor", err)
	}
	// An anchor from another epoch of the same source was measured before the
	// media clock jumped.
	jumped := anchorFor(recording, 2*time.Second, 100*time.Second)
	jumped.Epoch = recording.SourceMediaEpoch + 1
	if _, err := recording.SourceMediaTime(0, jumped); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("anchor from another epoch = %v, want ErrNoAnchor", err)
	}
	// The anchor only explains the contiguous interval it was measured in.
	outside := anchorFor(recording, 2*time.Second, 100*time.Second)
	outside.Interval = Range{Start: recording.EndBufferTime, End: recording.EndBufferTime + 10*time.Second}
	if _, err := recording.SourceMediaTime(0, outside); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("anchor outside its interval = %v, want ErrNoAnchor", err)
	}

	// A measured anchor: buffer 2s was the source's 100s.
	anchor := anchorFor(recording, 2*time.Second, 100*time.Second)
	for _, test := range []struct {
		position time.Duration
		want     time.Duration
	}{
		{0, 100 * time.Second},
		{time.Second, 101 * time.Second},
		{2 * time.Second, 102 * time.Second},
	} {
		got, err := recording.SourceMediaTime(test.position, anchor)
		if err != nil {
			t.Fatalf("media time at %s: %v", test.position, err)
		}
		if got != test.want {
			t.Fatalf("media time at %s = %s, want %s", test.position, got, test.want)
		}
	}
}

func TestEventTimeFollowsTheCoreTimelineSign(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-1")
	recording, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	anchor := anchorFor(recording, 2*time.Second, 3*time.Second)

	// The file starts at 2s of buffer time and buffer time is content time, so
	// position 0 is content 2s; the anchor's container timestamp (3s) is a
	// calibration for opening the file, not where the frame sits on the
	// timeline. Core fixes media = event + offset, so event = content - offset.
	for _, test := range []struct {
		offset time.Duration
		want   time.Duration
	}{
		{0, 2 * time.Second},
		{500 * time.Millisecond, 1500 * time.Millisecond},
		{-2 * time.Second, 4 * time.Second},
	} {
		got, err := recording.EventTime(0, anchor, test.offset)
		if err != nil {
			t.Fatalf("event time with offset %s: %v", test.offset, err)
		}
		if got != test.want {
			t.Fatalf("event time with offset %s = %s, want %s", test.offset, got, test.want)
		}
	}
	// The container mapping stays available and still answers in its own domain:
	// the same position opens the file at the measured media time.
	if media, err := recording.SourceMediaTime(0, anchor); err != nil || media != anchor.MediaStart {
		t.Fatalf("the container mapping gave %s (err %v), want the measured %s", media, err, anchor.MediaStart)
	}
}

func TestRecordRefusesATimeInsideAGap(t *testing.T) {
	server := vodServer(t)
	buffer, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	t.Cleanup(func() { _ = buffer.Close() })
	info := readPlaylist(t, server.URL+"/vod/index.m3u8")
	if _, err := buffer.Add(context.Background(), server.Client(), info.Segments[0]); err != nil {
		t.Fatalf("add first segment: %v", err)
	}
	skipped := info.Segments[1]
	skipped.Sequence = 2
	skipped.ProgramDateTime = skipped.ProgramDateTime.Add(2 * time.Second)
	skipped.URI = server.URL + "/vod/seg1.ts"
	if _, err := buffer.Add(context.Background(), server.Client(), skipped); err != nil {
		t.Fatalf("add later segment: %v", err)
	}

	if _, err := buffer.Record(3 * time.Second); !errors.Is(err, ErrNoRange) {
		t.Fatalf("record inside the gap = %v, want ErrNoRange", err)
	}
	after, err := buffer.Record(4*time.Second + 500*time.Millisecond)
	if err != nil {
		t.Fatalf("record after the gap: %v", err)
	}
	if after.StartBufferTime != 4*time.Second || after.EndBufferTime != 6*time.Second {
		t.Fatalf("file after the gap = %s..%s, want 4s..6s", after.StartBufferTime, after.EndBufferTime)
	}
	if len(after.Gaps) != 1 || len(after.Ranges) != 2 {
		t.Fatalf("the recording does not describe the gap: %d gaps, %d ranges", len(after.Gaps), len(after.Ranges))
	}
}

func TestTwoCutsInTheSameSecondDoNotShareAFile(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	early, err := buffer.Record(3*time.Second + 100*time.Millisecond)
	if err != nil {
		t.Fatalf("early cut: %v", err)
	}
	late, err := buffer.Record(3*time.Second + 900*time.Millisecond)
	if err != nil {
		t.Fatalf("late cut: %v", err)
	}
	if early.Path == late.Path || early.ID == late.ID {
		t.Fatalf("both cuts published %q", early.Path)
	}
	if err := early.Valid(); err != nil {
		t.Fatalf("the first cut was damaged by the second: %v", err)
	}
	if err := late.Valid(); err != nil {
		t.Fatalf("the second cut is not valid: %v", err)
	}
}

func TestAFailedCutLeavesTheEarlierRecordingAlone(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	first, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("first cut: %v", err)
	}

	// Break the second segment so the next cut fails while it is being written.
	segments := buffer.Segments()
	if len(segments) < 2 {
		t.Fatalf("the fixture stored %d segments, want 2", len(segments))
	}
	if err := os.Remove(segments[1].Path); err != nil {
		t.Fatalf("remove a stored segment: %v", err)
	}
	if _, err := buffer.Record(2 * time.Second); !errors.Is(err, ErrNotPlayable) {
		t.Fatalf("the failed cut reports %v, want ErrNotPlayable", err)
	}

	if err := first.Valid(); err != nil {
		t.Fatalf("the earlier recording did not survive the failed cut: %v", err)
	}
	entries, err := os.ReadDir(buffer.opts.Dir)
	if err != nil {
		t.Fatalf("read the buffer directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			t.Fatalf("the failed cut left %q behind", entry.Name())
		}
	}
}

func TestRecordingsAreRemovedWhenTheBufferCloses(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := os.Stat(recording.Path); err != nil {
		t.Fatalf("the recording was not published: %v", err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(recording.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the recording survived the close: %v", err)
	}
}

func TestRecordedFilesStayInsideTheBufferDirectory(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if dir := filepath.Dir(recording.Path); dir != buffer.opts.Dir {
		t.Fatalf("recording written to %q, want %q", dir, buffer.opts.Dir)
	}
	if filepath.Base(recording.Path) != recording.FileName() {
		t.Fatalf("the file name disagrees with the path")
	}
}

// anchorFor builds a measured anchor for a recording: the test says which
// buffer time it observed and which source media time it saw there. Tests that
// want a refusal change one field afterwards.
func anchorFor(recording Recording, bufferStart, mediaStart time.Duration) Anchor {
	return Anchor{
		Source:         recording.Source,
		Epoch:          recording.SourceMediaEpoch,
		BufferInstance: recording.BufferInstance,
		BufferStart:    bufferStart,
		MediaStart:     mediaStart,
		// The buffer still held a second past the file when the anchor was taken.
		Interval:   Range{Start: recording.StartBufferTime, End: recording.EndBufferTime + time.Second},
		MeasuredAt: time.Now(),
	}
}

// TestEventTimeRefusesAForeignClockOrAnImpossiblePosition pins the checks that
// moved to the content domain: an anchor from another source, buffer or epoch
// can cover the same numbers and produce a plausible time, and a position
// outside the file must be refused before any arithmetic happens.
func TestEventTimeRefusesAForeignClockOrAnImpossiblePosition(t *testing.T) {
	recording := Recording{
		ID:               "rec-right",
		Path:             "rec-right.ts",
		Source:           "right",
		SourceMediaEpoch: 1,
		BufferInstance:   "buf-right",
		StartBufferTime:  0,
		EndBufferTime:    4 * time.Second,
	}
	valid := func(mutate func(*Anchor)) Anchor {
		anchor := Anchor{
			Source:         "right",
			Epoch:          1,
			BufferInstance: "buf-right",
			BufferStart:    0,
			MediaStart:     2 * time.Second,
			Interval:       Range{Start: 0, End: 10 * time.Second},
			MeasuredAt:     time.Now(),
		}
		if mutate != nil {
			mutate(&anchor)
		}
		return anchor
	}

	for _, test := range []struct {
		name     string
		position time.Duration
		anchor   Anchor
		want     error
	}{
		{"another source", 0, valid(func(a *Anchor) { a.Source = "wrong" }), ErrNoAnchor},
		{"another instance", 0, valid(func(a *Anchor) { a.BufferInstance = "buf-wrong" }), ErrNoAnchor},
		{"another epoch", 0, valid(func(a *Anchor) { a.Epoch = 9 }), ErrNoAnchor},
		{"a negative position", -time.Second, valid(nil), ErrNoRange},
		{"past the last frame", 5 * time.Second, valid(nil), ErrNoRange},
		{"on the closing edge", 4 * time.Second, valid(nil), ErrNoRange},
	} {
		_, err := recording.EventTime(test.position, test.anchor, 0)
		if !errors.Is(err, test.want) {
			t.Errorf("%s: err = %v, want %v", test.name, err, test.want)
		}
	}

	// A position inside the file still converts, and the offset keeps its sign.
	event, err := recording.EventTime(2*time.Second, valid(nil), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("event time inside the file: %v", err)
	}
	if event != 1500*time.Millisecond {
		t.Fatalf("event = %s, want 1.5s", event)
	}
	if _, err := recording.EventTime(0, valid(nil), math.MinInt64); err == nil {
		t.Fatal("EventTime accepted an offset that wraps around")
	}
}
