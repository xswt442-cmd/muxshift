package main

import (
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo"
)

// runFrames measures the question the replay clock still owes P16.4: when a
// player reports position p, is the picture on the glass the frame that p names?
//
// It reuses the mid-flight attach -- a recording cut from a buffer that joined a
// live stream late -- keeps a copy of the joined file for offline inspection,
// samples fixed positions, and brackets every capture with two readings of the
// position in the same drawing-thread pass. It also seeks backwards, because a
// zero point that is only right when approached from the left is not right.
func runFrames(window *mygo.Window, a *appState, dir string) {
	go framesBody(window, a, dir, "frames")
}

func framesBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	m, finish := attachMidFlight(a, dir, scenario)
	defer finish()
	if m == nil {
		return
	}
	logf(scenario, "the file runs %s of media", sceneClock(m.length))

	// Keep the bytes the player is reading, so the frame structure can be read
	// without the buffer, the player or MyGo in the way.
	joined := filepath.Join(dir, "joined.ts")
	if err := copyFile(m.recording.Path, joined); err != nil {
		logf(scenario, "keep a copy of the joined file: %v", err)
		check(false, "keep a copy of the joined file: %v", err)
	} else {
		logf(scenario, "kept the joined file at %s (%s of media, buffer %s..%s)",
			joined, sceneClock(m.length), sceneClock(m.recording.StartBufferTime), sceneClock(m.recording.EndBufferTime))
	}

	samples := []time.Duration{0, 2 * time.Second, 4 * time.Second, 6 * time.Second, 8 * time.Second, 10 * time.Second}
	for index, target := range samples {
		target = m.clamp(target)
		position, settled := m.seekSettle(a, scenario, target)
		check(settled, "sample %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		shot := "sample-" + strconv.Itoa(index+1) + ".png"
		before, after, ready, buffering := m.captureSample(window, a, dir, scenario, shot)
		check(ready, "sample %d captured while the player was not ready", index+1)
		check(!buffering, "sample %d captured while the player was buffering", index+1)
		check(absDuration(after-before) <= 40*time.Millisecond,
			"sample %d moved from %s to %s inside one capture", index+1, sceneClock(before), sceneClock(after))
		logf(scenario, "sample %d: target %s | captured between %s and %s | ready=%t buffering=%t",
			index+1, sceneClock(target), sceneClock(before), sceneClock(after), ready, buffering)

		// A paused player may not paint the frame it seeks to: ask it to play
		// for a moment, then pause again, and capture what the window shows
		// then. If this picture differs from the one above, the first capture
		// was a stale surface, not a wrong clock.
		mygoRunOnMain(func() {
			if m.it.player != nil {
				m.it.player.Play()
			}
		})
		time.Sleep(600 * time.Millisecond)
		mygoRunOnMain(func() {
			m.it.player.Pause()
		})
		time.Sleep(300 * time.Millisecond)
		woke := "woken-" + strconv.Itoa(index+1) + ".png"
		wokenBefore, wokenAfter, wokenReady, wokenBuffering := m.captureSample(window, a, dir, scenario, woke)
		check(wokenReady, "woken sample %d captured while the player was not ready", index+1)
		logf(scenario, "woken sample %d: target %s | captured between %s and %s | ready=%t buffering=%t",
			index+1, sceneClock(target), sceneClock(wokenBefore), sceneClock(wokenAfter), wokenReady, wokenBuffering)
	}

	// A zero point that is only right when approached from the left is not a
	// zero point: ask for the same positions in the other order.
	backwards := []time.Duration{10 * time.Second, 6 * time.Second, 0, 6 * time.Second}
	for index, target := range backwards {
		target = m.clamp(target)
		position, settled := m.seekSettle(a, scenario, target)
		check(settled, "reverse seek %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		logf(scenario, "reverse seek %d: asked for %s, player reported %s", index+1, sceneClock(target), sceneClock(position))
	}

	logf(scenario, "checks failed: %d", failures)
}
