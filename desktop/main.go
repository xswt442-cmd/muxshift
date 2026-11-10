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
		it.openErr = err
		return
	}
	it.openErr = nil
	var kept *handover
	if previous != nil {
		kept = &handover{previous: previous}
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
// directory, which the buffer removes again when the session ends.
var (
	dvrDir       = filepath.Join(os.TempDir(), "muxshift-dvr")
	dvrRetention = 60 * time.Second
	dvrMaxBytes  int64
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
		replacement = it.player
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
	return strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://")
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
	if !needsWake(uri) {
		return
	}
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
		return "closed"
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
	c.SetTheme(muxshiftTheme())
	ui.Column(c).Fill().Background(colorBg).Padding(spaceLg).Gap(spaceMd).Children(func() {
		a.headerRow(c)
		a.stage(c)
		a.transportRow(c)
		for i, it := range a.items {
			a.sourceCard(c, i, it)
		}
		ui.Text(c, a.status()).FontSize(textSmall).TextColor(colorTextDim).FontFeatures("tnum")
		if a.syncNote != "" {
			ui.Text(c, a.syncNote).FontSize(textSmall).TextColor(colorTextDim)
		}
	})
}

// headerRow is the title and the two chips that say which scene is live and
// how the session is doing. It is the same top bar the web page has.
func (a *appState) headerRow(c *ui.Context) {
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "MuxShift").FontSize(textTitle).TextColor(colorText)
		pill(c, a.sceneLabel(), colorAccent, true)
		label := fmt.Sprintf("%d source", len(a.items))
		if len(a.items) != 1 {
			label += "s"
		}
		pill(c, label, colorTextDim, false)
		if a.lastErr != nil {
			pill(c, "error: "+a.lastErr.Error(), colorBad, true)
		}
	})
}

// stage draws every player in one frame. The scene hides the players it does
// not show instead of removing their elements: the window stops repainting
// when a video element is added or removed while it is running.
func (a *appState) stage(c *ui.Context) {
	ui.Box(c).Fill().Grow(1).Radius(radiusLg).Border(1, colorLine).Background(colorPanel).Children(func() {
		shown := 0
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			if it.previous != nil {
				ui.Video(c, it.previous).Fill()
				shown++
			}
			ui.Video(c, it.player).Fill().Hidden(!it.shown)
			if it.shown {
				shown++
			}
		}
		if shown == 0 {
			ui.Text(c, "no picture yet\nopen a source, or press Show on a card").
				FontSize(textBody).TextColor(colorTextDim).Center().Fill()
		}
	})
}

// transportRow is play/pause for the whole group plus the actions that act on
// the first source, as the web page's control bar does.
func (a *appState) transportRow(c *ui.Context) {
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		label := "Pause all"
		if !a.sync.playhead.Playing() {
			label = "Play all"
		}
		if ui.Button(c.Key("pause-all"), label).Width(96).Clicked() {
			var problems []string
			if a.sync.playhead.Playing() {
				problems = a.pauseAll()
			} else {
				problems = a.playAll()
			}
			if len(problems) > 0 {
				a.syncNote = "sync: group command reported: " + strings.Join(problems, "; ")
				log.Printf("group command: %s", strings.Join(problems, "; "))
			} else {
				members, _ := a.group()
				a.syncNote = "sync: group command done for " + itoa(len(members)) + " source(s)"
			}
		}
		if main := a.first(); main != nil && main.player != nil {
			if ui.Button(c.Key("mute"), muteLabel(main.player)).Width(88).Clicked() {
				main.player.SetMuted(!main.player.Muted())
			}
		}
		if ui.Button(c.Key("reload"), "Reload").Width(88).Clicked() {
			if first := a.first(); first != nil {
				a.reopenPlayer(first)
			}
		}
		if ui.Button(c.Key("quit"), "Close").Width(88).Clicked() {
			a.release()
			mygo.App.Quit()
		}
	})
}

// sourceCard is one source: its name, what the scene does with it, and the
// controls that change that. The web page lists the same fields in the same
// order, which is what keeps the two clients recognisable as one product.
func (a *appState) sourceCard(c *ui.Context, i int, it *item) {
	index := i
	item := it
	ui.Row(c).Gap(spaceMd).Padding(spaceMd).Radius(radiusMd).Border(1, colorLine).
		Background(colorPanel).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(spaceXs).Grow(1).Children(func() {
			ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
				ui.Text(c, itoa(i+1)).FontSize(textMicro).TextColor(colorTextDim).FontFeatures("tnum")
				ui.Text(c, shortURI(item.uri)).FontSize(textBody).TextColor(colorText)
				if item.shown {
					pill(c, "PICTURE", colorAccent, true)
				}
				heard := item.audio != "" && item.player != nil && !item.player.Muted()
				if heard {
					pill(c, "SOUND", colorSound, true)
				}
				if item.openErr != nil {
					pill(c, "error", colorBad, true)
				}
			})
			ui.Text(c, status(item)).FontSize(textMicro).TextColor(colorTextDim).FontFeatures("tnum")
		})
		if item.video != "" {
			if ui.Button(c.Key("show-"+itoa(i)), "Show").Width(72).Clicked() {
				if err := a.showVideo(index); err != nil {
					a.lastErr = err
				}
			}
		}
		if item.audio != "" {
			if ui.Button(c.Key("hear-"+itoa(i)), "Hear").Width(72).Clicked() {
				if err := a.hearAudio(index, item.volume); err != nil {
					a.lastErr = err
				}
			}
			if ui.Button(c.Key("allow-"+itoa(i)), allowLabel(a, item)).Width(96).Clicked() {
				if err := a.allowAudio(index); err != nil {
					a.lastErr = err
				}
			}
			if ui.Button(c.Key("vol-down-"+itoa(i)), "Vol -").Width(64).Clicked() {
				if err := a.setVolume(index, max(0, item.volume-0.25)); err != nil {
					a.lastErr = err
				}
			}
			if ui.Button(c.Key("vol-up-"+itoa(i)), "Vol +").Width(64).Clicked() {
				if err := a.setVolume(index, min(1, item.volume+0.25)); err != nil {
					a.lastErr = err
				}
			}
		}
		if item.video != "" || item.audio != "" {
			// A tenth of a second is the step a person can still see on a
			// timecode; the report after each press says where the player
			// actually landed.
			if ui.Button(c.Key("back-"+itoa(i)), "-0.1s").Width(64).Clicked() {
				a.nudgeAndRecord(index, -100*time.Millisecond)
			}
			if ui.Button(c.Key("fwd-"+itoa(i)), "+0.1s").Width(64).Clicked() {
				a.nudgeAndRecord(index, 100*time.Millisecond)
			}
			if i > 0 {
				// The probe reads both files and offers an offset; it does
				// not move either player or the timeline.
				if ui.Button(c.Key("probe-"+itoa(i)), "Try align").Width(88).Clicked() {
					a.probeAsync(0, index)
				}
			}
		}
	})
}

// pill draws a short label, the desktop equivalent of the web page's .chip and
// .badge elements. MyGo boxes do not size themselves around their children, so
// the label is drawn as coloured text between two thin rules instead.
func pill(c *ui.Context, label string, tint ui.Color, on bool) {
	textColor := colorTextDim
	if on {
		textColor = tint
	}
	ui.Text(c, label).FontSize(textMicro).TextColor(textColor)
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
	line := strings.Join(parts, "   璺?  ")
	if line == "" {
		line = "no media"
	}
	if label := a.syncLabel(); label != "" {
		line += "   璺?  " + label
	}
	if a.syncNote != "" {
		line += "   璺?  " + a.syncNote
	}
	if a.lastErr != nil {
		line += "   璺?  error: " + a.lastErr.Error()
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
	var media multiFlag
	flag.Var(&media, "media", "local file or http(s) address to play; may be repeated")
	replay := flag.String("replay", "", "play one joined recording as the only source, in a window labelled as a recording")
	shot := flag.String("shot", "", "write a PNG of the window here and quit")
	shotDelay := flag.Duration("shot-delay", 6*time.Second, "how long to let the first picture arrive before -shot")
	trace := flag.Bool("trace", false, "log every player's state twice a second")
	flag.StringVar(&replayProbeURL, "replay-url", "", "recorded playlist the replayprobe scenario opens")
	scenario := flag.String("scenario", "", "run a scripted check: "+scenarioNames())
	dir := flag.String("dir", "", "directory the scenario writes its PNGs into")
	flag.StringVar(&wakeMode, "wake", wakeMode, "nudge a network player once for its first frame, or none to record what happens without it")
	flag.DurationVar(&handoverWait, "handover", handoverWait, "how long a reopened player may take to show a picture before the previous one is kept")
	flag.DurationVar(&syncAt, "at", 10*time.Second, "syncmap: the shared event time to measure")
	flag.DurationVar(&syncOffset, "offset", 0, "syncmap: the offset applied to the measured source")
	flag.StringVar(&dvrDir, "dvr-dir", dvrDir, "directory the replay buffer keeps its segments in")
	flag.DurationVar(&dvrRetention, "dvr-retention", dvrRetention, "how much history the replay buffer keeps")
	flag.Int64Var(&dvrMaxBytes, "dvr-max-bytes", dvrMaxBytes, "byte limit of the replay buffer; 0 keeps only the retention limit")
	flag.Parse()
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
	if len(media) == 0 {
		log.Fatal("muxshift-desktop: -media or -replay is required")
	}
	for _, uri := range media {
		if !isNetworkAddress(uri) {
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

	s, err := newAppState(media)
	if err != nil {
		log.Fatalf("muxshift-desktop: %v", err)
	}
	for i, it := range s.items {
		log.Printf("source %d: %s 璺?video=%q audio=%q open=%v", i+1, it.uri, it.video, it.audio, errorString(it.openErr))
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
		window := mygo.NewWindow(mygo.WindowOptions{
			Title:   title,
			Width:   960,
			Height:  height,
			Content: ui.View(s.view),
		})
		window.OnClose(func(*mygo.CloseEvent) { s.release() })
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
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
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
