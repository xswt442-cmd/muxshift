// The session half of the desktop application: Core holds what the user
// configured, and this file maps it onto the players MyGo actually opened.
//
// The mapping runs one way. A scene says which video is shown and which audio
// tracks are heard at which level; applying it sets the players' mute and
// volume. The players' positions and buffering are never written back into the
// session, because a Session holds configuration, not playback state.
package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// item is one media source, the Core objects that describe it, and the player
// opened for it.
type item struct {
	uri string

	// origin says whether the address came from the command line; only a source
	// somebody added by hand writes its level back to the settings file.
	origin sourceOrigin

	source core.Source
	video  core.TrackID
	audio  core.TrackID

	player  desktopPlayer
	openErr error

	// previous is the player this item opened before the current one, kept
	// alive and drawn until the current one has a picture. See reopenPlayer.
	previous desktopPlayer

	// handover owns previous while a task waits for the replacement's picture.
	// It moves to the newer task on a reload, so a burst of reloads releases
	// each player exactly once. See handOver.
	handover *handover

	// shown is whether the window draws this player right now. It changes
	// before the player is closed, never after.
	shown bool

	// generation counts how many players this item has opened. A wake task
	// carries the generation it was started for, so a task left over from an
	// earlier player cannot seek the current one or a closed one.
	generation int

	// replay is the recording this item currently shows, nil when it shows the
	// address it was opened from. The recording belongs to the source its
	// buffer was started for, which is what SourceID on it says.
	replay *dvr.Recording

	// replayAnchor is the measured pair that places that recording on the
	// source's media clock, and replayAnchorErr says why it could not be
	// measured. A switch commits without an anchor — the picture is the product
	// — and a time conversion then refuses instead of guessing.
	replayAnchor    *dvr.Anchor
	replayAnchorErr error

	// replayLease keeps the file of replay alive while its player reads it. A
	// live source has none.
	replayLease *dvr.Lease

	// swap is a replacement player and its lease waiting for a picture, drawn
	// under the current picture until it commits. See playRecording.
	swap *replaySwap

	// volume is the level this source's audio track gets when the scene plays
	// it, kept here so the window's buttons can raise and lower it.
	volume float64

	// muted is this source's own mute. The scene keeps its routing and level;
	// the mixer overlay decides whether the sound is heard. See mixer.go.
	muted bool
}

// trackedRunner is one replay buffer this run opened, kept so shutdown can stop
// recording before it closes anything else.
type trackedRunner struct {
	uri    string
	runner *dvrRunner
}

// appState is a Core session plus the players it is mapped onto.
type appState struct {
	session *core.Session
	scene   core.SceneID
	output  core.OutputID
	items   []*item
	lastErr error

	// solo names the one source the mixer keeps audible, empty when nobody is
	// soloed. masterMute silences everything while it is on. Both are listening
	// overlays: neither edits the scene's routing or any source's own state.
	solo       core.SourceID
	masterMute bool

	// zoom is the content zoom the frame asks for (0 = the display's own scale)
	// and lastWidth/lastHeight are the DIPs it last laid out at. The P15.5
	// scale walk sets the first and reports the second.
	zoom                  float32
	lastWidth, lastHeight float32

	// keysTyping is whether the last pass found a text field holding the
	// keyboard focus, which is when the window's shortcuts stay silent, and
	// keysSeen counts the passes that asked. typingEdges counts the passes
	// where the keyboard newly entered a text field. The focus scenario reads
	// all three.
	keysTyping       bool
	keysSeen         int
	typingEdges      int
	shortcutFires    int
	firesWhileTyping int
	closes           int

	// dvrRoot is the cache root the DVR directories live under, and dvrSession
	// is this run's own directory in it. The session holds a lock for as long as
	// the process runs, so a later run can tell this one's leftovers from a live
	// one's and only collect the former.
	dvrRoot    string
	dvrSession *dvr.Session
	// runners are the replay buffers this run opened, in the order they started.
	// Shutdown walks them first: recording stops before any player closes.
	runners []trackedRunner

	// leaseMu guards the counters the replay scenario uses to show that every
	// recording a player was handed was given back: leasesOpen is what players
	// hold now, leasesGiven how many were taken over the whole run.
	leaseMu     sync.Mutex
	leasesOpen  int
	leasesGiven int

	// openReplay and replayStarted are the seams desktop/replay_swap.go uses in
	// its scenario: a run can stand in for a player that cannot open a file, or
	// for one that never shows a picture. Production leaves both nil.
	openReplay    func(path string) (*ui.VideoPlayer, error)
	replayStarted func(player *ui.VideoPlayer) bool

	// The replay transaction has its own seams: a unit test drives it without a
	// window, standing in for the file it opens, the picture that player would
	// show, and the moment between preparing and committing.
	openSeek         func(recording dvr.Recording) (desktopPlayer, error)
	seekStarted      func(player desktopPlayer) bool
	beforeSeekCommit func()

	// seek is the replay transaction in flight, nil when none is running. The
	// window keeps the player it has until that transaction commits.
	seek    *seekTxn
	seekMu  sync.Mutex
	settled []seekLanding
	// notice is the newest account of a replay transaction, kept as a value so
	// a reader never sees it change under a caller.
	notice replayReport
	// seekStart serialises the decision to start a transaction, so two requests
	// that arrive together cannot both find the field empty and both plan.
	seekStart sync.Mutex
	// seekMain runs the commit step where players may be touched. Tests run
	// without a window and call the step directly.
	seekMain func(func())

	// syncNote is the outcome of the last sync command, so a refused seek stays
	// visible in the window instead of only in the log.
	syncNote string

	// releaseOnce makes shutdown run once even though a scenario and the window
	// both ask for it.
	releaseOnce sync.Once

	// lastSeek is the most recent seek with what was requested, what the player
	// reported, the playable window, and both wall-clock readings. It is the
	// adapter's answer to "did the seek land where the clock said".
	lastSeek       seekOutcome
	lastSeekSource core.SourceID

	// lastCommand is the outcome of the last command that came through
	// runCommand, kept so the window and a scenario can read what happened
	// instead of parsing the status line. See controller.go.
	lastCommand commandResult

	// panel is the page the window shows below its native stage in the P13.3
	// experiment, and webView draws it. They stay nil unless the webview
	// scenario asks for them, so an ordinary run never starts a second engine.
	panel *Panel
	// panelCalls counts the page's calls, so a log can show the page is alive.
	panelCalls int
	webView    *mygo.WebView

	// workspace is the page the rail shows. The stage page is the default so a
	// window opened without a scenario looks exactly as it did before the rail.
	workspace workspace

	// selected is the source the inspector follows. The empty ID means nothing
	// is selected, and the inspector shows the session summary instead.
	selected core.SourceID

	// importText is what the Sources page's address field holds. It lives here
	// so a redraw does not lose what was typed.
	importText string

	// importing is the address being read right now, or empty. The field is the
	// first thing checked so a second Add cannot start a second read.
	importing string

	// importToken tells a landing read from a cancelled one: Cancel bumps it,
	// and a result carrying an older token is dropped without touching the
	// session, so a slow origin cannot add a source after Cancel.
	importToken int
	// importSince is when the read started, for the seconds beside Reading….
	importSince time.Time
	// importCancel stops the probe; importDone ends the repaint ticker that
	// keeps the elapsed seconds moving while the read is in flight.
	importCancel context.CancelFunc
	importDone   chan struct{}

	// window is the one window this session draws into. The import ticker asks
	// it for frames while a read is in flight; it is set once, before the view
	// runs, and only touched on the drawing thread.
	window *mygo.Window

	// settings is what is applied and running; settingsDraft is what the
	// Settings page edits until Apply. settingsPath is where applied values are
	// written, and settingsSaved says this start has written that file, so the
	// page can be exact about the persistence boundary.
	settings        settings
	settingsDraft   settings
	settingsPath    string
	settingsSaved   bool
	settingsErr     string
	settingsNote    string
	settingsSection string
	// settingsTrusted says the file on disk was read and understood. A start
	// that fell back to the defaults must not write over the person's file with
	// its defaults, so the workspace memory checks this before saving.
	settingsTrusted bool

	// budget is the DVR budget a buffer opened now would get, and budgetFrom
	// names the rule that produced it: the command line, the settings file or
	// the built-in defaults. flagBudget and budgetFlags keep what the flags were
	// and which of them a person actually typed, because a flag left at its
	// default is not an override (P18.2).
	budget      dvrBudget
	budgetFrom  string
	flagBudget  dvrBudget
	budgetFlags map[string]bool

	// rail, inspector and dock say whether each pane follows the window-size
	// rule or a person's explicit choice; see workbench.go. The zero value is
	// paneAuto, so an untouched window behaves as the size rule says.
	rail      paneMode
	inspector paneMode
	dock      paneMode

	// frameWidth and frameHeight are the size the last frame laid out at, so
	// a scenario log can name the size it actually drew instead of the size
	// it asked the window for.
	frameWidth, frameHeight float32

	// sync is the shared event clock and the per-track offsets the panel
	// edits; see sync.go.

	// audible holds the audio tracks a person has explicitly allowed to be heard
	// while their place on the shared timeline is still unknown.
	audible map[core.TrackID]bool
	sync    *syncState

	// frames receives one token for every frame the window builds, which is
	// what says a player dropped from the view is no longer being drawn.
	frames chan struct{}

	// frameCount counts the same frames without losing any: the channel drops a
	// token when the buffer is full, the counter does not.
	frameCount int

	// probeRunning and lastProbe describe the one alignment comparison that may
	// be in flight: the button refuses a second one, and a scenario reads the
	// finished suggestion here instead of parsing the status line.
	probeRunning bool
	lastProbe    alignSuggestion
	// lastProbeErr is why the newest comparison did not finish. It is kept
	// beside lastProbe, not instead of it: an older proposal may still describe
	// the session, but a failed attempt must be the first thing the report says.
	lastProbeErr string
	// lastProbeFail keeps the identity of the attempt that failed, so the failure
	// is named after its own sources instead of borrowing the older proposal's.
	lastProbeFail *probeFailure
	// probeAttempt counts started comparisons and probeDone the newest one that
	// finished, so a caller can tell "my attempt finished" from "an attempt once
	// finished".
	probeAttempt uint64
	probeDone    uint64

	// pending holds players that were removed while no other player was alive.
	// See close for why they cannot be released at that moment. A player that
	// was showing a recording carries the lease that keeps its file alive until
	// the player is really closed.
	pendingMu sync.Mutex
	pending   []pendingClose
}

// newAppState builds a session with one scene per given source set, opens a
// player for every source, and applies the first scene.
func newAppState(uris []string) (*appState, error) {
	return newAppStateWithSettings(uris, defaultSettings())
}

// newAppStateWithSettings is the real constructor. The settings have to exist
// before the first source is opened: a remembered level and mute are installed
// while the source is added, and applying them afterwards would let a source
// speak at the default volume for a frame before its own level came back.
func newAppStateWithSettings(uris []string, loaded settings) (*appState, error) {
	// The command line decides which sources exist; the settings file only
	// carries their sound, so a remembered address never opens anything.
	session := core.NewSession("desktop")
	scene, err := session.AddScene("main")
	if err != nil {
		return nil, err
	}
	output, err := session.AddOutput(core.OutputDesktopWindow, scene.ID)
	if err != nil {
		return nil, err
	}
	app := &appState{session: session, scene: scene.ID, output: output.ID, frames: make(chan struct{}, 1), sync: newSyncState(), audible: make(map[core.TrackID]bool), settings: cloneSettings(loaded), settingsDraft: cloneSettings(loaded), settingsSection: "General"}
	for _, uri := range uris {
		it, err := app.addFrom(uri, originCommandLine)
		if err != nil {
			// One unreachable source does not take the session down: the item
			// keeps its open error so the window can report it.
			app.lastErr = err
		}
		app.items = append(app.items, it)
	}
	// The first source that opened is what the window shows and hears until a
	// button says otherwise. Without a video track in the scene nothing is
	// drawn, and a player that is not drawn does not advance in this MyGo
	// build, so this is not cosmetic.
	for _, it := range app.items {
		if it.video != "" {
			if _, err := session.SetSceneVideo(app.scene, it.video); err != nil {
				app.lastErr = err
			}
			break
		}
	}
	for _, it := range app.items {
		if it.audio != "" {
			if _, err := session.SetSceneAudio(app.scene, it.audio, 1, false); err != nil {
				app.lastErr = err
			}
			break
		}
	}
	app.apply()
	return app, nil
}

// add registers one source and the tracks its player exposes, then opens the
// player. It is the manual route: the address came from the page or a check, so
// the source may be remembered once somebody changes its sound.
func (a *appState) add(uri string) (*item, error) {
	return a.addFrom(uri, originAdded)
}

// sourceOrigin says how a source came into the session. A remembered level is
// written back only for a source somebody added by hand: replaying the same
// command line flag on every start must not keep saving the same preference.
type sourceOrigin int

const (
	originAdded sourceOrigin = iota
	originCommandLine
)

func (a *appState) addFrom(uri string, origin sourceOrigin) (*item, error) {
	it := &item{uri: uri, volume: a.settingsSourceVolume(), origin: origin}
	if isNetworkAddress(uri) {
		if err := a.addProbed(it); err != nil {
			return it, err
		}
	} else if err := a.addLocal(it); err != nil {
		return it, err
	}
	// A remembered preference is installed before the first scene is applied, so
	// a source that was muted last time never speaks for a frame at the default
	// volume first.
	a.applySourceMemory(it)
	player, err := openPlayerOnMain(uri)
	if err != nil {
		it.openErr = err
		return it, err
	}
	it.player = player
	it.generation++
	wakeNetworkPlayer(it.uri, rawPlayer(player), a.generationIs(it, it.generation))
	a.closePendingWhenReady()
	return it, nil
}

// generationIs returns a function that reports whether it still owns the player
// of the given generation. A wake task left over from an earlier player stops
// asking as soon as the item opened a new one, so it cannot seek a player that
// was closed or take over one that replaced it.
func (a *appState) generationIs(it *item, generation int) func() bool {
	return func() bool {
		var current bool
		mygoRunOnMain(func() { current = it.generation == generation && it.player != nil })
		return current
	}
}

// addProbed reads an HLS address with core/hls, so the tracks in the session
// are the ones the stream really declares.
func (a *appState) addProbed(it *item) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := hls.Probe(ctx, it.uri, hls.Options{})
	if err != nil {
		it.openErr = err
		return err
	}
	return a.attachStream(it, stream)
}

// attachStream puts the tracks a probed stream declared into the session. It is
// separate from the probe so an import can read the origin off the drawing
// thread and only then touch the session, which is not safe to write from two
// threads.
func (a *appState) attachStream(it *item, stream hls.Stream) error {
	source, attachments, err := stream.Attach(a.session)
	if err != nil {
		it.openErr = err
		return err
	}
	it.source = source
	for _, attachment := range attachments {
		switch attachment.Track.Kind {
		case core.MediaVideo:
			if it.video == "" {
				it.video = attachment.Track.ID
			}
		case core.MediaAudio:
			if it.audio == "" {
				it.audio = attachment.Track.ID
			}
		}
	}
	return nil
}

// addLocal registers a local file. MyGo does not enumerate the tracks of a
// file it opens, so the session records the one video and one audio stream the
// player takes from it, and says so in the labels.
func (a *appState) addLocal(it *item) error {
	source, err := a.session.AddSource(it.uri)
	if err != nil {
		it.openErr = err
		return err
	}
	it.source = source
	videoTrack, err := a.session.AddTrack(source.ID, core.MediaVideo, "video (player does not enumerate tracks)")
	if err != nil {
		return err
	}
	audioTrack, err := a.session.AddTrack(source.ID, core.MediaAudio, "audio (player does not enumerate tracks)")
	if err != nil {
		return err
	}
	it.video = videoTrack.ID
	it.audio = audioTrack.ID
	return nil
}

// importSource adds a source without freezing the window. Reading an HLS
// address takes as long as the origin takes, so the probe runs off the drawing
// thread; everything that writes the session or opens a player happens back on
// it. The outcome is reported as a command result, so the status line and the
// log say what happened to the import that was asked for.
func (a *appState) importSource(ctx context.Context, uri string, token int) {
	var (
		stream   hls.Stream
		probed   bool
		probeErr error
	)
	if isNetworkAddress(uri) {
		stream, probeErr = hls.Probe(ctx, uri, hls.Options{})
		probed = probeErr == nil
		if probeErr != nil && ctx.Err() != nil {
			probeErr = fmt.Errorf("reading %s stopped: %w", shortURI(uri), ctx.Err())
		}
	}
	mygoRunOnMain(func() {
		if token != a.importToken {
			// Cancel already cleared the busy state and said so; this landing is
			// from the read it stopped and must not add anything.
			return
		}
		it := &item{uri: uri, volume: a.settingsSourceVolume()}
		var err error
		switch {
		case probeErr != nil:
			err = probeErr
		case probed:
			err = a.attachStream(it, stream)
		default:
			err = a.addLocal(it)
		}
		if err == nil {
			// An imported address is a source somebody added, so it comes back
			// with its remembered sound, and changing that sound may be
			// remembered in turn.
			a.applySourceMemory(it)
			// The player opens at import, as it does for a source given on the
			// command line: the stage builds a video for every source with a
			// player, and a player created later, in the same pass that first
			// shows it, stayed unready in this build. Opening it here gives it
			// the frames that come before the user shows it.
			var player *ui.VideoPlayer
			player, err = openPlayerOnMain(uri)
			if err == nil {
				it.player = player
				it.generation++
				wakeNetworkPlayer(it.uri, rawPlayer(player), a.generationIs(it, it.generation))
				a.items = append(a.items, it)
				a.selected = it.source.ID
				a.apply()
				a.closePendingWhenReady()
			}
		}
		a.closeImportTicker()
		a.importing = ""
		a.importText = ""
		result := commandResult{Command: command{Kind: commandAdd, URI: uri}, At: time.Now()}
		if err != nil {
			it.openErr = err
			result.Err = err
			result.Message = fmt.Sprintf("could not import %s", shortURI(uri))
			a.lastErr = err
		} else {
			result.Message = fmt.Sprintf("imported %s as %s; it is selected, Show puts it on the stage", shortURI(uri), it.source.ID)
		}
		a.lastCommand = result
		a.report(result)
	})
}

// tickImport keeps the window repainting while a read is in flight, so the
// elapsed seconds beside Reading… actually move. It runs off the drawing thread
// and asks for a frame on it, which is the only thread allowed to do that.
func (a *appState) tickImport(done chan struct{}) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			mygoRunOnMain(func() {
				if a.window != nil {
					a.window.Invalidate()
				}
			})
		}
	}
}

// closeImportTicker stops the repaint loop, if one is running. Safe to call
// when it never started; closing the same channel twice is not, so the field is
// cleared as it is closed.
func (a *appState) closeImportTicker() {
	if a.importDone != nil {
		close(a.importDone)
		a.importDone = nil
	}
	if a.importCancel != nil {
		a.importCancel()
		a.importCancel = nil
	}
}

// apply maps the current scene onto the players: the video tracks in the scene
// are drawn, everything else is hidden, and each source's player takes the
// level the scene gives its audio track, muted when it is not in the scene.
func (a *appState) apply() {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		a.lastErr = err
		return
	}
	shown := make(map[core.TrackID]bool, len(scene.Video))
	for _, track := range scene.Video {
		shown[track] = true
	}
	levels := make(map[core.TrackID]core.AudioLevel, len(scene.Audio))
	for _, level := range scene.Audio {
		levels[level.Track] = level
	}
	a.dropSoloIfGone()
	for _, it := range a.items {
		if it.player == nil {
			continue
		}
		it.shown = shown[it.video]
		level, ok := levels[it.audio]
		if !ok {
			it.player.SetMuted(true)
			continue
		}
		// A source whose place on the shared timeline is unknown makes no claim
		// about which moment of the match it is playing, so its sound stays off
		// until someone who can see both pictures says it is safe; the mixer
		// overlay adds mute, master mute and solo on top of that.
		muted := level.Muted || it.muted || a.masterMute || (a.solo != "" && it.source.ID != a.solo)
		if !muted && a.protectedSilence(it) {
			muted = true
		}
		it.player.SetMuted(muted)
		if !muted {
			it.player.SetVolume(a.volumeFor(level.Volume))
		}
	}
}

// showVideo makes one source's video the scene's video, keeping the audio
// levels as they are.
func (a *appState) showVideo(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.video == "" {
		return fmt.Errorf("source %d has no video track", index+1)
	}
	if err := a.ensurePlayer(it); err != nil {
		return err
	}
	if _, err := a.session.SetSceneVideo(a.scene, it.video); err != nil {
		return err
	}
	a.apply()
	a.wakeShown(it)
	return nil
}

// ensurePlayer opens the player of a source that has none yet. Sources added
// while the window runs stay closed until something needs them: the player of
// a source that never draws does not become ready, so opening it on import only
// produces a card that says "opening" forever.
func (a *appState) ensurePlayer(it *item) error {
	if it.player != nil {
		return it.openErr
	}
	player, err := openPlayerOnMain(it.uri)
	if err != nil {
		it.openErr = err
		return err
	}
	it.openErr = nil
	it.player = player
	it.generation++
	a.closePendingWhenReady()
	return nil
}

// wakeShown asks one seek of a network player that only became visible now. A
// network source that is shown for the first time after it was opened does not
// deliver its first picture on its own with this build of MyGo, and the seek is
// the only signal observed to make it start.
func (a *appState) wakeShown(it *item) {
	if it == nil || it.player == nil {
		return
	}
	if it.player.Ready() || it.player.Err() != nil {
		return
	}
	wakeNetworkPlayer(it.uri, rawPlayer(it.player), a.generationIs(it, it.generation))
}

// hearAudio makes the scene play one source's audio at the given level and mute
// every other source, so what is heard is unambiguous.
func (a *appState) hearAudio(index int, volume float64) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.audio == "" {
		return fmt.Errorf("source %d has no audio track", index+1)
	}
	if err := a.ensurePlayer(it); err != nil {
		return err
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return err
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio {
			continue
		}
		if _, err := a.session.RemoveSceneAudio(a.scene, level.Track); err != nil {
			return err
		}
	}
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, volume, false); err != nil {
		return err
	}
	// The scene holds the level now; the source's own copy has to agree, or the
	// inspector shows one number while the output plays another, and the
	// remembered preference would save the stale one.
	it.volume = volume
	a.apply()
	return nil
}

// setVolume changes the level of one source's audio track inside the scene.
func (a *appState) setVolume(index int, volume float64) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.audio == "" {
		return fmt.Errorf("source %d has no audio track", index+1)
	}
	if volume < 0 || volume > 1 {
		return fmt.Errorf("volume %.2f is outside 0..1", volume)
	}
	// Changing a level is not routing. A source outside the scene has no level
	// to change, and writing one would put its sound on the output as a side
	// effect of touching a slider, so refuse and point at Hear instead.
	level, routed := a.sceneLevel(it)
	if !routed {
		return fmt.Errorf("source %d is not in the scene, so it has no level to change; use Hear to route it first", index+1)
	}
	// The level that reaches the scene keeps whatever mute the scene already
	// carries: changing a volume must not turn a muted track back on, which is
	// what passing a fresh `false` here would do.
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, volume, level.Muted); err != nil {
		return err
	}
	// The scene now holds the number the controls must show; keeping the item's
	// copy in step is what makes a slider and its label agree on the next frame.
	it.volume = volume
	a.apply()
	a.noteSourcePreference(it)
	return nil
}

// sceneLevel reports the level the scene holds for a source's audio track.
func (a *appState) sceneLevel(it *item) (core.AudioLevel, bool) {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return core.AudioLevel{}, false
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio {
			return level, true
		}
	}
	return core.AudioLevel{}, false
}

// hearBoth makes the scene play two sources' audio together, each at its own
// level, which is what independent volume control means here.
func (a *appState) hearBoth(first, second int, firstVolume, secondVolume float64) error {
	a1, err := a.at(first)
	if err != nil {
		return err
	}
	a2, err := a.at(second)
	if err != nil {
		return err
	}
	if a1.audio == "" || a2.audio == "" {
		return fmt.Errorf("source %d or %d has no audio track", first+1, second+1)
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return err
	}
	for _, level := range scene.Audio {
		if level.Track == a1.audio || level.Track == a2.audio {
			continue
		}
		if _, err := a.session.RemoveSceneAudio(a.scene, level.Track); err != nil {
			return err
		}
	}
	if _, err := a.session.SetSceneAudio(a.scene, a1.audio, firstVolume, false); err != nil {
		return err
	}
	if _, err := a.session.SetSceneAudio(a.scene, a2.audio, secondVolume, false); err != nil {
		return err
	}
	a.apply()
	return nil
}

// remove deletes a source from the session and closes its player. The session
// drops its tracks and every scene reference; the player is closed only after
// the window stopped drawing it.
func (a *appState) remove(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if err := a.session.RemoveSource(it.source.ID); err != nil {
		return err
	}
	a.close(it)
	a.items = append(a.items[:index], a.items[index+1:]...)
	a.apply()
	return nil
}

// at is the item at a zero-based index.
func (a *appState) at(index int) (*item, error) {
	if index < 0 || index >= len(a.items) {
		return nil, fmt.Errorf("source %d: %w", index+1, core.ErrNotFound)
	}
	return a.items[index], nil
}

// close stops drawing a player and then closes it.
//
// The order matters: a player closed while a frame still draws it crashes the
// process inside Media Foundation, so the view drops it first and the close
// waits for frames the window really built, not for a fixed sleep. The drop and
// the close both run on the drawing thread, and the player pointer is cleared
// there too, so the view cannot read a half-released player.
//
// A player that is the last live one in the window is not closed here. In this
// MyGo build, once the last video player of a window is closed, a player opened
// afterwards reports its duration but never gets a picture or moves its
// position: the window keeps a Media Foundation resource it does not rebuild.
// The smallest reproduction (testplace/poc/onevid -reopen) fails the same way,
// and opening the new player before closing the old one works. So the last one
// is paused and kept until another player is ready, and the pending close runs
// then.
func (a *appState) close(it *item) {
	var kept *handover
	var previous *ui.VideoPlayer
	var swap *replaySwap
	var lease *dvr.Lease
	mygoRunOnMain(func() {
		kept = it.handover
		previous = rawPlayer(it.previous)
		swap = it.swap
		lease = it.replayLease
		it.handover = nil
		it.previous = nil
		it.swap = nil
		it.replayLease = nil
		it.replay = nil
	})
	if swap != nil {
		// The replacement never became the item's player, so this close is the
		// only owner of it and of the lease on the file it was handed. A
		// rollback that is still waiting finds the work done and reports its
		// own reason without touching the lease counters again.
		a.finishSwap(swap)
	}
	switch {
	case kept != nil:
		// The handover that was waiting owns the player it kept drawn; a close
		// here takes that player over instead of closing it twice later.
		a.releasePlayer(kept.take())
	case previous != nil:
		a.releasePlayer(previous)
	}
	if it.player == nil {
		if lease != nil {
			a.releaseLease(lease, "of the recording on close")
		}
		return
	}
	var player *ui.VideoPlayer
	mygoRunOnMain(func() {
		it.shown = false
		it.generation++
		player = rawPlayer(it.player)
		it.player = nil
	})
	a.waitFrames(3)
	if a.livePlayers() > 0 {
		mygoRunOnMain(func() { player.Close() })
		if lease != nil {
			a.releaseLease(lease, "of the recording on close")
		}
		return
	}
	mygoRunOnMain(func() { player.Pause() })
	a.pendingMu.Lock()
	a.pending = append(a.pending, pendingClose{player: player, lease: lease})
	a.pendingMu.Unlock()
	a.closePendingWhenReady()
}

// pendingClose is a player that was paused because it was the last live one,
// together with the lease on the recording it was reading. The lease is given
// back after the player is really closed, not when the item is removed: the file
// has to stay readable for as long as the player holds it open.
type pendingClose struct {
	player *ui.VideoPlayer
	lease  *dvr.Lease
}

// releaseLease gives a recording lease back and counts it done.
func (a *appState) releaseLease(lease *dvr.Lease, where string) {
	if lease == nil {
		return
	}
	if err := lease.Release(); err != nil {
		logf("replay", "release the lease %s: %v", where, err)
	}
	a.leftLease()
}

// livePlayers counts players still open for a source in this session.
func (a *appState) livePlayers() int {
	n := 0
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if it.player != nil {
				n++
			}
		}
	})
	return n
}

// applySourceMemory installs a remembered level and mute on a source. It reads
// only when memory is on: with the switch off the file's entries stay as they
// are and are not used, which is what "inactive" on the settings page means.
func (a *appState) applySourceMemory(it *item) {
	memory := a.settings.SourceMemory
	if !memory.On {
		return
	}
	identity := identityOf(it.uri)
	if identity == "" {
		return
	}
	volume, muted, ok := memory.lookup(identity)
	if !ok {
		return
	}
	it.volume = volume
	if muted {
		// The source's own mute, not the unaligned protection: that one is
		// decided by the timeline and the Allow button on every start.
		it.muted = true
	}
	log.Printf("source memory: %s comes back at %.0f%%%s", identityDigest(identity), volume*100, muteWord(muted))
}

// muteWord is the tail of the log line above, so a remembered mute is visible
// without looking at the page.
func muteWord(muted bool) string {
	if muted {
		return ", muted"
	}
	return ""
}

// noteSourcePreference remembers the sound of a source in the background: the
// write must not block the drawing thread that just moved a slider.
func (a *appState) noteSourcePreference(it *item) {
	go a.rememberSource(it)
}

// rememberSource writes the level and mute now in force for the source's
// address. It is the only write path, so the four refusals live here:
// memory off, remembering off, a source started from the command line, and an
// address that cannot be an identity.
func (a *appState) rememberSource(it *item) {
	memory := a.settings.SourceMemory
	if !memory.On || !memory.Remember {
		return
	}
	if it.origin == originCommandLine {
		return
	}
	identity := identityOf(it.uri)
	if identity == "" {
		return
	}
	changed, refusal := a.settings.SourceMemory.set(identity, it.volume, it.muted)
	if refusal != "" {
		log.Printf("source memory: %s", refusal)
		return
	}
	if !changed {
		return
	}
	// The applied settings own the copy that will be saved; the draft is left
	// alone so a page someone is editing is not rewritten underneath them.
	a.settings = cloneSettings(a.settings)
	if err := saveSettings(a.settingsPath, a.settings); err != nil {
		log.Printf("source memory: could not save %s: %v", identityDigest(identity), err)
		return
	}
	a.settingsSaved = true
	log.Printf("source memory: remembered %s at %.0f%%%s", identityDigest(identity), it.volume*100, muteWord(it.muted))
}

// closePendingWhenReady releases the players kept because they were the last
// live one, as soon as another player actually has a picture.
func (a *appState) closePendingWhenReady() {
	a.pendingMu.Lock()
	waiting := len(a.pending)
	a.pendingMu.Unlock()
	if waiting == 0 || a.livePlayers() == 0 {
		return
	}
	go func() {
		for i := 0; i < 40; i++ {
			ready := false
			mygoRunOnMain(func() {
				for _, it := range a.items {
					if it.player != nil && it.player.Ready() {
						ready = true
					}
				}
			})
			if ready {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		a.pendingMu.Lock()
		wait := a.pending
		a.pending = nil
		a.pendingMu.Unlock()
		if len(wait) == 0 {
			return
		}
		mygoRunOnMain(func() {
			for _, close := range wait {
				close.player.Close()
			}
		})
		for _, close := range wait {
			a.releaseLease(close.lease, "of a player that was closed late")
		}
	}()
}

// waitFrames blocks until the window built n frames after this call, or until
// it stops building them.
func (a *appState) waitFrames(n int) {
	for i := 0; i < n; i++ {
		select {
		case <-a.frames:
		case <-time.After(2 * time.Second):
			return
		}
	}
}

// signalFrame records that the window built a frame, so a close that just
// dropped a player can wait for the view to stop drawing it. The counter next to
// it lets a scenario prove the window kept drawing while a probe ran.
func (a *appState) signalFrame() {
	a.frameCount++
	select {
	case a.frames <- struct{}{}:
	default:
	}
}

// framesBuilt reports how many frames the window has drawn. It is read on the UI
// thread, where the counter is written.
func (a *appState) framesBuilt() int {
	return a.frameCount
}

// release closes every player and the session.
// release shuts the run down in the one order that keeps a recording readable:
// stop producing files, close the players that read them, give the leases back,
// let the buffers remove what nobody holds, then close the sessions. Closing the
// same way in pieces — session first, say — would delete a file a lease was
// still guarding. Both a scenario and the window call it, so it runs once.
func (a *appState) release() {
	a.releaseOnce.Do(a.shutdown)
}

func (a *appState) shutdown() {
	// A transaction still preparing owns files and players of its own; give them
	// back before the sources they were cut from are closed.
	a.shutdownReplayTxn()
	a.stopRecording()
	log.Printf("shutdown: recording stopped; closing %d player(s)", len(a.items))
	for _, it := range a.items {
		a.close(it)
	}
	a.pendingMu.Lock()
	wait := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	if len(wait) > 0 {
		mygoRunOnMain(func() {
			for _, close := range wait {
				close.player.Close()
			}
		})
		for _, close := range wait {
			a.releaseLease(close.lease, "left open at shutdown")
		}
	}
	if pending := a.releaseLeases(); len(pending) > 0 {
		log.Printf("shutdown: the system still holds %s", strings.Join(pending, ", "))
	}
	a.closeBuffers()
	if left := a.retryCleanup(); len(left) > 0 {
		log.Printf("shutdown: the system still holds %s", strings.Join(left, ", "))
	}
	if a.session != nil && !a.session.Closed() {
		a.lastErr = a.session.Close()
	}
	if a.dvrSession != nil {
		if err := a.dvrSession.Close(); err != nil && a.lastErr == nil {
			a.lastErr = err
		}
		a.dvrSession = nil
	}
}

// trackRunner records a buffer so shutdown reaches it after the players.
func (a *appState) trackRunner(uri string, runner *dvrRunner) *dvrRunner {
	a.runners = append(a.runners, trackedRunner{uri: uri, runner: runner})
	return runner
}

func (a *appState) stopRecording() {
	for _, tracked := range a.runners {
		tracked.runner.stopRecording()
	}
}

func (a *appState) releaseLeases() []string {
	var pending []string
	for _, tracked := range a.runners {
		left, err := tracked.runner.releaseHold()
		if err != nil {
			log.Printf("shutdown: release the lease on %s: %v", shortURI(tracked.uri), err)
		}
		pending = append(pending, left...)
	}
	return pending
}

func (a *appState) closeBuffers() {
	for _, tracked := range a.runners {
		err := tracked.runner.Close()
		if err == nil {
			continue
		}
		// A close that failed because a handle was still open is not the last
		// word: the holder may have let go since. Say which of the two happened
		// instead of reporting a failure that no longer exists.
		if left := tracked.runner.retryCleanup(); len(left) == 0 {
			log.Printf("shutdown: the buffer for %s reported %v; the file was released before exit",
				shortURI(tracked.uri), err)
			continue
		}
		log.Printf("shutdown: close the buffer for %s: %v", shortURI(tracked.uri), err)
	}
}

// retryCleanup gives a file whose handle closed late a bounded few chances to go
// away before the process exits, and reports what is still held. The wait is
// short on purpose: shutdown must not hang on a file another program keeps open.
func (a *appState) retryCleanup() []string {
	const attempts = 3
	const pause = 250 * time.Millisecond
	var left []string
	for attempt := 1; attempt <= attempts; attempt++ {
		left = left[:0]
		for _, tracked := range a.runners {
			left = append(left, tracked.runner.retryCleanup()...)
		}
		if len(left) == 0 {
			return nil
		}
		log.Printf("shutdown: cleanup attempt %d/%d left %s",
			attempt, attempts, strings.Join(left, ", "))
		if attempt < attempts {
			time.Sleep(pause)
		}
	}
	return left
}

// dvrDirFor names one buffer directory inside this run's session, so every
// segment and recording this process writes carries the session's identity.
func (a *appState) dvrDirFor(name string) string {
	if a.dvrSession != nil {
		return filepath.Join(a.dvrSession.SegmentsDir(), name)
	}
	return filepath.Join(a.dvrRoot, name)
}

// dvrRecordingsDirFor names the directory a buffer keeps its published
// recordings in. It sits beside the segments rather than inside them, so a
// recording is never mistaken for a rolling segment or swept up with one.
func (a *appState) dvrRecordingsDirFor(name string) string {
	if a.dvrSession != nil {
		return filepath.Join(a.dvrSession.RecordingsDir(), name)
	}
	return filepath.Join(a.dvrRoot, name, "recordings")
}

// sceneLabel describes the scene by source number, which is what the cards and
// the web page both show: "picture 2 · sound 1 at 1.00 · 3 at 0.50".
func (a *appState) sceneLabel() string {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return "scene: " + err.Error()
	}
	picture := "none"
	for i, it := range a.items {
		if it.video != "" && containsTrack(scene.Video, it.video) {
			picture = itoa(i + 1)
			break
		}
	}
	audio := make([]string, 0, len(scene.Audio))
	for _, level := range scene.Audio {
		if i := itemIndex(a.items, level.Track); i >= 0 {
			audio = append(audio, fmt.Sprintf("%d at %.2f", i+1, level.Volume))
		}
	}
	if len(audio) == 0 {
		return "picture " + picture + " · silent"
	}
	return "picture " + picture + " · sound " + strings.Join(audio, " + ")
}

// itemIndex finds the card that owns a track, so the window can name sources by
// the number it prints instead of by the file behind them.
func itemIndex(items []*item, track core.TrackID) int {
	for i, it := range items {
		if it.video == track || it.audio == track {
			return i
		}
	}
	return -1
}

// containsTrack reports whether the track is in the list.
func containsTrack(tracks []core.TrackID, want core.TrackID) bool {
	for _, track := range tracks {
		if track == want {
			return true
		}
	}
	return false
}

// trackURI is the address of the source that owns an audio track.
func trackURI(items []*item, track core.TrackID) string {
	for _, it := range items {
		if it.audio == track {
			return it.uri
		}
	}
	return ""
}

// shortURI is the last path element, with the directory before it when that
// element is a playlist, so a status line stays readable and still tells two
// index.m3u8 addresses apart.
// shortURI names a source the way a person does: the file, with the directory
// that makes it unique when several sources share a file name. Windows paths
// use backslashes, so they are folded onto the same separator first.
func shortURI(uri string) string {
	if uri == "" {
		return "none"
	}
	trimmed := strings.TrimSuffix(strings.ReplaceAll(uri, "\\", "/"), "/")
	parts := strings.Split(trimmed, "/")
	last := parts[len(parts)-1]
	if strings.HasPrefix(last, "index.") && len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + last
	}
	return last
}
