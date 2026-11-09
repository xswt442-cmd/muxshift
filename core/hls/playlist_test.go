package hls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// serveFixtures serves the recorded test media over HTTP, so a read goes
// through the same request path as a real address.
func serveFixtures(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	t.Cleanup(server.Close)
	return server
}

// TestReadVOD reads the recorded playlist and checks it against the media that
// was generated: two two-second segments with a stated program date time.
func TestReadVOD(t *testing.T) {
	server := serveFixtures(t)

	info, err := Read(context.Background(), server.URL+"/vod/index.m3u8", Options{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if info.Kind != KindMedia {
		t.Fatalf("kind = %s, want media", info.Kind)
	}
	if !info.EndList {
		t.Fatal("vod playlist reported as open-ended")
	}
	if info.Version != 3 || info.TargetDuration != 2*time.Second || info.MediaSequence != 0 {
		t.Fatalf("header = version %d, target %s, sequence %d", info.Version, info.TargetDuration, info.MediaSequence)
	}
	if len(info.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(info.Segments))
	}
	if info.Duration != 4*time.Second {
		t.Fatalf("duration = %s, want 4s", info.Duration)
	}
	for i, want := range []int{0, 1} {
		segment := info.Segments[i]
		if segment.Sequence != want {
			t.Fatalf("segment %d sequence = %d, want %d", i, segment.Sequence, want)
		}
		if segment.Duration != 2*time.Second {
			t.Fatalf("segment %d duration = %s", i, segment.Duration)
		}
		if !strings.HasPrefix(segment.URI, server.URL+"/vod/") || !strings.HasSuffix(segment.URI, ".ts") {
			t.Fatalf("segment %d URI = %q, want an absolute segment address", i, segment.URI)
		}
		if !segment.HasProgramDateTime {
			t.Fatalf("segment %d lost its program date time", i)
		}
		want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 2 * time.Second)
		if !segment.ProgramDateTime.Equal(want) {
			t.Fatalf("segment %d program date time = %s, want %s", i, segment.ProgramDateTime, want)
		}
	}
}

// TestReadFlagsGapsAndDiscontinuities checks that the two segment markers Core
// needs are carried through instead of being lost in parsing.
func TestReadFlagsGapsAndDiscontinuities(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:12
#EXTINF:6.000,
one.ts
#EXT-X-DISCONTINUITY
#EXTINF:6.000,
two.ts
#EXT-X-GAP
#EXTINF:6.000,
three.ts
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)

	info, err := Read(context.Background(), server.URL+"/live.m3u8", Options{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(info.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(info.Segments))
	}
	if info.Segments[0].Discontinuity || info.Segments[0].Gap {
		t.Fatalf("first segment carries a marker: %+v", info.Segments[0])
	}
	if !info.Segments[1].Discontinuity {
		t.Fatal("discontinuity before the second segment was lost")
	}
	if !info.Segments[2].Gap {
		t.Fatal("gap on the third segment was lost")
	}
	for i, segment := range info.Segments {
		if segment.HasProgramDateTime {
			t.Fatalf("segment %d invented a program date time", i)
		}
		if segment.Sequence != 12+i {
			t.Fatalf("segment %d sequence = %d, want %d", i, segment.Sequence, 12+i)
		}
	}
}

// TestReadRollingLivePlaylist checks a playlist that advances between reads:
// the media sequence moves, segment order is preserved, and the end is not
// claimed.
func TestReadRollingLivePlaylist(t *testing.T) {
	var round atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		start := int(round.Add(1)) - 1
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:%d\n", start)
		for i := 0; i < 2; i++ {
			fmt.Fprintf(w, "#EXTINF:2.000,\nseg%d.ts\n", start+i)
		}
	}))
	t.Cleanup(server.Close)

	first, err := Read(context.Background(), server.URL+"/live.m3u8", Options{})
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := Read(context.Background(), server.URL+"/live.m3u8", Options{})
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if first.EndList || second.EndList {
		t.Fatal("live playlist claimed to be complete")
	}
	if first.MediaSequence != 0 || second.MediaSequence != 1 {
		t.Fatalf("media sequences = %d then %d, want 0 then 1", first.MediaSequence, second.MediaSequence)
	}
	if second.Segments[0].Sequence != 1 || second.Segments[1].Sequence != 2 {
		t.Fatalf("second read sequences = %d, %d, want 1, 2", second.Segments[0].Sequence, second.Segments[1].Sequence)
	}
	for _, segment := range second.Segments {
		if !strings.HasPrefix(segment.URI, server.URL+"/") {
			t.Fatalf("segment URI = %q, want a resolved address", segment.URI)
		}
	}
}

// TestReadRejectsMasterPlaylist keeps variant choice with the client: a master
// playlist is reported as such, not silently treated as media.
func TestReadRejectsMasterPlaylist(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
low/index.m3u8
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)

	info, err := Read(context.Background(), server.URL+"/master.m3u8", Options{})
	if !errorsIs(err, ErrMasterPlaylist) {
		t.Fatalf("error = %v, want ErrMasterPlaylist", err)
	}
	if info.Kind != KindMaster {
		t.Fatalf("kind = %s, want master", info.Kind)
	}
	if len(info.Segments) != 0 {
		t.Fatalf("master playlist reported %d segments", len(info.Segments))
	}
}

// TestReadRejectsEmptyMediaPlaylist keeps an empty stream from looking usable.
func TestReadRejectsEmptyMediaPlaylist(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-ENDLIST
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)

	if _, err := Read(context.Background(), server.URL+"/empty.m3u8", Options{}); err == nil {
		t.Fatal("empty media playlist read as usable")
	}
}

// TestReadReportsBadResponses checks the failure modes a client must tell
// apart: a status error, a body that is not a playlist, and an oversized body.
func TestReadReportsBadResponses(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(server.Close)
		if _, err := Read(context.Background(), server.URL+"/missing.m3u8", Options{}); !errorsIs(err, ErrResponse) {
			t.Fatalf("error = %v, want ErrResponse", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream failed", http.StatusBadGateway)
		}))
		t.Cleanup(server.Close)
		if _, err := Read(context.Background(), server.URL+"/broken.m3u8", Options{}); !errorsIs(err, ErrResponse) {
			t.Fatalf("error = %v, want ErrResponse", err)
		}
	})

	t.Run("not a playlist", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "<html>login required</html>")
		}))
		t.Cleanup(server.Close)
		if _, err := Read(context.Background(), server.URL+"/login", Options{}); !errorsIs(err, core.ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})

	t.Run("too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "#EXTM3U\n")
			chunk := strings.Repeat("#\n", 1024)
			for i := 0; i < maxPlaylistSize/len(chunk)+2; i++ {
				fmt.Fprint(w, chunk)
			}
		}))
		t.Cleanup(server.Close)
		if _, err := Read(context.Background(), server.URL+"/huge.m3u8", Options{}); !errorsIs(err, ErrPlaylistTooLarge) {
			t.Fatalf("error = %v, want ErrPlaylistTooLarge", err)
		}
	})
}

// TestReadHonoursCancellationAndTimeout checks that a stalled address does not
// hold a caller past its context or its timeout.
func TestReadHonoursCancellationAndTimeout(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(blocked.Close)

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		if _, err := Read(ctx, blocked.URL+"/live.m3u8", Options{}); err == nil {
			t.Fatal("cancelled read succeeded")
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("cancelled read took %s", elapsed)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		started := time.Now()
		if _, err := Read(context.Background(), blocked.URL+"/live.m3u8", Options{Timeout: 200 * time.Millisecond}); err == nil {
			t.Fatal("read past the timeout succeeded")
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("timed-out read took %s", elapsed)
		}
	})
}
