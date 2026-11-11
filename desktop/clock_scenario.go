package main

import (
	"bytes"
	"os"
	"strconv"
	"time"

	"github.com/egoist/mygo"
)

// runOnMainValue runs fn on the drawing thread and returns what it returned.
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
		finishClock(a)
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
			finishClock(a)
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
	finishClock(a)
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

func finishClock(a *appState) {
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}
