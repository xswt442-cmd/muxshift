// The scripted checks: each drives the players and the session through their
// own methods and captures the window, so what is recorded is the players'
// state rather than a click no test can make.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

// scenario runs a scripted check against the window and its sources.
type scenario func(window *mygo.Window, a *appState, dir string)

// scenarios are the checks -scenario names.
var scenarios = map[string]scenario{
	"single":           runSingle,
	"reopen":           runReopen,
	"reopen-strict":    runReopenStrict,
	"handover-fail":    runHandoverFail,
	"reload-rapid":     runReloadRapid,
	"pair":             runPair,
	"replay-swap":      runReplaySwap,
	"switch":           runSwitch,
	"rapid":            runRapid,
	"retry":            runRetry,
	"scene":            runScene,
	"four":             runFour,
	"audio":            runAudio,
	"sync":             runSync,
	"syncgroup":        runSyncGroup,
	"syncmap":          runSyncMap,
	"syncfail":         runSyncFail,
	"dvr":              runDVR,
	"replayprobe":      runReplayProbe,
	"align-probe":      runAlignProbe,
	"align-async":      runAlignAsync,
	"commands":         runCommands,
	"webview":          runWebView,
	"workspaces":       runWorkspaces,
	"workbench":        runWorkbench,
	"matrix":           runMatrix,
	"lifecycle":        runLifecycle,
	"sources":          runSources,
	"settings":         runSettings,
	"settings-restart": runSettingsRestart,
	"keys":             runKeys,
	"narrow":           runNarrow,
	"sizes":            runSizes,
	"sizes-empty":      runSizesEmpty,
	"dpi":              runDPI,
	"hidden":           runHidden,
	"contract":         runContract,
	"lease":            runLease,
	"sweep":            runSweep,
	"replay":           runReplay,
	"swap":             runSwap,
	"swap-two":         runSwapTwo,
	"common":           runCommon,
	"midjoin":          runMidJoin,
}

// runReplayProbe narrows down why a replay player added mid-run does not start:
// it opens a known-good recorded playlist while the live players are paused, then
// resumes them, and reports the replay player at each step.
func runReplayProbe(window *mygo.Window, a *appState, dir string) {
	const scenario = "replayprobe"
	url := replayProbeURL
	if url == "" {
		url = "http://127.0.0.1:8791/replay-from1/index.m3u8"
	}
	step(window, a, dir, scenario, "", "live sources at start", nil)
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "", "pause the live output", func(a *appState) {
		logGroup(scenario, "pause", a.pauseAll())
	})
	time.Sleep(2 * time.Second)
	index := -1
	step(window, a, dir, scenario, "", "add and show the recorded source while paused", func(a *appState) {
		it, err := a.add(url)
		if err != nil {
			logf(scenario, "open replay: %v", err)
			return
		}
		a.items = append(a.items, it)
		index = len(a.items) - 1
		if err := a.showVideo(index); err != nil {
			logf(scenario, "show replay: %v", err)
		}
	})
	if index < 0 {
		return
	}
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "", "recorded source three seconds after showing while paused", nil)
	// Sample for a while: a recorded source added mid-run has been seen to take
	// far longer than a startup source before it presents its first picture.
	for i := 1; i <= 8; i++ {
		time.Sleep(5 * time.Second)
		step(window, a, dir, scenario, "", fmt.Sprintf("recorded source %d0 seconds after showing", i*5), nil)
	}
	step(window, a, dir, scenario, "", "resume the live group", func(a *appState) {
		logGroup(scenario, "play", a.playAll())
	})
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "1-replay-after-resume.png", "recorded source after the live group resumed", nil)
	step(window, a, dir, scenario, "", "seek inside the recorded source", func(a *appState) {
		it, err := a.at(index)
		if err != nil || it.player == nil {
			logf(scenario, "seek: %v", err)
			return
		}
		it.player.Seek(4 * time.Second)
	})
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "2-replay-after-seek.png", "recorded source after a four second seek", nil)
	mygoRunOnMain(func() { mygo.App.Quit() })
}

// syncAt and syncOffset are the syncmap measurement points, set by flags.
var (
	syncAt     time.Duration
	syncOffset time.Duration
)

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
		// A page change or a selection does not paint by itself; the window
		// only redraws when it is told the content moved.
		window.Invalidate()
	})
	if path != "" {
		// A capture taken in the same pass as the change shows the previous
		// frame: the raster of the window is painted between passes. Let a few
		// frames go by, then capture twice with a pass in between and keep the
		// second: switching which video is drawn also leaves one stale frame.
		time.Sleep(600 * time.Millisecond)
	}
	mygoRunOnMain(func() {
		if path != "" {
			_ = capture(window, filepath.Join(dir, path))
		}
	})
	if path != "" {
		time.Sleep(400 * time.Millisecond)
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

// runSync aligns two sources that carry the same picture 2.5 seconds apart.
//
// It pauses the group on the first source, draws the second while paused so the
// two timecodes can be compared, applies offsets through the panel the window
// uses, and captures after each one. The paused group is what makes the
// comparison possible: both players hold their position, so a difference in the
// burnt-in timecode is the difference the offsets have to remove.
func runSync(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the second one is shown once before anything is measured.
		step(window, a, dir, "sync", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(2 * time.Second)
		// The case the app is for: the first source's picture with the second
		// source's sound. Both are in the scene, so a group command reaches both
		// players even though only one picture is drawn.
		step(window, a, dir, "sync", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(4 * time.Second)

		// The way out of the conservative mute is visible and reversible: the
		// button lets a person who can see both pictures allow the sound, and
		// pressing it again puts the hold back.
		step(window, a, dir, "sync", "", "second sound allowed while its offset is unknown", func(a *appState) {
			if err := a.allowAudio(1); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "", "second sound held again", func(a *appState) {
			if err := a.allowAudio(1); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)

		step(window, a, dir, "sync", "", "paused, event anchored on the first source", func(a *appState) {
			// Pause first: the event time has to be the position the group stops
			// at, not a sample taken while the players are still moving.
			logGroup("sync", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "1-paused-a.png", "first picture while paused", nil)

		step(window, a, dir, "sync", "", "second picture while paused", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
			// A seek is what makes a paused player hand over a new picture, so a
			// zero nudge is used to draw the frame this offset puts on screen.
			// It also places the second source on the timeline, which is what
			// lets its sound through: until then it is held silent.
			a.nudgeAndRecord(1, 0)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "2-paused-b-before.png", "second picture before any offset", nil)

		step(window, a, dir, "sync", "", "second delayed by 2s", func(a *appState) {
			a.nudgeAndRecord(1, -2000*time.Millisecond)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "3-paused-b-after.png", "second picture after -2s", nil)

		step(window, a, dir, "sync", "", "second delayed 0.1s more", func(a *appState) {
			a.nudgeAndRecord(1, -100*time.Millisecond)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "sync", "4-b-minus-0.1.png", "second picture after another -0.1s", nil)

		step(window, a, dir, "sync", "", "second moved 0.1s back", func(a *appState) {
			a.nudgeAndRecord(1, 100*time.Millisecond)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "sync", "5-b-plus-0.1.png", "second picture after +0.1s", nil)

		step(window, a, dir, "sync", "", "delay past the start of the stream is refused", func(a *appState) {
			a.nudgeAndRecord(1, -60*time.Second)
		})
		step(window, a, dir, "sync", "6-refused.png", "after the refused delay", nil)

		step(window, a, dir, "sync", "", "sound back on the first source", func(a *appState) {
			if err := a.hearAudio(0, 1); err != nil {
				a.lastErr = err
			}
		})

		step(window, a, dir, "sync", "", "group resumed", func(a *appState) {
			logGroup("sync", "play", a.playAll())
		})
		wait(2 * time.Second)
		step(window, a, dir, "sync", "", "group paused again", func(a *appState) {
			// Pause first: the event time has to be the position the group stops
			// at, not a sample taken while the players are still moving.
			logGroup("sync", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "7-paused-again.png", "second picture after resume and pause", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSyncFail pauses a group whose second member stops playing mid-session, to
// show that one failing member is reported instead of pretending the group
// stopped together.
func runSyncFail(window *mygo.Window, a *appState, dir string) {
	go func() {
		time.Sleep(4 * time.Second)
		step(window, a, dir, "syncfail", "", "both sources playing", func(a *appState) {
			if err := a.hearAudio(1, 1); err != nil {
				logf("syncfail", "could not put the second source in the scene: %v", err)
			}
		})
		// Fault injection with the player factory the app really uses: the second
		// player is pointed at an address that answers 404, which is what a
		// source that breaks mid-session looks like to the group.
		step(window, a, dir, "syncfail", "", "second player pointed at a missing stream", func(a *appState) {
			it := a.items[1]
			broken, err := openPlayer("http://127.0.0.1:8790/missing.m3u8")
			if err != nil {
				a.lastErr = err
				return
			}
			old := it.player
			it.player = broken
			it.generation++
			if old != nil {
				old.Close()
			}
		})
		time.Sleep(2 * time.Second)
		step(window, a, dir, "syncfail", "", "pause with a failing member", func(a *appState) {
			logGroup("syncfail", "pause", a.pauseAll())
		})
		time.Sleep(time.Second)
		step(window, a, dir, "syncfail", "", "resume with a failing member", func(a *appState) {
			logGroup("syncfail", "play", a.playAll())
		})
		time.Sleep(time.Second)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSyncGroup shows that pause and play act on the session's sync group rather
// than on whatever the scene selects: a source the scene shows after the group
// was made keeps playing, and a source removed from the session leaves the group
// without any command going to a player that no longer exists.
// positions is every live player position, used to show that a probe left the
// players where they were.
func (a *appState) positions() []string {
	out := make([]string, 0, len(a.items))
	for index, it := range a.items {
		if it.player == nil {
			out = append(out, fmt.Sprintf("%d: none", index+1))
			continue
		}
		out = append(out, fmt.Sprintf("%d: %s", index+1, it.player.Position()))
	}
	return out
}

func describeShifts(shifts []core.Shift) string {
	if len(shifts) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(shifts))
	for _, shift := range shifts {
		parts = append(parts, fmt.Sprintf("%s %+.3fs", shift.Track, shift.Offset.Seconds()))
	}
	return strings.Join(parts, ", ")
}

func sameShifts(before, after []core.Shift) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index] != after[index] {
			return false
		}
	}
	return true
}

func runSyncGroup(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the second source is shown once before it can be measured.
		step(window, a, dir, "syncgroup", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(2 * time.Second)
		step(window, a, dir, "syncgroup", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)
		step(window, a, dir, "syncgroup", "", "group created by the first sync command", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after creation %s", a.groupState())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "1-group-paused.png", "group paused", nil)

		step(window, a, dir, "syncgroup", "", "group resumed", func(a *appState) {
			logGroup("syncgroup", "play", a.playAll())
		})
		wait(2 * time.Second)

		// The third source joins the picture but not the group: the scene now
		// shows a source that is not being synchronized.
		step(window, a, dir, "syncgroup", "", "third source shown, outside the group", func(a *appState) {
			if err := a.showVideo(2); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after scene change %s", a.groupState())
		})
		wait(2 * time.Second)
		step(window, a, dir, "syncgroup", "", "pause the group while the third source plays", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "2-third-still-playing.png", "group paused, third source outside it", nil)
		wait(1500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "", "third source a second and a half later", nil)

		step(window, a, dir, "syncgroup", "", "group resumed, second source removed", func(a *appState) {
			logGroup("syncgroup", "play", a.playAll())
			if err := a.remove(1); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after removal %s", a.groupState())
		})
		wait(time.Second)
		step(window, a, dir, "syncgroup", "3-after-removal.png", "second source removed from the session", nil)

		step(window, a, dir, "syncgroup", "", "pause the group after its member left", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "4-paused-after-removal.png", "group paused with the surviving member", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// groupState is every sync group of the session as one line, so a run shows who
// the pause commands reach and how a removal changed that.
func (a *appState) groupState() string {
	groups := a.session.SyncGroups()
	if len(groups) == 0 {
		return "groups=none"
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		parts = append(parts, fmt.Sprintf("%s ref=%s tracks=%s", group.ID, group.Reference, trackList(group.Tracks)))
	}
	return "groups=" + strings.Join(parts, ";")
}

// runSyncMap measures one source at a known target time. It pauses the source,
// seeks it to `-at` plus `-offset`, plays a moment so the captured picture cannot
// be a leftover frame, and logs the position the player reports at capture time.
// Reading the burnt-in timecode of that picture says what the player's position
// means for this source, which is the ground truth an alignment claim needs.
func runSyncMap(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "syncmap", "", "paused", func(a *appState) {
			logGroup("syncmap", "pause", a.pauseAll())
		})

		target := syncAt + syncOffset
		step(window, a, dir, "syncmap", "", "seek to "+target.String(), func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Seek(target)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "syncmap", "", "play a moment", func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Play()
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "syncmap", "map.png", "captured while paused at the target", func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Pause()
			logf("syncmap", "at=%s offset=%s target=%s reported=%s", syncAt, syncOffset, target, it.player.Position())
		})

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// logGroup records what a group command reported.
func logGroup(scenario, what string, problems []string) {
	if len(problems) == 0 {
		logf(scenario, "%s: every group member accepted the command", what)
		return
	}
	logf(scenario, "%s: %s", what, strings.Join(problems, "; "))
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

// runHandoverFail reopens a working source onto an address that never answers,
// so the replacement never gets a picture. The handover has to keep the player
// that still works, close the replacement, and say why in the source line; then
// reopening the real address has to work again.
func runHandoverFail(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "handover-fail", "1-before.png", "playing before the failed reopen", nil)
		step(window, a, dir, "handover-fail", "", "reopened onto an address that never answers", func(a *appState) {
			it := a.first()
			it.uri = "http://10.255.255.1/hls/index.m3u8"
			a.reopenPlayer(it)
		})
		wait(handoverWait + 2*time.Second)
		step(window, a, dir, "handover-fail", "2-kept-previous.png", "after the handover gave up", nil)
		step(window, a, dir, "handover-fail", "", "reopened onto the working address again", func(a *appState) {
			it := a.first()
			it.uri = a.session.Sources()[0].URI
			a.reopenPlayer(it)
		})
		wait(6 * time.Second)
		step(window, a, dir, "handover-fail", "3-recovered.png", "after reopening the working address", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runReloadRapid presses Reload several times faster than the handover can
// finish. Every task has to stay with the player it started for, so the last
// reload owns the source at the end, no player is closed twice, and the picture
// still arrives.
func runReloadRapid(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "reload-rapid", "1-before.png", "playing before the burst", nil)
		for i := 0; i < 5; i++ {
			step(window, a, dir, "reload-rapid", "", "reload "+itoa(i+1)+" of 5", func(a *appState) {
				a.reopenPlayer(a.first())
			})
			wait(150 * time.Millisecond)
		}
		wait(8 * time.Second)
		step(window, a, dir, "reload-rapid", "2-after-burst.png", "after the burst", func(a *appState) {
			logf("reload-rapid", "live players=%s previous=%s handover=%s",
				itoa(a.livePlayers()), boolString(a.first().previous != nil), boolString(a.first().handover != nil))
		})
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

// runDVR checks the replay window: the buffer keeps filling while the output is
// paused, two sources agree on the part of history both can play, and a player
// opens that part through the loopback endpoint and jumps inside it.
func runDVR(window *mygo.Window, a *appState, dir string) {
	const scenario = "dvr"
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	// The last card is the spare the recording takes over: a video element
	// added while the window draws freezes its repaint, so the card exists from
	// startup and the replay reopens it in place.
	liveCount := len(a.items) - 1
	uris := make([]string, liveCount)
	for i := 0; i < liveCount; i++ {
		it := a.items[i]
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address; the window needs live playlists", i+1)
			return
		}
		if it.video == "" {
			logf(scenario, "source %d did not open: %s", i+1, errorString(it.openErr))
			return
		}
		uris[i] = it.uri
	}
	spare := a.items[liveCount]
	if spare.player == nil {
		logf(scenario, "the spare card did not open: %s", errorString(spare.openErr))
		return
	}

	sources := make([]replaySource, 0, liveCount)
	bufferDirs := make([]string, 0, liveCount)
	for index := 0; index < liveCount; index++ {
		bufferDir := a.dvrDirFor(fmt.Sprintf("source-%d", index+1))
		recordingsDir := a.dvrRecordingsDirFor(fmt.Sprintf("source-%d", index+1))
		runner, err := startDVR(uris[index], dvrFromMedia(bufferDir, recordingsDir, dvrRetention, dvrMaxBytes, dvrRecordingBytes, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open replay buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		sources = append(sources, replaySource{index: index, runner: runner})
		bufferDirs = append(bufferDirs, bufferDir)
	}
	defer func() {
		// The buffer owns its directory: closing it must leave nothing behind.
		mygoRunOnMain(func() {
			for i, source := range sources {
				if err := source.runner.Close(); err != nil {
					logf(scenario, "source %d: close buffer: %v", i+1, err)
				}
				if _, err := os.Stat(bufferDirs[i]); err == nil {
					logf(scenario, "source %d: the cache directory survived the close", i+1)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	// Wait for the pollers to collect a few segments of each stream.
	time.Sleep(8 * time.Second)
	for i, source := range sources {
		logf(scenario, "buffered at %s: %s", shortURI(uris[i]), source.runner.state())
	}

	// The second stream is the same content two seconds later, so it needs a
	// -2s offset to sit on the first one's clock.
	step(window, a, dir, scenario, "1-live.png", "both live sources playing", func(a *appState) {
		for _, source := range sources {
			it, err := a.at(source.index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			offset := time.Duration(0)
			if source.index == 1 {
				offset = -2000 * time.Millisecond
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, offset); err != nil {
					logf(scenario, "align %s: %v", track, err)
				}
			}
		}
		a.apply()
	})

	// Pausing the output must not pause the input: the window only helps if it
	// keeps growing while the picture stands still. Both sources join the scene
	// first, so the sync group that pause acts on covers both of them.
	step(window, a, dir, scenario, "2-paused.png", "pause the output", func(a *appState) {
		if err := a.showVideo(0); err != nil {
			logf(scenario, "show source 1: %v", err)
		}
		if err := a.hearAudio(1, 1); err != nil {
			logf(scenario, "hear source 2: %v", err)
		}
		logGroup(scenario, "pause", a.pauseAll())
	})
	for i, source := range sources {
		logf(scenario, "while paused, at %s: %s", shortURI(uris[i]), source.runner.state())
	}
	time.Sleep(6 * time.Second)
	for i, source := range sources {
		logf(scenario, "six seconds paused, at %s: %s", shortURI(uris[i]), source.runner.state())
	}

	var start, end time.Duration
	var windowErr error
	mygoRunOnMain(func() {
		start, end, windowErr = a.commonReplay(sources)
	})
	if windowErr != nil {
		logf(scenario, "common window: %v", windowErr)
	} else {
		logf(scenario, "common window on the shared event clock: %s..%s", start, end)
	}

	// The buffer proved it keeps growing while the output is paused; the group
	// The buffer proved it keeps growing while the output is paused, and the
	// recording now takes the picture in the session's own window: a player
	// created on the drawing thread can be shown at any time, which is what the
	// earlier runs of this scenario measured as "never ready".
	step(window, a, dir, scenario, "", "resume the group before opening the recording", func(a *appState) {
		logGroup(scenario, "resume", a.playAll())
	})
	time.Sleep(2 * time.Second)

	replayIndex := -1
	step(window, a, dir, scenario, "", "open the recording in the session window", func(a *appState) {
		source := sources[0]
		it, err := a.at(source.index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		shift, ok := a.sync.timeline.Shift(it.audio)
		if !ok {
			shift, _ = a.sync.timeline.Shift(it.video)
		}
		media := start + shift.Offset
		recording, err := source.runner.replay(media)
		if err != nil {
			logf(scenario, "source 1: replay from %s: %v", media, err)
			return
		}
		path := recording.Path
		// The contract says where the file really starts: a request inside a
		// segment begins at that segment's first frame, not at the request.
		logf(scenario, "source 1: %s", recording)
		// The card that will hold the recording exists from startup: a video
		// element added while the window is drawing freezes its repaint, and a
		// reopened player keeps the element where it already is.
		replayed := a.items[len(a.items)-1]
		if replayed.player == nil {
			logf(scenario, "source %d did not open, so there is no card to replay in", len(a.items))
			return
		}
		replayed.uri = path
		a.reopenPlayer(replayed)
		replayIndex = len(a.items) - 1
		if err := a.showVideo(replayIndex); err != nil {
			logf(scenario, "show the recording: %v", err)
		}
		logf(scenario, "source 1: replay %s from %s in the session window", shortURI(path), media)
		// The recording is kept beside the run so a later run can open it on its
		// own; the buffer removes its own directory when it closes.
		kept := filepath.Join(dir, "replay-kept.ts")
		if err := copyFile(path, kept); err != nil {
			logf(scenario, "keep the recording: %v", err)
		} else {
			logf(scenario, "kept the recording at %s", kept)
		}
	})
	if replayIndex < 0 {
		return
	}
	replayed, err := a.at(replayIndex)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !waitPlayerReady(scenario, rawPlayer(replayed.player), 30*time.Second) {
		logf(scenario, "the replay did not report itself ready within 30s")
	}
	// Three captures five seconds apart show whether the recording advances at
	// the speed it was recorded.
	for _, sample := range []struct {
		name string
		wait time.Duration
	}{
		{"3-replay-start.png", 0},
		{"3b-replay-plus5.png", 5 * time.Second},
		{"3c-replay-plus10.png", 5 * time.Second},
	} {
		time.Sleep(sample.wait)
		step(window, a, dir, scenario, sample.name, "record the replay picture", nil)
	}

	// Jumping inside the recording is a seek into buffered media, not a new stream.
	step(window, a, dir, scenario, "", "jump six seconds into the recording", func(a *appState) {
		target := replayed.player.Position() + 6*time.Second
		replayed.player.Seek(target)
		logf(scenario, "seek replay to %s (asked %s)", replayed.player.Position(), target)
	})
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "5-replay-later.png", "record the replay picture after the jump", nil)
}

// waitPlayerReady waits for one player to present its first picture, logging its
// position every five seconds so a slow start is visible in the record.
func waitPlayerReady(scenario string, player *ui.VideoPlayer, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	next := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		var ready, playing, buffering bool
		var position, duration time.Duration
		var err error
		mygoRunOnMain(func() {
			if player == nil {
				return
			}
			ready = player.Ready()
			playing = player.Playing()
			buffering = player.Buffering()
			position = player.Position()
			duration = player.Duration()
			err = player.Err()
		})
		if ready {
			logf(scenario, "the replay started at %s of %s", position, duration)
			return true
		}
		if time.Now().After(next) {
			logf(scenario, "the replay has not started yet: position %s of %s ready=%v playing=%v buffering=%v err=%v",
				position, duration, ready, playing, buffering, err)
			next = time.Now().Add(5 * time.Second)
		}
	}
	return false
}

// runReplaySwap swaps the picture of the session to a recording that was
// registered as an ordinary source. It answers one question: with the player
// created on the drawing thread, can a recording be shown in the session's own
// window, which is where a person would want to watch it.
func runReplaySwap(window *mygo.Window, a *appState, dir string) {
	go func() {
		time.Sleep(2 * time.Second)
		step(window, a, dir, "replay-swap", "", "open with the live picture", nil)
		index := len(a.items) - 1
		step(window, a, dir, "replay-swap", "1-live.png", "show the recording in the session window", func(a *appState) {
			if err := a.showVideo(index); err != nil {
				logf("replay-swap", "%v", err)
				return
			}
			it, err := a.at(index)
			if err != nil {
				logf("replay-swap", "%v", err)
				return
			}
			logGroup("replay-swap", "scene", nil)
			logf("replay-swap", "showing %s (open=%v)", shortURI(it.uri), errorString(it.openErr))
		})
		it, err := a.at(index)
		if err != nil {
			logf("replay-swap", "%v", err)
			mygoRunOnMain(func() { mygo.App.Quit() })
			return
		}
		if !waitPlayerReady("replay-swap", rawPlayer(it.player), 30*time.Second) {
			logf("replay-swap", "the recording did not start within 30s")
		}
		time.Sleep(time.Second)
		step(window, a, dir, "replay-swap", "2-recording.png", "capture the recording picture", nil)
		step(window, a, dir, "replay-swap", "", "jump into the recording", func(a *appState) {
			target := it.player.Position() + 5*time.Second
			it.player.Seek(target)
			logf("replay-swap", "seek to %s (asked %s)", it.player.Position(), target)
		})
		time.Sleep(2 * time.Second)
		step(window, a, dir, "replay-swap", "3-recording-later.png", "capture after the jump", nil)
		mygoRunOnMain(func() { mygo.App.Quit() })
	}()
}

// copyFile copies one file to another, which is how a recording leaves the
// buffer directory before the buffer removes it.
func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}
