// The scripted checks of P5: each drives the players through their own methods
// and captures the window, so what is recorded is the players' state rather
// than a click no test can make.
package main

import (
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// scenario runs a scripted check against the window and its sources.
type scenario func(window *mygo.Window, s *state, dir string)

// scenarios are the checks -scenario names.
var scenarios = map[string]scenario{
	"single": runSingle,
	"reopen": runReopen,
	"pair":   runPair,
	"switch": runSwitch,
	"rapid":  runRapid,
	"retry":  runRetry,
}

// scenarioNames lists the checks in order, for messages.
func scenarioNames() string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// logf records one observation with the time it was made at.
func logf(scenario, format string, args ...any) {
	log.Printf(scenario+": "+format, args...)
}

// playerState is what a player reported at one moment.
func playerState(i int, src *source) string {
	if src.player == nil {
		return "no player"
	}
	p := src.player
	return logLine(i, p.Playing(), p.Muted(), p.Position(), p.Duration(), p.Ready(), p.Err())
}

// logLine formats a player's state as one line.
func logLine(i int, playing, muted bool, pos, dur time.Duration, ready bool, err error) string {
	return strings.Join([]string{
		"source " + itoa(i+1),
		"playing=" + boolString(playing),
		"muted=" + boolString(muted),
		"ready=" + boolString(ready),
		"position=" + pos.String(),
		"duration=" + dur.String(),
		"err=" + errorString(err),
	}, " ")
}

// itoa is a small decimal of a non-negative int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// boolString is true or false.
func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// errorString is an error as text, or nil.
func errorString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// step runs fn on the main thread, where the window and its players live, and
// then records every source, capturing the window when path is not empty.
func step(window *mygo.Window, s *state, dir, scenario, path, what string, fn func(*state)) {
	mygo.RunOnMain(func() {
		if fn != nil {
			fn(s)
		}
	})
	if path != "" {
		// A capture taken in the same pass as the change shows the previous
		// frame: the raster of the window is painted between passes. Let a few
		// frames go by, then capture.
		time.Sleep(300 * time.Millisecond)
	}
	mygo.RunOnMain(func() {
		logErr := error(nil)
		if path != "" {
			logErr = capture(window, filepath.Join(dir, path))
		}
		if logErr != nil {
			logf(scenario, "%s: %s (capture failed: %v)", what, allStates(s), logErr)
			return
		}
		logf(scenario, "%s: %s", what, allStates(s))
	})
}

// allStates is every source's state as one line.
func allStates(s *state) string {
	parts := make([]string, 0, len(s.sources))
	for i, src := range s.sources {
		parts = append(parts, playerState(i, src))
	}
	return strings.Join(parts, " | ")
}

// runSingle plays one source, pauses it, resumes it, reloads it, mutes it, and
// closes it, capturing the window at each change.
func runSingle(window *mygo.Window, s *state, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, s, dir, "single", "1-playing.png", "playing", nil)
		step(window, s, dir, "single", "2-paused.png", "paused", func(s *state) {
			s.first().player.Pause()
		})
		wait(2 * time.Second)
		step(window, s, dir, "single", "3-still-paused.png", "still paused", nil)
		step(window, s, dir, "single", "", "resumed", func(s *state) {
			s.first().player.Play()
		})
		wait(700 * time.Millisecond)
		step(window, s, dir, "single", "4-resumed.png", "resumed after 0.7s", nil)
		wait(700 * time.Millisecond)
		step(window, s, dir, "single", "", "reloaded", func(s *state) {
			s.open(s.first())
		})
		wait(1500 * time.Millisecond)
		step(window, s, dir, "single", "5-reloaded.png", "reloaded after 1.5s", nil)
		wait(2500 * time.Millisecond)
		step(window, s, dir, "single", "5b-reloaded-later.png", "reloaded after 4s", nil)
		wait(1 * time.Second)
		step(window, s, dir, "single", "", "muted", func(s *state) { s.first().player.SetMuted(true) })
		wait(1 * time.Second)
		step(window, s, dir, "single", "", "unmuted", func(s *state) { s.first().player.SetMuted(false) })
		wait(1 * time.Second)
		step(window, s, dir, "single", "", "closed", func(s *state) { s.close(s.first()) })
		wait(1 * time.Second)
		step(window, s, dir, "single", "6-after-close.png", "after close", nil)
		mygo.RunOnMain(func() { mygo.App.Quit() })
	}()
}

// runReopen closes the only source and opens it again, giving the second
// player six seconds to show a picture, which is where a reopen behaves
// differently from a first open in this MyGo build.
func runReopen(window *mygo.Window, s *state, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, s, dir, "reopen", "1-first-open.png", "first open after 3s", nil)
		step(window, s, dir, "reopen", "", "closed", func(s *state) { s.close(s.first()) })
		wait(1 * time.Second)
		step(window, s, dir, "reopen", "", "reopened", func(s *state) { s.open(s.first()) })
		wait(6 * time.Second)
		step(window, s, dir, "reopen", "2-reopened.png", "reopened after 6s", nil)
		mygo.RunOnMain(func() {
			s.release()
			mygo.App.Quit()
		})
	}()
}

// runPair plays two sources at once: the first shows its picture muted, the
// second keeps its sound with its video out of the view. It records whether the
// hidden player still moves.
func runPair(window *mygo.Window, s *state, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, s, dir, "pair", "", "both playing", nil)
		step(window, s, dir, "pair", "1-a-muted-b-shown.png", "a muted", func(s *state) {
			s.sources[0].player.SetMuted(true)
		})
		wait(1 * time.Second)
		step(window, s, dir, "pair", "", "a muted b hidden", func(s *state) {
			// Stop drawing b's video: does its sound keep playing?
			s.sources[1].shown = false
		})
		wait(2 * time.Second)
		step(window, s, dir, "pair", "2-b-hidden.png", "b hidden for 2s", nil)
		wait(2 * time.Second)
		step(window, s, dir, "pair", "3-b-hidden-4s.png", "b hidden for 4s", nil)
		step(window, s, dir, "pair", "", "b shown again", func(s *state) {
			s.sources[1].shown = true
		})
		wait(1 * time.Second)
		step(window, s, dir, "pair", "4-b-shown-again.png", "b shown again", nil)
		step(window, s, dir, "pair", "", "a unmuted", func(s *state) {
			s.sources[0].player.SetMuted(false)
		})
		wait(1 * time.Second)
		step(window, s, dir, "pair", "5-both-audible.png", "both audible", nil)
		mygo.RunOnMain(func() {
			s.release()
			mygo.App.Quit()
		})
	}()
}

// runSwitch shows one source, then the other, then the first again while both
// keep playing, and closes each one in turn.
func runSwitch(window *mygo.Window, s *state, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, s, dir, "switch", "1-a-shown.png", "a shown", nil)
		step(window, s, dir, "switch", "", "switch to b", func(s *state) {
			s.sources[0].shown = false
			s.sources[1].shown = true
		})
		wait(2 * time.Second)
		step(window, s, dir, "switch", "2-b-shown.png", "b shown for 2s", nil)
		step(window, s, dir, "switch", "", "close a", func(s *state) { s.close(s.sources[0]) })
		wait(1 * time.Second)
		step(window, s, dir, "switch", "", "switch back to a (closed)", func(s *state) {
			s.open(s.sources[0])
			s.sources[0].shown = true
			s.sources[1].shown = false
		})
		wait(2 * time.Second)
		step(window, s, dir, "switch", "3-a-shown-again.png", "a shown again", nil)
		step(window, s, dir, "switch", "", "close b", func(s *state) { s.close(s.sources[1]) })
		wait(1 * time.Second)
		step(window, s, dir, "switch", "4-b-closed.png", "b closed", nil)
		mygo.RunOnMain(func() {
			s.release()
			mygo.App.Quit()
		})
	}()
}

// runRapid opens and closes the source several times in a row, the way a view
// switched by a person would, and records every pass.
func runRapid(window *mygo.Window, s *state, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		for pass := 1; pass <= 5; pass++ {
			step(window, s, dir, "rapid", "", "reopen "+itoa(pass), func(s *state) { s.open(s.first()) })
			wait(400 * time.Millisecond)
			step(window, s, dir, "rapid", "", "close "+itoa(pass), func(s *state) { s.close(s.first()) })
			wait(200 * time.Millisecond)
		}
		step(window, s, dir, "rapid", "1-after-5-passes.png", "after five passes", nil)
		mygo.RunOnMain(func() {
			s.release()
			mygo.App.Quit()
		})
	}()
}

// runRetry closes the source, opens an address that fails, and opens the good
// address again, recording the error the view shows and the recovery.
func runRetry(window *mygo.Window, s *state, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, s, dir, "retry", "1-playing.png", "playing", nil)
		step(window, s, dir, "retry", "", "open a missing address", func(s *state) {
			s.first().uri = missingAddress(s.first().uri)
			s.open(s.first())
		})
		wait(3 * time.Second)
		step(window, s, dir, "retry", "2-failed.png", "after the failure", nil)
		step(window, s, dir, "retry", "", "open the good address again", func(s *state) {
			s.first().uri = goodAddress(s.first().uri)
			s.open(s.first())
		})
		wait(4 * time.Second)
		step(window, s, dir, "retry", "3-recovered.png", "after the retry", nil)
		mygo.RunOnMain(func() {
			s.release()
			mygo.App.Quit()
		})
	}()
}

// missingAddress is uri with its last path element replaced by one a server
// does not have.
func missingAddress(uri string) string {
	slash := strings.LastIndex(uri, "/")
	if slash < 0 || slash == len(uri)-1 {
		return uri + ".missing"
	}
	return uri[:slash+1] + "missing.m3u8"
}

// goodAddress is the address a source had before missingAddress changed it.
func goodAddress(uri string) string {
	return strings.Replace(uri, "/missing.m3u8", "/index.m3u8", 1)
}

// The unused import guard: ui is used by the scenarios through capture and
// state only, so keep the compiler honest about it.
var _ = ui.Context{}
