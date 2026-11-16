package main

// P5: the browser hands one media address to this window over a loopback HTTP
// listener the desktop owns. The extension pairs once with a code the person
// reads from the desktop, keeps the token, and later posts one address at a
// time. Nothing is shared through files, no cookie or Referer is invented, and
// the socket is reachable from this machine only.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	handoffBodyLimit = 8 << 10
	handoffPairTTL   = 2 * time.Minute
)

type handoffServer struct {
	addr   string
	secret string
	// add is the window's own add command, handed in when the listener starts so
	// an accepted address takes exactly the path the chooser's Add takes.
	add func(string) commandResult
	// onMain runs the add on the drawing thread; nil means call it directly,
	// which only a test does.
	onMain func(func())

	mu     sync.Mutex
	code   string
	expiry time.Time
	server *http.Server
}

// startHandoff listens on the loopback address the extension is told about. A
// port already in use is reported as a blocked reason instead of being retried
// elsewhere: the extension has one address, and silently moving it would leave
// the person pairing with a listener that is not this window.
func startHandoff(a *appState, port int) (*handoffServer, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("the handoff port %d is not a port this build can listen on", port)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("the browser handoff could not listen on 127.0.0.1:%d: %w", port, err)
	}
	secret, err := randomHex(32)
	if err != nil {
		listener.Close()
		return nil, err
	}
	h := &handoffServer{addr: listener.Addr().String(), secret: secret, add: a.handoffAdd, onMain: mygoRunOnMain}
	h.rotate()
	h.server = &http.Server{
		Handler:           h.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() {
		if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("handoff: the listener stopped: %v", err)
		}
	}()
	a.handoff = h
	log.Printf("handoff: listening on %s for the browser extension", h.addr)
	return h, nil
}

// routes is the whole surface a browser can reach, behind the loopback guard.
func (h *handoffServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/pair", h.pair)
	mux.HandleFunc("/import", h.importAddress)
	return h.localOnly(mux)
}

// stop closes the listener. The token and the pairing code live in this process
// only, so there is nothing on disk to clean up.
func (h *handoffServer) stop() {
	if h == nil || h.server == nil {
		return
	}
	_ = h.server.Close()
}

// rotate makes a new pairing code, valid for a short while.
func (h *handoffServer) rotate() string {
	code, err := randomDigits(6)
	if err != nil {
		code = "000000"
	}
	h.mu.Lock()
	h.code = code
	h.expiry = time.Now().Add(handoffPairTTL)
	h.mu.Unlock()
	return code
}

// codeLine is what the diagnostics page shows: where the listener is and which
// code a browser may pair with right now.
func (h *handoffServer) codeLine() string {
	if h == nil {
		return "the browser handoff is not listening"
	}
	h.mu.Lock()
	code, expiry := h.code, h.expiry
	h.mu.Unlock()
	left := time.Until(expiry).Round(time.Second)
	if left < 0 {
		return h.addr + " · the pairing code has expired; make a new one"
	}
	return fmt.Sprintf("%s · pairing code %s (expires in %s)", h.addr, code, left)
}

// localOnly refuses anything that did not arrive with the exact Host the
// listener was given: a DNS rebinding attempt carries another Host header, and
// an off-machine request cannot reach a loopback socket at all.
func (h *handoffServer) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != h.addr {
			http.Error(w, "this listener only answers its own address", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !strings.HasPrefix(origin, "chrome-extension://") {
			http.Error(w, "only the MuxShift extension may call this listener", http.StatusForbidden)
			return
		}
		// The extension's pages send a JSON post and one extra header, so the
		// browser asks about the request first and then checks the answer. Both
		// halves are allowed only for the extension's own origin.
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "chrome-extension://") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-MuxShift-Handoff")
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handoffServer) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": "muxshift"})
}

func (h *handoffServer) tokenMatches(token string) bool {
	if len(token) != len(h.secret) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.secret)) == 1
}

func (h *handoffServer) pair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	ok := time.Now().Before(h.expiry) && strings.TrimSpace(body.Code) == h.code
	if ok {
		h.expiry = time.Now().Add(-time.Second)
	}
	h.mu.Unlock()
	if !ok {
		http.Error(w, "that is not the pairing code this window is showing, or it has expired", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": h.secret, "address": "http://" + h.addr})
}

// importAddress accepts one address at a time. The answer says the address was
// accepted by the add command; it does not claim the picture plays.
func (h *handoffServer) importAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	if !h.tokenMatches(r.Header.Get("X-MuxShift-Handoff")) {
		http.Error(w, "this window has not paired with that token", http.StatusForbidden)
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	address := strings.TrimSpace(body.URL)
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		http.Error(w, "a browser can only hand over an http or https address", http.StatusBadRequest)
		return
	}
	if err := mediaAddressProblem(address); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var out commandResult
	if h.onMain == nil {
		// Tests have no window loop; the product always has one.
		out = h.add(address)
	} else {
		done := make(chan struct{})
		h.onMain(func() {
			out = h.add(address)
			close(done)
		})
		<-done
	}
	if out.Err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"accepted": false, "reason": out.Err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "message": out.Message})
}

// handoffAdd is the one path an accepted handoff takes: the same add command the
// window's own chooser uses. It runs on the draw thread like every other command.
func (a *appState) handoffAdd(address string) commandResult {
	result := a.runCommand(command{Kind: commandAdd, URI: address})
	a.report(result)
	return result
}

func readJSON(r *http.Request, into any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return fmt.Errorf("the body must be application/json")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, handoffBodyLimit+1))
	if err != nil {
		return fmt.Errorf("the body could not be read: %w", err)
	}
	if len(data) > handoffBodyLimit {
		return fmt.Errorf("the body is larger than %d bytes", handoffBodyLimit)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("the body is not the JSON this endpoint expects: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("no randomness for the handoff token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func randomDigits(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	digits := make([]byte, n)
	for i, b := range buf {
		digits[i] = '0' + b%10
	}
	return string(digits), nil
}
