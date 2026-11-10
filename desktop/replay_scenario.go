// P16.3a: the smallest reproduction of "a recording does not play", and the
// comparison the ruling asked for. One real Buffer.Record file is opened three
// ways:
//
//	A. registered as an ordinary source before the window exists (one -media);
//	C. registered before the window exists but shown only while it runs;
//	B. created, registered and shown while the window runs.
//
// Playing=true, a sane Duration and a decodable file all pass while the window
// shows nothing, so every case records the player state, the generation, whether
// the source is drawn at all, and two window captures a few seconds apart whose
// burnt-in timecode says whether the picture really moved.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runReplay performs case A with one source and cases C and B with two.
func runReplay(window *mygo.Window, a *appState, dir string) {
	go func() {
		count := sourceCount(a)
		logf("replay", "started with %d source(s); players opened at boot: %d", count, count)
		if count == 0 {
			logf("replay", "no source to play")
			mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
			return
		}
		// Case A: one source, a recording registered before the window existed
		// and shown from the first frame.
		if count == 1 {
			replayCaseA(window, a, dir)
			mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
			return
		}
		// Case C: the recording was registered before the window, drawn hidden
		// from the first frame, and is shown while the window runs.
		replayCaseC(window, a, dir)
		// Case B: the same file is imported, registered and shown at runtime.
		replayCaseB(window, a, dir)
		// Case D: an existing source is reloaded onto a different recording.
		replayCaseD(window, a, dir)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

// replayCaseA plays a recording that was registered before the window opened.
func replayCaseA(window *mygo.Window, a *appState, dir string) {
	logf("replay", "case A: the recording is an ordinary source from boot: %s", replayState(a, 0))
	if !waitForReady(a, 0, 30*time.Second) {
		logf("replay", "case A: no first frame within 30s: %s", replayState(a, 0))
		step(window, a, dir, "replay", "A3-no-picture.png", "case A after 30s without a picture", nil)
		return
	}
	first := positionOf(a, 0)
	step(window, a, dir, "replay", "A1-recording.png", "case A: the recording playing", nil)
	time.Sleep(3 * time.Second)
	second := positionOf(a, 0)
	step(window, a, dir, "replay", "A2-recording-later.png", "case A: the same recording three seconds later", nil)
	logf("replay", "case A: ready=true, position %s -> %s (advanced=%v), %s",
		sceneClock(first), sceneClock(second), moved(first, second), replayState(a, 0))
}

// replayCaseC shows the recording that was registered (and drawn, hidden) from
// the first frame of the window.
func replayCaseC(window *mygo.Window, a *appState, dir string) {
	logf("replay", "case C: live %s | recording parked since boot: %s",
		replayState(a, 0), replayState(a, 1))
	if !waitForReady(a, 0, 30*time.Second) {
		logf("replay", "case C: the live source never opened: %s", replayState(a, 0))
	}
	step(window, a, dir, "replay", "C1-live.png", "case C: the live source before the switch", nil)
	// The recording has been drawn (hidden) since the first frame; this reading
	// says whether its clock ran while nothing showed it.
	parked := positionOf(a, 1)
	logf("replay", "case C: the parked recording after %s of live play: %s",
		sceneClock(parked), replayState(a, 1))

	step(window, a, dir, "replay", "C2-recording.png", "case C: the parked recording shown", func(a *appState) {
		a.report(a.runCommand(command{Kind: commandShow, Source: sourceAt(a, 1)}))
	})
	ready := waitForReady(a, 1, 15*time.Second)
	first := positionOf(a, 1)
	step(window, a, dir, "replay", "C3-recording-2s.png", "case C: two seconds after the switch", nil)
	time.Sleep(3 * time.Second)
	second := positionOf(a, 1)
	step(window, a, dir, "replay", "C4-recording-5s.png", "case C: five seconds after the switch", nil)
	logf("replay", "case C: ready=%v, position %s -> %s (advanced=%v), %s",
		ready, sceneClock(first), sceneClock(second), moved(first, second), replayState(a, 1))
	logf("replay", "case C: the live source while the recording is shown: %s", replayState(a, 0))
}

// replayCaseB imports the same file while the window runs and shows it.
func replayCaseB(window *mygo.Window, a *appState, dir string) {
	uri := uriOf(a, 1)
	before := sourceCount(a)
	logf("replay", "case B: importing %s at runtime", uri)
	mygoRunOnMain(func() { a.report(a.runCommand(command{Kind: commandAdd, URI: uri})) })
	waitUntil(30*time.Second, func() bool {
		done := false
		mygoRunOnMain(func() { done = a.importing == "" && len(a.items) > before })
		return done
	})
	added := sourceCount(a)
	if added <= before {
		logf("replay", "case B: the import did not add a source (%d source(s))", added)
		return
	}
	index := added - 1
	time.Sleep(2 * time.Second)
	logf("replay", "case B: the imported recording two seconds after the import: %s", replayState(a, index))
	step(window, a, dir, "replay", "B1-imported.png", "case B: the imported recording shown", func(a *appState) {
		a.report(a.runCommand(command{Kind: commandShow, Source: sourceAt(a, index)}))
	})
	ready := waitForReady(a, index, 15*time.Second)
	first := positionOf(a, index)
	step(window, a, dir, "replay", "B2-imported-2s.png", "case B: two seconds after showing the import", nil)
	time.Sleep(3 * time.Second)
	second := positionOf(a, index)
	step(window, a, dir, "replay", "B3-imported-5s.png", "case B: five seconds after showing the import", nil)
	logf("replay", "case B: ready=%v, position %s -> %s (advanced=%v), %s",
		ready, sceneClock(first), sceneClock(second), moved(first, second), replayState(a, index))
	// The live source has to stay alive across both switches.
	live := positionOf(a, 0)
	step(window, a, dir, "replay", "B4-live-still-there.png", "the live source after the runtime replay", func(a *appState) {
		a.report(a.runCommand(command{Kind: commandShow, Source: sourceAt(a, 0)}))
	})
	later := positionOf(a, 0)
	logf("replay", "the live source: position %s -> %s (advanced=%v), %s",
		sceneClock(live), sceneClock(later), moved(live, later), replayState(a, 0))
}

// replayCaseD points a source that already has a player at a different
// recording and reloads it, which is the handover path P16.3c has to use: the
// old picture stays drawn until the replacement has one of its own.
func replayCaseD(window *mygo.Window, a *appState, dir string) {
	next := siblingRecording(uriOf(a, 1), "clock-14s.ts")
	if next == "" {
		logf("replay", "case D: no second recording next to %s", shortURI(uriOf(a, 1)))
		return
	}
	logf("replay", "case D: reloading source 2 onto %s", shortURI(next))
	step(window, a, dir, "replay", "D1-reload-start.png", "case D before the reload", func(a *appState) {
		a.items[1].uri = next
		a.reopenPlayer(a.items[1])
		a.report(a.runCommand(command{Kind: commandShow, Source: sourceAt(a, 1)}))
	})
	ready := waitForReady(a, 1, 15*time.Second)
	first := positionOf(a, 1)
	step(window, a, dir, "replay", "D2-reloaded.png", "case D after the reload", nil)
	time.Sleep(3 * time.Second)
	second := positionOf(a, 1)
	step(window, a, dir, "replay", "D3-reloaded-3s.png", "case D three seconds after the reload", nil)
	logf("replay", "case D: ready=%v, position %s -> %s (advanced=%v), %s",
		ready, sceneClock(first), sceneClock(second), moved(first, second), replayState(a, 1))
}

// siblingRecording looks for another recording next to the first one. The
// fixtures the runs use are cut from the same playlist at different offsets, so
// the two files show different timecodes and a stale picture is visible.
func siblingRecording(first, name string) string {
	if first == "" {
		return ""
	}
	next := filepath.Join(filepath.Dir(first), name)
	if next == first {
		return ""
	}
	if _, err := os.Stat(next); err != nil {
		return ""
	}
	return next
}

// replayState is one source's player state plus the facts that decide whether a
// picture can arrive at all: the player generation, whether the scene draws it,
// and whether it is in the drawing tree.
func replayState(a *appState, index int) string {
	state := ""
	mygoRunOnMain(func() {
		if index < 0 || index >= len(a.items) {
			state = "source missing"
			return
		}
		it := a.items[index]
		parts := []string{playerState(index, it)}
		parts = append(parts,
			"generation="+itoa(it.generation),
			"drawn="+boolString(it.shown),
			"in-tree="+boolString(it.player != nil),
			"open="+errorString(it.openErr),
		)
		state = strings.Join(parts, " ")
	})
	return state
}

// uriOf reads one source's address on the drawing thread.
func uriOf(a *appState, index int) string {
	uri := ""
	mygoRunOnMain(func() {
		if index >= 0 && index < len(a.items) {
			uri = a.items[index].uri
		}
	})
	return uri
}
