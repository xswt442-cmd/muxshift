// The source memory check is two processes: one sets a level and a mute on a
// source it added by hand, the next one has to come back with them without
// writing anything itself.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runSourceMemory is the first process: it turns memory on through the page's
// own Apply path, checks that a source the command line opened is never written
// back, then changes the sound of a source it added by hand and reads the file.
func runSourceMemory(window *mygo.Window, a *appState, dir string) {
	go sourceMemoryBody(window, a, dir)
}

func sourceMemoryBody(window *mygo.Window, a *appState, dir string) {
	scenario := "sourcememory"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	step(window, a, dir, scenario, "0-before.png",
		"memory is on, and the command line opened one source",
		func(a *appState) {
			a.workspace = workspaceSettings
			a.settingsSection = "Sources"
			a.settingsDraft = cloneSettings(a.settings)
			a.settingsDraft.SourceMemory.On = true
			a.settingsDraft.SourceMemory.Remember = true
			a.applySettingsCommand()
		})
	mygoRunOnMain(func() {
		check(a.settingsErr == "", "Apply reported %q", a.settingsErr)
		check(a.settings.SourceMemory.On && a.settings.SourceMemory.Remember, "the applied settings do not have memory on")
	})

	// A source the command line opened must not be written back: replaying the
	// same flag on every start would otherwise keep saving the same preference.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() {
			ready = len(a.items) > 0 && a.items[0].player != nil && a.items[0].audio != ""
			if ready {
				if err := a.setVolume(0, 0.3); err != nil {
					logf(scenario, "setVolume on the command line source: %v", err)
				}
			}
		})
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)
	commandLine, note := loadSettings(a.settingsPath)
	mygoRunOnMain(func() {
		check(note == "", "the file loaded without a complaint (%s)", note)
		check(len(commandLine.SourceMemory.Entries) == 0,
			"the command line source left no entry (%d entr(ies))", len(commandLine.SourceMemory.Entries))
	})

	// Now add one by hand and change its sound: that is the source whose
	// preference the file is allowed to keep.
	extra := extraMedia
	var added int
	var identity string
	mygoRunOnMain(func() {
		added = len(a.items)
		if extra == "" {
			logf(scenario, "no -extra address was given, so no hand-added source is available")
			return
		}
		it, err := a.add(extra)
		if err != nil {
			logf(scenario, "add(%s): %v", identityDigest(identityOf(extra)), err)
			return
		}
		a.items = append(a.items, it)
		if err := a.hearAudio(added, 0.25); err != nil {
			logf(scenario, "hearAudio: %v", err)
		}
		if _, err := a.toggleMute(added); err != nil {
			logf(scenario, "toggleMute: %v", err)
		}
		identity = identityOf(extra)
	})
	mygoRunOnMain(func() {
		check(extra != "", "the check gave an address to add by hand")
		check(identity != "", "the -extra address can be an identity (%s)", identityDigest(identityOf(extra)))
	})

	// The write happens off the drawing thread, so wait for a newer file and
	// read it once. Watching it by reading in a loop would hold the file open
	// while the writer tries to replace it, and Windows refuses that rename.
	remembered := time.Now()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(a.settingsPath); err == nil && !info.ModTime().Before(remembered) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var saved settings
	saved, note = loadSettings(a.settingsPath)
	// The capture only succeeds while the window is actually drawn; a start that
	// has not painted yet reports "the window's content has no size". Retry a
	// few times and count a missing picture as a failure rather than leave it in
	// the log only.
	shot := filepath.Join(dir, "1-remembered.png")
	for attempt := 0; attempt < 4; attempt++ {
		time.Sleep(800 * time.Millisecond)
		step(window, a, dir, scenario, "1-remembered.png",
			"the hand-added source is audible and muted, and the file keeps its sound",
			func(a *appState) { a.workspace = workspaceMixer })
		if info, err := os.Stat(shot); err == nil && info.Size() > 0 {
			break
		}
	}
	mygoRunOnMain(func() {
		info, err := os.Stat(shot)
		check(err == nil && info.Size() > 0, "the Mixer page was captured (%v)", err)
		logf(scenario, "the file holds %s", sourceMemoryLine(saved.SourceMemory))
		check(note == "", "the file loaded without a complaint (%s)", note)
		check(len(saved.SourceMemory.Entries) == 1, "the file remembers one address (it holds %d)", len(saved.SourceMemory.Entries))
		if len(saved.SourceMemory.Entries) == 1 {
			entry := saved.SourceMemory.Entries[0]
			check(entry.Address == identity, "the remembered identity matches (%s)", identityDigest(entry.Address))
			check(entry.Volume == 0.25, "the remembered level came back at %.2f", entry.Volume)
			check(entry.Muted, "the remembered mute came back on")
		}
		// Neither the page nor the log prints a remembered address.
		check(!strings.Contains(sourceMemoryLine(saved.SourceMemory), "http") &&
			!strings.Contains(sourceMemoryLine(saved.SourceMemory), ":\\"), "the summary prints no address")
	})

	// A write must not rewrite the draft somebody may be editing.
	mygoRunOnMain(func() {
		if len(a.items) > added {
			a.settingsDraft.DefaultVolume = 0.42
			a.rememberSource(&item{uri: a.items[added].uri, volume: 0.25, muted: true, origin: originAdded})
			check(a.settingsDraft.DefaultVolume == 0.42, "remembering changed the draft's level to %.2f", a.settingsDraft.DefaultVolume)
		}
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// runSourceMemoryClear is the third process: it starts from the file the first
// one wrote and does what only an explicit Clear may do. It is separate because
// the restore check needs the entry to still be there.
func runSourceMemoryClear(window *mygo.Window, a *appState, dir string) {
	go sourceMemoryClearBody(window, a, dir)
}

func sourceMemoryClearBody(window *mygo.Window, a *appState, dir string) {
	scenario := "sourcememory-clear"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() { ready = len(a.items) > 0 && a.items[0].player != nil })
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mygoRunOnMain(func() {
		check(len(a.settings.SourceMemory.Entries) == 1, "the file starts with one remembered address (%d)", len(a.settings.SourceMemory.Entries))
	})

	// The snapshot below is taken before the Clear, so its ticket is older: it
	// is the write that must never land.
	var staleWritten bool
	mygoRunOnMain(func() {
		stale := cloneSettings(a.settings)
		staleTicket := a.saveTicket()
		a.workspace = workspaceSettings
		a.settingsSection = "Sources"
		a.beginDraft(a.settings)
		a.settingsDraft.SourceMemory.clear()
		a.draftCleared = true
		a.applySettingsCommand()
		check(a.settingsErr == "", "Apply after Clear reported %q", a.settingsErr)
		check(len(a.settings.SourceMemory.Entries) == 0,
			"Clear emptied the applied list (%d entr(ies))", len(a.settings.SourceMemory.Entries))
		if err := a.saveSettingsSerialized(stale, staleTicket); err != nil {
			logf(scenario, "the stale snapshot reported %v", err)
		}
		staleWritten = true
		// With remembering off, nothing is written at all.
		a.settings.SourceMemory.Remember = false
		a.rememberSource(&item{uri: a.items[0].uri, volume: 0.9, muted: false, origin: originAdded})
		a.settings.SourceMemory.Remember = true
	})
	if !staleWritten {
		failures++
		logf(scenario, "CHECK FAILED: the clear phase did not run")
	}
	time.Sleep(700 * time.Millisecond)
	cleared, note := loadSettings(a.settingsPath)
	mygoRunOnMain(func() {
		check(note == "", "the file loaded after Clear (%s)", note)
		check(len(cleared.SourceMemory.Entries) == 0,
			"the file kept the forgotten entries (%d entr(ies))", len(cleared.SourceMemory.Entries))
	})
	step(window, a, dir, scenario, "1-cleared.png",
		"the list is empty after an explicit Clear, and a late older write did not fill it again",
		func(a *appState) {
			a.workspace = workspaceSettings
			a.settingsSection = "Sources"
		})
	mygoRunOnMain(func() {
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// runSourceMemoryRestore is the second process: it starts with the file the
// first one wrote and the same hand-added address on the command line, so the
// remembered sound must already be in force when the first scene is applied.
func runSourceMemoryRestore(window *mygo.Window, a *appState, dir string) {
	go sourceMemoryRestoreBody(window, a, dir)
}

func sourceMemoryRestoreBody(window *mygo.Window, a *appState, dir string) {
	scenario := "sourcememory-restore"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	before, beforeErr := os.ReadFile(a.settingsPath)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() { ready = len(a.items) > 0 && a.items[0].player != nil })
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	step(window, a, dir, scenario, "1-restored.png",
		"the window starts with the remembered sound already in force",
		func(a *appState) { a.workspace = workspaceMixer })
	mygoRunOnMain(func() {
		check(a.settings.SourceMemory.On, "memory came back on")
		if len(a.items) == 0 {
			failures++
			logf(scenario, "CHECK FAILED: no source was opened")
		} else {
			it := a.items[0]
			logf(scenario, "source 1 came back at %.2f muted=%v", it.volume, it.muted)
			check(it.volume == 0.25, "the level came back at %.2f", it.volume)
			check(it.muted, "the source came back muted")
			// The card and the output have to agree: the scene level is what
			// the player is handed, so a memory that stops at it.volume would
			// leave the file saying 25% while the sound plays at 100%.
			level, routed := a.sceneLevel(it)
			check(routed && level.Volume == 0.25,
				"the scene plays it at %.2f (the remembered 0.25)", level.Volume)
			if !a.heard(it) {
				// It comes back muted, so nothing is heard yet and a silent
				// player is not handed a level. Unmute the way a person would,
				// and confirm its place, to read what the output was given.
				logf(scenario, "source 1 is silent for now (%s); unmuting it to read the output", joinWords(a.silences(it)))
				if _, err := a.toggleMute(0); err != nil {
					logf(scenario, "unmute: %v", err)
				}
				if err := a.sync.timeline.SetShift(it.audio, 0); err != nil {
					logf(scenario, "confirm the track's shift: %v", err)
				}
				a.apply()
			}
			if it.player != nil {
				check(a.heard(it), "the remembered source is audible once its place is confirmed")
				check(it.player.Volume() == a.volumeFor(0.25),
					"the player was handed %.2f (want %.2f)", it.player.Volume(), a.volumeFor(0.25))
			}
			// A command line source is never written back, even when its sound
			// came from memory: this start must leave the file alone.
			check(it.origin == originCommandLine, "the source came from the command line")
		}
		after, afterErr := os.ReadFile(a.settingsPath)
		check(beforeErr == nil && afterErr == nil, "the file was readable twice (%v / %v)", beforeErr, afterErr)
		check(string(before) == string(after), "this start left the settings file alone")
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// sourceMemoryLine summarises the memory for a log line without printing the
// remembered addresses.
func sourceMemoryLine(memory sourceMemory) string {
	parts := make([]string, 0, len(memory.Entries))
	for _, entry := range memory.Entries {
		muted := ""
		if entry.Muted {
			muted = " muted"
		}
		parts = append(parts, identityDigest(entry.Address)+muted)
	}
	return "on=" + boolWord(memory.On) + " remember=" + boolWord(memory.Remember) + " set=" + memoryDigest(memory) + " entries=[" + strings.Join(parts, " ") + "]"
}
