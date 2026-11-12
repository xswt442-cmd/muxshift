package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runBackground checks the background failure ledger: a write that cannot land
// is counted for the run, the diagnostics page states the count and the newest
// failure, clearing the status bar's message leaves the count alone, and a
// write that does land does not add to it.
func runBackground(window *mygo.Window, a *appState, dir string) {
	go backgroundBody(window, a, dir)
}

func backgroundBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "background"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	// A path a file cannot be renamed onto: renaming a file over a directory
	// fails on every system this build runs on. The settings themselves stay
	// trustworthy, so the only reason a write fails is the filesystem.
	blocked := filepath.Join(dir, "a-directory-stands-here")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		logf(scenario, "cannot set up the blocked settings path: %v", err)
		return
	}
	mygoRunOnMain(func() {
		a.settingsPath = blocked
		a.settingsTrusted = true
		a.settings.SourceMemory.On = true
		a.settings.SourceMemory.Remember = true
	})

	// ledgerState is what the scenario reads through the drawing thread: the
	// count and the newest failure's sentence.
	type ledgerState struct {
		count int
		line  string
	}
	count := func() ledgerState {
		return runOnMainValue(func() ledgerState {
			n, last := a.backgroundFailures()
			return ledgerState{count: n, line: last.line()}
		})
	}

	for i, uri := range []string{
		"https://example.test/first.m3u8",
		"https://example.test/second.m3u8",
	} {
		it := &item{uri: uri, volume: 1, origin: originAdded}
		mygoRunOnMain(func() { a.rememberSource(it) })
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if count().count > i {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	state := count()
	logf(scenario, "two writes to %s: %d failure(s) counted; newest: %s", filepath.Base(blocked), state.count, state.line)
	check(state.count == 2, "the two failed writes were counted as %d, want 2", state.count)
	check(strings.Contains(state.line, "saving a source's remembered sound"),
		"the newest failure names what failed: %q", state.line)
	check(strings.Contains(state.line, "a-directory-stands-here"),
		"the newest failure says where the write went: %q", state.line)

	step(window, a, dir, scenario, "1-failures.png",
		"the diagnostics page states what failed and how often", func(a *appState) {
			a.workspace = workspaceDiagnostics
		})
	time.Sleep(500 * time.Millisecond)

	// Dismissing the notification clears the message, not the record.
	before := runOnMainValue(func() int {
		a.lastErr = errors.New("a message in the status bar")
		a.dismissError()
		if a.lastErr != nil {
			failures++
			logf(scenario, "CHECK FAILED: dismiss left the message in place")
		}
		n, _ := a.backgroundFailures()
		return n
	})
	logf(scenario, "after dismissing the message: %d failure(s) still counted", before)
	check(before == 2, "dismissing the message changed the count to %d, want 2", before)

	// A write that lands does not add to the count, and says so.
	mygoRunOnMain(func() { a.settingsPath = filepath.Join(dir, "settings.json") })
	good := &item{uri: "https://example.test/third.m3u8", volume: 1, origin: originAdded}
	mygoRunOnMain(func() { a.rememberSource(good) })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runOnMainValue(func() bool { return a.settingsSaved.Load() }) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	saved := runOnMainValue(func() bool { return a.settingsSaved.Load() })
	after := count().count
	logf(scenario, "a write to a path that works: saved=%v, %d failure(s) counted", saved, after)
	check(saved, "the write to a working path did not land")
	check(after == 2, "a successful write changed the count to %d, want 2", after)

	step(window, a, dir, scenario, "2-after-a-good-write.png",
		"a write that landed leaves the count where it was", func(a *appState) {
			a.workspace = workspaceDiagnostics
		})
	time.Sleep(500 * time.Millisecond)

	logf(scenario, "done: %d check(s) failed", failures)
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
	logf(scenario, "window closed")
}
