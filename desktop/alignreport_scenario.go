package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runAlignReport drives the read-only alignment report on the stage: one probe
// that still describes the session, then one the session has outgrown. It
// writes no shift of its own; the only offsets it changes are the two nudges a
// person would make, and those go through the ordinary command path.
func runAlignReport(window *mygo.Window, a *appState, dir string) {
	go alignReportBody(window, a, dir)
}

func alignReportBody(window *mygo.Window, a *appState, dir string) {
	scenario := "alignreport"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf("alignreport", "ok: "+format, args...)
			return
		}
		failures++
		logf("alignreport", "CHECK FAILED: "+format, args...)
	}

	if len(a.items) < 2 {
		logf(scenario, "two sources are needed, the run has %d", len(a.items))
		a.release()
		mygo.App.Quit()
		return
	}
	if err := a.waitPlayers(20 * time.Second); err != nil {
		logf(scenario, "the players never came up: %v", err)
		a.release()
		mygo.App.Quit()
		return
	}

	// Put the target a second away from where it is, so the proposal has a
	// current value to be compared against.
	var nudgeErr error
	mygoRunOnMain(func() { _, nudgeErr = a.nudge(1, time.Second) })
	if nudgeErr != nil {
		logf(scenario, "nudging the second source failed: %v", nudgeErr)
	}
	current := runOnMainValue(func() time.Duration { return a.targetOffset(1) })
	logf(scenario, "the second source sits at %+.3fs before the probe", current.Seconds())

	probeErr := a.probeAndWait(0, 1, 30*time.Second)
	if probeErr != nil {
		logf(scenario, "the probe did not finish: %v", probeErr)
		a.release()
		mygo.App.Quit()
		return
	}

	report := runOnMainValue(a.alignLines)
	logf(scenario, "the probe describes:\n%s", strings.Join(report, "\n"))
	for _, want := range []string{
		"compared with",
		"windows ",
		"matched ",
		"peak ",
		"confidence ",
		"(track-",
		"suggested ",
		"now ",
		"difference ",
	} {
		check(strings.Contains(strings.Join(report, "\n"), want), "the report mentions %q", want)
	}
	check(strings.Contains(strings.Join(report, "\n"), "difference -1.0"),
		"the difference is the proposal against the current offset:\n%s", strings.Join(report, "\n"))
	step(window, a, dir, scenario, "1-proposal.png", "the probe that still describes the session", func(*appState) {})

	// Move the target: the finished probe no longer describes the session, and
	// the report must say so instead of presenting it as current.
	var moveErr error
	mygoRunOnMain(func() { _, moveErr = a.nudge(1, -500*time.Millisecond) })
	if moveErr != nil {
		logf(scenario, "moving the second source failed: %v", moveErr)
	}
	before := runOnMainValue(a.sync.timeline.Shifts)
	stale := runOnMainValue(a.alignLines)
	logf(scenario, "after moving the target, the same probe reads:\n%s", strings.Join(stale, "\n"))
	after := runOnMainValue(a.sync.timeline.Shifts)
	check(strings.Contains(strings.Join(stale, "\n"), "expired: "),
		"the report says it expired after the target moved:\n%s", strings.Join(stale, "\n"))
	check(len(before) == len(after), "reading the report did not add or remove shifts: %d -> %d", len(before), len(after))
	for index := range before {
		if before[index] != after[index] {
			check(false, "reading the report changed shift %d: %+v -> %+v", index, before[index], after[index])
		}
	}
	step(window, a, dir, scenario, "2-expired.png", "the same probe after the target moved", func(*appState) {})

	logf(scenario, "alignreport: %d report line(s), checks failed: %d", len(stale), failures)
	a.release()
	mygo.App.Quit()
}

// targetOffset is the offset the timeline holds for a source, or 0 when it has
// none yet.
func (a *appState) targetOffset(index int) time.Duration {
	track := a.alignTrack(index)
	if track == "" {
		return 0
	}
	if shift, ok := a.sync.timeline.Shift(track); ok {
		return shift.Offset
	}
	return 0
}

// probeAndWait starts the button's own path and waits for it to finish, so the
// scenario exercises the same code the button does.
func (a *appState) probeAndWait(reference, target int, timeout time.Duration) error {
	a.probeAsync(reference, target)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		done := runOnMainValue(func() bool { return !a.probeRunning && a.lastProbe.Target.Source != "" })
		if done {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the comparison did not finish within %s", timeout)
}

// waitPlayers waits until every source has a player that reports ready.
func (a *appState) waitPlayers(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready := 0
		mygoRunOnMain(func() {
			for _, it := range a.items {
				if it.player != nil && it.player.Ready() {
					ready++
				}
			}
		})
		if ready == len(a.items) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("no picture within %s", timeout)
}
