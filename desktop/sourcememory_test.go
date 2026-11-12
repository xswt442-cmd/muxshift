package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

func TestHTTPIdentityKeepsThePathItWasGiven(t *testing.T) {
	// The path is not tidied: two addresses a server may resolve differently are
	// two identities, because a false match applies one source's level to
	// another source's sound.
	cases := []struct {
		raw, want string
	}{
		{"https://Example.COM/a/b.m3u8", "https://example.com/a/b.m3u8"},
		{"https://example.com:443/a/b.m3u8", "https://example.com/a/b.m3u8"},
		{"http://example.com:80/a/b.m3u8", "http://example.com/a/b.m3u8"},
		{"https://example.com/a//b.m3u8", "https://example.com/a//b.m3u8"},
		{"https://example.com/a%2Fb.m3u8", "https://example.com/a%2Fb.m3u8"},
		{"https://example.com", "https://example.com"},
	}
	for _, tc := range cases {
		got, err := normalizeSourceIdentity(tc.raw)
		if err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("%q became %q, want %q", tc.raw, got, tc.want)
		}
	}
	first, _ := normalizeSourceIdentity("https://example.com/a//b.m3u8")
	second, _ := normalizeSourceIdentity("https://example.com/a/b.m3u8")
	if first == second {
		t.Fatalf("two different paths share one identity: %q", first)
	}
}

func TestIdentityRefusesWhatMayCarryASecret(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://user:secret@example.com/a.m3u8", "credentials"},
		{"https://example.com/a.m3u8?token=abc", "query"},
		{"https://example.com/a.m3u8?", "query"},
		{"https://example.com/a.m3u8#fragment", "fragment"},
		{"rtmp://example.com/a", "not remembered"},
		{"file:///C:/media/a.mp4", "file: addresses are not remembered"},
		{"relative/a.mp4", "relative"},
		{"", "empty"},
	}
	for _, tc := range cases {
		_, err := normalizeSourceIdentity(tc.raw)
		if err == nil {
			t.Fatalf("%q was accepted", tc.raw)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q refused with %q, want it to mention %q", tc.raw, err, tc.want)
		}
	}
	if _, err := normalizeSourceIdentity("https://user:secret@example.com/a.m3u8"); !errors.Is(err, core.ErrCredentialsInURI) {
		t.Fatalf("credentials were refused without core.ErrCredentialsInURI: %v", err)
	}
}

func TestLocalIdentityIsAbsoluteAndCaseFolded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Mixed.Case.mp4")
	got, err := normalizeSourceIdentity(path)
	if err != nil {
		t.Fatalf("absolute path refused: %v", err)
	}
	again, err := normalizeSourceIdentity(strings.ToLower(path))
	if err != nil {
		t.Fatalf("lower case path refused: %v", err)
	}
	if got != again {
		// On Windows a path's case is spelling; elsewhere the two files really
		// differ, and two identities are the honest answer.
		if os.PathSeparator == '\\' {
			t.Fatalf("%q and %q are one file but two identities", got, again)
		}
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("%q is not absolute", got)
	}
}

func TestRememberedEntriesKeepTheNewestFirst(t *testing.T) {
	var memory sourceMemory
	for i := 0; i < maxRememberedSources; i++ {
		identity := "https://example.com/" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".m3u8"
		if _, refusal := memory.set(identity, 0.5, false); refusal != "" {
			t.Fatalf("entry %d refused: %s", i, refusal)
		}
	}
	if len(memory.Entries) != maxRememberedSources {
		t.Fatalf("kept %d entries, want %d", len(memory.Entries), maxRememberedSources)
	}
	changed, refusal := memory.set("https://example.com/new.m3u8", 0.25, true)
	if changed || refusal == "" {
		t.Fatalf("a 51st address was accepted (changed=%v, refusal=%q)", changed, refusal)
	}
	// Updating an address already remembered is allowed and moves it to the front.
	oldest := memory.Entries[len(memory.Entries)-1].Address
	changed, refusal = memory.set(oldest, 0.75, true)
	if !changed || refusal != "" {
		t.Fatalf("updating a remembered address failed (changed=%v, refusal=%q)", changed, refusal)
	}
	if memory.Entries[0].Address != oldest || memory.Entries[0].Volume != 0.75 || !memory.Entries[0].Muted {
		t.Fatalf("the updated entry is not first or not updated: %+v", memory.Entries[0])
	}
	if again, _ := memory.set(oldest, 0.75, true); again {
		t.Fatalf("writing the same level again reported a change")
	}
	if forgotten := memory.clear(); forgotten != maxRememberedSources {
		t.Fatalf("cleared %d entries, want %d", forgotten, maxRememberedSources)
	}
}

func TestSourceMemoryRoundTripsThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	want := defaultSettings()
	want.SourceMemory = sourceMemory{
		On:       true,
		Remember: true,
		Entries: []sourceMemoryEntry{
			{Address: "https://example.com/a.m3u8", Volume: 0.25, Muted: true},
			{Address: "c:\\media\\local.mp4", Volume: 1, Muted: false},
		},
	}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, note := loadSettings(path)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if !sameSettings(got, want) {
		t.Fatalf("loaded %s, want %s", settingsSummary(got), settingsSummary(want))
	}

	// A draft and the applied settings must not share the entries array: editing
	// one must not edit the other.
	draft := cloneSettings(got)
	draft.SourceMemory.Entries[0].Volume = 0.9
	if got.SourceMemory.Entries[0].Volume != 0.25 {
		t.Fatalf("the clone shares its entries with the original")
	}
	if sameSettings(draft, got) {
		t.Fatalf("two settings with different entries compare equal")
	}
}

func TestSettingsSummaryDoesNotPrintAddresses(t *testing.T) {
	st := defaultSettings()
	st.SourceMemory = sourceMemory{On: true, Entries: []sourceMemoryEntry{
		{Address: "https://example.com/secret-name.m3u8", Volume: 0.4},
	}}
	line := settingsSummary(st)
	if strings.Contains(line, "secret-name") || strings.Contains(line, "example.com") {
		t.Fatalf("the summary prints the address: %q", line)
	}
	if !strings.Contains(line, identityDigest("https://example.com/secret-name.m3u8")) {
		t.Fatalf("the summary does not name the set: %q", line)
	}
}

func TestValidateSourceMemoryRefusesWhatItCannotHonour(t *testing.T) {
	cases := []struct {
		name   string
		memory sourceMemory
		want   string
	}{
		{"unnormalised", sourceMemory{Entries: []sourceMemoryEntry{{Address: "HTTPS://Example.com/a.m3u8", Volume: 0.5}}}, "normalised"},
		{"duplicate", sourceMemory{Entries: []sourceMemoryEntry{
			{Address: "https://example.com/a.m3u8", Volume: 0.5},
			{Address: "https://example.com/a.m3u8", Volume: 0.6},
		}}, "repeats"},
		{"level", sourceMemory{Entries: []sourceMemoryEntry{{Address: "https://example.com/a.m3u8", Volume: 1.5}}}, "level"},
		{"secret", sourceMemory{Entries: []sourceMemoryEntry{{Address: "https://example.com/a.m3u8?k=v", Volume: 0.5}}}, "query"},
	}
	for _, tc := range cases {
		err := validateSourceMemory(tc.memory)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	over := defaultSettings()
	for i := 0; i <= maxRememberedSources; i++ {
		over.SourceMemory.Entries = append(over.SourceMemory.Entries, sourceMemoryEntry{
			Address: "https://example.com/" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + ".m3u8",
			Volume:  0.5,
		})
	}
	if err := validateSourceMemory(over.SourceMemory); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("an over-long list was accepted: %v", err)
	}
}

func TestApplySourceMemoryInstallsTheRememberedSound(t *testing.T) {
	app := &appState{settings: defaultSettings()}
	app.settings.SourceMemory = sourceMemory{On: true, Entries: []sourceMemoryEntry{
		{Address: "https://example.com/a.m3u8", Volume: 0.25, Muted: true},
	}}
	it := &item{uri: "https://example.com/a.m3u8", volume: 1}
	app.applySourceMemory(it)
	if it.volume != 0.25 || !it.muted {
		t.Fatalf("remembered sound not installed: volume %.2f muted %v", it.volume, it.muted)
	}
	// With the switch off nothing is applied, and the entry stays in the file.
	app.settings.SourceMemory.On = false
	off := &item{uri: "https://example.com/a.m3u8", volume: 1}
	app.applySourceMemory(off)
	if off.volume != 1 || off.muted {
		t.Fatalf("memory off still changed the source: volume %.2f muted %v", off.volume, off.muted)
	}
	// An address that is not remembered keeps the default.
	app.settings.SourceMemory.On = true
	other := &item{uri: "https://example.com/b.m3u8", volume: 1}
	app.applySourceMemory(other)
	if other.volume != 1 {
		t.Fatalf("an unknown address got a level: %.2f", other.volume)
	}
}

func TestRememberSourceWritesOnlyForAHandAddedSource(t *testing.T) {
	dir := t.TempDir()
	app := &appState{settings: defaultSettings(), settingsPath: filepath.Join(dir, "settings.json")}
	app.settings.SourceMemory = sourceMemory{On: true, Remember: true}
	app.settingsTrusted = true

	fromCommandLine := &item{uri: "https://example.com/a.m3u8", volume: 0.5, origin: originCommandLine}
	app.rememberSource(fromCommandLine)
	if _, err := os.Stat(app.settingsPath); !os.IsNotExist(err) {
		t.Fatalf("a command line source wrote the settings file")
	}

	added := &item{uri: "https://example.com/a.m3u8", volume: 0.5, origin: originAdded}
	app.rememberSource(added)
	got, note := loadSettings(app.settingsPath)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if len(got.SourceMemory.Entries) != 1 || got.SourceMemory.Entries[0].Volume != 0.5 {
		t.Fatalf("the entry was not written: %+v", got.SourceMemory.Entries)
	}
	if got.SourceMemory.Entries[0].Address != "https://example.com/a.m3u8" {
		t.Fatalf("the entry is not the normalised address: %q", got.SourceMemory.Entries[0].Address)
	}
	// Remembering a change keeps the draft alone: the applied settings own the
	// copy that gets saved.
	app.settingsDraft.SourceMemory.Entries = nil
	app.rememberSource(added)
	if len(app.settingsDraft.SourceMemory.Entries) != 0 {
		t.Fatalf("remembering rewrote the draft")
	}
}

func TestMemoryOffNeverWrites(t *testing.T) {
	dir := t.TempDir()
	app := &appState{settings: defaultSettings(), settingsPath: filepath.Join(dir, "settings.json")}
	// Remember is on while On is off: nothing is written, which is what the page
	// says out loud.
	app.settings.SourceMemory = sourceMemory{On: false, Remember: true}
	app.rememberSource(&item{uri: "https://example.com/a.m3u8", volume: 0.5})
	if _, err := os.Stat(app.settingsPath); !os.IsNotExist(err) {
		t.Fatalf("memory off wrote the settings file")
	}
	if len(app.settings.SourceMemory.Entries) != 0 {
		t.Fatalf("memory off added an entry in memory")
	}
}

func TestMemoryEntriesSurviveAMissingBlock(t *testing.T) {
	// A file from before P18.2-b has no sourceMemory key; it must load with
	// memory off rather than fail validation.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := `{"startPage":"stage","defaultVolume":0.5,"nudgeStep":0.1,"muteUnaligned":true,"masterVolume":1,"dockOpen":true,"compact":false,"dvr":{"retentionSeconds":60,"maxBytes":0,"recordingBytes":268435456}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, note := loadSettings(path)
	if note != "" {
		t.Fatalf("an old file was reported: %q", note)
	}
	if got.SourceMemory.On || got.SourceMemory.Remember || len(got.SourceMemory.Entries) != 0 {
		t.Fatalf("an old file turned memory on: %+v", got.SourceMemory)
	}
}

// The digest names an identity in a log line; the test pins that it is stable,
// so evidence written today still matches the code later.
func TestIdentityDigestIsStable(t *testing.T) {
	first := identityDigest("https://example.com/a.m3u8")
	if len(first) != 8 {
		t.Fatalf("digest %q is not 8 characters", first)
	}
	if first != identityDigest("https://example.com/a.m3u8") {
		t.Fatalf("the digest changed between calls")
	}
	if first == identityDigest("https://example.com/b.m3u8") {
		t.Fatalf("two addresses share a digest")
	}
	_ = time.Now
}
