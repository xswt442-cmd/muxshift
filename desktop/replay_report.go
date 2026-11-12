package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// replayPhase is where a replay transaction stands. It describes the life of
// the transaction, not the confidence of any number inside it: a transaction
// that measured positions and was then rolled back is still rolled back.
type replayPhase int

const (
	replayPreparing replayPhase = iota
	replayCommitted
	replayRefused
	replayRolledBack
)

func (p replayPhase) String() string {
	switch p {
	case replayPreparing:
		return "preparing"
	case replayCommitted:
		return "committed"
	case replayRefused:
		return "refused"
	case replayRolledBack:
		return "rolled back"
	default:
		return fmt.Sprintf("phase(%d)", int(p))
	}
}

// replayReportSource is what one source was asked for and what its player
// reported, as measured by the transaction. Reported is the player's own
// answer, not the request repeated back.
type replayReportSource struct {
	Source    core.SourceID
	Requested time.Duration
	Reported  time.Duration
	Residual  time.Duration
	Settled   bool
}

// replayReport is an immutable account of one replay transaction: what it
// asked for, what it measured, and how it ended. It is built when the
// transaction changes phase and never edited afterwards, so a caller cannot
// read a half-updated account or turn a rollback into a success by waiting.
type replayReport struct {
	ID      uint64
	Event   time.Duration
	Phase   replayPhase
	Reason  string
	Sources []replayReportSource
}

// Published is the only question a surface may ask to decide whether the
// session switched: a preparing, refused or rolled back transaction is not a
// success, even if it measured positions along the way.
func (r replayReport) Published() bool { return r.Phase == replayCommitted }

// FrameConfirmed is always false: the engine reports positions, and nothing in
// this build confirms that the frame at a position is on screen. The method
// exists so callers cannot mistake "settled" for "confirmed".
func (r replayReport) FrameConfirmed() bool { return false }

// Line is a single log-ready sentence.
func (r replayReport) Line() string {
	var parts []string
	for _, source := range r.Sources {
		if source.Settled {
			parts = append(parts, fmt.Sprintf("%s requested %s reported %s (residual %s)",
				source.Source, source.Requested, source.Reported, source.Residual))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s requested %s, no position", source.Source, source.Requested))
	}
	line := fmt.Sprintf("replay %d for event %s: %s", r.ID, r.Event, r.Phase)
	if r.Reason != "" {
		line += ": " + r.Reason
	}
	if len(parts) > 0 {
		line += "; " + strings.Join(parts, ", ")
	}
	if r.Phase == replayCommitted {
		line += "; position settled, frame not confirmed"
	}
	return line
}

// replaySeq numbers transactions so a report can be tied to one of them.
var replaySeq atomic.Uint64

// reportFor builds the report of a transaction in its current phase. The
// returned value owns its slice: later changes to the transaction do not
// rewrite a report a caller already holds.
func (txn *seekTxn) reportFor(phase replayPhase, reason string) replayReport {
	report := replayReport{ID: txn.id, Event: txn.event, Phase: phase, Reason: reason}
	for _, step := range txn.steps {
		source := replayReportSource{Source: step.item.source.ID, Requested: step.offset}
		if step.measured {
			source.Reported = step.reported
			source.Residual = step.reported - step.offset
			source.Settled = true
		}
		report.Sources = append(report.Sources, source)
	}
	return report
}

// publishReport keeps the newest account for whatever presents replay state.
// It never moves the session back to an older attempt: a transaction that
// finished earlier may report later, and its line still belongs in the log,
// but it must not replace the account of a request that came after it.
func (a *appState) publishReport(report replayReport) {
	a.seekMu.Lock()
	newest := a.notice.ID
	older := newest != 0 && report.ID < newest
	if !older {
		a.notice = report
		newest = report.ID
	}
	a.seekMu.Unlock()
	logf("seek", "%s", report.Line())
	if older {
		logf("seek", "replay %d is older than the newest attempt %d, so it stays out of the session's account", report.ID, newest)
	}
}

// lastReport returns the newest account, if any.
func (a *appState) lastReport() (replayReport, bool) {
	a.seekMu.Lock()
	defer a.seekMu.Unlock()
	if a.notice.ID == 0 {
		return replayReport{}, false
	}
	report := a.notice
	report.Sources = append([]replayReportSource(nil), a.notice.Sources...)
	return report, true
}
