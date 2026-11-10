package main

import (
	"os"
	"time"

	"github.com/egoist/mygo"
)

// runSettings edits the Settings page the way a person would: it changes the
// draft, captures it, applies it, then tries Cancel and Reset and checks that
// neither reaches the live settings. The applied file is read back from disk,
// so the last claim is about bytes, not about the page.
func runSettings(window *mygo.Window, a *appState, dir string) {
	go func() {
		step(window, a, dir, "settings", "1-general.png",
			"the Settings page opens on General; the file has not been written yet: "+a.settingsBoundary(),
			func(a *appState) {
				a.workspace = workspaceSettings
				a.settingsSection = "General"
			})

		step(window, a, dir, "settings", "2-draft.png",
			"a draft changes six values; nothing is applied until Apply",
			func(a *appState) {
				a.settingsSection = "Playback"
				a.settingsDraft.DefaultVolume = 0.3
				a.settingsDraft.NudgeStep = 0.5
				a.settingsDraft.MasterVolume = 0.5
				a.settingsDraft.MuteUnaligned = false
				a.settingsDraft.StartPage = "sources"
				a.settingsDraft.DockOpen = false
			})

		step(window, a, dir, "settings", "3-applied.png",
			"Apply validates, pushes into the players and writes the file",
			func(a *appState) {
				a.applySettingsCommand()
			})
		mygoRunOnMain(func() {
			data, err := os.ReadFile(a.settingsPath)
			logf("settings", "live nudge step %s, volume(0.5)=%.3f, saved=%v, file=%s, read back=%v (%v)",
				a.settingsNudge(-1), a.volumeFor(0.5), a.settingsSaved, a.settingsPath, oneLine(data), err)
		})

		step(window, a, dir, "settings", "4-cancelled.png",
			"Cancel after a dirty draft leaves the live settings alone",
			func(a *appState) {
				a.settingsDraft.NudgeStep = 0.9
				a.settingsDraft.DefaultVolume = 0.8
			})
		mygoRunOnMain(func() {
			a.settingsDraft = a.settings
			a.settingsNote = "cancelled: the draft is back to what is applied"
			logf("settings", "after Cancel the live nudge step is still %s", a.settingsNudge(-1))
		})

		step(window, a, dir, "settings", "5-reset-draft.png",
			"Reset puts the defaults in the draft only; Apply would write them",
			func(a *appState) {
				a.settingsDraft = defaultSettings()
				a.settingsSection = "Shortcuts"
			})
		mygoRunOnMain(func() {
			logf("settings", "after Reset the live start page is %q and the draft start page is %q",
				a.settings.StartPage, a.settingsDraft.StartPage)
		})

		step(window, a, dir, "settings", "6-dvr-disabled.png",
			"the DVR section is shown disabled with the reason, not hidden",
			func(a *appState) {
				a.settingsSection = "DVR"
			})

		step(window, a, dir, "settings", "7-diagnostics.png",
			"Diagnostics shows the real event count and the settings path",
			func(a *appState) {
				a.settingsSection = "Diagnostics"
			})

		step(window, a, dir, "settings", "8-stage-resumed.png",
			"back on the stage, the players that stood still on the settings page should advance again",
			func(a *appState) { a.workspace = workspaceStage })

		mygoRunOnMain(func() {
			time.Sleep(3 * time.Second)
		})

		mygoRunOnMain(func() {
			data, err := os.ReadFile(a.settingsPath)
			logf("settings", "after returning to the stage: %s", allStates(a))
			logf("settings", "file after the run: %s (%v)", oneLine(data), err)
			logf("settings", "done: workspace=%v, start page applied next boot=%q", a.workspace, a.settings.StartPage)
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSettingsRestart is the second process of the settings check: it starts
// with the file the first process wrote and reports what came back. A start
// page is only believable from a start, so this is a separate run.
func runSettingsRestart(window *mygo.Window, a *appState, dir string) {
	go func() {
		step(window, a, dir, "settings-restart", "1-restored.png",
			"a fresh process read the applied file: the start page, the step and the master volume came back",
			func(a *appState) {
				a.settingsSection = "Playback"
			})
		mygoRunOnMain(func() {
			logf("settings-restart", "loaded start page %q, workspace %v, nudge step %s, default volume %.2f, master %.2f, mute unaligned %v, dock open %v, saved %v",
				a.settings.StartPage, a.workspace, a.settingsNudge(-1), a.settings.DefaultVolume,
				a.settings.MasterVolume, a.settings.MuteUnaligned, a.settings.DockOpen, a.settingsSaved)
			logf("settings-restart", "the dock is %s and the window frame is %.0fx%.0f",
				boolString(a.dock.shown(a.settings.DockOpen && a.frameHeight >= dockAutoHeight)),
				a.frameWidth, a.frameHeight)
			a.release()
			mygo.App.Quit()
		})
	}()
}
