package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInspectLivePlaylistListsUsableDeclaredVariants(t *testing.T) {
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/master.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=640x360\n360.m3u8?token=secret\n#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720\n720.m3u8?token=secret\n#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080\nbroken.m3u8\n")
		case "/360.m3u8", "/720.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg.ts\n")
		case "/broken.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n#EXTINF:6,\nseg.ts\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	inspection, err := inspectLivePlaylist(context.Background(), server.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("inspectLivePlaylist: %v", err)
	}
	if inspection.kind != liveMasterPlaylist || len(inspection.variants) != 2 {
		t.Fatalf("kind=%v variants=%+v; want master with two usable variants", inspection.kind, inspection.variants)
	}
	if inspection.variants[0].width != 640 || inspection.variants[0].height != 360 || inspection.variants[0].bandwidth != 900000 {
		t.Fatalf("first variant metadata = %+v", inspection.variants[0])
	}
	if inspection.variants[1].width != 1280 || inspection.variants[1].height != 720 || inspection.variants[1].bandwidth != 2400000 {
		t.Fatalf("second variant metadata = %+v", inspection.variants[1])
	}
	if !strings.HasSuffix(inspection.role.address, "/720.m3u8?token=secret") {
		t.Fatalf("default address = %q; want highest usable declared variant", inspection.role.address)
	}
	if strings.Contains(fmt.Sprintf("%+v", inspection.variants[0]), "token=secret") {
		t.Fatal("variant metadata formatting exposed its private signed address")
	}
	if requests["/master.m3u8"] != 1 || requests["/360.m3u8"] != 1 || requests["/720.m3u8"] != 1 || requests["/broken.m3u8"] != 1 {
		t.Fatalf("inspection requests = %#v; want one request per root and declared child", requests)
	}
}

func TestInspectPlaylistFetchesOnlyTheChosenChild(t *testing.T) {
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/master.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=640x360\n360.m3u8?token=secret\n#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720\n720.m3u8?token=secret\n")
		case "/360.m3u8", "/720.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg.ts\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	role, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8?root=private")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if requests["/master.m3u8"] != 1 || requests["/720.m3u8"] != 1 || requests["/360.m3u8"] != 0 {
		t.Fatalf("ordinary import requests = %#v; want root and selected child only", requests)
	}
	if !strings.Contains(role.address, "token=secret") {
		t.Fatalf("selected media address lost its signed query: %q", shortURI(role.address))
	}
}

func TestPlaylistReadErrorsRedactSignedQueries(t *testing.T) {
	secret := "token=super-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := inspectLivePlaylist(context.Background(), server.URL+"/master.m3u8?"+secret)
	if err == nil {
		t.Fatal("inspectLivePlaylist unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("error exposed signed query: %v", err)
	}
}

func TestInspectLivePlaylistDistinguishesSingleMediaPlaylist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg.ts\n")
	}))
	defer server.Close()

	inspection, err := inspectLivePlaylist(context.Background(), server.URL+"/live.m3u8")
	if err != nil {
		t.Fatalf("inspectLivePlaylist: %v", err)
	}
	if inspection.kind != liveMediaPlaylist || len(inspection.variants) != 0 {
		t.Fatalf("kind=%v variants=%+v; want a single media playlist with no choices", inspection.kind, inspection.variants)
	}
	if inspection.role.address != server.URL+"/live.m3u8" {
		t.Fatalf("media playlist address = %q", inspection.role.address)
	}
}

func TestParseVariantsRejectsNonHTTPAndCredentialedReferences(t *testing.T) {
	for _, uri := range []string{"file:///tmp/variant.m3u8", "https://user:pass@example.test/variant.m3u8"} {
		text := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1x1\n" + uri + "\n"
		if _, err := parseVariants("https://example.test/master.m3u8", text); err == nil {
			t.Errorf("parseVariants accepted %q", uri)
		}
	}
}
