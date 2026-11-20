package main

import (
	"context"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// This file groups the scenario entry points in this domain. It was merged from replay_scenario.go, replaybuffers_scenario.go, replayui_scenario.go, scenario_dvr.go, scenario_playback.go, seekclose_scenario.go, seekedge_scenario.go, seekentry_scenario.go, seektxn_scenario.go, sweep_scenario.go.

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

func runReplayBuffers(window *mygo.Window, a *appState, dir string) {
	go replayBuffersBody(window, a, dir)
}

func replayBuffersBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "replaybuffers"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	if len(a.items) < 1 {
		logf(scenario, "needs a network source, got none")
		return
	}
	source, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(source.uri) {
		logf(scenario, "source 1 is not a network address: %s", shortURI(source.uri))
		return
	}

	runner, err := startDVR(source.uri, dvrFromMedia(
		a.dvrDirFor("buffers"), a.dvrRecordingsDirFor("buffers"),
		dvrRetention, 0, dvrRecordingBytes, source.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(source.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			if err := runner.Close(); err != nil {
				logf(scenario, "close the buffer: %v", err)
			}
		})
	}()

	rng, ok := waitForRange(runner, 8*time.Second, 90*time.Second)
	if !ok {
		logf(scenario, "the buffer never held a range to cut from")
		return
	}

	// What the page reports before anything is recorded: segments on disk, no
	// recordings, and a limit that is stated as a limit.
	before := runner.usage()
	logf(scenario, "before a cut: %s", runner.usageLine(source.uri))
	check(before.bufferBytes > 0, "the buffer reports no cached bytes at all")
	check(before.segments > 0, "the buffer reports no cached segments at all")
	check(before.recordings == 0, "the buffer reports %d recording(s) before any cut", before.recordings)
	check(strings.Contains(a.budgetLine(), "a limit, not what is in use"),
		"the budget line does not say it is a limit: %q", a.budgetLine())
	logf(scenario, "the limit beside it: %s", a.budgetLine())

	// A cut with a lease is what a player would hold. The page must count it, and
	// the cleanup pass must leave it alone.
	recording, lease, err := runner.recordAndHold(rng.Start)
	if err != nil {
		logf(scenario, "cut the buffer: %v", err)
		return
	}
	held := runner.usage()
	logf(scenario, "after the cut: %s", runner.usageLine(source.uri))
	check(held.recordings == 1, "recordings = %d after one cut, want 1", held.recordings)
	check(held.recordingBytes >= recording.Size,
		"the page reports %d bytes for a %d byte recording", held.recordingBytes, recording.Size)

	step(window, a, dir, scenario, "1-buffers.png", "the diagnostics page with one recording held", func(a *appState) {
		a.workspace = workspaceDiagnostics
	})
	if failures == 0 {
		logf(scenario, "ok: the page shows the cached bytes, the recording and its lease")
	}

	// The button path is the one a person uses: one pass over every buffer, with
	// its outcome kept for the page. The held file must survive it and the page
	// must say why it survived.
	heldCleanup := dvrCleanup{}
	step(window, a, dir, scenario, "2-held.png", "the page's cleanup left the held recording alone", func(a *appState) {
		a.workspace = workspaceDiagnostics
		heldCleanup = a.cleanupDVR()
	})
	logf(scenario, "cleanup while a player holds the file: %s", heldCleanup.line())
	check(heldCleanup.removed == 0, "the cleanup removed %d held recording(s)", heldCleanup.removed)
	check(heldCleanup.freed == 0, "the cleanup reports %d bytes freed while the file is held", heldCleanup.freed)
	check(len(heldCleanup.kept) == 1, "kept %d recording(s) while one was held, want 1", len(heldCleanup.kept))
	if len(heldCleanup.kept) > 0 {
		check(strings.Contains(heldCleanup.kept[0], "a player still holds it"),
			"the kept reason does not name the player's hold: %q", heldCleanup.kept[0])
	}
	if after := runner.usage(); after.recordings != 1 {
		check(false, "the held recording left the page's count: %d", after.recordings)
	}

	// Releasing the lease is what closing a player does. The next pass takes the
	// file and the page follows the bytes back to zero.
	if err := lease.Release(); err != nil {
		logf(scenario, "release the lease: %v", err)
		return
	}
	freed := dvrCleanup{}
	step(window, a, dir, scenario, "3-released.png", "the file went back and the count followed it", func(a *appState) {
		a.workspace = workspaceDiagnostics
		freed = a.cleanupDVR()
	})
	logf(scenario, "cleanup after the lease went back: %s", freed.line())
	check(freed.removed == 1, "the cleanup removed %d recording(s) after the lease went back, want 1", freed.removed)
	check(freed.freed == recording.Size, "the cleanup freed %d bytes, want the recording's %d", freed.freed, recording.Size)
	after := runner.usage()
	check(after.recordings == 0, "the page still lists %d recording(s) after the cleanup", after.recordings)
	check(after.recordingBytes == 0, "the page still reports %d recording bytes after the cleanup", after.recordingBytes)

	// Nothing is left for a second pass, and it says so.
	pageCleanup := dvrCleanup{}
	step(window, a, dir, scenario, "4-page.png", "the page's cleanup ran over a buffer with nothing to remove", func(a *appState) {
		a.workspace = workspaceDiagnostics
		pageCleanup = a.cleanupDVR()
	})
	if pageCleanup.removed != 0 {
		check(false, "the page's cleanup removed %d recording(s) when there were none", pageCleanup.removed)
	}
	if !strings.Contains(pageCleanup.line(), "removed 0 recording(s)") {
		check(false, "the page's cleanup does not say it removed nothing: %q", pageCleanup.line())
	}
	logf(scenario, "the page's own cleanup: %s", pageCleanup.line())
	logf(scenario, "replaybuffers: done: %d check(s) failed", failures)

	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
	logf(scenario, "window closed")
}

func runReplayUI(window *mygo.Window, a *appState, dir string) {
	go replayUIBody(window, a, dir)
}

func replayUIBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "replayui"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	say := func(where string) {
		for _, line := range a.noticeLines() {
			logf(scenario, "%s: %s", where, line)
		}
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", index+1, shortURI(it.uri))
			return
		}
		uris[index] = it.uri
	}

	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := "replayui-" + shortName(index)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for index, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", index+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 90s", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
	}

	step(window, a, dir, scenario, "", "put both sources on the event clock", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.showVideo(0)
		a.apply()
	})

	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[index], 0); err != nil {
			logf(scenario, "source %d: start its first recording: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording cut at 0s", index+1)
			return
		}
	}

	mygoRunOnMain(func() { _ = a.anchor(0) })
	mygoRunOnMain(func() { a.nudge(1, 0) })
	time.Sleep(time.Second)

	// 1. A target outside anything the buffers measured: the plan must refuse
	// and say so, and the dock must not describe it as a replay.
	if err := a.startSeekTxn(a.items, 10*time.Minute); err != nil {
		logf(scenario, "the refused request was reported at once: %v", err)
	}
	waitForNotice(a, func(report replayReport) bool { return report.Phase == replayRefused }, 10*time.Second)
	step(window, a, dir, scenario, "1-refused.png", "the dock after a refused request", func(a *appState) {
		say("refused")
		lines := strings.Join(a.noticeLines(), "\n")
		check(strings.Contains(lines, "refused"), "the dock does not say the request was refused: %q", lines)
		check(!strings.Contains(lines, "position settled"), "a refused request claims a settled position: %q", lines)
		a.showVideo(0)
	})

	// 2. A target inside the measured window: the account is published with the
	// positions each player reported, and still says the frame is unconfirmed.
	if err := a.startSeekTxn(a.items, 7*time.Second); err != nil {
		logf(scenario, "start the replay transaction: %v", err)
		return
	}
	waitForSeekTxn(a, 90*time.Second)
	waitForNotice(a, func(report replayReport) bool { return report.Phase == replayCommitted }, 10*time.Second)
	step(window, a, dir, scenario, "2-committed.png", "the dock after a committed replay", func(a *appState) {
		say("committed")
		lines := strings.Join(a.noticeLines(), "\n")
		check(strings.Contains(lines, "committed"), "the dock does not say the replay committed: %q", lines)
		check(strings.Contains(lines, "frame is not confirmed"), "the dock claims a confirmed frame: %q", lines)
		a.showVideo(0)
	})

	// 3. A commit that finds its second source replaced: nothing is installed,
	// and the account keeps its measurements without reporting success.
	a.beforeSeekCommit = func() {
		a.seekMu.Lock()
		a.items[1].generation++
		a.seekMu.Unlock()
	}
	if err := a.startSeekTxn(a.items, 7*time.Second); err != nil {
		logf(scenario, "start the rolled-back transaction: %v", err)
		return
	}
	// The hook has to stay in place until the preparation reaches it; clearing
	// it right after the start would race the goroutine and let the commit
	// through.
	waitForSeekTxn(a, 90*time.Second)
	a.beforeSeekCommit = nil
	waitForNotice(a, func(report replayReport) bool { return report.Phase == replayRolledBack }, 10*time.Second)
	step(window, a, dir, scenario, "3-rolledback.png", "the dock after a rolled-back replay", func(a *appState) {
		say("rolled back")
		lines := strings.Join(a.noticeLines(), "\n")
		check(strings.Contains(lines, "rolled back"), "the dock does not say the replay rolled back: %q", lines)
		check(!strings.Contains(lines, "position settled"), "a rolled-back replay claims a settled position: %q", lines)
		a.showVideo(0)
	})

	logf(scenario, "checks failed: %d", failures)
}

// waitForNotice waits until the newest account satisfies want. A missing account
// reads as "not yet", so a phase that never arrives fails on the caller's
// timeout rather than on a guess.
func waitForNotice(a *appState, want func(replayReport) bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if report, ok := a.lastReport(); ok && want(report) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForSeekTxn waits until no transaction is registered any more.
func waitForSeekTxn(a *appState, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

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
		// commonReplay converts each source's buffer window through its measured
		// anchor and then subtracts the confirmed offsets, but the numbers it
		// returns are container timestamps, not a seekable range: P16.4 may not
		// use them until the player clock is verified across segments.
		logf(scenario, "common window in container timestamps: %s..%s", start, end)
	}

	// The buffer proved it keeps growing while the output is paused, and the
	// recording now takes the picture in the session's own window: a player
	// created on the drawing thread can be shown at any time. The runs of this
	// scenario before players moved to the drawing thread measured "never ready".
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

// runReplayProbe opens a recorded playlist while the live players are paused,
// then resumes them, and reports the replay player at each step: it isolates
// whether a replay player added mid-run starts on its own.
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

func runSeekClose(window *mygo.Window, a *appState, dir string) {
	go seekCloseBody(window, a, dir)
}

func seekCloseBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seekclose"
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	runners := make([]*dvrRunner, 0, len(a.items))
	for index := range a.items {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address", index+1)
			return
		}
		name := "seekclose-" + shortName(index)
		runner, err := startDVR(it.uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, it.source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(it.uri, runner)
		runners = append(runners, runner)
	}

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer in time", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
	}

	step(window, a, dir, scenario, "", "put both sources on the event clock", func(a *appState) {
		for index := range a.items {
			it, err := a.at(index)
			if err != nil {
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				_ = a.sync.timeline.SetShift(track, 0)
			}
		}
		a.apply()
	})
	for index, runner := range runners {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runner, 0); err != nil {
			logf(scenario, "source %d: replay: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording", index+1)
			return
		}
	}
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	mygoRunOnMain(func() { _, _ = a.nudge(1, 10*time.Millisecond) })

	var note string
	mygoRunOnMain(func() {
		a.seekText = "7"
		note = a.requestReplaySeek()
	})
	logf(scenario, "started a replay seek through the entry: %s", note)
	logf(scenario, "closing now: the transaction is still preparing; the window is left to the user")

	// The window must stay interactive while the transaction runs; this goroutine
	// only waits for the process to end.
	time.Sleep(120 * time.Second)
	logf(scenario, "the window was not closed within 120s")
}

func runSeekEdge(window *mygo.Window, a *appState, dir string) {
	go seekEdgeBody(window, a, dir)
}

func seekEdgeBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seekedge"
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		name := "seekedge-" + shortName(index)
		runner, err := startDVR(it.uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, it.source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(it.uri, runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for _, runner := range runners {
				_ = runner.Close()
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 90s", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		logf(scenario, "source %d buffers %s", index+1, logRange(windowOf))
	}

	cuts := []time.Duration{0, 4 * time.Second}
	asks := []time.Duration{0, 300 * time.Millisecond,
		766 * time.Millisecond, 900 * time.Millisecond, 1200 * time.Millisecond}

	for index, runner := range runners {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			continue
		}
		for _, cut := range cuts {
			for _, ask := range asks {
				// Every request gets a player of its own: a seek observed on a
				// player that has already refused one would mix the two, and
				// the point here is what a fresh player does with one ask.
				if err := a.playRecording(it, runner, cut); err != nil {
					logf(scenario, "source %d cut %s: open: %v", index+1, cut, err)
					break
				}
				if !waitRecording(a, index, cut, 40*time.Second) {
					logf(scenario, "source %d cut %s: never showed a picture", index+1, cut)
					break
				}
				time.Sleep(600 * time.Millisecond)
				rec := runOnMainValue(func() *dvr.Recording { return it.replay })
				before := runOnMainValue(func() time.Duration { return it.player.Position() })
				runOnMainValue(func() int {
					it.player.Pause()
					it.player.Seek(ask)
					return 0
				})
				var trail []string
				for step := 0; step < 8; step++ {
					time.Sleep(200 * time.Millisecond)
					frame := runOnMainValue(func() edgeSample {
						return edgeSample{position: it.player.Position(), ready: it.player.Ready(), buffering: it.player.Buffering(), err: it.player.Err()}
					})
					trail = append(trail, fmt.Sprintf("%dms=%s%s",
						(step+1)*200, sceneClock(frame.position), edgeFlag(frame)))
				}
				name := "no file"
				if rec != nil && ask == asks[0] && dir != "" {
					kept := filepath.Join(dir, fmt.Sprintf("cut-%dms.ts", cut.Milliseconds()))
					if err := copyFile(rec.Path, kept); err != nil {
						logf(scenario, "source %d cut %s: keep the file: %v", index+1, cut, err)
					} else {
						logf(scenario, "source %d cut %s: kept %s", index+1, cut, kept)
					}
				}
				if rec != nil {
					name = fmt.Sprintf("%d bytes, %s", rec.Size, probeEdge(rec.Path))
				}
				logf(scenario, "source %d cut %s: opened at %s, asked %s → %s | %s",
					index+1, cut, sceneClock(before), ask, strings.Join(trail, " "), name)
				// Then play from the same ask for a second: a refused seek
				// shows up as movement starting from somewhere else.
				runOnMainValue(func() int {
					it.player.Play()
					return 0
				})
				time.Sleep(time.Second)
				played := runOnMainValue(func() time.Duration { return it.player.Position() })
				logf(scenario, "source %d cut %s: asked %s then played 1s → %s", index+1, cut, ask, sceneClock(played))
			}
			// Which of the two happened to a 0s request: ignored, or carried
			// out and then clamped to the earliest position the player can
			// report? Seeking to 3s first, then to 0s, tells them apart: an
			// ignored request leaves the player at 3s, a clamp moves it down
			// to the floor.
			if err := a.playRecording(it, runner, cut); err == nil && waitRecording(a, index, cut, 40*time.Second) {
				time.Sleep(600 * time.Millisecond)
				runOnMainValue(func() int {
					it.player.Pause()
					it.player.Seek(3 * time.Second)
					return 0
				})
				time.Sleep(1200 * time.Millisecond)
				afterThree := runOnMainValue(func() time.Duration { return it.player.Position() })
				runOnMainValue(func() int {
					it.player.Seek(0)
					return 0
				})
				time.Sleep(1500 * time.Millisecond)
				afterZero := runOnMainValue(func() time.Duration { return it.player.Position() })
				logf(scenario, "source %d cut %s: seek 3s → %s, then seek 0s → %s",
					index+1, cut, sceneClock(afterThree), sceneClock(afterZero))
			}
		}
	}
	if dir != "" {
		step(window, a, dir, scenario, "1-edge.png", "the stage while the edge is measured", func(a *appState) {
			a.showVideo(0)
		})
	}
	logf(scenario, "done")
}

// edgeSample is what the player reports at one instant while a seek is being
// observed; kept small so the trail can say whether a still position is a
// refusal, a buffering stall or a player that has not become ready yet.
type edgeSample struct {
	position  time.Duration
	ready     bool
	buffering bool
	err       error
}

// flag names what the player was doing, so a trail of equal positions is not
// mistaken for a frozen player.
func edgeFlag(frame edgeSample) string {
	switch {
	case frame.err != nil:
		return " err=" + frame.err.Error()
	case !frame.ready:
		return " not-ready"
	case frame.buffering:
		return " buffering"
	}
	return ""
}

// probeEdge asks ffprobe what the cut file says about its own start: the first
// stream timestamps and the first frames' presentation times.
func probeEdge(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=start_time,duration:frame=pts_time,best_effort_timestamp_time",
		"-read_intervals", "%+#3", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		return "ffprobe: " + err.Error()
	}
	trimmed := strings.TrimSpace(strings.ReplaceAll(string(out), "\n", " | "))
	return trimmed
}

func runSeekEntry(window *mygo.Window, a *appState, dir string) {
	go seekEntryBody(window, a, dir)
}

func seekEntryBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seekentry"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, len(a.items))
	for index := range a.items {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", index+1, shortURI(it.uri))
			return
		}
		uris[index] = it.uri
	}

	runners := make([]*dvrRunner, 0, len(uris))
	for index, uri := range uris {
		name := "seekentry-" + shortName(index)
		runner, err := startDVR(uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uri, runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for index, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", index+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 90s", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		logf(scenario, "source %d buffers %s", index+1, logRange(windowOf))
	}

	step(window, a, dir, scenario, "", "confirm both sources on the event clock", func(a *appState) {
		for index := range a.items {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.showVideo(0)
		a.apply()
	})

	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[index], 0); err != nil {
			logf(scenario, "source %d: start its first recording: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording cut at 0s", index+1)
			return
		}
	}
	time.Sleep(2 * time.Second)
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	// The second source needs a recorded origin of its own; a shift written
	// straight into the timeline is a statement without a measurement, and the
	// seek plan refuses to lean on it. Nudging is how the window records one.
	var nudgeNote string
	mygoRunOnMain(func() { nudgeNote, _ = a.nudge(1, 10*time.Millisecond) })
	logf(scenario, "source 2 nudged by 10ms: %s", nudgeNote)
	time.Sleep(500 * time.Millisecond)

	// The entry computes the window itself, so the scenario only records what a
	// user would see: the window, and the numbers the entry will accept.
	start, end, windowErr := a.replayWindowForEntry()
	if windowErr != nil {
		logf(scenario, "the entry offers no window: %v", windowErr)
		return
	}
	logf(scenario, "the entry offers %s..%s (event time)", sceneClock(start), sceneClock(end))

	step(window, a, dir, scenario, "1-window.png", "the inspector with the seek entry", func(a *appState) {
		a.showVideo(0)
	})

	generations := make([]int, 2)
	for index := 0; index < 2; index++ {
		generations[index] = a.items[index].generation
	}

	// A valid target goes through the entry, and the entry hands it to the same
	// transaction the phase already tested.
	var validNote string
	mygoRunOnMain(func() {
		a.seekText = "7"
		validNote = a.requestReplaySeek()
	})
	logf(scenario, "a valid request said: %s", validNote)
	if !waitSeekDone(a, 90*time.Second) {
		logf(scenario, "the transaction did not finish within 90s")
		return
	}
	time.Sleep(1500 * time.Millisecond)
	report, ok := a.lastReport()
	logf(scenario, "after the valid request the account says: %s", report.Line())
	check(ok && report.Phase == replayCommitted, "the account is %v, want a committed replay", report.Phase)
	for index := 0; index < 2; index++ {
		check(a.items[index].generation == generations[index]+1,
			"source %d: generation = %d, want %d", index+1, a.items[index].generation, generations[index]+1)
	}
	step(window, a, dir, scenario, "2-committed.png", "after the entry committed a replay seek", func(a *appState) {
		a.showVideo(0)
	})

	// A target outside the window is refused by the entry, and nothing about the
	// players changes: same generations, same account.
	before := make([]int, 2)
	for index := 0; index < 2; index++ {
		before[index] = a.items[index].generation
	}
	var invalidNote string
	var invalidStarted bool
	mygoRunOnMain(func() {
		a.seekText = "999"
		invalidNote = a.requestReplaySeek()
		invalidStarted = a.seekInFlight() != nil
	})
	logf(scenario, "an out-of-window request said: %s", invalidNote)
	check(invalidNote != "", "an out-of-window request produced no sentence")
	check(!invalidStarted, "an out-of-window request started a transaction")
	for index := 0; index < 2; index++ {
		check(a.items[index].generation == before[index],
			"source %d: an out-of-window request changed the player (%d → %d)", index+1, before[index], a.items[index].generation)
	}
	step(window, a, dir, scenario, "3-refused.png", "the refusal stays on the page and the stage is unchanged", func(a *appState) {
		a.showVideo(0)
	})

	// A second request while one is preparing is refused rather than replacing
	// the first: the entry says so, and only the first transaction commits.
	var firstNote, secondNote string
	mygoRunOnMain(func() {
		a.seekText = "9"
		firstNote = a.requestReplaySeek()
		secondNote = a.requestReplaySeek()
	})
	logf(scenario, "two requests in a row said: %q then %q", firstNote, secondNote)
	check(secondNote != "" && secondNote != firstNote, "the second request was not refused on its own")
	if waitSeekDone(a, 90*time.Second) {
		time.Sleep(time.Second)
	}
	open, given := a.leaseStats()
	logf(scenario, "leases open=%d given=%d, players=%d", open, given, openPlayers(a))
	check(open == len(a.items), "the run holds %d lease(s), want %d", open, len(a.items))

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ = a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	logf(scenario, "checks failed: %d", failures)
}

// waitSeekDone waits until no transaction is preparing and reports whether one
// was seen running at all.
func waitSeekDone(a *appState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		a.seekMu.Lock()
		running := a.seek != nil
		a.seekMu.Unlock()
		if !running {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runSeekTxn(window *mygo.Window, a *appState, dir string) {
	go seekTxnBody(window, a, dir)
}

func seekTxnBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seektxn"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", index+1, shortURI(it.uri))
			return
		}
		uris[index] = it.uri
	}

	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := "seektxn-" + shortName(index)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for index, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", index+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 90s", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		logf(scenario, "source %d buffers %s", index+1, logRange(windowOf))
	}

	// Put both sources on the event clock before a recording exists: the
	// content offset between the fixtures is zero, so a confirmed zero is the
	// honest statement.
	step(window, a, dir, scenario, "", "confirm both sources on the event clock", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.showVideo(0)
		a.apply()
	})

	// Both sources must already be replaying: the transaction plans against the
	// recording an item shows, and a live item is refused.
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[index], 0); err != nil {
			logf(scenario, "source %d: start its first recording: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording cut at 0s", index+1)
			return
		}
		logf(scenario, "source %d replays a recording cut at 0s", index+1)
	}
	time.Sleep(2 * time.Second)

	// Calibrate against the buffer the player is really reading, which is what
	// a seek plan may lean on.
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	logf(scenario, "source 1 is the reference; its tracks are placed on its buffer clock")
	var nudgeNote string
	mygoRunOnMain(func() {
		nudgeNote, _ = a.nudge(1, 10*time.Millisecond)
	})
	logf(scenario, "source 2 nudged by 10ms: %s", nudgeNote)
	time.Sleep(500 * time.Millisecond)

	_, givenBefore := a.leaseStats()
	before := make([]int, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		before[index] = it.generation
	}

	// The target is 7s while the fixtures' segments are 2s long, so the
	// recordings are cut at 6s and the target sits 1s into each file. A run
	// that only asked for the start of a file would land one second short.
	target := 7 * time.Second
	wantOffset := time.Second
	if err := a.startSeekTxn(a.items, target); err != nil {
		logf(scenario, "start the replay transaction: %v", err)
		return
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running == nil {
			break
		}
		if !time.Now().Before(deadline) {
			logf(scenario, "the transaction did not finish within 90s")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)

	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		check(it.generation == before[index]+1, "source %d: generation = %d, want %d", index+1, it.generation, before[index]+1)
		check(it.replay != nil, "source %d: no recording after the transaction", index+1)
		if it.replay != nil {
			logf(scenario, "source %d now shows a recording cut at %s", index+1, sceneClock(it.replay.StartBufferTime))
		}
		check(it.player != nil, "source %d: no player after the transaction", index+1)
		if it.player == nil {
			continue
		}
		landing := landingFor(a, it.source.ID)
		snapshot := runOnMainValue(func() seekFrame {
			return seekFrame{position: it.player.Position(), ready: it.player.Ready(), err: it.player.Err()}
		})
		logf(scenario, "source %d requested %s, was told %s, reported %s now (residual %s); a position, not a confirmed frame",
			index+1, sceneClock(landing.requested), sceneClock(landing.reported), sceneClock(snapshot.position), sceneClock(landing.residual))
		// The second source carries the 10ms nudge, so its position inside the
		// file is that much further in; the request must follow the offset.
		expected := wantOffset
		if index == 1 {
			expected += 10 * time.Millisecond
		}
		check(landing.requested == expected, "source %d: the transaction asked for %s, want %s", index+1, sceneClock(landing.requested), sceneClock(expected))
		check(snapshot.err == nil, "source %d: the player reports %v", index+1, snapshot.err)
		check(snapshot.position >= wantOffset, "source %d: the player reports %s, before the target at %s", index+1, sceneClock(snapshot.position), sceneClock(wantOffset))
		check(snapshot.position < wantOffset+5*time.Second, "source %d: the player ran to %s, well past the target at %s", index+1, sceneClock(snapshot.position), sceneClock(wantOffset))
		check(snapshot.ready, "source %d: the player is not ready after the transaction", index+1)
	}
	open, given := a.leaseStats()
	logf(scenario, "leases open=%d given=%d, players=%d", open, given, openPlayers(a))
	check(open == 2, "the transaction left %d lease(s) open, want 2", open)
	check(given == givenBefore+2, "the transaction took %d lease(s), want %d more than the %d it started with", given, 2, givenBefore)

	step(window, a, dir, scenario, "1-committed.png", "the stage after the transaction committed", func(a *appState) {
		a.showVideo(0)
	})

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ = a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	logf(scenario, "checks failed: %d", failures)
}

// shortName keeps the fixture directory names distinct without repeating the
// index arithmetic at each call.
func shortName(index int) string {
	if index == 0 {
		return "one"
	}
	return "two"
}

// landingFor returns what the transaction recorded as the player's arrival for
// one source, so the check reports the position the player really gave rather
// than the one it was asked for.
func landingFor(a *appState, source core.SourceID) seekLanding {
	for _, landing := range a.seekLandings() {
		if landing.source == source {
			return landing
		}
	}
	return seekLanding{}
}

func runSweep(window *mygo.Window, a *appState, dir string) {
	const scenario = "sweep"
	if a.dvrSession == nil {
		logf(scenario, "no session was opened")
		return
	}
	logf(scenario, "session %s lives at %s", a.dvrSession.ID, a.dvrSession.Dir)
	logf(scenario, "segments %s, recordings %s", a.dvrSession.SegmentsDir(), a.dvrSession.RecordingsDir())
	step(window, a, dir, scenario, "sweep.png", "the window while the session is live", nil)
	time.Sleep(9 * time.Second)
	logf(scenario, "still live: %s", a.dvrSession.Dir)
	a.release()
	mygo.App.Quit()
}
