// Shared helpers for the scripted checks: formatting, waiting, and the step
// that captures one screenshot.
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

// scenarioNames lists the checks in order, for messages.
func scenarioNames() string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
