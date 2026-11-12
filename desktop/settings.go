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

	// DVR is the cache budget a new replay buffer starts with (P18.2). A
	// command line flag given explicitly wins over this, so a check can ask for
	// a different budget without editing the file.
	DVR dvrBudget `json:"dvr"`
}

// dvrBudget is the replay buffer's share of the disk. Both byte limits use zero
// to mean "no limit of that kind": only the retention or the disk itself stops
// the buffer growing, which the page warns about rather than hiding.
type dvrBudget struct {
	RetentionSeconds int   `json:"retentionSeconds"`
	MaxBytes         int64 `json:"maxBytes"`
	RecordingBytes   int64 `json:"recordingBytes"`
}

// builtinDVRBudget is the third rule in "explicit flag > settings file > this".
func builtinDVRBudget() dvrBudget {
	return dvrBudget{RetentionSeconds: 60, MaxBytes: 0, RecordingBytes: 256 << 20}
}

// dvrBudgetLimits bound what a settings file may ask for. They are wide: the
// point is to refuse typos and absurd values, not to second-guess a person with
// a big disk.
const (
	minRetentionSeconds = 5
	maxRetentionSeconds = 3600
	maxBudgetBytes      = int64(1) << 40 // 1 TiB
)

// validateDVRBudget rejects a budget the buffer cannot run with.
func validateDVRBudget(b dvrBudget) error {
	if b.RetentionSeconds < minRetentionSeconds || b.RetentionSeconds > maxRetentionSeconds {
		return fmt.Errorf("dvr.retentionSeconds %d is not between %d and %d", b.RetentionSeconds, minRetentionSeconds, maxRetentionSeconds)
	}
	if b.MaxBytes < 0 || b.MaxBytes > maxBudgetBytes {
		return fmt.Errorf("dvr.maxBytes %d is not between 0 (no limit) and %d", b.MaxBytes, maxBudgetBytes)
	}
	if b.RecordingBytes < 0 || b.RecordingBytes > maxBudgetBytes {
		return fmt.Errorf("dvr.recordingBytes %d is not between 0 (no limit) and %d", b.RecordingBytes, maxBudgetBytes)
	}
	return nil
}

// dvrBudgetInForce resolves which budget a new buffer gets: a flag the person
// actually typed beats the file, and the file beats the built-in defaults. The
// second result names the rule that won, for the page and the log.
func dvrBudgetInForce(from settings, explicit map[string]bool, flags dvrBudget) (dvrBudget, string) {
	effective := from.DVR
	won := "the settings file"
	var named []string
	if explicit["dvr-retention"] {
		effective.RetentionSeconds = flags.RetentionSeconds
		named = append(named, "-dvr-retention")
	}
	if explicit["dvr-max-bytes"] {
		effective.MaxBytes = flags.MaxBytes
		named = append(named, "-dvr-max-bytes")
	}
	if explicit["dvr-recording-bytes"] {
		effective.RecordingBytes = flags.RecordingBytes
		named = append(named, "-dvr-recording-bytes")
	}
	if len(named) > 0 {
		won = "the command line (" + strings.Join(named, ", ") + ")"
	}
	return effective, won
}

// bytesLine spells a byte limit for the page and the log. Zero is not a number
// here: it is the absence of that limit.
func bytesLine(n int64) string {
	switch {
	case n == 0:
		return "no limit"
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// budgetLine spells all three limits, in the order the page shows them.
func (b dvrBudget) line() string {
	return fmt.Sprintf("keeps %ds, buffer %s, recordings %s", b.RetentionSeconds, bytesLine(b.MaxBytes), bytesLine(b.RecordingBytes))
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
		DVR:           builtinDVRBudget(),
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
	if err := validateDVRBudget(s.DVR); err != nil {
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
		ui.Text(c, "what a new replay buffer may use; the marker shows what a buffer opened now would get").Wrap()
		// The three numbers a buffer runs with. They are steppers rather than
		// sliders because a budget is a number a person reads back and a slider
		// cannot be read from a screenshot.
		a.settingsRow(c, "Keep this much history", func() {
			if ui.Button(c.Key("dvr-ret-less"), "-1 min").Width(80).Clicked() {
				a.settingsDraft.DVR.RetentionSeconds = max(minRetentionSeconds, a.settingsDraft.DVR.RetentionSeconds-60)
			}
			ui.Text(c, fmt.Sprintf("%d s", a.settingsDraft.DVR.RetentionSeconds)).Width(90)
			if ui.Button(c.Key("dvr-ret-more"), "+1 min").Width(80).Clicked() {
				a.settingsDraft.DVR.RetentionSeconds = min(maxRetentionSeconds, a.settingsDraft.DVR.RetentionSeconds+60)
			}
		})
		a.settingsRow(c, "Segment cache limit", func() {
			if ui.Button(c.Key("dvr-max-less"), "-64 MiB").Width(90).Clicked() {
				a.settingsDraft.DVR.MaxBytes = max(0, a.settingsDraft.DVR.MaxBytes-64<<20)
			}
			ui.Text(c, bytesLine(a.settingsDraft.DVR.MaxBytes)).Width(120)
			if ui.Button(c.Key("dvr-max-more"), "+64 MiB").Width(90).Clicked() {
				a.settingsDraft.DVR.MaxBytes = min(maxBudgetBytes, a.settingsDraft.DVR.MaxBytes+64<<20)
			}
		})
		a.settingsRow(c, "Recording limit", func() {
			if ui.Button(c.Key("dvr-rec-less"), "-64 MiB").Width(90).Clicked() {
				a.settingsDraft.DVR.RecordingBytes = max(0, a.settingsDraft.DVR.RecordingBytes-64<<20)
			}
			ui.Text(c, bytesLine(a.settingsDraft.DVR.RecordingBytes)).Width(120)
			if ui.Button(c.Key("dvr-rec-more"), "+64 MiB").Width(90).Clicked() {
				a.settingsDraft.DVR.RecordingBytes = min(maxBudgetBytes, a.settingsDraft.DVR.RecordingBytes+64<<20)
			}
		})
		a.settingsHint(c, "in force for a buffer opened now: "+a.budget.line()+" from "+a.budgetFrom)
		if a.settingsDraft.DVR.MaxBytes == 0 || a.settingsDraft.DVR.RecordingBytes == 0 {
			ui.Text(c, "a zero byte limit is not a small budget: it removes that limit, so only the retention and the disk stop the buffer").
				FontSize(textMicro).TextColor(colorWarn).Wrap()
		}
		a.settingsHint(c, "command line flags given explicitly win over these numbers; the line above says which rule is in force")
	case "Appearance":
		ui.Checkbox(c, &a.settingsDraft.DockOpen, "Open the timeline dock by default")
		ui.Checkbox(c, &a.settingsDraft.Compact, "Tighter spacing")
		a.settingsHint(c, "the spacing switch is read on the next start; the dock switch applies as soon as it is applied")
	case "Shortcuts":
		a.settingsSlider(c, "Nudge step", &a.settingsDraft.NudgeStep, 0.02, 1)
		// P18.1: the list is what the window actually binds, read-only. The
		// transport keys are still unbound, and the list says so instead of
		// offering a switch that would not do anything.
		a.shortcutLine(c, "Ctrl+S", "show the selected source")
		a.shortcutLine(c, "Ctrl+H", "hear the selected source")
		a.shortcutLine(c, "Ctrl+R", "retry the selected source's player")
		a.shortcutLine(c, "Ctrl+1 … Ctrl+5", "stage, sources, diagnostics, settings, mixer")
		a.settingsHint(c, "these keys pause while a text field has the focus, so typing never shows or moves anything")
		ui.Button(c.Key("shortcut-space"), "Space: play or pause").Width(300).Disabled(true).
			Tooltip("not bound: the transport buttons are the only play control")
		ui.Button(c.Key("shortcut-nudge"), "Left/Right: nudge the selected source").Width(300).Disabled(true).
			Tooltip("not bound: the inspector's -/+ buttons move a source")
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

// shortcutLine names one window shortcut and what it does. The list is read
// from the bindings in main.go's view; a key that is not bound is not listed.
func (a *appState) shortcutLine(c *ui.Context, keys, what string) {
	ui.Row(c).Gap(12).WidthPercent(100).Children(func() {
		ui.Text(c, keys).Width(150).FontSize(textMicro).TextColor(colorText)
		ui.Text(c, what).FontSize(textMicro).TextColor(colorDim)
	})
}

// resolveBudget works out the budget a buffer opened now would get and pushes it
// into the flags the buffer is built from, so the page, the log and the buffer
// read one value instead of three that can disagree.
func (a *appState) resolveBudget() {
	a.budget, a.budgetFrom = dvrBudgetInForce(a.settings, a.budgetFlags, a.flagBudget)
	dvrRetention = time.Duration(a.budget.RetentionSeconds) * time.Second
	dvrMaxBytes = a.budget.MaxBytes
	dvrRecordingBytes = a.budget.RecordingBytes
	log.Printf("dvr: budget %s from %s", a.budget.line(), a.budgetFrom)
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
	// A new budget has to reach the buffer too: applying it changes what the
	// next buffer opened would get, not the one already running.
	a.resolveBudget()
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
