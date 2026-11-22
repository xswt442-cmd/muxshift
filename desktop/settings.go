package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
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
	// timeline is unknown off until a person allows it. It is off by default so
	// a freshly added source can be heard; turning it on is a deliberate
	// protection for spoiler-sensitive setups (questions-v4 P0-1).
	MuteUnaligned bool `json:"muteUnaligned"`

	// MasterVolume multiplies every source's scene level at the player.
	MasterVolume float64 `json:"masterVolume"`

	// DockOpen makes the timeline dock open by default while the window is
	// wide enough for it.
	DockOpen bool `json:"dockOpen"`

	// ConsoleLayout is how the console draws the same sources: "panels" is the
	// three side-by-side panels, "rows" is one row per source with its volume,
	// its place in the stack and its offset. Both read the same state.
	ConsoleLayout string `json:"consoleLayout"`

	// Compact tightens the spacing of the workbench.
	Compact bool `json:"compact"`

	// Language is the window's language: "auto" follows the system, "zh" and
	// "en" are explicit choices a person made.
	Language string `json:"language"`

	// Theme names one of the palette packs in tokens.go; TypeScale names one of
	// the three text scales; FontFamily is a family the window can ask for, or
	// "auto" to follow the language.
	Theme      string `json:"theme"`
	TextScale  string `json:"textScale"`
	FontFamily string `json:"fontFamily"`

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
		MuteUnaligned: false,
		MasterVolume:  1,
		DockOpen:      true,
		ConsoleLayout: consolePanels,
		Compact:       false,
		Language:      languageAuto,
		Theme:         "midnight",
		TextScale:     "standard",
		FontFamily:    "auto",
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
		a.DockOpen != b.DockOpen || a.ConsoleLayout != b.ConsoleLayout || a.Compact != b.Compact ||
		a.Language != b.Language || a.Theme != b.Theme || a.TextScale != b.TextScale || a.FontFamily != b.FontFamily ||
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
	return fmt.Sprintf("start page %q, console %q, nudge %.2fs, default volume %.2f, master %.2f, mute unaligned %v, dock %v, compact %v, language %q, theme %q, text scale %q, font %q, dvr %s, source memory %d entr(ies) on=%v remember=%v set=%s",
		st.StartPage, st.ConsoleLayout, st.NudgeStep, st.DefaultVolume, st.MasterVolume, st.MuteUnaligned,
		st.DockOpen, st.Compact, st.Language, st.Theme, st.TextScale, st.FontFamily,
		st.DVR.line(), len(st.SourceMemory.Entries),
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

// fontSettings are the font settings the Appearance page offers. "auto" follows
// the language; the rest are families the build can rely on being installed.
var fontSettings = []string{"auto", "segoe", "yahei", "cascadia", "inter"}

func validFontChoice(name string) bool {
	for _, choice := range fontSettings {
		if choice == name {
			return true
		}
	}
	return false
}

func validScaleName(name string) bool {
	for _, scale := range typeScales {
		if scale.name == name {
			return true
		}
	}
	return false
}

func validThemeName(name string) bool {
	for _, pack := range themePacks {
		if pack.name == name {
			return true
		}
	}
	return false
}

// validateSettings rejects values the rest of the code cannot honour. A file
// that fails this check is not used at all: half-applied settings would be
// worse than the defaults, which are always valid.
func validateSettings(s settings) error {
	// Appearance arrived after the first settings files were written. An empty
	// value means "this file predates the field", which is not a mistake: the
	// reader fills it with the default. Rejecting the whole file over it would
	// throw away settings the user did choose.
	if s.Language != "" {
		switch normalizeLanguage(s.Language) {
		case languageAuto, languageZH, languageEN:
		default:
			return fmt.Errorf("language %q is not auto, zh or en", s.Language)
		}
	}
	if s.ConsoleLayout != "" {
		switch s.ConsoleLayout {
		case consolePanels, consoleRows:
		default:
			return fmt.Errorf("consoleLayout %q is not panels or rows", s.ConsoleLayout)
		}
	}
	switch s.StartPage {
	case "stage", "sources", "diagnostics", "settings":
	default:
		return fmt.Errorf("startPage %q is not a page, want stage, sources, diagnostics or settings", s.StartPage)
	}
	if s.Theme != "" && !validThemeName(s.Theme) {
		return fmt.Errorf("theme %q is not one of the packs this build carries", s.Theme)
	}
	if s.TextScale != "" && !validScaleName(s.TextScale) {
		return fmt.Errorf("textScale %q is not compact, standard or large", s.TextScale)
	}
	if s.FontFamily != "" && !validFontChoice(s.FontFamily) {
		return fmt.Errorf("fontFamily %q is not a family the Appearance page offers", s.FontFamily)
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
// from the drawing thread; the lock keeps two temp files from racing, and the
// ticket keeps them in order: whoever built its snapshot last wins, even if the
// goroutine is scheduled after the other write has already renamed.
func (a *appState) saveSettingsSerialized(st settings, ticket uint64) error {
	a.settingsSave.Lock()
	defer a.settingsSave.Unlock()
	if ticket < a.saveDone.Load() {
		// A newer snapshot is already on disk; writing this one would put an
		// older file in its place.
		return nil
	}
	err := saveSettings(a.settingsPath, st)
	if err == nil {
		a.saveDone.Store(ticket)
	}
	return err
}

// saveTicket reserves the place of one settings snapshot in the order of
// writes; call it where the snapshot is made, not where the write happens.
func (a *appState) saveTicket() uint64 {
	return a.saveSeq.Add(1)
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
	// harmless. On Windows it fails while another process — a reader, an indexer,
	// a virus scanner — still holds the target open, and that failure looks like
	// a permission error. Retry only that for a moment; every other failure is
	// reported at once rather than delayed.
	var last error
	for attempt := 0; attempt < 10; attempt++ {
		if last = os.Rename(name, path); last == nil {
			return nil
		}
		if !errors.Is(last, fs.ErrPermission) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
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
	a.beginDraft(next)
	a.apply()
	return nil
}

// beginDraft starts a fresh draft from the given settings and clears the mark
// that says this draft asked to forget the remembered sources. Nothing reaches
// the session until Apply, so the draft is the only place a Clear lives until
// then.
func (a *appState) beginDraft(from settings) {
	a.settingsDraft = cloneSettings(from)
	a.draftCleared = false
}

// mergeMemory joins the live remembered sources with the draft's list: the live
// entries are the newest, so they come first and keep their values, and an entry
// only the draft still holds is kept as the draft has it. It is what makes an
// Apply that never opened the Sources section leave the list alone.
func mergeMemory(live, draft sourceMemory) []sourceMemoryEntry {
	merged := make([]sourceMemoryEntry, 0, len(live.Entries)+len(draft.Entries))
	seen := make(map[string]bool, len(live.Entries))
	for _, entry := range live.Entries {
		merged = append(merged, entry)
		seen[entry.Address] = true
	}
	for _, entry := range draft.Entries {
		if seen[entry.Address] {
			continue
		}
		merged = append(merged, entry)
	}
	return merged
}

// volumeFor is the level a player gets for a scene level: the scene's own
// volume times the master volume from the settings.
func (a *appState) volumeFor(level float64) float64 {
	v := level * a.settings.MasterVolume
	return min(max(v, 0), 1)
}

// settingsSections names the page's sections in the order the page shows them.
// Six, not eight: the master volume belongs with the other playback levels, and
// the shortcut list is reference material for the same page that explains the
// keys, not a destination of its own.
var settingsSections = []string{"General", "Playback", "Appearance", "Sources", "DVR", "Diagnostics", "About"}

// settingsSectionLabel is the section's name in the window's language. The key
// in settingsSections stays English so a settings file or a log line never
// depends on the language the window happens to be in.
func (a *appState) settingsSectionLabel(name string) string {
	switch name {
	case "General":
		return a.tr("General", "通用")
	case "Playback":
		return a.tr("Playback", "播放")
	case "Appearance":
		return a.tr("Appearance", "外观")
	case "Sources":
		return a.tr("Sources", "来源")
	case "DVR":
		return a.tr("DVR", "回放缓存")
	case "Diagnostics":
		return a.tr("Diagnostics", "诊断")
	case "About":
		return a.tr("About", "关于")
	}
	return name
}

// choiceOption is one button in a segmented choice: the value stored in the
// settings and the text drawn for it.
type choiceOption struct {
	value string
	label string
}

// settingsPage draws the Global Settings page. The page edits a draft; nothing
// reaches the session until Apply, and Cancel puts the draft back.
func (a *appState) settingsPage(c *ui.Context) {
	ui.Row(c).WidthPercent(100).AlignItems(ui.Start).Gap(spaceLg).Padding(spaceMd).Children(func() {
		ui.Column(c).Width(190).Gap(spaceXs).Padding(spaceSm).Children(func() {
			ui.Text(c, a.tr("settings", "设置")).FontSize(textCaption).FontWeight(600).TextColor(colorTextDim)
			for _, section := range settingsSections {
				name := section
				selected := name == a.settingsSection
				row := ui.ButtonBase(c.Key("settings-tab-"+name)).Width(170).Height(34).
					Padding(0, spaceSm).Gap(spaceSm).Radius(radiusMd).Justify(ui.Start)
				row = row.Background(stateFill(row.Hovered(), row.Pressed(), selected))
				ink := colorTextDim
				if selected {
					ink = colorSelectedText
				}
				row = row.TextColor(ink)
				row.Children(func() {
					ui.Text(c, a.settingsSectionLabel(name)).FontSize(textBody).FontWeight(500).TextColor(ink)
				})
				if row.Clicked() {
					a.settingsSection = name
					a.settingsErr = ""
					a.settingsNote = ""
					if name == "About" && !a.updateSnapshot().checked {
						a.checkForUpdates()
					}
				}
			}
			ui.Box(c).Grow(1)
			ui.Text(c, a.settingsBoundary()).FontSize(textMicro).TextColor(colorDim).Wrap()
		})
		ui.Column(c).Grow(1).Gap(spaceSm).Padding(spaceXl).Radius(radiusLg).
			Background(colorPanel).Border(1, colorLineSoft).
			Shadow(0, 2, 8, 0, shadowCard).Children(func() {
			ui.Text(c, a.settingsSectionLabel(a.settingsSection)).FontSize(textTitle).FontWeight(600)
			a.settingsForm(c)
			if a.settingsErr != "" {
				ui.Text(c, a.settingsErr).TextColor(colorBad).Wrap()
			}
			if a.settingsNote != "" {
				ui.Text(c, a.settingsNote).TextColor(colorTextDim).Wrap()
			}
			if a.settingsSection != "About" {
				dirty := !sameSettings(a.settingsDraft, a.settings)
				ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).Children(func() {
					apply := ui.ButtonBase(c.Key("settings-apply")).Width(100).Height(32).
						Padding(0, spaceMd).Radius(radiusMd).AlignItems(ui.Center)
					applyFill, applyInk := ui.Transparent, colorDim
					if dirty {
						applyFill, applyInk = colorAccent, colorAccentText
						if apply.Pressed() {
							applyFill = colorAccentDown
						} else if apply.Hovered() {
							applyFill = colorAccentHi
						}
					} else {
						apply = apply.Disabled(true)
					}
					apply = apply.Background(applyFill).TextColor(applyInk)
					apply.Children(func() {
						ui.Text(c, a.tr("Apply", "应用")).FontSize(textSmall).FontWeight(500).
							TextColor(applyInk).SingleLine()
					})
					if apply.Clicked() {
						a.applySettingsCommand()
					}
					cancel := ui.ButtonBase(c.Key("settings-cancel")).Width(100).Height(32).
						Padding(0, spaceMd).Radius(radiusMd).AlignItems(ui.Center)
					cancelFill, cancelInk := ui.Transparent, colorDim
					if dirty {
						cancelFill, cancelInk = colorPanel2, colorText
						if cancel.Pressed() {
							cancelFill = colorPressed
						} else if cancel.Hovered() {
							cancelFill = colorHover
						}
					} else {
						cancel = cancel.Disabled(true)
					}
					cancel = cancel.Background(cancelFill).TextColor(cancelInk)
					cancel.Children(func() {
						ui.Text(c, a.tr("Cancel", "取消")).FontSize(textSmall).FontWeight(500).
							TextColor(cancelInk).SingleLine()
					})
					if cancel.Clicked() {
						a.beginDraft(a.settings)
						a.settingsErr = ""
						a.settingsNote = a.tr("cancelled: the draft is back to what is applied", "已取消：草稿恢复为当前生效的设置")
					}
					a.ghostButton(c, "settings-reset", a.tr("Reset to defaults", "恢复默认"), 160, func() {
						a.beginDraft(defaultSettings())
						a.settingsErr = ""
						a.settingsNote = a.tr("defaults are in the draft; Apply writes them", "默认值已填入草稿，点击“应用”写入")
					})
					if dirty {
						ui.Text(c, a.tr("unsaved changes", "有未应用的更改")).FontSize(textMicro).TextColor(colorWarn)
					}
				})
			}
		})
	})
}

// settingsBoundary says, in the page itself, what is saved and what is not.
func (a *appState) settingsBoundary() string {
	switch {
	case a.settingsPath == "":
		return a.tr("nothing is saved: this start has no configuration directory", "不会保存：本次启动没有配置目录")
	case a.settingsSaved.Load():
		return a.tr("saved to ", "已保存到 ") + a.settingsPath
	default:
		return a.tr("will be saved to ", "将保存到 ") + a.settingsPath
	}
}

// settingsForm is the right-hand part of one section.
func (a *appState) settingsForm(c *ui.Context) {
	switch a.settingsSection {
	case "General":
		a.choiceRow(c, a.tr("Language", "语言"), "settings-language", &a.settingsDraft.Language, []choiceOption{
			{"auto", a.tr("System", "跟随系统")},
			{"zh", "中文"},
			{"en", "English"},
		})
		a.settingsHint(c, a.tr("follows the Windows display language until you choose one", "默认跟随 Windows 显示语言，选择后以你的选择为准"))
		a.choiceRow(c, a.tr("Open on", "启动页"), "settings-start", &a.settingsDraft.StartPage, []choiceOption{
			{"stage", a.tr("Stage", "舞台")},
			{"sources", a.tr("Sources", "来源")},
		})
		a.settingsSlider(c, a.tr("Nudge step", "微调步长"), &a.settingsDraft.NudgeStep, 0.02, 1)
		a.checkboxRow(c, &a.settingsDraft.Workspace.On, a.tr("Reopen the window where I left it", "记住窗口位置与页面"), false, "")
		a.settingsHint(c, a.tr("written when the window closes; an unknown page or frame falls back", "关窗时保存；页面或窗口位置无效时回到默认"))
		ui.Text(c, a.tr("Keys", "快捷键")).FontSize(textMicro).TextColor(colorDim).Padding(spaceSm, 0)
		a.shortcutLine(c, "Ctrl+S", a.tr("show or hide the selected source", "显示/隐藏选中来源"))
		a.shortcutLine(c, "Ctrl+H", a.tr("hear the selected source", "试听选中来源"))
		a.shortcutLine(c, "Ctrl+R", a.tr("retry the selected player", "重试选中来源的播放器"))
		a.shortcutLine(c, "Ctrl+1 … 5", a.tr("stage, sources, diagnostics, settings, mixer", "舞台、来源、诊断、设置、混音"))
		a.shortcutLine(c, "F11 / Esc", a.tr("window full screen, back to editing", "窗口全屏、返回编辑"))
		a.settingsHint(c, a.tr("a text field keeps the keys to itself while it has the focus", "文本框获得焦点时这些键不生效"))
	case "Playback":
		a.settingsSlider(c, a.tr("Default volume for new sources", "新来源的默认音量"), &a.settingsDraft.DefaultVolume, 0, 1)
		a.settingsSlider(c, a.tr("Master volume", "总音量"), &a.settingsDraft.MasterVolume, 0, 1)
		a.settingsHint(c, a.tr("the master multiplies every source; per-source levels live in the mixer", "总音量作用于所有来源；单个来源的音量在混音器里"))
		a.checkboxRow(c, &a.settingsDraft.MuteUnaligned, a.tr("Keep an unplaced source silent until it is allowed", "共同时间轴未确定的来源先静音"), false, "")
		a.settingsHint(c, a.tr("with this off, sound can play before a person allows it", "关闭后，未对齐的来源可能提前出声"))
		a.settingsHint(c, a.tr("a source's own Allow button still overrides the mute", "来源自己的“允许”按钮仍然优先"))
	case "Sources":
		a.checkboxRow(c, &a.settingsDraft.SourceMemory.On, a.tr("Reuse the level and mute I set for a source", "来源再次出现时沿用音量和静音"), false, "")
		a.checkboxRow(c, &a.settingsDraft.SourceMemory.Remember, a.tr("Remember new sources' level and mute", "记住新来源的音量与静音"), !a.settingsDraft.SourceMemory.On,
			a.tr("memory is off, so nothing new is remembered", "记忆已关闭，不会记录新来源"))
		a.settingsHint(c, a.tr("remembers level and mute only; never reopens an address and never allows a source", "只记住音量与静音；不会重新打开地址，也不会授权任何来源"))
		if !a.settingsDraft.SourceMemory.On && a.settingsDraft.SourceMemory.Remember {
			ui.Text(c, a.tr("Remember is on while memory is off, so nothing is written", "记忆关闭时“记住”不起作用，不会写入")).
				FontSize(textMicro).TextColor(colorWarn).Wrap()
		}
		if a.settingsDraft.SourceMemory.On != a.settings.SourceMemory.On || a.settingsDraft.SourceMemory.Remember != a.settings.SourceMemory.Remember {
			ui.Text(c, a.tr("draft: these switches take effect on Apply", "草稿：点击“应用”后生效")).
				FontSize(textMicro).TextColor(colorWarn).Wrap()
		}
		entries := a.settingsDraft.SourceMemory.Entries
		if len(entries) == 0 {
			ui.Text(c, a.tr("no remembered address", "没有记住的地址")).FontSize(textMicro).TextColor(colorDim)
		} else {
			ui.Text(c, strconv.Itoa(len(entries))+a.tr(" remembered address(es), newest first", " 个地址已记住，最近的在前")).
				FontSize(textMicro).TextColor(colorDim)
			for i, entry := range entries {
				if i >= 4 {
					ui.Text(c, "… "+strconv.Itoa(len(entries)-4)+a.tr(" more", " 个")).FontSize(textMicro).TextColor(colorDim)
					break
				}
				muted := ""
				if entry.Muted {
					muted = a.tr(", muted", "，已静音")
				}
				ui.Text(c, fmt.Sprintf("%s  %.0f%%%s", identityDigest(entry.Address), entry.Volume*100, muted)).
					FontSize(textMicro).TextColor(colorTextDim)
			}
			if !a.settingsDraft.SourceMemory.On {
				ui.Text(c, strconv.Itoa(len(entries))+a.tr(" remembered sources are inactive while memory is off", " 个已记住的来源在记忆关闭期间不生效")).
					FontSize(textMicro).TextColor(colorWarn).Wrap()
			}
			if ui.Button(c.Key("settings-memory-clear"), a.tr("Forget every remembered address", "清除所有记住的地址")).Width(280).Clicked() {
				forgotten := a.settingsDraft.SourceMemory.clear()
				a.draftCleared = true
				a.settingsNote = a.tr("forgot ", "已清除 ") + strconv.Itoa(forgotten) + a.tr(" remembered source(s); Apply writes the empty list", " 个记住的来源；应用后写入空列表")
			}
		}
	case "Appearance":
		a.choiceRow(c, a.tr("Colour theme", "配色"), "settings-theme", &a.settingsDraft.Theme, a.themeChoices())
		a.settingsHint(c, a.tr("applies at once; the stage itself is never tinted", "立即生效；画布本身不着色"))
		a.choiceRow(c, a.tr("Text size", "字号"), "settings-scale", &a.settingsDraft.TextScale, []choiceOption{
			{"compact", a.tr("Compact", "紧凑")},
			{"standard", a.tr("Standard", "标准")},
			{"large", a.tr("Large", "大")},
		})
		a.choiceRow(c, a.tr("Font", "字体"), "settings-font", &a.settingsDraft.FontFamily, a.fontChoices())
		a.settingsHint(c, a.tr("system picks a family that covers the window's language", "“跟随系统”会按界面语言选择字体"))
		a.checkboxRow(c, &a.settingsDraft.Compact, a.tr("Tighter spacing", "更紧凑的间距"), false, "")
		a.checkboxRow(c, &a.settingsDraft.DockOpen, a.tr("Show the timeline under the stage", "在画布下方显示时间线"), false, "")
		a.settingsHint(c, a.tr("spacing and the timeline apply when applied; both are remembered", "间距与时间线在应用后生效并记住"))
		a.choiceRow(c, a.tr("Console layout", "控制台布局"), "settings-console", &a.settingsDraft.ConsoleLayout, []choiceOption{
			{consolePanels, a.tr("Panels", "面板")},
			{consoleRows, a.tr("One row per source", "每源一行")},
		})
		a.settingsHint(c, a.tr("both read the same sources, levels and offsets", "两种布局读的是同一批来源、音量与偏移"))
	case "DVR":
		ui.Text(c, a.tr("what a new replay buffer may use; the marker shows a buffer opened now", "新回放缓存的上限；下方数值为当前生效值")).Wrap()
		a.dvrStepper(c, a.tr("Keep this much history", "保留时长"), "dvr-ret-less", a.tr("-1 min", "-1 分"), "dvr-ret-more", a.tr("+1 min", "+1 分"),
			strconv.Itoa(a.settingsDraft.DVR.RetentionSeconds)+a.tr(" s", " 秒"), func(delta int) {
				next := a.settingsDraft.DVR.RetentionSeconds + 60*delta
				a.settingsDraft.DVR.RetentionSeconds = min(maxRetentionSeconds, max(minRetentionSeconds, next))
			})
		a.dvrStepper(c, a.tr("Segment cache limit", "分片缓存上限"), "dvr-max-less", "-64 MiB", "dvr-max-more", "+64 MiB",
			bytesLine(a.settingsDraft.DVR.MaxBytes), func(delta int) {
				next := a.settingsDraft.DVR.MaxBytes + int64(delta)*64<<20
				a.settingsDraft.DVR.MaxBytes = min(maxBudgetBytes, max(0, next))
			})
		a.dvrStepper(c, a.tr("Recording limit", "录制上限"), "dvr-rec-less", "-64 MiB", "dvr-rec-more", "+64 MiB",
			bytesLine(a.settingsDraft.DVR.RecordingBytes), func(delta int) {
				next := a.settingsDraft.DVR.RecordingBytes + int64(delta)*64<<20
				a.settingsDraft.DVR.RecordingBytes = min(maxBudgetBytes, max(0, next))
			})
		a.settingsHint(c, a.tr("in force for a buffer opened now: ", "当前生效：")+a.budget.line()+a.tr(" from ", "，来自 ")+a.budgetFrom)
		if a.settingsDraft.DVR.MaxBytes == 0 || a.settingsDraft.DVR.RecordingBytes == 0 {
			ui.Text(c, a.tr("a zero byte limit removes that limit, not shrinks it", "字节上限为 0 表示不限制，而不是更小")).
				FontSize(textMicro).TextColor(colorWarn).Wrap()
		}
		a.settingsHint(c, a.tr("an explicit command line flag wins over these numbers", "命令行显式给出的参数优先"))
	case "Diagnostics":
		ui.Text(c, strconv.Itoa(a.eventCount())+a.tr(" event(s) in the session, ", " 个事件，")+strconv.Itoa(len(a.items))+a.tr(" source(s)", " 个来源"))
		ui.Text(c, a.tr("settings file: ", "设置文件：")+a.settingsPath).Wrap()
		if ui.Button(c.Key("diag-log-state"), a.tr("Write the current state to the log", "把当前状态写入日志")).Width(260).Clicked() {
			a.logState()
		}
		ui.Button(c.Key("diag-capture"), a.tr("Capture a page on the next failure", "下次失败时抓取页面")).Width(260).Disabled(true).
			Tooltip(a.tr("there is no page capture on failure yet", "尚未实现失败时抓取页面"))
	case "About":
		a.aboutPage(c)
	}
}

// choiceRow is a labelled segmented control: every option visible at once, so a
// five-pack theme list needs no dropdown and no explanation.
func (a *appState) choiceRow(c *ui.Context, label, key string, current *string, options []choiceOption) {
	ui.Column(c).Gap(spaceXs).WidthPercent(100).Children(func() {
		ui.Text(c, label).FontSize(textMicro).TextColor(colorDim)
		ui.Row(c).Height(32).Padding(3).Gap(2).Radius(9).Background(colorBg).
			AlignSelf(ui.Start).AlignItems(ui.Center).Children(func() {
			for _, option := range options {
				selected := *current == option.value
				button := ui.ButtonBase(c.Key(key+"-"+option.value)).Height(26).
					Padding(0, spaceMd).Radius(radiusSm).AlignItems(ui.Center)
				fill, ink := ui.Transparent, colorTextDim
				if selected {
					fill, ink = colorSegment, colorText
				}
				if button.Pressed() {
					fill, ink = colorPressed, colorText
				} else if button.Hovered() {
					ink = colorText
					if !selected {
						fill = colorHover
					}
				}
				button = button.Background(fill).TextColor(ink)
				button.Children(func() {
					weight := 400
					if selected {
						weight = 500
					}
					ui.Text(c, option.label).FontSize(textSmall).FontWeight(weight).
						TextColor(ink).SingleLine()
				})
				if button.Clicked() {
					*current = option.value
				}
			}
		})
	})
}

// themeChoices names the packs in the window's language.
func (a *appState) themeChoices() []choiceOption {
	out := make([]choiceOption, 0, len(themePacks))
	for _, pack := range themePacks {
		out = append(out, choiceOption{pack.name, a.tr(pack.label, pack.labelZH)})
	}
	return out
}

// fontChoices labels the font setting.
func (a *appState) fontChoices() []choiceOption {
	return []choiceOption{
		{"auto", a.tr("System", "跟随系统")},
		{"yahei", a.tr("YaHei (CJK)", "微软雅黑")},
		{"segoe", "Segoe UI"},
		{"inter", "Inter"},
		{"cascadia", a.tr("Cascadia Mono", "Cascadia 等宽")},
	}
}

// settingsRow puts a label and a control on one line. Its 260px label is fine
// on a wide page and too wide beside a folded rail, so the DVR numbers use
// dvrStepper instead.
func (a *appState) settingsRow(c *ui.Context, label string, control func()) {
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(260).FontSize(textBody).TextColor(colorText)
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
	ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(textBody).TextColor(colorText)
		ui.Box(c).Grow(1)
		ui.Text(c, fmt.Sprintf("%.2f", *value)).Width(64)
	})
	a.rangeSlider(c, "settings-"+label, value, lo, hi, 0)
}

// settingsHint is the small line under a control that explains what it does.
func (a *appState) settingsHint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(textMicro).TextColor(colorDim).Wrap()
}

// checkboxRow is the settings check box: an 18 DIP box with a 5 DIP radius,
// 12 DIP check mark and the label 8 DIP to its right. It keeps the stock row's
// click, Space and focus behavior; only the face is ours.
func (a *appState) checkboxRow(c *ui.Context, checked *bool, label string, disabled bool, tip string) ui.Element {
	row := ui.CheckboxBase(c, checked).Gap(spaceSm).AlignItems(ui.Center).FocusRing(false)
	if disabled {
		row = row.Disabled(true)
	}
	if tip != "" {
		row = row.Tooltip(tip)
	}
	fill, edge := colorPanel2, colorCheckEdge
	if disabled {
		fill, edge = colorPanel2, colorLine
	} else if row.Pressed() {
		fill, edge = colorAccentDown, colorAccentDown
	} else if *checked {
		fill, edge = colorAccent, colorAccent
	} else if row.Hovered() {
		fill, edge = colorHover, colorDim
	}
	ink := colorText
	if disabled {
		ink = colorDim
	}
	row.Children(func() {
		box := ui.Box(c).Width(18).Height(18).Radius(5).Shrink(0).
			Background(fill).Border(1, edge).Center()
		box.DrawOver(func(p *ui.Painter, r ui.Rect) {
			if row.FocusVisible() {
				p.FocusRing(r, [4]float32{5, 5, 5, 5})
			}
		})
		box.Children(func() {
			if *checked {
				ui.Icon(c, lucide.Check()).Size(12, 12).TextColor(colorAccentText)
			}
		})
		ui.Text(c, label).FontSize(textBody).TextColor(ink)
	})
	return row
}

// shortcutLine names one window shortcut and what it does. The list is read
// from the bindings in main.go's view; a key that is not bound is not listed.
func (a *appState) shortcutLine(c *ui.Context, keys, what string) {
	ui.Row(c).Gap(12).WidthPercent(100).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Width(150).Gap(4).AlignItems(ui.Center).Children(func() {
			a.shortcutKeys(c, keys)
		})
		ui.Text(c, what).FontSize(textCaption).TextColor(colorTextDim)
	})
}

// shortcutKeys draws one shortcut as keycaps: Ctrl + S, or F11 / Esc. The
// separators stay in the quiet ink so the caps, not the punctuation, carry
// the shape.
func (a *appState) shortcutKeys(c *ui.Context, keys string) {
	if parts := strings.Split(keys, " / "); len(parts) == 2 {
		a.keycap(c, parts[0])
		ui.Text(c, "/").FontSize(textMicro).TextColor(colorDim)
		a.keycap(c, parts[1])
		return
	}
	parts := strings.Split(keys, "+")
	for i, part := range parts {
		if i > 0 {
			ui.Text(c, "+").FontSize(textMicro).TextColor(colorDim)
		}
		a.keycap(c, strings.TrimSpace(part))
	}
}

// keycap is display-only: a 22 DIP cap with a 4 DIP radius and one hairline.
func (a *appState) keycap(c *ui.Context, label string) {
	ui.Box(c).Height(22).MinWidth(22).Padding(0, 6).Radius(4).
		Background(colorPanel2).Border(1, colorLine).Center().Children(func() {
		ui.Text(c, label).FontSize(textMicro).FontWeight(500).TextColor(colorText).SingleLine()
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
	// A page that never cleared the Sources list applies its other sections
	// without deleting what was remembered while the draft was open: the live
	// entries come first (they are the newest), and any entry the draft still
	// holds but the live set dropped is kept as the draft has it. Only a Clear
	// in this draft empties the list.
	if !a.draftCleared {
		next.SourceMemory.Entries = mergeMemory(a.settings.SourceMemory, next.SourceMemory)
	}
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
	if err := a.saveSettingsSerialized(next, a.saveTicket()); err != nil {
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
