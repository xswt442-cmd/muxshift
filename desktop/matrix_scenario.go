// P14.1: the playback baseline matrix. It opens the sources the command line
// names, records how long each one takes to report itself ready, walks the
// picture from source to source, then pauses, resumes, and switches the heard
// source, logging what the players actually report at each step.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// readySample is one source's measurement at the moment the matrix looked.
type readySample struct {
	Index   int
	Ready   bool
	After   time.Duration
	Playing bool
	Buf     bool
	Pos     time.Duration
	Dur     time.Duration
	Err     string
	Frames  int
}

func (s readySample) line() string {
	state := "not_ready"
	if s.Ready {
		state = "ready"
	}
	return fmt.Sprintf("source %d: %s after %s playing=%t buffering=%t pos=%s dur=%s frames=%d err=%s",
		s.Index+1, state, s.After.Round(time.Millisecond), s.Playing, s.Buf,
		s.Pos.Round(time.Millisecond), s.Dur.Round(time.Millisecond), s.Frames, emptyWord(s.Err))
}

// sampleOnMain reads one source on the drawing thread.
func (a *appState) sampleOnMain(index int, since time.Time) readySample {
	sample := readySample{Index: index, After: time.Since(since)}
	mygoRunOnMain(func() {
		it := a.items[index]
		sample.Frames = a.frameCount
		if it.player == nil {
			sample.Err = "no player"
			return
		}
		sample.Ready = it.player.Ready()
		sample.Playing = it.player.Playing()
		sample.Buf = it.player.Buffering()
		sample.Pos = it.player.Position()
		sample.Dur = it.player.Duration()
		if err := it.player.Err(); err != nil {
			sample.Err = err.Error()
		}
	})
	return sample
}

// waitForMatrixReady polls every source until each reports ready, an error, or
// the timeout passes, so the log records what the players did instead of what
// the scenario hoped.
func waitForMatrixReady(a *appState, start time.Time, timeout time.Duration) []readySample {
	deadline := time.Now().Add(timeout)
	samples := make([]readySample, len(a.items))
	for {
		all := true
		for i := range a.items {
			samples[i] = a.sampleOnMain(i, start)
			if !samples[i].Ready && samples[i].Err == "" && samples[i].Err != "no player" {
				all = false
			}
		}
		if all || time.Now().After(deadline) {
			return samples
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// runMatrix records the baseline the later P14 work is compared against.
func runMatrix(window *mygo.Window, a *appState, dir string) {
	scenario := "matrix"
	start := time.Now()
	go func() {
		time.Sleep(300 * time.Millisecond)
		logf(scenario, "opened %d source(s), waiting up to 25s for each to report ready", len(a.items))
		samples := waitForMatrixReady(a, start, 25*time.Second)
		for _, sample := range samples {
			logf(scenario, sample.line())
		}

		// Walk the picture across the sources, pausing long enough for the
		// picture to actually change between captures.
		for i := range a.items {
			mygoRunOnMain(func() {
				if err := a.showVideo(i); err != nil {
					logf(scenario, "show source %d failed: %v", i+1, err)
				}
			})
			time.Sleep(2 * time.Second)
			sample := a.sampleOnMain(i, start)
			logf(scenario, "picture on source %d: %s", i+1, sample.line())
			if i == 0 || i == len(a.items)-1 {
				step(window, a, dir, scenario, fmt.Sprintf("%d-picture-%d.png", i+1, i+1),
					fmt.Sprintf("source %d on the stage", i+1), nil)
			}
		}

		// Pause: the positions must stop moving.
		mygoRunOnMain(func() { logGroup(scenario, "pause", a.pauseAll()) })
		before := a.sampleOnMain(0, start)
		time.Sleep(2 * time.Second)
		after := a.sampleOnMain(0, start)
		logf(scenario, "while paused: pos %s -> %s (moved %s)",
			before.Pos.Round(time.Millisecond), after.Pos.Round(time.Millisecond),
			(after.Pos - before.Pos).Round(time.Millisecond))

		// Resume: the positions must move again.
		mygoRunOnMain(func() { logGroup(scenario, "play", a.playAll()) })
		resumed := a.sampleOnMain(0, start)
		time.Sleep(2 * time.Second)
		later := a.sampleOnMain(0, start)
		logf(scenario, "after resuming: pos %s -> %s (moved %s)",
			resumed.Pos.Round(time.Millisecond), later.Pos.Round(time.Millisecond),
			(later.Pos - resumed.Pos).Round(time.Millisecond))

		// Two sources at once: picture from the first, sound from the second.
		if len(a.items) > 1 {
			mygoRunOnMain(func() {
				if err := a.showVideo(0); err != nil {
					logf(scenario, "show source 1 failed: %v", err)
				}
				if err := a.hearAudio(1, 1); err != nil {
					logf(scenario, "hear source 2 failed: %v", err)
				}
			})
			time.Sleep(2 * time.Second)
			logf(scenario, "picture 1 with sound 2: %s", a.sceneLabel())
			step(window, a, dir, scenario, "picture-1-sound-2.png",
				"picture from the first source, sound from the second", nil)
			mygoRunOnMain(func() {
				if err := a.showVideo(1); err != nil {
					logf(scenario, "show source 2 failed: %v", err)
				}
				if err := a.hearAudio(0, 1); err != nil {
					logf(scenario, "hear source 1 failed: %v", err)
				}
			})
			time.Sleep(2 * time.Second)
			logf(scenario, "picture 2 with sound 1: %s", a.sceneLabel())
		}

		logf(scenario, "the matrix ends with %d source(s) and %d event(s)",
			len(a.items), len(a.session.Events()))
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
