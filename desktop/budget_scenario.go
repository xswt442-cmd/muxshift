package main

import (
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runBudget shows the DVR budget a buffer opened now would run with, then
// applies a different one through the same command the settings page uses. It
// is the P18.2 check: the numbers, the rule that produced them, and the file
// they were written to all in one log.
func runBudget(window *mygo.Window, a *appState, dir string) {
	go budgetBody(window, a, dir)
}

func budgetBody(window *mygo.Window, a *appState, dir string) {
	scenario := "budget"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	mygoRunOnMain(func() {
		logf(scenario, "the file asks for %s", a.settings.DVR.line())
		logf(scenario, "in force for a buffer opened now: %s from %s", a.budget.line(), a.budgetFrom)
		// The numbers in force are the file's numbers unless a flag was typed;
		// both cases have to agree with the rule the app names.
		if strings.Contains(a.budgetFrom, "settings file") {
			check(a.budget == a.settings.DVR, "without a flag the file's budget is what runs")
		} else {
			check(a.budget == a.flagBudget || a.budget.RetentionSeconds == a.flagBudget.RetentionSeconds,
				"with a flag the command line decides the budget")
		}
		check(dvrRetention == time.Duration(a.budget.RetentionSeconds)*time.Second && dvrMaxBytes == a.budget.MaxBytes &&
			dvrRecordingBytes == a.budget.RecordingBytes,
			"the buffer is built from the budget in force")

		// Apply a new budget exactly as the page does: draft, validate, write.
		a.settingsDraft = a.settings
		a.settingsDraft.DVR = dvrBudget{RetentionSeconds: 180, MaxBytes: 0, RecordingBytes: 32 << 20}
		a.applySettingsCommand()
		check(a.settingsErr == "", "apply reported %q", a.settingsErr)
		if data, err := os.ReadFile(a.settingsPath); err == nil {
			logf(scenario, "the file now holds %s", oneLine(data))
		} else {
			logf(scenario, "could not read back %s: %v", a.settingsPath, err)
		}
		logf(scenario, "after apply: %s from %s", a.budget.line(), a.budgetFrom)
		check(dvrRecordingBytes == a.budget.RecordingBytes && dvrMaxBytes == a.budget.MaxBytes,
			"the buffer follows the budget the page just applied")
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}
