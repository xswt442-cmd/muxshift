package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// servePlaylists answers the paths a live import may ask for.
func servePlaylists(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestInspectPlaylistKeepsAMediaPlaylist(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/live.m3u8": "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg1.ts\n#EXT-X-ENDLIST\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/live.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if role.address != server.URL+"/live.m3u8" {
		t.Fatalf("address = %q, want the handed-over playlist", role.address)
	}
	if role.live {
		t.Fatal("a playlist that ends was reported as live")
	}
}

func TestInspectPlaylistReportsALivePlaylist(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/live.m3u8": "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg1.ts\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/live.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if !role.live {
		t.Fatal("a playlist without EXT-X-ENDLIST was not reported as live")
	}
}

func TestInspectPlaylistChoosesThePictureVariant(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/master.m3u8": strings.Join([]string{
			"#EXTM3U",
			`#EXT-X-STREAM-INF:BANDWIDTH=128000,CODECS="mp4a.40.2"`,
			"sound.m3u8",
			`#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2"`,
			"720.m3u8",
			`#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.4d4028,mp4a.40.2"`,
			"1080.m3u8",
			"",
		}, "\n"),
		"/sound.m3u8": "#EXTM3U\n#EXTINF:2,\na.aac\n",
		"/720.m3u8":   "#EXTM3U\n#EXTINF:2,\nb.ts\n",
		"/1080.m3u8":  "#EXTM3U\n#EXTINF:2,\nc.ts\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if want := server.URL + "/1080.m3u8"; role.address != want {
		t.Fatalf("address = %q, want %q", role.address, want)
	}
	if !role.live {
		t.Fatal("a variant without EXT-X-ENDLIST was not reported as live")
	}
}

func TestInspectPlaylistFallsBackToBandwidthWithoutResolutions(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/master.m3u8": strings.Join([]string{
			"#EXTM3U",
			"#EXT-X-STREAM-INF:BANDWIDTH=600000",
			"low.m3u8",
			"#EXT-X-STREAM-INF:BANDWIDTH=2400000",
			"high.m3u8",
			"",
		}, "\n"),
		"/low.m3u8":  "#EXTM3U\n#EXTINF:2,\na.ts\n",
		"/high.m3u8": "#EXTM3U\n#EXTINF:2,\nb.ts\n#EXT-X-ENDLIST\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if want := server.URL + "/high.m3u8"; role.address != want {
		t.Fatalf("address = %q, want %q", role.address, want)
	}
	if role.live {
		t.Fatal("the chosen variant ends, so the stream must not be reported as live")
	}
}

func TestInspectPlaylistRefusesAProtectedStream(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/drm.m3u8": "#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://key\"\n#EXTINF:2,\na.ts\n",
	})
	_, err := inspectPlaylist(context.Background(), server.URL+"/drm.m3u8")
	if !errors.Is(err, errProtectedPlaylist) {
		t.Fatalf("err = %v, want a protected stream refusal", err)
	}
	if err == nil || !strings.Contains(err.Error(), "SAMPLE-AES") {
		t.Fatalf("the refusal should name the method it cannot read, got %v", err)
	}
}

func TestInspectPlaylistRefusesAProtectedVariant(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/master.m3u8": "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=1280x720\n720.m3u8\n",
		"/720.m3u8":    "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:2,\na.ts\n",
	})
	_, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8")
	if !errors.Is(err, errProtectedPlaylist) {
		t.Fatalf("err = %v, want a protected stream refusal", err)
	}
}

func TestInspectPlaylistRejectsSomethingThatIsNotAPlaylist(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/page.m3u8": "<!doctype html><title>not a stream</title>",
	})
	_, err := inspectPlaylist(context.Background(), server.URL+"/page.m3u8")
	if err == nil || !strings.Contains(err.Error(), "not a playlist") {
		t.Fatalf("err = %v, want a refusal naming the body", err)
	}
}

func TestInspectPlaylistReportsABadStatus(t *testing.T) {
	server := servePlaylists(t, map[string]string{})
	_, err := inspectPlaylist(context.Background(), server.URL+"/nothing.m3u8")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the status the origin answered", err)
	}
}

func TestProtectedMethodIgnoresNONEE(t *testing.T) {
	text := "#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n"
	if got := protectedMethod(text); got != "AES-128" {
		t.Fatalf("protectedMethod = %q, want AES-128", got)
	}
	if got := protectedMethod("#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n"); got != "" {
		t.Fatalf("protectedMethod = %q, want no method", got)
	}
}
