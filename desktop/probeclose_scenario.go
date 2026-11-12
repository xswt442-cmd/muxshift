package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

// runProbeClose shows what a session close does to a comparison that is still
// being prepared: the decoder's context is cancelled, the attempt counter moves
// on so the late result cannot become the session's account, and the close path
// itself returns without waiting for the drawing thread. It needs two sources,
// because a comparison names two.
//
// The close waits for the decoder's pid before it starts: closing in the same
// step that asked for the comparison would close before the decoder existed,
// which proves the cancellation path but not what happens to a decoder that is
// already running. The pid is written to the check's directory, so the process
// outside can watch that exact process disappear.
func runProbeClose(window *mygo.Window, a *appState, dir string) {
	go probeCloseBody(window, a, dir)
}

func probeCloseBody(window *mygo.Window, a *appState, dir string) {
	var started, closed bool
	var attempt, after uint64
	var pid int
	var failed int
	check := func(ok bool, format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if ok {
			logf("probeclose", "ok: %s", line)
			return
		}
		failed++
		logf("probeclose", "FAIL: %s", line)
	}

	// A comparison needs a duration, and a player reports one only after the
	// playlist and its first segments have arrived. Starting before that fails
	// before any decoder exists, which is the opposite of what this check is for.
	ready := func() bool {
		return runOnMainValue(func() bool {
			if len(a.items) < 2 {
				return false
			}
			for _, it := range a.items[:2] {
				if it.player == nil || it.player.Duration() <= 0 {
					return false
				}
			}
			return true
		})
	}
	for deadline := time.Now().Add(30 * time.Second); !ready() && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		mygoRunOnMain(func() { window.Invalidate() })
	}

	mygoRunOnMain(func() {
		if len(a.items) < 2 {
			return
		}
		started = true
		attempt = a.startProbe(0, 1)
	})
	if !started {
		logf("probeclose", "the session needs two sources; it has %d", runOnMainValue(func() int { return len(a.items) }))
		mygo.App.Quit()
		return
	}

	// The comparison reads the session on the drawing thread and only then starts
	// ffmpeg, so the close has to wait for the pid; otherwise it would race the
	// decoder and prove nothing about a running one.
	deadline := time.Now().Add(30 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		mygoRunOnMain(func() { window.Invalidate() })
		pid = a.probePID()
	}
	check(pid != 0, "the decoder started before the close (pid %d)", pid)
	if pid != 0 {
		path := filepath.Join(dir, "decoder.pid")
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
			logf("probeclose", "could not write %s: %v", path, err)
		}
		logf("probeclose", "the decoder runs as pid %d; the window closes when %s says so", pid, filepath.Join(dir, "close")) // The process outside has to see the pid alive before the close, or the
		// evidence would again describe a decoder that may already be gone.
		closeFile := filepath.Join(dir, "close")
		waitUntil := time.Now().Add(30 * time.Second)
		for time.Now().Before(waitUntil) {
			if _, err := os.Stat(closeFile); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
			mygoRunOnMain(func() { window.Invalidate() })
		}
		if _, err := os.Stat(closeFile); err != nil {
			logf("probeclose", "FAIL: nothing asked the close to start")
		}
		logf("probeclose", "closing the window while decoder %d is still the comparison in flight", pid)
	}

	mygoRunOnMain(func() {
		a.release()
		closed = true
		after = a.probeAttempt
	})
	if !closed {
		logf("probeclose", "FAIL: the close never ran")
		mygo.App.Quit()
		return
	}
	time.Sleep(1500 * time.Millisecond)

	var running bool
	mygoRunOnMain(func() { running = a.probeRunning })
	check(!running, "the comparison is not marked as running after the close")
	check(after > attempt, "the close moved the attempt counter on (%d -> %d), so a late result is stale", attempt, after)
	check(closed, "the close returned without waiting for the decoder")

	// The pid is the session's record of the process, and the record has to follow
	// the process: once the decoder has ended, a reader must not be told about a
	// pid that no longer runs.
	left := pid
	for deadline := time.Now().Add(15 * time.Second); left != 0 && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		left = a.probePID()
	}
	check(left == 0, "the decoder record is empty once it ended (pid %d)", left)

	logf("probeclose", "done: %d check(s) failed", failed)
	mygoRunOnMain(func() { mygo.App.Quit() })
}
