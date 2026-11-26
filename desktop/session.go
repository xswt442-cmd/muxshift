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
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	// persistentID identifies this source in saved scenes independently of the
	// Core ID, which is allocated afresh whenever a session is rebuilt.
	persistentID  string
	persistedName string
	restored      bool

	// origin says whether the address came from the command line; only a source
	// somebody added by hand writes its level back to the settings file.
	origin sourceOrigin

	source core.Source
	video  core.TrackID
	audio  core.TrackID

	player              desktopPlayer
	openErr             error
	restoring           bool
	restoringGeneration uint64
	// lifecyclePending is set while a stage transition closes this player's
	// resources off the drawing thread. It prevents a second transition from
	// starting a competing close for the same player.
	lifecyclePending bool
	resumePosition   time.Duration
	resumePositionOK bool
	resumePaused     bool
	stageOnly        bool
	playlistMaster   string
	mediaMenu        bool
	mediaLoading     bool
	mediaCancel      context.CancelFunc
	mediaChoices     []liveVariant
	mediaError       string
	mediaReplaceErr  error

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

// itemKey is the stable identifier the UI and command boundary can use before
// an unavailable saved source has been registered into Core.
func itemKey(it *item) core.SourceID {
	if it == nil {
		return ""
	}
	if it.source.ID != "" {
		return it.source.ID
	}
	if it.persistentID != "" {
		return core.SourceID("saved:" + it.persistentID)
	}
	return ""
}

func (it *item) canRestoreOnDemand() bool {
	return it.source.ID == "" && strings.TrimSpace(it.uri) != ""
}

func (a *appState) updateRecoveredSourceKey(it *item, oldKey core.SourceID) {
	if it == nil {
		return
	}
	newKey := itemKey(it)
	savedKey := core.SourceID("saved:" + it.persistentID)
	legacyKey := core.SourceID(it.persistentID)
	if a.selected == oldKey || a.selected == savedKey || a.selected == legacyKey {
		a.selected = newKey
	}
	if a.reselectSource == oldKey || a.reselectSource == savedKey || a.reselectSource == legacyKey {
		a.reselectSource = newKey
	}
}

type pendingSceneBinding struct {
	video      bool
	videoIndex int
	audio      *storedAudio
	audioIndex int
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

	// bgMu guards the background failure ledger: bgFailures counts what went
	// wrong away from the drawing thread this run, bgLast keeps the newest one.
	// Clearing the status bar's message leaves both alone; see background.go.
	bgMu       sync.Mutex
	bgFailures int
	bgLast     backgroundFailure

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

	// probe, when set, replaces the whole workbench with the canvas
	// experiment: two videos, two rectangles, nothing else. Only the -scenario
	// canvas run sets it, so a person's window never sees this branch.
	probe *canvasProbe

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
	afterSeekCommit  func()

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
	// requestFrame asks for a frame where a window exists. Tests run without
	// one and count the requests instead; see invalidate.
	requestFrame func()

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
	// importFailure remains available to the Sources dock even after another
	// command replaces lastCommand.
	importFailureURI string
	importFailureErr error

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
	importing           string
	retryTargetID       string
	reselectSource      core.SourceID
	reselectText        string
	reselectProblem     string
	startupMediaQueue   []string
	startupMediaScene   core.SceneID
	pendingImportOrigin sourceOrigin
	importScene         core.SceneID

	// The shell's own state. systemLang is the window's locale, resolved once on
	// the first frame, and is what "follow the system" means. applied* remember
	// which appearance the token vars were last rebuilt from, so a settings
	// change is noticed once instead of every frame.
	systemLang   string
	appliedTheme string
	appliedScale string

	// The panels the shell can show without a page change: the error report, the
	// page menu, the scene menu and the narrow window's properties drawer.
	errorOpen  bool
	pageMenu   bool
	sceneMenu  bool
	drawerOpen bool

	// The shell's three parts are sized by the person using them: the scene
	// column keeps its own width, the console keeps its own height, and each
	// console panel can be hidden on its own.
	navWidth   float32
	navUp      bool
	dockHeight float32
	dockUp     bool
	dockHidden map[string]bool
	// syncRef is the source the other ones are lined up against. Empty means
	// "the first picture on the stage", which is what the console shows.
	syncRef core.SourceID
	// winW is the width the shell drew at last frame; the console uses it to
	// decide between three panels and tabs.
	winW    float32
	dockTab int
	// dockWidths is the share of the console each panel holds, and dockW is the
	// console's width in DIP, so a dragged seam can turn pixels into a share.
	dockWidths map[string]float32
	dockW      float32
	// dockSeam remembers how far each dragged seam has travelled, so only the
	// new part of the travel moves the panels.
	dockSeam map[string]float32
	// editingShell mounts the editing columns. The workbench is what the window
	// is: the scene list, the picture and the console are the product, and the
	// bare player is a mode the person switches to.
	editingShell bool
	// propsTab is the properties panel's tab: 0 picture, 1 sound, 2 time.
	propsTab int

	// editMode is the stage's editing switch: handles and the properties
	// form are live while it is on.
	editMode bool
	// mixerOpen folds the mixer panel in the right column.
	mixerOpen bool

	// closed is set by the window's close handler. A landing that arrives after
	// it has nothing to write into: the session and the players were released.
	closed bool

	// openPlayerForImport lets a test make the player open fail at a known
	// point; the real path opens the player on the drawing thread.
	openPlayerForImport func(string) (*ui.VideoPlayer, error)

	// addOpen, addChoice, addNote and addSiteText belong to the "add a source"
	// chooser P1 puts behind both the top bar's Open… and the Sources page's
	// Add. The chooser asks what a person has instead of guessing from the
	// text: a file, a media address, or a website a browser opens. The file
	// choice opens the desktop's picker (P2), the media choice reaches the add
	// command, and the website choice says what this build does not do yet.
	// See addsource.go.
	addOpen     bool
	addChoice   addChoice
	addNote     string
	addNoteBad  bool
	addSiteText string
	// siteOpened records that the last website attempt did reach a browser, so
	// the panel can say where the media still is instead of looking finished.
	siteOpened bool

	// pickingFile is true while the desktop file picker is open, so one
	// picker at a time. pickFile is the seam tests use: it returns the path
	// the picker would have returned, or "" when cancelled.
	pickingFile bool
	pickFile    func() (string, error)
	// onMain runs the picker's answer on the drawing thread; tests have no
	// window loop and replace it with a direct call.
	onMain func(func())
	// openURL asks the system browser for a page; a test can put a recorder in
	// its place, and contextOpenURL is what the panel hands over each frame.
	openURL        func(string, func(error))
	contextOpenURL func(string, func(error))
	// openFolder shows the browser extension's directory in the shell. It is a
	// test seam like openURL; exeDir overrides where this build says it runs
	// from, which only a test needs, and extensionDir caches the folder once it
	// has been found so the About page need not stat the disk every frame.
	openFolder   func(string) error
	exeDir       string
	extensionDir string

	// handoff is the loopback listener a browser extension pairs with; it is
	// nil when that listener could not start, and handoffErr says why (P5).
	handoff    *handoffServer
	handoffErr string

	// The About page's update check runs off the drawing thread; these fields
	// are read through updateMu.
	updateMu      sync.Mutex
	updateBusy    bool
	updateChecked bool
	updateLatest  string
	updateURL     string
	updateErr     string

	// seekText and seekNote belong to the inspector's replay seek entry: the
	// first is what the small field holds, the second the sentence the last
	// attempt produced. Neither is a decision; PlanSeek still decides.
	seekText string
	seekNote string

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

	// settings is what is applied and running; settingsDraft is what the Settings
	// page edits. Appearance controls update both through their immediate path;
	// the other draft values wait for Apply. settingsPath is where applied values
	// are written, and settingsSaved says this start has written that file, so the
	// page can be exact about the persistence boundary.
	settings      settings
	settingsDraft settings
	dvrInputs     [3]dvrDraftInput
	settingsPath  string
	settingsSaved atomic.Bool
	settingsErr   string
	settingsNote  string
	// draftCleared marks a Settings draft that asked to forget the remembered
	// sources. Only such a draft may apply an empty list; every other Apply
	// merges what the live settings hold instead of deleting what was
	// remembered while the page was open.
	draftCleared    bool
	settingsSection string
	// settingsTrusted says the file on disk was read and understood. A start
	// that fell back to the defaults must not write over the person's file with
	// its defaults, so the workspace memory checks this before saving.
	settingsTrusted bool
	// settingsSave serializes the two writers: Apply on the drawing thread and
	// a remembered preference saving from its goroutine. saveSeq hands out the
	// order of snapshots and saveDone is the last ticket actually written, so a
	// write that lost the race cannot replace a newer file.
	settingsSave sync.Mutex
	saveSeq      atomic.Uint64
	saveDone     atomic.Uint64

	// Session snapshots are coalesced off the drawing thread. The mutex protects
	// the newest pending copy and its debounce timer; timer and shutdown writes
	// share this one serialized path.
	sessionSaveMu         sync.Mutex
	sessionSavePending    *storedSession
	sessionSavePendingSeq uint64
	sessionSaveTimer      *time.Timer
	sessionSaveWriteMu    sync.Mutex
	sessionStoreWriter    func(string, storedSession) error
	sessionSaveNotify     func(func())
	sessionSaveSeq        uint64
	sessionSaveDone       uint64

	// sessionPath is where the session snapshot lives, derived from the settings
	// path so the two files sit together; empty when there is nowhere to write.
	// See session_store.go.
	sessionPath         string
	sessionWriteEnabled bool
	sessionErr          string
	sourceIdentitySeq   uint64
	pendingBindings     map[core.SceneID]map[string]pendingSceneBinding
	pendingStageEdits   []storedStageEdits
	pendingSyncGroups   []storedSyncGroup
	pendingShifts       []storedShift
	persistentSceneIDs  map[string]core.SceneID
	scenePersistentIDs  map[core.SceneID]string
	pendingLayoutOrder  []string
	sceneLayouts        map[core.SceneID]storedSceneLayout
	// sceneMembers records linked sources independently from video visibility
	// and audio routing. Each scene maps persistent source IDs to membership.
	sceneMembers         map[core.SceneID]map[string]bool
	stageCatalogOpen     bool
	startupSources       []storedSource
	startupRestoreCancel context.CancelFunc
	startupRestoreToken  uint64
	stageGeneration      uint64
	retryScene           core.SceneID
	retryStageGeneration uint64

	// The rename dialog edits one scene's or one source's display name. It is
	// kept here so a redraw does not lose what was typed; see rename.go.
	renameOpen bool
	renameKind renameTarget
	renameID   string
	renameText string

	// budget is the DVR budget a buffer opened now would get, and budgetFrom
	// names the rule that produced it: the command line, the settings file or
	// the built-in defaults. flagBudget and budgetFlags keep what the flags were
	// and which of them a person actually typed, because a flag left at its
	// default is not an override (P18.2).
	budget      dvrBudget
	budgetFrom  string
	flagBudget  dvrBudget
	budgetFlags map[string]bool
	// dvrNote is the outcome of the last cleanup pass, kept beside the button so
	// a file that stayed on disk says why it stayed.
	dvrNote string

	// rail, inspector and dock say whether each pane follows the window-size
	// rule or a person's explicit choice; see workbench.go. The zero value is
	// paneAuto, so an untouched window behaves as the size rule says.
	rail      paneMode
	inspector paneMode
	dock      paneMode

	// layout is how the stage arranges the pictures the scene shows; see
	// layout.go. It never changes which sources are in the scene, what the
	// output hears, or where the players are — only where the pictures sit.
	layout layoutKind

	// layoutTaken holds the sources a person explicitly asked to see, in the
	// order they asked. When it is empty the stage falls back to the scene
	// order, so a scene edited elsewhere still draws something.
	layoutTaken []core.SourceID

	// edits holds each stage's picture positions, stacking order and locks.
	// Session snapshots use stable scene and source IDs for this arrangement.
	edits map[core.SceneID]*stageEdits

	// drag is the gesture in progress: which source, which handle, and the
	// stage size the press happened in, so a drag keeps following the hand
	// even if the window is resized mid-gesture.
	drag       dragState
	freeRatio  bool
	editNote   string
	hoverX     float32
	hoverY     float32
	hoverKnown bool
	boxFields  boxFields

	// stageW and stageH are the stage's size in the frame just built, kept so
	// a keyboard nudge or a gesture can turn a pixel step into a fraction.
	// stageAt is where the stage sits in the window, which is what a mouse
	// script needs to aim at a picture.
	stageW  float32
	stageH  float32
	stageAt stageBox

	// viewing is the P10 watching state and savedForViewing is what the
	// workbench looked like when a watching mode was entered, so leaving can
	// put the window back. onTop is compact mode's switch, kept here so the
	// exit path can restore the state the window had before.
	viewing         viewingMode
	viewingReturn   viewingMode
	savedForViewing viewingSaved
	onTop           bool

	// The overlay in a watching mode hides itself after controlsIdle without a
	// pointer move. controlsShown is what the last frame drew, controlsAt when
	// the pointer last moved, controlsHovered whether the pointer rests on the
	// bar itself, controlsFocused whether a keyboard user has a button in it,
	// and idleTick makes an older hide timer harmless.
	controlsShown      bool
	controlsAt         time.Time
	controlsHovered    bool
	controlsFocused    bool
	focusControls      bool
	idleTick           uint64
	pointerX, pointerY float32
	pointerKnown       bool

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
	alignRow     alignRowResult
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
	// probeCtx and probeCancel own the comparison in flight. Closing the session
	// cancels it, so the ffmpeg child ends with the window instead of decoding
	// three more minutes against a session nobody can see. probeChild is the
	// child's pid, written by the goroutine that starts it: the checks read it to
	// prove the process is really gone, which a "cancelled" string cannot.
	probeCtx    context.Context
	probeCancel context.CancelFunc
	probeChild  atomic.Int64

	// pending holds players that were removed while no other player was alive.
	// See close for why they cannot be released at that moment. A player that
	// was showing a recording carries the lease that keeps its file alive until
	// the player is really closed.
	pendingMu sync.Mutex
	pending   []pendingClose
	retiring  map[*mediaRetirement]struct{}
	closeDone chan struct{}
	closeWait sync.WaitGroup
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
	scene, err := session.AddScene("")
	if err != nil {
		return nil, err
	}
	output, err := session.AddOutput(core.OutputDesktopWindow, scene.ID)
	if err != nil {
		return nil, err
	}
	app := newAppShell(session, scene.ID, output.ID, loaded)
	for _, uri := range uris {
		it, err := app.addFrom(uri, originCommandLine)
		if err != nil {
			// One unreachable source does not take the session down: the item
			// keeps its open error so the window can report it.
			app.lastErr = safeOpenError(err)
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
				app.lastErr = safeOpenError(err)
			}
			break
		}
	}
	for _, it := range app.items {
		if it.audio != "" {
			// Each source is routed at its own level, which is where a
			// remembered preference already sits. Routing at 1 here would make
			// the memory a decoration: the scene would say 100% and the player
			// would be told 100%, whatever the file remembered. Every source is
			// routed, not just the first, so several command-line sources mix
			// together instead of only the first one being heard (P0-1).
			if _, err := session.SetSceneAudio(app.scene, it.audio, it.volume, false); err != nil {
				app.lastErr = safeOpenError(err)
			}
		}
	}
	app.apply()
	return app, nil
}

// newAppShell builds the bare application around a session: the fields every
// start needs, and the shell's opening layout (scenes on the left, the console
// under the picture, every panel of it visible). Both the command-line start and
// the saved-session start grow from here, so a restored window looks the same.
func newAppShell(session *core.Session, scene core.SceneID, output core.OutputID, loaded settings) *appState {
	app := &appState{session: session, scene: scene, output: output, frames: make(chan struct{}, 1), closeDone: make(chan struct{}), sync: newSyncState(), audible: make(map[core.TrackID]bool), settings: cloneSettings(loaded), settingsDraft: cloneSettings(loaded), settingsSection: "General"}
	app.navWidth = 236
	app.navUp = true
	app.editingShell = true
	app.dockHeight = 240
	app.dockUp = loaded.DockOpen
	app.dockHidden = make(map[string]bool)
	return app
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
	player, err := a.openImportedPlayer(uri)
	if err != nil {
		a.dropUnreachableSource(it)
		it.openErr = safeOpenError(err)
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
		it.openErr = safeOpenError(err)
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
		it.openErr = safeOpenError(err)
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
	// A local choice names a file that must already be there: a path that does
	// not exist, a directory or a device is refused before the session grows a
	// source that could never play.
	info, err := os.Stat(it.uri)
	if err != nil {
		it.openErr = safeOpenError(err)
		return fmt.Errorf("no file at %s: %w", shortURI(it.uri), err)
	}
	if info.IsDir() {
		it.openErr = fmt.Errorf("%s is a directory, not a media file", shortURI(it.uri))
		return it.openErr
	}
	if !info.Mode().IsRegular() {
		it.openErr = fmt.Errorf("%s is not a regular file", shortURI(it.uri))
		return it.openErr
	}
	return a.registerUnenumerated(it)
}

// addDirect registers an http(s) address that serves a media file rather than a
// playlist. The player opens it the way it opens a local file and does not
// enumerate its tracks either, so the session records the same two streams and
// says so in the labels.
func (a *appState) addDirect(it *item) error {
	return a.registerUnenumerated(it)
}

// registerUnenumerated adds a source whose tracks the player does not list: one
// video and one audio stream, labelled so a reader knows where they came from.
func (a *appState) registerUnenumerated(it *item) error {
	source, err := a.session.AddSource(it.uri)
	if err != nil {
		it.openErr = safeOpenError(err)
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

// importMode says how an import reached the session. A local path is recorded as
// the one video and one audio stream the player takes from it; a playlist
// brings the tracks it declared; a plain media address is recorded like a local
// file, because the player does not enumerate its tracks either.
type importMode int

const (
	importLocal importMode = iota
	importProbed
	importDirect
)

type importPlan struct {
	uri    string
	stream hls.Stream
	mode   importMode
	live   bool
	master string
}

func inspectImport(ctx context.Context, uri string) (importPlan, error) {
	plan := importPlan{uri: uri}
	if !isNetworkAddress(uri) {
		return plan, nil
	}
	kind, err := sniffAddress(ctx, uri)
	if err != nil {
		return plan, err
	}
	if kind != addressPlaylist {
		plan.mode = importDirect
		return plan, nil
	}
	role, err := inspectPlaylist(ctx, uri)
	if err != nil {
		return plan, err
	}
	if role.live {
		plan.uri = role.address
		plan.mode = importDirect
		plan.live = true
		plan.master = role.master
		return plan, nil
	}
	plan.stream, err = hls.Probe(ctx, uri, hls.Options{})
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("reading %s stopped: %w", shortURI(uri), ctx.Err())
		}
		return plan, err
	}
	plan.mode = importProbed
	return plan, nil
}

// importSource adds a source without freezing the window. Reading a network
// address takes as long as the origin takes, so both the sniff and the probe run
// off the drawing thread; everything that writes the session or opens a player
// happens back on it. The outcome is reported as a command result, so the status
// line and the log say what happened to the import that was asked for.
//
// An http(s) address is not assumed to be HLS: it is asked what it serves first.
// A playlist goes through core/hls, so the session gets the tracks the stream
// declares; anything else that looks like media is handed to the player as it
// is. A failed probe is never treated as "probably an MP4": the failure is the
// outcome, with the address's own words in it.
func (a *appState) importSource(ctx context.Context, uri string, token int) {
	plan, probeErr := inspectImport(ctx, uri)
	mygoRunOnMain(func() {
		a.landImportPlan(token, plan, probeErr)
	})
}

// retryUnavailableSource repeats address inspection off the drawing thread;
// only the resulting Core registration and player open return to the window.
func (a *appState) retryUnavailableSource(it *item, uri string) error {
	if a.importing != "" {
		return errors.New("another source read is still in progress")
	}
	if it.persistentID == "" {
		it.persistentID = a.nextPersistentSourceID()
	}
	a.importToken++
	token := a.importToken
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	a.importing = uri
	a.retryTargetID = it.persistentID
	a.retryScene = a.scene
	a.retryStageGeneration = a.stageGeneration
	a.importSince = time.Now()
	a.importCancel = cancel
	a.importDone = make(chan struct{})
	go a.tickImport(a.importDone)
	go func(targetID string) {
		plan, err := inspectImport(ctx, uri)
		mygoRunOnMain(func() { a.landUnavailableRetry(token, targetID, uri, plan, err) })
	}(it.persistentID)
	return nil
}

func (a *appState) startStoredSourceRestores() {
	if len(a.startupSources) == 0 || a.closed || a.startupRestoreCancel != nil || (a.window == nil && a.onMain == nil) {
		return
	}
	a.startupRestoreToken++
	token := a.startupRestoreToken
	ctx, cancel := context.WithCancel(context.Background())
	a.startupRestoreCancel = cancel
	sceneID, generation := a.scene, a.stageGeneration
	sem := make(chan struct{}, 3)
	for _, stored := range a.startupSources {
		if stored.NeedsReselect || strings.TrimSpace(stored.URI) == "" {
			continue
		}
		it := a.itemByPersistentID(stored.ID)
		if it == nil || it.source.ID != "" || it.restoring || it.openErr != nil || !a.sceneConsumesItem(sceneID, it) {
			continue
		}
		it.restoring = true
		it.restoringGeneration = token
		targetID, uri := stored.ID, stored.URI
		go func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				a.mainDo(func() {
					current := a.itemByPersistentID(targetID)
					if current != nil && current.restoringGeneration == token {
						current.restoring = false
					}
				})
				return
			}
			defer func() { <-sem }()
			probeCtx, probeCancel := context.WithTimeout(ctx, 20*time.Second)
			plan, err := inspectImport(probeCtx, uri)
			probeCancel()
			a.mainDo(func() {
				current := a.itemByPersistentID(targetID)
				if current != nil && current.restoringGeneration == token {
					current.restoring = false
				}
				defer func() {
					if token == a.startupRestoreToken && !a.hasRestoringSources() {
						restart := !a.closed && ctx.Err() == nil
						cancel()
						a.startupRestoreCancel = nil
						if restart {
							a.startStoredSourceRestores()
						}
					}
				}()
				if a.closed || ctx.Err() != nil {
					return
				}
				if a.scene != sceneID || a.stageGeneration != generation || !a.sceneConsumesItem(sceneID, current) {
					return
				}
				if current != nil && current.source.ID == "" {
					if openErr := a.installSavedSource(current, plan, err); openErr != nil {
						current.openErr = safeOpenError(openErr)
						a.lastErr = current.openErr
					} else if current.player != nil {
						go a.watchFirstOpen(current, current.generation, true)
					}
				}
				a.invalidate()
			})
		}()
	}
	if token == a.startupRestoreToken && !a.hasRestoringSources() {
		cancel()
		a.startupRestoreCancel = nil
	}
}

func (a *appState) advanceStageGeneration() {
	a.stageGeneration++
	for _, it := range a.items {
		if it.mediaCancel != nil {
			it.mediaCancel()
			it.mediaCancel = nil
			it.mediaLoading = false
		}
		it.mediaMenu = false
	}
	if a.startupRestoreCancel == nil {
		return
	}
	a.startupRestoreCancel()
	a.startupRestoreCancel = nil
	for _, it := range a.items {
		if it.restoringGeneration == a.startupRestoreToken {
			it.restoring = false
		}
	}
}

func (a *appState) hasRestoringSources() bool {
	for _, it := range a.items {
		if it.restoring {
			return true
		}
	}
	return false
}

func (a *appState) startCommandLineSources(uris []string) {
	a.startupMediaQueue = append([]string(nil), uris...)
	a.startupMediaScene = a.scene
	a.advanceCommandLineSources()
}

func (a *appState) advanceCommandLineSources() {
	if a.closed || len(a.startupMediaQueue) == 0 {
		a.startupMediaQueue = nil
		a.startupMediaScene = ""
		return
	}
	uri := a.startupMediaQueue[0]
	a.startupMediaQueue = a.startupMediaQueue[1:]
	a.importToken++
	token := a.importToken
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	a.importing = uri
	a.importScene = a.startupMediaScene
	a.importSince = time.Now()
	a.importCancel = cancel
	a.importDone = make(chan struct{})
	go a.tickImport(a.importDone)
	go func() {
		plan, err := inspectImport(ctx, uri)
		mygoRunOnMain(func() {
			if token != a.importToken || a.closed {
				return
			}
			a.pendingImportOrigin = originCommandLine
			a.landImportPlan(token, plan, err)
			a.advanceCommandLineSources()
		})
	}()
}

func (a *appState) landUnavailableRetry(token int, targetID, requestedURI string, plan importPlan, inspectErr error) {
	commandID := core.SourceID("saved:" + targetID)
	if token != a.importToken {
		return
	}
	if a.closed {
		a.closeImportTicker()
		a.importing = ""
		a.retryTargetID = ""
		return
	}
	if a.scene != a.retryScene || a.stageGeneration != a.retryStageGeneration {
		a.closeImportTicker()
		a.importing = ""
		a.retryTargetID = ""
		a.retryScene = ""
		a.importText = ""
		a.lastErr = errors.New("stage changed while the source was being reopened; retry it on the active stage")
		a.invalidate()
		return
	}
	it := a.itemByPersistentID(targetID)
	if it == nil {
		a.closeImportTicker()
		a.importing = ""
		a.retryTargetID = ""
		return
	}
	err := a.installSavedSource(it, plan, inspectErr)
	if err == nil {
		a.saveSession()
		if it.player != nil && (a.window != nil || a.onMain != nil) {
			go a.watchFirstOpen(it, it.generation, true)
		}
	}
	a.closeImportTicker()
	a.importing = ""
	a.retryTargetID = ""
	a.retryScene = ""
	result := commandResult{Command: command{Kind: commandRetry, Source: commandID, URI: requestedURI}, At: time.Now()}
	if err != nil {
		it.openErr = safeOpenError(err)
		result.Err = it.openErr
		result.Message = "the source could not be reopened"
		a.lastErr = it.openErr
	} else {
		result.Generation = it.generation
		result.Message = "reopened " + sourceDisplayName(it)
		a.lastErr = nil
	}
	a.lastCommand = result
	a.report(result)
	a.invalidate()
}

func (a *appState) installSavedSource(it *item, plan importPlan, inspectErr error) error {
	oldKey := itemKey(it)
	oldSourceID := it.source.ID
	wasShown := it.shown
	savedName := it.persistedName
	if savedName == "" {
		savedName = it.source.Name
	}
	if inspectErr != nil {
		return inspectErr
	}
	candidate := &item{uri: plan.uri, volume: it.volume, muted: it.muted, origin: originAdded}
	switch plan.mode {
	case importProbed:
		inspectErr = a.attachStream(candidate, plan.stream)
	case importDirect:
		inspectErr = a.addDirect(candidate)
	default:
		inspectErr = a.addLocal(candidate)
	}
	if inspectErr == nil && savedName != "" {
		inspectErr = a.session.RenameSource(candidate.source.ID, savedName)
		if inspectErr == nil {
			candidate.source, _ = a.session.Source(candidate.source.ID)
		}
	}
	if inspectErr == nil {
		var player *ui.VideoPlayer
		player, inspectErr = a.openImportedPlayer(plan.uri)
		if inspectErr == nil && player == nil {
			inspectErr = errors.New("player open returned no player")
		}
		if inspectErr == nil {
			candidate.player = player
			if oldSourceID != "" {
				a.preserveSourceBeforeReplacement(it)
				a.closeOnMain(it)
				if removeErr := a.session.RemoveSource(oldSourceID); removeErr != nil {
					candidate.player.Close()
					candidate.player = nil
					inspectErr = removeErr
				} else {
					a.keepValidActiveSyncGroup()
				}
			}
			if inspectErr == nil {
				candidate.generation = it.generation + 1
				candidate.persistentID = it.persistentID
				candidate.persistedName = savedName
				candidate.shown = wasShown
				if plan.master != "" || it.uri != candidate.uri {
					it.playlistMaster = plan.master
				}
				it.uri = candidate.uri
				it.source = candidate.source
				it.video = candidate.video
				it.audio = candidate.audio
				it.player = candidate.player
				it.generation = candidate.generation
				it.persistedName = savedName
				it.shown = wasShown
				it.restored = true
				it.openErr = nil
				a.pruneStageEdits()
				a.updateRecoveredSourceKey(it, oldKey)
				a.restoreRetryBindings(it)
				a.restorePersistentMetadata(a.persistentSceneIDs)
				for _, id := range a.pendingLayoutOrder {
					if id == it.persistentID {
						if !containsTrackSource(a.layoutTaken, it.source.ID) {
							a.layoutTaken = append(a.layoutTaken, it.source.ID)
						}
					}
				}
				a.projectLayoutOrder()
				wakeNetworkPlayer(it.uri, rawPlayer(it.player), a.generationIs(it, it.generation))
				a.closePendingWhenReady()
				a.apply()
				return nil
			}
		}
	}
	a.dropUnreachableSource(candidate)
	return inspectErr
}

func (a *appState) restoreRetryBindings(it *item) {
	for sceneID, bindings := range a.pendingBindings {
		binding, ok := bindings[it.persistentID]
		if !ok {
			continue
		}
		if binding.video && it.video != "" {
			scene, err := a.session.Scene(sceneID)
			if err == nil && !containsTrack(scene.Video, it.video) {
				tracks := append([]core.TrackID(nil), scene.Video...)
				index := binding.videoIndex
				for _, pending := range bindings {
					if pending.video && pending.videoIndex < binding.videoIndex {
						index--
					}
				}
				index = min(max(index, 0), len(tracks))
				tracks = append(tracks, "")
				copy(tracks[index+1:], tracks[index:])
				tracks[index] = it.video
				_, err = a.session.SetSceneVideo(sceneID, tracks...)
			}
			if err == nil {
				binding.video = false
			}
		}
		if binding.audio != nil && it.audio != "" {
			scene, err := a.session.Scene(sceneID)
			if err == nil {
				levels := append([]core.AudioLevel(nil), scene.Audio...)
				ordered := make([]storedAudio, 0, len(levels)+1)
				for _, level := range levels {
					if owner := a.sourceOfTrack(level.Track); owner != "" {
						if source := a.itemBySourceID(owner); source != nil {
							ordered = append(ordered, storedAudio{Source: source.persistentID, Volume: level.Volume, Muted: level.Muted})
						}
					}
				}
				index := binding.audioIndex
				for _, pending := range bindings {
					if pending.audio != nil && pending.audioIndex < binding.audioIndex {
						index--
					}
				}
				index = min(max(index, 0), len(ordered))
				ordered = append(ordered, storedAudio{})
				copy(ordered[index+1:], ordered[index:])
				ordered[index] = *binding.audio
				for _, level := range levels {
					_, _ = a.session.RemoveSceneAudio(sceneID, level.Track)
				}
				for _, level := range ordered {
					if source := a.itemByPersistentID(level.Source); source != nil && source.audio != "" {
						if _, err = a.session.SetSceneAudio(sceneID, source.audio, level.Volume, level.Muted); err != nil {
							break
						}
					}
				}
			}
			if err == nil {
				binding.audio = nil
			}
		}
		if !binding.video && binding.audio == nil {
			delete(bindings, it.persistentID)
		} else {
			bindings[it.persistentID] = binding
		}
	}
}

func (a *appState) itemBySourceID(id core.SourceID) *item {
	for _, it := range a.items {
		if it.source.ID == id {
			return it
		}
	}
	return nil
}

// landImport ends an import on the drawing thread: it adds the source when the
// read worked, reports either outcome, and asks for a frame. The window shows
// the Sources page while the read runs, so without that frame it keeps showing
// "Reading…" and the old source count until something else happens to draw.
func (a *appState) landImport(token int, uri string, stream hls.Stream, mode importMode, live bool, probeErr error) {
	a.landImportPlan(token, importPlan{uri: uri, stream: stream, mode: mode, live: live}, probeErr)
}

func (a *appState) landImportPlan(token int, plan importPlan, probeErr error) {
	uri, stream, mode, live := plan.uri, plan.stream, plan.mode, plan.live
	if token != a.importToken {
		// Cancel already cleared the busy state and said so; this landing is
		// from the read it stopped and must not add anything.
		return
	}
	if a.closed {
		// The window is gone: its session and players were released, so there
		// is nothing left to import into and nothing to report to.
		a.closeImportTicker()
		a.importing = ""
		return
	}
	origin := a.pendingImportOrigin
	a.pendingImportOrigin = originAdded
	target := a.importScene
	a.importScene = ""
	if target == "" {
		target = a.scene
	}
	_, targetErr := a.session.Scene(target)
	targetAvailable := targetErr == nil
	it := &item{uri: uri, volume: a.settingsSourceVolume(), origin: origin, stageOnly: targetAvailable, playlistMaster: plan.master}
	var err error
	switch {
	case probeErr != nil:
		err = probeErr
	case mode == importProbed:
		err = a.attachStream(it, stream)
	case mode == importDirect:
		err = a.addDirect(it)
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
		// A completed read still registers its source against the stage where
		// the import began, but opening it after that stage was left would start
		// an invisible background player. The active-stage reconciler opens it
		// later if the user returns and the scene consumes it.
		if target == a.scene {
			player, err = a.openImportedPlayer(uri)
		}
		if err == nil {
			if player != nil {
				it.player = player
				it.generation++
				wakeNetworkPlayer(it.uri, rawPlayer(player), a.generationIs(it, it.generation))
			}
			a.items = append(a.items, it)
			if target == a.scene {
				a.selected = it.source.ID
			}
			// Ordinary startup selects the first video whose source and player
			// both landed successfully. A failed earlier CLI candidate must not
			// leave the stage pointing at a track that can never show a picture.
			// UI and handoff imports keep their explicit Show behavior.
			if origin == originCommandLine && targetAvailable && it.video != "" {
				if scene, sceneErr := a.session.Scene(target); sceneErr == nil && len(scene.Video) == 0 {
					if _, setErr := a.session.SetSceneVideo(target, it.video); setErr != nil {
						a.lastErr = safeOpenError(setErr)
					}
				}
			}
			// A source the user just imported is audible by default: route its
			// audio into the scene at its own level, exactly the way a source
			// given on the command line is routed at startup. Without this the
			// scene held no level for the track and apply() muted a source the
			// user had just added, which is the "没有声音" in questions-v4 P0-1.
			if _, sceneErr := a.session.Scene(target); sceneErr == nil {
				a.materializeStageMembers(target)[a.stageSourceID(it)] = true
				if it.audio != "" {
					if _, err := a.session.SetSceneAudio(target, it.audio, it.volume, false); err != nil {
						a.lastErr = safeOpenError(err)
					}
				}
			}
			a.apply()
			a.saveSession()
			if live && it.player != nil {
				// A live stream arrives without a timeline to align against:
				// the picture is already moving, so the shared clock starts
				// with it. Without this the top bar called a running stream
				// "paused" and its event time stayed at zero.
				a.sync.playhead.Play()
				it.player.Play()
				a.invalidate()
			}
			a.closePendingWhenReady()
			// The open returned nil, but this engine reports a file it cannot
			// decode a moment later; the item is watched until it has shown a
			// frame or reported that it never will. A test without a window has
			// no loop for the watch to run on, so it only starts where the
			// window (or the test's own seam) can take a callback.
			if it.player != nil && (a.window != nil || a.onMain != nil) {
				go a.watchFirstOpen(it, it.generation, false)
			}
		} else {
			// The session accepted the source and its tracks before the player
			// was opened. An import that cannot be shown is not an import, so
			// the source goes back out; otherwise the window would count a
			// source nothing can play, and the next scene would pick it up.
			a.dropUnreachableSource(it)
		}
	}
	a.closeImportTicker()
	a.importing = ""
	a.importText = ""
	result := commandResult{Command: command{Kind: commandAdd, URI: uri}, At: time.Now()}
	if err != nil {
		it.openErr = safeOpenError(err)
		result.Err = safeOpenError(err)
		result.Message = fmt.Sprintf("could not import %s", shortURI(uri))
		a.lastErr = safeOpenError(err)
	} else {
		result.Message = fmt.Sprintf("imported %s as %s; it is selected, Show puts it on the stage", shortURI(uri), it.source.ID)
		if !targetAvailable {
			result.Message = fmt.Sprintf("imported %s as %s into the shared catalog; the starting stage was deleted", shortURI(uri), it.source.ID)
		} else if target != a.scene {
			stage, _ := a.session.Scene(target)
			if stage.Name != "" {
				result.Message = fmt.Sprintf("imported %s into stage %s; switch back to that stage to use it", shortURI(uri), stage.Name)
			} else {
				result.Message = fmt.Sprintf("imported %s into its starting stage; switch back to that stage to use it", shortURI(uri))
			}
		}
	}
	a.lastCommand = result
	if err != nil {
		a.importFailureURI = uri
		a.importFailureErr = safeOpenError(err)
	} else {
		a.importFailureURI = ""
		a.importFailureErr = nil
	}
	a.report(result)
	a.invalidate()
}

// firstOpenGrace is how long an imported player gets to report an error before
// the import is treated as shown. Media Foundation accepts a file when the
// player is created and answers "could not play the video" a moment later.
const firstOpenGrace = 3 * time.Second

// shouldAbandonImport says an import did not become a source: the player
// reported an error and never reached ready. A player that is merely slow has no
// error and is left alone.
func shouldAbandonImport(ready bool, err error) bool {
	return err != nil && !ready
}

// watchFirstOpen watches a freshly added source for the deferred error Media
// Foundation reports after openPlayerOnMain has already returned nil. An import
// that never shows a frame is handed back, so a source nothing can play is not
// counted in the window or picked up by the scene.
func (a *appState) watchFirstOpen(it *item, generation int, keepSource bool) {
	deadline := time.Now().Add(firstOpenGrace)
	for keepSource || time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		ready, err, live := a.playerState(it, generation)
		if !live {
			return
		}
		if !shouldAbandonImport(ready, err) {
			if ready {
				return
			}
			continue
		}
		a.abandonImport(it, generation, err, keepSource)
		return
	}
}

// playerState reads the player on the drawing thread. live is false once the
// item left the window or someone else owns its player now.
func (a *appState) playerState(it *item, generation int) (ready bool, err error, live bool) {
	a.mainDo(func() {
		if a.closed || it.generation != generation || it.player == nil {
			return
		}
		live = true
		ready = it.player.Ready()
		err = it.player.Err()
	})
	return ready, err, live
}

// abandonImport takes a source back out after its player failed to show it, and
// says so in the same words a failed import uses.
func (a *appState) abandonImport(it *item, generation int, cause error, keepSource bool) {
	a.mainDo(func() {
		if a.closed || it.generation != generation || it.handover != nil {
			return
		}
		if keepSource || it.restored {
			a.closeOnMain(it)
			err := safeOpenError(cause)
			it.openErr = err
			a.lastErr = err
			result := commandResult{
				Command: command{Kind: commandRetry, Source: itemKey(it)},
				At:      time.Now(),
				Err:     err,
				Message: fmt.Sprintf("could not open %s; the saved source and its bindings are still here", shortURI(it.uri)),
			}
			a.lastCommand = result
			a.report(result)
			a.saveSession()
			a.invalidate()
			return
		}
		removed := false
		wasStartupPicture := it.origin == originCommandLine && it.shown
		for index, candidate := range a.items {
			if candidate != it {
				continue
			}
			if err := a.remove(index); err != nil {
				log.Printf("import: could not remove %s after the player failed: %v", it.source.ID, err)
				return
			}
			removed = true
			break
		}
		if removed {
			if wasStartupPicture {
				a.selectStartupPicture()
			}
			a.saveSession()
		}
		err := safeOpenError(cause)
		if removed {
			a.importFailureURI = it.uri
			a.importFailureErr = err
		}
		a.lastErr = err
		result := commandResult{
			Command: command{Kind: commandAdd, URI: it.uri},
			At:      time.Now(),
			Err:     err,
			Message: fmt.Sprintf("could not import %s: the player could not show it", shortURI(it.uri)),
		}
		a.lastCommand = result
		a.report(result)
		a.invalidate()
	})
}

func (a *appState) selectStartupPicture() {
	scene, err := a.session.Scene(a.scene)
	if err != nil || len(scene.Video) != 0 {
		return
	}
	for _, candidate := range a.items {
		if candidate.origin == originCommandLine && a.isStageMember(candidate) && candidate.video != "" &&
			candidate.player != nil && candidate.player.Err() == nil {
			if _, err := a.session.SetSceneVideo(a.scene, candidate.video); err != nil {
				a.lastErr = safeOpenError(err)
				return
			}
			a.selected = itemKey(candidate)
			a.apply()
			a.wakeShown(candidate)
			return
		}
	}
}

// mainDo runs fn on the drawing thread. onMain is the seam tests use, because a
// test has no window loop for mygoRunOnMain to reach.
func (a *appState) mainDo(fn func()) {
	if a.onMain != nil {
		a.onMain(fn)
		return
	}
	mygoRunOnMain(fn)
}

// openImportedPlayer opens the player of an imported source on the drawing
// thread. openPlayerForImport is the seam a test uses to make the open fail
// without depending on what this machine's Media Foundation accepts.
func (a *appState) openImportedPlayer(uri string) (*ui.VideoPlayer, error) {
	if a.openPlayerForImport != nil {
		return a.openPlayerForImport(uri)
	}
	return openPlayerOnMain(uri)
}

// dropUnreachableSource hands back a source the session accepted when the
// player that would have shown it could not be opened. The item never reaches
// a.items, so nothing else in the window knows the source exists; leaving it in
// the session would leave a source counted in every scene and in "N sources".
func (a *appState) dropUnreachableSource(it *item) {
	if it.source.ID == "" {
		return
	}
	if err := a.session.RemoveSource(it.source.ID); err != nil {
		log.Printf("import: could not remove %s after the player failed to open: %v", it.source.ID, err)
		return
	}
	it.source = core.Source{}
	it.video = ""
	it.audio = ""
}

// invalidate asks for a frame after the state the window shows has changed. It
// runs on the drawing thread; requestFrame is the seam tests use because they
// have no window.
func (a *appState) invalidate() {
	if a.requestFrame != nil {
		a.requestFrame()
		return
	}
	if a.window != nil {
		a.window.Invalidate()
	}
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
				if !a.closed {
					a.invalidate()
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
// addScene creates a scene and switches to it. A new scene starts empty: its
// purpose is to hold a different arrangement of the same sources.
func (a *appState) addScene() {
	// An empty name keeps the friendly "Scene N" label; only a name the person
	// typed is stored, so a default scene is never displayed as "scene 2".
	scene, err := a.session.AddScene("")
	if err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	a.persistentSceneID(scene.ID)
	a.materializeStageMembers(a.scene)
	a.sceneMembers[scene.ID] = make(map[string]bool)
	a.selectScene(scene.ID)
}

func (a *appState) apply() {
	a.pruneStageEdits()
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		a.lastErr = safeOpenError(err)
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
			it.shown = false
			continue
		}
		it.shown = shown[it.video]
		player := it.outputPlayer()
		if player != it.player {
			it.player.SetMuted(true)
		}
		level, ok := levels[it.audio]
		if !ok {
			player.SetMuted(true)
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
		player.SetMuted(muted)
		if !muted {
			player.SetVolume(a.volumeFor(level.Volume))
		}
	}
}

func (it *item) outputPlayer() desktopPlayer {
	if it.handover != nil && it.handover.retainState && it.previous != nil {
		return it.previous
	}
	return it.player
}

// sceneConsumesItem reports whether the selected scene actually uses this
// source for picture or sound. Membership alone is intentionally insufficient:
// it records where a source may be used, not whether it should run now.
func (a *appState) sceneConsumesItem(sceneID core.SceneID, it *item) bool {
	if it == nil {
		return false
	}
	if it.source.ID == "" {
		binding := a.pendingBindings[sceneID][it.persistentID]
		return binding.video || binding.audio != nil
	}
	scene, err := a.session.Scene(sceneID)
	if err != nil {
		return false
	}
	for _, track := range scene.Video {
		if track == it.video && it.video != "" {
			return true
		}
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio && it.audio != "" {
			return true
		}
	}
	return false
}

// reconcileScenePlayers runs after a stage change. Player close waits for
// frames and therefore must not run in the frame callback that selected the
// stage. Completion is checked against both the scene and its generation so a
// rapid switch cannot restore a player for an older stage.
func (a *appState) reconcileScenePlayers(sceneID core.SceneID, generation uint64) {
	if a.window == nil || a.closed || a.scene != sceneID || a.stageGeneration != generation {
		return
	}
	needsRestore := false
	for _, it := range a.items {
		needed := a.sceneConsumesItem(sceneID, it)
		if !needed {
			if it.player != nil && !it.lifecyclePending {
				output := it.outputPlayer()
				it.resumePaused = output.Ready() && !output.Buffering() && !output.Playing()
				it.resumePositionOK = it.replay == nil && !isNetworkAddress(it.uri)
				if it.resumePositionOK {
					it.resumePosition = output.Position()
				}
				it.lifecyclePending = true
				a.retireItemOnMain(it, func() {
					it.lifecyclePending = false
					if a.sceneConsumesItem(a.scene, it) {
						a.reconcileScenePlayers(a.scene, a.stageGeneration)
					}
				})
			}
			continue
		}
		if it.player != nil || it.lifecyclePending || it.restoring || it.openErr != nil || strings.TrimSpace(it.uri) == "" {
			continue
		}
		if it.source.ID == "" {
			needsRestore = true
			continue
		}
		go func(source *item) {
			a.mainDo(func() {
				if a.closed || a.scene != sceneID || a.stageGeneration != generation || !a.sceneConsumesItem(sceneID, source) || source.player != nil || source.lifecyclePending {
					return
				}
				player, err := a.openImportedPlayer(source.uri)
				if err == nil && player == nil {
					err = errors.New("player open returned no player")
				}
				if err != nil {
					source.openErr = safeOpenError(err)
					a.lastErr = source.openErr
					a.invalidate()
					return
				}
				source.player = player
				source.openErr = nil
				source.generation++
				if source.resumePositionOK {
					source.player.Seek(source.resumePosition)
				}
				if source.resumePaused {
					source.player.Pause()
				}
				source.resumePositionOK = false
				a.closePendingWhenReady()
				a.apply()
				wakeNetworkPlayer(source.uri, rawPlayer(source.player), a.generationIs(source, source.generation))
				go a.watchFirstOpen(source, source.generation, true)
				a.invalidate()
			})
		}(it)
	}
	if needsRestore {
		a.startStoredSourceRestores()
	}
}

// showVideo puts a source's picture on the stage, or takes it off again when it
// is already there. The scene keeps every shown track in the order it was added,
// which is the order the layout places the pictures in; the audio levels are
// untouched either way.
func (a *appState) showVideo(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return err
	}
	if it.canRestoreOnDemand() {
		id := a.stageSourceID(it)
		bindings := a.pendingBindings[scene.ID]
		if !bindings[id].video {
			a.setPendingVideo(scene.ID, id, expandedPendingIndex(len(scene.Video), bindings, pendingVideoSlot))
		}
		it.openErr = nil
		a.startStoredSourceRestores()
		a.invalidate()
		return nil
	}
	if it.video == "" {
		return fmt.Errorf("source %d has no video track", index+1)
	}
	tracks := make([]core.TrackID, 0, len(scene.Video)+1)
	showing := false
	for _, track := range scene.Video {
		if track == it.video {
			showing = true
			continue
		}
		tracks = append(tracks, track)
	}
	if !showing {
		if err := a.ensurePlayer(it); err != nil {
			return err
		}
		tracks = append(tracks, it.video)
	}
	if _, err := a.session.SetSceneVideo(a.scene, tracks...); err != nil {
		return err
	}
	if showing {
		a.removePendingStageRoutes(scene, it, true, false)
	}
	a.apply()
	if !showing {
		a.wakeShown(it)
	}
	return nil
}

var errPlayerClosing = errors.New("the previous player is still closing")

// ensurePlayer opens the player of a source that has none yet. Sources added
// while the window runs stay closed until something needs them: the player of
// a source that never draws does not become ready, so opening it on import only
// produces a card that says "opening" forever.
func (a *appState) ensurePlayer(it *item) error {
	if it.lifecyclePending {
		return errPlayerClosing
	}
	if it.player != nil {
		return it.openErr
	}
	player, err := openPlayerOnMain(it.uri)
	if err != nil {
		it.openErr = safeOpenError(err)
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
	deferred := it.canRestoreOnDemand()
	if it.audio == "" && !deferred {
		return fmt.Errorf("source %d has no audio track", index+1)
	}
	if deferred {
		if math.IsNaN(volume) || volume < 0 || volume > 1 {
			return fmt.Errorf("source %d: %w: the volume must be between 0 and 1", index+1, core.ErrInvalid)
		}
	} else {
		if err := a.ensurePlayer(it); err != nil {
			return err
		}
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
	if deferred {
		a.clearPendingSceneAudio(a.scene)
		id := a.stageSourceID(it)
		a.setPendingAudio(a.scene, id, storedAudio{Source: id, Volume: volume}, 0)
		it.openErr = nil
		a.startStoredSourceRestores()
	} else {
		if _, err := a.session.SetSceneAudio(a.scene, it.audio, volume, false); err != nil {
			return err
		}
		a.clearPendingSceneAudio(a.scene)
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
	if it.mediaCancel != nil {
		it.mediaCancel()
		it.mediaCancel = nil
	}
	scenes := a.session.Scenes()
	if it.source.ID != "" {
		if err := a.session.RemoveSource(it.source.ID); err != nil {
			return err
		}
	}
	for _, scene := range scenes {
		a.removePendingStageRoutes(scene, it, true, true)
	}
	if a.probeRunning {
		a.invalidateAlignmentRow()
	}
	a.forgetPendingSource(it.persistentID)
	a.layoutTaken = removeTrackID(a.layoutTaken, it.source.ID)
	delete(a.audible, it.audio)
	if a.sync != nil {
		delete(a.sync.origins, it.video)
		delete(a.sync.origins, it.audio)
		if a.sync.timeline != nil {
			a.sync.timeline.Forget(it.video)
			a.sync.timeline.Forget(it.audio)
		}
		a.keepValidActiveSyncGroup()
	}
	for _, members := range a.sceneMembers {
		delete(members, it.persistentID)
	}
	if !it.lifecyclePending {
		a.closeOnMain(it)
	}
	a.items = append(a.items[:index], a.items[index+1:]...)
	a.reconcileStageSelection()
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

// close dispatches the scenario entry point to the drawing thread.
func (a *appState) close(it *item) {
	a.mainDo(func() { a.closeOnMain(it) })
}

type pendingClose struct {
	player *ui.VideoPlayer
	lease  *dvr.Lease
}

type mediaRetirement struct {
	handles []pendingClose
	keep    *ui.VideoPlayer
	after   func()
	frame   int
	drawn   chan struct{}
}

func (a *appState) closeOnMain(it *item) {
	a.retireItemOnMain(it, nil)
}

// retireItemOnMain invalidates the generation and takes every handle in one
// drawing-thread step. An obsolete handover can no longer reinstall a player.
func (a *appState) retireItemOnMain(it *item, after func()) {
	if it == nil {
		return
	}
	current := rawPlayer(it.player)
	previous := rawPlayer(it.previous)
	keep := rawPlayer(it.outputPlayer())
	if it.handover != nil {
		previous = it.handover.take()
	}
	lease, swap := it.replayLease, it.swap
	it.player, it.previous, it.handover, it.swap = nil, nil, nil, nil
	it.replay, it.replayLease, it.replayAnchor, it.replayAnchorErr = nil, nil, nil, nil
	it.shown = false
	it.generation++

	var handles []pendingClose
	if swap != nil {
		swap.finished.Do(func() {
			handles = append(handles, pendingClose{player: swap.player, lease: swap.lease})
		})
	}
	if previous != nil && previous != current {
		handles = append(handles, pendingClose{player: previous})
	}
	if current != nil || lease != nil {
		handles = append(handles, pendingClose{player: current, lease: lease})
	}
	a.retireMediaOnMain(handles, keep, after)
}

func (a *appState) retireMediaOnMain(handles []pendingClose, keep *ui.VideoPlayer, after func()) {
	if len(handles) == 0 {
		if after != nil {
			after()
		}
		return
	}
	for _, handle := range handles {
		if handle.player != nil {
			handle.player.SetMuted(true)
			handle.player.Pause()
		}
	}
	select {
	case <-a.closeDone:
		a.closeMediaOnMain(handles)
		return
	default:
	}
	if a.closeDone == nil {
		a.closeDone = make(chan struct{})
	}
	if a.retiring == nil {
		a.retiring = make(map[*mediaRetirement]struct{})
	}
	job := &mediaRetirement{
		handles: handles, keep: keep, after: after, frame: a.frameCount + 3, drawn: make(chan struct{}),
	}
	a.retiring[job] = struct{}{}
	stop := a.closeDone
	window := a.window
	a.closeWait.Go(func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-stop:
			return
		case <-job.drawn:
		case <-timer.C:
		}
		// The frame waiter holds no player or lease. Shutdown can drain the
		// queue even if the completion callback has not reached the UI yet.
		if window != nil {
			window.Update(func() { a.finishRetirementOnMain(job) })
		}
	})
	a.invalidate()
}

func (a *appState) readyOutputPlayer() bool {
	for _, it := range a.items {
		if player := it.outputPlayer(); player != nil && player.Ready() && !player.Buffering() {
			return true
		}
	}
	return false
}

func (a *appState) finishRetirementOnMain(job *mediaRetirement) {
	if _, owned := a.retiring[job]; !owned {
		return
	}
	delete(a.retiring, job)
	keep := job.keep != nil && !a.readyOutputPlayer()
	for _, handle := range job.handles {
		if keep && handle.player == job.keep {
			// MyGo needs a paused player until another player produces a
			// picture. Keep one, along with any lease on its recording.
			a.pendingMu.Lock()
			alreadyKept := len(a.pending) != 0
			if !alreadyKept {
				a.pending = append(a.pending, handle)
			}
			a.pendingMu.Unlock()
			if !alreadyKept {
				continue
			}
		}
		a.closeMediaOnMain([]pendingClose{handle})
	}
	if job.after != nil {
		job.after()
	}
}

func (a *appState) closeMediaOnMain(handles []pendingClose) {
	for _, handle := range handles {
		if handle.player != nil {
			handle.player.Close()
		}
		a.releaseLease(handle.lease, "of a closed player")
	}
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
	// A source that is already in the scene carries its level there, and that
	// is the number the player is handed: setting the item's copy alone would
	// show 25% on the card while the output kept playing at the scene's 100%.
	if a.session != nil {
		if level, routed := a.sceneLevel(it); routed && level.Volume != volume {
			if _, err := a.session.SetSceneAudio(a.scene, it.audio, volume, level.Muted); err != nil {
				log.Printf("source memory: %s stays at the scene's level: %v", identityDigest(identity), err)
			}
		}
		a.apply()
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

// noteSourcePreference remembers the sound of a source without blocking the
// drawing thread that just moved a slider: the in-memory set is made here, on
// the thread that owns it, and only the write is handed to a goroutine.
func (a *appState) noteSourcePreference(it *item) {
	a.rememberSource(it)
}

// rememberSource records the level and mute now in force for the source's
// address. It is the only write path, so the four refusals live here: memory
// off, remembering off, a source started from the command line, and an address
// that cannot be an identity. It runs on the drawing thread: a.settings is
// read by every frame, so a background goroutine must not change it.
func (a *appState) rememberSource(it *item) {
	memory := a.settings.SourceMemory
	if !memory.On || !memory.Remember {
		return
	}
	// A start that could not read the settings file must not write it: the file
	// may be holding a configuration this start never applied, and replacing it
	// with the in-memory defaults would lose it. The same rule already protects
	// the workspace save.
	if !a.settingsTrusted {
		log.Printf("source memory: not saved, the settings file was not loaded")
		return
	}
	if it.origin == originCommandLine {
		return
	}
	identity := identityOf(it.uri)
	if identity == "" {
		return
	}
	// The applied settings own the copy that will be saved; the draft is left
	// alone so a page someone is editing is not rewritten underneath them.
	next := cloneSettings(a.settings)
	changed, refusal := next.SourceMemory.set(identity, it.volume, it.muted)
	if refusal != "" {
		log.Printf("source memory: %s", refusal)
		return
	}
	if !changed {
		return
	}
	a.settings = next
	// The write gets its own copy: by the time it runs, a newer set or an
	// Apply may already have replaced a.settings. The ticket is taken here, on
	// the thread that made the snapshot, so a write that starts late still
	// knows it is older than one that already landed.
	saved := cloneSettings(next)
	ticket := a.saveTicket()
	digest, volume, muted := identityDigest(identity), it.volume, it.muted
	go func() {
		if err := a.saveSettingsSerialized(saved, ticket); err != nil {
			log.Printf("source memory: could not save %s: %v", digest, err)
			a.noteBackgroundFailure("saving a source's remembered sound", err)
			return
		}
		// The flag the page reads is owned by the drawing thread like the rest
		// of the state; setting it here would be a write from a goroutine.
		a.settingsSaved.Store(true)
		log.Printf("source memory: remembered %s at %.0f%%%s", digest, volume*100, muteWord(muted))
	}()
}

// closePendingWhenReady closes the retained player only after another output
// can supply an actual picture. It runs on the drawing thread.
func (a *appState) closePendingWhenReady() {
	if len(a.pending) == 0 || !a.readyOutputPlayer() {
		return
	}
	a.pendingMu.Lock()
	wait := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	a.closeMediaOnMain(wait)
}

// waitFrames blocks until the window built n frames after this call, or until
// it stops building them.
func (a *appState) waitFrames(n int) {
	for i := 0; i < n; i++ {
		select {
		case <-a.frames:
		case <-a.closeDone:
			return
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
	a.closePendingWhenReady()
	for job := range a.retiring {
		if job.frame == a.frameCount {
			close(job.drawn)
		} else if job.frame > a.frameCount {
			a.invalidate()
		}
	}
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
	a.mainDo(func() { a.releaseOnce.Do(a.shutdown) })
}

func (a *appState) shutdown() {
	if a.closeDone == nil {
		a.closeDone = make(chan struct{})
	}
	close(a.closeDone)
	a.stopProbe("the session closed")
	// A comparison still decoding belongs to the session that is closing: cancel
	// it first, so its ffmpeg ends here instead of running against a window that
	// will never show the result.
	// A transaction still preparing owns files and players of its own; give them
	// back before the sources they were cut from are closed.
	a.shutdownReplayTxn()
	a.stopRecording()
	log.Printf("shutdown: recording stopped; closing %d player(s)", len(a.items))
	for _, it := range a.items {
		a.closeOnMain(it)
	}
	a.closeWait.Wait()
	for job := range a.retiring {
		delete(a.retiring, job)
		a.closeMediaOnMain(job.handles)
	}
	a.pendingMu.Lock()
	wait := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	a.closeMediaOnMain(wait)
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
			a.lastErr = safeOpenError(err)
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
		base := "picture " + picture + " · silent"
		if name := strings.TrimSpace(scene.Name); name != "" {
			return name + " · " + base
		}
		return base
	}
	base := "picture " + picture + " · sound " + strings.Join(audio, " + ")
	if name := strings.TrimSpace(scene.Name); name != "" {
		return name + " · " + base
	}
	return base
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
	uri = stripSecrets(uri)
	trimmed := strings.TrimSuffix(strings.ReplaceAll(uri, "\\", "/"), "/")
	parts := strings.Split(trimmed, "/")
	last := parts[len(parts)-1]
	if strings.HasPrefix(last, "index.") && len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + last
	}
	return last
}

// sourceDisplayName is what the window calls a source: the person's own name
// when they set one, otherwise the short address. Keep shortURI as the fallback
// so a source without a name reads exactly as it always did.
func sourceDisplayName(it *item) string {
	if it == nil {
		return "none"
	}
	if name := strings.TrimSpace(it.source.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(it.persistedName); name != "" {
		return name
	}
	return shortURI(it.uri)
}

// sceneDisplayName is what the window calls a scene: the person's own name when
// they set one, otherwise the generated "Scene N" label.
func (a *appState) sceneDisplayName(sc core.Scene, index int) string {
	if name := strings.TrimSpace(sc.Name); name != "" {
		return name
	}
	return fmt.Sprintf(a.tr("Scene %d", "场景 %d"), index+1)
}
