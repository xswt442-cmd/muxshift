package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failure to open a source is stored for the window: the item keeps it, the
// session keeps it as its last error, and the status bar and the tooltip show
// it. A signed address in that text must not survive, while the reason stays.
func TestASafeOpenErrorKeepsTheChainButNotTheSecret(t *testing.T) {
	refused := errors.New(`open "http://127.0.0.1:9/live/index.m3u8?token=TOP_SECRET_123#key": 503 Service Unavailable`)
	safe := safeOpenError(refused)

	if strings.Contains(safe.Error(), "TOP_SECRET_123") || strings.Contains(safe.Error(), "?token=") {
		t.Fatalf("the stored error repeats the secret: %q", safe.Error())
	}
	if !strings.Contains(safe.Error(), "503 Service Unavailable") {
		t.Fatalf("the stored error lost the reason: %q", safe.Error())
	}
	if !errors.Is(safe, refused) {
		t.Fatalf("the stored error no longer wraps the one it came from")
	}
	if safeOpenError(nil) != nil {
		t.Fatal("a missing error became a present one")
	}
	plain := errors.New("the file is not there")
	if got := safeOpenError(plain); got != plain {
		t.Fatalf("an error with nothing to redact was replaced: %v", got)
	}
}

// The same through the path a start-up takes: a signed playlist address the
// origin refuses leaves a safe open error on the item.
func TestAFailedProbeLeavesASafeOpenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	it := &item{uri: server.URL + "/live/index.m3u8?token=TOP_SECRET_123"}
	if err := app.addProbed(it); err == nil {
		t.Fatal("a refused playlist opened")
	}
	if it.openErr == nil {
		t.Fatal("the item kept no open error")
	}
	if strings.Contains(it.openErr.Error(), "TOP_SECRET_123") {
		t.Fatalf("the item's open error repeats the token: %q", it.openErr.Error())
	}
	if !strings.Contains(errorString(it.openErr), "503") {
		t.Fatalf("the reported error lost the status: %q", errorString(it.openErr))
	}
}
