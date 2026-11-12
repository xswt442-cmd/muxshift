package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// aPlaylistOverOneSegment is a small enough playlist for a test to serve: one
// segment that the handler can refuse and then hand out.
const aPlaylistOverOneSegment = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
#EXT-X-PROGRAM-DATE-TIME:2026-01-01T00:00:00.000Z
seg0.ts
#EXT-X-ENDLIST
`

// TestAFailedSegmentReadIsNamedAndClearsItself keeps an outage visible when the
// playlist is fine but the segment is not. The page used to clear the failure as
// soon as the playlist parsed, so a source whose segments were refused read as
// healthy; the row now keeps the failure until a segment is actually stored.
func TestAFailedSegmentReadIsNamedAndClearsItself(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the segment fixture: %v", err)
	}
	var serving atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte(aPlaylistOverOneSegment))
		case "/seg0.ts":
			if !serving.Load() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := startDVR(server.URL+"/index.m3u8", dvr.Options{
		Dir:         t.TempDir(),
		Source:      "fixture",
		SourceEpoch: 1,
	})
	if err != nil {
		t.Fatalf("start the buffer: %v", err)
	}
	defer func() { _ = runner.Close() }()

	// The playlist reads; the segment does not. The outage must be named.
	usage := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool { return u.readErr != "" })
	if !strings.Contains(usage.readErr, "404") {
		t.Fatalf("the failure reads %q, want the refused segment", usage.readErr)
	}
	if usage.segments != 0 {
		t.Fatalf("a refused segment was counted as stored: %d segment(s)", usage.segments)
	}
	if !strings.Contains(runner.usageLine("fixture/index.m3u8"), "cannot be read") {
		t.Fatalf("the row does not name the failure: %q", runner.usageLine("fixture/index.m3u8"))
	}

	// The same segment starts working: the failure clears only now.
	serving.Store(true)
	served := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool {
		return u.readErr == "" && u.segments == 1
	})
	if served.segments != 1 {
		t.Fatalf("the segment landed %d times, want once", served.segments)
	}
	if strings.Contains(runner.usageLine("fixture/index.m3u8"), "cannot be read") {
		t.Fatalf("the row still names a failure after a segment landed: %q",
			runner.usageLine("fixture/index.m3u8"))
	}
}

// waitForUsage polls the runner until want agrees, and fails the test with the
// last reading when the deadline passes first.
func waitForUsage(t *testing.T, runner *dvrRunner, within time.Duration, want func(dvrUsage) bool) dvrUsage {
	t.Helper()
	deadline := time.Now().Add(within)
	var usage dvrUsage
	for {
		usage = runner.usage()
		if want(usage) {
			return usage
		}
		if time.Now().After(deadline) {
			t.Fatalf("the buffer never got there: %+v", usage)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
