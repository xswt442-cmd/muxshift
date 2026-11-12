package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runReplayBuffers checks the diagnostics page's replay-buffer numbers and the
// cleanup action behind it: the page reports what the buffer holds right now, the
// limit it is measured against is stated apart from that, and a cleanup pass only
// removes recordings a player is not reading.
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
