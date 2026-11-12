package main

// The background failure ledger: what went wrong away from the drawing thread,
// counted for the run rather than only written to the log. Saving a remembered
// sound, saving the window's shape and measuring a recording's anchor all
// happen on worker goroutines, where a failure has no card to land on; without
// a count the only record is a log line the person has to think to open. The
// status bar still shows the newest error and still has its Dismiss, but the
// count belongs to the run: dismissing a message is not a repair.

import (
	"fmt"
	"log"
	"time"
)

// backgroundFailure is one thing that went wrong away from the drawing thread.
type backgroundFailure struct {
	what string
	err  error
	at   time.Time
}

// line describes the newest failure, or says the ledger is empty.
func (f backgroundFailure) line() string {
	if f.what == "" {
		return "nothing has failed in the background this run"
	}
	return fmt.Sprintf("%s failed at %s: %v", f.what, f.at.Format("15:04:05"), f.err)
}

// noteBackgroundFailure records one failure. Worker goroutines call it; the
// page reads the ledger through backgroundFailures on the drawing thread.
func (a *appState) noteBackgroundFailure(what string, err error) {
	if err == nil {
		return
	}
	a.bgMu.Lock()
	a.bgFailures++
	a.bgLast = backgroundFailure{what: what, err: err, at: time.Now()}
	count := a.bgFailures
	a.bgMu.Unlock()
	log.Printf("background: %s failed (%d this run): %v", what, count, err)
}

// backgroundFailures returns how many background failures this run has seen
// and the newest one. The zero value's line() says the ledger is empty.
func (a *appState) backgroundFailures() (int, backgroundFailure) {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	return a.bgFailures, a.bgLast
}

// dismissError clears the message the status bar shows. The ledger keeps its
// count on purpose: the person asked the notification to go away, not the
// record of what happened.
func (a *appState) dismissError() {
	if a.lastErr == nil {
		return
	}
	a.lastErr = nil
	count, _ := a.backgroundFailures()
	log.Printf("error: the notification was dismissed; %d background failure(s) stay counted", count)
}
