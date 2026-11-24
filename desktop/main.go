// Command muxshift-desktop shows media sources in a MyGo window.
//
// It exists to find out what the native video path of MyGo actually does before
// anything is built on it: open local files and HLS addresses, play, pause,
// mute, switch between sources, close them, and report the real state of each
// player rather than an assumed one.
//
// What the window shows and hears comes from a Core session: the sources and
// tracks registered for each address, and one scene that names the video track
// drawn and the audio tracks heard. The buttons edit that scene, and the scene
// is applied to the players; see session.go.
//
//	go run . -media <file-or-url> [-media <another>]
//
// -media accepts a local path or an http(s) address and may be given more than
// once. -shot writes a PNG of the window and quits, so playback on a machine
// without a person watching can still be checked. -scenario runs one of the
// scripted checks in scenarios.go.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"github.com/xswt442-cmd/muxshift/desktop/internal/version"
	"github.com/xswt442-cmd/muxshift/desktop/internal/win"
)

// mygoRunOnMain runs fn on the thread that draws the window and waits for it.
func mygoRunOnMain(fn func()) {
	mygo.RunOnMain(fn)
}

// reopenPlayer closes the player of it and opens it again from the same
// address, keeping the session's source and tracks.
//
// The old player is not closed first. In this MyGo build a window that is left
// with no video element at all never hands a picture to the next player: the
// player reports its duration but stays not ready and keeps its position at
// zero. The smallest reproduction (testplace/poc/onevid -reopen -reopen-mode
// view) fails the same way, while reopening the address with the old player
// still drawn works for both a local file and HLS (testplace/poc/onevid -reopen
// -reopen-mode keep). So the old player stays drawn until the new one has a
// picture, and is closed then.
func (a *appState) reopenPlayer(it *item) {
	previous := it.player
	player, err := openPlayerOnMain(it.uri)
	if err != nil {
		it.openErr = safeOpenError(err)
		return
	}
	it.openErr = nil
	var kept *handover
	if previous != nil {
		kept = &handover{previous: rawPlayer(previous)}
		it.previous = previous
	}
	// The handover that was still waiting for an older replacement owns the
	// player it kept drawn; it is not drawn any more, so the new task releases
	// it. Transferring the pointer here is what keeps a burst of reloads from
	// releasing the same player twice.
	older := it.handover
	it.handover = kept
	it.player = player
	it.generation++
	generation := it.generation
	a.apply()
	wakeNetworkPlayer(it.uri, player, a.generationIs(it, generation))
	a.closePendingWhenReady()
	if kept != nil {
		go a.handOver(it, generation, kept, older)
	} else if older != nil {
		a.releasePlayer(older.take())
	}
}

// handover owns the player a reopen kept drawn until the replacement has a
// picture. Exactly one goroutine releases each player: the task that started
// the handover, or the task a later reopen hands the player over to.
type handover struct {
	mu       sync.Mutex
	previous *ui.VideoPlayer
	released bool
}

// take returns the player this handover still owns, or nil when it was
// released already.
func (h *handover) take() *ui.VideoPlayer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return nil
	}
	h.released = true
	player := h.previous
	h.previous = nil
	return player
}

// open reports whether the handover still owns a player, without taking it.
func (h *handover) open() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.released && h.previous != nil
}

// releasePlayer closes a player that no frame draws any more.
func (a *appState) releasePlayer(player *ui.VideoPlayer) {
	if player == nil {
		return
	}
	a.waitFrames(3)
	mygoRunOnMain(func() { player.Close() })
}

// handoverWait is how long a replacement may take to show a picture before the
// handover gives up and keeps the player it was replacing.
var handoverWait = 10 * time.Second

// The replay buffer's defaults: a minute of history in the user's temporary
// directory, which the buffer removes again when the session ends. The recording
// budget bounds the snapshots a player may hold, separately from the segments:
// a recording the buffer would have to drop to make room is refused, never the
// one a player is reading.
var (
	retryProbeMode     string
	retryProbePause    bool
	retryProbeBoundary string
	retryProbeRetry    string
)
var (
	dvrDir            = filepath.Join(os.TempDir(), "muxshift-dvr")
	dvrRetention      = 60 * time.Second
	dvrMaxBytes       int64
	dvrRecordingBytes = int64(256 << 20)

	// extraMedia is the second address the sourcememory check adds by hand, so it
	// the write path is exercised with a source the command line did not open.
	extraMedia string

	// scenarioDecoder replaces the decoder a comparison starts, so the probeclose
	// check can watch a child that is still running when the window closes. The
	// close path under test is the product's: same context, same kill, same Wait.
	scenarioDecoder string

	// outageMode says which outage the offline check arranged: a server that
	// stops answering, or a publisher that stops while its server keeps serving.
	// The two read differently on the page, and the check must not demand the
	// same words from both.
	outageMode string
)

// handOver waits for the replacement player of a reopen to show a picture, then
// releases the player the reopen kept drawn.
//
// Two outcomes matter. When the replacement starts, the old player is closed
// and the item ends up with one player again. When it never starts, the old
// player stays drawn and the replacement is closed instead: a handover that
// fails must not leave the source with a player that never shows anything, and
// the failure is reported in the source line instead of disappearing in a log.
//
// Every step checks the item's generation. A newer reload, or a close, bumps it,
// and this task then only releases the player it still owns: the view draws a
// newer player, so the pointer it was waiting on is not drawn any more.
func (a *appState) handOver(it *item, generation int, kept *handover, older *handover) {
	if older != nil {
		a.releasePlayer(older.take())
	}
	deadline := time.Now().Add(handoverWait)
	for {
		var current, ready bool
		var playerErr error
		mygoRunOnMain(func() {
			current = it.generation == generation
			if it.player != nil {
				ready = it.player.Ready() && !it.player.Buffering()
				playerErr = it.player.Err()
			}
		})
		if !current {
			a.releasePlayer(kept.take())
			return
		}
		if playerErr != nil {
			break
		}
		if ready {
			mygoRunOnMain(func() {
				if it.generation == generation && it.handover == kept {
					it.previous = nil
					it.handover = nil
				}
			})
			a.releasePlayer(kept.take())
			log.Printf("handover: replaced the player of %s after %s", shortURI(it.uri), "a picture")
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	// The replacement never started: keep what was drawn and drop the new one.
	var replacement *ui.VideoPlayer
	keptPrevious := false
	mygoRunOnMain(func() {
		if it.generation != generation {
			return
		}
		replacement = rawPlayer(it.player)
		it.player = kept.previous
		kept.previous = nil
		kept.released = true
		it.previous = nil
		it.handover = nil
		it.generation++
		it.shown = true
		it.openErr = fmt.Errorf("reopen did not start within %s; kept the previous player", handoverWait)
		keptPrevious = true
	})
	if !keptPrevious {
		a.releasePlayer(kept.take())
		return
	}
	log.Printf("handover: %s did not start within %s; kept the previous player and closed the new one", shortURI(it.uri), handoverWait)
	a.releasePlayer(replacement)
}

// openPlayer opens a local file or a network address.
// openPlayerOnMain creates a player on the thread that draws the window. A
// player created from another goroutine never delivered a picture for a live
// playlist in the runs recorded in testplace/evidence/P8/replay-isolation.md,
// while a probe that created its players inside WhenReady drew frames at once.
func openPlayerOnMain(uri string) (*ui.VideoPlayer, error) {
	var player *ui.VideoPlayer
	var err error
	mygoRunOnMain(func() { player, err = openPlayer(uri) })
	return player, err
}

func openPlayer(uri string) (*ui.VideoPlayer, error) {
	if isNetworkAddress(uri) {
		return video.OpenURL(uri)
	}
	return video.Open(uri)
}

// isNetworkAddress reports whether uri is something to stream rather than a
// file on this machine.
func isNetworkAddress(uri string) bool {
	lower := strings.ToLower(strings.TrimSpace(uri))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// needsWake reports whether a source is played through a player that does not
// deliver its first picture on its own. A local file does, but an HLS playlist
// holds segments behind another layer even when it sits on this machine, and it
// behaved like a network source in the replay runs.
func needsWake(uri string) bool {
	if isNetworkAddress(uri) {
		return true
	}
	lower := strings.ToLower(uri)
	return strings.HasSuffix(lower, ".m3u8") || strings.HasSuffix(lower, ".ts")
}

// wakeNetworkPlayer nudges a network player into showing its first picture.
//
// In this desktop build a network source plays, reports its duration and moves
// its position, but its first picture does not reach the window: the player
// stays not ready with a black area, and a seek is what brings a frame over. A
// seek to just ahead of the current position keeps the playhead where it was
// and is enough. A second video in the same window was once believed to do it
// too; an isolation probe (testplace/poc/twowin) showed that a second player,
// a second window, and a paused window all draw frames, so that explanation is
// no longer used. What the probe did not settle is why a live playlist nudged
// this way stops at the point of the seek instead of playing on.
//
// The seek is a compensation for the missing first frame, not a synchronisation
// step: it moves the playhead by a millisecond and is not something a precise
// timeline should build on. It runs once, while the player is being opened; if
// that one seek does not bring a picture the player stays not ready and the
// window says so, instead of a background task moving the playhead again and
// again. live reports whether this task still owns the player it was started
// for; once the player is replaced or closed the task stops, so it never
// touches a released player.
func wakeNetworkPlayer(uri string, player *ui.VideoPlayer, live func() bool) {
	go func() {
		if wakeMode == "none" {
			return
		}
		// A few attempts while the player still reports no picture, spaced so a
		// source that needs to buffer first gets the chance: the task stops as
		// soon as the player is ready or reports an error, and gives up after
		// six attempts instead of moving the playhead forever. How many attempts
		// a live playlist tolerates is still open; testplace/poc/twowin is where
		// that question is being narrowed down.
		for attempt := 0; attempt < 6; attempt++ {
			time.Sleep(1500 * time.Millisecond)
			if !live() {
				return
			}
			var before, after time.Duration
			var done bool
			mygoRunOnMain(func() {
				if !live() || player.Ready() || player.Err() != nil {
					done = true
					return
				}
				before = player.Position()
				player.Seek(before + time.Millisecond)
				// The position the player reports afterwards is the real result;
				// the millisecond asked for is not assumed to have taken effect.
				after = player.Position()
			})
			if done {
				return
			}
			if before != 0 || after != 0 {
				log.Printf("wake: %s seeked from %s to %s (asked for +1ms)", shortURI(uri), before, after)
			}
		}
	}()
}

// wakeMode selects whether a network player is nudged into showing its first
// frame. The nudge is a one-shot compensation at open time; there is no
// repeating mode because a task that keeps seeking would move the playhead
// behind the timeline's back. -wake=none records what happens without it.
var wakeMode = "once"

// replayProbeURL lets the replayprobe scenario open any recorded playlist, so a
// replay served by this application can be compared with one served as files.
var replayProbeURL string

// replayMode marks a process that was started to play one recording as its only
// source; the window title and the status line say so.
var replayMode bool

// status is one player's state as a line, so the window reports what actually
// happened: an opening error, buffering, or the position it reached.
func status(it *item) string {
	switch {
	case it.openErr != nil:
		return "error: " + it.openErr.Error()
	case it.player == nil:
		return "not shown yet"
	case it.player.Err() != nil:
		return "error: " + it.player.Err().Error()
	case !it.player.Ready():
		return "opening"
	case it.player.Buffering():
		return "buffering"
	}
	return fmt.Sprintf("%s / %s", clock(it.player.Position()), clock(it.player.Duration()))
}

// clock is a duration as minutes and seconds.
func clock(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// view builds the window's interface from the state on every frame.
//
// The videos sit directly in the column. A video nested in a Box did not get
// its first picture for a network source in this MyGo build: the player stayed
// not ready with a black area while its clock ran, and the same address played
// once the video was a direct child.
func (a *appState) view(c *ui.Context) {
	defer a.signalFrame()
	// P9: the canvas experiment draws the two probe players and nothing else.
	// Keeping it above the workbench is the point — a picture that never
	// arrives here cannot be excused by the layout around it.
	if a.probe != nil {
		a.probe.view(c)
		return
	}
	// P15.5: the display-scale walk. A zoom lays the view out in a window 1/z
	// its size and draws it z times larger, which is what a larger display scale
	// does to the same window pixels; 0 leaves the scale alone.
	if a.zoom > 0 {
		c.SetZoom(a.zoom)
	}
	a.detectLocale(c.Locale())
	a.syncAppearance()
	c.SetTheme(muxshiftTheme())
	// P10: a watching mode draws the picture and its overlay instead of the
	// workbench. The keys stay available there, because a mode that can only be
	// left with the mouse is a trap.
	if !a.viewing.editing() {
		a.viewingShortcuts(c)
		a.viewingView(c)
		return
	}
	// P15.4: window shortcuts. They are declared every pass because ui actions
	// live one frame; each one sends the same command a button would, against
	// whatever source is selected when the key is pressed.
	//
	// P18.1: none of them fire while a text field has the focus. Ctrl+S in the
	// import box is part of typing, not a request to show a source, and the
	// page keys would move a person away from the text they are in the middle
	// of entering.
	typing := c.FocusTakesText()
	if typing && !a.keysTyping {
		a.typingEdges++
	}
	a.keysTyping = typing
	a.keysSeen++
	if !a.keysTyping {
		c.OnWindowShortcut(ui.Ctrl, ui.KeyS, func() { a.shortcutCommand(commandShow) })
		c.OnWindowShortcut(ui.Ctrl, ui.KeyH, func() { a.shortcutCommand(commandHear) })
		c.OnWindowShortcut(ui.Ctrl, ui.KeyR, func() { a.shortcutCommand(commandRetry) })
		c.OnWindowShortcut(ui.Ctrl, ui.Key1, func() { a.navigateWorkspace(workspaceStage) })
		c.OnWindowShortcut(ui.Ctrl, ui.Key2, func() { a.navigateWorkspace(workspaceDiagnostics) })
		c.OnWindowShortcut(ui.Ctrl, ui.Key3, func() { a.navigateToSettings("") })
		// P10.3: F11 is the standard full-screen key and works from the
		// workbench too, so a person does not have to find Cinema first.
		c.OnWindowShortcut(0, ui.KeyF11, func() { a.enterViewing(viewFullscreen, a.windowRef()) })
		// P12: the arrow keys place the selected picture without a drag, so a
		// fine adjustment does not depend on the pointer's precision. Shift
		// takes a larger step; neither key is bound elsewhere in the workbench.
		c.OnWindowShortcut(0, ui.KeyLeft, func() { a.nudgeSelected(-nudgeStep, 0) })
		c.OnWindowShortcut(0, ui.KeyRight, func() { a.nudgeSelected(nudgeStep, 0) })
		c.OnWindowShortcut(0, ui.KeyUp, func() { a.nudgeSelected(0, -nudgeStep) })
		c.OnWindowShortcut(0, ui.KeyDown, func() { a.nudgeSelected(0, nudgeStep) })
		c.OnWindowShortcut(ui.Shift, ui.KeyLeft, func() { a.nudgeSelected(-nudgeStepFast, 0) })
		c.OnWindowShortcut(ui.Shift, ui.KeyRight, func() { a.nudgeSelected(nudgeStepFast, 0) })
		c.OnWindowShortcut(ui.Shift, ui.KeyUp, func() { a.nudgeSelected(0, -nudgeStepFast) })
		c.OnWindowShortcut(ui.Shift, ui.KeyDown, func() { a.nudgeSelected(0, nudgeStepFast) })
	}
	// P15.1: the workbench. The top bar and the status bar always exist; the
	// rail, the inspector and the dock fold away by window size, and each can
	// be switched by hand. The panes sit beside the stage, never on top of it,
	// so the picture is never covered.
	width, height := c.Size()
	a.lastWidth, a.lastHeight = width, height
	a.detectLocale(c.Locale())
	a.syncAppearance()
	a.shell(c, width, height)
}

// stagePictures is the list of sources the stage draws in this frame: the ones
// the scene shows, in the order the layout places them. A layout that holds
// fewer pictures than the scene shows keeps the extra ones off the stage — it
// never draws the same picture twice to fill a slot.
func (a *appState) stagePictures() []*item {
	var shown []*item
	for _, it := range a.items {
		if it.player != nil && it.shown {
			shown = append(shown, it)
		}
	}
	if len(shown) == 0 {
		return nil
	}
	// The order a person asked for wins; sources not named there follow in
	// scene order, so a scene edited elsewhere still has an order.
	if len(a.layoutTaken) > 0 {
		ordered := make([]*item, 0, len(shown))
		taken := map[*item]bool{}
		for _, id := range a.layoutTaken {
			for _, it := range shown {
				if it.source.ID != id || taken[it] {
					continue
				}
				ordered = append(ordered, it)
				taken[it] = true
			}
		}
		for _, it := range shown {
			if !taken[it] {
				ordered = append(ordered, it)
			}
		}
		shown = ordered
	}
	if max := a.layout.slots(); len(shown) > max {
		shown = shown[:max]
	}
	return shown
}

// setLayout switches the stage arrangement. The scene, the players, the sound
// and the shared clock are untouched: a layout only says where pictures sit.
func (a *appState) setLayout(kind layoutKind) {
	if a.layout == kind {
		return
	}
	a.layout = kind
	a.invalidate()
	a.saveSession()
}

// setPrimary puts a source first in the arrangement, so the layout that has one
// large slot — pip and hero — gives it to that source. The rest keep the order
// the scene already has.
func (a *appState) setPrimary(id core.SourceID) {
	primary := a.itemOf(id)
	if primary == nil {
		return
	}
	if primary.persistentID == "" {
		primary.persistentID = a.nextPersistentSourceID()
	}
	order := a.captureSceneLayout().LayoutOrder
	taken := []core.SourceID{id}
	for _, it := range a.items {
		if it.source.ID != id && it.player != nil && it.shown {
			taken = append(taken, it.source.ID)
		}
	}
	a.layoutTaken = taken
	a.pendingLayoutOrder = append([]string{primary.persistentID}, removeString(order, primary.persistentID)...)
	a.projectLayoutOrder()
	a.invalidate()
	a.saveSession()
}

// stage draws every player in one frame. The scene hides the players it does
// not show instead of removing their elements: the window stops repainting
// when a video element is added or removed while it is running.
//
// P9: a shown source's picture is placed by its own rect, so two shown sources
// put two pictures on the stage at once. A single shown source keeps filling
// the stage exactly as before; the rect path is only entered when the scene
// shows more than one.
func (a *appState) stage(c *ui.Context) {
	stage := ui.Box(c).Fill().Grow(1).Radius(radiusLg).Border(1, colorLine).Background(colorPanel)
	// The box's own size, from the frame before this one. Absolute children
	// placed with percentages resolve against a taller containing box than the
	// one the stage is clipped to, so the arrangement is worked out in pixels.
	bounds := stage.Bounds()
	a.stageW, a.stageH = bounds.W, bounds.H
	a.stageAt = stageBox{X: bounds.X, Y: bounds.Y}
	stage.Children(func() {
		// The arrangement is read once and used both to draw the pictures and
		// to draw their frames: a picture and its handles can never disagree
		// about where it is.
		geometry := a.stageGeometry()
		shown := make([]*item, 0, len(geometry))
		placed := map[*item]slotRect{}
		for i, spot := range geometry {
			shown = append(shown, spot.item)
			placed[spot.item] = slotRect{
				left:   spot.box.X * 100,
				top:    spot.box.Y * 100,
				width:  spot.box.W * 100,
				height: spot.box.H * 100,
				z:      i,
			}
		}
		// A source the arrangement cannot hold stays in the tree — removing a
		// video element while the window runs stops it repainting — but it
		// draws nothing, and it must not fall back to the whole stage: that
		// would paint a picture over the ones the layout did place.
		onStage := map[*item]bool{}
		for _, it := range shown {
			onStage[it] = true
		}
		// place gives one video element its rectangle. The picture ignores the
		// pointer so the stage hears the click: a click lands on the element
		// that draws the video, and the double click asking for full screen is
		// counted on the stage, not below the picture.
		place := func(e ui.Element, it *item, p *ui.VideoPlayer) ui.Element {
			e = e.IgnoreInput(true)
			if !onStage[it] {
				// Hiding alone is not enough: the engine composites a video
				// element on its own and keeps drawing its last frame in the
				// flow. A source the arrangement cannot hold is moved out of
				// the stage and shrunk to nothing as well as hidden.
				return e.Absolute().Left(-4000).Top(-4000).Width(1).Height(1).Hidden(true)
			}
			r := placed[it]
			if bounds.W < 1 || bounds.H < 1 {
				return e.Absolute().LeftPercent(r.left).TopPercent(r.top).
					WidthPercent(r.width).HeightPercent(r.height).Fit(ui.Contain).ZIndex(r.z)
			}
			if len(shown) <= 1 && a.layout == layoutFocus && !a.manualBox(it.source.ID) {
				// Contain, not the raw Fill: a picture stretched to the
				// window's own ratio is not the picture. The stage keeps the
				// bars at the edges instead.
				return e.Fill().Fit(ui.Contain)
			}
			shapeW, shapeH := 0, 0
			if p != nil {
				shapeW, shapeH = p.Size()
			}
			px := slotPixels(r, bounds.W, bounds.H, shapeW, shapeH)
			return e.Absolute().Left(px.left).Top(px.top).
				Width(px.width).Height(px.height).Fit(ui.Contain).ZIndex(px.z)
		}
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			// A replacement waiting for its first picture is drawn under the
			// picture the item already shows, so a viewer sees the old one
			// until the switch commits and never a black stage.
			if it.swap != nil {
				// The replacement follows the source's visibility: a switch of a
				// source that is not in the scene must not take over the stage
				// while it is still waiting. The element stays in the tree
				// either way, because adding and removing video elements while
				// the window runs stops it repainting.
				place(ui.Video(c, it.swap.player), it, it.swap.player).Hidden(!it.shown)
			}
			if it.previous != nil {
				place(ui.Video(c, rawPlayer(it.previous)), it, rawPlayer(it.previous))
			}
			if pending := a.pendingSeekPlayer(it); pending != nil {
				// The replacement is drawn for the same reason the swap
				// candidate is: a player that is never drawn never produces its
				// first frame, and the transaction waits for one. The source
				// keeps its old picture until the commit.
				place(ui.Video(c, rawPlayer(pending)), it, rawPlayer(pending)).Hidden(!it.shown)
			}
			place(ui.Video(c, rawPlayer(it.player)), it, rawPlayer(it.player)).Hidden(!it.shown)
		}
		// A player that has not produced its first frame draws at a size of its
		// own — a thumbnail in the middle of its slot. The element has to keep
		// being drawn or it never produces that frame, so the stage covers it
		// with a placeholder instead of leaving a stray picture.
		for _, it := range shown {
			if it.previous != nil || it.swap != nil || a.pendingSeekPlayer(it) != nil {
				continue
			}
			p := rawPlayer(it.player)
			if w, h := p.Size(); w > 0 && h > 0 {
				continue
			}
			r := placed[it]
			if bounds.W < 1 || bounds.H < 1 {
				continue
			}
			px := slotPixels(r, bounds.W, bounds.H, 0, 0)
			ui.Box(c).Absolute().Left(px.left).Top(px.top).
				Width(px.width).Height(px.height).ZIndex(r.z + 1).
				Background(colorPanel2).Padding(spaceSm).Children(func() {
				ui.Text(c, "waiting for the first picture\n"+a.name(it)).
					FontSize(textSmall).TextColor(colorTextDim)
			})
		}
		// The frames and handles only exist while the workbench is editing; a
		// watching mode has no editable border at all.
		if a.viewing.editing() {
			a.stageEditing(c, geometry, bounds.W, bounds.H)
		}
		if len(shown) == 0 {
			// An empty stage asks for the one thing it needs. When the session
			// already holds sources, opening another one is the wrong offer:
			// this scene is simply empty, and the console is where a picture
			// comes from.
			hasSources := len(a.items) > 0
			ui.Column(c).Fill().Center().Gap(spaceSm).Children(func() {
				ui.Text(c, a.tr("No picture yet", "还没有画面")).
					FontSize(textTitle).TextColor(colorText)
				if hasSources {
					ui.Text(c, a.tr("This scene holds no picture. Add one in the console's sources.",
						"这个场景还没有画面，在控制台的「来源」里把它放上来。")).
						FontSize(textSmall).TextColor(colorDim)
					if ui.Button(c.Key("empty-console"), a.tr("Show the sources", "显示来源")).Height(26).
						Padding(0, spaceMd).Background(colorAccent).TextColor(colorAccentText).Clicked() {
						delete(a.dockHidden, dockSources)
						a.dockUp = true
						a.invalidate()
					}
					return
				}
				ui.Text(c, a.tr("Drop in a file, or paste an address that plays.",
					"打开一个文件，或粘贴一个能播放的地址。")).
					FontSize(textSmall).TextColor(colorDim)
				ui.Row(c).Gap(spaceSm).Padding(0, spaceSm).Children(func() {
					if ui.Button(c.Key("empty-open"), a.tr("Open a file\u2026", "打开文件\u2026")).
						Height(a.controlHeight()).Padding(0, spaceMd).
						Background(colorAccent).TextColor(colorAccentText).Clicked() {
						if path, err := a.chooseLocalFile(); err != nil || path != "" {
							a.acceptPickedFile(path, err)
						}
					}
					if ui.Button(c.Key("empty-address"), a.tr("Add an address\u2026", "添加地址\u2026")).
						Height(a.controlHeight()).Padding(0, spaceMd).Clicked() {
						a.openAddSource()
					}
				})
			})
		}
	})
	// A double click on the picture is the third way into full screen, and it
	// has to work from the workbench too, not only from a watching mode.
	if stage.DoubleClicked() && a.viewing.editing() {
		a.enterViewing(viewFullscreen, a.windowRef())
	}
}

// selectVideo shows a source's picture and makes the inspector follow it.
func (a *appState) selectVideo(it *item) {
	a.selected = it.source.ID
	a.report(a.runCommand(command{Kind: commandShow, Source: it.source.ID}))
}

// toggleVideo is the eye's whole job: showVideo also removes the picture when
// the scene already names it, so showing and hiding are one command.
func (a *appState) toggleVideo(index int) {
	it, err := a.at(index)
	if err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	a.selectVideo(it)
}

// selectAudio plays a source's sound and makes the inspector follow it.
func (a *appState) selectAudio(it *item) {
	a.selected = it.source.ID
	a.report(a.runCommand(command{Kind: commandHear, Source: it.source.ID}))
}

// labelWidth estimates the width a one-line label needs. It is deliberately a
// little generous: a label that is short of its box is fine, a truncated count
// is not.
func labelWidth(label string, size float32) float32 {
	return float32(len([]rune(label)))*size*0.62 + size
}

// first is the item the buttons drive: the first one given.
func (a *appState) first() *item {
	if len(a.items) == 0 {
		return nil
	}
	return a.items[0]
}

// status is one line: the clock of every source, then the sync group, then
// whatever the last action had to report.
func (a *appState) status() string {
	parts := make([]string, 0, len(a.items))
	for i, it := range a.items {
		parts = append(parts, fmt.Sprintf("%d: %s", i+1, status(it)))
	}
	line := strings.Join(parts, "   ·  ")
	if line == "" {
		line = "no media"
	}
	if label := a.syncLabel(); label != "" {
		line += "   ·  " + label
	}
	// A probe's own summary is rendered from the report rather than from the
	// text stored when it finished, so a suggestion the session has outgrown
	// reads as expired here too.
	if a.lastProbe.Target.Source != "" {
		if summary := a.alignLine(); summary != "" {
			line += "   ·  " + summary
		}
	} else if a.syncNote != "" {
		line += "   ·  " + a.syncNote
	}
	if a.lastErr != nil {
		line += "   ·  error: " + a.lastErr.Error()
	}
	return line
}

// allowLabel names what the override does next: a source whose offset is
// unknown is held silent until someone says its sound is safe to hear.
func allowLabel(a *appState, it *item) string {
	if it.audio == "" {
		return "No audio"
	}
	if a.audible[it.audio] {
		return "Hold again"
	}
	return "Hear anyway"
}

// muteLabel names what the mute button does next.
func muteLabel(p *ui.VideoPlayer) string {
	if p.Muted() {
		return "Unmute"
	}
	return "Mute"
}

// capture writes a PNG of the window, or reports why it could not.
//
// It recovers from a panic in the rasteriser: with more than one video in the
// window, MyGo's read-back of a frame that was never delivered panics with
// "slice bounds out of range [:2560] with capacity 0" in mfEngine.readBack
// (mf_windows.go:574, reached from Bitmap.showPicture and raster.restorePixels).
// Recovering keeps the rest of a check running, and the failure is reported
// rather than hidden.
func capture(window *mygo.Window, path string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("capture panicked: %v", r)
		}
	}()
	png, err := window.CapturePage()
	if err == nil {
		err = os.WriteFile(path, png, 0o644)
	}
	if err != nil {
		log.Printf("capture %s: %v", path, err)
		return err
	}
	log.Printf("captured %s", path)
	return nil
}

func main() {
	win.AttachConsole()
	var media multiFlag
	flag.Var(&media, "media", "local file or http(s) address to play; may be repeated")
	replay := flag.String("replay", "", "play one joined recording as the only source, in a window labelled as a recording")
	shot := flag.String("shot", "", "write a PNG of the window here and quit")
	shotDelay := flag.Duration("shot-delay", 6*time.Second, "how long to let the first picture arrive before -shot")
	trace := flag.Bool("trace", false, "log every player's state twice a second")
	flag.StringVar(&replayProbeURL, "replay-url", "", "recorded playlist the replayprobe scenario opens")
	flag.StringVar(&retryProbeMode, "retry-probe", "both", "retryprobe phase order: direct, after or both")
	flag.BoolVar(&retryProbePause, "retry-probe-pause", false, "retryprobe: pause the stage before each request")
	flag.StringVar(&retryProbeBoundary, "retry-probe-boundary", "0", "retryprobe: offset the boundary request asks for")
	flag.StringVar(&retryProbeRetry, "retry-probe-retry", "5", "retryprobe: offset the direct and retry requests ask for")
	scenario := flag.String("scenario", "", "run a scripted check: "+scenarioNames())
	dir := flag.String("dir", "", "directory the scenario writes its PNGs into")
	flag.StringVar(&wakeMode, "wake", wakeMode, "nudge a network player once for its first frame, or none to record what happens without it")
	flag.DurationVar(&handoverWait, "handover", handoverWait, "how long a reopened player may take to show a picture before the previous one is kept")
	settingsFile := flag.String("settings", "", "settings file to read and write; empty uses the per-user configuration directory")
	flag.StringVar(&extraMedia, "scenario-extra", "", "with -scenario sourcememory: the address the check adds by hand")
	flag.StringVar(&scenarioDecoder, "scenario-decoder", "", "with -scenario probeclose: the program a comparison starts instead of ffmpeg")
	flag.StringVar(&outageMode, "scenario-outage", "server", "with -scenario offline: whether the check arranged a stopped server or a stopped publisher")
	frameFlag := flag.String("frame", "", "place the window at x,y,width,height instead of restoring it (a check's starting point)")
	flag.DurationVar(&syncAt, "at", 10*time.Second, "syncmap: the shared event time to measure")
	flag.DurationVar(&syncOffset, "offset", 0, "syncmap: the offset applied to the measured source")
	flag.StringVar(&dvrDir, "dvr-dir", dvrDir, "directory the replay buffer keeps its segments in")
	handoffPort := flag.Int("handoff-port", 8731, "loopback port the browser extension pairs with; the extension supports 8731 only")
	flag.DurationVar(&dvrRetention, "dvr-retention", dvrRetention, "how much history the replay buffer keeps")
	flag.Int64Var(&dvrMaxBytes, "dvr-max-bytes", dvrMaxBytes, "byte limit of the replay buffer; 0 keeps only the retention limit")
	flag.Int64Var(&dvrRecordingBytes, "dvr-recording-bytes", dvrRecordingBytes,
		"bytes of recordings one buffer may keep; 0 means no limit and may fill the disk")
	flag.DurationVar(&commonShift, "common-shift", commonShift,
		"common: the timestamp shift the second fixture was built with; 0 for two identical fixtures")
	showVersion := flag.Bool("version", false, "print the build and exit")
	silent := flag.Bool("silent", false, "start with the master mute on, for checks that should not play sound")
	flag.Parse()
	if *handoffPort != 8731 {
		log.Fatalf("handoff: the extension pairs with 127.0.0.1:8731; -handoff-port %d is not supported", *handoffPort)
	}
	if *showVersion {
		// Before every other check: asking a binary what it is must not need a
		// media address, a window, or a display.
		fmt.Println(version.BuildLine())
		return
	}
	// Before the first request: the streams a browser can open are the ones this
	// window can open, and on this kind of machine that includes the proxy.
	if proxy := win.ApplyProxy(); proxy != "" {
		log.Printf("proxy: using the Windows Internet Settings proxy %s", proxy)
	}
	// -scenario-extra is a check's input, not an option a person can hand to a
	// normal start: an address passed with it would look like a source and be
	// dropped without a word.
	if extraMedia != "" && *scenario != "sourcememory" {
		log.Fatalf("muxshift-desktop: -scenario-extra only goes with -scenario sourcememory")
	}
	if scenarioDecoder != "" {
		if *scenario != "probeclose" {
			log.Fatalf("muxshift-desktop: -scenario-decoder only goes with -scenario probeclose")
		}
		decodeProgram = scenarioDecoder
	}
	if outageMode != "server" && outageMode != "publisher" {
		log.Fatalf("muxshift-desktop: -scenario-outage is server or publisher, not %q", outageMode)
	}
	if outageMode != "server" && *scenario != "offline" {
		log.Fatalf("muxshift-desktop: -scenario-outage only goes with -scenario offline")
	}
	if *replay != "" {
		// A recording is played by a process of its own: this build cannot put a
		// recording on screen inside a session that already presents sources, but
		// it does play one as the only source. The mode is labelled so nobody
		// mistakes it for replay inside a running session.
		if len(media) > 0 {
			log.Fatal("muxshift-desktop: -replay cannot be combined with -media")
		}
		if _, err := os.Stat(*replay); err != nil {
			log.Fatalf("muxshift-desktop: %v", err)
		}
		media = append(media, *replay)
		replayMode = true
	}
	// No media is a real state: first run shows the empty stage with one way
	// in, instead of refusing to open a window.
	for _, uri := range media {
		if *scenario != "" && !isNetworkAddress(uri) {
			if _, err := os.Stat(uri); err != nil {
				log.Fatalf("muxshift-desktop: %v", err)
			}
		}
	}
	if *scenario != "" && *dir == "" {
		log.Fatal("muxshift-desktop: -dir is required with -scenario")
	}
	if *scenario != "" {
		if _, ok := scenarios[*scenario]; !ok {
			log.Fatalf("muxshift-desktop: unknown scenario %q, want %s", *scenario, scenarioNames())
		}
		// A missing output directory used to turn every capture into a logged
		// failure, so the check would look done while it had written nothing.
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			log.Fatalf("muxshift-desktop: cannot create %s: %v", *dir, err)
		}
	}

	// Settings are read before the first source is opened, so a remembered level
	// and mute are in force from the first frame rather than applied on top of a
	// source that already spoke. A file that does not validate is reported and
	// the defaults stay in memory; the file is left alone so it can be read,
	// and a failed read never becomes a write.
	path := settingsPath(*settingsFile)
	loaded, note := loadSettings(path)
	sessionPath := sessionStorePath(path)
	// The command line wins when it names sources: those are what the run is
	// about. Without it the saved arrangement comes back, and one source that
	// cannot reopen keeps its error instead of stopping the start.
	var s *appState
	var err error
	var startupMedia []string
	storeNote := ""
	if len(media) > 0 {
		if *scenario == "" {
			s, err = newAppStateWithSettings(nil, loaded)
			startupMedia = append([]string(nil), media...)
		} else {
			s, err = newAppStateWithSettings(media, loaded)
		}
	} else {
		snap, note := loadSessionStore(sessionPath)
		storeNote = note
		if storeNote != "" {
			log.Printf("session: %s", storeNote)
		}
		if *scenario == "" {
			s, err = newAppStateFromStoreDeferred(loaded, snap)
		} else {
			s, err = newAppStateFromStore(loaded, snap)
		}
	}
	if err != nil {
		log.Fatalf("muxshift-desktop: %v", err)
	}
	s.settingsPath = path
	s.settingsTrusted = note == ""
	s.sessionPath = sessionPath
	// A temporary -media run is isolated from the saved arrangement. A session
	// file that failed to load is likewise protected until the user can recover
	// it; startup must never replace either with an empty/default snapshot.
	s.sessionWriteEnabled = len(media) == 0 && storeNote == ""
	if storeNote != "" {
		s.sessionErr = storeNote + "; saving is disabled to preserve the original session file"
	}
	// A check that takes pictures should not take sound with it: the same master
	// mute the overlay offers is switched on before the first source is opened,
	// so nothing has to be turned down on the machine that runs it.
	if *silent {
		s.masterMute = true
		log.Printf("silent: master mute is on, sound stays off for this run")
	}
	if note != "" {
		log.Printf("settings: %s", note)
	}
	setStartPage := func(page string) {
		switch page {
		case "sources":
			s.navigateWorkspace(workspaceSources)
		case "diagnostics":
			s.navigateWorkspace(workspaceDiagnostics)
		case "settings":
			s.navigateToSettings("")
		}
	}
	setStartPage(loaded.StartPage)
	// The DVR cache root belongs to this process: a sweep collects only the
	// directories of runs that are gone, and the session opened right after keeps
	// this run's own cache distinguishable while it lives.
	s.dvrRoot = dvrDir
	if sweep, err := dvr.SweepSessions(s.dvrRoot); err != nil {
		log.Printf("dvr: sweep %s: %v", s.dvrRoot, err)
	} else {
		log.Printf("dvr: sweep %s: removed %d stale, %d live, %d kept",
			s.dvrRoot, len(sweep.Removed), len(sweep.Live), len(sweep.Kept))
		for _, kept := range sweep.Kept {
			log.Printf("dvr: kept %s: %s", kept.Path, kept.Reason)
		}
	}
	session, err := dvr.OpenSession(s.dvrRoot)
	if err != nil {
		log.Fatalf("muxshift-desktop: %v", err)
	}
	s.dvrSession = session
	log.Printf("dvr: session %s under %s", session.ID, s.dvrRoot)
	// P18.2: a buffer opened now gets the budget from "explicit flag > settings
	// file > built-in defaults". Which flags were actually typed matters, so it
	// is read from flag.Visit rather than by comparing values with the defaults:
	// "-dvr-max-bytes 0" is an explicit choice and must stay one.
	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	s.budgetFlags = explicit
	s.flagBudget = dvrBudget{
		RetentionSeconds: int(dvrRetention / time.Second),
		MaxBytes:         dvrMaxBytes,
		RecordingBytes:   dvrRecordingBytes,
	}
	if err := s.resolveBudget(); err != nil {
		log.Fatalf("muxshift-desktop: %v", err)
	}
	s.apply()
	log.Printf("settings: start page %q, file %s", loaded.StartPage, s.settingsPath)
	for i, it := range s.items {
		log.Printf("source %d: %s · video=%q audio=%q open=%v", i+1, shortURI(it.uri), it.video, it.audio, errorString(it.openErr))
	}
	log.Printf("scene %s: %s", s.scene, s.sceneLabel())
	// The shared event clock is wall time while the group plays and stands
	// still while it is paused; Core's Playhead owns that rule.
	go func() {
		for range time.Tick(200 * time.Millisecond) {
			mygoRunOnMain(func() {
				if err := s.sync.tick(time.Now()); err != nil {
					log.Printf("sync clock: %v", err)
				}
			})
		}
	}()
	if *trace {
		go func() {
			for range time.Tick(500 * time.Millisecond) {
				for i, it := range s.items {
					if it.player == nil {
						log.Printf("trace %d: no player", i+1)
						continue
					}
					p := it.player
					log.Printf("trace %d: ready=%v playing=%v muted=%v buffering=%v duration=%s position=%s err=%v",
						i+1, p.Ready(), p.Playing(), p.Muted(), p.Buffering(), p.Duration(), p.Position(), p.Err())
				}
			}
		}()
	}

	// The P13.3 experiment registers its frontend before the app starts: the
	// frontend is read when the run boots, not when the window appears, so a
	// page registered inside WhenReady would never be served.
	if *scenario == "webview" {
		if _, err := startPanelFrontend(s); err != nil {
			log.Fatalf("muxshift-desktop: panel frontend: %v", err)
		}
	}

	mygo.App.WhenReady(func() {
		title := "MuxShift"
		if replayMode {
			title = "MuxShift replay (recording)"
		}
		// A scenario is read from its capture as well as its log, and the status
		// text sits below the buttons, so the scripted runs get a window tall
		// enough for one row per source plus the status line instead of a capture
		// that cuts the answer off.
		height := 600
		if *scenario != "" {
			height = 700 + 70*len(media)
		}
		if *scenario == "webview" {
			// The native stage, one card per source and the page below them.
			height = 1080 + 70*len(media)
		}
		window := mygo.NewWindow(mygo.WindowOptions{
			Title:   title,
			Width:   1180,
			Height:  height,
			Content: ui.View(s.view),
		})
		win.InstallWindowIcon(window)
		s.window = window
		s.sessionSaveMu.Lock()
		s.sessionSaveNotify = mygoRunOnMain
		s.sessionSaveMu.Unlock()
		// P5.3: the browser's only way in is a loopback socket this window
		// owns. It starts with the window and stops with it; a port already in
		// use is a visible reason, never a quiet move to another port.
		if handoff, err := startHandoff(s, *handoffPort); err != nil {
			s.handoffErr = err.Error()
			log.Printf("handoff: %v", err)
		} else {
			s.handoff = handoff
		}
		// A scripted run says what size it needs; a person's window comes back
		// the way they left it (P18.1). The restore check is the one scripted
		// run that has to see the restore itself. A frame given on the command
		// line is where a check starts, so the window goes there and the saved
		// frame is left alone.
		switch {
		case *frameFlag != "" && *scenario == "":
			if frame, ok := parseFrame(*frameFlag); ok {
				window.SetBounds(frame)
				log.Printf("window: placed at %d,%d %dx%d from -frame", frame.X, frame.Y, frame.Width, frame.Height)
			} else {
				log.Printf("window: -frame %q is not x,y,width,height", *frameFlag)
			}
		case *scenario == "" || *scenario == "workspace-restore":
			s.restoreWorkspace(window, mygo.Screen.Displays())
		}
		if *scenario == "webview" {
			view, err := window.NewWebView(mygo.WebViewOptions{URL: "/panel.html"})
			if err != nil {
				log.Fatalf("muxshift-desktop: web view: %v", err)
			}
			s.webView = view
			// The frames built before the view existed did not ask for it, so
			// the window has to build one that does.
			window.Invalidate()
		}
		window.OnClose(func(*mygo.CloseEvent) {
			// P18.1: one close, recorded. A window that closed twice would
			// leave a reader wondering whether the second pass released
			// anything twice; this line and the release below answer that
			// from the log.
			s.closes++
			log.Printf("focus: the window closed (%d time(s) so far)", s.closes)
			// A read that is still running must not land in a window that is
			// gone: the landing writes the session and the players, and both
			// belong to this window's drawing thread.
			s.closed = true
			if s.startupRestoreCancel != nil {
				s.startupRestoreCancel()
				s.startupRestoreCancel = nil
			}
			s.closeImportTicker()
			// The listener holds nothing on disk, but a closed window must not
			// leave an accepting socket behind.
			s.handoff.stop()
			// The window is still the window here, so this is the last moment
			// its bounds can be read; a scripted run leaves the file alone.
			if *scenario == "" {
				s.saveWorkspaceOnExit(window)
				// Save the arrangement last, while the session and the players
				// are still alive, so the next start comes back to it.
				s.saveSession()
				if err := s.flushSessionSave(); err != nil {
					log.Printf("session: final save failed (%v)", err)
				}
			}
			s.release()
		})
		if *shot != "" {
			time.AfterFunc(*shotDelay, func() {
				// The window and the players belong to the main thread: capturing and
				// closing from the timer's goroutine races a frame that is drawing.
				mygoRunOnMain(func() {
					capture(window, *shot)
					s.release()
					mygo.App.Quit()
				})
			})
		}
		if *scenario != "" {
			scenarios[*scenario](window, s, *dir)
		} else if len(startupMedia) > 0 {
			s.startCommandLineSources(startupMedia)
		} else {
			s.startStoredSourceRestores()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	if *scenario != "" && scenarioFailed.Load() {
		log.Fatalf("%s: scenario assertions failed", *scenario)
	}
}

// multiFlag collects a flag given more than once, in order.
type multiFlag []string

// String is the addresses joined for printing.
func (m *multiFlag) String() string { return strings.Join(*m, ", ") }

// Set adds one address.
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
