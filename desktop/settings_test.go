package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// TestDefaultSettingsValidate keeps the first-run values inside the rules the
// page enforces; a default that its own validator rejects would make Apply
// fail on a fresh profile.
func TestDefaultSettingsValidate(t *testing.T) {
	if err := validateSettings(defaultSettings()); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

// TestValidateSettingsRejects keeps each rule pinned to the message a person
// would see, so a value that cannot be honoured never reaches a player.
func TestValidateSettingsRejects(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*settings)
		wants string
	}{
		{"page", func(s *settings) { s.StartPage = "mixer" }, `startPage "mixer"`},
		{"default volume high", func(s *settings) { s.DefaultVolume = 1.5 }, "defaultVolume"},
		{"default volume low", func(s *settings) { s.DefaultVolume = -0.1 }, "defaultVolume"},
		{"nudge step low", func(s *settings) { s.NudgeStep = 0.01 }, "nudgeStep"},
		{"nudge step high", func(s *settings) { s.NudgeStep = 2 }, "nudgeStep"},
		{"master volume", func(s *settings) { s.MasterVolume = 2 }, "masterVolume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := defaultSettings()
			tc.edit(&s)
			err := validateSettings(s)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error %q does not mention %q", err, tc.wants)
			}
		})
	}
}

// TestSaveLoadSettingsRoundTrip proves the file that Apply writes is the file
// the next start reads, with owner-only permissions and no leftover temp file.
func TestSaveLoadSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "settings.json")
	want := settings{
		StartPage:     "sources",
		DefaultVolume: 0.3,
		NudgeStep:     0.5,
		MuteUnaligned: false,
		MasterVolume:  0.4,
		DockOpen:      false,
		Compact:       true,
		DVR:           dvrBudget{RetentionSeconds: 120, MaxBytes: 4 << 20, RecordingBytes: 64 << 20},
	}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Windows has no POSIX permission bits; os.Chmod there only toggles the
	// read-only flag, so the owner-only check is a POSIX assertion.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("permissions are %o, want 600", perm)
		}
	}
	got, note := loadSettings(path)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if got != want {
		t.Fatalf("loaded %+v, want %+v", got, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d files left in the directory, want just settings.json", len(entries))
	}
}

// TestLoadSettingsFirstRun treats a missing file as a first run: defaults, no
// complaint, nothing created.
func TestLoadSettingsFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	got, note := loadSettings(path)
	if note != "" {
		t.Fatalf("first run reported %q", note)
	}
	if got != defaultSettings() {
		t.Fatalf("first run loaded %+v, want the defaults", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the first run created %s", path)
	}
}

// TestLoadSettingsRejectsBrokenFile proves a file is all or nothing: an
// unknown key, a broken value or an out-of-range value falls back to the
// defaults as a whole and says why, and the file is left for inspection.
func TestLoadSettingsRejectsBrokenFile(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown key", `{"startPage":"stage","nudgeStep":0.1,"defaultVolume":1,"masterVolume":1,"muteUnaligned":true,"dockOpen":true,"compact":false,"wat":1}`, "unknown field"},
		{"broken json", `{"startPage":`, "not a settings file"},
		{"out of range", `{"startPage":"stage","defaultVolume":9,"nudgeStep":0.1,"masterVolume":1,"muteUnaligned":true,"dockOpen":true,"compact":false}`, "does not validate"},
		{"bad page", `{"startPage":"dvr","defaultVolume":1,"nudgeStep":0.1,"masterVolume":1,"muteUnaligned":true,"dockOpen":true,"compact":false}`, "does not validate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, note := loadSettings(path)
			if !strings.Contains(note, tc.want) {
				t.Fatalf("note %q does not mention %q", note, tc.want)
			}
			if got != defaultSettings() {
				t.Fatalf("loaded %+v, want the defaults", got)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != tc.body {
				t.Fatalf("the bad file was changed or removed: %q, %v", data, err)
			}
		})
	}
}

// TestApplySettingsPushesVolumes proves Apply is what moves a value into the
// players: the master multiplies the scene level, and invalid input cannot be
// applied.
func TestApplySettingsPushesVolumes(t *testing.T) {
	newState := func(t *testing.T) *appState {
		t.Helper()
		session := core.NewSession("settings test")
		scene, err := session.AddScene("main")
		if err != nil {
			t.Fatalf("scene: %v", err)
		}
		return &appState{session: session, scene: scene.ID, settings: defaultSettings()}
	}
	a := newState(t)
	if got := a.volumeFor(0.5); got != 0.5 {
		t.Fatalf("default volumeFor(0.5) = %v, want 0.5", got)
	}
	next := defaultSettings()
	next.MasterVolume = 0.5
	if err := a.applySettingsState(next); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := a.volumeFor(0.5); got != 0.25 {
		t.Fatalf("volumeFor(0.5) with a half master = %v, want 0.25", got)
	}
	bad := defaultSettings()
	bad.MasterVolume = 3
	if err := a.applySettingsState(bad); err == nil {
		t.Fatal("an out-of-range master volume was applied")
	}
	if a.settings.MasterVolume != 0.5 {
		t.Fatalf("a refused apply changed the live settings: %+v", a.settings)
	}
}

// TestSettingsNudgeUsesStep keeps the inspector's buttons tied to the setting
// instead of a hard-coded hundred milliseconds.
func TestSettingsNudgeUsesStep(t *testing.T) {
	a := &appState{settings: defaultSettings()}
	if got := a.settingsNudge(-1); got != -100*time.Millisecond {
		t.Fatalf("default nudge = %v, want -100ms", got)
	}
	a.settings.NudgeStep = 0.5
	if got := a.settingsNudge(1); got != 500*time.Millisecond {
		t.Fatalf("nudge with a half-second step = %v, want 500ms", got)
	}
}
