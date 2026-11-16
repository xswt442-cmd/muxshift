package main

// P1: the add-a-source chooser. The tests below cover the three intents, the
// two checks in front of them, and the promise that a website address never
// becomes a Core source.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTheChooserOpensOnNoChoiceAndForgetsTheLastNote(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.openAddSource()
	if !app.addOpen || app.addChoice != addChoiceNone {
		t.Fatalf("opened with open=%v choice=%v, want an open chooser on no choice", app.addOpen, app.addChoice)
	}
	app.addChoice = addChoiceSite
	app.addNote, app.addNoteBad = "the last attempt said something", true
	app.openAddSource()
	if app.addNote != "" || app.addNoteBad {
		t.Fatalf("reopening kept the last note %q (bad=%v)", app.addNote, app.addNoteBad)
	}
	app.closeAddSource()
	if app.addOpen || app.addChoice != addChoiceNone || app.addNote != "" {
		t.Fatalf("close left open=%v choice=%v note=%q", app.addOpen, app.addChoice, app.addNote)
	}
}

func TestWebsiteAddressesAreCheckedBeforeOpening(t *testing.T) {
	cases := []struct {
		raw  string
		want string // empty means the address is accepted
	}{
		{"", "type the address"},
		{"   ", "type the address"},
		{"javascript:alert(1)", "not an http or https address"},
		{"file:///C:/media/a.mp4", "not an http or https address"},
		{"data:text/html,hi", "not an http or https address"},
		{"example.com/watch", "not an http or https address"},
		{"https://user:secret@example.com/watch", "credentials"},
		{"https://example.com/a b", "space"},
		{"https://", "names no host"},
		{"https://example.com/watch?v=1", ""},
		{"http://127.0.0.1:8080/live", ""},
	}
	for _, c := range cases {
		err := siteAddressProblem(c.raw)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%q was refused: %v", c.raw, err)
		case c.want != "" && err == nil:
			t.Errorf("%q was accepted, want an error mentioning %q", c.raw, c.want)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%q was refused with %q, want a message mentioning %q", c.raw, err, c.want)
		}
	}
	if err := siteAddressProblem("https://user:secret@example.com/watch"); err != nil && strings.Contains(err.Error(), "secret") {
		t.Errorf("the refusal repeats the password: %v", err)
	}
}

func TestMediaAddressesAreCheckedBeforeReading(t *testing.T) {
	local := filepath.Join(t.TempDir(), "clip.mp4")
	cases := []struct {
		raw  string
		want string
	}{
		{"", "type a media address"},
		{"   ", "type a media address"},
		{local, ""},
		{"https://example.com/a.mp4?sig=abc", ""},
		{"ftp://example.com/a.mp4", "scheme"},
		{"file:///C:/media/a.mp4", "scheme"},
		{"javascript:alert(1)", "scheme"},
		{"media/a.mp4", "relative path"},
		{"https://user:secret@example.com/a.mp4", "credentials"},
		{"https://", "names no host"},
	}
	for _, c := range cases {
		err := mediaAddressProblem(c.raw)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%q was refused: %v", c.raw, err)
		case c.want != "" && err == nil:
			t.Errorf("%q was accepted, want an error mentioning %q", c.raw, c.want)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%q was refused with %q, want a message mentioning %q", c.raw, err, c.want)
		}
	}
}

func TestAWebsiteDoesNotBecomeASource(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.openAddSource()
	app.addChoice = addChoiceSite
	app.addSiteText = "https://example.com/watch?v=1"
	if _, ok := app.submitAddSource(); ok {
		t.Fatal("submitting a website reported a source")
	}
	if len(app.items) != 0 {
		t.Fatalf("the website added %d item(s), want none", len(app.items))
	}
	if app.session.Sources() != nil && len(app.session.Sources()) != 0 {
		t.Fatalf("the website added %d core source(s), want none", len(app.session.Sources()))
	}
	if !strings.Contains(app.addNote, "browser is not opened") {
		t.Fatalf("the note does not say the browser is not opened: %q", app.addNote)
	}
	if !app.addOpen {
		t.Fatal("the chooser closed after saying it could not open the browser")
	}
}

func TestABadWebsiteAddressIsRefusedBeforeTheNote(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.openAddSource()
	app.addChoice = addChoiceSite
	app.addSiteText = "javascript:alert(1)"
	if _, ok := app.submitAddSource(); ok {
		t.Fatal("a javascript: address reported a source")
	}
	if !app.addNoteBad || !strings.Contains(app.addNote, "not an http or https address") {
		t.Fatalf("note %q (bad=%v) does not explain the refusal", app.addNote, app.addNoteBad)
	}
	if len(app.items) != 0 || app.importing != "" {
		t.Fatalf("the refusal left items=%d importing=%q", len(app.items), app.importing)
	}
}

func TestChoosingAFileSaysThePickerIsNotHereYet(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.openAddSource()
	app.addChoice = addChoiceFile
	if _, ok := app.submitAddSource(); ok {
		t.Fatal("the file choice reported a source")
	}
	if len(app.items) != 0 {
		t.Fatalf("the file choice added %d item(s)", len(app.items))
	}
	if !strings.Contains(app.addNote, "file picker") {
		t.Fatalf("the note does not name the missing picker: %q", app.addNote)
	}
}

func TestAnEmptyMediaAddressNeverStartsARead(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.openAddSource()
	app.addChoice = addChoiceMedia
	app.importText = "   "
	if _, ok := app.submitAddSource(); ok {
		t.Fatal("an empty address started a read")
	}
	if app.importing != "" || len(app.items) != 0 {
		t.Fatalf("the refusal left importing=%q items=%d", app.importing, len(app.items))
	}
	if !strings.Contains(app.addNote, "type a media address") {
		t.Fatalf("note %q does not ask for an address", app.addNote)
	}
}

func TestAValidMediaAddressReachesTheAddCommand(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.requestFrame = func() {}
	app.openAddSource()
	app.addChoice = addChoiceMedia
	path := filepath.Join(t.TempDir(), "clip.mp4")
	app.importText = path
	result, ok := app.submitAddSource()
	if !ok {
		t.Fatalf("a valid path was refused: %v", result.Err)
	}
	if result.Command.Kind != commandAdd || result.Command.URI != path {
		t.Fatalf("the command was %+v, want an add of %q", result.Command, path)
	}
	if app.importing == "" {
		t.Fatal("the add command did not mark the read as in flight")
	}
	if app.addOpen {
		t.Fatal("the chooser stayed open after starting a read")
	}
	// Stop the read before the test ends; nothing was added either way.
	app.runCommand(command{Kind: commandCancelImport})
}
