package dvr

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
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
	if recording.RequestedMediaTime != 3*time.Second {
		t.Fatalf("requested = %s, want 3s", recording.RequestedMediaTime)
	}
	if recording.SegmentStartTime != 2*time.Second {
		t.Fatalf("segment start = %s, want 2s", recording.SegmentStartTime)
	}
	if recording.ContentStart != 2*time.Second || recording.ContentEnd != 4*time.Second {
		t.Fatalf("content = %s..%s, want 2s..4s", recording.ContentStart, recording.ContentEnd)
	}
	if recording.ContentStart == recording.RequestedMediaTime {
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
	if recording.FileName() != "replay-3.ts" {
		t.Fatalf("file name = %q", recording.FileName())
	}
}

func TestRecordAtASegmentBoundaryStartsThere(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if recording.SegmentStartTime != recording.ContentStart || recording.ContentStart != 2*time.Second {
		t.Fatalf("segment start %s and content start %s, want both 2s",
			recording.SegmentStartTime, recording.ContentStart)
	}
	if recording.ContentEnd != 4*time.Second {
		t.Fatalf("content end = %s, want 4s", recording.ContentEnd)
	}
}

func TestRecordingMapsPositionsBothWays(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(3 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// Invariant: SourceMediaTime = RecordingPosition + ContentStart.
	for _, position := range []time.Duration{0, 250 * time.Millisecond, time.Second, 2 * time.Second} {
		at, err := recording.SourceMediaTime(position)
		if err != nil {
			t.Fatalf("media time at %s: %v", position, err)
		}
		if want := recording.ContentStart + position; at != want {
			t.Fatalf("media time at %s = %s, want %s", position, at, want)
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
	offset, err := recording.Position(recording.RequestedMediaTime)
	if err != nil {
		t.Fatalf("position of the request: %v", err)
	}
	if offset != time.Second {
		t.Fatalf("the request sits at %s in the file, want 1s", offset)
	}

	if _, err := recording.SourceMediaTime(2*time.Second + time.Millisecond); !errors.Is(err, ErrNoRange) {
		t.Fatalf("media time past the end = %v, want ErrNoRange", err)
	}
	if _, err := recording.Position(time.Second); !errors.Is(err, ErrNoRange) {
		t.Fatalf("position before the content = %v, want ErrNoRange", err)
	}
}

func TestRecordingEventTimeUsesTheTrackOffset(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	recording, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	for _, test := range []struct {
		offset time.Duration
		want   time.Duration
	}{
		{0, 2 * time.Second},
		{500 * time.Millisecond, 2*time.Second + 500*time.Millisecond},
		{-2 * time.Second, 0},
	} {
		got, err := recording.EventTime(0, test.offset)
		if err != nil {
			t.Fatalf("event time with offset %s: %v", test.offset, err)
		}
		if got != test.want {
			t.Fatalf("event time with offset %s = %s, want %s", test.offset, got, test.want)
		}
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
	if after.ContentStart != 4*time.Second || after.ContentEnd != 6*time.Second {
		t.Fatalf("content after the gap = %s..%s, want 4s..6s", after.ContentStart, after.ContentEnd)
	}
	if len(after.Gaps) != 1 || len(after.Ranges) != 2 {
		t.Fatalf("the recording does not describe the gap: %d gaps, %d ranges", len(after.Gaps), len(after.Ranges))
	}
}

func TestRecordingIsInvalidatedWhenReplaced(t *testing.T) {
	buffer, _, _ := recBuffer(t, "")
	first, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := first.Valid(); err != nil {
		t.Fatalf("the first recording is not valid: %v", err)
	}

	second, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	if _, err := os.Stat(first.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the first file survived the second recording: %v", err)
	}
	if err := first.Valid(); !errors.Is(err, ErrNoRange) {
		t.Fatalf("the replaced recording reports %v, want ErrNoRange", err)
	}
	if err := second.Valid(); err != nil {
		t.Fatalf("the second recording is not valid: %v", err)
	}

	if err := buffer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := second.Valid(); !errors.Is(err, ErrNoRange) {
		t.Fatalf("the recording outlived the buffer: %v", err)
	}
}
