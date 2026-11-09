package hls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// TestProbeRecordedVOD is checkpoint A: a recorded playlist is read for real,
// and the tracks it reports match the media that was generated (H.264 video and
// one AAC audio track, both with a first sample and a program date time).
func TestProbeRecordedVOD(t *testing.T) {
	server := serveFixtures(t)

	stream, err := Probe(context.Background(), server.URL+"/vod/index.m3u8", Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if stream.Playlist.Kind != KindMedia {
		t.Fatalf("playlist kind = %s, want media", stream.Playlist.Kind)
	}
	if len(stream.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2: %+v", len(stream.Tracks), stream.Tracks)
	}

	var video, audio *TrackInfo
	for i := range stream.Tracks {
		track := &stream.Tracks[i]
		switch track.Kind {
		case core.MediaVideo:
			video = track
		case core.MediaAudio:
			audio = track
		default:
			t.Fatalf("track %d has unexpected kind %q", i, track.Kind)
		}
	}
	if video == nil || audio == nil {
		t.Fatalf("missing a video or audio track: %+v", stream.Tracks)
	}
	// MPEG-TS measures both tracks in a 90 kHz clock, so the audio track's
	// clock rate is the container timescale, not its 44.1 kHz sample rate.
	if video.Codec != "h264" || video.ClockRate != 90000 {
		t.Fatalf("video = %+v, want h264 at 90000", *video)
	}
	if audio.Codec != "mpeg4-audio" || audio.ClockRate != 90000 {
		t.Fatalf("audio = %+v, want mpeg4-audio at the 90000 container clock", *audio)
	}
	for _, track := range []*TrackInfo{video, audio} {
		if !track.HasSample {
			t.Fatalf("track %s reported no first sample", track.Codec)
		}
		if track.FirstPTS < 0 {
			t.Fatalf("track %s first pts = %s", track.Codec, track.FirstPTS)
		}
		if !track.HasProgramDateTime {
			t.Fatalf("track %s lost its program date time", track.Codec)
		}
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if track.ProgramDateTime.Before(base) || track.ProgramDateTime.After(base.Add(4*time.Second)) {
			t.Fatalf("track %s program date time = %s, want within the recorded window", track.Codec, track.ProgramDateTime)
		}
	}
}

// TestProbeWithMissingSegment is checkpoint B's other half: a playlist that
// points at a segment the server does not have must not hang the probe. The
// library retries a failed segment, so the result is either the error it
// reports or the timeout, but never an unbounded wait.
func TestProbeWithMissingSegment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vod/broken.m3u8" {
			fmt.Fprint(w, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000,
seg0.ts
#EXTINF:2.000,
missing.ts
#EXT-X-ENDLIST
`)
			return
		}
		if r.URL.Path == "/vod/missing.ts" {
			http.NotFound(w, r)
			return
		}
		http.FileServer(http.Dir("testdata")).ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	started := time.Now()
	_, err := Probe(context.Background(), server.URL+"/vod/broken.m3u8", Options{Timeout: 2 * time.Second})
	elapsed := time.Since(started)
	if elapsed > 8*time.Second {
		t.Fatalf("probe with a missing segment took %s", elapsed)
	}
	if err == nil {
		t.Log("probe returned the tracks it could read before the missing segment")
		return
	}
	if !errorsIs(err, ErrTimeout) && !errorsIs(err, ErrResponse) {
		t.Fatalf("error = %v, want the timeout or the failed response", err)
	}
}

// TestProbeReportsBadPlaylist keeps a failed read from looking like an empty
// stream.
func TestProbeReportsBadPlaylist(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	if _, err := Probe(context.Background(), server.URL+"/missing.m3u8", Options{}); !errorsIs(err, ErrResponse) {
		t.Fatalf("error = %v, want ErrResponse", err)
	}
}

// TestProbeStopsOnCancel is checkpoint B: a stream that stalls must not hold
// the caller past its cancellation, and the client it started must be released.
func TestProbeStopsOnCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vod/index.m3u8" {
			http.FileServer(http.Dir("testdata")).ServeHTTP(w, r)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	_, err := Probe(ctx, server.URL+"/vod/index.m3u8", Options{Timeout: 30 * time.Second})
	if !errorsIs(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled probe took %s", elapsed)
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if leaked := runtime.NumGoroutine() - before; leaked > 0 {
		t.Fatalf("%d goroutines still running after the probe was cancelled", leaked)
	}
}

// TestProbeWithoutProgramDateTime keeps an unlabelled stream honest: tracks are
// still reported, but no date is invented.
func TestProbeWithoutProgramDateTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vod/plain.m3u8" {
			fmt.Fprint(w, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000,
seg0.ts
#EXTINF:2.000,
seg1.ts
#EXT-X-ENDLIST
`)
			return
		}
		http.FileServer(http.Dir("testdata")).ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	stream, err := Probe(context.Background(), server.URL+"/vod/plain.m3u8", Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(stream.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(stream.Tracks))
	}
	for _, track := range stream.Tracks {
		if track.HasProgramDateTime {
			t.Fatalf("track %s invented a program date time", track.Codec)
		}
	}
}
