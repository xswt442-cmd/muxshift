package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core/dvr"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// TestMeasureAnchorKeepsTheEpochOfItsSegment runs the race the epoch check has
// to survive: the playlist declares a discontinuity while the oldest segment is
// being measured. The anchor belongs to the media it measured, so it has to name
// the old epoch, and asking the buffer for a window afterwards has to be refused
// instead of silently pairing old media with the new clock.
func TestMeasureAnchorKeepsTheEpochOfItsSegment(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the fixture segment: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

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
		_, err := buffer.Add(context.Background(), server.Client(), hls.Segment{
			Sequence:      sequence,
			URI:           server.URL + "/seg.ts",
			Duration:      2 * time.Second,
			Discontinuity: discontinuity,
		})
		if err != nil {
			t.Fatalf("add segment %d: %v", sequence, err)
		}
	}
	add(1, false)

	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		// The measurement takes as long as it takes; meanwhile the playlist
		// rebuilds the media clock.
		add(2, true)
		return 55 * time.Second, nil
	}

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	_, err = runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "moved to epoch") {
		t.Fatalf("a buffer that jumped during the measurement reports %v, want a refusal naming the epoch", err)
	}
	if got := buffer.Epoch(); got != 2 {
		t.Fatalf("the buffer's epoch is %d, want 2", got)
	}
	if runner.anchor == nil || runner.anchor.Epoch != 1 {
		t.Fatalf("the anchor names epoch %v, want the epoch of the segment it measured (1)",
			epochOf(runner.anchor))
	}
	if runner.anchor.BufferStart != 0 || runner.anchor.MediaStart != 55*time.Second {
		t.Fatalf("the anchor measures buffer %s at media %s", runner.anchor.BufferStart, runner.anchor.MediaStart)
	}
}

func epochOf(anchor *dvr.Anchor) uint64 {
	if anchor == nil {
		return 0
	}
	return anchor.Epoch
}
