package main

// P8 performance benchmarks: measure the draw-path helpers and the session
// event list so a change is judged by numbers, not by feel. These tests build
// state directly and never open a window.

import (
	"fmt"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// newBenchApp builds a session with n shown sources, each with one video and
// one audio track, and the matching item list the draw path walks.
func newBenchApp(n int) *appState {
	sess := core.NewSession("bench")
	scene, err := sess.AddScene("scene")
	if err != nil {
		panic(err)
	}
	app := &appState{
		session: sess,
		scene:   scene.ID,
		sync:    newSyncState(),
		audible: make(map[core.TrackID]bool),
	}
	for i := 0; i < n; i++ {
		src, err := sess.AddSource(fmt.Sprintf("https://example.test/v/%d/index.m3u8", i))
		if err != nil {
			panic(err)
		}
		video, err := sess.AddTrack(src.ID, core.MediaVideo, "video")
		if err != nil {
			panic(err)
		}
		audio, err := sess.AddTrack(src.ID, core.MediaAudio, "audio")
		if err != nil {
			panic(err)
		}
		app.items = append(app.items, &item{uri: src.URI, source: src, video: video.ID, audio: audio.ID, volume: 1})
	}
	return app
}

// BenchmarkSnapshot measures one full session snapshot, the cost every frame
// pays once.
func BenchmarkSnapshot(b *testing.B) {
	app := newBenchApp(50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = app.snapshot()
	}
}

// BenchmarkEntryOfFirst measures one row asking the snapshot for its entry.
// Before the fix this rebuilt the whole snapshot, so it scaled with the number
// of sources rather than being constant.
func BenchmarkEntryOfFirst(b *testing.B) {
	app := newBenchApp(50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = app.entryOf(app.items[0])
	}
}

// BenchmarkEntryOfAllRows measures one draw pass that asks every row for its
// entry, which is what the dock and the inspector do per frame.
func BenchmarkEntryOfAllRows(b *testing.B) {
	app := newBenchApp(50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, it := range app.items {
			_ = app.entryOf(it)
		}
	}
}

// BenchmarkFormatHelpers measures the per-row label formatting the draw path
// repeats: event time, short URI, source display name, clock.
func BenchmarkFormatHelpers(b *testing.B) {
	app := newBenchApp(50)
	it := app.items[0]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = formatEvent(time.Second)
		_ = shortURI(it.uri)
		_ = sourceDisplayName(it)
		_ = clock(time.Minute + 2*time.Second)
	}
}

// BenchmarkSessionEvents measures reading the event list after a long run. The
// list used to grow without bound; the cap keeps it near 1000 events.
func BenchmarkSessionEvents(b *testing.B) {
	sess := core.NewSession("events")
	for i := 0; i < 20000; i++ {
		if _, err := sess.AddSource(fmt.Sprintf("https://e.test/%d/index.m3u8", i)); err != nil {
			panic(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := sess.Events()
		if len(ev) == 0 {
			b.Fatal("empty event list")
		}
	}
}
