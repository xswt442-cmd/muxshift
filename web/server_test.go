package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardMutationRejectsAnotherOrigin checks that a state change carrying a
// cookie from this page but an Origin from somewhere else is refused, which is
// what keeps another site from driving the scene through the browser.
func TestGuardMutationRejectsAnotherOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		status int
	}{
		{"same origin", "http://127.0.0.1:8812", http.StatusOK},
		{"another host", "https://evil.example", http.StatusForbidden},
		{"another port", "http://127.0.0.1:9999", http.StatusForbidden},
		{"null origin", "null", http.StatusForbidden},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/scene", strings.NewReader("{}"))
			request.Host = "127.0.0.1:8812"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			recorder := httptest.NewRecorder()

			if !guardMutation(recorder, request) {
				if recorder.Code != test.status {
					t.Fatalf("status = %d, want %d", recorder.Code, test.status)
				}
				return
			}
			if test.status != http.StatusOK {
				t.Fatalf("request was accepted, want status %d", test.status)
			}
		})
	}
}

// TestGuardMutationRequiresJSON checks that a plain form post, which a
// cross-site page can send without a preflight, is refused before the body is
// read.
func TestGuardMutationRequiresJSON(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/scene", strings.NewReader("video=source-1"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()

	if guardMutation(recorder, request) {
		t.Fatal("a form post was accepted")
	}
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnsupportedMediaType)
	}
}

// TestChangeRejectsInvalidVolumeAndKeepsScene checks that an invalid request
// leaves the previous scene in place instead of half applying a change.
func TestChangeRejectsInvalidVolumeAndKeepsScene(t *testing.T) {
	state, err := newSessionState([]string{"/media/a/index.m3u8", "/media/b/index.m3u8"})
	if err != nil {
		t.Fatalf("newSessionState: %v", err)
	}
	before, err := state.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	second := before.Sources[1]
	if _, err := state.change(changeScene{Video: second.ID, Audio: second.ID, Volume: 5}); err == nil {
		t.Fatal("a volume of 5 was accepted")
	}
	after, err := state.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if after.Video != before.Video {
		t.Fatalf("video = %q, want %q", after.Video, before.Video)
	}
	check := func(view view) map[string]srcView {
		entries := map[string]srcView{}
		for _, entry := range view.Sources {
			entries[entry.ID] = entry
		}
		return entries
	}
	beforeSources, afterSources := check(before), check(after)
	if len(afterSources) != len(beforeSources) {
		t.Fatalf("sources = %d, want %d", len(afterSources), len(beforeSources))
	}
	for id, want := range beforeSources {
		got, ok := afterSources[id]
		if !ok {
			t.Fatalf("source %s disappeared", id)
		}
		if got != want {
			t.Fatalf("source %s = %+v, want %+v", id, got, want)
		}
	}
}

// TestAuthorizedAcceptsOnlyTheToken checks the three ways the token may arrive
// and that a wrong one is refused.
func TestAuthorizedAcceptsOnlyTheToken(t *testing.T) {
	const token = "bridgetoken"
	request := httptest.NewRequest(http.MethodGet, "/api/session?token="+token, nil)
	if !authorized(request, token) {
		t.Fatal("a query token was refused")
	}
	wrong := httptest.NewRequest(http.MethodGet, "/api/session?token=other", nil)
	if authorized(wrong, token) {
		t.Fatal("a wrong query token was accepted")
	}
	header := httptest.NewRequest(http.MethodPost, "/api/import", nil)
	header.Header.Set("X-MuxShift-Token", token)
	if !authorized(header, token) {
		t.Fatal("a header token was refused")
	}
	cookie := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	cookie.AddCookie(&http.Cookie{Name: "muxshift_token", Value: token})
	if !authorized(cookie, token) {
		t.Fatal("a cookie token was refused")
	}
	empty := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	if authorized(empty, token) {
		t.Fatal("a request without a token was accepted")
	}
}

// TestListenAddressStaysOnLoopback checks that -addr cannot turn the session
// into a network listener.
func TestListenAddressStaysOnLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "localhost:8812", "[::1]:0"} {
		if err := checkListenAddress(addr); err != nil {
			t.Fatalf("checkListenAddress(%q) = %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:8812", ":8812", "192.168.1.10:8812", "127.0.0.1"} {
		if err := checkListenAddress(addr); err == nil {
			t.Fatalf("checkListenAddress(%q) was accepted", addr)
		}
	}
	if !loopbackHost("127.0.0.1:8812") || !loopbackHost("localhost") {
		t.Fatal("a loopback host was refused")
	}
	if loopbackHost("evil.example") || loopbackHost("10.0.0.2:8812") {
		t.Fatal("a non-loopback host was accepted")
	}
}

// TestServeMediaStaysInsideRoot checks that neither a parent path nor a link
// inside the media root can read a file outside it.
func TestServeMediaStaysInsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	playlist := filepath.Join(root, "hls")
	if err := os.MkdirAll(playlist, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(playlist, "index.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatalf("write playlist: %v", err)
	}
	outside := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(outside, []byte("do not read me\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(playlist, "link.txt")); err != nil {
		t.Logf("symlink unavailable on this machine: %v", err)
	}

	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer handle.Close()

	serve := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		serveMedia(recorder, httptest.NewRequest(http.MethodGet, path, nil), handle)
		return recorder
	}

	ok := serve("/media/hls/index.m3u8")
	if ok.Code != http.StatusOK {
		t.Fatalf("playlist status = %d, want 200", ok.Code)
	}
	if got := ok.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Fatalf("content type = %q", got)
	}
	for _, path := range []string{"/media/../secret.txt", "/media/hls/../../secret.txt", "/media/", "/media/hls"} {
		if recorder := serve(path); recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, recorder.Code)
		}
	}
	if _, err := os.Lstat(filepath.Join(playlist, "link.txt")); err == nil {
		if recorder := serve("/media/hls/link.txt"); recorder.Code != http.StatusNotFound {
			t.Fatalf("a link out of the root returned %d, want 404", recorder.Code)
		}
	}
}

// TestSourceURLKeepsAbsoluteAddresses checks that an imported address is not
// wrapped in /media/ again, which is what made hls.js load the wrong path.
func TestSourceURLKeepsAbsoluteAddresses(t *testing.T) {
	if got := sourceURL("/media/hls/index.m3u8"); got != "/media/hls/index.m3u8" {
		t.Fatalf("served path = %q", got)
	}
	const imported = "https://example.net/live/index.m3u8"
	if got := sourceURL(imported); got != imported {
		t.Fatalf("imported address = %q, want %q", got, imported)
	}
}
