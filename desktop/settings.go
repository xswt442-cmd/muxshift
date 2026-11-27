package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
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
	// protection for spoiler-sensitive setups.
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

const bytesPerMiB = int64(1) << 20

type dvrField struct {
	key, label, labelZH   string
	scale, min, max, step int64
}

var dvrFields = [...]dvrField{
	{"dvr-ret", "Keep this much history", "保留时长", 1, minRetentionSeconds, maxRetentionSeconds, 60},
	{"dvr-max", "Segment cache limit", "分片缓存上限", bytesPerMiB, 0, maxBudgetBytes, 64 * bytesPerMiB},
	{"dvr-rec", "Recording limit", "录制上限", bytesPerMiB, 0, maxBudgetBytes, 64 * bytesPerMiB},
}

type dvrDraftInput struct {
	text string
	base string // formatted draft value before the user edited the field
}

func (f dvrField) format(value int64) string {
	number := float64(value) / float64(f.scale)
	if number > 0 && number < .01 {
		return strconv.FormatFloat(number, 'g', 8, 64)
	}
	return strconv.FormatFloat(number, 'f', -1, 64)
}

func (f dvrField) parse(text string) (int64, bool) {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, "_xX") || strings.HasPrefix(text, "-") {
		return 0, false
	}
	if f.scale == 1 {
		n, err := strconv.ParseInt(text, 10, 64)
		return n, err == nil && n >= f.min && n <= f.max
	}
	n, err := strconv.ParseFloat(text, 64)
	scaled := n * float64(f.scale)
	if err != nil || !(scaled >= float64(f.min) && scaled <= float64(f.max)) {
		return 0, false
	}
	// Convert fractional MiB to the nearest byte. Only an explicit zero
	// should remove the limit.
	bytes := int64(scaled + 0.5)
	if bytes == 0 && n > 0 {
		bytes = 1
	}
	return bytes, true
}

func (b dvrBudget) fieldValues() [3]int64 {
	return [3]int64{int64(b.RetentionSeconds), b.MaxBytes, b.RecordingBytes}
}

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

// bytesLabel is bytesLine for the page: it reads in the window's language and
// names zero as "no limit" rather than a number, because in a budget zero
// removes the limit instead of shrinking it. The log keeps bytesLine.
func (a *appState) bytesLabel(n int64) string {
	if n == 0 {
		return a.tr("no limit", "不限制")
	}
	return strconv.FormatFloat(float64(n)/float64(bytesPerMiB), 'f', -1, 64) + " MiB"
}

// dvrLimitSummary states the budget in force as one short line in the window's
// language, so the DVR section does not print the English dvrBudget.line().
func (a *appState) dvrLimitSummary(b dvrBudget) string {
	return a.tr(
		fmt.Sprintf("in force now: keep %ds · buffer %s · recordings %s", b.RetentionSeconds, a.bytesLabel(b.MaxBytes), a.bytesLabel(b.RecordingBytes)),
		fmt.Sprintf("当前生效：保留 %d 秒 · 缓存 %s · 录制 %s", b.RetentionSeconds, a.bytesLabel(b.MaxBytes), a.bytesLabel(b.RecordingBytes)),
	)
}

// budgetSourceLabel names where the budget in force came from, in the page's
// language, so the DVR line never mixes an English clause into Chinese.
func (a *appState) budgetSourceLabel() string {
	switch {
	case a.budgetFrom == "the built-in defaults":
		return a.tr("built-in defaults", "内置默认")
	case a.budgetFrom == "the settings file":
		return a.tr("the settings file", "设置文件")
	case strings.HasPrefix(a.budgetFrom, "the command line"):
		return a.tr("the command line", "命令行")
	}
	return a.budgetFrom
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
		if math.IsNaN(entry.Volume) || entry.Volume < 0 || entry.Volume > 1 {
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
	if math.IsNaN(s.DefaultVolume) || s.DefaultVolume < 0 || s.DefaultVolume > 1 {
		return fmt.Errorf("defaultVolume %.3f is not a level between 0 and 1", s.DefaultVolume)
	}
	if math.IsNaN(s.NudgeStep) || s.NudgeStep < 0.02 || s.NudgeStep > 1 {
		return fmt.Errorf("nudgeStep %.3fs is not between 0.02s and 1s", s.NudgeStep)
	}
	if math.IsNaN(s.MasterVolume) || s.MasterVolume < 0 || s.MasterVolume > 1 {
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
	if next.DockOpen != a.settings.DockOpen {
		a.dockUp = next.DockOpen
	}
	a.settings = cloneSettings(next)
	a.beginDraft(next)
	a.apply()
	return nil
}

// beginDraft starts a fresh draft from the given settings and clears the mark
// that says this draft asked to forget the remembered sources. Appearance has
// its immediate path; the other draft changes reach the session on Apply.
func (a *appState) beginDraft(from settings) {
	a.settingsDraft = cloneSettings(from)
	a.dvrInputs = [3]dvrDraftInput{}
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
// Seven, not eight: the master volume belongs with the other playback levels,
// and the shortcut list is reference material for the same page that explains
// the keys, not a destination of its own.
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

// settingsPage draws the Global Settings page. The page edits a draft that Apply
// commits; Cancel restores it. Appearance has its explicit immediate path.
func (a *appState) settingsPage(c *ui.Context) {
	ui.Row(c).WidthPercent(100).AlignItems(ui.Start).Gap(spaceLg).Padding(spaceMd).Children(func() {
		ui.Column(c).Width(190).Gap(spaceXs).Padding(spaceSm).Children(func() {
			a.overlineLabel(c, a.tr("settings", "设置"))
			for _, section := range settingsSections {
				name := section
				selected := name == a.settingsSection
				row := ui.ButtonBase(c.Key("settings-tab-"+name)).Width(170).Height(a.controlHeight()).
					Padding(0, controlPadX).Gap(spaceSm).Radius(radiusMd).Justify(ui.Start)
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
			// Only a start that cannot save needs a line here. The boundary
			// never prints the file's path: the About page lists it instead.
			if a.settingsPath == "" {
				ui.Text(c, a.settingsBoundary()).FontSize(textMicro).TextColor(colorDim).Wrap()
			}
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
			if a.settingsSection != "About" && a.settingsSection != "Diagnostics" {
				dirty := !sameSettings(a.settingsDraft, a.settings)
				for _, input := range a.dvrInputs {
					dirty = dirty || input.text != input.base
				}
				ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).Children(func() {
					a.primaryButton(c, "settings-apply", a.tr("Apply", "应用"), 100, dirty || a.settingsSaveDirty, func() {
						a.applySettingsCommand()
					})
					a.secondaryButton(c, "settings-cancel", a.tr("Cancel", "取消"), 100, dirty, func() {
						a.cancelSettingsDraft()
					}).Border(1, colorLineSoft)
					a.secondaryButton(c, "settings-reset", a.tr("Restore defaults", "恢复默认"), 180, true, func() {
						a.resetSettingsDraft()
					}).Border(1, colorLineSoft)
					if dirty {
						ui.Text(c, a.tr("unsaved changes", "有未应用的更改")).FontSize(textMicro).TextColor(colorWarn)
					}
				})
			}
		})
	})
}

// settingsBoundary says, in one short line, whether this start writes the file.
// It never prints the file's path: a settings page is user-facing, and an
// absolute path there is noise (the About page lists it for diagnosis).
func (a *appState) settingsBoundary() string {
	switch {
	case !a.settingsTrusted:
		return a.tr("the original settings file is protected", "原设置文件受保护")
	case a.settingsPath == "":
		return a.tr("this start will not save", "本次启动不会保存")
	case a.settingsSaved.Load():
		return a.tr("saved to the settings file", "已保存到设置文件")
	default:
		return a.tr("changes save to the settings file", "更改会保存到设置文件")
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
		a.choiceRow(c, a.tr("Open on", "启动页"), "settings-start", &a.settingsDraft.StartPage, []choiceOption{
			{"stage", a.tr("Stage", "舞台")},
			{"sources", a.tr("Sources", "来源")},
		})
		a.settingsSlider(c, a.tr("Nudge step", "微调步长"), &a.settingsDraft.NudgeStep, 0.02, 1)
		a.checkboxRow(c, &a.settingsDraft.Workspace.On, a.tr("Reopen the window where I left it", "记住窗口位置与页面"), false, "")
		ui.Text(c, a.tr("Keys", "快捷键")).FontSize(textMicro).TextColor(colorDim).Padding(spaceSm, 0)
		a.shortcutLine(c, "Ctrl+S", a.tr("show or hide the selected source", "显示/隐藏选中来源"))
		a.shortcutLine(c, "Ctrl+H", a.tr("hear the selected source", "试听选中来源"))
		a.shortcutLine(c, "Ctrl+R", a.tr("retry the selected player", "重试选中来源的播放器"))
		a.shortcutLine(c, "Ctrl+1 … 3", a.tr("stage, diagnostics, settings", "舞台、诊断、设置"))
		a.shortcutLine(c, "F11 / Esc", a.tr("window full screen, back to editing", "窗口全屏、返回编辑"))
	case "Playback":
		a.settingsSlider(c, a.tr("Default volume for new sources", "新来源的默认音量"), &a.settingsDraft.DefaultVolume, 0, 1)
		a.settingsSlider(c, a.tr("Master volume", "总音量"), &a.settingsDraft.MasterVolume, 0, 1)
		a.checkboxRow(c, &a.settingsDraft.MuteUnaligned, a.tr("Keep an unplaced source silent until it is allowed", "共同时间轴未确定的来源先静音"), false, "")
	case "Sources":
		a.checkboxRow(c, &a.settingsDraft.SourceMemory.On, a.tr("Reuse the level and mute I set for a source", "来源再次出现时沿用音量和静音"), false, "")
		a.checkboxRow(c, &a.settingsDraft.SourceMemory.Remember, a.tr("Remember new sources' level and mute", "记住新来源的音量与静音"), !a.settingsDraft.SourceMemory.On,
			a.tr("memory is off, so nothing new is remembered", "记忆已关闭，不会记录新来源"))
		a.settingsHint(c, a.tr("only level and mute; never reopens or authorises a source", "只记住音量与静音；不会重新打开或授权来源"))
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
			if ui.Button(c.Key("settings-memory-clear"), a.tr("Forget every remembered address", "清除所有记住的地址")).Width(280).Clicked() {
				forgotten := a.settingsDraft.SourceMemory.clear()
				a.draftCleared = true
				a.settingsNote = a.tr("forgot ", "已清除 ") + strconv.Itoa(forgotten) + a.tr(" remembered source(s); Apply writes the empty list", " 个记住的来源；应用后写入空列表")
			}
		}
	case "Appearance":
		ui.Text(c, a.tr("These appearance controls apply and save immediately. Resetting the settings draft does not change them.",
			"这些外观选项会立即生效并保存。重置设置草稿不会更改它们。")).
			FontSize(textMicro).TextColor(colorTextDim).Wrap()
		before := appearanceOf(a.settingsDraft)
		a.choiceRow(c, a.tr("Colour theme", "配色"), "settings-theme", &a.settingsDraft.Theme, a.themeChoices())
		a.choiceRow(c, a.tr("Text size", "字号"), "settings-scale", &a.settingsDraft.TextScale, []choiceOption{
			{"compact", a.tr("Compact", "紧凑")},
			{"standard", a.tr("Standard", "标准")},
			{"large", a.tr("Large", "大")},
		})
		a.choiceRow(c, a.tr("Font", "字体"), "settings-font", &a.settingsDraft.FontFamily, a.fontChoices())
		a.checkboxRow(c, &a.settingsDraft.Compact, a.tr("Tighter spacing", "更紧凑的间距"), false, "")
		a.checkboxRow(c, &a.settingsDraft.DockOpen, a.tr("Show the timeline under the stage", "在画布下方显示时间线"), false, "")
		if appearanceOf(a.settingsDraft) != before {
			a.applyAppearanceNow()
		}
		if ui.Button(c.Key("settings-appearance-reset"), a.tr("Reset appearance now", "立即恢复外观默认值")).Height(28).Clicked() {
			a.resetAppearanceNow()
		}
		ui.Text(c, a.tr("Console layout is saved with Apply.", "控制台布局会随“应用”保存。")).
			FontSize(textMicro).TextColor(colorTextDim).Wrap()
		a.choiceRow(c, a.tr("Console layout", "控制台布局"), "settings-console", &a.settingsDraft.ConsoleLayout, []choiceOption{
			{consolePanels, a.tr("Panels", "面板")},
			{consoleRows, a.tr("One row per source", "每源一行")},
		})
	case "DVR":
		ui.Text(c, a.tr("These limits apply to newly opened replay buffers.", "这些设置适用于新开的回放缓存。")).Wrap()
		values := a.settingsDraft.DVR.fieldValues()
		for i, field := range dvrFields {
			a.dvrStepper(c, field, &a.dvrInputs[i], values[i])
		}
		a.settingsHint(c, a.dvrLimitSummary(a.budget)+a.tr(" (from ", "（来自 ")+a.budgetSourceLabel()+a.tr(")", "）"))
		a.settingsHint(c, a.tr("Enter 0 for an unlimited cache or recording size.", "缓存或录制上限填 0 表示不限制。"))
	case "Diagnostics":
		// This settings section is read-only. The shared page navigation owns
		// the route to the full diagnostics page.
		ui.Text(c, strconv.Itoa(a.eventCount())+a.tr(" event(s) in the session, ", " 个事件，")+strconv.Itoa(len(a.items))+a.tr(" source(s)", " 个来源"))
	case "About":
		a.aboutPage(c)
	}
}

// choiceRow is a labelled segmented control: every option visible at once, so a
// five-pack theme list needs no dropdown and no explanation.
func (a *appState) choiceRow(c *ui.Context, label, key string, current *string, options []choiceOption) {
	ui.Column(c).Gap(spaceXs).WidthPercent(100).Children(func() {
		ui.Text(c, label).FontSize(textMicro).TextColor(colorDim)
		track := a.segmentTrack(c)
		track.AlignSelf(ui.Start).Children(func() {
			for _, option := range options {
				opt := option
				a.segmentedOption(c, key+"-"+opt.value, opt.label, *current == opt.value, false, func() {
					*current = opt.value
				})
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
func (a *appState) dvrStepper(c *ui.Context, field dvrField, input *dvrDraftInput, fallback int64) {
	if input.text == input.base {
		input.base = field.format(fallback)
		input.text = input.base
	}
	name := a.tr(field.label, field.labelZH)
	unit := a.dvrFieldUnit(field)
	value, valid := field.parse(input.text)
	change := func(delta int64) {
		input.text = field.format(min(field.max, max(field.min, value+delta)))
	}
	step := field.format(field.step) + " " + unit
	ui.Column(c).Gap(spaceXs).WidthPercent(100).Children(func() {
		ui.Text(c, name).FontSize(textMicro).TextColor(colorDim)
		ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
			a.secondaryButton(c, field.key+"-less", "−", 36, valid && value > field.min,
				func() { change(-field.step) }).Tooltip("−" + step)
			c.Key(field.key + "-value")
			number := a.field(c, &input.text).Width(100).TextAlign(ui.End).
				FontSize(textBody).FontWeight(500).FontFeatures("tnum").TextColor(colorText).
				Description(name + " (" + unit + ")")
			if !valid {
				number.Border(1, colorBad)
			}
			ui.Text(c, unit).Width(34).FontSize(textMicro).TextColor(colorTextDim).SingleLine()
			a.secondaryButton(c, field.key+"-more", "+", 36, valid && value < field.max,
				func() { change(field.step) }).Tooltip("+" + step)
		})
	})
}

func (a *appState) dvrFieldUnit(field dvrField) string {
	if field.scale == 1 {
		return a.tr("s", "秒")
	}
	return "MiB"
}

// settingsSlider is a slider with the value spelled out beside it, because a
// slider alone cannot be read back from a screenshot.
func (a *appState) settingsSlider(c *ui.Context, label string, value *float64, lo, hi float64) {
	ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(textBody).TextColor(colorText)
		ui.Box(c).Grow(1)
		ui.Text(c, fmt.Sprintf("%.2f", *value)).Width(64).FontSize(textSmall).
			FontWeight(500).TextColor(colorTextDim).TextAlign(ui.End).FontFeatures("tnum").SingleLine()
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
	values := next.DVR.fieldValues()
	for i, input := range a.dvrInputs {
		if input.text == input.base {
			continue
		}
		field := dvrFields[i]
		value, ok := field.parse(input.text)
		if !ok {
			name := a.tr(field.label, field.labelZH)
			limits := field.format(field.min) + "–" + field.format(field.max) + " " + a.dvrFieldUnit(field)
			a.settingsErr = fmt.Sprintf(a.tr("%s must be within %s.", "%s应在 %s 之间。"), name, limits)
			a.settingsNote = ""
			return
		}
		values[i] = value
	}
	next.DVR = dvrBudget{RetentionSeconds: int(values[0]), MaxBytes: values[1], RecordingBytes: values[2]}
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
	a.finishSettingsSave(next, a.tr("applied and saved", "已应用并保存"))
	log.Printf("settings: applied %s", settingsSummary(next))
}

func (a *appState) finishSettingsSave(next settings, savedNote string) {
	defer a.invalidate()
	a.settingsErr, a.settingsNote = "", ""
	a.settingsSaveDirty = false
	if !a.settingsTrusted {
		a.settingsSaved.Store(false)
		a.settingsNote = a.tr("Applied for this run. The original settings file was left untouched.",
			"已在本次运行中应用，原设置文件未改动。")
		return
	}
	if err := a.saveSettingsSerialized(next, a.saveTicket()); err != nil {
		a.settingsSaved.Store(false)
		a.settingsSaveDirty = true
		a.settingsErr = fmt.Sprintf(a.tr("Applied, but saving failed: %v", "已应用，但保存失败：%v"), safeOpenError(err))
		return
	}
	a.settingsSaved.Store(true)
	a.settingsNote = savedNote
}

// appearanceFields groups the appearance controls so the page can detect a
// draft change and preview it without writing settings.
type appearanceFields struct {
	Theme      string
	TextScale  string
	FontFamily string
	Compact    bool
	DockOpen   bool
}

// appearanceOf reads the five immediate controls out of a settings value.
func appearanceOf(st settings) appearanceFields {
	return appearanceFields{
		Theme:      st.Theme,
		TextScale:  st.TextScale,
		FontFamily: st.FontFamily,
		Compact:    st.Compact,
		DockOpen:   st.DockOpen,
	}
}

func (a *appState) cancelSettingsDraft() {
	a.beginDraft(a.settings)
	a.settingsErr = ""
	a.settingsNote = a.tr("Changes discarded.", "已取消未应用的更改。")
	a.invalidate()
}

func (a *appState) resetSettingsDraft() {
	a.beginDraft(defaultSettings())
	copyAppearance(&a.settingsDraft, a.settings)
	a.settingsErr = ""
	a.settingsNote = a.tr("Defaults loaded. Select Apply to save them. Appearance has its own reset button.", "已填入默认值，点击「应用」保存。外观有单独的恢复按钮。")
}

func copyAppearance(target *settings, source settings) {
	target.Theme = source.Theme
	target.TextScale = source.TextScale
	target.FontFamily = source.FontFamily
	target.Compact = source.Compact
	target.DockOpen = source.DockOpen
}

func (a *appState) applyAppearanceNow() {
	draft := cloneSettings(a.settingsDraft)
	dvrInputs := a.dvrInputs
	cleared := a.draftCleared
	next := cloneSettings(a.settings)
	copyAppearance(&next, draft)
	if err := a.applySettingsState(next); err != nil {
		a.settingsErr = err.Error()
		return
	}
	a.settingsDraft = draft
	a.dvrInputs = dvrInputs
	a.draftCleared = cleared
	a.finishSettingsSave(next, a.tr("appearance applied and saved immediately", "外观已立即应用并保存"))
}

func (a *appState) resetAppearanceNow() {
	draft := cloneSettings(a.settingsDraft)
	dvrInputs := a.dvrInputs
	cleared := a.draftCleared
	next := cloneSettings(a.settings)
	copyAppearance(&next, defaultSettings())
	if err := a.applySettingsState(next); err != nil {
		a.settingsErr = err.Error()
		return
	}
	copyAppearance(&draft, next)
	a.settingsDraft = draft
	a.dvrInputs = dvrInputs
	a.draftCleared = cleared
	a.finishSettingsSave(next, a.tr("appearance defaults applied and saved immediately", "外观默认值已立即应用并保存"))
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
