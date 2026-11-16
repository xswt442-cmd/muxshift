package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

// TestAnAddressIsClassifiedByWhatItServes covers the decision that keeps an MP4
// out of the playlist parser and a web page out of the player. The bytes and the
// declared type are both used, and neither one alone is trusted.
func TestAnAddressIsClassifiedByWhatItServes(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...)
	cases := []struct {
		name        string
		contentType string
		body        []byte
		want        addressKind
		wantErr     string
	}{
		{"a playlist by its body", "application/octet-stream", []byte("#EXTM3U\r\n#EXT-X-VERSION:3\r\n"), addressPlaylist, ""},
		{"a playlist by its type", "application/vnd.apple.mpegurl; charset=utf-8", []byte("not obviously a playlist\n"), addressPlaylist, ""},
		{"a playlist with a byte order mark", "text/plain", []byte("\xef\xbb\xbf#EXTM3U\n"), addressPlaylist, ""},
		{"an mp4 by its box", "application/octet-stream", mp4, addressDirect, ""},
		{"an mp4 by its type", "video/mp4", []byte("anything"), addressDirect, ""},
		{"a transport stream by its sync byte", "application/octet-stream", transportStream(), addressDirect, ""},
		{"a web page by its type", "text/html; charset=utf-8", []byte("<p>hello</p>"), 0, "web page"},
		{"a web page by its body", "application/octet-stream", []byte("<!DOCTYPE html><html></html>"), 0, "web page"},
		{"a json body", "application/json", []byte(`{"ok":true}`), 0, "does not know how to play"},
		{"an untyped unknown body", "", []byte("\x00\x01\x02\x03"), 0, "states no content type"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, err := classifyAddress(c.contentType, c.body)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("%q was accepted as a kind", c.contentType)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("the refusal says %q, want it to mention %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("the address was refused: %v", err)
			}
			if kind != c.want {
				t.Fatalf("the address was read as %v, want %v", kind, c.want)
			}
		})
	}
}

// TestSniffReadsOnlyTheFirstBytes checks the request and the refusals around it:
// a small ranged read, no credentials, and the server's own status when it says
// no.
func TestSniffReadsOnlyTheFirstBytes(t *testing.T) {
	var rangeHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader = r.Header.Get("Range")
		switch r.URL.Path {
		case "/playlist.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
		case "/movie.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><html><body>not media</body></html>")
		case "/broken":
			http.Error(w, "the origin is having a bad day", http.StatusInternalServerError)
		case "/to-page":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/to-nothing":
			http.Redirect(w, r, "/nothing", http.StatusTemporaryRedirect)
		case "/to-file":
			w.Header().Set("Location", "file:///C:/Windows/win.ini")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	kind, err := sniffAddress(context.Background(), server.URL+"/playlist.m3u8")
	if err != nil {
		t.Fatalf("a playlist was refused: %v", err)
	}
	if kind != addressPlaylist {
		t.Fatalf("the playlist was read as %v", kind)
	}
	if !strings.HasPrefix(rangeHeader, "bytes=0-") {
		t.Fatalf("the read asked for %q, want a small leading range", rangeHeader)
	}

	kind, err = sniffAddress(context.Background(), server.URL+"/movie.mp4")
	if err != nil {
		t.Fatalf("an mp4 was refused: %v", err)
	}
	if kind != addressDirect {
		t.Fatalf("the mp4 was read as %v", kind)
	}

	if _, err := sniffAddress(context.Background(), server.URL+"/page"); err == nil || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("a web page was not refused as one: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/nothing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a missing address did not report the server's status: %v", err)
	}
	if _, err := sniffAddress(context.Background(), "http://user:secret@"+strings.TrimPrefix(server.URL, "http://")+"/movie.mp4"); err == nil || !strings.Contains(err.Error(), "user name") {
		t.Fatalf("an address with credentials was not refused: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/broken"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a 500 did not report the server's status: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/to-page"); err == nil || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("a redirect to a web page was not refused as one: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/to-nothing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a redirect to a missing address did not report the status: %v", err)
	}
	// The http client itself refuses the scheme before this package sees the
	// response; either wording is a refusal, and nothing below http is read.
	if _, err := sniffAddress(context.Background(), server.URL+"/to-file"); err == nil ||
		(!strings.Contains(err.Error(), "not an http or https address") && !strings.Contains(err.Error(), "unsupported protocol scheme")) {
		t.Fatalf("a redirect away from http was not refused: %v", err)
	}
}

// TestADirectAddressIsRecordedLikeAFile checks that a plain media address gives
// the session the same two labelled streams a local file gets, rather than a
// guessed track list.
func TestADirectAddressIsRecordedLikeAFile(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("the app state was not built: %v", err)
	}
	it := &item{uri: "http://127.0.0.1:1/movie.mp4"}
	if err := app.addDirect(it); err != nil {
		t.Fatalf("a direct address was refused: %v", err)
	}
	if it.source.ID == "" {
		t.Fatal("the address produced no source")
	}
	var tracks []core.Track
	for _, track := range app.session.Tracks() {
		if track.Source == it.source.ID {
			tracks = append(tracks, track)
		}
	}
	if len(tracks) != 2 {
		t.Fatalf("the address produced %d tracks, want 2", len(tracks))
	}
	for _, track := range tracks {
		if !strings.Contains(track.Label, "does not enumerate") {
			t.Fatalf("%s is labelled %q, which does not say where it came from", track.ID, track.Label)
		}
	}
}

// transportStream builds enough of a transport stream for the sync-byte test.
func transportStream() []byte {
	head := make([]byte, 200)
	head[0] = 0x47
	head[188] = 0x47
	return head
}
