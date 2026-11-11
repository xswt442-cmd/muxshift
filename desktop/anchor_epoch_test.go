package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core/dvr"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// fixtureBuffer starts a buffer fed by a server that serves one real transport
// stream segment, so the buffer has media to measure without a playlist. The
// second result appends a segment, which is how a test stages "the playlist
// moved on while ffprobe was still reading".
func fixtureBuffer(t *testing.T) (*dvr.Buffer, func(sequence int, discontinuity bool)) {
	t.Helper()
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the fixture segment: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	buffer, err := dvr.New(dvr.Options{
		Dir:           t.TempDir(),
		RecordingsDir: t.TempDir(),
		Source:        "fixture",
	})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	t.Cleanup(func() { _ = buffer.Close() })
	add := func(sequence int, discontinuity bool) {
		t.Helper()
		if _, err := buffer.Add(context.Background(), server.Client(), hls.Segment{
			Sequence:      sequence,
			URI:           server.URL + "/seg.ts",
			Duration:      2 * time.Second,
			Discontinuity: discontinuity,
		}); err != nil {
			t.Fatalf("add segment %d: %v", sequence, err)
		}
	}
	add(1, false)
	return buffer, add
}

// stubProbe replaces the measurement for the duration of one test.
func stubProbe(t *testing.T, media time.Duration, during func()) {
	t.Helper()
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		if during != nil {
			during()
		}
		return media, nil
	}
}

// TestMeasureAnchorKeepsTheEpochOfItsSegment runs the race the epoch check has
// to survive: the playlist declares a discontinuity while the segment is being
// measured. The anchor belongs to the media it measured, so it has to name the
// clock that media was stored in, and asking for a window afterwards has to be
// refused instead of silently pairing old media with the new clock.
func TestMeasureAnchorKeepsTheEpochOfItsSegment(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	point, err := buffer.OldestSegment()
	if err != nil {
		t.Fatalf("oldest segment: %v", err)
	}
	jumped := false
	stubProbe(t, 55*time.Second, func() {
		if !jumped {
			jumped = true
			add(2, true)
		}
	})

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	anchor, err := runner.measureAnchor(point)
	if err != nil {
		t.Fatalf("measure the anchor: %v", err)
	}
	if anchor.Epoch != 1 {
		t.Fatalf("the anchor names epoch %d, want the epoch of the segment it measured (1)", anchor.Epoch)
	}
	if got := buffer.Epoch(); got != 2 {
		t.Fatalf("the buffer's epoch is %d, want 2 after the jump", got)
	}
	// The pair measured at the old point describes a clock the buffer has left,
	// so it must not be handed out for the new one; a fresh measurement of the
	// new point is what restores a usable anchor.
	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil {
		t.Fatalf("the anchor measured in epoch 1 was used for the new clock: %+v", anchor)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fresh, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 2 * time.Second, End: 4 * time.Second})
	if err != nil {
		t.Fatalf("measuring the new clock: %v", err)
	}
	if fresh.Epoch != 2 {
		t.Fatalf("the new anchor names epoch %d, want 2", fresh.Epoch)
	}
}

// TestFailedAnchorRetriesAfterNextSegmentInSameEpoch pins the retry trigger. The
// point an anchor describes does not move while a range grows, so "the buffer
// stored another segment" is a different question from "which point is
// measured". A failed measurement must be retried once new media arrives, must
// not be retried on every poll, and a measurement that succeeded must stand.
func TestFailedAnchorRetriesAfterNextSegmentInSameEpoch(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	refused := errors.New("ffprobe could not read the segment")

	var mu sync.Mutex
	calls := 0
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			return 0, refused
		}
		return 100 * time.Second, nil
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runner.refreshAnchor()
	if _, err := runner.waitAnchor(ctx); !errors.Is(err, refused) {
		t.Fatalf("waiting on a failed measurement reports %v, want the probe's error", err)
	}
	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil {
		t.Fatalf("a failed measurement produced an anchor: %+v", anchor)
	}

	// Nothing new has arrived, so nothing new can be learned: polling must not
	// start another measurement.
	after := count()
	for i := 0; i < 5; i++ {
		runner.refreshAnchor()
	}
	time.Sleep(50 * time.Millisecond)
	if got := count(); got != after {
		t.Fatalf("the buffer measured %d times without a new segment, want %d", got, after)
	}

	// A second segment in the same epoch is the trigger.
	add(2, false)
	anchor, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 0, End: 2 * time.Second})
	if err != nil {
		t.Fatalf("the retry after new media did not produce an anchor: %v", err)
	}
	if anchor.Epoch != 1 {
		t.Fatalf("the retried anchor names epoch %d, want 1", anchor.Epoch)
	}
	if anchor.BufferStart != 0 {
		t.Fatalf("the retried anchor starts at buffer %s, want the measured segment's 0s", anchor.BufferStart)
	}
	if got := count(); got != after+1 {
		t.Fatalf("the retry measured %d times, want one more than %d", got, after)
	}

	// A measurement that succeeded stands: growing the range does not make it
	// stale, and re-measuring would only cost another ffprobe.
	add(3, false)
	runner.refreshAnchor()
	time.Sleep(50 * time.Millisecond)
	if got := count(); got != after+1 {
		t.Fatalf("a successful acquisition was measured again (%d calls, want %d)", got, after+1)
	}
}

// TestCurrentSegmentFollowsTheClock guards the choice of measurement point: after
// a jump the oldest segment is old media, so the buffer has to offer the first
// segment of the range it is filling now.
func TestCurrentSegmentFollowsTheClock(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	add(2, true)
	current, err := buffer.CurrentSegment()
	if err != nil {
		t.Fatalf("current segment: %v", err)
	}
	if current.Epoch != 2 {
		t.Fatalf("the current point is in epoch %d, want 2", current.Epoch)
	}
	oldest, err := buffer.OldestSegment()
	if err != nil {
		t.Fatalf("oldest segment: %v", err)
	}
	if oldest.Epoch != 1 {
		t.Fatalf("the oldest point is in epoch %d, want 1", oldest.Epoch)
	}
}

// TestBufferAnchorDoesNotWaitForTheMeasurement keeps ffprobe off the drawing
// thread: a caller asking while the measurement runs is told to come back, and
// the answer arrives once it is done.
func TestBufferAnchorDoesNotWaitForTheMeasurement(t *testing.T) {
	buffer, _ := fixtureBuffer(t)
	release := make(chan struct{})
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		<-release
		return 100 * time.Second, nil
	}

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	runner.refreshAnchor()
	if _, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil ||
		!strings.Contains(err.Error(), "still measuring") {
		t.Fatalf("an unfinished measurement reports %v, want a come-back-later error", err)
	}
	close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	anchor, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 0, End: 12 * time.Second})
	if err != nil {
		t.Fatalf("waiting for the measurement: %v", err)
	}
	if anchor.MediaStart != 100*time.Second || anchor.Interval.End != 12*time.Second {
		t.Fatalf("the anchor measures %s and covers to %s", anchor.MediaStart, anchor.Interval.End)
	}
}
