package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// request sends one request with the Host the listener expects. Unlike postJSON
// it does not fail the test from arbitrary goroutines, so it is safe to use in
// the concurrency checks.
func request(server string, method, path, body, contentType string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, server+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Host = strings.TrimPrefix(server, "http://")
	for name, value := range header {
		req.Header.Set(name, value)
	}
	return http.DefaultClient.Do(req)
}

// A code is consumed once, but the token it handed out keeps working: a browser
// that paired yesterday does not lose the window because the page shows a new
// code today.
func TestAPairedTokenSurvivesANewCode(t *testing.T) {
	h, server, added := newTestHandoff(t)

	resp := postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pairing answered %d, want 200", resp.StatusCode)
	}
	var paired struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("read the pairing answer: %v", err)
	}

	h.rotate()

	resp = postJSON(t, server, "/import", `{"url":"https://example.com/after-rotate.m3u8"}`,
		map[string]string{"X-MuxShift-Handoff": paired.Token})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("an import with the earlier token answered %d, want 202", resp.StatusCode)
	}
	if len(*added) != 1 || (*added)[0] != "https://example.com/after-rotate.m3u8" {
		t.Fatalf("the add command saw %v, want the address handed over after a new code", *added)
	}
}

// Two browsers can race for the same code. Exactly one wins; the other is told
// the code is no longer valid instead of silently sharing the token.
func TestOneCodePairsOnlyOnceWhenBrowsersRace(t *testing.T) {
	h, _, _ := newTestHandoff(t)
	server := newLoopbackServer(t, h)

	const callers = 8
	codes := make(chan int, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := request(server, http.MethodPost, "/pair", `{"code":"`+h.code+`"}`, "application/json", nil)
			if err != nil {
				codes <- -1
				return
			}
			defer resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)

	paired, refused := 0, 0
	for code := range codes {
		switch code {
		case http.StatusOK:
			paired++
		case http.StatusForbidden:
			refused++
		default:
			t.Fatalf("a racing pair answered %d, want 200 or 403", code)
		}
	}
	if paired != 1 || refused != callers-1 {
		t.Fatalf("the race paired %d and refused %d, want exactly one pairing", paired, refused)
	}
}

// Each endpoint answers only the method it serves.
func TestEachEndpointRefusesTheMethodItDoesNotServe(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/pair", ""},
		{http.MethodGet, "/import", ""},
		{http.MethodPost, "/health", `{}`},
	}
	for _, tc := range cases {
		resp, err := request(server.URL, tc.method, tc.path, tc.body, "application/json", map[string]string{"X-MuxShift-Handoff": h.secret})
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s answered %d, want 405", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// An empty or missing address never reaches the add command.
func TestAnImportWithoutAnAddressIsRefused(t *testing.T) {
	h, server, added := newTestHandoff(t)
	for _, body := range []string{`{}`, `{"url":""}`, `{"url":"   "}`, `{"url":"\t"}`} {
		resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s answered %d, want 400", body, resp.StatusCode)
		}
	}
	if len(*added) != 0 {
		t.Fatalf("the add command saw %v, want nothing", *added)
	}
}

// What the add command says goes back to the browser: a refusal with its reason,
// or the accepted message the popup shows.
func TestAnImportTellsWhatTheCommandDid(t *testing.T) {
	h := &handoffServer{
		secret: strings.Repeat("test-token-", 4),
		add: func(string) commandResult {
			return commandResult{Err: errors.New("the player could not open it")}
		},
	}
	h.rotate()
	server := httptest.NewServer(h.routes())
	h.addr = server.Listener.Addr().String()
	t.Cleanup(server.Close)

	resp := postJSON(t, server, "/import", `{"url":"https://example.com/broken.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a refused add answered %d, want 400", resp.StatusCode)
	}
	var refused struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refused); err != nil {
		t.Fatalf("read the refusal: %v", err)
	}
	if refused.Accepted || !strings.Contains(refused.Reason, "could not open") {
		t.Fatalf("the refusal said %+v, want accepted=false and the command's reason", refused)
	}
}

// A control character in the address is refused before it can reach the command.
func TestAnImportRefusesAControlCharacterInTheAddress(t *testing.T) {
	h, server, added := newTestHandoff(t)
	resp := postJSON(t, server, "/import", `{"url":"https://example.com/a\nb.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an address with a newline answered %d, want 400", resp.StatusCode)
	}
	if len(*added) != 0 {
		t.Fatalf("the add command saw %v, want nothing", *added)
	}
}

// The body has to say it is JSON; otherwise even a correct code is not read.
func TestTheBodyMustSayItIsJSON(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	resp, err := request(server.URL, http.MethodPost, "/pair", `{"code":"`+h.code+`"}`, "text/plain", nil)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a text/plain body answered %d, want 400", resp.StatusCode)
	}
}

// The port is checked before anything is listed, so a bad number fails loudly.
func TestStartHandoffRefusesAPortOutsideTheRange(t *testing.T) {
	for _, port := range []int{-1, 65536, 70000} {
		if _, err := startHandoff(&appState{}, port); err == nil {
			t.Fatalf("port %d was accepted", port)
		}
	}
}

// Closing the listener really closes it: the port stops answering, which is what
// makes a second window's refusal honest.
func TestStoppingTheListenerReleasesThePort(t *testing.T) {
	h, err := startHandoff(&appState{}, 0)
	if err != nil {
		t.Fatalf("start the listener: %v", err)
	}
	resp, err := http.Get("http://" + h.addr + "/health")
	if err != nil {
		t.Fatalf("health before stopping: %v", err)
	}
	resp.Body.Close()

	h.stop()

	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", h.addr, 200*time.Millisecond)
		if dialErr != nil {
			return
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("%s still accepts connections after stop", h.addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newLoopbackServer serves the same handler in front of an address the requests
// can be stamped with, for tests that need their own client and goroutines.
func newLoopbackServer(t *testing.T, h *handoffServer) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h.addr = listener.Addr().String()
	server := &http.Server{Handler: h.routes()}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return "http://" + h.addr
}
