package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// settings is what the Global Settings page edits. Every field is applied to
// something this build actually does; the page still shows the sections that
// have nothing behind them yet, disabled, rather than hiding them.
type settings struct {
	// StartPage is the page the window opens on: "stage" or "sources".
	StartPage string `json:"startPage"`

	// DefaultVolume is the level a newly imported source's audio track gets.
	DefaultVolume float64 `json:"defaultVolume"`

	// NudgeStep is how far one nudge button moves a source, in seconds.
	NudgeStep float64 `json:"nudgeStep"`

	// MuteUnaligned keeps the sound of a source whose place on the shared
	// timeline is unknown off until a person allows it. Turning it off is a
	// deliberate choice, not a default.
	MuteUnaligned bool `json:"muteUnaligned"`

	// MasterVolume multiplies every source's scene level at the player.
	MasterVolume float64 `json:"masterVolume"`

	// DockOpen makes the timeline dock open by default while the window is
	// wide enough for it.
	DockOpen bool `json:"dockOpen"`

	// Compact tightens the spacing of the workbench.
	Compact bool `json:"compact"`

	// Workspace is the remembered shape of the window: its frame, the page it
	// was on and the panes a person left open or closed (P18.1).
	Workspace workspaceMemory `json:"workspace"`
}

// defaultSettings is both the first-run state and what Reset restores.
func defaultSettings() settings {
	return settings{
		StartPage:     "stage",
		DefaultVolume: 1,
		NudgeStep:     0.1,
		MuteUnaligned: true,
		MasterVolume:  1,
		DockOpen:      true,
		Compact:       false,
	}
}

// validateSettings rejects values the rest of the code cannot honour. A file
// that fails this check is not used at all: half-applied settings would be
// worse than the defaults, which are always valid.
func validateSettings(s settings) error {
	switch s.StartPage {
	case "stage", "sources":
	default:
		return fmt.Errorf("startPage %q is not a page, want stage or sources", s.StartPage)
	}
	if s.DefaultVolume < 0 || s.DefaultVolume > 1 {
		return fmt.Errorf("defaultVolume %.3f is not a level between 0 and 1", s.DefaultVolume)
	}
	if s.NudgeStep < 0.02 || s.NudgeStep > 1 {
		return fmt.Errorf("nudgeStep %.3fs is not between 0.02s and 1s", s.NudgeStep)
	}
	if s.MasterVolume < 0 || s.MasterVolume > 1 {
		return fmt.Errorf("masterVolume %.3f is not a level between 0 and 1", s.MasterVolume)
	}
	if err := validateWorkspaceMemory(s.Workspace); err != nil {
		return err
	}
	return nil
}

// oneLine flattens a file for a log line, so a multi-line JSON file does not
// break the one-line-per-event log.
func oneLine(data []byte) string {
	return strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", " ")
}

// settingsPath is where applied settings live: an override for scenarios and
// tests, otherwise the per-user configuration directory.
func settingsPath(override string) string {
	if override != "" {
		return override
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "MuxShift", "settings.json")
}

// loadSettings reads a settings file. A missing file is the first run and is
// not an error; a file that cannot be read or does not validate is reported
// and replaced by the defaults in memory, leaving the bad file untouched so it
// can be inspected.
func loadSettings(path string) (settings, string) {
	st := defaultSettings()
	if path == "" {
		return st, "no configuration directory; settings are not saved"
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, ""
	}
	if err != nil {
		return st, fmt.Sprintf("could not read %s (%v); using the defaults", path, err)
	}
	loaded := defaultSettings()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&loaded); err != nil {
		return st, fmt.Sprintf("%s is not a settings file (%v); using the defaults", path, err)
	}
	if err := validateSettings(loaded); err != nil {
		return st, fmt.Sprintf("%s does not validate (%v); using the defaults", path, err)
	}
	return loaded, ""
}

// saveSettings writes the settings atomically with owner-only permissions: a
// crash mid-write must not leave a half file that the next start reads.
func saveSettings(path string, st settings) error {
	if err := validateSettings(st); err != nil {
		return err
	}
	if path == "" {
		return errors.New("there is no configuration directory to save into")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// applySettingsState copies the draft into the live settings and pushes the
// live values into the players and the window. It is the only path from the
// page to the running session, so every control has one place to look.
func (a *appState) applySettingsState(next settings) error {
	if err := validateSettings(next); err != nil {
		return err
	}
	a.settings = next
	a.settingsDraft = next
	a.apply()
	return nil
}

// volumeFor is the level a player gets for a scene level: the scene's own
// volume times the master volume from the settings.
func (a *appState) volumeFor(level float64) float64 {
	v := level * a.settings.MasterVolume
	return min(max(v, 0), 1)
}

// settingsSections names the page's sections in the order the page shows them.
var settingsSections = []string{"General", "Playback", "Audio", "DVR", "Appearance", "Shortcuts", "Diagnostics"}

// settingsPage draws the Global Settings page. The page edits a draft; nothing
// reaches the session until Apply, and Cancel puts the draft back.
func (a *appState) settingsPage(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Start).Gap(spaceLg).Padding(spaceMd).Children(func() {
		ui.Column(c).Width(190).Gap(spaceXs).Padding(spaceSm).Children(func() {
			ui.Text(c, "settings").FontSize(textMicro).TextColor(colorDim)
			for _, section := range settingsSections {
				name := section
				button := ui.Button(c.Key("settings-tab-"+name), name).Width(170).Height(32)
				if name == a.settingsSection {
					button = button.Background(colorAccentSoft).Border(1, colorAccent)
				}
				if button.Clicked() {
					a.settingsSection = name
					a.settingsErr = ""
					a.settingsNote = ""
				}
			}
			ui.Box(c).Grow(1)
			ui.Text(c, a.settingsBoundary()).FontSize(textMicro).TextColor(colorDim).Wrap()
		})
		ui.Column(c).Grow(1).Gap(spaceSm).Padding(spaceMd).Children(func() {
			ui.Text(c, a.settingsSection).FontSize(textTitle)
			a.settingsForm(c)
			if a.settingsErr != "" {
				ui.Text(c, a.settingsErr).TextColor(colorBad).Wrap()
			}
			if a.settingsNote != "" {
				ui.Text(c, a.settingsNote).TextColor(colorTextDim).Wrap()
			}
			dirty := a.settingsDraft != a.settings
			ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).Children(func() {
				if ui.Button(c.Key("settings-apply"), "Apply").Width(100).Disabled(!dirty).Clicked() {
					a.applySettingsCommand()
				}
				if ui.Button(c.Key("settings-cancel"), "Cancel").Width(100).Disabled(!dirty).Clicked() {
					a.settingsDraft = a.settings
					a.settingsErr = ""
					a.settingsNote = "cancelled: the draft is back to what is applied"
				}
				if ui.Button(c.Key("settings-reset"), "Reset to defaults").Width(160).Clicked() {
					a.settingsDraft = defaultSettings()
					a.settingsErr = ""
					a.settingsNote = "the draft is back to the defaults; Apply writes them"
				}
			})
		})
	})
}

// settingsBoundary says, in the page itself, what is saved and what is not.
func (a *appState) settingsBoundary() string {
	switch {
	case a.settingsPath == "":
		return "nothing is saved: this start has no configuration directory"
	case a.settingsSaved:
		return "applied settings are saved to " + a.settingsPath
	default:
		return "applied settings are saved to " + a.settingsPath + " (this start has not written it yet)"
	}
}

// settingsForm is the right-hand part of one section.
func (a *appState) settingsForm(c *ui.Context) {
	switch a.settingsSection {
	case "General":
		a.settingsRow(c, "Open on", func() { ui.Select(c, &a.settingsDraft.StartPage, []string{"stage", "sources"}).Width(220) })
		a.settingsHint(c, "the start page is read when the window opens, so it takes effect on the next start")
		ui.Checkbox(c, &a.settingsDraft.Workspace.On, "Come back to the window I left: its frame, page and panes")
		a.settingsHint(c, "written when the window closes; a frame no display can show falls back to the primary display, and a page or pane name this build does not know is refused")
	case "Playback":
		a.settingsSlider(c, "Default volume for new sources", &a.settingsDraft.DefaultVolume, 0, 1)
		ui.Checkbox(c, &a.settingsDraft.MuteUnaligned, "Mute a source until its place on the shared timeline is known")
		a.settingsHint(c, "with this off, an unaligned source can be heard before anyone allows it; the per-source Allow button still overrides the mute")
	case "Audio":
		a.settingsSlider(c, "Master volume", &a.settingsDraft.MasterVolume, 0, 1)
		a.settingsHint(c, "the master multiplies every source's scene level at its player; per-source levels stay in the inspector")
	case "DVR":
		ui.Text(c, "the replay buffer keeps segments and knows its gaps, but seeking it inside this window is not implemented").Wrap()
		held := a.settingsDraft.DockOpen
		ui.Checkbox(c, &held, "Keep the replay buffer open by default").Disabled(true).
			Tooltip("recording controls arrive with the DVR phase; this switch does nothing yet")
		a.settingsHint(c, "disabled until the DVR phase lands")
	case "Appearance":
		ui.Checkbox(c, &a.settingsDraft.DockOpen, "Open the timeline dock by default")
		ui.Checkbox(c, &a.settingsDraft.Compact, "Tighter spacing")
		a.settingsHint(c, "the spacing switch is read on the next start; the dock switch applies as soon as it is applied")
	case "Shortcuts":
		a.settingsSlider(c, "Nudge step", &a.settingsDraft.NudgeStep, 0.02, 1)
		ui.Button(c.Key("shortcut-space"), "Space: play or pause").Width(300).Disabled(true).
			Tooltip("keyboard shortcuts are not bound yet; the transport buttons are the only play control")
		ui.Button(c.Key("shortcut-nudge"), "Left/Right: nudge the selected source").Width(300).Disabled(true).
			Tooltip("keyboard shortcuts are not bound yet")
		a.settingsHint(c, "the nudge step above is real: it is the distance the inspector's -/+ buttons move a source")
	case "Diagnostics":
		ui.Text(c, fmt.Sprintf("%d event(s) in the session, %d source(s)", a.eventCount(), len(a.items)))
		ui.Text(c, "settings file: "+a.settingsPath).Wrap()
		if ui.Button(c.Key("diag-log-state"), "Write the current state to the log").Width(260).Clicked() {
			a.logState()
		}
		ui.Button(c.Key("diag-capture"), "Capture a page on the next failure").Width(260).Disabled(true).
			Tooltip("there is no page capture on failure yet")
	}
}

// settingsRow puts a label and a control on one line.
func (a *appState) settingsRow(c *ui.Context, label string, control func()) {
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(260)
		control()
	})
}

// settingsSlider is a slider with the value spelled out beside it, because a
// slider alone cannot be read back from a screenshot.
func (a *appState) settingsSlider(c *ui.Context, label string, value *float64, lo, hi float64) {
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label)
		ui.Box(c).Grow(1)
		ui.Text(c, fmt.Sprintf("%.2f", *value)).Width(50)
	})
	ui.Slider(c, value, lo, hi).WidthPercent(100)
}

// settingsHint is the small line under a control that explains what it does.
func (a *appState) settingsHint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(textMicro).TextColor(colorDim).Wrap()
}

// applySettingsCommand is the Apply button: validate, apply, write, report.
func (a *appState) applySettingsCommand() {
	next := a.settingsDraft
	// Turning the memory on from the page means "this window, as it is now":
	// the frame the page cannot see is filled in here, before validation gets a
	// chance to refuse a zero size.
	if next.Workspace.On && (next.Workspace.Width == 0 || next.Workspace.Height == 0) {
		next.Workspace = a.workspaceFromWindow(a.window, true)
	}
	if err := validateSettings(next); err != nil {
		a.settingsErr = err.Error()
		return
	}
	a.settingsErr = ""
	if err := a.applySettingsState(next); err != nil {
		a.settingsErr = err.Error()
		return
	}
	if err := saveSettings(a.settingsPath, next); err != nil {
		a.settingsErr = fmt.Sprintf("applied, but saving failed: %v", err)
		return
	}
	a.settingsSaved = true
	a.settingsNote = fmt.Sprintf("applied %s", a.settingsPath)
	log.Printf("settings: applied %+v", next)
}

// eventCount is how many events the session has, for the Diagnostics section.
func (a *appState) eventCount() int {
	if a.session == nil {
		return 0
	}
	return len(a.session.Events())
}

// logState writes the read-only state the Diagnostics section shows, so a
// screenshot can point at a log line with the same numbers.
func (a *appState) logState() {
	snap := a.snapshot()
	log.Printf("state: scene=%s event=%s playing=%v picture=%v sound=%d sources=%d", snap.Scene, formatEvent(snap.Event), snap.Playing, snap.Picture, len(snap.Sound), len(snap.Sources))
	log.Printf("settings: %+v", a.settings)
}

// space picks the tight spacing when the settings ask for it; the two
// numbers are the compact and the comfortable value.
func (a *appState) space(tight, loose float32) float32 {
	if a.settings.Compact {
		return tight
	}
	return loose
}

// settingsSourceVolume is the level a newly added source starts at.
func (a *appState) settingsSourceVolume() float64 {
	return min(max(a.settings.DefaultVolume, 0), 1)
}

// settingsNudge is the delta one nudge button applies: the settings step in
// the direction the button asks for.
func (a *appState) settingsNudge(sign time.Duration) time.Duration {
	step := a.settings.NudgeStep
	return sign * time.Duration(step*float64(time.Second))
}
