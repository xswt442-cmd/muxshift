package main

import (
	"context"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// This file groups the scenario entry points in this domain. It was merged from canvas_scenario.go, canvaslive_scenario.go, commands_scenario.go, dpi_scenario.go, enter_scenario.go, focus_scenario.go, hidden_scenario.go, keys_scenario.go, layout_scenario.go, narrow_scenario.go, pages_scenario.go, realdpi_scenario.go, settings_scenario.go, sizes_scenario.go, timeline_scenario.go, viewing_scenario.go, webview_scenario.go, windows_scenario.go, workbench_scenario.go, workspace_restore_scenario.go, workspace_scenario.go.

type canvasRect struct {
	left, top, width, height float32
	name                     string
}

var canvasRects = []canvasRect{
	{left: 1, top: 2, width: 55, height: 96, name: "left"},
	{left: 58, top: 2, width: 41, height: 96, name: "right"},
}

// canvasProbe is the state of the experiment: the players and where each is
// drawn.
type canvasProbe struct {
	players []*ui.VideoPlayer
	uris    []string
}

// runCanvas starts the probe after the window exists.
func runCanvas(window *mygo.Window, a *appState, dir string) {
	go canvasBody(window, a, dir)
}

func canvasBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "canvas"
	probe := &canvasProbe{}

	// Two sources, opened the way the application opens any source: on the
	// drawing thread, through the same helper the session uses. A player made
	// off that thread never produced a picture in this build.
	for i, it := range a.items {
		if i >= len(canvasRects) {
			break
		}
		player := rawPlayer(it.player)
		if player == nil {
			opened, err := openPlayerOnMain(it.uri)
			if err != nil {
				logf(scenario, "CHECK FAILED: source %d does not open: %v", i+1, err)
				continue
			}
			player = opened
		}
		probe.players = append(probe.players, player)
		probe.uris = append(probe.uris, it.uri)
		logf(scenario, "rect %q: source %d %s", canvasRects[i].name, i+1, shortURI(it.uri))
	}
	if len(probe.players) < 2 {
		logf(scenario, "CHECK FAILED: the probe needs two sources, got %d", len(probe.players))
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	mygoRunOnMain(func() {
		a.probe = probe
		window.Invalidate()
	})

	left, right := probe.players[0], probe.players[1]
	var startBounds mygo.Rectangle
	mygoRunOnMain(func() { startBounds = window.Bounds() })
	logf(scenario, "window starts at %dx%d", startBounds.Width, startBounds.Height)

	// The script: each step either acts on the running window or takes a
	// picture. A step that acts says what it did, so the log can be read next
	// to the picture that follows it.
	type canvasStep struct {
		at    time.Duration
		label string
		act   func()
		shot  bool
	}
	steps := []canvasStep{
		{2 * time.Second, "start", nil, true},
		{10 * time.Second, "resize", func() {
			window.SetBounds(mygo.Rectangle{X: startBounds.X, Y: startBounds.Y, Width: 900, Height: 620})
		}, false},
		{13 * time.Second, "after resize", nil, true},
		{16 * time.Second, "restore size", func() { window.SetBounds(startBounds) }, false},
		{19 * time.Second, "after size restore", nil, true},
		{22 * time.Second, "pause right", func() { right.Pause() }, false},
		{25 * time.Second, "paused right", nil, true},
		{27 * time.Second, "resume right", func() { right.Play() }, false},
		{30 * time.Second, "minimize", func() { window.Minimize() }, false},
		{34 * time.Second, "restore after minimize", func() { window.Restore() }, false},
		{38 * time.Second, "after window restore", nil, true},
		{62 * time.Second, "end", nil, true},
	}

	started := time.Now()
	mygoRunOnMain(func() {
		left.Play()
		right.Play()
	})
	last := map[int]time.Duration{}
	stalled := map[int]int{}
	next := 0
	deadline := started.Add(65 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		now := time.Since(started)
		for i, p := range probe.players {
			pos := p.Position()
			if pos == last[i] {
				stalled[i]++
			} else {
				stalled[i] = 0
			}
			if now >= 5*time.Second && stalled[i] > 6 && !expectPaused(i, now) {
				logf(scenario, "STALLED: rect %q has not moved for %s at %s",
					canvasRects[i].name, pos, now.Truncate(time.Second))
				stalled[i] = 0
			}
			last[i] = pos
		}
		if next >= len(steps) || now < steps[next].at {
			continue
		}
		step := steps[next]
		if step.act != nil {
			mygoRunOnMain(step.act)
		}
		if step.shot {
			path := filepath.Join(dir, fmt.Sprintf("%d-%s.png", next+1, slug(step.label)))
			// The raster is painted between passes: capture after asking for a
			// pass, or the picture written belongs to the frame before.
			mygoRunOnMain(func() { window.Invalidate() })
			time.Sleep(300 * time.Millisecond)
			mygoRunOnMain(func() { _ = capture(window, path) })
			logf(scenario, "capture %s at %s: %s", path, now.Truncate(time.Second), probe.report())
		} else {
			logf(scenario, "step %q at %s: %s", step.label, now.Truncate(time.Second), probe.report())
		}
		next++
	}
	logf(scenario, "final: %s", probe.report())
	mygoRunOnMain(func() {
		a.probe = nil
		a.release()
		mygo.App.Quit()
	})
}

// expectPaused reports whether a still position at this moment is what the
// script asked for rather than a failure: the right-hand video is paused from
// 22s to 27s, and the window is minimized from 30s to 34s.
func expectPaused(rect int, now time.Duration) bool {
	if rect == 1 && now >= 22*time.Second && now < 29*time.Second {
		return true
	}
	return now >= 30*time.Second && now < 36*time.Second
}

// slug turns a step's words into a file name.
func slug(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// report is what each player says about itself, in one line.
func (p *canvasProbe) report() string {
	line := ""
	for i, player := range p.players {
		line += fmt.Sprintf("rect %q ready=%v playing=%v pos=%s dur=%s err=%v | ",
			canvasRects[i].name, player.Ready(), player.Playing(),
			player.Position().Truncate(time.Millisecond), player.Duration().Truncate(time.Millisecond), player.Err())
	}
	return line
}

// view draws nothing but the two rectangles.
func (p *canvasProbe) view(c *ui.Context) {
	c.SetTheme(muxshiftTheme())
	ui.Column(c).Fill().Background(colorBg).Children(func() {
		for i, player := range p.players {
			if player == nil || i >= len(canvasRects) {
				continue
			}
			rect := canvasRects[i]
			ui.Video(c, player).Absolute().
				LeftPercent(rect.left).TopPercent(rect.top).
				WidthPercent(rect.width).HeightPercent(rect.height).
				Fit(ui.Contain)
		}
	})
}

func runCanvasLive(window *mygo.Window, a *appState, dir string) {
	go canvasLiveBody(window, a, dir)
}

func canvasLiveBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "canvaslive"
	if len(a.items) < 2 {
		logf(scenario, "CHECK FAILED: the check needs two sources, got %d", len(a.items))
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	first, second := a.items[0], a.items[1]
	if first.video == "" || second.video == "" {
		logf(scenario, "CHECK FAILED: a source has no video track (%q, %q)", first.video, second.video)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}

	var fail string
	mygoRunOnMain(func() {
		for _, it := range []*item{first, second} {
			if err := a.ensurePlayer(it); err != nil {
				fail = fmt.Sprintf("source %s does not open: %v", a.name(it), err)
				return
			}
		}
		// The scene is what says which pictures are on the stage; giving it
		// both tracks is the whole of the multi-picture request.
		if _, err := a.session.SetSceneVideo(a.scene, first.video, second.video); err != nil {
			fail = fmt.Sprintf("scene does not take two tracks: %v", err)
			return
		}
		a.apply()
		// Play all is the product's own start: the shared clock and every
		// player in the group. Starting only the players would leave the top
		// bar saying "paused" while two pictures ran, which is the mismatch
		// this run is supposed to rule out, not reproduce.
		if problems := a.playAll(); len(problems) > 0 {
			fail = fmt.Sprintf("play all: %v", problems)
			return
		}
		window.Invalidate()
	})
	if fail != "" {
		logf(scenario, "CHECK FAILED: %s", fail)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	logf(scenario, "scene shows two sources: %s and %s", a.name(first), a.name(second))

	type liveStep struct {
		at    time.Duration
		label string
		act   func()
		shot  bool
	}
	var startBounds mygo.Rectangle
	mygoRunOnMain(func() { startBounds = window.Bounds() })
	steps := []liveStep{
		{2 * time.Second, "start", nil, true},
		{10 * time.Second, "leave for sources", func() { a.workspace = workspaceSources }, false},
		{13 * time.Second, "back to stage", func() { a.workspace = workspaceStage }, false},
		{15 * time.Second, "after page return", nil, true},
		{20 * time.Second, "reload right", func() {
			a.report(a.runCommand(command{Kind: commandReload, Source: second.source.ID}))
		}, false},
		{28 * time.Second, "after reload", nil, true},
		{32 * time.Second, "resize", func() {
			window.SetBounds(mygo.Rectangle{X: startBounds.X, Y: startBounds.Y, Width: 900, Height: 620})
		}, false},
		{36 * time.Second, "after resize", nil, true},
		{62 * time.Second, "end", nil, true},
	}

	started := time.Now()
	deadline := started.Add(65 * time.Second)
	next := 0
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		now := time.Since(started)
		if next >= len(steps) || now < steps[next].at {
			continue
		}
		step := steps[next]
		if step.act != nil {
			mygoRunOnMain(func() {
				step.act()
				window.Invalidate()
			})
		}
		if step.shot {
			path := filepath.Join(dir, fmt.Sprintf("%d-%s.png", next+1, slug(step.label)))
			mygoRunOnMain(func() { window.Invalidate() })
			time.Sleep(300 * time.Millisecond)
			mygoRunOnMain(func() { _ = capture(window, path) })
			logf(scenario, "capture %s at %s: %s", path, now.Truncate(time.Second), liveReport(a))
		} else {
			logf(scenario, "step %q at %s: %s", step.label, now.Truncate(time.Second), liveReport(a))
		}
		next++
	}
	logf(scenario, "final: %s", liveReport(a))
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

// liveReport says what each source's player reports, in one line, so the log
// and the pictures can be read together.
func liveReport(a *appState) string {
	line := ""
	for i, it := range a.items {
		if it.player == nil {
			line += fmt.Sprintf("source %d: no player | ", i+1)
			continue
		}
		line += fmt.Sprintf("source %d shown=%v ready=%v playing=%v pos=%s err=%v | ",
			i+1, it.shown, it.player.Ready(), it.player.Playing(),
			it.player.Position().Truncate(time.Millisecond), it.player.Err())
	}
	return line
}

func snapshotLine(s sessionSnapshot) string {
	line := "picture=" + tracksLine(s.Picture)
	line += " sound="
	for i, track := range s.Sound {
		if i > 0 {
			line += ","
		}
		line += string(track.Track) + "@" + itoa(int(track.Volume*100)) + " muted=" + boolWord(track.Muted)
	}
	line += " sources="
	for i, source := range s.Sources {
		if i > 0 {
			line += " | "
		}
		line += string(source.ID) + " gen=" + itoa(source.Generation) +
			" shown=" + boolWord(source.Shown) + " player=" + boolWord(source.Player) +
			" ready=" + boolWord(source.Ready) + " pos=" + source.Position.String()
	}
	line += " event=" + s.Event.String() + " playing=" + boolWord(s.Playing) +
		" group=" + emptyWord(s.Group) + " last=" + s.LastCommand
	return line
}

func tracksLine(tracks []core.TrackID) string {
	if len(tracks) == 0 {
		return "none"
	}
	out := ""
	for i, track := range tracks {
		if i > 0 {
			out += ","
		}
		out += string(track)
	}
	return out
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func emptyWord(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// waitUntilReady polls the players without issuing any command, so the
// commands that follow run against a picture that already exists.
func waitUntilReady(a *appState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var allReady bool
		mygoRunOnMain(func() {
			for _, it := range a.items {
				if it.player == nil || !it.player.Ready() {
					return
				}
			}
			allReady = len(a.items) > 0
		})
		if allReady {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func runCommands(window *mygo.Window, a *appState, dir string) {
	if len(a.items) < 2 {
		logf("commands", "this scenario needs two sources, got %d", len(a.items))
		return
	}
	// A scenario runs on the UI goroutine, so anything slow has to move off it:
	// sleeping here stops the window from drawing the very picture this
	// scenario is about, and the players then report no picture at all.
	go func() {
		time.Sleep(2 * time.Second)
		first := a.items[0]
		second := a.items[1]

		run := func(cmd command) commandResult {
			var result commandResult
			mygoRunOnMain(func() { result = a.runCommand(cmd) })
			logf("commands", "command: %s", result.String())
			logf("commands", "snapshot: %s", snapshotLine(a.readSnapshotOnMain()))
			return result
		}

		logf("commands", "start: %s", snapshotLine(a.readSnapshotOnMain()))
		if !waitUntilReady(a, 30*time.Second) {
			logf("commands", "players did not report a picture before the first command: %s", snapshotLine(a.readSnapshotOnMain()))
		} else {
			logf("commands", "every player reports a picture: %s", snapshotLine(a.readSnapshotOnMain()))
		}

		run(command{Kind: commandShow, Source: first.source.ID})
		run(command{Kind: commandHear, Source: second.source.ID})
		run(command{Kind: commandVolume, Source: second.source.ID, Volume: 0.4})
		// A nudge before playback has produced any history is refused by design,
		// so the scene runs for a moment first and the accepted nudge is the one
		// the log keeps as evidence.
		time.Sleep(2 * time.Second)
		// The nudge is measured from the shared event time, which only means
		// anything once the playhead has been anchored to a real position.
		mygoRunOnMain(func() {
			if err := a.anchor(0); err != nil {
				logf("commands", "cannot anchor the event clock: %v", err)
			}
		})
		run(command{Kind: commandNudge, Source: second.source.ID, Delta: -100 * time.Millisecond})
		run(command{Kind: commandPause})
		run(command{Kind: commandPlay})
		run(command{Kind: commandReload, Source: first.source.ID})
		time.Sleep(3 * time.Second)
		// An identifier that is not in the session must be refused, not guessed by
		// row number.
		run(command{Kind: commandShow, Source: "source-99"})

		step(window, a, dir, "commands", "1-after-commands.png", "after running every command once", nil)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

func runDPI(window *mygo.Window, a *appState, dir string) {
	go dpiBody(window, a, dir)
}

func dpiBody(window *mygo.Window, a *appState, dir string) {
	scales := []struct {
		name string
		zoom float32
	}{
		{"100", 1},
		{"125", 1.25},
		{"150", 1.5},
		{"200", 2},
	}
	// The same window pixels at every scale: 1426×862 is the default client size
	// at 125% and the widest the host screen allows, and 900×600 is small enough
	// to fold the rail at 100%.
	sizes := []struct {
		name string
		w, h int
	}{
		{"full", 1426, 862},
		{"small", 900, 600},
	}
	for _, scale := range scales {
		for _, size := range sizes {
			step(window, a, dir, "dpi", fmt.Sprintf("%s-%s.png", scale.name, size.name),
				fmt.Sprintf("scale %s%% in a %dx%d window", scale.name, size.w, size.h),
				func(a *appState) {
					a.zoom = scale.zoom
					window.SetSize(size.w, size.h)
				})
			mygoRunOnMain(func() {
				logf("dpi", "scale %s%% at %dx%d: the content sees %.1fx%.1f DIPs (rail %v, inspector %v, dock %v)",
					scale.name, size.w, size.h, a.lastWidth, a.lastHeight,
					a.railOpen(a.lastWidth), a.inspectorOpen(a.lastWidth),
					a.dockOpen(a.lastWidth, a.lastHeight))
			})
		}
	}
	// Leave the window as the other scenarios find it.
	step(window, a, dir, "dpi", "restored.png", "back at 100%", func(a *appState) {
		a.zoom = 1
		window.SetSize(1426, 862)
	})
	logf("dpi", "done: %d sources", len(a.items))
	a.release()
	mygo.App.Quit()
}

// railOpen, inspectorOpen and dockOpen report the folds the workbench itself
// would draw, so a scale check says what the DIPs it measured should have folded
// instead of repeating the thresholds beside them.
func (a *appState) railOpen(width float32) bool { return a.rail.shown(width >= railAutoWidth) }

func (a *appState) inspectorOpen(width float32) bool {
	return a.inspector.shown(width >= inspectorAutoWidth)
}

func (a *appState) dockOpen(width, height float32) bool {
	return a.dock.shown(a.settings.DockOpen && height >= dockAutoHeight)
}

func runEnterReplay(window *mygo.Window, a *appState, dir string) {
	go enterReplayBody(window, a, dir, 2)
}

func runEnterReplayOne(window *mygo.Window, a *appState, dir string) {
	go enterReplayBody(window, a, dir, 1)
}

func enterReplayBody(window *mygo.Window, a *appState, dir string, want int) {
	const scenario = "enterreplay"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			failures++
			logf(scenario, "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < want {
		logf(scenario, "needs %d network source(s), got %d", want, len(a.items))
		return
	}
	uris := make([]string, 0, want)
	for index := 0; index < want; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", index+1, shortURI(it.uri))
			return
		}
		uris = append(uris, it.uri)
	}

	runners := make([]*dvrRunner, 0, want)
	for index, uri := range uris {
		name := "enter-" + shortName(index)
		runner, err := startDVR(uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uri, runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for index, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", index+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 90*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 90s", index+1)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := runner.waitBufferAnchor(ctx, windowOf)
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		logf(scenario, "source %d buffers %s", index+1, logRange(windowOf))
	}

	// The live stage has to be on the event clock before an entry can mean
	// anything: anchoring the picture in front of the person, then nudging the
	// others, is the same path a person takes.
	step(window, a, dir, scenario, "", "put the live stage on the event clock", func(a *appState) {
		a.showVideo(0)
	})
	time.Sleep(500 * time.Millisecond)
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor source 1: %v", anchorErr)
		return
	}
	for index := 1; index < want; index++ {
		var note string
		mygoRunOnMain(func() { note, _ = a.nudge(index, 10*time.Millisecond) })
		logf(scenario, "source %d nudged by 10ms: %s", index+1, note)
	}
	time.Sleep(300 * time.Millisecond)
	step(window, a, dir, scenario, "1-live.png", "the live stage before the entry", func(a *appState) {
		a.showVideo(0)
	})

	generations := make([]int, want)
	for index := 0; index < want; index++ {
		generations[index] = a.items[index].generation
	}

	// Hold the commit after settlement, so the picture can be captured while the
	// replacements already have their first frame and the stage is still live.
	release := make(chan struct{})
	var entryErr error
	mygoRunOnMain(func() {
		a.beforeSeekCommit = func() { <-release }
		entryErr = a.startEnterReplayTxn(a.items, 0, false)
	})
	entryNote := ""
	if entryErr != nil {
		entryNote = entryErr.Error()
	}
	logf(scenario, "the entry said: %s", entryNote)
	check(entryNote == "", "entering replay was refused: %s", entryNote)
	if entryNote != "" {
		close(release)
		mygoRunOnMain(func() { a.beforeSeekCommit = nil })
		return
	}
	time.Sleep(1500 * time.Millisecond)
	step(window, a, dir, scenario, "2-preparing.png", "the stage while the replacements wait behind the live picture", func(a *appState) {
		a.showVideo(0)
	})
	close(release)
	mygoRunOnMain(func() { a.beforeSeekCommit = nil })
	if !waitSeekDone(a, 120*time.Second) {
		logf(scenario, "the entry did not finish within 120s")
		return
	}
	time.Sleep(1500 * time.Millisecond)
	report, ok := a.lastReport()
	logf(scenario, "after the entry the account says: %s", report.Line())
	check(ok && report.Phase == replayCommitted, "the account is %v, want a committed entry", report.Phase)
	check(report.Kind == replayKindEnter, "the account is labelled %q, want an entry", report.Kind)
	for index := 0; index < want; index++ {
		it := a.items[index]
		check(it.replay != nil, "source %d has no recording after the entry", index+1)
		check(it.generation == generations[index]+1,
			"source %d: generation = %d, want %d", index+1, it.generation, generations[index]+1)
	}
	step(window, a, dir, scenario, "3-committed.png", "after the entry committed", func(a *appState) {
		a.showVideo(0)
	})

	// The seek entry has to work straight away: the same account machinery, the
	// same transaction, now from a stage that is already replaying.
	var seekNote string
	mygoRunOnMain(func() {
		a.seekText = "5"
		seekNote = a.requestReplaySeek()
	})
	logf(scenario, "a seek right after the entry said: %s", seekNote)
	if !waitSeekDone(a, 120*time.Second) {
		logf(scenario, "the seek after the entry did not finish within 120s")
		return
	}
	time.Sleep(time.Second)
	report, _ = a.lastReport()
	logf(scenario, "the seek account says: %s", report.Line())
	check(report.Phase == replayCommitted, "the seek after the entry is %v, want committed", report.Phase)
	step(window, a, dir, scenario, "4-seek.png", "a seek immediately after the entry", func(a *appState) {
		a.showVideo(0)
	})

	// A target outside the shared window is refused by the entry itself and
	// changes nothing.
	before := make([]int, want)
	for index := 0; index < want; index++ {
		before[index] = a.items[index].generation
	}
	var refusedErr error
	var started bool
	mygoRunOnMain(func() {
		refusedErr = a.startEnterReplayTxn(a.items, 0, true)
		started = a.seekInFlight() != nil
	})
	refused := ""
	if refusedErr != nil {
		refused = refusedErr.Error()
	}
	logf(scenario, "an explicit entry at 0s said: %s", refused)
	check(refused != "", "an entry at the window edge was not refused")
	check(!started, "a refused entry started a transaction")
	for index := 0; index < want; index++ {
		check(a.items[index].generation == before[index],
			"source %d: a refused entry changed the player", index+1)
	}
	step(window, a, dir, scenario, "5-refused.png", "an entry that the plan refuses", func(a *appState) {
		a.showVideo(0)
	})

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ := a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(openPlayers(a) == 0, "the run ended with %d player(s) open", openPlayers(a))
	logf(scenario, "checks failed: %d", failures)
}

func runFocus(window *mygo.Window, a *appState, dir string) {
	go focusBody(window, a, dir)
}

func focusBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "focus"
	logf(scenario, "the walk is outside this process: testplace\\p18\\focusclient sends Tab, text and key chords")

	deadline := time.Now().Add(70 * time.Second)
	samples := 0
	seenPages := map[string]bool{}
	lastPage := ""
	for {
		time.Sleep(200 * time.Millisecond)
		samples++
		var (
			typing      bool
			seen        int
			typingCount int
			fires       int
			whileTyping int
			closes      int
			page        string
		)
		mygoRunOnMain(func() {
			typing = a.keysTyping
			seen = a.keysSeen
			typingCount = a.typingEdges
			fires = a.shortcutFires
			whileTyping = a.firesWhileTyping
			closes = a.closes
			page = a.workspace.title()
		})
		seenPages[page] = true
		if page != lastPage {
			logf(scenario, "the page under the keyboard is now %s", page)
			lastPage = page
		}
		if samples%5 == 0 {
			logf(scenario, "sample %d: a text field has the focus %v (seen in %d samples of %d), shortcuts fired %d (while typing %d), closes %d",
				samples, typing, typingCount, seen, fires, whileTyping, closes)
		}
		if closes > 0 {
			logf(scenario, "the window closed; stopping the watch")
			break
		}
		if time.Now().After(deadline) {
			logf(scenario, "the deadline passed without a close")
			break
		}
	}

	var typingCount, fires, whileTyping, closes int
	mygoRunOnMain(func() {
		typingCount = a.typingEdges
		fires = a.shortcutFires
		whileTyping = a.firesWhileTyping
		closes = a.closes
	})

	failed := false
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failed = true
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	check(typingCount > 0, "Tab reached a text field: the focus entered one %d time(s)", typingCount)
	check(whileTyping == 0, "no shortcut fired while a text field had the focus (fired %d time(s) overall, %d while typing)", fires, whileTyping)
	check(seenPages["Mixer"], "the keyboard reached the mixer page")
	check(closes <= 1, "the window closed at most once (%d)", closes)
	if failed {
		logf(scenario, "checks failed: 1")
	} else {
		logf(scenario, "checks failed: 0")
	}
	a.release()
	mygo.App.Quit()
}

func runHidden(window *mygo.Window, a *appState, dir string) {
	go func() {
		// 1. The picture opens for the first time while another page is on
		// screen. The stage has not been drawn yet, so this is the case the
		// P16.0 experiment left open: a player opened while hidden.
		mygoRunOnMain(func() {
			a.workspace = workspaceSources
			window.Invalidate()
		})
		logf("hidden", "the Sources page is up before the first frame")
		if !waitForFirstPicture(a, 20*time.Second) {
			logf("hidden", "no first frame while hidden within 20s: %s", allStates(a))
			step(window, a, dir, "hidden", "1-hidden-open.png", "the Sources page after the picture failed to open", nil)
			mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
			return
		}
		opening := positionOf(a, 0)
		step(window, a, dir, "hidden", "1-hidden-open.png", "the Sources page holding the opening picture", nil)

		// 2. It has to keep running while nothing draws it.
		time.Sleep(3 * time.Second)
		hidden := positionOf(a, 0)
		logf("hidden", "three seconds hidden: position %s -> %s, advanced=%v",
			sceneClock(opening), sceneClock(hidden), moved(opening, hidden))

		// 3. Back on the stage the arriving frame is the current one.
		step(window, a, dir, "hidden", "2-stage.png", "the stage after the picture opened hidden", func(a *appState) {
			a.workspace = workspaceStage
		})

		// 4. Import a second local file from the Sources page and let it open
		// while that page is up.
		uri := a.hiddenAddition()
		before := sourceCount(a)
		mygoRunOnMain(func() {
			a.workspace = workspaceSources
			a.report(a.runCommand(command{Kind: commandAdd, URI: uri}))
		})
		waitUntil(25*time.Second, func() bool {
			done := false
			mygoRunOnMain(func() { done = a.importing == "" && len(a.items) > before })
			return done
		})
		added := sourceCount(a)
		index := added - 1
		imported := added > before && waitForReady(a, index, 20*time.Second)
		logf("hidden", "import of %s while hidden: %d source(s), ready=%v", uri, added, imported)
		step(window, a, dir, "hidden", "3-imported.png", "the Sources page after the import", nil)
		if imported {
			first := positionOf(a, index)
			time.Sleep(2 * time.Second)
			second := positionOf(a, index)
			logf("hidden", "imported source hidden for 2s: position %s -> %s, advanced=%v",
				sceneClock(first), sceneClock(second), moved(first, second))
			step(window, a, dir, "hidden", "4-import-stage.png", "the stage showing the imported source", func(a *appState) {
				a.workspace = workspaceStage
				a.report(a.runCommand(command{Kind: commandShow, Source: sourceAt(a, index)}))
			})
		} else {
			logf("hidden", "the imported source did not open while hidden; it is not shown on the stage")
		}

		// 5. Stage -> Settings -> Sources -> Stage: no stop, no reset, and the
		// pages must not be covered by the parked picture.
		walk := make([]time.Duration, 0, 3)
		step(window, a, dir, "hidden", "5-settings.png", "the settings page with the picture parked", func(a *appState) {
			a.workspace = workspaceSettings
		})
		walk = append(walk, positionOf(a, index))
		step(window, a, dir, "hidden", "6-sources.png", "the sources page with the picture parked", func(a *appState) {
			a.workspace = workspaceSources
		})
		walk = append(walk, positionOf(a, index))
		step(window, a, dir, "hidden", "7-final-stage.png", "the stage after the walk", func(a *appState) {
			a.workspace = workspaceStage
		})
		walk = append(walk, positionOf(a, index))
		reset := false
		for i := 1; i < len(walk); i++ {
			if !moved(walk[i-1], walk[i]) {
				reset = true
			}
		}
		logf("hidden", "walk readings %s, %s, %s | stopped or reset=%v",
			sceneClock(walk[0]), sceneClock(walk[1]), sceneClock(walk[2]), reset)

		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

// waitReady polls until the first item has a ready player, so a slow file cannot
// be mistaken for a stalled one.
func waitForFirstPicture(a *appState, limit time.Duration) bool {
	return waitForReady(a, 0, limit)
}

// sceneClock prints a duration without its nanoseconds, which is noise here.
func sceneClock(d time.Duration) string {
	return d.Round(10 * time.Millisecond).String()
}

// positionOf reads one source's position on the drawing thread, where the
// players live.
func positionOf(a *appState, index int) time.Duration {
	var pos time.Duration
	mygoRunOnMain(func() {
		if index >= 0 && index < len(a.items) && a.items[index].player != nil {
			pos = a.items[index].player.Position()
		}
	})
	return pos
}

// sourceAt reads one source's ID on the drawing thread.
func sourceAt(a *appState, index int) core.SourceID {
	var id core.SourceID
	mygoRunOnMain(func() {
		if index >= 0 && index < len(a.items) {
			id = a.items[index].source.ID
		}
	})
	return id
}

// sourceCount reads the number of sources on the drawing thread.
func sourceCount(a *appState) int {
	count := 0
	mygoRunOnMain(func() { count = len(a.items) })
	return count
}

// waitForReady waits for one source's player to produce a frame.
func waitForReady(a *appState, index int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ready := false
		mygoRunOnMain(func() {
			if index >= 0 && index < len(a.items) && a.items[index].player != nil {
				ready = a.items[index].player.Ready()
			}
		})
		if ready {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// waitUntil polls cond until it holds or the limit passes.
func waitUntil(limit time.Duration, cond func() bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// moved says whether the clock advanced enough to be more than sampling noise.
func moved(before, after time.Duration) bool {
	return after > before+500*time.Millisecond
}

// hiddenAddition picks a local file that is not already in the session, so the
// import opens a new player rather than a second handle on the first source.
func (a *appState) hiddenAddition() string {
	var candidates []string
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if isNetworkAddress(it.uri) {
				continue
			}
			dir := filepath.Dir(it.uri)
			for _, name := range []string{"long.mp4", "second.mp4"} {
				candidate := filepath.Join(dir, name)
				if candidate == it.uri {
					continue
				}
				if _, err := os.Stat(candidate); err == nil {
					candidates = append(candidates, candidate)
					return
				}
			}
		}
	})
	if len(candidates) > 0 {
		return candidates[0]
	}
	log.Printf("hidden: no second local file next to the media, importing the first source again")
	return a.lifecycleAddition()
}

func runKeys(window *mygo.Window, a *appState, dir string) {
	go func() {
		// The window and its players may only be released on the drawing thread,
		// and Quit has to come after that: releasing from this goroutine leaves
		// the process alive with the window already gone.
		defer func() {
			mygoRunOnMain(func() {
				a.release()
				mygo.App.Quit()
			})
		}()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			var ready bool
			done := make(chan struct{})
			mygoRunOnMain(func() {
				ready = len(a.items) > 0 && a.items[0].player != nil
				close(done)
			})
			<-done
			if ready {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		mygoRunOnMain(func() {
			if len(a.items) > 0 {
				a.selected = a.items[0].source.ID
			}
			a.workspace = workspaceSources
			log.Printf("keys: selected %q on the Sources page; press Ctrl+S, Ctrl+H, Ctrl+R, Ctrl+1..5", a.selected)
		})
		var lastAt time.Time
		deadline := time.Now().Add(14 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			mygoRunOnMain(func() {
				if a.lastCommand.At.Equal(lastAt) {
					return
				}
				lastAt = a.lastCommand.At
				log.Printf("keys: last command %s", a.lastCommand)
				log.Printf("keys: workspace %d, selected %q", a.workspace, a.selected)
				capture(window, fmt.Sprintf("%s/last.png", dir))
			})
		}
	}()
}

func runLayouts(window *mygo.Window, a *appState, dir string) {
	go layoutsBody(window, a, dir)
}

func layoutsBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "layouts"
	if len(a.items) < 2 {
		layoutsQuit(scenario, "CHECK FAILED: a layout check needs two sources, this run has %d", len(a.items))
		return
	}

	players := map[core.SourceID]*ui.VideoPlayer{}
	heard := map[core.TrackID]bool{}
	var initialScene core.Scene
	var gone core.SourceID
	var goneVideo, goneAudio core.TrackID
	scene := ""
	bad := ""
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandPlay}))
		// Every source with a video takes part, so the grid has four pictures
		// to place and the smaller arrangements have something to leave out.
		for i, it := range a.items {
			if it.player != nil && it.video != "" && !it.shown {
				if err := a.showVideo(i); err != nil {
					bad = err.Error()
				}
			}
		}
		for _, it := range a.items {
			if it.player != nil {
				players[it.source.ID] = rawPlayer(it.player)
			}
		}
		heard = heardTracks(a)
		scene = a.sceneLabel()
		if snapshot, err := a.session.Scene(a.scene); err == nil {
			initialScene = snapshot
		} else {
			bad = fmt.Sprintf("could not snapshot scene video routes: %v", err)
		}
	})
	logf(scenario, "started with %d source(s) as %s; heard %s", len(players), scene, tracksOf(heard))

	positions := map[core.SourceID]time.Duration{}
	lastShot := map[core.SourceID]time.Duration{}
	shot := func(name string) {
		path := filepath.Join(dir, name+".png")
		mygoRunOnMain(func() {
			window.Invalidate()
			_ = capture(window, path)
		})
		logf(scenario, "capture %s: %s", path, layoutReport(a))
		mygoRunOnMain(func() {
			for _, it := range a.items {
				if it.player == nil {
					continue
				}
				pos := it.player.Position()
				if pos > positions[it.source.ID] {
					positions[it.source.ID] = pos
				}
				lastShot[it.source.ID] = pos
			}
		})
	}

	type step struct {
		at   time.Duration
		act  func()
		shot string
	}
	var steps []step
	at := 2 * time.Second
	// The first frames are not there yet for every source; the stage has to
	// show a placeholder for the ones still opening instead of whatever a
	// player with no frame draws.
	steps = append(steps, step{at: 400 * time.Millisecond, act: func() { a.setLayout(layoutGrid) }})
	steps = append(steps, step{at: 900 * time.Millisecond, shot: "at-once"})
	for _, kind := range layoutKinds {
		kind := kind
		steps = append(steps, step{at: at, act: func() { a.setLayout(kind) }})
		steps = append(steps, step{at: at + 1200*time.Millisecond, shot: kind.String()})
		at += 2600 * time.Millisecond
	}
	// The source a person puts first takes the big slot. The order a layout
	// reads is the one a Main click writes, not the scene's own order.
	var second core.SourceID
	mygoRunOnMain(func() {
		if len(a.items) > 1 {
			second = a.items[1].source.ID
		}
	})
	if second != "" {
		steps = append(steps, step{at: at, act: func() { a.setLayout(layoutPip); a.setPrimary(second) }})
		steps = append(steps, step{at: at + 1200*time.Millisecond, shot: "pip-with-second-first"})
		at += 2400 * time.Millisecond
		steps = append(steps, step{at: at, act: func() { a.setLayout(layoutGrid) }})
	}

	// A watching mode keeps the arrangement: the stage is the same stage. Each
	// arrangement is walked through the two modes that leave room to look at
	// it, and the grid also in full screen, where the window itself changes.
	for _, mode := range []viewingMode{viewCinema, viewCompact} {
		mode := mode
		steps = append(steps, step{at: at, act: func() { a.enterViewing(mode, window) }})
		at += 900 * time.Millisecond
		for _, kind := range layoutKinds {
			kind := kind
			steps = append(steps, step{at: at, act: func() { a.setLayout(kind) }})
			steps = append(steps, step{at: at + 1100*time.Millisecond, shot: mode.String() + "-" + kind.String()})
			at += 2300 * time.Millisecond
		}
		steps = append(steps, step{at: at, act: func() { a.leaveViewing(window) }})
		at += 900 * time.Millisecond
	}
	steps = append(steps, step{at: at, act: func() { a.setLayout(layoutGrid); a.enterViewing(viewFullscreen, window) }})
	steps = append(steps, step{at: at + 1300*time.Millisecond, shot: "fullscreen-grid"})
	steps = append(steps, step{at: at + 2500*time.Millisecond, act: func() { a.leaveFullscreen(window) }})
	at += 3400 * time.Millisecond

	// A window that changes size while a four-picture grid is up keeps its
	// pictures: the slots are worked out from the box on every frame.
	steps = append(steps, step{at: at, act: func() { a.setLayout(layoutGrid) }})
	steps = append(steps, step{at: at + 1100*time.Millisecond, shot: "grid-before-resize"})
	steps = append(steps, step{at: at + 1600*time.Millisecond, act: func() {
		window.SetBounds(mygo.Rectangle{X: 120, Y: 90, Width: 820, Height: 620})
	}})
	steps = append(steps, step{at: at + 2800*time.Millisecond, shot: "grid-resized"})
	at += 3600 * time.Millisecond

	// Removing the source a grid slot holds shrinks the grid; nothing is left
	// drawing in the slot it used to occupy.
	if len(a.items) > 0 {
		mygoRunOnMain(func() {
			gone = a.items[len(a.items)-1].source.ID
			goneVideo = a.items[len(a.items)-1].video
			goneAudio = a.items[len(a.items)-1].audio
		})
		steps = append(steps, step{at: at, act: func() {
			window.SetBounds(mygo.Rectangle{X: 60, Y: 60, Width: 1150, Height: 660})
		}})
		steps = append(steps, step{at: at + 900*time.Millisecond, act: func() {
			beforeRemoval, err := a.session.Scene(a.scene)
			if err != nil || beforeRemoval.ID != initialScene.ID ||
				!slices.Equal(beforeRemoval.Video, initialScene.Video) ||
				!slices.Equal(beforeRemoval.Audio, initialScene.Audio) {
				bad = "layout changed the scene or its routes before removal"
			}
			result := a.runCommand(command{Kind: commandRemove, Source: gone})
			a.report(result)
			if result.Err != nil {
				bad = fmt.Sprintf("removal failed: %v", result.Err)
			}
		}})
		steps = append(steps, step{at: at + 2100*time.Millisecond, shot: "grid-after-removal"})
		at += 3000 * time.Millisecond
	}

	started := time.Now()
	deadline := started.Add(at + 4*time.Second)
	next := 0
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		now := time.Since(started)
		if next >= len(steps) || now < steps[next].at {
			continue
		}
		step := steps[next]
		if step.act != nil {
			mygoRunOnMain(func() {
				step.act()
				window.Invalidate()
			})
		}
		if step.shot != "" {
			shot(step.shot)
		}
		next++
	}

	var after map[core.TrackID]bool
	mygoRunOnMain(func() {
		after = heardTracks(a)
		if bad != "" {
			return
		}
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			if players[it.source.ID] != rawPlayer(it.player) {
				bad = fmt.Sprintf("source %s has a new player after the layout changes", it.source.ID)
				return
			}
			// The picture that is not on the stage is still the picture that is
			// playing: a layout may hide it, it may not stop it.
			if positions[it.source.ID] <= 0 {
				bad = fmt.Sprintf("source %s never advanced", it.source.ID)
				return
			}
			if lastShot[it.source.ID] < positions[it.source.ID] {
				bad = fmt.Sprintf("source %s went backwards", it.source.ID)
				return
			}
		}
		delete(heard, goneAudio)
		if !sameTracks(heard, after) {
			bad = fmt.Sprintf("what is heard changed from %s to %s", tracksOf(heard), tracksOf(after))
			return
		}
		currentScene, err := a.session.Scene(a.scene)
		if err != nil {
			bad = fmt.Sprintf("could not read scene video routes after removal: %v", err)
			return
		}
		wantVideo := slices.DeleteFunc(slices.Clone(initialScene.Video), func(track core.TrackID) bool {
			return track == goneVideo
		})
		wantAudio := slices.DeleteFunc(slices.Clone(initialScene.Audio), func(level core.AudioLevel) bool {
			return level.Track == goneAudio
		})
		if gone == "" || a.find(gone) != nil || currentScene.ID != initialScene.ID ||
			!slices.Equal(currentScene.Video, wantVideo) || !slices.Equal(currentScene.Audio, wantAudio) {
			bad = fmt.Sprintf("removing %s did not preserve the other ordered routes", gone)
		}
	})
	if bad != "" {
		logf(scenario, "CHECK FAILED: %s", bad)
	} else {
		logf(scenario, "CHECK PASSED: five layouts preserve players, scene identity, audio routes and retained video routes; removal drops its target route; pictures advanced")
	}
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

// layoutReport says where the stage put each picture right now, which is what a
// capture has to agree with.
func layoutReport(a *appState) string {
	pictures := a.stagePictures()
	slots := layoutSlots(a.layout, len(pictures))
	line := fmt.Sprintf("layout=%s shown=%d placed=%d note=%q event=%s audio=%s scene=%s",
		a.layout, len(pictures), len(slots), a.layoutNote(),
		a.sync.playhead.Event().Round(10*time.Millisecond), tracksOf(heardTracks(a)), sceneAudio(a))
	for i, it := range pictures {
		line += fmt.Sprintf(" | %s rect=%.0f,%.0f %.0fx%.0f", it.source.ID,
			slots[i].left, slots[i].top, slots[i].width, slots[i].height)
	}
	for _, it := range a.items {
		if it.player == nil || it.shown {
			continue
		}
		line += fmt.Sprintf(" | %s off-stage", it.source.ID)
	}
	return line
}

// heardTracks is the set of audio tracks the scene is actually playing.
func heardTracks(a *appState) map[core.TrackID]bool {
	out := map[core.TrackID]bool{}
	for _, it := range a.items {
		if it.audio != "" && it.player != nil && !it.player.Muted() {
			out[it.audio] = true
		}
	}
	return out
}

func sameTracks(a, b map[core.TrackID]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func tracksOf(set map[core.TrackID]bool) string {
	if len(set) == 0 {
		return "nothing"
	}
	out := ""
	for id := range set {
		if out != "" {
			out += ","
		}
		out += string(id)
	}
	return out
}

func layoutsQuit(scenario, format string, args ...any) {
	logf(scenario, format, args...)
	mygoRunOnMain(func() {
		mygo.App.Quit()
	})
}

// sceneAudio writes the routing the scene holds — track and level — which is
// what a layout must leave alone. It is not what the speakers do: a source can
// be silent for reasons of its own (not aligned yet, muted, no player).
func sceneAudio(a *appState) string {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	if len(scene.Audio) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(scene.Audio))
	for _, level := range scene.Audio {
		parts = append(parts, fmt.Sprintf("%s@%.2f", level.Track, level.Volume))
	}
	return strings.Join(parts, ",")
}

func runNarrow(window *mygo.Window, a *appState, dir string) {
	scenario := "narrow"
	go func() {
		time.Sleep(2500 * time.Millisecond)
		mygoRunOnMain(func() {
			window.SetSize(640, 560)
			a.workspace = workspaceSources
			window.Invalidate()
		})
		time.Sleep(700 * time.Millisecond)
		step(window, a, dir, scenario, "1-narrow-sources.png", "the sources page at 640x560", func(_ *appState) {
			logf(scenario, "%s", a.layoutLine(a.frameWidth, a.frameHeight))
		})

		// A read that cannot finish: the address is unroutable, so the probe
		// sits in the connect until the import's own timeout or Cancel.
		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandAdd, URI: "http://10.255.255.1/never.m3u8"}))
		})
		time.Sleep(1500 * time.Millisecond)
		step(window, a, dir, scenario, "2-reading.png", "Reading… and Cancel while the read is in flight", func(_ *appState) {
			logf(scenario, "in flight: %q, last command %q", a.importing, a.lastCommand.Message)
		})

		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandCancelImport}))
		})
		time.Sleep(700 * time.Millisecond)
		step(window, a, dir, scenario, "3-cancelled.png", "after Cancel: nothing added, the row is idle again", func(_ *appState) {
			logf(scenario, "after cancel: importing=%q, last command %q", a.importing, a.lastCommand.Message)
		})

		// A read that fails at once: connection refused, so the failure reaches
		// the status bar and the global error button together.
		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandAdd, URI: "http://127.0.0.1:9/not-there.m3u8"}))
		})
		time.Sleep(1500 * time.Millisecond)
		step(window, a, dir, scenario, "4-error.png", "a refused import: error button, status bar and Dismiss", func(_ *appState) {
			logf(scenario, "after failure: lastErr=%v, sources=%d", a.lastErr, len(a.items))
		})

		mygoRunOnMain(func() {
			a.lastErr = nil
			window.Invalidate()
		})
		time.Sleep(500 * time.Millisecond)
		step(window, a, dir, scenario, "5-dismissed.png", "after Dismiss: the status bar keeps the record, the button is gone", func(_ *appState) {
			logf(scenario, "after dismiss: lastErr=%v, last command %q", a.lastErr, a.lastCommand.Message)
		})
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

func runPages(window *mygo.Window, a *appState, dir string) {
	go pagesBody(window, a, dir)
}

func pagesBody(window *mygo.Window, a *appState, dir string) {
	scenario := "pages"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	// 641x560 pixels at 100% is the smallest content the workbench supports
	// (626 DIPs wide), which is exactly where the rail folds.
	step(window, a, dir, scenario, "", "the smallest window", func(a *appState) {
		a.zoom = 1
		window.SetSize(641, 560)
	})
	var folded bool
	mygoRunOnMain(func() {
		folded = !a.railOpen(a.lastWidth)
		a.workspace = workspaceStage
		window.Invalidate()
	})
	check(folded, "the rail folds at the smallest window, which is what this walk needs")
	logf(scenario, "the rail is folded; the bar's page button is the visible way between pages")

	// The runner uses the visible navigation. Sources opens a dock tab on the
	// stage, so it does not count as a separate workspace visit here.
	visited := []workspace{}
	seen := map[workspace]bool{}
	last := workspace(-1)
	returned := false
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		var current workspace
		mygoRunOnMain(func() { current = a.workspace })
		if current != last {
			visited = append(visited, current)
			seen[current] = true
			last = current
			logf(scenario, "page is now %s", current.title())
		}
		returned = current == workspaceStage && seen[workspaceSettings] &&
			seen[workspaceDiagnostics] && seen[workspaceMixer]
		if returned {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	step(window, a, dir, scenario, "pages.png", "after navigating by mouse", nil)

	if len(visited) > 0 {
		check(visited[0] == workspaceStage, "the walk started on Stage (%s)", visited[0].title())
	}
	for _, page := range []workspace{workspaceSettings, workspaceDiagnostics, workspaceMixer} {
		check(seen[page], "the page menu reached %s", page.title())
	}
	check(returned, "the walk returned to Stage after visiting all three workspaces")
	logf(scenario, "the page button walked: %s", pagePath(visited))

	step(window, a, dir, scenario, "", "back to a normal window", func(a *appState) {
		window.SetSize(1426, 862)
	})
	logf(scenario, "checks failed: %d", failures)
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

func pagePath(pages []workspace) string {
	out := ""
	for index, page := range pages {
		if index > 0 {
			out += " -> "
		}
		out += page.title()
	}
	return out
}

// pageSummary is the one-line state the log keeps beside the walk, so a failure
// says what the bar was told it was showing.
func (a *appState) pageSummary() string {
	return fmt.Sprintf("%s at %.1f DIPs wide", a.workspace.title(), a.lastWidth)
}

func runRealDPI(window *mygo.Window, a *appState, dir string) {
	go realDPIBody(window, a, dir)
}

func realDPIBody(window *mygo.Window, a *appState, dir string) {
	scenario := "realdpi"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	defer func() {
		if err := captureErr(window, dir+"\\realdpi.png"); err != nil {
			logf(scenario, "the screenshot could not be taken: %v", err)
		}
		logf(scenario, "done: %d check(s) failed", failures)
		mygoRunOnMain(func() { a.release() })
		mygo.App.Quit()
	}()
	time.Sleep(1200 * time.Millisecond)
	// A window that has not drawn its first frame is still hidden, and a
	// hidden window has no size worth measuring: show it and wait until the
	// bounds it reports are the ones a person would see.
	window.Show()
	visible := func() bool {
		var b mygo.Rectangle
		mygoRunOnMain(func() { b = window.Bounds() })
		return b.Width > 400 && b.Height > 300
	}
	for waited := time.Duration(0); waited < 20*time.Second && !visible(); waited += 250 * time.Millisecond {
		time.Sleep(250 * time.Millisecond)
	}
	check(visible(), "the window is on screen with a size before it is measured")

	displays := mygo.Screen.Displays()
	var home mygo.Display
	for _, d := range displays {
		logf(scenario, "display %q bounds %dx%d at %d,%d, work area %dx%d at %d,%d, scale %.2f, primary %v, internal %v",
			d.Label, d.Bounds.Width, d.Bounds.Height, d.Bounds.X, d.Bounds.Y,
			d.WorkArea.Width, d.WorkArea.Height, d.WorkArea.X, d.WorkArea.Y,
			d.ScaleFactor, d.Primary, d.Internal)
		if d.Primary {
			home = d
		}
	}
	if home.Bounds.Width == 0 && len(displays) > 0 {
		home = displays[0]
	}
	if len(displays) == 0 {
		logf(scenario, "no display was reported; nothing to measure")
		return
	}
	zoom := float64(home.ScaleFactor)
	if zoom <= 0 {
		zoom = 1
	}
	check(zoom > 0, "the system reports a scale factor: %.2f", zoom)

	var bounds mygo.Rectangle
	mygoRunOnMain(func() { bounds = window.Bounds() })
	logf(scenario, "the window reports %dx%d at %d,%d before it is placed", bounds.Width, bounds.Height, bounds.X, bounds.Y)

	// The smallest content the workbench draws is a fixed number of DIPs; the
	// frame around it is the title bar and border, which do not grow with the
	// scale, so they are added unmultiplied. They can only be measured from the
	// start-up size the window was actually opened at, which a run may have
	// overridden with -frame; when it does not match, the 15x38 measured at 100%
	// in the P18.1 windows run is used and the log says so rather than calling
	// the estimate a measurement.
	frameWidth, frameHeight, measured := 15, 38, false
	if bounds.Width > 0 && bounds.Height > 0 {
		fw := bounds.Width - int(1180*zoom)
		fh := bounds.Height - int(float64(700+70*len(a.items))*zoom)
		if fw >= 0 && fw <= 60 && fh >= 0 && fh <= 120 {
			frameWidth, frameHeight, measured = fw, fh, true
		}
	}
	if measured {
		logf(scenario, "the window frame measures %dx%d pixels at scale %.2f", frameWidth, frameHeight, zoom)
	} else {
		logf(scenario, "the window did not start at the size its frame would be measured from; using the 15x38 estimate measured at 100%% (scale %.2f)", zoom)
	}

	minWidth := int(minContentWidth*zoom) + frameWidth
	minHeight := int(minContentHeight*zoom) + frameHeight
	frame := centeredFrame(mygo.Rectangle{Width: minWidth, Height: minHeight}, home.WorkArea)
	mygoRunOnMain(func() { window.SetBounds(frame) })
	time.Sleep(700 * time.Millisecond)
	mygoRunOnMain(func() { bounds = window.Bounds() })
	logf(scenario, "asked for the smallest window at scale %.2f: %dx%d at %d,%d; it reports %dx%d at %d,%d",
		zoom, frame.Width, frame.Height, frame.X, frame.Y, bounds.Width, bounds.Height, bounds.X, bounds.Y)

	contentWidth := (float64(bounds.Width) - float64(frameWidth)) / zoom
	contentHeight := (float64(bounds.Height) - float64(frameHeight)) / zoom
	check(inside(bounds, home.WorkArea),
		"the smallest window is wholly inside the work area: %dx%d at %d,%d within %dx%d at %d,%d",
		bounds.Width, bounds.Height, bounds.X, bounds.Y,
		home.WorkArea.Width, home.WorkArea.Height, home.WorkArea.X, home.WorkArea.Y)
	check(contentWidth >= minContentWidth-6 && contentHeight >= minContentHeight-6,
		"the smallest window keeps %.1fx%.1f DIPs of content", contentWidth, contentHeight)

	// Stand still and record every change the external clicks make.
	type state struct {
		page          string
		rail          bool
		inspector     bool
		dock          bool
		railMode      string
		inspectorMode string
	}
	current := func() state {
		return state{
			page:          a.workspace.title(),
			rail:          a.railOpen(a.lastWidth),
			inspector:     a.inspectorOpen(a.lastWidth),
			dock:          a.dockOpen(a.lastWidth, a.lastHeight),
			railMode:      paneModeName(a.rail),
			inspectorMode: paneModeName(a.inspector),
		}
	}
	first := state{}
	mygoRunOnMain(func() { first = current() })
	logf(scenario, "before the clicks: page %s, rail %v (%s), inspector %v (%s), dock %v",
		first.page, first.rail, first.railMode, first.inspector, first.inspectorMode, first.dock)

	pageMoves, paneMoves := 0, 0
	last := first
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		now := state{}
		mygoRunOnMain(func() { now = current() })
		if now.page != last.page {
			pageMoves++
			logf(scenario, "the page became %s", now.page)
		}
		if now.inspector != last.inspector {
			paneMoves++
			logf(scenario, "the inspector became %v", now.inspector)
		}
		last = now
		if pageMoves > 0 && paneMoves > 0 {
			logf(scenario, "both kinds of click landed; stopping the watch early")
			break
		}
	}

	check(pageMoves > 0, "a click on a page button changed the page (%d change(s))", pageMoves)
	check(paneMoves > 0, "a click on the inspector button toggled the inspector (%d change(s))", paneMoves)
}

// captureErr takes the screenshot without the log line capture writes.
func captureErr(window *mygo.Window, path string) error {
	var err error
	mygoRunOnMain(func() { err = capture(window, path) })
	return err
}

// parseFrame reads "x,y,width,height" as a window frame. It is a check's
// starting point, not a setting: nothing writes it back to the file.
func parseFrame(s string) (mygo.Rectangle, bool) {
	var x, y, w, h int
	if _, err := fmt.Sscanf(s, "%d,%d,%d,%d", &x, &y, &w, &h); err != nil {
		return mygo.Rectangle{}, false
	}
	if w < 320 || h < 240 {
		return mygo.Rectangle{}, false
	}
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}, true
}

func runSettings(window *mygo.Window, a *appState, dir string) {
	go func() {
		step(window, a, dir, "settings", "1-general.png",
			"the Settings page opens on General; the file has not been written yet: "+a.settingsBoundary(),
			func(a *appState) {
				a.workspace = workspaceSettings
				a.settingsSection = "General"
			})

		step(window, a, dir, "settings", "2-draft.png",
			"a draft changes six values; nothing is applied until Apply",
			func(a *appState) {
				a.settingsSection = "Playback"
				a.settingsDraft.DefaultVolume = 0.3
				a.settingsDraft.NudgeStep = 0.5
				a.settingsDraft.MasterVolume = 0.5
				a.settingsDraft.MuteUnaligned = false
				a.settingsDraft.StartPage = "sources"
				a.settingsDraft.DockOpen = false
			})

		step(window, a, dir, "settings", "3-applied.png",
			"Apply validates, pushes into the players and writes the file",
			func(a *appState) {
				a.applySettingsCommand()
			})
		mygoRunOnMain(func() {
			data, err := os.ReadFile(a.settingsPath)
			logf("settings", "live nudge step %s, volume(0.5)=%.3f, saved=%v, file=%s, read back=%v (%v)",
				a.settingsNudge(-1), a.volumeFor(0.5), a.settingsSaved.Load(), a.settingsPath, oneLine(data), err)
		})

		step(window, a, dir, "settings", "4-cancelled.png",
			"Cancel after a dirty draft leaves the live settings alone",
			func(a *appState) {
				a.settingsDraft.NudgeStep = 0.9
				a.settingsDraft.DefaultVolume = 0.8
			})
		mygoRunOnMain(func() {
			a.settingsDraft = a.settings
			a.settingsNote = "cancelled: the draft is back to what is applied"
			logf("settings", "after Cancel the live nudge step is still %s", a.settingsNudge(-1))
		})

		step(window, a, dir, "settings", "5-reset-draft.png",
			"Reset puts the defaults in the draft only; Apply would write them",
			func(a *appState) {
				a.settingsDraft = defaultSettings()
				a.settingsSection = "Shortcuts"
			})
		mygoRunOnMain(func() {
			logf("settings", "after Reset the live start page is %q and the draft start page is %q",
				a.settings.StartPage, a.settingsDraft.StartPage)
		})

		step(window, a, dir, "settings", "6-dvr.png",
			"the DVR section shows the budget in force, the rule it came from, and a warning when a limit is zero",
			func(a *appState) {
				a.settingsSection = "DVR"
				a.settingsDraft = a.settings
			})
		mygoRunOnMain(func() {
			logf("settings", "the budget in force is %s from %s; the draft asks for %s",
				a.budget.line(), a.budgetFrom, a.settingsDraft.DVR.line())
			if a.budgetFrom == "" {
				logf("settings", "CHECK FAILED: the page cannot say where the budget came from")
			}
		})

		step(window, a, dir, "settings", "7-diagnostics.png",
			"Diagnostics shows the real event count and the settings path",
			func(a *appState) {
				a.settingsSection = "Diagnostics"
			})

		step(window, a, dir, "settings", "8-stage-resumed.png",
			"back on the stage, the players that stood still on the settings page should advance again",
			func(a *appState) { a.workspace = workspaceStage })

		mygoRunOnMain(func() {
			time.Sleep(3 * time.Second)
		})

		mygoRunOnMain(func() {
			data, err := os.ReadFile(a.settingsPath)
			logf("settings", "after returning to the stage: %s", allStates(a))
			logf("settings", "file after the run: %s (%v)", oneLine(data), err)
			logf("settings", "done: workspace=%v, start page applied next boot=%q", a.workspace, a.settings.StartPage)
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSettingsRestart is the second process of the settings check: it starts
// with the file the first process wrote and reports what came back. A start
// page is only believable from a start, so this is a separate run.
func runSettingsRestart(window *mygo.Window, a *appState, dir string) {
	go func() {
		step(window, a, dir, "settings-restart", "1-restored.png",
			"a fresh process read the applied file: the start page, the step and the master volume came back",
			func(a *appState) {
				a.settingsSection = "Playback"
			})
		mygoRunOnMain(func() {
			logf("settings-restart", "loaded start page %q, workspace %v, nudge step %s, default volume %.2f, master %.2f, mute unaligned %v, dock open %v, saved %v",
				a.settings.StartPage, a.workspace, a.settingsNudge(-1), a.settings.DefaultVolume,
				a.settings.MasterVolume, a.settings.MuteUnaligned, a.settings.DockOpen, a.settingsSaved.Load())
			logf("settings-restart", "the dock is %s and the window frame is %.0fx%.0f",
				boolString(a.dock.shown(a.settings.DockOpen && a.frameHeight >= dockAutoHeight)),
				a.frameWidth, a.frameHeight)
			a.release()
			mygo.App.Quit()
		})
	}()
}

func runSizes(window *mygo.Window, a *appState, dir string) {
	sizesWalk(window, a, dir, "sizes", false)
}

// runSizesEmpty is the same walk after removing every source, which is the only
// way to reach the empty session: the app refuses to start without -media, so a
// zero-source window has to be emptied rather than launched.
func runSizesEmpty(window *mygo.Window, a *appState, dir string) {
	sizesWalk(window, a, dir, "sizes-empty", true)
}

func sizesWalk(window *mygo.Window, a *appState, dir, scenario string, empty bool) {
	sizes := []struct {
		name   string
		width  int
		height int
	}{
		{"wide", 1440, 900},
		{"medium", 1166, 802},
		{"small", 900, 600},
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		if empty {
			mygoRunOnMain(func() {
				for len(a.items) > 0 {
					if err := a.remove(0); err != nil {
						logf(scenario, "remove the last sources: %v", err)
						break
					}
				}
				window.Invalidate()
			})
			time.Sleep(600 * time.Millisecond)
			logf(scenario, "emptied the session: %s", a.sessionState())
		}
		for _, size := range sizes {
			mygoRunOnMain(func() {
				window.SetSize(size.width, size.height)
				a.workspace = workspaceStage
				window.Invalidate()
			})
			time.Sleep(700 * time.Millisecond)
			step(window, a, dir, scenario, fmt.Sprintf("stage-%s.png", size.name),
				fmt.Sprintf("the stage at %s with %d source(s)", size.name, len(a.items)),
				func(a *appState) {
					logf(scenario, "%s | %s", size.name, a.layoutLine(a.frameWidth, a.frameHeight))
				})
			mygoRunOnMain(func() {
				a.workspace = workspaceSources
				window.Invalidate()
			})
			time.Sleep(500 * time.Millisecond)
			step(window, a, dir, scenario, fmt.Sprintf("sources-%s.png", size.name),
				fmt.Sprintf("the sources page at %s with %d source(s)", size.name, len(a.items)), nil)
			if len(a.items) > 0 {
				step(window, a, dir, scenario, fmt.Sprintf("inspector-%s.png", size.name),
					fmt.Sprintf("the first source selected at %s", size.name),
					func(a *appState) {
						a.selected = a.items[0].source.ID
					})
			}
		}
		mygoRunOnMain(func() {
			window.SetSize(1166, 802)
			a.workspace = workspaceStage
			a.selected = ""
			window.Invalidate()
		})
		time.Sleep(600 * time.Millisecond)
		step(window, a, dir, scenario, "restored.png", "the stage at the default size again", func(a *appState) {
			logf(scenario, "restored: %s | %s", a.layoutLine(a.frameWidth, a.frameHeight), a.sessionState())
		})
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

func runTimeline(window *mygo.Window, a *appState, dir string) {
	go timelineBody(window, a, dir)
}

func timelineBody(window *mygo.Window, a *appState, dir string) {
	checks := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			checks++
			logf("timeline", "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < 2 {
		logf("timeline", "this check needs two sources")
		finishScenario(a)
		return
	}
	if !waitPlayers(a, 30*time.Second) {
		logf("timeline", "the players never came up")
		finishScenario(a)
		return
	}

	step(window, a, dir, "timeline", "1-unaligned.png", "before anybody aligns anything", func(a *appState) {
		lines := a.timelineLines()
		for _, line := range lines {
			logf("timeline", "before aligning: %s", line)
			check(strings.Contains(line, "not aligned"), "an unaligned source does not read as unknown: %q", line)
			check(!strings.Contains(line, "offset"), "an unaligned source claims an offset: %q", line)
		}
		check(len(lines) == 2, "want two rows, got %d", len(lines))
	})

	// A person anchors: that is the real command path, not a view-only edit. It
	// runs on the drawing thread because it seeks players and changes the
	// session the window reads.
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf("timeline", "anchor: %v", anchorErr)
		checks++
	}
	mygoRunOnMain(func() {
		if _, err := a.nudge(1, 0); err != nil {
			logf("timeline", "the nudge on the second source: %v", err)
		}
	})

	step(window, a, dir, "timeline", "2-aligned.png", "after anchoring the first source", func(a *appState) {
		lines := a.timelineLines()
		joined := strings.Join(lines, "\n")
		for _, line := range lines {
			logf("timeline", "after anchoring: %s", line)
		}
		check(strings.Contains(joined, "offset +0.0s"), "the anchored source does not show its real zero offset: %q", joined)
		check(strings.Contains(joined, "manual"), "the anchored source does not say where its offset came from: %q", joined)
		check(strings.Contains(joined, "ref"), "the anchored reference track is not marked: %q", joined)
		check(strings.Contains(joined, "media "), "the view does not show the mapped media time: %q", joined)
		check(strings.Contains(joined, "player "), "the view does not show what the player reports: %q", joined)
	})

	// A nudge moves one source by a real, non-zero amount: the row must show the
	// offset it was given and the media time the timeline maps the event to,
	// while the other source stays where it was.
	var nudgeErr error
	mygoRunOnMain(func() { _, nudgeErr = a.nudge(1, -500*time.Millisecond) })
	if nudgeErr != nil {
		logf("timeline", "the nudge on the second source: %v", nudgeErr)
		checks++
	}

	step(window, a, dir, "timeline", "3-nudged.png", "after nudging the second source by -500ms", func(a *appState) {
		lines := a.timelineLines()
		joined := strings.Join(lines, "\n")
		for _, line := range lines {
			logf("timeline", "after nudging: %s", line)
		}
		check(strings.Contains(joined, "offset -0.5s"), "the nudged source does not show the offset it was given: %q", joined)
		check(strings.Contains(joined, "offset +0.0s"), "nudging one source moved the other: %q", joined)
		event := a.sync.playhead.Event()
		want := sceneClock(event - 500*time.Millisecond)
		check(strings.Contains(joined, "media "+want), "the nudged source does not map the event to %s: %q", want, joined)
	})

	// Drawing is read-only: a second read must produce the same rows and leave
	// the timeline and playhead exactly where the person left them.
	before := a.sync.timeline.Shifts()
	eventBefore := a.sync.playhead.Event()
	first := strings.Join(a.timelineLines(), "\n")
	second := strings.Join(a.timelineLines(), "\n")
	after := a.sync.timeline.Shifts()
	check(first == second, "the view changed between two reads:\n%s\n---\n%s", first, second)
	check(len(before) == len(after), "the view changed the number of shifts: %d -> %d", len(before), len(after))
	check(eventBefore == a.sync.playhead.Event(), "the view moved the playhead: %s -> %s", eventBefore, a.sync.playhead.Event())

	logf("timeline", "timeline: %d row(s), checks failed: %d", len(a.items), checks)
	a.release()
	mygo.App.Quit()
}

func runViewing(window *mygo.Window, a *appState, dir string) {
	go viewingBody(window, a, dir)
}

func viewingBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "viewing"
	if len(a.items) == 0 {
		logf(scenario, "CHECK FAILED: no source")
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}

	players := map[core.SourceID]*ui.VideoPlayer{}
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandPlay}))
		for _, it := range a.items {
			if it.player != nil {
				players[it.source.ID] = rawPlayer(it.player)
			}
		}
	})
	logf(scenario, "started with %d source(s) in the scene as %s", len(players), a.sceneLabel())

	shot := func(name string) {
		path := filepath.Join(dir, name+".png")
		mygoRunOnMain(func() {
			window.Invalidate()
			_ = capture(window, path)
		})
		logf(scenario, "capture %s: %s", path, viewingReport(a, players))
	}

	type step struct {
		at    time.Duration
		label string
		act   func()
		shot  string
	}
	steps := []step{
		{2 * time.Second, "workbench", nil, "1-workbench"},
		{4 * time.Second, "enter cinema", func() { a.enterViewing(viewCinema, window) }, ""},
		{6 * time.Second, "cinema", nil, "2-cinema"},
		{8 * time.Second, "enter full screen", func() { a.enterViewing(viewFullscreen, window) }, ""},
		{10 * time.Second, "full screen", nil, "3-fullscreen"},
		{12 * time.Second, "out of the full screen, back in cinema", func() { a.leaveFullscreen(window) }, ""},
		{14 * time.Second, "cinema again", nil, "4-cinema-again"},
		{16 * time.Second, "leave to workbench", func() { a.leaveViewing(window) }, ""},
		{18 * time.Second, "back at the workbench", nil, "5-back-workbench"},
		{20 * time.Second, "enter compact", func() { a.enterViewing(viewCompact, window) }, ""},
		{22 * time.Second, "compact", nil, "6-compact"},
		{24 * time.Second, "compact on top", func() {
			a.toggleOnTop(window)
			if a.window != nil {
				a.window.Invalidate()
			}
		}, "7-compact-on-top"},
		{27 * time.Second, "leave to workbench again", func() { a.leaveViewing(window) }, ""},
		{29 * time.Second, "back at the workbench again", nil, "8-back-again"},
		{33 * time.Second, "final", nil, "9-final"},
	}

	started := time.Now()
	deadline := started.Add(36 * time.Second)
	next := 0
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		now := time.Since(started)
		if next >= len(steps) || now < steps[next].at {
			continue
		}
		step := steps[next]
		if step.act != nil {
			mygoRunOnMain(func() {
				step.act()
				window.Invalidate()
			})
		}
		if step.shot != "" {
			// The overlay hides itself after 2.5s without a pointer move, and
			// this run never moves the real pointer. Stopping the timer here is
			// what keeps a capture from depending on whether the picture
			// happened to keep the window repainting.
			mygoRunOnMain(func() {
				a.controlsShown = true
				a.idleTick++
			})
			time.Sleep(150 * time.Millisecond)
			shot(step.shot)
		} else {
			logf(scenario, "step %q at %s: %s", step.label, now.Truncate(time.Second), viewingReport(a, players))
		}
		next++
	}

	// The mode is part of the window, not of the player: after all of that, the
	// same players must still be the ones drawing, and their positions must
	// have moved.
	bad := ""
	mygoRunOnMain(func() {
		if a.viewing != viewWorkbench {
			bad = fmt.Sprintf("ended in %s, want the workbench", a.viewing)
			return
		}
		for _, it := range a.items {
			if it.player == nil {
				continue
			}
			if players[it.source.ID] != rawPlayer(it.player) {
				bad = fmt.Sprintf("source %s has a new player after the mode changes", it.source.ID)
				return
			}
			if it.player.Position() <= 0 {
				bad = fmt.Sprintf("source %s did not advance", it.source.ID)
				return
			}
		}
	})
	if bad != "" {
		logf(scenario, "CHECK FAILED: %s", bad)
	} else {
		logf(scenario, "CHECK PASSED: four modes, same players, pictures advanced")
	}
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
}

// viewingReport says what the window and the players are doing right now, so a
// capture can be read against the numbers that produced it.
func viewingReport(a *appState, players map[core.SourceID]*ui.VideoPlayer) string {
	line := fmt.Sprintf("mode=%s chrome=%v", a.viewing, a.viewing.editing())
	if a.window != nil {
		b := a.window.Bounds()
		line += fmt.Sprintf(" bounds=%dx%d@%d,%d full=%v max=%v onTop=%v",
			b.Width, b.Height, b.X, b.Y, a.window.IsFullScreen(), a.window.IsMaximized(), a.onTop)
	}
	for _, it := range a.items {
		if it.player == nil {
			line += fmt.Sprintf(" | %s no player", it.source.ID)
			continue
		}
		same := players[it.source.ID] == rawPlayer(it.player)
		line += fmt.Sprintf(" | %s shown=%v pos=%s same-player=%v muted=%v",
			it.source.ID, it.shown, it.player.Position().Truncate(time.Millisecond), same, it.player.Muted())
	}
	return line
}

func runWebView(window *mygo.Window, a *appState, dir string) {
	scenario := "webview"
	_ = window
	// Scenarios start on the UI goroutine: sleeping there stops the window from
	// drawing, so the whole check moves to its own goroutine.
	go func() {
		// The native players need a moment; the page loads in parallel.
		time.Sleep(3 * time.Second)
		logf(scenario, "native snapshot before the page acts: %s", snapshotLine(a.snapshot()))

		// The page clicks the second source's Show button 2.5 seconds after load;
		// by now the command should have gone through the boundary.
		waitForCommand(scenario, a, commandShow, 10*time.Second)
		logf(scenario, "native snapshot after the page acted: %s", snapshotLine(a.snapshot()))

		// Push a fresh view to the page and give it a moment to render.
		mygo.RunOnMain(a.broadcastPanel)
		time.Sleep(800 * time.Millisecond)
		step(window, a, dir, scenario, "1-both-interfaces.png",
			"the native stage and the page below it read one session", nil)

		logf(scenario, "last command: %s", a.lastCommand.String())
		mygo.RunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

// waitForCommand waits until the session records a command of the given kind.
func waitForCommand(scenario string, a *appState, kind commandKind, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var last commandResult
		mygo.RunOnMain(func() { last = a.lastCommand })
		if last.Command.Kind == kind && last.Err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logf(scenario, "the page never sent %s through the boundary", kind)
}

var windowsScales = []struct {
	name string
	zoom float32
}{
	{"100", 1},
	{"150", 1.5},
	{"200", 2},
}

// minContentWidth and minContentHeight are the smallest content size the
// workbench still draws every core control at (P15.5). The window pixels needed
// for it grow with the scale.
const (
	minContentWidth  = 626
	minContentHeight = 522
)

func runWindows(window *mygo.Window, a *appState, dir string) {
	go windowsBody(window, a, dir)
}

func windowsBody(window *mygo.Window, a *appState, dir string) {
	scenario := "windows"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	displays := mygo.Screen.Displays()
	for index, display := range displays {
		logf(scenario, "display %d %q: bounds %dx%d at %d,%d · work area %dx%d at %d,%d · scale %.2f · primary %v · internal %v",
			index, display.Label, display.Bounds.Width, display.Bounds.Height, display.Bounds.X, display.Bounds.Y,
			display.WorkArea.Width, display.WorkArea.Height, display.WorkArea.X, display.WorkArea.Y,
			display.ScaleFactor, display.Primary, display.Internal)
	}
	check(len(displays) > 0, "at least one display was reported")

	original := window.Bounds()
	home := displayHolding(original, displays)
	logf(scenario, "the window starts at %dx%d at %d,%d, on the %q display (scale %.2f, work area %dx%d)",
		original.Width, original.Height, original.X, original.Y, home.Label, home.ScaleFactor,
		home.WorkArea.Width, home.WorkArea.Height)
	// The walk places the window itself before it measures anything: a window
	// inherited from the person's last session can sit partly off-screen, and
	// "the window is inside the work area" has to be a claim about a window
	// this check put there.
	centered := centeredFrame(original, home.WorkArea)
	if !inside(original, home.WorkArea) {
		logf(scenario, "the window started at %d,%d %dx%d, which reaches outside the work area; moved it inside first",
			original.X, original.Y, original.Width, original.Height)
		step(window, a, dir, scenario, "", "moved the window inside the work area", func(*appState) {
			window.SetBounds(centered)
		})
	}
	moved := window.Bounds()
	check(inside(moved, home.WorkArea), "the whole window is inside the work area: %dx%d at %d,%d in %dx%d at %d,%d",
		moved.Width, moved.Height, moved.X, moved.Y,
		home.WorkArea.Width, home.WorkArea.Height, home.WorkArea.X, home.WorkArea.Y)

	// SetSize sizes the frame, not the content. Measure the frame once, at
	// 100%, so every scale can be compared with the content it should get
	// instead of with the pixels asked for.
	step(window, a, dir, scenario, "", "measure the frame", func(a *appState) {
		a.zoom = 1
		window.SetSize(900, 700)
	})
	var frameWidth, frameHeight int
	mygoRunOnMain(func() {
		frameWidth = 900 - int(a.lastWidth)
		frameHeight = 700 - int(a.lastHeight)
	})
	logf(scenario, "the frame takes %dx%d pixels at 100%%: 900x700 pixels leave %.1fx%.1f DIPs",
		frameWidth, frameHeight, a.lastWidth, a.lastHeight)
	check(frameWidth >= 0 && frameWidth < 120 && frameHeight >= 0 && frameHeight < 120,
		"the frame is a plausible %dx%d pixels", frameWidth, frameHeight)

	// The scale walk. "fit" is the widest window this screen allows at the
	// scale; "min" is the smallest content the workbench promises to support.
	for _, scale := range windowsScales {
		fitWidth := int(float32(home.WorkArea.Width)) - 80
		fitHeight := int(float32(home.WorkArea.Height)) - 80
		fitWidth = minInt(fitWidth, int(1426*scale.zoom))
		fitHeight = minInt(fitHeight, int(862*scale.zoom))
		sizes := []struct {
			name string
			w, h int
		}{
			{"fit", fitWidth, fitHeight},
			{"min", int(minContentWidth*scale.zoom) + frameWidth, int(minContentHeight*scale.zoom) + frameHeight},
		}
		for _, size := range sizes {
			name := fmt.Sprintf("scale-%s-%s.png", scale.name, size.name)
			caption := fmt.Sprintf("scale %s%% in a %dx%d window", scale.name, size.w, size.h)
			zoom := scale.zoom
			// One call sets the size and the place: a window this check sizes
			// must also be a window this check puts inside the work area,
			// otherwise "it stayed inside" proves nothing.
			frame := centeredFrame(mygo.Rectangle{Width: size.w, Height: size.h}, home.WorkArea)
			step(window, a, dir, scenario, name, caption, func(a *appState) {
				a.zoom = zoom
				window.SetBounds(frame)
			})
			var dips float32
			var rail, inspector, dock bool
			mygoRunOnMain(func() {
				dips = a.lastWidth
				rail, inspector, dock = a.railOpen(a.lastWidth), a.inspectorOpen(a.lastWidth), a.dockOpen(a.lastWidth, a.lastHeight)
				logf(scenario, "scale %s%% at %dx%d: the content sees %.1fx%.1f DIPs (rail %v, inspector %v, dock %v)",
					scale.name, size.w, size.h, a.lastWidth, a.lastHeight, rail, inspector, dock)
			})
			wantWidth := float32(size.w-frameWidth) / zoom
			check(abs32(dips-wantWidth) <= 2, "scale %s%%: the content got %.1f DIPs from %dx%d pixels (about %.1f)",
				scale.name, dips, size.w, size.h, wantWidth)
			check(rail == (dips >= railAutoWidth), "scale %s%%: the rail fold (%v) follows the %.1f DIPs it got",
				scale.name, rail, dips)
			check(inspector == (dips >= inspectorAutoWidth), "scale %s%%: the inspector fold (%v) follows the %.1f DIPs it got",
				scale.name, inspector, dips)
			if size.name == "min" {
				check(abs32(dips-float32(minContentWidth)) <= 1.5,
					"scale %s%%: the smallest window laid out at %.1f DIPs (%d)", scale.name, dips, minContentWidth)
			}
			bounds := window.Bounds()
			check(inside(bounds, home.WorkArea), "scale %s%% at %dx%d: the whole window is inside the work area (%dx%d at %d,%d)",
				scale.name, size.w, size.h, bounds.Width, bounds.Height, bounds.X, bounds.Y)
		}
	}

	// Back to a normal scale before the state walk, so the states are measured
	// at the scale a person would run the app at.
	step(window, a, dir, scenario, "", "back at 100%", func(a *appState) {
		a.zoom = 1
		window.SetSize(original.Width, original.Height)
	})

	// Maximize and restore: a person expects the un-maximized size to come back.
	step(window, a, dir, scenario, "1-maximized.png", "maximized", func(*appState) { window.Maximize() })
	time.Sleep(700 * time.Millisecond)
	maximized := window.Bounds()
	logf(scenario, "maximized: %dx%d at %d,%d, the work area is %dx%d at %d,%d (IsMaximized %v)",
		maximized.Width, maximized.Height, maximized.X, maximized.Y,
		home.WorkArea.Width, home.WorkArea.Height, home.WorkArea.X, home.WorkArea.Y, window.IsMaximized())
	check(window.IsMaximized(), "the window reported itself maximized")
	// A maximized window reports its frame, which reaches a few pixels beyond
	// the work area on each side.
	check(absInt(maximized.Width, home.WorkArea.Width) <= 20 && absInt(maximized.Height, home.WorkArea.Height) <= 20,
		"a maximized window is %dx%d for a %dx%d work area", maximized.Width, maximized.Height, home.WorkArea.Width, home.WorkArea.Height)

	step(window, a, dir, scenario, "2-restored.png", "unmaximized", func(*appState) { window.Unmaximize() })
	time.Sleep(500 * time.Millisecond)
	restored := window.Bounds()
	logf(scenario, "unmaximized: %dx%d at %d,%d (IsMaximized %v)", restored.Width, restored.Height, restored.X, restored.Y, window.IsMaximized())
	check(!window.IsMaximized(), "the window reported itself unmaximized")
	check(absInt(restored.Width, original.Width) <= 8 && absInt(restored.Height, original.Height) <= 8,
		"unmaximizing gave %dx%d, from %dx%d before maximizing", restored.Width, restored.Height, original.Width, original.Height)

	// Full screen covers the display, and leaving it gives the window back.
	step(window, a, dir, scenario, "3-fullscreen.png", "full screen", func(*appState) { window.SetFullScreen(true) })
	time.Sleep(700 * time.Millisecond)
	full := window.Bounds()
	logf(scenario, "full screen: %dx%d at %d,%d (IsFullScreen %v)", full.Width, full.Height, full.X, full.Y, window.IsFullScreen())
	check(window.IsFullScreen(), "the window reported itself full screen")
	check(full.Width >= home.Bounds.Width && full.Height >= home.Bounds.Height,
		"full screen is %dx%d for a %dx%d display", full.Width, full.Height, home.Bounds.Width, home.Bounds.Height)
	step(window, a, dir, scenario, "", "left full screen", func(*appState) { window.SetFullScreen(false) })
	time.Sleep(500 * time.Millisecond)

	// Minimize and bring the window back: nothing should be resized by it.
	before := window.Bounds()
	step(window, a, dir, scenario, "", "minimized", func(*appState) { window.Minimize() })
	time.Sleep(500 * time.Millisecond)
	step(window, a, dir, scenario, "4-restored-after-minimize.png", "restored from minimized", func(*appState) { window.Restore() })
	time.Sleep(400 * time.Millisecond)
	after := window.Bounds()
	logf(scenario, "minimize round trip: %dx%d at %d,%d -> %dx%d at %d,%d",
		before.Width, before.Height, before.X, before.Y, after.Width, after.Height, after.X, after.Y)
	check(absInt(after.Width, before.Width) <= 8 && absInt(after.Height, before.Height) <= 8,
		"minimizing kept the window size: %dx%d -> %dx%d", before.Width, before.Height, after.Width, after.Height)

	// A second display, when the machine has one.
	if len(displays) < 2 {
		logf(scenario, "one display: the second-monitor placement was not run")
	} else {
		other := displays[1]
		if other.Primary {
			other = displays[0]
		}
		target := other.WorkArea
		width := minInt(target.Width-64, int(1426*1.0))
		height := minInt(target.Height-64, 862)
		x := target.X + (target.Width-width)/2
		y := target.Y + (target.Height-height)/2
		step(window, a, dir, scenario, "5-second-monitor.png", "moved to "+other.Label, func(*appState) {
			window.SetBounds(mygo.Rectangle{X: x, Y: y, Width: width, Height: height})
		})
		time.Sleep(700 * time.Millisecond)
		moved := window.Bounds()
		logf(scenario, "second monitor %q: asked for %dx%d at %d,%d, the window reports %dx%d at %d,%d",
			other.Label, width, height, x, y, moved.Width, moved.Height, moved.X, moved.Y)
		check(intersects(moved, other.WorkArea), "the window landed on the %q display", other.Label)
		step(window, a, dir, scenario, "", "back on the first display", func(*appState) {
			window.SetBounds(original)
			_ = centeredFrame
		})
	}

	logf(scenario, "checks failed: %d", failures)
	a.release()
	mygo.App.Quit()
}

// displayHolding picks the display the rectangle sits on, and the primary when
// it sits on none.
func displayHolding(r mygo.Rectangle, displays []mygo.Display) mygo.Display {
	for _, display := range displays {
		if intersects(r, display.WorkArea) {
			return display
		}
	}
	for _, display := range displays {
		if display.Primary {
			return display
		}
	}
	if len(displays) > 0 {
		return displays[0]
	}
	return mygo.Display{}
}

// intersects reports whether two rectangles share any area.
func intersects(a, b mygo.Rectangle) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// inside reports whether a is entirely within b.
func inside(a, b mygo.Rectangle) bool {
	return a.X >= b.X && a.Y >= b.Y && a.X+a.Width <= b.X+b.Width && a.Y+a.Height <= b.Y+b.Height
}

// centeredFrame places r in the middle of area, shrinking it to fit when it is
// larger. A window this check places is a window this check can hold to being
// inside the area.
func centeredFrame(r, area mygo.Rectangle) mygo.Rectangle {
	if r.Width > area.Width {
		r.Width = area.Width
	}
	if r.Height > area.Height {
		r.Height = area.Height
	}
	r.X = area.X + (area.Width-r.Width)/2
	r.Y = area.Y + (area.Height-r.Height)/2
	return r
}

var workbenchSizes = []struct {
	width, height int
	note          string
}{
	{1440, 900, "large desktop: rail, inspector and dock all present"},
	{1180, 760, "inspector threshold: all three panes just fit"},
	{1000, 720, "rail threshold: inspector folded, rail and dock present"},
	{820, 620, "small window: only the global bar and the stage remain"},
	{640, 560, "floor: panes folded, the picture is not squeezed under them"},
}

// runWorkbench walks the five sizes and captures the frame at each.
func runWorkbench(window *mygo.Window, a *appState, dir string) {
	scenario := "workbench"
	go func() {
		time.Sleep(3 * time.Second)
		for i, size := range workbenchSizes {
			width, height := size.width, size.height
			mygoRunOnMain(func() {
				window.SetSize(width, height)
				window.Invalidate()
			})
			time.Sleep(500 * time.Millisecond)
			mygoRunOnMain(func() {
				logf(scenario, "%s | %s", a.layoutLine(a.frameWidth, a.frameHeight), size.note)
			})
			name := fmt.Sprintf("%d-%dx%d.png", i+1, width, height)
			step(window, a, dir, scenario, name, size.note, nil)
		}
		// A person can reopen what the size rule folded; prove the switch
		// survives one more resize instead of snapping back.
		mygoRunOnMain(func() {
			a.inspector = paneOpen
			a.rail = paneOpen
			window.SetSize(640, 560)
			window.Invalidate()
		})
		time.Sleep(500 * time.Millisecond)
		mygoRunOnMain(func() {
			logf(scenario, "manual panes at 640x560 | %s | %s", a.layoutLine(a.frameWidth, a.frameHeight), fmt.Sprintf("frame %.0fx%.0f", a.frameWidth, a.frameHeight))
		})
		step(window, a, dir, scenario, "6-manual-at-640x560.png",
			"inspector and rail reopened by hand at the smallest size", nil)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

func runWorkspace(window *mygo.Window, a *appState, dir string) {
	go workspaceBody(window, a, dir)
}

func workspaceBody(window *mygo.Window, a *appState, dir string) {
	scenario := "workspace"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	step(window, a, dir, scenario, "1-left.png",
		"the window is at a known frame, on the Sources page, with the rail closed",
		func(a *appState) {
			window.SetBounds(mygo.Rectangle{X: 180, Y: 120, Width: 1000, Height: 760})
			a.workspace = workspaceSources
			a.rail = paneClosed
			a.settingsDraft = a.settings
			a.settingsDraft.Workspace.On = true
			a.applySettingsCommand()
		})
	mygoRunOnMain(func() {
		check(a.settingsErr == "", "apply reported %q", a.settingsErr)
	})
	time.Sleep(600 * time.Millisecond)
	mygoRunOnMain(func() {
		b := window.Bounds()
		logf(scenario, "leaving on page %v at %d,%d %dx%d, rail %s, saved %v",
			a.workspace, b.X, b.Y, b.Width, b.Height, paneModeName(a.rail), a.settingsSaved.Load())
		check(b.Width == 1000 && b.Height == 760,
			"the window was set to 1000x760 (it is %dx%d)", b.Width, b.Height)
		// What closing the window does, without closing it: a scripted run that
		// quit here would race the write it is trying to prove.
		a.saveWorkspaceOnExit(window)
		if data, err := os.ReadFile(a.settingsPath); err == nil {
			logf(scenario, "the file now holds %s", oneLine(data))
		} else {
			logf(scenario, "could not read back %s: %v", a.settingsPath, err)
		}
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

// runWorkspaceRestore is the second process: it starts with the file the first
// one wrote and says what came back. Every number below is read from the window
// and the app state, not from the file.
func runWorkspaceRestore(window *mygo.Window, a *appState, dir string) {
	go workspaceRestoreBody(window, a, dir)
}

func workspaceRestoreBody(window *mygo.Window, a *appState, dir string) {
	scenario := "workspace-restore"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	time.Sleep(700 * time.Millisecond)
	mygoRunOnMain(func() {
		b := window.Bounds()
		want := a.settings.Workspace
		logf(scenario, "loaded page %q, rail %q, inspector %q, dock %q, on %v",
			want.Page, want.Rail, want.Inspector, want.Dock, want.On)
		logf(scenario, "the window is at %d,%d %dx%d, page %v, rail %s",
			b.X, b.Y, b.Width, b.Height, a.workspace, paneModeName(a.rail))
		// The claim is that the window comes back the way the file says, so the
		// check reads what came back against what the file asked for; a frame
		// hard-coded here would only pass for one run. What the file asks for is
		// not always what a window may take: a frame saved on a larger screen or
		// at another scale can be clamped to stay reachable, which is a correct
		// restore rather than a failure. So the expectation is the file's frame
		// after the same clamp the restore itself applies.
		wantFrame := clampWorkspace(mygo.Rectangle{X: want.X, Y: want.Y, Width: want.Width, Height: want.Height},
			mygo.Screen.Displays())
		check(want.On, "the file asked for the window to come back")
		check(b.X == wantFrame.X && b.Y == wantFrame.Y && b.Width == wantFrame.Width && b.Height == wantFrame.Height,
			"the frame came back as %d,%d %dx%d (the file asked for %d,%d %dx%d, or %d,%d %dx%d once clamped)",
			b.X, b.Y, b.Width, b.Height, want.X, want.Y, want.Width, want.Height,
			wantFrame.X, wantFrame.Y, wantFrame.Width, wantFrame.Height)
		wantPage, pageOK := parseWorkspaceID(want.Page)
		check(pageOK && a.workspace == wantPage,
			"the page came back as %v (the file asked for %q)", a.workspace, want.Page)
		wantRail, railOK := parsePaneMode(want.Rail)
		check(railOK && a.rail == wantRail,
			"the rail came back as %s (the file asked for %q)", paneModeName(a.rail), want.Rail)
		capture(window, dir+`\1-restored.png`)
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

func runWorkspaces(window *mygo.Window, a *appState, dir string) {
	scenario := "workspaces"
	go func() {
		time.Sleep(3 * time.Second)
		step(window, a, dir, scenario, "1-stage.png", "the stage page on open", nil)

		mygoRunOnMain(func() { a.workspace = workspaceSources; window.Invalidate() })
		time.Sleep(600 * time.Millisecond)
		step(window, a, dir, scenario, "2-sources.png", "the sources page", nil)

		mygoRunOnMain(func() { a.workspace = workspaceDiagnostics; window.Invalidate() })
		time.Sleep(600 * time.Millisecond)
		step(window, a, dir, scenario, "3-diagnostics.png", "the diagnostics page", nil)

		var page workspace
		mygoRunOnMain(func() { page = a.workspace })
		logf(scenario, "the rail ends on %q with %d sources and %d events",
			page.title(), len(a.items), len(a.session.Events()))

		mygoRunOnMain(func() { a.workspace = workspaceStage; window.Invalidate() })
		time.Sleep(400 * time.Millisecond)
		step(window, a, dir, scenario, "4-back-to-stage.png", "back on the stage", nil)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
