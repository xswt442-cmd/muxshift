package main

import (
	"bytes"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file groups the scenario entry points in this domain. It was merged from align_scenario.go, alignreport_scenario.go, clock_scenario.go.

func runAlignAsync(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "async", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)

		step(window, a, dir, "async", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
			logGroup("async", "play", a.playAll())
		})
		wait(2 * time.Second)

		// The group keeps playing on purpose: a paused window does not repaint in
		// this MyGo build, and the point here is that the window keeps drawing
		// while the comparison runs.
		step(window, a, dir, "async", "", "anchor the event clock", func(a *appState) {
			logGroup("async", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
			logGroup("async", "play", a.playAll())
		})
		wait(700 * time.Millisecond)

		var framesBefore int
		step(window, a, dir, "async", "", "start the probe off the UI thread", func(a *appState) {
			framesBefore = a.framesBuilt()
			a.probeAsync(0, 1)
			logf("async", "probe started, frames so far %d", framesBefore)
		})

		// The decode takes a few hundred milliseconds, so this lands inside it.
		wait(150 * time.Millisecond)
		step(window, a, dir, "async", "", "the user moves the target while the probe runs", func(a *appState) {
			logf("async", "frames drawn while the probe ran: %d", a.framesBuilt()-framesBefore)
			logf("async", "positions while the probe ran [%s]", strings.Join(a.positions(), " | "))
			a.nudgeAndRecord(1, -100*time.Millisecond)
		})

		// The result arrives on the UI thread; the running flag says when.
		for done := time.Now().Add(30 * time.Second); time.Now().Before(done); {
			var running bool
			mygoRunOnMain(func() { running = a.probeRunning })
			if !running {
				break
			}
			wait(100 * time.Millisecond)
		}

		step(window, a, dir, "async", "1-async-expired.png", "what the window kept doing", func(a *appState) {
			logf("async", "frames drawn in total while the probe ran: %d", a.framesBuilt()-framesBefore)
			logf("async", "positions after the probe [%s]", strings.Join(a.positions(), " | "))
			logf("async", "the finished probe says: %s", a.suggestionLine(a.lastProbe))
			if a.lastProbe.Expired == "" {
				logf("async", "the result did not report the change made after the probe started")
			}
		})

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runAlignProbe shows what the alignment probe proposes and what it refuses,
// then proves that neither answer touched the timeline or the players.
func runAlignProbe(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the target is shown once before it is measured.
		step(window, a, dir, "align", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)

		// The probe is read-only, so this one runs before any sync command has
		// created a group: a suggestion may not build one either.
		step(window, a, dir, "align", "", "probe before any sync command", func(a *appState) {
			logf("align", "groups before [%s]", a.groupState())
			before := a.sync.timeline.Shifts()
			a.probeAndReport(0, 1)
			logf("align", "groups after [%s] shifts [%s]", a.groupState(), describeShifts(a.sync.timeline.Shifts()))
			if !sameShifts(before, a.sync.timeline.Shifts()) {
				logf("align", "the probe changed the timeline offsets")
			}
		})
		wait(500 * time.Millisecond)

		step(window, a, dir, "align", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
			logGroup("align", "play", a.playAll())
		})
		wait(3 * time.Second)

		// Pausing first keeps the players still, so the positions captured
		// before and after the probe can actually be compared.
		step(window, a, dir, "align", "", "pause before probing", func(a *appState) {
			logGroup("align", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)

		var suggestion alignSuggestion
		var before []core.Shift
		var positionsBefore []string
		step(window, a, dir, "align", "", "probe source 2 against source 1", func(a *appState) {
			before = a.sync.timeline.Shifts()
			positionsBefore = a.positions()
			suggestion = a.probeAndReport(0, 1)
		})

		step(window, a, dir, "align", "", "state after the probe", func(a *appState) {
			logf("align", "shifts before [%s] after [%s]", describeShifts(before), describeShifts(a.sync.timeline.Shifts()))
			logf("align", "positions before [%s] after [%s]", strings.Join(positionsBefore, " | "), strings.Join(a.positions(), " | "))
			if !sameShifts(before, a.sync.timeline.Shifts()) {
				logf("align", "the probe changed the timeline offsets")
			}
		})

		// A paused window does not repaint in this build, so the picture of the
		// proposal is taken while the group plays again.
		step(window, a, dir, "align", "", "resume so the window redraws", func(a *appState) {
			logGroup("align", "play", a.playAll())
		})
		wait(1500 * time.Millisecond)
		step(window, a, dir, "align", "1-suggestion.png", "the proposal in the status bar", nil)
		step(window, a, dir, "align", "", "pause again for the manual change", func(a *appState) {
			logGroup("align", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(300 * time.Millisecond)

		// A manual change after the probe must invalidate the proposal.
		step(window, a, dir, "align", "", "user moves the target by hand", func(a *appState) {
			a.nudgeAndRecord(1, -100*time.Millisecond)
			a.logStale(suggestion)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "align", "2-after-manual-nudge.png", "after the manual nudge", nil)

		// A source with unrelated content must be refused, not guessed. The
		// scenario only runs this step when a third source was given.
		step(window, a, dir, "align", "3-refused.png", "probe a source with different content", func(a *appState) {
			if len(a.items) < 3 {
				logf("align", "no third source was given, so the refusal case is not run")
				return
			}
			a.probeAndReport(0, 2)
		})
		wait(time.Second)
		step(window, a, dir, "align", "", "state after the refused probe", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

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

	// Make a comparison fail for real: point the target at something that cannot
	// be decoded, so the comparison itself fails while its identity is still
	// known. The failure must be named after that target, and the earlier
	// proposal may only appear as an earlier one.
	originalURI := runOnMainValue(func() string { return a.items[1].uri })
	const missing = "testplace/media/missing-for-failure.mp4"
	mygoRunOnMain(func() { a.items[1].uri = missing })
	attempt := runOnMainValue(func() uint64 { return a.startProbe(0, 1) })
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if runOnMainValue(func() bool { return a.probeDone >= attempt }) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mygoRunOnMain(func() { a.items[1].uri = originalURI })
	failed := runOnMainValue(a.alignLines)
	logf(scenario, "after a comparison that could not read its target:\n%s", strings.Join(failed, "\n"))
	if len(failed) == 0 {
		check(false, "a failed comparison shows nothing")
	} else {
		check(strings.Contains(failed[0], "did not finish"),
			"the first line is the failure and names its own target: %q", failed[0])
		check(strings.Contains(failed[0], "missing-for-failure"),
			"the failure names the target it compared: %q", failed[0])
		check(strings.Contains(strings.Join(failed, "\n"), "an earlier comparison"),
			"the older proposal is marked as an earlier one:\n%s", strings.Join(failed, "\n"))
	}
	barred := runOnMainValue(a.alignLine)
	logf(scenario, "the status bar reads: %s", barred)
	check(strings.Contains(barred, "missing-for-failure"),
		"the status bar names the same target as the failure: %q", barred)
	step(window, a, dir, scenario, "3-failed.png", "the comparison that could not finish", func(*appState) {})

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

// probeAndWait starts the button's own path on the drawing thread and waits for
// that attempt to finish, so the scenario exercises the same code the button does
// and cannot mistake an older proposal for its own result.
func (a *appState) probeAndWait(reference, target int, timeout time.Duration) error {
	attempt := runOnMainValue(func() uint64 { return a.startProbe(reference, target) })
	if attempt == 0 {
		return fmt.Errorf("the comparison did not start")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		done := runOnMainValue(func() bool { return a.probeDone >= attempt })
		if done {
			if err := runOnMainValue(func() error {
				if a.lastProbeErr != "" {
					return fmt.Errorf("%s", a.lastProbeErr)
				}
				return nil
			}); err != nil {
				return err
			}
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
	return fmt.Errorf("the players did not report ready within %s", timeout)
}

func runOnMainValue[T any](fn func() T) T {
	var out T
	mygoRunOnMain(func() { out = fn() })
	return out
}

// snapshot is what the player reports in one drawing-thread pass.
type snapshot struct {
	position  time.Duration
	ready     bool
	buffering bool
}

// takeSnapshot reads the player's state on the drawing thread.
func takeSnapshot(a *appState, it *item) snapshot {
	return runOnMainValue(func() snapshot {
		position, ready, buffering := playerSnapshot(a, it)
		return snapshot{position, ready, buffering}
	})
}

// runClock asks one file the same question the frames check asks a recording:
// when the player says it is at p, which frame is actually on the glass? It
// opens the file directly -- no buffer, no recording, no swap -- so a mismatch
// here belongs to the player, and only a match here makes a mismatch in the
// recording a DVR question.
func runClock(window *mygo.Window, a *appState, dir string) {
	go clockBody(window, a, dir, "clock")
}

func clockBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		finishScenario(a)
		return
	}
	opened := time.Now()
	for {
		ready := takeSnapshot(a, it).ready
		if ready && time.Since(opened) > 2*time.Second {
			break
		}
		if time.Since(opened) > 30*time.Second {
			logf(scenario, "the player never became ready")
			check(false, "the player never became ready")
			finishScenario(a)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	length := runOnMainValue(func() time.Duration {
		if it.player == nil {
			return 0
		}
		return it.player.Duration()
	})
	logf(scenario, "the file runs %s", sceneClock(length))

	samples := []time.Duration{0, 2 * time.Second, 4 * time.Second, 6 * time.Second, 8 * time.Second, 10 * time.Second}
	for index, target := range samples {
		target = insideFile(target, length)
		position, settled := settle(a, it, target)
		check(settled, "sample %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		shot := "sample-" + strconv.Itoa(index+1) + ".png"
		before, after, ready, buffering, rounds := stableCapture(window, a, it, dir, shot, scenario)
		logf(scenario, "sample %d settled after %d capture(s)", index+1, rounds)
		check(ready, "sample %d captured while the player was not ready", index+1)
		check(!buffering, "sample %d captured while the player was buffering", index+1)
		check(absDuration(after-before) <= 40*time.Millisecond,
			"sample %d moved from %s to %s inside one capture", index+1, sceneClock(before), sceneClock(after))
		logf(scenario, "sample %d: target %s | captured between %s and %s | ready=%t buffering=%t",
			index+1, sceneClock(target), sceneClock(before), sceneClock(after), ready, buffering)
	}
	for index, target := range []time.Duration{10 * time.Second, 6 * time.Second, 0, 6 * time.Second} {
		target = insideFile(target, length)
		position, settled := settle(a, it, target)
		check(settled, "reverse seek %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		logf(scenario, "reverse seek %d: asked for %s, player reported %s", index+1, sceneClock(target), sceneClock(position))
	}
	logf(scenario, "checks failed: %d", failures)
	finishScenario(a)
}

// insideFile keeps a sample away from the very end of a file.
func insideFile(target, length time.Duration) time.Duration {
	if target < 0 {
		return 0
	}
	if target > length-500*time.Millisecond {
		return length - 500*time.Millisecond
	}
	return target
}

// settle pauses the player, asks for target and waits until the player reports
// it while ready and not buffering.
func settle(a *appState, it *item, target time.Duration) (time.Duration, bool) {
	for attempt := 0; attempt < 4; attempt++ {
		mygoRunOnMain(func() {
			if it.player != nil {
				it.player.Pause()
				it.player.Seek(target)
			}
		})
		deadline := time.Now().Add(4 * time.Second)
		settled := 0
		for time.Now().Before(deadline) {
			time.Sleep(150 * time.Millisecond)
			state := takeSnapshot(a, it)
			position, ready, buffering := state.position, state.ready, state.buffering
			if ready && !buffering && absDuration(position-target) <= 40*time.Millisecond {
				settled++
				if settled >= 2 {
					return position, true
				}
				continue
			}
			settled = 0
		}
	}
	position := takeSnapshot(a, it).position
	return position, false
}

// bracketCapture reads the position, captures the window and reads the position
// again, all in one drawing-thread pass.
func bracketCapture(window *mygo.Window, a *appState, it *item, dir, name, scenario string) (before, after time.Duration, ready, buffering bool) {
	mygoRunOnMain(func() {
		before, ready, buffering = playerSnapshot(a, it)
		_ = capture(window, dir+"/"+name)
		after, _, _ = playerSnapshot(a, it)
	})
	logf(scenario, "%s: captured between Position() %s and %s (ready=%t buffering=%t)",
		name, sceneClock(before), sceneClock(after), ready, buffering)
	return before, after, ready, buffering
}

// stableCapture captures the window repeatedly until two captures in a row are
// byte for byte the same, and returns the positions bracketing the last one.
// A player that reports a seek position before it has painted that frame makes
// a single capture a picture of the past; waiting for the surface to stand
// still is what tells the two apart.
func stableCapture(window *mygo.Window, a *appState, it *item, dir, name, scenario string) (before, after time.Duration, ready, buffering bool, rounds int) {
	var previous []byte
	path := dir + "/" + name
	for round := 1; round <= 12; round++ {
		mygoRunOnMain(func() {
			before, ready, buffering = playerSnapshot(a, it)
			_ = capture(window, path)
			after, _, _ = playerSnapshot(a, it)
		})
		data, err := os.ReadFile(path)
		if err != nil {
			logf(scenario, "%s: read the capture back: %v", name, err)
			return before, after, ready, buffering, round
		}
		stable := previous != nil && bytes.Equal(previous, data)
		previous = data
		if stable {
			logf(scenario, "%s: the surface stopped moving after %d capture(s)", name, round)
			return before, after, ready, buffering, round
		}
		time.Sleep(250 * time.Millisecond)
	}
	logf(scenario, "%s: the surface never stopped moving", name)
	return before, after, ready, buffering, 12
}

func finishScenario(a *appState) {
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}
