// P16.0: does the picture keep advancing while a page other than the stage is
// open? The experiment compares one window per -stage-mode value and logs the
// player state every two seconds while the settings page is displayed, then
// shows the stage again. The screenshots are the other half of the answer: a
// running position is not proof that the media moved, so the burnt-in timecode
// of the first frame after returning has to match the time that passed.
package main

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

// runContinuity walks one stage mode: stage, settings for eight seconds,
// stage again. It logs the players before, during and after the visit, plus the
// replay buffer's byte count when an HLS source is present, so a stalled player
// and a stalled cache can be told apart.
func runContinuity(window *mygo.Window, a *appState, dir string) {
	go func() {
		if !waitForFirstPicture(a, 20*time.Second) {
			logf("continuity", "no picture arrived within 20s: %s", allStates(a))
			mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
			return
		}
		// The first source is the one the stage shows; its audio is heard so a
		// silent picture cannot be mistaken for a running one.
		mygoRunOnMain(func() {
			if err := a.showVideo(0); err != nil {
				logf("continuity", "show the first source: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("continuity", "hear the first source: %v", err)
			}
			window.Invalidate()
		})
		time.Sleep(1500 * time.Millisecond)

		// A replay buffer runs beside the player when a live HLS source is on
		// the command line: P8's cache is the other thing that must keep moving
		// while a page is open.
		var buffer *dvrRunner
		if uri := firstBuffered(a); uri != "" {
			runner, err := startDVR(uri, dvrFromMedia(filepath.Join(dir, "cache"), dvrRetention, dvrMaxBytes))
			if err != nil {
				logf("continuity", "replay buffer: %v", err)
			} else {
				buffer = runner
				defer buffer.Close()
			}
		}
		cacheLine := func() string {
			if buffer == nil {
				return "cache none"
			}
			return fmt.Sprintf("cache %d bytes", buffer.buffer.Bytes())
		}

		step(window, a, dir, "continuity", "1-stage.png", "baseline on the stage", func(a *appState) {
			logf("continuity", "mode %q | %s | %s", a.stageMode, allStates(a), cacheLine())
		})

		// Switch away and watch the clock for eight seconds. Every reading is
		// taken on the main thread: the players belong to it.
		mygoRunOnMain(func() { a.workspace = workspaceSettings })
		awayStart := time.Now()
		for i := 1; i <= 4; i++ {
			time.Sleep(2 * time.Second)
			mygoRunOnMain(func() {
				logf("continuity", "away +%s | %s | %s", sceneClock(time.Since(awayStart)), allStates(a), cacheLine())
			})
		}
		step(window, a, dir, "continuity", "2-away.png", "the settings page after eight seconds away", func(a *appState) {
			logf("continuity", "mode %q away total %s | %s | %s",
				a.stageMode, sceneClock(time.Since(awayStart)), allStates(a), cacheLine())
		})

		mygoRunOnMain(func() { a.workspace = workspaceStage })
		step(window, a, dir, "continuity", "3-back.png", "the stage the moment it returns", func(a *appState) {
			logf("continuity", "mode %q back after %s | %s | %s",
				a.stageMode, sceneClock(time.Since(awayStart)), allStates(a), cacheLine())
		})
		time.Sleep(2000 * time.Millisecond)
		step(window, a, dir, "continuity", "4-back-2s.png", "the stage two seconds later", func(a *appState) {
			logf("continuity", "mode %q two seconds on the stage | %s | %s", a.stageMode, allStates(a), cacheLine())
		})
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

// waitForFirstPicture polls until the first item has a ready player, so a slow
// file cannot be mistaken for a stalled one.
func waitForFirstPicture(a *appState, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() {
			if len(a.items) > 0 && a.items[0].player != nil {
				ready = a.items[0].player.Ready()
			}
		})
		if ready {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// firstBuffered returns the first network address in the session, which is the
// source a replay buffer can be opened on.
func firstBuffered(a *appState) string {
	var uri string
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if isNetworkAddress(it.uri) {
				uri = it.uri
				return
			}
		}
	})
	if uri == "" {
		log.Printf("continuity: no network source, the cache check is skipped")
	}
	return uri
}

// sceneClock prints a duration without its nanoseconds, which is noise here.
func sceneClock(d time.Duration) string {
	return d.Round(10 * time.Millisecond).String()
}
