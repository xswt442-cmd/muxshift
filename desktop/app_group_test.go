package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests were grouped from addsource_test.go, background_test.go, controller_test.go, handoff_extra_test.go, handoff_test.go, import_frame_test.go, import_rollback_test.go, live_test.go, mediakind_test.go, player_test.go, probe_cancel_test.go, redact_test.go, settings_dvr_test.go, settings_test.go, shortcut_test.go, sourcememory_test.go.

var errPicked = errors.New("the dialog could not open")

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
		{"javascript:alert(1)", "only http and https addresses can be opened"},
		{"file:///C:/media/a.mp4", "only http and https addresses can be opened"},
		{"data:text/html,hi", "only http and https addresses can be opened"},
		{"example.com/watch", "only http and https addresses can be opened"},
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
		{"ftp://example.com/a.mp4", "http and https"},
		{"file:///C:/media/a.mp4", "http and https"},
		{"javascript:alert(1)", "http and https"},
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

func TestAWebsiteIsOpenedAndDoesNotBecomeASource(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	var opened []string
	app.openURL = func(address string, done func(error)) { opened = append(opened, address) }
	app.openAddSource()
	app.addChoice = addChoiceSite
	app.addSiteText = "https://example.com/watch?v=1"
	result, ok := app.submitAddSource()
	if !ok {
		t.Fatalf("submitting a website failed: %v", result.Err)
	}
	if len(opened) != 1 || opened[0] != "https://example.com/watch?v=1" {
		t.Fatalf("the browser was asked for %v, want the one address", opened)
	}
	if !app.addOpen {
		t.Fatal("the chooser closed while the media was still in the browser")
	}
	if !strings.Contains(app.addNote, "opened https://example.com/watch") ||
		strings.Contains(app.addNote, "?v=1") ||
		!strings.Contains(app.addNote, "can hand one media address back") {
		t.Fatalf("the note does not say where the media still is: %q", app.addNote)
	}
	if app.addNoteBad {
		t.Fatal("the opened page reads as a refusal")
	}
	if got := app.addSubmitLabel(); got != "Open again" {
		t.Fatalf("the button says %q after the page opened, want Open again", got)
	}
	if len(app.items) != 0 {
		t.Fatalf("the website added %d item(s), want none", len(app.items))
	}
	if app.session.Sources() != nil && len(app.session.Sources()) != 0 {
		t.Fatalf("the website added %d core source(s), want none", len(app.session.Sources()))
	}
	if !strings.Contains(result.Message, "opened https://example.com/watch") ||
		strings.Contains(result.Message, "?v=1") ||
		!strings.Contains(result.Message, "can hand one media address back") {
		t.Fatalf("the message does not say where the media still is: %q", result.Message)
	}
}

func TestAWebsiteWithoutABrowserIsRefused(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.openAddSource()
	app.addChoice = addChoiceSite
	app.addSiteText = "https://example.com/watch?v=1"
	if _, ok := app.submitAddSource(); ok {
		t.Fatal("a website without a browser reported success")
	}
	if !app.addNoteBad || !strings.Contains(app.addNote, "no system browser") {
		t.Fatalf("the note does not say there is no browser: %q (bad=%v)", app.addNote, app.addNoteBad)
	}
	if !app.addOpen {
		t.Fatal("the chooser closed after saying it could not open the browser")
	}
	if len(app.items) != 0 {
		t.Fatalf("the refused website added %d item(s)", len(app.items))
	}
}

func TestABrowserThatFailsIsReported(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.requestFrame = func() {}
	app.openURL = func(address string, done func(error)) {
		done(errors.New("no application is registered for this address"))
	}
	app.openAddSource()
	app.addChoice = addChoiceSite
	app.addSiteText = "https://example.com/watch?v=1"
	if _, ok := app.submitAddSource(); !ok {
		t.Fatal("a browser failure should still be recorded as the attempt's outcome")
	}
	if app.lastErr == nil || !strings.Contains(app.lastErr.Error(), "opening https://example.com/watch failed") {
		t.Fatalf("the browser failure was not reported: %v", app.lastErr)
	}
	if len(app.items) != 0 {
		t.Fatalf("the failed browser added %d item(s)", len(app.items))
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
	if !app.addNoteBad || !strings.Contains(app.addNote, "only http and https addresses can be opened") {
		t.Fatalf("note %q (bad=%v) does not explain the refusal", app.addNote, app.addNoteBad)
	}
	if len(app.items) != 0 || app.importing != "" {
		t.Fatalf("the refusal left items=%d importing=%q", len(app.items), app.importing)
	}
}

func TestAFileChoiceOpensThePickerAndAddsItsAnswer(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.requestFrame = func() {}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	released := make(chan struct{})
	done := make(chan struct{})
	app.pickFile = func() (string, error) {
		<-released
		return path, nil
	}
	app.onMain = func(f func()) {
		f()
		close(done)
	}
	app.openAddSource()
	app.addChoice = addChoiceFile
	if _, ok := app.submitAddSource(); !ok {
		t.Fatal("the file choice did not start the picker")
	}
	if !app.pickingFile {
		t.Fatal("the picker did not mark itself as open")
	}
	if app.addOpen {
		t.Fatal("the chooser stayed open while the picker ran")
	}
	close(released)
	<-done
	if app.pickingFile {
		t.Fatal("the picker stayed marked as open after its answer")
	}
	if app.importing == "" {
		t.Fatal("the picked path did not reach the add command")
	}
	app.runCommand(command{Kind: commandCancelImport})
}

func TestACancelledPickerTouchesNothing(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.requestFrame = func() {}
	app.acceptPickedFile("", nil)
	if len(app.items) != 0 || app.importing != "" || app.lastErr != nil {
		t.Fatalf("a cancelled picker left items=%d importing=%q lastErr=%v", len(app.items), app.importing, app.lastErr)
	}
	if app.pickingFile {
		t.Fatal("the cancelled picker stayed marked as open")
	}
}

func TestAPickerErrorIsReportedAndAddsNothing(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.requestFrame = func() {}
	app.acceptPickedFile("", errPicked)
	if len(app.items) != 0 {
		t.Fatalf("a failed picker added %d item(s)", len(app.items))
	}
	if app.lastErr == nil || !strings.Contains(app.lastErr.Error(), "choosing a file failed") {
		t.Fatalf("the failure is not reported: %v", app.lastErr)
	}
	if !strings.Contains(app.lastErr.Error(), "the dialog could not open") {
		t.Fatalf("the reported failure loses the reason: %v", app.lastErr)
	}
}

func TestASecondFileChoiceWhileOneIsOpenIsRefused(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	called := false
	app.pickFile = func() (string, error) { called = true; return "", nil }
	app.pickingFile = true
	app.openAddSource()
	app.addChoice = addChoiceFile
	if _, ok := app.submitAddSource(); ok {
		t.Fatal("a second picker was started")
	}
	if called {
		t.Fatal("the second choice reached the picker")
	}
	if !strings.Contains(app.addNote, "already open") {
		t.Fatalf("the note does not explain the open picker: %q", app.addNote)
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

func TestALocalPathMustNameAFile(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "gone.mp4")
	if err := app.addLocal(&item{uri: missing}); err == nil {
		t.Fatal("a missing file was accepted")
	}
	dir := t.TempDir()
	if err := app.addLocal(&item{uri: dir}); err == nil {
		t.Fatal("a directory was accepted")
	}
	if sources := app.session.Sources(); len(sources) != 0 {
		t.Fatalf("the refusals left %d source(s)", len(sources))
	}
}

func TestBackgroundFailuresAreCountedAndDismissKeepsThem(t *testing.T) {
	a := &appState{}

	if count, last := a.backgroundFailures(); count != 0 || last.what != "" {
		t.Fatalf("a fresh run has %d failure(s), newest %q", count, last.what)
	}
	if line := (backgroundFailure{}).line(); !strings.Contains(line, "nothing has failed") {
		t.Fatalf("an empty ledger reads %q", line)
	}

	a.noteBackgroundFailure("saving a source's remembered sound", errors.New("rename settings.json: Access is denied."))
	a.noteBackgroundFailure("measuring a recording's anchor", errors.New("ffprobe is not on PATH"))

	count, last := a.backgroundFailures()
	if count != 2 {
		t.Fatalf("counted %d failure(s), want 2", count)
	}
	if !strings.Contains(last.line(), "measuring a recording's anchor") {
		t.Fatalf("the newest failure reads %q", last.line())
	}
	if !strings.Contains(last.line(), "ffprobe is not on PATH") {
		t.Fatalf("the newest failure does not carry its error: %q", last.line())
	}

	a.lastErr = errors.New("the newest message")
	a.dismissError()
	if a.lastErr != nil {
		t.Fatalf("dismiss left the message in place: %v", a.lastErr)
	}
	if count, _ = a.backgroundFailures(); count != 2 {
		t.Fatalf("dismissing the message changed the count to %d, want 2", count)
	}
}

func TestASuccessfulWriteCountsNothing(t *testing.T) {
	a := &appState{}
	a.noteBackgroundFailure("saving a source's remembered sound", nil)
	if count, _ := a.backgroundFailures(); count != 0 {
		t.Fatalf("a nil error counted %d failure(s)", count)
	}
	a.dismissError() // nothing to dismiss
	if a.lastErr != nil {
		t.Fatalf("dismissing nothing invented an error: %v", a.lastErr)
	}
}

func bareState(t *testing.T) (*appState, *item) {
	t.Helper()
	session := core.NewSession("test")
	scene, err := session.AddScene("main")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := session.AddOutput(core.OutputDesktopWindow, scene.ID); err != nil {
		t.Fatalf("add output: %v", err)
	}
	source, err := session.AddSource("http://127.0.0.1:1/video.m3u8")
	if err != nil {
		t.Fatalf("add source: %v", err)
	}
	video, err := session.AddTrack(source.ID, core.MediaVideo, "video")
	if err != nil {
		t.Fatalf("add video track: %v", err)
	}
	audio, err := session.AddTrack(source.ID, core.MediaAudio, "audio")
	if err != nil {
		t.Fatalf("add audio track: %v", err)
	}
	it := &item{
		uri:    source.URI,
		source: source,
		video:  video.ID,
		audio:  audio.ID,
		volume: 1,
	}
	a := &appState{
		session: session,
		scene:   scene.ID,
		items:   []*item{it},
		audible: make(map[core.TrackID]bool),
		sync:    newSyncState(),
	}
	return a, it
}

func TestRunCommandRejectsAnUnknownSource(t *testing.T) {
	a, _ := bareState(t)
	result := a.runCommand(command{Kind: commandShow, Source: "source-99"})

	if !errors.Is(result.Err, UnknownSourceError) {
		t.Fatalf("error = %v, want UnknownSourceError", result.Err)
	}
	if result.Command.Kind != commandShow || result.Command.Source != "source-99" {
		t.Fatalf("result kept the wrong command: %+v", result.Command)
	}
	if a.lastCommand.Err == nil {
		t.Fatal("the failed command was not recorded on the app state")
	}
	if got := result.String(); !strings.Contains(got, "failed") || !strings.Contains(got, "source-99") {
		t.Fatalf("String() = %q, want a failed line naming the source", got)
	}
}

func TestRunCommandReportsACommandWithoutAPlayer(t *testing.T) {
	a, it := bareState(t)
	result := a.runCommand(command{Kind: commandReload, Source: it.source.ID})

	if result.Err == nil {
		t.Fatal("reloading a source without a player should fail")
	}
	if !strings.Contains(result.Err.Error(), "no player") {
		t.Fatalf("error = %v, want it to say there is no player", result.Err)
	}
	if result.Generation != it.generation {
		t.Fatalf("generation = %d, want %d", result.Generation, it.generation)
	}
}

func TestSnapshotCopiesTheSceneWithoutTouchingPlayers(t *testing.T) {
	a, it := bareState(t)
	if _, err := a.session.SetSceneVideo(a.scene, it.video); err != nil {
		t.Fatalf("set scene video: %v", err)
	}
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, 0.5, true); err != nil {
		t.Fatalf("set scene audio: %v", err)
	}

	snapshot := a.snapshot()

	if len(snapshot.Picture) != 1 || snapshot.Picture[0] != it.video {
		t.Fatalf("picture = %v, want %q", snapshot.Picture, it.video)
	}
	if len(snapshot.Sound) != 1 {
		t.Fatalf("sound = %v, want one track", snapshot.Sound)
	}
	if got := snapshot.Sound[0]; got.Track != it.audio || got.Volume != 0.5 || !got.Muted {
		t.Fatalf("sound = %+v, want the scene's track at 0.5 and muted", got)
	}
	if got := snapshot.Sound[0].Source; got != it.source.ID {
		t.Fatalf("sound source = %q, want %q", got, it.source.ID)
	}
	if len(snapshot.Sources) != 1 {
		t.Fatalf("sources = %v, want one", snapshot.Sources)
	}
	entry := snapshot.Sources[0]
	if entry.Player {
		t.Fatal("the snapshot claims a player that was never opened")
	}
	if entry.ID != it.source.ID || entry.URI != it.uri || entry.Video != it.video || entry.Audio != it.audio {
		t.Fatalf("source entry = %+v, want the item's identifiers", entry)
	}
	if entry.Shown {
		t.Fatal("a source that is not the picture is reported as shown")
	}
	if snapshot.Group != "" {
		t.Fatalf("group = %q, want none before a sync command", snapshot.Group)
	}
	if snapshot.LastCommand != "none yet" {
		t.Fatalf("last command = %q, want the empty marker", snapshot.LastCommand)
	}
}

func TestSnapshotReflectsTheLastCommand(t *testing.T) {
	a, it := bareState(t)
	a.report(a.runCommand(command{Kind: commandReload, Source: it.source.ID}))

	snapshot := a.snapshot()
	if !strings.Contains(snapshot.LastCommand, "failed") || !strings.Contains(snapshot.LastCommand, string(it.source.ID)) {
		t.Fatalf("last command = %q, want the failed reload", snapshot.LastCommand)
	}
}

func TestCommandResultString(t *testing.T) {
	ok := commandResult{
		Command:    command{Kind: commandVolume, Source: "source-1"},
		Generation: 3,
		Message:    "set to 0.5",
	}
	if got := ok.String(); got != "volume source=source-1 generation=3 ok · set to 0.5" {
		t.Fatalf("ok line = %q", got)
	}
	failed := commandResult{
		Command:    command{Kind: commandNudge, Source: "source-2"},
		Generation: 0,
		Err:        core.ErrNotAligned,
	}
	if got := failed.String(); !strings.HasPrefix(got, "nudge source=source-2 generation=0 failed: ") {
		t.Fatalf("failed line = %q", got)
	}
}

func request(server string, method, path, body, contentType string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, server+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Host = strings.TrimPrefix(server, "http://")
	for name, value := range header {
		req.Header.Set(name, value)
	}
	return http.DefaultClient.Do(req)
}

// A code is consumed once, but the token it handed out keeps working: a browser
// that paired yesterday does not lose the window because the page shows a new
// code today.
func TestAPairedTokenSurvivesANewCode(t *testing.T) {
	h, server, added := newTestHandoff(t)

	resp := postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pairing answered %d, want 200", resp.StatusCode)
	}
	var paired struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("read the pairing answer: %v", err)
	}

	h.rotate()

	resp = postJSON(t, server, "/import", `{"url":"https://example.com/after-rotate.m3u8"}`,
		map[string]string{"X-MuxShift-Handoff": paired.Token})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("an import with the earlier token answered %d, want 202", resp.StatusCode)
	}
	if len(*added) != 1 || (*added)[0] != "https://example.com/after-rotate.m3u8" {
		t.Fatalf("the add command saw %v, want the address handed over after a new code", *added)
	}
}

// Two browsers can race for the same code. Exactly one wins; the other is told
// the code is no longer valid instead of silently sharing the token.
func TestOneCodePairsOnlyOnceWhenBrowsersRace(t *testing.T) {
	h, _, _ := newTestHandoff(t)
	server := newLoopbackServer(t, h)

	const callers = 8
	codes := make(chan int, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := request(server, http.MethodPost, "/pair", `{"code":"`+h.code+`"}`, "application/json", nil)
			if err != nil {
				codes <- -1
				return
			}
			defer resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)

	paired, refused := 0, 0
	for code := range codes {
		switch code {
		case http.StatusOK:
			paired++
		case http.StatusForbidden:
			refused++
		default:
			t.Fatalf("a racing pair answered %d, want 200 or 403", code)
		}
	}
	if paired != 1 || refused != callers-1 {
		t.Fatalf("the race paired %d and refused %d, want exactly one pairing", paired, refused)
	}
}

// Each endpoint answers only the method it serves.
func TestEachEndpointRefusesTheMethodItDoesNotServe(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/pair", ""},
		{http.MethodGet, "/import", ""},
		{http.MethodPost, "/health", `{}`},
	}
	for _, tc := range cases {
		resp, err := request(server.URL, tc.method, tc.path, tc.body, "application/json", map[string]string{"X-MuxShift-Handoff": h.secret})
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s answered %d, want 405", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// An empty or missing address never reaches the add command.
func TestAnImportWithoutAnAddressIsRefused(t *testing.T) {
	h, server, added := newTestHandoff(t)
	for _, body := range []string{`{}`, `{"url":""}`, `{"url":"   "}`, `{"url":"\t"}`} {
		resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s answered %d, want 400", body, resp.StatusCode)
		}
	}
	if len(*added) != 0 {
		t.Fatalf("the add command saw %v, want nothing", *added)
	}
}

// What the add command says goes back to the browser: a refusal with its reason,
// or the accepted message the popup shows.
func TestAnImportTellsWhatTheCommandDid(t *testing.T) {
	h := &handoffServer{
		secret: strings.Repeat("test-token-", 4),
		add: func(string) commandResult {
			return commandResult{Err: errors.New("the player could not open it")}
		},
	}
	h.rotate()
	server := httptest.NewServer(h.routes())
	h.addr = server.Listener.Addr().String()
	t.Cleanup(server.Close)

	resp := postJSON(t, server, "/import", `{"url":"https://example.com/broken.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a refused add answered %d, want 400", resp.StatusCode)
	}
	var refused struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refused); err != nil {
		t.Fatalf("read the refusal: %v", err)
	}
	if refused.Accepted || !strings.Contains(refused.Reason, "could not open") {
		t.Fatalf("the refusal said %+v, want accepted=false and the command's reason", refused)
	}
}

// A control character in the address is refused before it can reach the command.
func TestAnImportRefusesAControlCharacterInTheAddress(t *testing.T) {
	h, server, added := newTestHandoff(t)
	resp := postJSON(t, server, "/import", `{"url":"https://example.com/a\nb.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an address with a newline answered %d, want 400", resp.StatusCode)
	}
	if len(*added) != 0 {
		t.Fatalf("the add command saw %v, want nothing", *added)
	}
}

// The body has to say it is JSON; otherwise even a correct code is not read.
func TestTheBodyMustSayItIsJSON(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	resp, err := request(server.URL, http.MethodPost, "/pair", `{"code":"`+h.code+`"}`, "text/plain", nil)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a text/plain body answered %d, want 400", resp.StatusCode)
	}
}

// The port is checked before anything is listed, so a bad number fails loudly.
func TestStartHandoffRefusesAPortOutsideTheRange(t *testing.T) {
	for _, port := range []int{-1, 65536, 70000} {
		if _, err := startHandoff(&appState{}, port); err == nil {
			t.Fatalf("port %d was accepted", port)
		}
	}
}

// Closing the listener really closes it: the port stops answering, which is what
// makes a second window's refusal honest.
func TestStoppingTheListenerReleasesThePort(t *testing.T) {
	h, err := startHandoff(&appState{}, 0)
	if err != nil {
		t.Fatalf("start the listener: %v", err)
	}
	resp, err := http.Get("http://" + h.addr + "/health")
	if err != nil {
		t.Fatalf("health before stopping: %v", err)
	}
	resp.Body.Close()

	h.stop()

	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", h.addr, 200*time.Millisecond)
		if dialErr != nil {
			return
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("%s still accepts connections after stop", h.addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newLoopbackServer serves the same handler in front of an address the requests
// can be stamped with, for tests that need their own client and goroutines.
func newLoopbackServer(t *testing.T, h *handoffServer) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h.addr = listener.Addr().String()
	server := &http.Server{Handler: h.routes()}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return "http://" + h.addr
}

func newTestHandoff(t *testing.T) (*handoffServer, *httptest.Server, *[]string) {
	t.Helper()
	added := &[]string{}
	h := &handoffServer{
		secret: strings.Repeat("test-token-", 4),
		add: func(address string) commandResult {
			*added = append(*added, address)
			return commandResult{Message: "reading " + address}
		},
	}
	h.rotate()
	server := httptest.NewServer(h.routes())
	h.addr = server.Listener.Addr().String()
	t.Cleanup(server.Close)
	return h, server, added
}

func postJSON(t *testing.T, server *httptest.Server, path, body string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = strings.TrimPrefix(server.URL, "http://")
	for name, value := range header {
		req.Header.Set(name, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestThePairingCodeIsTheOneTheWindowShows(t *testing.T) {
	h, server, _ := newTestHandoff(t)

	resp := postJSON(t, server, "/pair", `{"code":"000000"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong code answered %d, want 403", resp.StatusCode)
	}

	resp = postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the shown code answered %d, want 200", resp.StatusCode)
	}
	var paired struct {
		Token   string `json:"token"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&paired); err != nil {
		t.Fatalf("read the pairing answer: %v", err)
	}
	if !strings.HasPrefix(paired.Address, "http://") {
		t.Fatalf("the pairing answer gave %q, which a browser cannot fetch without a scheme", paired.Address)
	}
	if paired.Token != h.secret {
		t.Fatalf("the pairing answer carried a token that is not this window's")
	}

	// One pairing per code: the window shows a new one for the next browser.
	resp = postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a code that was just used answered %d, want 403", resp.StatusCode)
	}
}

func TestAnExpiredPairingCodeIsRefused(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	h.mu.Lock()
	h.expiry = time.Now().Add(-time.Second)
	h.mu.Unlock()

	resp := postJSON(t, server, "/pair", `{"code":"`+h.code+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an expired code answered %d, want 403", resp.StatusCode)
	}
}

func TestANewCodeReplacesTheOldOne(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	old := h.code
	for h.rotate() == old {
		// A collision is possible; make another.
	}

	resp := postJSON(t, server, "/pair", `{"code":"`+old+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the old code answered %d, want 403", resp.StatusCode)
	}
}

func TestAnImportNeedsThePairedToken(t *testing.T) {
	h, server, added := newTestHandoff(t)
	body := `{"url":"https://example.com/live.m3u8"}`

	if resp := postJSON(t, server, "/import", body, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an import without a token answered %d, want 403", resp.StatusCode)
	}
	if resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": "nope"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an import with a wrong token answered %d, want 403", resp.StatusCode)
	}
	resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("a paired import answered %d, want 202", resp.StatusCode)
	}
	if len(*added) != 1 || (*added)[0] != "https://example.com/live.m3u8" {
		t.Fatalf("the add command saw %v, want the one address", *added)
	}
}

func TestOnlyTheExtensionOriginMayCallTheListener(t *testing.T) {
	h, server, _ := newTestHandoff(t)

	resp := postJSON(t, server, "/import", `{"url":"https://example.com/a.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret, "Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin answered %d, want 403", resp.StatusCode)
	}

	resp = postJSON(t, server, "/import", `{"url":"https://example.com/a.mp4"}`,
		map[string]string{"X-MuxShift-Handoff": h.secret, "Origin": "chrome-extension://agacmebmmcbkclbblmekgdkfcehehccl"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("the extension origin answered %d, want 202", resp.StatusCode)
	}
}

func TestTheListenerRefusesAnotherHostName(t *testing.T) {
	_, server, _ := newTestHandoff(t)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/health", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Host = "rebind.example"
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("send the request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign Host answered %d, want 403", resp.StatusCode)
	}
}

func TestAnImportRefusesWhatIsNotAnHTTPAddress(t *testing.T) {
	h, server, added := newTestHandoff(t)
	for _, body := range []string{
		`{"url":"C:\\media\\local.mp4"}`,
		`{"url":"file:///C:/media/local.mp4"}`,
		`{"url":"javascript:alert(1)"}`,
		`{"url":"https://user:secret@example.com/a.mp4"}`,
	} {
		resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s answered %d, want 400", body, resp.StatusCode)
		}
	}
	if len(*added) != 0 {
		t.Fatalf("the add command saw %v, want nothing", *added)
	}
}

func TestAnImportRefusesANonJSONOrOversizedBody(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	big := `{"url":"https://example.com/` + strings.Repeat("a", handoffBodyLimit) + `.mp4"}`
	for _, body := range []string{`not json`, big} {
		resp := postJSON(t, server, "/import", body, map[string]string{"X-MuxShift-Handoff": h.secret})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("a body of %d bytes answered %d, want 400", len(body), resp.StatusCode)
		}
	}
}

func TestTheHealthAnswerSaysNothingSecret(t *testing.T) {
	h, server, _ := newTestHandoff(t)
	resp, err := server.Client().Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health answered %d, want 200", resp.StatusCode)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") == false {
		t.Fatalf("health is not JSON: %q", resp.Header.Get("Content-Type"))
	}
	if h.secret == "" || h.code == "" {
		t.Fatal("the listener has no secret or code to test against")
	}
}

func TestTheCodeLineSaysWhereAndForHowLong(t *testing.T) {
	h, _, _ := newTestHandoff(t)
	line := h.codeLine()
	if !strings.Contains(line, h.addr) || !strings.Contains(line, h.code) || !strings.Contains(line, "expires") {
		t.Fatalf("the code line %q does not show the address, the code and the time left", line)
	}
	var none *handoffServer
	if got := none.codeLine(); !strings.Contains(got, "not listening") {
		t.Fatalf("a missing listener says %q", got)
	}
}

// The extension's page asks before it posts, because its request carries JSON and
// one extra header. That question is answered only for the extension's origin.
func TestTheExtensionPreflightIsAnsweredOnlyForItsOrigin(t *testing.T) {
	h, server, _ := newTestHandoff(t)

	preflight := func(origin string) *http.Response {
		req, err := http.NewRequest(http.MethodOptions, server.URL+"/import", nil)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		req.Host = strings.TrimPrefix(server.URL, "http://")
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type, x-muxshift-handoff")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("send the request: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	extension := "chrome-extension://agacmebmmcbkclbblmekgdkfcehehccl"
	resp := preflight(extension)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the extension's preflight answered %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != extension {
		t.Fatalf("the extension's preflight allowed %q, want its own origin", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-MuxShift-Handoff") {
		t.Fatalf("the preflight allows %q, which lacks the handoff header", got)
	}

	foreign := preflight("https://evil.example")
	if foreign.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign preflight answered %d, want 403", foreign.StatusCode)
	}
	if got := foreign.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("a foreign preflight was allowed %q", got)
	}

	// The answer to the real request carries the same allowance, or the browser
	// would discard it even though the window accepted the address.
	resp = postJSON(t, server, "/import", `{"url":"http://127.0.0.1:8802/movie.mp4"}`, map[string]string{
		"Origin":             extension,
		"X-MuxShift-Handoff": h.secret,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("the paired import answered %d, want 202", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != extension {
		t.Fatalf("the import answer allowed %q, want the extension's origin", got)
	}
}

func TestAFinishedImportAsksForAFrame(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("the app state was not built: %v", err)
	}
	frames := 0
	app.requestFrame = func() { frames++ }
	app.importing = "reading"
	missing := filepath.Join(t.TempDir(), "not-there.mp4")

	app.landImport(0, missing, hls.Stream{}, importLocal, false, errors.New("read playlist: connection refused"))

	if frames == 0 {
		t.Fatal("an import that failed left the page without a frame to draw")
	}
	if app.importing != "" {
		t.Fatalf("the import is still marked in flight as %q", app.importing)
	}
	if app.lastErr == nil {
		t.Fatal("the failed import left no error to show")
	}
	if got, want := app.lastCommand.Message, "could not import not-there.mp4"; got != want {
		t.Fatalf("the status line says %q, want %q", got, want)
	}
	app.lastCommand = commandResult{Command: command{Kind: commandMute}}
	if app.importFailureURI != missing || app.importFailureErr == nil {
		t.Fatalf("unrelated command erased the import failure: uri=%q err=%v", app.importFailureURI, app.importFailureErr)
	}
}

// TestACancelledReadDoesNotDrawTwice guards the early return: a landing from the
// read that Cancel stopped must not add a source or ask for a frame, because
// Cancel already cleared the busy state and said so.
func TestACancelledReadDoesNotDrawTwice(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("the app state was not built: %v", err)
	}
	frames := 0
	app.requestFrame = func() { frames++ }
	app.importToken = 3

	app.landImport(2, filepath.Join(t.TempDir(), "late.mp4"), hls.Stream{}, importLocal, false, nil)

	if frames != 0 {
		t.Fatalf("a cancelled read asked for %d frame(s)", frames)
	}
	if len(app.items) != 0 {
		t.Fatalf("a cancelled read added %d source(s)", len(app.items))
	}
}

var errNoPlayer = errors.New("the player refused the media")

func TestAFailedOpenLeavesNoSourceBehind(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return nil, errNoPlayer }
	app.landImport(0, file, hls.Stream{}, importLocal, false, nil)

	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("a failed open left %d source(s) in the session", got)
	}
	if got := len(app.session.Tracks()); got != 0 {
		t.Fatalf("a failed open left %d track(s) in the session", got)
	}
	if len(app.items) != 0 {
		t.Fatalf("a failed open added %d item(s)", len(app.items))
	}
	if app.lastErr == nil || !strings.Contains(app.lastErr.Error(), "refused the media") {
		t.Fatalf("the failure was not reported: %v", app.lastErr)
	}
	if !strings.Contains(app.lastCommand.Message, "could not import") {
		t.Fatalf("the message does not say the import failed: %q", app.lastCommand.Message)
	}
}

func TestAFailedOpenRollsBackTheProbedTracks(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	stream := hls.Stream{
		URI: "https://example.test/live/index.m3u8",
		Tracks: []hls.TrackInfo{
			{Index: 0, Kind: core.MediaVideo, Codec: "h264"},
			{Index: 1, Kind: core.MediaAudio, Codec: "mpeg4-audio"},
		},
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return nil, errNoPlayer }
	app.landImport(0, stream.URI, stream, importProbed, false, nil)

	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("a failed open left %d source(s)", got)
	}
	if got := len(app.session.Tracks()); got != 0 {
		t.Fatalf("a failed open left %d track(s)", got)
	}
	if len(app.items) != 0 {
		t.Fatalf("a failed open added %d item(s)", len(app.items))
	}
}

func TestAMediaThatCannotOpenIsNotKeptEither(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return nil, errNoPlayer }
	if _, err := app.addFrom(file, originCommandLine); err == nil {
		t.Fatal("addFrom accepted a file whose player could not open")
	}
	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("addFrom left %d source(s) behind", got)
	}
	if got := len(app.session.Tracks()); got != 0 {
		t.Fatalf("addFrom left %d track(s) behind", got)
	}
}

func TestALandingAfterTheWindowClosedAddsNothing(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.importing = "there.mp4"
	done := make(chan struct{})
	app.importDone = done
	app.closed = true
	app.landImport(0, file, hls.Stream{}, importLocal, false, nil)

	if len(app.items) != 0 {
		t.Fatalf("a landing after close added %d item(s)", len(app.items))
	}
	if got := len(app.session.Sources()); got != 0 {
		t.Fatalf("a landing after close left %d source(s)", got)
	}
	if app.importing != "" {
		t.Fatalf("a landing after close kept importing=%q", app.importing)
	}
	select {
	case <-done:
	default:
		t.Fatal("a landing after close left the repaint loop running")
	}
}

// The engine error that decides whether an import worked arrives after the open
// returns, so the rule is separate from the loop that reads it.
func TestAnImportIsGivenBackOnlyWhenThePlayerErroredWithoutReady(t *testing.T) {
	cases := []struct {
		name  string
		ready bool
		err   error
		want  bool
	}{
		{"no error, not ready yet", false, nil, false},
		{"ready with no error", true, nil, false},
		{"ready despite an old error", true, errNoPlayer, false},
		{"error before any frame", false, errNoPlayer, true},
	}
	for _, c := range cases {
		if got := shouldAbandonImport(c.ready, c.err); got != c.want {
			t.Errorf("%s: shouldAbandonImport(%v, %v) = %v, want %v", c.name, c.ready, c.err, got, c.want)
		}
	}
}

// A player read off the drawing thread reports live only while the item still
// owns that generation.
func TestThePlayerStateIsReadOnlyWhileTheGenerationIsOwned(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	app.onMain = func(fn func()) { fn() }
	it := &item{player: &ui.VideoPlayer{}, generation: 2}
	it.source.ID = "source-1"
	app.items = append(app.items, it)

	_, _, live := app.playerState(it, 2)
	if !live {
		t.Fatal("the current player was not readable")
	}
	if _, _, live := app.playerState(it, 1); live {
		t.Fatal("a player from an older generation was still readable")
	}
	app.closed = true
	if _, _, live := app.playerState(it, 2); live {
		t.Fatal("a closed window still handed out a player")
	}
}

func TestASuccessfulImportStillAddsOneSource(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app state: %v", err)
	}
	file := filepath.Join(t.TempDir(), "there.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.openPlayerForImport = func(string) (*ui.VideoPlayer, error) { return &ui.VideoPlayer{}, nil }
	app.landImport(0, file, hls.Stream{}, importLocal, false, nil)

	if got := len(app.session.Sources()); got != 1 {
		t.Fatalf("a good import left %d source(s), want 1", got)
	}
	if len(app.items) != 1 {
		t.Fatalf("a good import left %d item(s), want 1", len(app.items))
	}
	if app.selected != app.items[0].source.ID {
		t.Fatalf("the imported source is not selected")
	}
}

func servePlaylists(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestInspectPlaylistKeepsAMediaPlaylist(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/live.m3u8": "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg1.ts\n#EXT-X-ENDLIST\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/live.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if role.address != server.URL+"/live.m3u8" {
		t.Fatalf("address = %q, want the handed-over playlist", role.address)
	}
	if role.live {
		t.Fatal("a playlist that ends was reported as live")
	}
}

func TestInspectPlaylistReportsALivePlaylist(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/live.m3u8": "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg1.ts\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/live.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if !role.live {
		t.Fatal("a playlist without EXT-X-ENDLIST was not reported as live")
	}
}

func TestInspectPlaylistChoosesThePictureVariant(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/master.m3u8": strings.Join([]string{
			"#EXTM3U",
			`#EXT-X-STREAM-INF:BANDWIDTH=128000,CODECS="mp4a.40.2"`,
			"sound.m3u8",
			`#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2"`,
			"720.m3u8",
			`#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,CODECS="avc1.4d4028,mp4a.40.2"`,
			"1080.m3u8",
			"",
		}, "\n"),
		"/sound.m3u8": "#EXTM3U\n#EXTINF:2,\na.aac\n",
		"/720.m3u8":   "#EXTM3U\n#EXTINF:2,\nb.ts\n",
		"/1080.m3u8":  "#EXTM3U\n#EXTINF:2,\nc.ts\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if want := server.URL + "/1080.m3u8"; role.address != want {
		t.Fatalf("address = %q, want %q", role.address, want)
	}
	if !role.live {
		t.Fatal("a variant without EXT-X-ENDLIST was not reported as live")
	}
}

func TestInspectPlaylistFallsBackToBandwidthWithoutResolutions(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/master.m3u8": strings.Join([]string{
			"#EXTM3U",
			"#EXT-X-STREAM-INF:BANDWIDTH=600000",
			"low.m3u8",
			"#EXT-X-STREAM-INF:BANDWIDTH=2400000",
			"high.m3u8",
			"",
		}, "\n"),
		"/low.m3u8":  "#EXTM3U\n#EXTINF:2,\na.ts\n",
		"/high.m3u8": "#EXTM3U\n#EXTINF:2,\nb.ts\n#EXT-X-ENDLIST\n",
	})
	role, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8")
	if err != nil {
		t.Fatalf("inspectPlaylist: %v", err)
	}
	if want := server.URL + "/high.m3u8"; role.address != want {
		t.Fatalf("address = %q, want %q", role.address, want)
	}
	if role.live {
		t.Fatal("the chosen variant ends, so the stream must not be reported as live")
	}
}

func TestInspectPlaylistRefusesAProtectedStream(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/drm.m3u8": "#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://key\"\n#EXTINF:2,\na.ts\n",
	})
	_, err := inspectPlaylist(context.Background(), server.URL+"/drm.m3u8")
	if !errors.Is(err, errProtectedPlaylist) {
		t.Fatalf("err = %v, want a protected stream refusal", err)
	}
	if err == nil || !strings.Contains(err.Error(), "SAMPLE-AES") {
		t.Fatalf("the refusal should name the method it cannot read, got %v", err)
	}
}

func TestInspectPlaylistRefusesAProtectedVariant(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/master.m3u8": "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=1280x720\n720.m3u8\n",
		"/720.m3u8":    "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:2,\na.ts\n",
	})
	_, err := inspectPlaylist(context.Background(), server.URL+"/master.m3u8")
	if !errors.Is(err, errProtectedPlaylist) {
		t.Fatalf("err = %v, want a protected stream refusal", err)
	}
}

func TestInspectPlaylistRejectsSomethingThatIsNotAPlaylist(t *testing.T) {
	server := servePlaylists(t, map[string]string{
		"/page.m3u8": "<!doctype html><title>not a stream</title>",
	})
	_, err := inspectPlaylist(context.Background(), server.URL+"/page.m3u8")
	if err == nil || !strings.Contains(err.Error(), "not a playlist") {
		t.Fatalf("err = %v, want a refusal naming the body", err)
	}
}

func TestInspectPlaylistReportsABadStatus(t *testing.T) {
	server := servePlaylists(t, map[string]string{})
	_, err := inspectPlaylist(context.Background(), server.URL+"/nothing.m3u8")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the status the origin answered", err)
	}
}

func TestProtectedMethodIgnoresNONEE(t *testing.T) {
	text := "#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n"
	if got := protectedMethod(text); got != "AES-128" {
		t.Fatalf("protectedMethod = %q, want AES-128", got)
	}
	if got := protectedMethod("#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n"); got != "" {
		t.Fatalf("protectedMethod = %q, want no method", got)
	}
}

func TestAnAddressIsClassifiedByWhatItServes(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...)
	cases := []struct {
		name        string
		contentType string
		body        []byte
		want        addressKind
		wantErr     string
	}{
		{"a playlist by its body", "application/octet-stream", []byte("#EXTM3U\r\n#EXT-X-VERSION:3\r\n"), addressPlaylist, ""},
		{"a playlist by its type", "application/vnd.apple.mpegurl; charset=utf-8", []byte("not obviously a playlist\n"), addressPlaylist, ""},
		{"a playlist with a byte order mark", "text/plain", []byte("\xef\xbb\xbf#EXTM3U\n"), addressPlaylist, ""},
		{"an mp4 by its box", "application/octet-stream", mp4, addressDirect, ""},
		{"an mp4 by its type", "video/mp4", []byte("anything"), addressDirect, ""},
		{"a transport stream by its sync byte", "application/octet-stream", transportStream(), addressDirect, ""},
		{"a web page by its type", "text/html; charset=utf-8", []byte("<p>hello</p>"), 0, "web page"},
		{"a web page by its body", "application/octet-stream", []byte("<!DOCTYPE html><html></html>"), 0, "web page"},
		{"a json body", "application/json", []byte(`{"ok":true}`), 0, "does not know how to play"},
		{"an untyped unknown body", "", []byte("\x00\x01\x02\x03"), 0, "states no content type"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, err := classifyAddress(c.contentType, c.body)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("%q was accepted as a kind", c.contentType)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("the refusal says %q, want it to mention %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("the address was refused: %v", err)
			}
			if kind != c.want {
				t.Fatalf("the address was read as %v, want %v", kind, c.want)
			}
		})
	}
}

// TestSniffReadsOnlyTheFirstBytes checks the request and the refusals around it:
// a small ranged read, no credentials, and the server's own status when it says
// no.
func TestSniffReadsOnlyTheFirstBytes(t *testing.T) {
	var rangeHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader = r.Header.Get("Range")
		switch r.URL.Path {
		case "/playlist.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
		case "/movie.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><html><body>not media</body></html>")
		case "/broken":
			http.Error(w, "the origin is having a bad day", http.StatusInternalServerError)
		case "/to-page":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/to-nothing":
			http.Redirect(w, r, "/nothing", http.StatusTemporaryRedirect)
		case "/to-file":
			w.Header().Set("Location", "file:///C:/Windows/win.ini")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	kind, err := sniffAddress(context.Background(), server.URL+"/playlist.m3u8")
	if err != nil {
		t.Fatalf("a playlist was refused: %v", err)
	}
	if kind != addressPlaylist {
		t.Fatalf("the playlist was read as %v", kind)
	}
	if !strings.HasPrefix(rangeHeader, "bytes=0-") {
		t.Fatalf("the read asked for %q, want a small leading range", rangeHeader)
	}

	kind, err = sniffAddress(context.Background(), server.URL+"/movie.mp4")
	if err != nil {
		t.Fatalf("an mp4 was refused: %v", err)
	}
	if kind != addressDirect {
		t.Fatalf("the mp4 was read as %v", kind)
	}

	if _, err := sniffAddress(context.Background(), server.URL+"/page"); err == nil || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("a web page was not refused as one: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/nothing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a missing address did not report the server's status: %v", err)
	}
	if _, err := sniffAddress(context.Background(), "http://user:secret@"+strings.TrimPrefix(server.URL, "http://")+"/movie.mp4"); err == nil || !strings.Contains(err.Error(), "user name") {
		t.Fatalf("an address with credentials was not refused: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/broken"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a 500 did not report the server's status: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/to-page"); err == nil || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("a redirect to a web page was not refused as one: %v", err)
	}
	if _, err := sniffAddress(context.Background(), server.URL+"/to-nothing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a redirect to a missing address did not report the status: %v", err)
	}
	// The http client itself refuses the scheme before this package sees the
	// response; either wording is a refusal, and nothing below http is read.
	if _, err := sniffAddress(context.Background(), server.URL+"/to-file"); err == nil ||
		(!strings.Contains(err.Error(), "not an http or https address") && !strings.Contains(err.Error(), "unsupported protocol scheme")) {
		t.Fatalf("a redirect away from http was not refused: %v", err)
	}
}

// TestADirectAddressIsRecordedLikeAFile checks that a plain media address gives
// the session the same two labelled streams a local file gets, rather than a
// guessed track list.
func TestADirectAddressIsRecordedLikeAFile(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("the app state was not built: %v", err)
	}
	it := &item{uri: "http://127.0.0.1:1/movie.mp4"}
	if err := app.addDirect(it); err != nil {
		t.Fatalf("a direct address was refused: %v", err)
	}
	if it.source.ID == "" {
		t.Fatal("the address produced no source")
	}
	var tracks []core.Track
	for _, track := range app.session.Tracks() {
		if track.Source == it.source.ID {
			tracks = append(tracks, track)
		}
	}
	if len(tracks) != 2 {
		t.Fatalf("the address produced %d tracks, want 2", len(tracks))
	}
	for _, track := range tracks {
		if !strings.Contains(track.Label, "does not enumerate") {
			t.Fatalf("%s is labelled %q, which does not say where it came from", track.ID, track.Label)
		}
	}
}

// transportStream builds enough of a transport stream for the sync-byte test.
func transportStream() []byte {
	head := make([]byte, 200)
	head[0] = 0x47
	head[188] = 0x47
	return head
}

type fakePlayer struct {
	ready      bool
	playing    bool
	buf        bool
	pos        time.Duration
	dur        time.Duration
	volume     float64
	muted      bool
	err        error
	seekOffset time.Duration
}

// seekOffset lets a test pretend the engine lands a little off the request.
func (f *fakePlayer) Play()  {}
func (f *fakePlayer) Pause() {}
func (f *fakePlayer) Seek(d time.Duration) {
	f.pos = d + f.seekOffset
}
func (f *fakePlayer) SetVolume(v float64) *ui.VideoPlayer { f.volume = v; return nil }
func (f *fakePlayer) SetMuted(m bool) *ui.VideoPlayer     { f.muted = m; return nil }
func (f *fakePlayer) Close()                              {}

func (f fakePlayer) Ready() bool             { return f.ready }
func (f fakePlayer) Playing() bool           { return f.playing }
func (f fakePlayer) Buffering() bool         { return f.buf }
func (f fakePlayer) Position() time.Duration { return f.pos }
func (f fakePlayer) Duration() time.Duration { return f.dur }
func (f fakePlayer) Volume() float64         { return f.volume }
func (f fakePlayer) Muted() bool             { return f.muted }
func (f fakePlayer) Err() error              { return f.err }

func TestStateFromCopiesEveryField(t *testing.T) {
	want := fakePlayer{
		ready: true, playing: true, buf: false,
		pos: 3*time.Second + 250*time.Millisecond, dur: 30 * time.Second,
		volume: 0.4, muted: true,
	}
	state := engineStateFrom(want)
	if !state.Ready || !state.Playing || state.Buf {
		t.Fatalf("flags: ready=%t playing=%t buffering=%t", state.Ready, state.Playing, state.Buf)
	}
	if state.Pos != want.pos || state.Dur != want.dur {
		t.Fatalf("times: pos=%s dur=%s", state.Pos, state.Dur)
	}
	if state.Volume != 0.4 || !state.Muted {
		t.Fatalf("audio: volume=%v muted=%t", state.Volume, state.Muted)
	}
	if state.Err != "" {
		t.Fatalf("unexpected error text %q", state.Err)
	}
	if !state.ready() {
		t.Fatal("a ready player without an error must be ready")
	}
}

func TestStateFromKeepsTheEngineError(t *testing.T) {
	state := engineStateFrom(fakePlayer{ready: true, err: errors.New("no first frame")})
	if state.Err != "no first frame" {
		t.Fatalf("error text = %q", state.Err)
	}
	if state.ready() {
		t.Fatal("a player that reports an error is not ready")
	}
}

func TestStateOfAnAbsentPlayer(t *testing.T) {
	state := readPlayer(nil)
	if state.Ready || state.Playing || state.Pos != 0 || state.Err != "" {
		t.Fatalf("absent player = %+v", state)
	}
	if state.ready() {
		t.Fatal("an absent player is never ready")
	}
}

func TestSeekAndMeasureReportsWhatThePlayerDid(t *testing.T) {
	player := &fakePlayer{dur: 30 * time.Second, seekOffset: 120 * time.Millisecond}
	outcome := seekAndMeasure(player, 5*time.Second)
	if outcome.Requested != 5*time.Second {
		t.Fatalf("requested = %s", outcome.Requested)
	}
	if outcome.Actual != 5*time.Second+120*time.Millisecond {
		t.Fatalf("actual = %s", outcome.Actual)
	}
	if outcome.Residual != 120*time.Millisecond {
		t.Fatalf("residual = %s", outcome.Residual)
	}
	if !outcome.Window.Known || outcome.Window.Start != 0 || outcome.Window.End != 30*time.Second {
		t.Fatalf("window = %s", outcome.Window)
	}
	if outcome.RequestedAt.IsZero() || outcome.ReadAt.Before(outcome.RequestedAt) {
		t.Fatalf("wall clock: requested=%v read=%v", outcome.RequestedAt, outcome.ReadAt)
	}
}

func TestSeekAndMeasureOnAnAbsentPlayer(t *testing.T) {
	outcome := seekAndMeasure(nil, 4*time.Second)
	if outcome.Actual != 0 || outcome.Residual != 0 {
		t.Fatalf("absent player outcome = %+v", outcome)
	}
	if outcome.Window.Known {
		t.Fatalf("absent player reported a window: %s", outcome.Window)
	}
}

func TestSeekAndMeasureLeavesLiveWindowsUnknown(t *testing.T) {
	player := &fakePlayer{dur: 0}
	outcome := seekAndMeasure(player, time.Second)
	if outcome.Window.Known {
		t.Fatalf("a player without a duration reported a window: %s", outcome.Window)
	}
	if outcome.Window.String() != "unknown" {
		t.Fatalf("unknown window renders as %q", outcome.Window.String())
	}
}

func stallProgram(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "stall.go")
	body := "package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(60 * time.Second) }\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write the helper: %v", err)
	}
	bin := filepath.Join(dir, "stall.exe")
	build := exec.Command("go", "build", "-o", bin, src)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build the helper: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return bin
}

// TestCancellingADecodeEndsTheChild checks the guarantee the close path depends
// on: cancelling the session's context ends the decoder it started. The helper
// sleeps for a minute, so returning at all is proof the child was killed; the pid
// it reports is the one the test watched.
func TestCancellingADecodeEndsTheChild(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so the helper cannot be built")
	}
	bin := stallProgram(t)
	old := decodeProgram
	decodeProgram = bin
	t.Cleanup(func() { decodeProgram = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pids := make(chan int, 1)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := decodeEnvelope(ctx, "stall://nowhere", 0, time.Second, func(pid int) {
			pids <- pid
		}, nil)
		done <- err
	}()

	var pid int
	select {
	case pid = <-pids:
	case err := <-done:
		t.Fatalf("the decoder ended before it was cancelled: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("the decoder never reported a pid")
	}
	if pid <= 0 {
		t.Fatalf("the decoder reported pid %d, want a real one", pid)
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled decode returned no error")
		}
		if !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("the cancelled decode says %q, want it to say it stopped", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the decoder with pid %d outlived its cancellation", pid)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("the cancelled decode took %s", elapsed)
	}
}

// TestStoppingAProbeDropsItsResult checks the second guarantee: after the session
// closed, a comparison that was in flight cannot become part of its account, even
// if its goroutine reaches the drawing thread later.
func TestStoppingAProbeDropsItsResult(t *testing.T) {
	app := &appState{sync: newSyncState()}
	ctx, cancel := context.WithCancel(context.Background())
	app.probeRunning = true
	app.probeAttempt = 4
	app.probeCtx, app.probeCancel = ctx, cancel
	app.probeChild.Store(4242)

	if !app.stopProbe("the check closed the session") {
		t.Fatal("stopProbe found nothing to stop")
	}
	if app.probeRunning {
		t.Fatal("the probe is still marked as running")
	}
	if app.probeAttempt != 5 {
		t.Fatalf("probeAttempt = %d, want 5: the cancelled attempt must not be reusable", app.probeAttempt)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("the decoder's context is still alive after stopProbe")
	}

	app.applyProbeResult(4, alignSuggestion{CoreOffset: 9 * time.Second}, nil, alignWindow{}, nil, alignWindow{}, nil)
	if app.syncNote != "" {
		t.Fatalf("a cancelled result reached the status bar: %q", app.syncNote)
	}
	if app.probeDone == 4 {
		t.Fatal("a cancelled result was recorded as the newest finished attempt")
	}
	if app.lastProbe.CoreOffset != 0 {
		t.Fatalf("a cancelled result became the account: %+v", app.lastProbe)
	}
}

// TestTheDecoderRecordIsClearedWhenItEnds checks the bookkeeping the reviewer
// reproduced: once ffmpeg has exited, probePID must say 0, or every reader has
// to guess whether a pid that once ran is still running.
func TestTheDecoderRecordIsClearedWhenItEnds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so the helper cannot be built")
	}
	bin := stallProgram(t)
	old := decodeProgram
	decodeProgram = bin
	t.Cleanup(func() { decodeProgram = old })

	app := &appState{sync: newSyncState()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := decodeEnvelope(ctx, "stall://nowhere", 0, time.Second, app.noteProbeChild, app.clearProbeChild)
		done <- err
	}()

	deadline := time.Now().Add(20 * time.Second)
	for app.probePID() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	pid := app.probePID()
	if pid == 0 {
		t.Fatal("the decoder never reported its pid")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("the decoder with pid %d outlived its cancellation", pid)
	}
	if left := app.probePID(); left != 0 {
		t.Fatalf("probePID still says %d after the decoder ended (%s)", left, time.Since(started))
	}
}

// TestAnEndedDecoderLeavesARunningOneRecorded proves the record belongs to the
// decoder that is still running: when an older one ends after a newer one
// started, it must not clear the newer one's pid.
//
// Comparisons do not overlap through the button today — a second one is refused —
// so this is a guard on the bookkeeping, not a path a user can walk today.
func TestAnEndedDecoderLeavesARunningOneRecorded(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so the helper cannot be built")
	}
	bin := stallProgram(t)
	old := decodeProgram
	decodeProgram = bin
	t.Cleanup(func() { decodeProgram = old })

	app := &appState{sync: newSyncState()}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan error, 1)
	firstPid := make(chan int, 1)
	go func() {
		_, err := decodeEnvelope(firstCtx, "stall://nowhere", 0, time.Second, func(pid int) {
			firstPid <- pid
			app.noteProbeChild(pid)
		}, app.clearProbeChild)
		firstDone <- err
	}()
	var older int
	select {
	case older = <-firstPid:
	case <-time.After(20 * time.Second):
		t.Fatal("the first decoder never reported its pid")
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondDone := make(chan error, 1)
	secondPid := make(chan int, 1)
	go func() {
		_, err := decodeEnvelope(secondCtx, "stall://nowhere", 0, time.Second, func(pid int) {
			secondPid <- pid
			app.noteProbeChild(pid)
		}, app.clearProbeChild)
		secondDone <- err
	}()
	var newer int
	select {
	case newer = <-secondPid:
	case <-time.After(20 * time.Second):
		t.Fatal("the second decoder never reported its pid")
	}

	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(20 * time.Second):
		t.Fatal("the first decoder outlived its cancellation")
	}
	if now := app.probePID(); now != newer {
		t.Fatalf("the ended decoder %d cleared the record of the running one: probePID = %d, want %d", older, now, newer)
	}

	cancelSecond()
	select {
	case <-secondDone:
	case <-time.After(20 * time.Second):
		t.Fatal("the second decoder outlived its cancellation")
	}
	if left := app.probePID(); left != 0 {
		t.Fatalf("probePID still says %d after the last decoder ended", left)
	}
}

// TestShutdownCancelsTheComparisonInFlight checks that closing the session is
// what cancels the decoder, not a separate button the user has to find.
func TestShutdownCancelsTheComparisonInFlight(t *testing.T) {
	app := &appState{sync: newSyncState()}
	ctx, cancel := context.WithCancel(context.Background())
	app.probeRunning = true
	app.probeAttempt = 1
	app.probeCtx, app.probeCancel = ctx, cancel
	app.shutdown()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("shutdown left the comparison's context alive")
	}
	if app.probeRunning {
		t.Fatal("shutdown left the probe marked as running")
	}
	if app.probeCancel != nil || app.probeCtx != nil {
		t.Fatal("shutdown kept the cancelled context around")
	}
}

func TestASafeOpenErrorKeepsTheChainButNotTheSecret(t *testing.T) {
	refused := errors.New(`open "http://127.0.0.1:9/live/index.m3u8?token=TOP_SECRET_123#key": 503 Service Unavailable`)
	safe := safeOpenError(refused)

	if strings.Contains(safe.Error(), "TOP_SECRET_123") || strings.Contains(safe.Error(), "?token=") {
		t.Fatalf("the stored error repeats the secret: %q", safe.Error())
	}
	if !strings.Contains(safe.Error(), "503 Service Unavailable") {
		t.Fatalf("the stored error lost the reason: %q", safe.Error())
	}
	if !errors.Is(safe, refused) {
		t.Fatalf("the stored error no longer wraps the one it came from")
	}
	if safeOpenError(nil) != nil {
		t.Fatal("a missing error became a present one")
	}
	plain := errors.New("the file is not there")
	if got := safeOpenError(plain); got != plain {
		t.Fatalf("an error with nothing to redact was replaced: %v", got)
	}
}

// The same through the path a start-up takes: a signed playlist address the
// origin refuses leaves a safe open error on the item.
func TestAFailedProbeLeavesASafeOpenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	it := &item{uri: server.URL + "/live/index.m3u8?token=TOP_SECRET_123"}
	if err := app.addProbed(it); err == nil {
		t.Fatal("a refused playlist opened")
	}
	if it.openErr == nil {
		t.Fatal("the item kept no open error")
	}
	if strings.Contains(it.openErr.Error(), "TOP_SECRET_123") {
		t.Fatalf("the item's open error repeats the token: %q", it.openErr.Error())
	}
	if !strings.Contains(errorString(it.openErr), "503") {
		t.Fatalf("the reported error lost the status: %q", errorString(it.openErr))
	}
}

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
	if !sameSettings(got, want) {
		t.Fatalf("loaded %s, want %s", settingsSummary(got), settingsSummary(want))
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
	if !sameSettings(got, defaultSettings()) {
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
			if !sameSettings(got, defaultSettings()) {
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

// TestAnOlderSaveCannotReplaceANewerFile is the ordering half of the fix: the
// lock stops two temp files from racing, and the ticket stops a snapshot made
// earlier — whose write happens to run later — from landing on top of a newer
// one.
func TestAnOlderSaveCannotReplaceANewerFile(t *testing.T) {
	dir := t.TempDir()
	app := &appState{settings: defaultSettings(), settingsPath: filepath.Join(dir, "settings.json")}
	newer := app.settings
	newer.DefaultVolume = 0.8
	older := app.settings
	older.DefaultVolume = 0.2
	olderTicket := app.saveTicket()
	newerTicket := app.saveTicket()
	if err := app.saveSettingsSerialized(newer, newerTicket); err != nil {
		t.Fatalf("the newer save failed: %v", err)
	}
	if err := app.saveSettingsSerialized(older, olderTicket); err != nil {
		t.Fatalf("an older save reported %v, want it skipped", err)
	}
	got, note := loadSettings(app.settingsPath)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if got.DefaultVolume != 0.8 {
		t.Fatalf("an older snapshot replaced the newer file: %.2f", got.DefaultVolume)
	}
	// A ticket newer than the last written one still writes.
	newest := app.settings
	newest.DefaultVolume = 0.6
	if err := app.saveSettingsSerialized(newest, app.saveTicket()); err != nil {
		t.Fatalf("a newer save after the skip failed: %v", err)
	}
	if got, _ := loadSettings(app.settingsPath); got.DefaultVolume != 0.6 {
		t.Fatalf("the newest save did not land: %.2f", got.DefaultVolume)
	}
}

func TestAShortcutWithoutASourceSaysWhatToDo(t *testing.T) {
	a, _ := bareState(t)
	a.selected = ""

	a.shortcutCommand(commandShow)

	if a.lastErr == nil || !strings.Contains(a.lastErr.Error(), "no source is selected") {
		t.Fatalf("lastErr = %v, want it to ask for a source", a.lastErr)
	}
	if a.lastCommand.Message != "nothing selected" {
		t.Fatalf("message = %q, want nothing selected", a.lastCommand.Message)
	}
	if a.shortcutFires != 1 || a.firesWhileTyping != 0 {
		t.Fatalf("fires = %d, while typing = %d, want 1 and 0", a.shortcutFires, a.firesWhileTyping)
	}
}

func TestAShortcutNoticesWhenATextFieldHeldTheFocus(t *testing.T) {
	a, _ := bareState(t)
	a.keysTyping = true

	a.shortcutCommand(commandHear)

	if a.firesWhileTyping != 1 {
		t.Fatalf("fires while typing = %d, want 1", a.firesWhileTyping)
	}
}

func TestSelectedSourceIsWhatAShortcutActsOn(t *testing.T) {
	a, it := bareState(t)
	a.selected = it.source.ID

	_, index, ok := a.selectedSource()
	if !ok || index != 0 {
		t.Fatalf("selectedSource() = %d, %v, want the only source", index, ok)
	}
}

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
	// The entry must be absolute on the platform running the test: a Windows
	// path is not absolute on Linux, and the save would be refused there. The
	// stored form is the normalised one, which lowercases a path on Windows.
	local, err := normalizeLocalIdentity(filepath.Join(dir, "local.mp4"))
	if err != nil {
		t.Fatalf("normalise the local path: %v", err)
	}
	want := defaultSettings()
	want.SourceMemory = sourceMemory{
		On:       true,
		Remember: true,
		Entries: []sourceMemoryEntry{
			{Address: "https://example.com/a.m3u8", Volume: 0.25, Muted: true},
			{Address: local, Volume: 1, Muted: false},
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
	got, note := waitForSettingsFile(t, app.settingsPath)
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

// TestValidateSettingsChecksTheMemory guards the wiring: validateSourceMemory
// existed on its own for a while, and a set that never called it accepted a
// file whose entries could not be honoured.
func TestValidateSettingsChecksTheMemory(t *testing.T) {
	st := defaultSettings()
	st.SourceMemory = sourceMemory{On: true, Entries: []sourceMemoryEntry{
		{Address: "HTTPS://Example.com/a.m3u8", Volume: 0.5},
	}}
	err := validateSettings(st)
	if err == nil || !strings.Contains(err.Error(), "normalised") {
		t.Fatalf("a bad memory entry was accepted: %v", err)
	}
}

// TestABadMemoryEntryFallsBackToTheDefaults is the same wiring seen from the
// file: a settings file with one unhonourable entry must not be used at all,
// and must stay on disk so it can be inspected.
func TestABadMemoryEntryFallsBackToTheDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := `{"sourceMemory":{"on":true,"remember":true,"entries":[` +
		`{"address":"HTTPS://Example.com/a.m3u8","volume":0.5,"muted":false}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, note := loadSettings(path)
	if note == "" || !strings.Contains(note, "sourceMemory") {
		t.Fatalf("the bad memory was not reported: %q", note)
	}
	if loaded.SourceMemory.On || len(loaded.SourceMemory.Entries) != 0 {
		t.Fatalf("the bad memory was used: %+v", loaded.SourceMemory)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("the bad file was rewritten:\n%s", after)
	}
}

// waitForSettingsFile waits for the background write a memory change starts:
// rememberSource owns the in-memory set, but hands the file to a goroutine.
func waitForSettingsFile(t *testing.T, path string) (settings, string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		st, note := loadSettings(path)
		if _, err := os.Stat(path); err == nil {
			// Let the goroutine finish its own bookkeeping before the test ends.
			time.Sleep(20 * time.Millisecond)
			return st, note
		}
		if time.Now().After(deadline) {
			t.Fatalf("the settings file never appeared at %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// memoryFixture builds a session with one routed source whose address can be an
// identity, so a remembered level has somewhere to land.
func memoryFixture(t *testing.T) (*appState, *item, *mutePlayer) {
	t.Helper()
	session := core.NewSession("memory test")
	scene, err := session.AddScene("main")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	uri := filepath.Join(t.TempDir(), "sample.mp4")
	source, err := session.AddSource(uri)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	video, err := session.AddTrack(source.ID, core.MediaVideo, "picture")
	if err != nil {
		t.Fatalf("video: %v", err)
	}
	sound, err := session.AddTrack(source.ID, core.MediaAudio, "sound")
	if err != nil {
		t.Fatalf("sound: %v", err)
	}
	if _, err := session.SetSceneAudio(scene.ID, sound.ID, 1, false); err != nil {
		t.Fatalf("route: %v", err)
	}
	player := &mutePlayer{fakeSeekPlayer: &fakeSeekPlayer{}}
	it := &item{uri: uri, source: source, video: video.ID, audio: sound.ID, volume: 1, player: player}
	app := &appState{
		session:  session,
		scene:    scene.ID,
		settings: defaultSettings(),
		audible:  map[core.TrackID]bool{},
		sync:     newSyncState(),
	}
	app.items = []*item{it}
	return app, it, player
}

func TestOrdinaryTransportDoesNotCreateSyncGroup(t *testing.T) {
	app, _, _ := memoryFixture(t)
	if result := app.runCommand(command{Kind: commandPlay}); result.Err != nil {
		t.Fatalf("play scene: %v", result.Err)
	}
	if groups := app.session.SyncGroups(); len(groups) != 0 {
		t.Fatalf("ordinary transport created %d sync groups", len(groups))
	}
	if result := app.runCommand(command{Kind: commandPause}); result.Err != nil {
		t.Fatalf("pause scene: %v", result.Err)
	}
	if groups := app.session.SyncGroups(); len(groups) != 0 {
		t.Fatalf("ordinary pause created %d sync groups", len(groups))
	}
}

func TestSingleSourceTransportRejectsExplicitGroupMember(t *testing.T) {
	app, it, _ := memoryFixture(t)
	if _, err := app.session.AddSyncGroup(it.video, it.video, it.audio); err != nil {
		t.Fatalf("add group: %v", err)
	}
	group := app.session.SyncGroups()[0]
	app.sync.group = group.ID
	result := app.runCommand(command{Kind: commandSourcePause, Source: itemKey(it)})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "sync group") {
		t.Fatalf("single-source pause error = %v", result.Err)
	}
}

// TestRestoredSoundReachesTheSceneAndThePlayer pins the difference between the
// card saying 25% and the output playing 25%: the scene level has to carry the
// remembered number, because that is the number the player is handed.
func TestRestoredSoundReachesTheSceneAndThePlayer(t *testing.T) {
	app, it, player := memoryFixture(t)
	// The unaligned protection would keep the player silent, and a silent
	// player is not handed a level: confirm the track's place first, so the
	// assertion below is about the remembered number and not about the guard.
	if err := app.sync.timeline.SetShift(it.audio, 0); err != nil {
		t.Fatalf("confirm the track's shift: %v", err)
	}
	identity := identityOf(it.uri)
	if identity == "" {
		t.Fatalf("the fixture's address is not an identity")
	}
	app.settings.SourceMemory = sourceMemory{On: true, Entries: []sourceMemoryEntry{
		{Address: identity, Volume: 0.25},
	}}
	app.applySourceMemory(it)
	if it.volume != 0.25 {
		t.Fatalf("the item kept %.2f", it.volume)
	}
	level, routed := app.sceneLevel(it)
	if !routed {
		t.Fatal("the fixture's source is not routed")
	}
	if level.Volume != 0.25 {
		t.Fatalf("the scene plays it at %.2f, not at the remembered 0.25", level.Volume)
	}
	if player.volume != app.volumeFor(0.25) {
		t.Fatalf("the player was handed %.2f, not %.2f", player.volume, app.volumeFor(0.25))
	}
}

// TestAnApplyKeepsWhatWasRememberedAfterTheDraftOpened is the case the page
// creates by accident: a draft opened before a source was remembered, then
// Apply on an unrelated field. The list must not go back to the older copy.
func TestAnApplyKeepsWhatWasRememberedAfterTheDraftOpened(t *testing.T) {
	app, _, _ := memoryFixture(t)
	app.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	open := "https://example.com/open.m3u8"
	added := "https://example.com/added.m3u8"
	app.settings.SourceMemory = sourceMemory{On: true, Remember: true, Entries: []sourceMemoryEntry{
		{Address: open, Volume: 0.8},
	}}
	app.beginDraft(app.settings)
	// A source is remembered while the page is open, so the live settings are
	// newer than the draft in exactly one entry.
	next := cloneSettings(app.settings)
	next.SourceMemory.Entries = append([]sourceMemoryEntry{{Address: added, Volume: 0.5}}, next.SourceMemory.Entries...)
	app.settings = next
	// Apply something else.
	app.settingsDraft.DefaultVolume = 0.42
	app.applySettingsCommand()
	if app.settingsErr != "" {
		t.Fatalf("Apply failed: %s", app.settingsErr)
	}
	entries := app.settings.SourceMemory.Entries
	if len(entries) != 2 {
		t.Fatalf("Apply changed the list to %d entr(ies): %+v", len(entries), entries)
	}
	if entries[0].Address != added || entries[1].Address != open {
		t.Fatalf("the newest entry is not first: %+v", entries)
	}
}

// TestAClearInTheDraftStillEmptiesTheList keeps the merge from making the
// Forget button a decoration: an explicit Clear is the one Apply that deletes.
func TestAClearInTheDraftStillEmptiesTheList(t *testing.T) {
	app, _, _ := memoryFixture(t)
	app.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	app.settings.SourceMemory = sourceMemory{On: true, Entries: []sourceMemoryEntry{
		{Address: "https://example.com/a.m3u8", Volume: 0.8},
		{Address: "https://example.com/b.m3u8", Volume: 0.6},
	}}
	app.beginDraft(app.settings)
	app.settingsDraft.SourceMemory.clear()
	app.draftCleared = true
	app.applySettingsCommand()
	if app.settingsErr != "" {
		t.Fatalf("Apply failed: %s", app.settingsErr)
	}
	if len(app.settings.SourceMemory.Entries) != 0 {
		t.Fatalf("the clear did not reach the applied settings: %+v", app.settings.SourceMemory.Entries)
	}
	if loaded, note := loadSettings(app.settingsPath); note != "" || len(loaded.SourceMemory.Entries) != 0 {
		t.Fatalf("the file still holds entries (%s): %+v", note, loaded.SourceMemory.Entries)
	}
}

// TestRememberSourceRefusesAnUntrustedFile closes the hole the workspace save
// already closed: a start that could not read the file must not replace it.
func TestRememberSourceRefusesAnUntrustedFile(t *testing.T) {
	app, it, _ := memoryFixture(t)
	app.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	app.settingsTrusted = false
	app.settings.SourceMemory = sourceMemory{On: true, Remember: true}

	app.rememberSource(it)

	if len(app.settings.SourceMemory.Entries) != 0 {
		t.Fatalf("an untrusted start remembered %d entr(ies)", len(app.settings.SourceMemory.Entries))
	}
	if _, err := os.Stat(app.settingsPath); err == nil {
		t.Fatal("an untrusted start wrote the settings file")
	}
}

// TestAStaleSnapshotCannotUndoAClear orders two writes by when their snapshots
// were made, not by which goroutine finished first: the snapshot taken before a
// Clear must not put the forgotten entries back.
func TestAStaleSnapshotCannotUndoAClear(t *testing.T) {
	app, _, _ := memoryFixture(t)
	app.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	app.settings.SourceMemory = sourceMemory{On: true, Remember: true, Entries: []sourceMemoryEntry{
		{Address: "https://example.com/a.m3u8", Volume: 0.8},
	}}
	stale := cloneSettings(app.settings)
	staleTicket := app.saveTicket()

	// The Clear happens after that snapshot was taken.
	next := cloneSettings(app.settings)
	next.SourceMemory.Entries = nil
	if err := app.saveSettingsSerialized(next, app.saveTicket()); err != nil {
		t.Fatalf("the clear did not write: %v", err)
	}
	// The older write arrives late.
	if err := app.saveSettingsSerialized(stale, staleTicket); err != nil {
		t.Fatalf("a skipped older write reported %v", err)
	}
	loaded, note := loadSettings(app.settingsPath)
	if note != "" {
		t.Fatalf("the file loaded with a complaint: %s", note)
	}
	if len(loaded.SourceMemory.Entries) != 0 {
		t.Fatalf("the older snapshot put %d entr(ies) back", len(loaded.SourceMemory.Entries))
	}
}
