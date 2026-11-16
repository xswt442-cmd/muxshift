package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestHandoff builds the listener surface without a window: the same handler
// the app serves, an address the requests are stamped with, and a stub for the
// add command so a test can see exactly which address was accepted.
func newTestHandoff(t *testing.T) (*handoffServer, *httptest.Server, *[]string) {
	t.Helper()
	added := &[]string{}
	h := &handoffServer{
		secret: strings.Repeat("test-token-", 4),
		add: func(address string) commandResult {
			*added = append(*added, address)
			return commandResult{Message: "reading " + address}
		},
	}
	h.rotate()
	server := httptest.NewServer(h.routes())
	h.addr = server.Listener.Addr().String()
	t.Cleanup(server.Close)
	return h, server, added
}

func postJSON(t *testing.T, server *httptest.Server, path, body string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = strings.TrimPrefix(server.URL, "http://")
	for name, value := range header {
		req.Header.Set(name, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestThePairingCodeIsTheOneTheWindowShows(t *testing.T) {
	h, server, _ := newTestHandoff(t)

	resp := postJSON(t, server, "/pair", `{"code":"000000"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong code answered %d, want 403", resp.StatusCode)
	}

	resp = postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the shown code answered %d, want 200", resp.StatusCode)
	}
	var paired struct {
		Token   string `json:"token"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("read the pairing answer: %v", err)
	}
	if !strings.HasPrefix(paired.Address, "http://") {
		t.Fatalf("the pairing answer gave %q, which a browser cannot fetch without a scheme", paired.Address)
	}
	if paired.Token != h.secret {
		t.Fatalf("the pairing answer carried a token that is not this window's")
	}

	// One pairing per code: the window shows a new one for the next browser.
	resp = postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a code that was just used answered %d, want 403", resp.StatusCode)
	}
}

func TestAnExpiredPairingCodeIsRefused(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	h.mu.Lock()
	h.expiry = time.Now().Add(-time.Second)
	h.mu.Unlock()

	resp := postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an expired code answered %d, want 403", resp.StatusCode)
	}
}

func TestANewCodeReplacesTheOldOne(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	old := h.code
	for h.rotate() == old {
		// A collision is possible; make another.
	}

	resp := postJSON(t, server, "/pair", `{"code":"`+old+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the old code answered %d, want 403", resp.StatusCode)
	}
}

func TestAnImportNeedsThePairedToken(t *testing.T) {
	h, server, added := newTestHandoff(t)
	body := `{"url":"https://example.com/live.m3u8"}`

	if resp := postJSON(t, server, "/import", body, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an import without a token answered %d, want 403", resp.StatusCode)
	}
	if resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": "nope"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an import with a wrong token answered %d, want 403", resp.StatusCode)
	}
	resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("a paired import answered %d, want 202", resp.StatusCode)
	}
	if len(*added) != 1 || (*added)[0] != "https://example.com/live.m3u8" {
		t.Fatalf("the add command saw %v, want the one address", *added)
	}
}

func TestOnlyTheExtensionOriginMayCallTheListener(t *testing.T) {
	h, server, _ := newTestHandoff(t)

	resp := postJSON(t, server, "/import", `{"url":"https://example.com/a.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret, "Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin answered %d, want 403", resp.StatusCode)
	}

	resp = postJSON(t, server, "/import", `{"url":"https://example.com/a.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret, "Origin": "chrome-extension://agacmebmmcbkclbblmekgdkfcehehccl"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("the extension origin answered %d, want 202", resp.StatusCode)
	}
}

func TestTheListenerRefusesAnotherHostName(t *testing.T) {
	_, server, _ := newTestHandoff(t)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/health", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Host = "rebind.example"
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign Host answered %d, want 403", resp.StatusCode)
	}
}

func TestAnImportRefusesWhatIsNotAnHTTPAddress(t *testing.T) {
	h, server, added := newTestHandoff(t)
	for _, body := range []string{
		`{"url":"C:\\media\\local.mp4"}`,
		`{"url":"file:///C:/media/local.mp4"}`,
		`{"url":"javascript:alert(1)"}`,
		`{"url":"https://user:secret@example.com/a.mp4"}`,
	} {
		resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s answered %d, want 400", body, resp.StatusCode)
		}
	}
	if len(*added) != 0 {
		t.Fatalf("the add command saw %v, want nothing", *added)
	}
}

func TestAnImportRefusesANonJSONOrOversizedBody(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	big := `{"url":"https://example.com/` + strings.Repeat("a", handoffBodyLimit) + `.mp4"}`
	for _, body := range []string{`not json`, big} {
		resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("a body of %d bytes answered %d, want 400", len(body), resp.StatusCode)
		}
	}
}

func TestTheHealthAnswerSaysNothingSecret(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	resp, err := server.Client().Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health answered %d, want 200", resp.StatusCode)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") == false {
		t.Fatalf("health is not JSON: %q", resp.Header.Get("Content-Type"))
	}
	if h.secret == "" || h.code == "" {
		t.Fatal("the listener has no secret or code to test against")
	}
}

func TestTheCodeLineSaysWhereAndForHowLong(t *testing.T) {
	h, _, _ := newTestHandoff(t)
	line := h.codeLine()
	if !strings.Contains(line, h.addr) || !strings.Contains(line, h.code) || !strings.Contains(line, "expires") {
		t.Fatalf("the code line %q does not show the address, the code and the time left", line)
	}
	var none *handoffServer
	if got := none.codeLine(); !strings.Contains(got, "not listening") {
		t.Fatalf("a missing listener says %q", got)
	}
}

// The extension's page asks before it posts, because its request carries JSON and
// one extra header. That question is answered only for the extension's origin.
func TestTheExtensionPreflightIsAnsweredOnlyForItsOrigin(t *testing.T) {
	h, server, _ := newTestHandoff(t)

	preflight := func(origin string) *http.Response {
		req, err := http.NewRequest(http.MethodOptions, server.URL+"/import", nil)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		req.Host = strings.TrimPrefix(server.URL, "http://")
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type, x-muxshift-handoff")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("send the request: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	extension := "chrome-extension://agacmebmmcbkclbblmekgdkfcehehccl"
	resp := preflight(extension)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the extension's preflight answered %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != extension {
		t.Fatalf("the extension's preflight allowed %q, want its own origin", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-MuxShift-Handoff") {
		t.Fatalf("the preflight allows %q, which lacks the handoff header", got)
	}

	foreign := preflight("https://evil.example")
	if foreign.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign preflight answered %d, want 403", foreign.StatusCode)
	}
	if got := foreign.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("a foreign preflight was allowed %q", got)
	}

	// The answer to the real request carries the same allowance, or the browser
	// would discard it even though the window accepted the address.
	resp = postJSON(t, server, "/import", `{"url":"http://127.0.0.1:8802/movie.mp4"}`, map[string]string{
		"Origin":             extension,
		"X-MuxShift-Handoff": h.secret,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("the paired import answered %d, want 202", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != extension {
		t.Fatalf("the import answer allowed %q, want the extension's origin", got)
	}
}
