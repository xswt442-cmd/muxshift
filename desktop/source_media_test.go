package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLoadSourceQualitiesGenerationRaceDoesNotLeaveBlankMenu(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:6,\nsegment.ts\n")
	}))
	defer server.Close()
	app, _, _ := newStoreFixture(t)
	callbacks := make(chan func(), 2)
	app.onMain = func(fn func()) { callbacks <- fn }
	waitForCallback := func() func() {
		t.Helper()
		select {
		case fn := <-callbacks:
			return fn
		case <-time.After(5 * time.Second):
			t.Fatal("quality request did not complete")
			return nil
		}
	}
	it := app.items[0]
	it.playlistMaster = server.URL
	it.mediaMenu = true
	app.loadSourceQualities(it)
	old := waitForCallback()
	it.closeMediaMenu()
	it.mediaMenu = true
	app.loadSourceQualities(it)
	old()
	if !it.mediaLoading || it.mediaCancel == nil {
		t.Fatal("the old completion cleared the newer quality request")
	}
	it.generation++
	waitForCallback()()
	if it.mediaLoading || it.mediaError == "" {
		t.Fatal("the changed player left an empty quality menu")
	}
}

func TestReplaceSourceMediaBlockedDuringReplaySwap(t *testing.T) {
	app, _, _ := newStoreFixture(t)
	it := app.items[0]
	it.player = &ui.VideoPlayer{}
	it.swap = &replaySwap{}
	if app.canReplaceSourceMedia(it) {
		t.Fatal("quality replacement is enabled while entering replay")
	}
	app.replaceSourceMedia(it, "replacement.mp4")
	if it.generation != 0 || it.mediaReplaceErr != nil {
		t.Fatal("quality replacement touched the active replay switch")
	}
}

func TestLoadSourceQualitiesReopensAfterHiddenCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:6,\nsegment.ts\n")
	}))
	defer server.Close()
	app, _, _ := newStoreFixture(t)
	callbacks := make(chan func(), 2)
	app.onMain = func(fn func()) { callbacks <- fn }
	it := app.items[0]
	it.playlistMaster = server.URL
	for _, reopen := range []bool{false, true} {
		it.mediaMenu = true
		app.loadSourceQualities(it)
		if !it.mediaLoading {
			t.Fatal("opening the menu did not start a new playlist read")
		}
		it.mediaMenu = reopen
		select {
		case landed := <-callbacks:
			landed()
		case <-time.After(5 * time.Second):
			t.Fatal("quality request did not complete")
		}
		if it.mediaLoading || it.mediaCancel != nil {
			t.Fatal("completion left the menu loading")
		}
	}
}

func TestRotatingQueryTokensDoNotDuplicateCurrentQuality(t *testing.T) {
	levels := []liveVariant{{address: "https://example.test/720.m3u8?token=new"}}
	if got := currentQuality("https://example.test/720.m3u8?token=old", levels); got != 0 {
		t.Fatalf("rotating token changed the current quality: index %d", got)
	}
	levels = append(levels, liveVariant{address: "https://example.test/720.m3u8?quality=low"})
	if got := currentQuality("https://example.test/720.m3u8?quality=low", levels); got != 1 {
		t.Fatalf("exact match lost a quality selected by query: index %d", got)
	}
}
