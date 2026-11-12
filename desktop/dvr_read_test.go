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

// aPlaylistOverTwoSegments offers a second segment after the first, so a poll
// that skips a refused segment can be told apart from one that stops there.
const aPlaylistOverTwoSegments = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
seg0.ts
#EXTINF:2.000000,
seg1.ts
#EXT-X-ENDLIST
`

// TestAMixedPlaylistDoesNotSkipTheSegmentItCouldNotRead is the counterexample the
// review found: the first segment was refused, the second was stored, and the
// cursor moved past the refused one, so it was never fetched again even after it
// became readable. A poll now stops at the first failure instead.
func TestAMixedPlaylistDoesNotSkipTheSegmentItCouldNotRead(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the segment fixture: %v", err)
	}
	var serving atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte(aPlaylistOverTwoSegments))
		case "/seg0.ts":
			if !serving.Load() {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		case "/seg1.ts":
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

	// The first segment is refused while the second is fine. Storing the second
	// would move the cursor past the first, so nothing may be stored at all.
	usage := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool { return u.readErr != "" })
	if !strings.Contains(usage.readErr, "404") {
		t.Fatalf("the failure reads %q, want the refused segment", usage.readErr)
	}
	if usage.segments != 0 {
		t.Fatalf("the poll stored %d segment(s) after a refusal, so the refused one was skipped", usage.segments)
	}

	// Once the first segment is readable again both land, in order.
	serving.Store(true)
	served := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool {
		return u.readErr == "" && u.segments == 2
	})
	if served.segments != 2 {
		t.Fatalf("the buffer holds %d segment(s), want both", served.segments)
	}
}

// TestAWindowThatMovedOnDoesNotStallTheBuffer follows the Core's gap rule: a
// segment that dropped out of the live window cannot be fetched again, so the
// buffer continues from what the window now offers instead of waiting forever.
func TestAWindowThatMovedOnDoesNotStallTheBuffer(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the segment fixture: %v", err)
	}
	var phase atomic.Int32
	first := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
seg0.ts
#EXTINF:2.000000,
seg1.ts
`
	later := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:2
#EXTINF:2.000000,
seg2.ts
#EXTINF:2.000000,
seg3.ts
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			if phase.Load() == 0 {
				_, _ = w.Write([]byte(first))
				return
			}
			_, _ = w.Write([]byte(later))
		case "/seg0.ts", "/seg2.ts", "/seg3.ts":
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

	// seg0 is stored, seg1 is refused: the cursor sits on seg1.
	waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool {
		return u.readErr != "" && u.segments == 1
	})

	// The window moves on: seg1 is gone and seg2 is what the source offers now.
	phase.Store(1)
	served := waitForUsage(t, runner, 15*time.Second, func(u dvrUsage) bool {
		return u.readErr == "" && u.segments >= 3
	})
	if served.segments < 3 {
		t.Fatalf("the buffer holds %d segment(s), want the window to continue", served.segments)
	}
	if !strings.Contains(runner.state(), "gap") {
		t.Fatalf("the skipped segment left no gap behind: %q", runner.state())
	}
}

// TestASignedAddressIsNotWrittenOut is the security case: a signed address carries
// its token in the query, and the failure it produces quotes the whole address.
// The reason is kept, the token is not — not in the row, and not in the text that
// goes to the log.
func TestASignedAddressIsNotWrittenOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "what the server says", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	signed := server.URL + "/index.m3u8?token=TOP_SECRET_123#key"
	runner, err := startDVR(signed, dvr.Options{
		Dir:         t.TempDir(),
		Source:      "fixture",
		SourceEpoch: 1,
	})
	if err != nil {
		t.Fatalf("start the buffer: %v", err)
	}
	defer func() { _ = runner.Close() }()

	usage := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool { return u.readErr != "" })
	if strings.Contains(usage.readErr, "TOP_SECRET_123") || strings.Contains(usage.readErr, "key") {
		t.Fatalf("the failure repeats the secret: %q", usage.readErr)
	}
	if !strings.Contains(usage.readErr, "503") {
		t.Fatalf("the failure lost the reason: %q", usage.readErr)
	}

	line := runner.usageLine("fixture/index.m3u8")
	if strings.Contains(line, "TOP_SECRET_123") {
		t.Fatalf("the row repeats the secret: %q", line)
	}
	if strings.Contains(line, "token=") {
		t.Fatalf("the row repeats the query: %q", line)
	}
	if got := shortURI(signed); strings.Contains(got, "TOP_SECRET_123") || strings.Contains(got, "token=") {
		t.Fatalf("the short address repeats the secret: %q", got)
	}
	reason := redactSecrets(`Get "` + signed + `": dial tcp: refused`)
	if strings.Contains(reason, "TOP_SECRET_123") || strings.Contains(reason, "key") {
		t.Fatalf("redaction left the secret: %q", reason)
	}
	if !strings.Contains(reason, "refused") {
		t.Fatalf("redaction lost the reason: %q", reason)
	}
}
