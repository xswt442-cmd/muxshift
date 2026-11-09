// The scripted checks: each drives the players and the session through their
// own methods and captures the window, so what is recorded is the players'
// state rather than a click no test can make.
package main

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// scenario runs a scripted check against the window and its sources.
type scenario func(window *mygo.Window, a *appState, dir string)

// scenarios are the checks -scenario names.
var scenarios = map[string]scenario{
	"single":        runSingle,
	"reopen":        runReopen,
	"reopen-strict": runReopenStrict,
	"pair":          runPair,
	"switch":        runSwitch,
	"rapid":         runRapid,
	"retry":         runRetry,
	"scene":         runScene,
	"four":          runFour,
	"audio":         runAudio,
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
func playerState(i int, it *item) string {
	if it.player == nil {
		return "source " + itoa(i+1) + " no player"
	}
	p := it.player
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
func step(window *mygo.Window, a *appState, dir, scenario, path, what string, fn func(*appState)) {
	mygoRunOnMain(func() {
		if fn != nil {
			fn(a)
		}
	})
	if path != "" {
		// A capture taken in the same pass as the change shows the previous
		// frame: the raster of the window is painted between passes. Let a few
		// frames go by, then capture.
		time.Sleep(300 * time.Millisecond)
	}
	mygoRunOnMain(func() {
		logErr := error(nil)
		if path != "" {
			logErr = capture(window, filepath.Join(dir, path))
		}
		if logErr != nil {
			logf(scenario, "%s: %s | scene %s | capture failed: %v", what, allStates(a), a.sessionState(), logErr)
			return
		}
		logf(scenario, "%s: %s | scene %s", what, allStates(a), a.sessionState())
	})
}

// allStates is every source's state as one line.
func allStates(a *appState) string {
	parts := make([]string, 0, len(a.items))
	for i, it := range a.items {
		parts = append(parts, playerState(i, it))
	}
	return strings.Join(parts, " | ")
}

// sessionState is the session's counts and the scene's contents, so a run shows
// whether anything was left dangling.
func (a *appState) sessionState() string {
	sources := a.session.Sources()
	tracks := a.session.Tracks()
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return "scene error: " + err.Error()
	}
	parts := []string{
		fmt.Sprintf("sources=%d tracks=%d outputs=%d", len(sources), len(tracks), len(a.session.Outputs())),
		"video=" + trackList(scene.Video),
		"audio=" + audioList(scene.Audio),
	}
	return strings.Join(parts, " ")
}

// trackList is a comma separated list of track ids, or none.
func trackList(tracks []core.TrackID) string {
	if len(tracks) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(tracks))
	for _, track := range tracks {
		parts = append(parts, string(track))
	}
	return strings.Join(parts, ",")
}

// audioList is the scene's audio levels as track and volume pairs, or none.
func audioList(levels []core.AudioLevel) string {
	if len(levels) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(levels))
	for _, level := range levels {
		parts = append(parts, fmt.Sprintf("%s@%.2f", level.Track, level.Volume))
	}
	return strings.Join(parts, ",")
}

// runAudio plays one source with its audio heard, then mutes it, then unmutes
// it, logging a marker at each change. A separate meter sampling the default
// output device records which windows had sound, so audibility can be checked
// without a person listening.
func runAudio(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "audio", "1-audible.png", "audible window starts", nil)
		wait(4 * time.Second)
		step(window, a, dir, "audio", "", "muted window starts", func(a *appState) {
			a.muteAll(true)
		})
		wait(4 * time.Second)
		step(window, a, dir, "audio", "2-muted.png", "unmuted window starts", func(a *appState) {
			a.muteAll(false)
		})
		wait(4 * time.Second)
		step(window, a, dir, "audio", "3-unmuted-again.png", "end", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// muteAll mutes or unmutes every open player, so a scripted check can compare
// what the output device meters with the sound on and off.
func (a *appState) muteAll(muted bool) {
	for _, it := range a.items {
		if it.player != nil {
			it.player.SetMuted(muted)
		}
	}
}

// runSingle plays one source, pauses it, resumes it, reloads it, mutes it, and
// closes it, capturing the window at each change.
func runSingle(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "single", "1-playing.png", "playing", nil)
		step(window, a, dir, "single", "2-paused.png", "paused", func(a *appState) {
			a.first().player.Pause()
		})
		wait(2 * time.Second)
		step(window, a, dir, "single", "3-still-paused.png", "still paused", nil)
		step(window, a, dir, "single", "", "resumed", func(a *appState) {
			a.first().player.Play()
		})
		wait(700 * time.Millisecond)
		step(window, a, dir, "single", "4-resumed.png", "resumed after 0.7s", nil)
		wait(700 * time.Millisecond)
		step(window, a, dir, "single", "", "reloaded", func(a *appState) {
			a.reopenPlayer(a.first())
		})
		wait(1500 * time.Millisecond)
		step(window, a, dir, "single", "5-reloaded.png", "reloaded after 1.5s", nil)
		wait(2500 * time.Millisecond)
		step(window, a, dir, "single", "5b-reloaded-later.png", "reloaded after 4s", nil)
		wait(1 * time.Second)
		step(window, a, dir, "single", "", "muted", func(a *appState) { a.first().player.SetMuted(true) })
		wait(1 * time.Second)
		step(window, a, dir, "single", "", "unmuted", func(a *appState) { a.first().player.SetMuted(false) })
		wait(1 * time.Second)
		step(window, a, dir, "single", "", "closed", func(a *appState) { a.close(a.first()) })
		wait(1 * time.Second)
		step(window, a, dir, "single", "6-after-close.png", "after close", nil)
		mygoRunOnMain(func() { mygo.App.Quit() })
	}()
}

// runReopen closes the only source and opens it again, giving the second
// player six seconds to show a picture, which is where a reopen behaves
// differently from a first open in this MyGo build.
func runReopen(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "reopen", "1-first-open.png", "first open after 3s", nil)
		step(window, a, dir, "reopen", "", "reopened with the old player still drawn", func(a *appState) {
			a.reopenPlayer(a.first())
		})
		wait(6 * time.Second)
		step(window, a, dir, "reopen", "2-reopened.png", "reopened after 6s", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runReopenStrict closes the only player first and opens the address again,
// which is the order this MyGo build cannot serve; it records the failure that
// reopenPlayer avoids.
func runReopenStrict(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "reopen-strict", "1-first-open.png", "first open after 3s", nil)
		step(window, a, dir, "reopen-strict", "", "closed", func(a *appState) { a.close(a.first()) })
		wait(1 * time.Second)
		step(window, a, dir, "reopen-strict", "", "reopened", func(a *appState) { a.reopenPlayer(a.first()) })
		wait(6 * time.Second)
		step(window, a, dir, "reopen-strict", "2-reopened.png", "reopened after 6s", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runPair plays two sources at once: the first shows its picture muted, the
// second keeps its sound with its video out of the view. It records whether the
// hidden player still moves.
func runPair(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "pair", "", "both playing", nil)
		step(window, a, dir, "pair", "1-a-muted-b-shown.png", "a muted", func(a *appState) {
			a.items[0].player.SetMuted(true)
		})
		wait(1 * time.Second)
		step(window, a, dir, "pair", "", "a muted b hidden", func(a *appState) {
			// Stop drawing b's video: does its sound keep playing?
			a.items[1].shown = false
		})
		wait(2 * time.Second)
		step(window, a, dir, "pair", "2-b-hidden.png", "b hidden for 2s", nil)
		wait(2 * time.Second)
		step(window, a, dir, "pair", "3-b-hidden-4s.png", "b hidden for 4s", nil)
		step(window, a, dir, "pair", "", "b shown again", func(a *appState) {
			a.items[1].shown = true
		})
		wait(1 * time.Second)
		step(window, a, dir, "pair", "4-b-shown-again.png", "b shown again", nil)
		step(window, a, dir, "pair", "", "a unmuted", func(a *appState) {
			a.items[0].player.SetMuted(false)
		})
		wait(1 * time.Second)
		step(window, a, dir, "pair", "5-both-audible.png", "both audible", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSwitch shows one source, then the other, then the first again while both
// keep playing, and closes each one in turn.
func runSwitch(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "switch", "1-a-shown.png", "a shown", nil)
		step(window, a, dir, "switch", "", "switch to b", func(a *appState) {
			a.items[0].shown = false
			a.items[1].shown = true
		})
		wait(2 * time.Second)
		step(window, a, dir, "switch", "2-b-shown.png", "b shown for 2s", nil)
		step(window, a, dir, "switch", "", "close a", func(a *appState) { a.close(a.items[0]) })
		wait(1 * time.Second)
		step(window, a, dir, "switch", "", "switch back to a (closed)", func(a *appState) {
			a.reopenPlayer(a.items[0])
			a.items[0].shown = true
			a.items[1].shown = false
		})
		wait(2 * time.Second)
		step(window, a, dir, "switch", "3-a-shown-again.png", "a shown again", nil)
		step(window, a, dir, "switch", "", "close b", func(a *appState) { a.close(a.items[1]) })
		wait(1 * time.Second)
		step(window, a, dir, "switch", "4-b-closed.png", "b closed", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runRapid opens and closes the source several times in a row, the way a view
// switched by a person would, and records every pass.
func runRapid(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		for pass := 1; pass <= 5; pass++ {
			step(window, a, dir, "rapid", "", "reopen "+itoa(pass), func(a *appState) { a.reopenPlayer(a.first()) })
			wait(400 * time.Millisecond)
			step(window, a, dir, "rapid", "", "close "+itoa(pass), func(a *appState) { a.close(a.first()) })
			wait(200 * time.Millisecond)
		}
		step(window, a, dir, "rapid", "1-after-5-passes.png", "after five passes", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runRetry closes the source, opens an address that fails, and opens the good
// address again, recording the error the view shows and the recovery.
func runRetry(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "retry", "1-playing.png", "playing", nil)
		step(window, a, dir, "retry", "", "open a missing address", func(a *appState) {
			a.first().uri = missingAddress(a.first().uri)
			a.reopenPlayer(a.first())
		})
		wait(3 * time.Second)
		step(window, a, dir, "retry", "2-failed.png", "after the failure", nil)
		step(window, a, dir, "retry", "", "open the good address again", func(a *appState) {
			a.first().uri = goodAddress(a.first().uri)
			a.reopenPlayer(a.first())
		})
		wait(4 * time.Second)
		step(window, a, dir, "retry", "3-recovered.png", "after the retry", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runScene is the P6 check on two sources: the first keeps its picture, the
// second supplies the sound, each level is set on its own, then the picture
// switches to the second source, and finally the source being played is
// removed from the session while its player is open.
func runScene(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "scene", "1-a-video-a-audio.png", "a video with a audio", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf("scene", "show 1: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("scene", "hear 1: %v", err)
			}
		})
		step(window, a, dir, "scene", "2-a-video-b-audio.png", "a video with b audio", func(a *appState) {
			if err := a.hearAudio(1, 1); err != nil {
				logf("scene", "hear 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "3-b-audio-lowered.png", "b audio at 0.25", func(a *appState) {
			if err := a.setVolume(1, 0.25); err != nil {
				logf("scene", "volume 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "4-both-audio.png", "both audio at their own levels", func(a *appState) {
			if err := a.hearBoth(0, 1, 0.5, 1); err != nil {
				logf("scene", "hear both: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "5-b-video-a-audio.png", "b video with a audio", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				logf("scene", "show 2: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("scene", "hear 1: %v", err)
			}
		})
		// The acceptance case: A's picture with B's sound, B having been shown
		// once so its engine is running.
		wait(1 * time.Second)
		step(window, a, dir, "scene", "6-a-video-b-audio-warm.png", "a video with b audio after b was shown", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf("scene", "show 1: %v", err)
			}
			if err := a.hearAudio(1, 1); err != nil {
				logf("scene", "hear 2: %v", err)
			}
		})
		wait(2 * time.Second)
		step(window, a, dir, "scene", "7-b-audio-2s-later.png", "a video with b audio, 2s later", nil)
		wait(1 * time.Second)
		step(window, a, dir, "scene", "", "remove source 1 while playing", func(a *appState) {
			if err := a.remove(0); err != nil {
				logf("scene", "remove 1: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "8-after-removal.png", "after removing the playing source", nil)
		step(window, a, dir, "scene", "", "events", func(a *appState) {
			logf("scene", "events: %s", eventList(a))
		})
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runFour opens four sources, shows one picture with another's sound, and
// removes two of them in turn, recording that every player and every session
// object stayed consistent.
func runFour(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "four", "1-four-open.png", "four sources open", nil)
		step(window, a, dir, "four", "2-video1-audio2.png", "video 1 with audio 2", func(a *appState) {
			for _, call := range []struct {
				name string
				fn   func() error
			}{
				{"show 1", func() error { return a.showVideo(0) }},
				{"hear 2", func() error { return a.hearAudio(1, 1) }},
			} {
				if err := call.fn(); err != nil {
					logf("four", "%s: %v", call.name, err)
				}
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "3-video3-audio4.png", "video 3 with audio 4", func(a *appState) {
			if err := a.showVideo(2); err != nil {
				logf("four", "show 3: %v", err)
			}
			if err := a.hearBoth(1, 3, 1, 0.5); err != nil {
				logf("four", "hear 2 and 4: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "", "remove source 2 while it is heard", func(a *appState) {
			if err := a.remove(1); err != nil {
				logf("four", "remove 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "4-after-removing-2.png", "after removing source 2", nil)
		step(window, a, dir, "four", "", "remove source 3 while it is shown", func(a *appState) {
			if err := a.remove(1); err != nil {
				logf("four", "remove 3: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "5-after-removing-3.png", "after removing source 3", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// eventList is the session's event log, newest last, as one line.
func eventList(a *appState) string {
	events := a.session.Events()
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%d:%s(%s)", event.Seq, event.Kind, event.Object))
	}
	return strings.Join(parts, " ")
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
