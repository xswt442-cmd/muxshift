package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// The budget is a settings field with three numbers, and each of them has a
// meaning the rest of the code depends on: a retention the buffer can run with,
// and two byte limits where zero means "no limit" rather than "nothing".
func TestDVRBudgetRejectsWhatTheBufferCannotRun(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*dvrBudget)
		field string
	}{
		{"no history", func(b *dvrBudget) { b.RetentionSeconds = 0 }, "retentionSeconds"},
		{"a day of history", func(b *dvrBudget) { b.RetentionSeconds = 86400 }, "retentionSeconds"},
		{"negative cache", func(b *dvrBudget) { b.MaxBytes = -1 }, "maxBytes"},
		{"cache beyond a terabyte", func(b *dvrBudget) { b.MaxBytes = maxBudgetBytes + 1 }, "maxBytes"},
		{"negative recording", func(b *dvrBudget) { b.RecordingBytes = -1 }, "recordingBytes"},
		{"recording beyond a terabyte", func(b *dvrBudget) { b.RecordingBytes = maxBudgetBytes + 1 }, "recordingBytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := defaultSettings()
			tc.edit(&st.DVR)
			err := validateSettings(st)
			if err == nil {
				t.Fatalf("a budget of %+v was accepted", st.DVR)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error %q does not name %s", err, tc.field)
			}
		})
	}
}

// TestDVRBudgetAcceptsNoLimit is the other half: zero bytes is legal and means
// the limit is gone, not that nothing may be stored.
func TestDVRBudgetAcceptsNoLimit(t *testing.T) {
	st := defaultSettings()
	st.DVR = dvrBudget{RetentionSeconds: minRetentionSeconds, MaxBytes: 0, RecordingBytes: 0}
	if err := validateSettings(st); err != nil {
		t.Fatalf("a budget with no byte limits was refused: %v", err)
	}
	if got := bytesLine(0); got != "no limit" {
		t.Fatalf("zero bytes reads as %q, want %q", got, "no limit")
	}
}

// TestDVRBudgetComesFromAnExplicitFlag pins the precedence: a flag a person
// actually typed wins, including when it sets a limit to zero, and the file
// wins when no flag was typed. Comparing the flag's value with its default
// would get the zero case wrong, which is exactly what this guards.
func TestDVRBudgetComesFromAnExplicitFlag(t *testing.T) {
	fromFile := defaultSettings()
	fromFile.DVR = dvrBudget{RetentionSeconds: 300, MaxBytes: 8 << 20, RecordingBytes: 1 << 30}

	flags := dvrBudget{RetentionSeconds: 60, MaxBytes: 0, RecordingBytes: 64 << 20}

	got, from, err := dvrBudgetInForce(fromFile, map[string]bool{}, flags)
	if err != nil {
		t.Fatalf("the file's budget was refused: %v", err)
	}
	if got != fromFile.DVR {
		t.Fatalf("without any flag the budget is %+v, want the file's %+v", got, fromFile.DVR)
	}
	if !strings.Contains(from, "settings file") {
		t.Fatalf("the rule that won reads %q, want the settings file", from)
	}

	got, from, err = dvrBudgetInForce(fromFile, map[string]bool{"dvr-max-bytes": true}, flags)
	if err != nil {
		t.Fatalf("an explicit zero was refused: %v", err)
	}
	if got.MaxBytes != 0 {
		t.Fatalf("an explicit -dvr-max-bytes 0 gave %d, want no limit", got.MaxBytes)
	}
	if got.RetentionSeconds != 300 || got.RecordingBytes != 1<<30 {
		t.Fatalf("one flag changed the other two limits: %+v", got)
	}
	if !strings.Contains(from, "-dvr-max-bytes") {
		t.Fatalf("the rule that won reads %q, want the flag named", from)
	}
}

// TestAnImpossibleFlagIsRefused is the other edge the flag path opened: a flag
// is as able to ask for a budget the buffer cannot run with as a file is, so it
// goes through the same validation and names the flag in the refusal.
func TestAnImpossibleFlagIsRefused(t *testing.T) {
	fromFile := defaultSettings()
	flags := dvrBudget{RetentionSeconds: 1, MaxBytes: 0, RecordingBytes: 256 << 20}
	if _, _, err := dvrBudgetInForce(fromFile, map[string]bool{"dvr-retention": true}, flags); err == nil {
		t.Fatal("a one second retention was accepted")
	} else {
		if !strings.Contains(err.Error(), "-dvr-retention") || !strings.Contains(err.Error(), "retentionSeconds") {
			t.Fatalf("the refusal %q does not name the flag and the field", err)
		}
	}

	oldRetention, oldMax, oldRecording := dvrRetention, dvrMaxBytes, dvrRecordingBytes
	t.Cleanup(func() { dvrRetention, dvrMaxBytes, dvrRecordingBytes = oldRetention, oldMax, oldRecording })
	a := &appState{budgetFlags: map[string]bool{"dvr-retention": true}, flagBudget: flags}
	a.settings = fromFile
	if err := a.resolveBudget(); err == nil {
		t.Fatal("resolveBudget accepted a budget that cannot run")
	}
	if dvrRetention != oldRetention {
		t.Fatalf("a refused budget changed the running retention to %s", dvrRetention)
	}
}

// TestAFileWithoutABudgetGetsTheDefaults keeps the upgrade path honest: a file
// written before the budget existed must load as the built-in budget, not fail.
func TestAFileWithoutABudgetGetsTheDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := `{"startPage":"sources","defaultVolume":0.4,"nudgeStep":0.2,"muteUnaligned":true,"masterVolume":0.8,"dockOpen":false,"compact":true}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, note := loadSettings(path)
	if note != "" {
		t.Fatalf("an old file was refused: %s", note)
	}
	if got.DVR != builtinDVRBudget() {
		t.Fatalf("an old file got %+v, want the built-in budget %+v", got.DVR, builtinDVRBudget())
	}
	if got.StartPage != "sources" || got.Compact != true {
		t.Fatalf("the rest of the old file was lost: %+v", got)
	}
}

// TestDVRBudgetSurvivesTheFile round-trips all three numbers, because a budget
// that silently comes back as its default would be worse than no budget.
func TestDVRBudgetSurvivesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	want := defaultSettings()
	want.DVR = dvrBudget{RetentionSeconds: 600, MaxBytes: 3 << 30, RecordingBytes: 512 << 20}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, note := loadSettings(path)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if got.DVR != want.DVR {
		t.Fatalf("the budget came back as %+v, want %+v", got.DVR, want.DVR)
	}
}

// TestResolveBudgetReachesTheBuffer proves applying settings changes what the
// next buffer would get, through the same globals the scenarios and startDVR
// read.
func TestResolveBudgetReachesTheBuffer(t *testing.T) {
	oldRetention, oldMax, oldRecording := dvrRetention, dvrMaxBytes, dvrRecordingBytes
	t.Cleanup(func() { dvrRetention, dvrMaxBytes, dvrRecordingBytes = oldRetention, oldMax, oldRecording })

	a := &appState{budgetFlags: map[string]bool{}, flagBudget: dvrBudget{RetentionSeconds: 60, RecordingBytes: 256 << 20}}
	a.settings = defaultSettings()
	a.settings.DVR = dvrBudget{RetentionSeconds: 180, MaxBytes: 0, RecordingBytes: 2 << 30}
	if err := a.resolveBudget(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if a.budget.RetentionSeconds != 180 || a.budget.RecordingBytes != 2<<30 {
		t.Fatalf("the budget in force is %+v", a.budget)
	}
	if dvrRetention != 180*time.Second || dvrRecordingBytes != 2<<30 || dvrMaxBytes != 0 {
		t.Fatalf("the buffer would run with %s/%d/%d, want the settings file's values", dvrRetention, dvrMaxBytes, dvrRecordingBytes)
	}

	a.budgetFlags = map[string]bool{"dvr-recording-bytes": true}
	a.flagBudget = dvrBudget{RetentionSeconds: 60, RecordingBytes: 0}
	if err := a.resolveBudget(); err != nil {
		t.Fatalf("resolve with a zero flag: %v", err)
	}
	if dvrRecordingBytes != 0 || !strings.Contains(a.budgetFrom, "-dvr-recording-bytes") {
		t.Fatalf("an explicit zero did not win: %d from %q", dvrRecordingBytes, a.budgetFrom)
	}
}

// TestApplyResolvesTheBudgetEvenWhenTheSaveFails pins the order Apply uses: the
// budget is resolved against the draft, then applied, then written. A save that
// fails leaves the settings and the budget agreeing with each other instead of
// the page describing a budget the buffer never got.
func TestApplyResolvesTheBudgetEvenWhenTheSaveFails(t *testing.T) {
	oldRetention, oldMax, oldRecording := dvrRetention, dvrMaxBytes, dvrRecordingBytes
	t.Cleanup(func() { dvrRetention, dvrMaxBytes, dvrRecordingBytes = oldRetention, oldMax, oldRecording })

	session := core.NewSession("budget apply test")
	scene, err := session.AddScene("main")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "settings.json")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatalf("make the save fail: %v", err)
	}
	a := &appState{
		session:      session,
		scene:        scene.ID,
		settings:     defaultSettings(),
		settingsPath: blocked,
		budgetFlags:  map[string]bool{},
		flagBudget:   dvrBudget{RetentionSeconds: 60, RecordingBytes: 256 << 20},
	}
	a.settingsDraft = a.settings
	a.settingsDraft.DVR = dvrBudget{RetentionSeconds: 120, MaxBytes: 0, RecordingBytes: 64 << 20}

	a.applySettingsCommand()
	if !strings.Contains(a.settingsErr, "saving failed") {
		t.Fatalf("the failure reads %q, want it to name the save", a.settingsErr)
	}
	if a.settings.DVR.RecordingBytes != 64<<20 || a.budget.RecordingBytes != 64<<20 {
		t.Fatalf("the applied settings ask for %d but the budget in force is %d",
			a.settings.DVR.RecordingBytes, a.budget.RecordingBytes)
	}
	if dvrRecordingBytes != 64<<20 || dvrRetention != 120*time.Second {
		t.Fatalf("the buffer would run with %s/%d, want the applied budget", dvrRetention, dvrRecordingBytes)
	}
}
