package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/xswt442-cmd/muxshift/core"
)

// alignReport is what a probe found, in the terms a person needs to judge it:
// which two sources were compared, the windows that were read, how much of
// them matched, how strong the match was, what it suggests, what the timeline
// currently holds, and whether the session has moved since.
//
// It is a value derived from a finished suggestion and the session as it is
// now. Deriving it never writes a shift, so showing a report cannot align
// anything by itself.
type alignReport struct {
	Reference  alignWindow
	Target     alignWindow
	Peak       float64
	RunnerUp   float64
	RunnerAt   time.Duration
	Confidence float64
	Overlap    time.Duration
	Common     time.Duration
	Suggested  time.Duration
	Current    time.Duration
	Delta      time.Duration
	Elapsed    time.Duration
	Accepted   bool
	Reason     string
	Expired    string
}

// reportOf turns a finished suggestion into the report the inspector shows.
func (a *appState) reportOf(s alignSuggestion) alignReport {
	common := s.Reference.Length
	if s.Target.Length < common {
		common = s.Target.Length
	}
	return alignReport{
		Reference:  s.Reference,
		Target:     s.Target,
		Peak:       s.Result.Peak,
		RunnerUp:   s.Result.RunnerUp,
		RunnerAt:   s.Result.RunnerOffset,
		Confidence: s.Result.Confidence,
		Overlap:    s.Result.Overlap,
		Common:     common,
		Suggested:  s.CoreOffset,
		Current:    s.Current,
		Delta:      s.Delta,
		Elapsed:    s.Elapsed,
		Accepted:   s.Result.Accepted,
		Reason:     s.Result.Reason,
		Expired:    a.fresh(s),
	}
}

// state names what the report can be used for right now. A refused comparison
// and one the session has outgrown are different from a proposal that still
// describes the session.
func (r alignReport) state() string {
	switch {
	case !r.Accepted:
		return "refused"
	case r.Expired != "":
		return "expired"
	default:
		return "proposed"
	}
}

// lines renders the report as short rows: identity, windows, strength,
// proposal, and what it is worth now.
func (r alignReport) lines() []string {
	first := fmt.Sprintf("%s (%s) compared with %s (%s) · %s vs %s",
		fmt.Sprintf("source %d", r.Target.Index+1), trackName(r.Target.Track),
		fmt.Sprintf("source %d", r.Reference.Index+1), trackName(r.Reference.Track),
		shortURI(r.Target.URI), shortURI(r.Reference.URI))
	windows := fmt.Sprintf("windows %s..%s · %s..%s · matched %s of %s",
		roundMS(r.Target.Start), roundMS(r.Target.Start+r.Target.Length),
		roundMS(r.Reference.Start), roundMS(r.Reference.Start+r.Reference.Length),
		roundMS(r.Overlap), roundMS(r.Common))
	strength := fmt.Sprintf("peak %.3f · next %.3f at %+.3fs · confidence %.3f",
		r.Peak, r.RunnerUp, r.RunnerAt.Seconds(), r.Confidence)
	lines := []string{first, windows, strength}
	if !r.Accepted {
		lines = append(lines, fmt.Sprintf("no suggestion: %s · took %s", r.Reason, r.Elapsed.Round(time.Millisecond)))
	} else {
		adjective := "now"
		if r.Expired != "" {
			adjective = "then"
		}
		lines = append(lines, fmt.Sprintf("suggested %+.3fs · %s %+.3fs · difference %+.3fs · took %s",
			r.Suggested.Seconds(), adjective, r.Current.Seconds(), r.Delta.Seconds(), r.Elapsed.Round(time.Millisecond)))
	}
	if r.Expired != "" {
		lines = append(lines, "expired: "+r.Expired)
	}
	return lines
}

// alignLines is what the inspector draws: the newest probe, described in the
// terms above. It reads state and changes none.
//
// A comparison that did not finish is named after its own sources and put first;
// an older proposal may follow, but only labelled as an earlier comparison, so a
// failure can never be read as having been measured on the older target.
func (a *appState) alignLines() []string {
	var out []string
	if failure := a.lastProbeFail; failure != nil {
		out = append(out, "the last comparison did not finish: "+
			windowLabel(failure.Target)+" compared with "+windowLabel(failure.Reference)+": "+failure.Reason)
		if hasProbe(a.lastProbe) {
			out = append(out, "what it would have replaced (an earlier comparison):")
		}
	}
	if hasProbe(a.lastProbe) {
		out = append(out, a.reportOf(a.lastProbe).lines()...)
	}
	if len(out) == 0 && a.lastProbeErr != "" {
		out = append(out, "the last comparison did not finish: "+a.lastProbeErr)
	}
	return out
}

// alignState is what the inspector colours by. A fresh failure outranks an
// older proposal's own state: the failure is the news, and it must not borrow
// the colour of the proposal it would have replaced.
func (a *appState) alignState() string {
	if a.lastProbeFail != nil || a.lastProbeErr != "" {
		return "failed"
	}
	return a.reportOf(a.lastProbe).state()
}

// alignLine is the one-line status summary, kept short enough for the bar.
func (a *appState) alignLine() string {
	if failure := a.lastProbeFail; failure != nil {
		return fmt.Sprintf("alignment probe failed for %s: %s", shortURI(failure.Target.URI), failure.Reason)
	}
	if !hasProbe(a.lastProbe) {
		if a.lastProbeErr != "" {
			return "alignment probe failed: " + a.lastProbeErr
		}
		return ""
	}
	report := a.reportOf(a.lastProbe)
	switch report.state() {
	case "refused":
		return fmt.Sprintf("no suggestion for %s: %s (peak %.3f, next %.3f)",
			shortURI(report.Target.URI), report.Reason, report.Peak, report.RunnerUp)
	case "expired":
		return fmt.Sprintf("suggestion for %s expired: %s", shortURI(report.Target.URI), report.Expired)
	default:
		return fmt.Sprintf("suggested %+.3fs for %s (now %+.3fs)", report.Suggested.Seconds(),
			shortURI(report.Target.URI), report.Current.Seconds())
	}
}

// alignTone colours the block by what the report is worth: a live proposal, one
// the session outgrew, or a refusal.
func alignTone(state string) ui.Color {
	switch state {
	case "failed", "refused":
		return colorWarn
	case "expired":
		return colorDim
	default:
		return colorGood
	}
}

// hasProbe reports whether a comparison ever produced a proposal.
func hasProbe(s alignSuggestion) bool {
	return s.Reference.Source != "" || s.Target.Source != ""
}

// windowLabel names one side of a comparison: its place in the session, the
// track that was read, and the address a person recognises.
func windowLabel(w alignWindow) string {
	return fmt.Sprintf("source %d (%s) · %s", w.Index+1, trackName(w.Track), shortURI(w.URI))
}

// trackName keeps a track id out of the middle of a sentence when it is empty.
func trackName(track core.TrackID) string {
	if track == "" {
		return "no track"
	}
	return string(track)
}

// roundMS keeps a duration readable: ten milliseconds is finer than the probe
// resolves, and printing a full nanosecond value would only add noise.
func roundMS(value time.Duration) time.Duration {
	return value.Round(10 * time.Millisecond)
}
