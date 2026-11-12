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

	// SourceMemory is the per-source preference (level, mute) keyed by
	// normalised address (P18.2-b). It is off by default and never opens a
	// source: see sourcememory.go for what it does and does not remember.
	SourceMemory sourceMemory `json:"sourceMemory"`
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
//
// The winner is validated here too, because a flag is as capable of asking for
// a budget the buffer cannot run with as a file is: -dvr-retention 1s must be
// refused rather than turned into a buffer that keeps nothing.
func dvrBudgetInForce(from settings, explicit map[string]bool, flags dvrBudget) (dvrBudget, string, error) {
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
	if err := validateDVRBudget(effective); err != nil {
		return dvrBudget{}, won, fmt.Errorf("the budget from %s cannot run: %w", won, err)
	}
	return effective, won, nil
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
		// Source memory starts off in both senses: nothing is applied and
		// nothing new is recorded until a person asks for it.
		SourceMemory: sourceMemory{On: false, Remember: false},
	}
}

// cloneSettings copies a settings value deeply enough that a draft and the
// applied settings never share an array. The source memory's entries are the
// only part that could.
func cloneSettings(st settings) settings {
	next := st
	next.SourceMemory = st.SourceMemory.clone()
	return next
}

// sameSettings reports whether two settings values describe the same choices.
// Slices cannot be compared with ==, which is why the page cannot use one, and
// the entries are also compared by content rather than by address.
func sameSettings(a, b settings) bool {
	if a.StartPage != b.StartPage || a.DefaultVolume != b.DefaultVolume || a.NudgeStep != b.NudgeStep ||
		a.MuteUnaligned != b.MuteUnaligned || a.MasterVolume != b.MasterVolume ||
		a.DockOpen != b.DockOpen || a.Compact != b.Compact ||
		a.Workspace != b.Workspace || a.DVR != b.DVR {
		return false
	}
	if a.SourceMemory.On != b.SourceMemory.On || a.SourceMemory.Remember != b.SourceMemory.Remember {
		return false
	}
	if len(a.SourceMemory.Entries) != len(b.SourceMemory.Entries) {
		return false
	}
	for i := range a.SourceMemory.Entries {
		if a.SourceMemory.Entries[i] != b.SourceMemory.Entries[i] {
			return false
		}
	}
	return true
}

// settingsSummary describes a settings value for the log without printing the
// remembered addresses: a log line is copied into evidence, bug reports and
// terminal scrollback, and an address can carry a private path or a host name.
func settingsSummary(st settings) string {
	return fmt.Sprintf("start page %q, nudge %.2fs, default volume %.2f, master %.2f, mute unaligned %v, dock %v, compact %v, dvr %s, source memory %d entr(ies) on=%v remember=%v set=%s",
		st.StartPage, st.NudgeStep, st.DefaultVolume, st.MasterVolume, st.MuteUnaligned,
		st.DockOpen, st.Compact, st.DVR.line(), len(st.SourceMemory.Entries),
		st.SourceMemory.On, st.SourceMemory.Remember, memoryDigest(st.SourceMemory))
}

// validateSourceMemory refuses a memory the rest of the code cannot honour. It
// checks every entry rather than skipping a bad one: a half-remembered set would
// silently apply one source's level to another address.
func validateSourceMemory(m sourceMemory) error {
	if len(m.Entries) > maxRememberedSources {
		return fmt.Errorf("sourceMemory.entries has %d addresses, more than the %d the file allows", len(m.Entries), maxRememberedSources)
	}
	seen := make(map[string]bool, len(m.Entries))
	for i, entry := range m.Entries {
		if entry.Volume < 0 || entry.Volume > 1 {
			return fmt.Errorf("sourceMemory.entries[%d].volume %.3f is not a level between 0 and 1", i, entry.Volume)
		}
		identity, err := normalizeSourceIdentity(entry.Address)
		if err != nil {
			return fmt.Errorf("sourceMemory.entries[%d]: %w", i, err)
		}
		if identity != entry.Address {
			return fmt.Errorf("sourceMemory.entries[%d] is not stored in its normalised form", i)
		}
		if seen[identity] {
			return fmt.Errorf("sourceMemory.entries[%d] repeats an address already in the list", i)
		}
		seen[identity] = true
	}
	return nil
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
	if err := validateSourceMemory(s.SourceMemory); err != nil {
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

// saveSettingsSerialized writes the settings file under the app's save lock. A
// remembered preference saves from a background goroutine while Apply saves
// from the drawing thread; without the lock the two could rename in the other
// order and leave the older file behind.
func (a *appState) saveSettingsSerialized(st settings) error {
	a.settingsSave.Lock()
	defer a.settingsSave.Unlock()
	return saveSettings(a.settingsPath, st)
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
	// The rename replaces the file in one step, which is what makes a crash
	// harmless. On Windows it fails while a reader still holds the target open,
	// so a check that reads the file to watch for a write can make the write
	// itself fail; a few short retries cover that without weakening the atomic
	// replace.
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		if last = os.Rename(name, path); last == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

// applySettingsState copies the draft into the live settings and pushes the
// live values into the players and the window. It is the only path from the
// page to the running session, so every control has one place to look.
func (a *appState) applySettingsState(next settings) error {
	if err := validateSettings(next); err != nil {
		return err
	}
	// Both sides get their own copy of the entries: the draft keeps being edited
	// after Apply, and a shared array would let the next draft edit change what
	// the applied settings say.
	a.settings = cloneSettings(next)
	a.settingsDraft = cloneSettings(next)
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
var settingsSections = []string{"General", "Playback", "Sources", "Audio", "DVR", "Appearance", "Shortcuts", "Diagnostics"}

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
			dirty := !sameSettings(a.settingsDraft, a.settings)
			ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).Children(func() {
				if ui.Button(c.Key("settings-apply"), "Apply").Width(100).Disabled(!dirty).Clicked() {
					a.applySettingsCommand()
				}
				if ui.Button(c.Key("settings-cancel"), "Cancel").Width(100).Disabled(!dirty).Clicked() {
					a.settingsDraft = cloneSettings(a.settings)
					a.settingsErr = ""
					a.settingsNote = "cancelled: the draft is back to what is applied"
				}
				if ui.Button(c.Key("settings-reset"), "Reset to defaults").Width(160).Clicked() {
					a.settingsDraft = cloneSettings(defaultSettings())
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
	case a.settingsSaved.Load():
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
	case "Sources":
		ui.Checkbox(c, &a.settingsDraft.SourceMemory.On, "Use the level and mute I set for a source when its address comes back")
		ui.Checkbox(c, &a.settingsDraft.SourceMemory.Remember, "Remember the level and mute of sources I add from now on").Disabled(!a.settingsDraft.SourceMemory.On).
			Tooltip("memory is off, so nothing new is remembered")
		a.settingsHint(c, "remembered: the level and the mute of an http(s) address or an absolute file path. Not remembered: where playback stopped, timeline offsets, whether an unaligned source was allowed, or the session itself. It is a preference, not an authorisation: a remembered address is not opened again, and a remembered level does not allow an unaligned source to be heard.")
		if !a.settingsDraft.SourceMemory.On && a.settingsDraft.SourceMemory.Remember {
			ui.Text(c, "Remember is on while memory is off, so nothing is written; Apply keeps both switches as they stand.").
				FontSize(textMicro).TextColor(colorWarn).Wrap()
		}
		// The draft's switches only take effect on Apply, so say so rather than
		// letting somebody believe they just stopped the writing.
		if a.settingsDraft.SourceMemory.On != a.settings.SourceMemory.On || a.settingsDraft.SourceMemory.Remember != a.settings.SourceMemory.Remember {
			ui.Text(c, "this is a draft: the switches above take effect when Apply is pressed").
				FontSize(textMicro).TextColor(colorWarn).Wrap()
		}
		entries := a.settingsDraft.SourceMemory.Entries
		if len(entries) == 0 {
			ui.Text(c, "no address is remembered").FontSize(textMicro).TextColor(colorDim)
		} else {
			ui.Text(c, fmt.Sprintf("%d remembered address(es), most recently saved first", len(entries))).
				FontSize(textMicro).TextColor(colorDim)
			for i, entry := range entries {
				if i >= 4 {
					ui.Text(c, fmt.Sprintf("… and %d more", len(entries)-4)).FontSize(textMicro).TextColor(colorDim)
					break
				}
				// The page never prints a remembered address: a digest names it
				// well enough to tell two entries apart in a screenshot.
				muted := ""
				if entry.Muted {
					muted = ", muted"
				}
				ui.Text(c, fmt.Sprintf("%s  %.0f%%%s", identityDigest(entry.Address), entry.Volume*100, muted)).
					FontSize(textMicro).TextColor(colorTextDim)
			}
			if !a.settingsDraft.SourceMemory.On {
				ui.Text(c, fmt.Sprintf("%d remembered sources are currently inactive; Clear them to delete their saved preferences.", len(entries))).
					FontSize(textMicro).TextColor(colorWarn).Wrap()
			}
			if ui.Button(c.Key("settings-memory-clear"), "Forget every remembered address").Width(280).Clicked() {
				forgotten := a.settingsDraft.SourceMemory.clear()
				a.settingsNote = fmt.Sprintf("forgot %d remembered source(s); Apply writes the empty list", forgotten)
			}
		}
	case "Audio":
		a.settingsSlider(c, "Master volume", &a.settingsDraft.MasterVolume, 0, 1)
		a.settingsHint(c, "the master multiplies every source's scene level at its player; per-source levels stay in the inspector")
	case "DVR":
		ui.Text(c, "what a new replay buffer may use; the marker shows what a buffer opened now would get").Wrap()
		// The three numbers a buffer runs with. They are steppers rather than
		// sliders because a budget is a number a person reads back and a slider
		// cannot be read from a screenshot.
		a.dvrStepper(c, "Keep this much history", "dvr-ret-less", "-1 min", "dvr-ret-more", "+1 min",
			fmt.Sprintf("%d s", a.settingsDraft.DVR.RetentionSeconds), func(delta int) {
				next := a.settingsDraft.DVR.RetentionSeconds + 60*delta
				a.settingsDraft.DVR.RetentionSeconds = min(maxRetentionSeconds, max(minRetentionSeconds, next))
			})
		a.dvrStepper(c, "Segment cache limit", "dvr-max-less", "-64 MiB", "dvr-max-more", "+64 MiB",
			bytesLine(a.settingsDraft.DVR.MaxBytes), func(delta int) {
				next := a.settingsDraft.DVR.MaxBytes + int64(delta)*64<<20
				a.settingsDraft.DVR.MaxBytes = min(maxBudgetBytes, max(0, next))
			})
		a.dvrStepper(c, "Recording limit", "dvr-rec-less", "-64 MiB", "dvr-rec-more", "+64 MiB",
			bytesLine(a.settingsDraft.DVR.RecordingBytes), func(delta int) {
				next := a.settingsDraft.DVR.RecordingBytes + int64(delta)*64<<20
				a.settingsDraft.DVR.RecordingBytes = min(maxBudgetBytes, max(0, next))
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

// settingsRow puts a label and a control on one line. Its 260px label is fine
// on a wide page and too wide beside a folded rail, so the DVR numbers use
// dvrStepper instead.
func (a *appState) settingsRow(c *ui.Context, label string, control func()) {
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(260)
		control()
	})
}

// dvrStepper is one budget number: its name on a line of its own, then the two
// buttons and the value. The name is not on the same row as the buttons because
// the settings page still has to fit beside the folded rail at the smallest
// window, and a 260px label plus three controls does not.
func (a *appState) dvrStepper(c *ui.Context, name, lessKey, lessLabel, moreKey, moreLabel, value string, adjust func(delta int)) {
	ui.Column(c).Gap(spaceXs).WidthPercent(100).Children(func() {
		ui.Text(c, name).FontSize(textMicro).TextColor(colorDim)
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key(lessKey), lessLabel).Width(84).Clicked() {
				adjust(-1)
			}
			ui.Text(c, value).Width(96)
			if ui.Button(c.Key(moreKey), moreLabel).Width(84).Clicked() {
				adjust(1)
			}
		})
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

// resolveBudgetFor works out the budget that would be in force for these
// settings and pushes it into the flags the buffer is built from, so the page,
// the log and the buffer read one value instead of three that can disagree. It
// leaves the running values alone when the answer cannot run: a budget that
// failed validation must not half-apply.
func (a *appState) resolveBudgetFor(st settings) error {
	effective, from, err := dvrBudgetInForce(st, a.budgetFlags, a.flagBudget)
	if err != nil {
		return err
	}
	a.budget = effective
	a.budgetFrom = from
	dvrRetention = time.Duration(a.budget.RetentionSeconds) * time.Second
	dvrMaxBytes = a.budget.MaxBytes
	dvrRecordingBytes = a.budget.RecordingBytes
	log.Printf("dvr: budget %s from %s", a.budget.line(), a.budgetFrom)
	return nil
}

// resolveBudget re-resolves the budget for the settings already applied.
func (a *appState) resolveBudget() error { return a.resolveBudgetFor(a.settings) }

// applySettingsCommand is the Apply button: validate, apply, write, report.
func (a *appState) applySettingsCommand() {
	next := cloneSettings(a.settingsDraft)
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
	// The budget is resolved against the draft before anything is applied: a
	// flag the buffer cannot run with must stop the whole Apply, not leave the
	// page describing a budget the buffer never got. A save that fails after
	// this point still leaves the applied settings and the budget agreeing.
	if err := a.resolveBudgetFor(next); err != nil {
		a.settingsErr = err.Error()
		return
	}
	a.settingsErr = ""
	if err := a.applySettingsState(next); err != nil {
		a.settingsErr = err.Error()
		return
	}
	if err := a.saveSettingsSerialized(next); err != nil {
		a.settingsErr = fmt.Sprintf("applied, but saving failed: %v", err)
		return
	}
	a.settingsSaved.Store(true)
	a.settingsNote = fmt.Sprintf("applied %s", a.settingsPath)
	log.Printf("settings: applied %s", settingsSummary(next))
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
	log.Printf("settings: %s", settingsSummary(a.settings))
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
