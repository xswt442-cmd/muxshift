package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file groups the scenario entry points in this domain. It was merged from background_scenario.go, budget_scenario.go, contract_scenario.go, crash_scenario.go, faults_scenario.go, frames_scenario.go, interleave_scenario.go, lease_scenario.go, lifecycle_scenario.go, matrix_scenario.go, midjoin_scenario.go, offline_scenario.go, playthrough_scenario.go, probeclose_scenario.go.

func runBackground(window *mygo.Window, a *appState, dir string) {
	go backgroundBody(window, a, dir)
}

func backgroundBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "background"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	// A path a file cannot be renamed onto: renaming a file over a directory
	// fails on every system this build runs on. The settings themselves stay
	// trustworthy, so the only reason a write fails is the filesystem.
	blocked := filepath.Join(dir, "a-directory-stands-here")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		logf(scenario, "cannot set up the blocked settings path: %v", err)
		return
	}
	mygoRunOnMain(func() {
		a.settingsPath = blocked
		a.settingsTrusted = true
		a.settings.SourceMemory.On = true
		a.settings.SourceMemory.Remember = true
	})

	// ledgerState is what the scenario reads through the drawing thread: the
	// count and the newest failure's sentence.
	type ledgerState struct {
		count int
		line  string
	}
	count := func() ledgerState {
		return runOnMainValue(func() ledgerState {
			n, last := a.backgroundFailures()
			return ledgerState{count: n, line: last.line()}
		})
	}

	for i, uri := range []string{
		"https://example.test/first.m3u8",
		"https://example.test/second.m3u8",
	} {
		it := &item{uri: uri, volume: 1, origin: originAdded}
		mygoRunOnMain(func() { a.rememberSource(it) })
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if count().count > i {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	state := count()
	logf(scenario, "two writes to %s: %d failure(s) counted; newest: %s", filepath.Base(blocked), state.count, state.line)
	check(state.count == 2, "the two failed writes were counted as %d, want 2", state.count)
	check(strings.Contains(state.line, "saving a source's remembered sound"),
		"the newest failure names what failed: %q", state.line)
	check(strings.Contains(state.line, "a-directory-stands-here"),
		"the newest failure says where the write went: %q", state.line)

	step(window, a, dir, scenario, "1-failures.png",
		"the diagnostics page states what failed and how often", func(a *appState) {
			a.workspace = workspaceDiagnostics
		})
	time.Sleep(500 * time.Millisecond)

	// Dismissing the notification clears the message, not the record.
	before := runOnMainValue(func() int {
		a.lastErr = errors.New("a message in the status bar")
		a.dismissError()
		if a.lastErr != nil {
			failures++
			logf(scenario, "CHECK FAILED: dismiss left the message in place")
		}
		n, _ := a.backgroundFailures()
		return n
	})
	logf(scenario, "after dismissing the message: %d failure(s) still counted", before)
	check(before == 2, "dismissing the message changed the count to %d, want 2", before)

	// A write that lands does not add to the count, and says so.
	mygoRunOnMain(func() { a.settingsPath = filepath.Join(dir, "settings.json") })
	good := &item{uri: "https://example.test/third.m3u8", volume: 1, origin: originAdded}
	mygoRunOnMain(func() { a.rememberSource(good) })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runOnMainValue(func() bool { return a.settingsSaved.Load() }) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	saved := runOnMainValue(func() bool { return a.settingsSaved.Load() })
	after := count().count
	logf(scenario, "a write to a path that works: saved=%v, %d failure(s) counted", saved, after)
	check(saved, "the write to a working path did not land")
	check(after == 2, "a successful write changed the count to %d, want 2", after)

	step(window, a, dir, scenario, "2-after-a-good-write.png",
		"a write that landed leaves the count where it was", func(a *appState) {
			a.workspace = workspaceDiagnostics
		})
	time.Sleep(500 * time.Millisecond)

	logf(scenario, "done: %d check(s) failed", failures)
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
	logf(scenario, "window closed")
}

func runBudget(window *mygo.Window, a *appState, dir string) {
	go budgetBody(window, a, dir)
}

func budgetBody(window *mygo.Window, a *appState, dir string) {
	scenario := "budget"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	mygoRunOnMain(func() {
		logf(scenario, "the file asks for %s", a.settings.DVR.line())
		logf(scenario, "in force for a buffer opened now: %s from %s", a.budget.line(), a.budgetFrom)
		// The numbers in force are the file's numbers unless a flag was typed;
		// both cases have to agree with the rule the app names.
		if strings.Contains(a.budgetFrom, "settings file") {
			check(a.budget == a.settings.DVR, "without a flag the file's budget is what runs")
		} else {
			check(a.budget == a.flagBudget || a.budget.RetentionSeconds == a.flagBudget.RetentionSeconds,
				"with a flag the command line decides the budget")
		}
		check(dvrRetention == time.Duration(a.budget.RetentionSeconds)*time.Second && dvrMaxBytes == a.budget.MaxBytes &&
			dvrRecordingBytes == a.budget.RecordingBytes,
			"the buffer is built from the budget in force")

		// Apply a new budget exactly as the page does: draft, validate, write.
		a.settingsDraft = a.settings
		a.settingsDraft.DVR = dvrBudget{RetentionSeconds: 180, MaxBytes: 0, RecordingBytes: 32 << 20}
		a.applySettingsCommand()
		check(a.settingsErr == "", "apply reported %q", a.settingsErr)
		if data, err := os.ReadFile(a.settingsPath); err == nil {
			logf(scenario, "the file now holds %s", oneLine(data))
		} else {
			logf(scenario, "could not read back %s: %v", a.settingsPath, err)
		}
		logf(scenario, "after apply: %s from %s", a.budget.line(), a.budgetFrom)
		check(dvrRecordingBytes == a.budget.RecordingBytes && dvrMaxBytes == a.budget.MaxBytes,
			"the buffer follows the budget the page just applied")
		logf(scenario, "checks failed: %d", failures)
		a.release()
		mygo.App.Quit()
	})
}

func runContract(window *mygo.Window, a *appState, dir string) {
	const scenario = "contract"
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source with burnt-in timecode, got %s", shortURI(it.uri))
		return
	}
	step(window, a, dir, scenario, "1-live.png", "the live source before the buffer is cut", nil)

	bufferDir := a.dvrDirFor("contract")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, a.dvrRecordingsDirFor("contract"), dvrRetention, dvrMaxBytes, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			// release closes the buffer after the players and their leases, so
			// the recording is not deleted while anything still reads it.
			a.release()
			mygo.App.Quit()
		})
	}()

	// A range of six seconds holds a middle segment, a boundary after the
	// request, and enough room to ask for a time outside it.
	chosen, ok := waitForRange(runner, 6*time.Second, 30*time.Second)
	if !ok {
		logf(scenario, "no six-second range arrived within 30s: %s", runner.state())
		return
	}
	logf(scenario, "window %s", runner.state())
	logf(scenario, "range %s picked for the cut", chosen)

	// The buffer's clock is its own. This fixture's playlist begins at media
	// zero and the buffer stored its first segment, so buffer zero is media zero
	// here; the burnt-in timecode is what confirms the claim afterwards. A live
	// buffer that joined mid-stream must measure its anchor instead. The anchor
	// is built after the cut because it has to name the recording's own buffer
	// instance and epoch, and it counts as measured, which both mappings demand.

	// The request falls inside a segment, so the file cannot start there: a
	// transport stream segment cannot be cut. The recording has to say both the
	// requested time and the first frame it actually holds.
	request := chosen.Start + 3*time.Second
	mid, err := runner.replay(request)
	if err != nil {
		logf(scenario, "mid-segment request %s: %v", request, err)
		return
	}
	anchor := dvr.Anchor{
		Source:         it.source.ID,
		Epoch:          mid.SourceMediaEpoch,
		BufferInstance: mid.BufferInstance,
		BufferStart:    0,
		MediaStart:     0,
		Interval:       dvr.Range{Start: 0, End: mid.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	logf(scenario, "anchor for %s: buffer %s is media %s (instance %s, epoch %d)",
		anchor.Source, anchor.BufferStart, anchor.MediaStart, anchor.BufferInstance, anchor.Epoch)
	logf(scenario, "mid-segment: %s", mid)
	inside, err := mid.Position(request)
	if err != nil {
		logf(scenario, "position of the request: %v", err)
		return
	}
	at, err := mid.SourceMediaTime(inside, anchor)
	if err != nil {
		logf(scenario, "media time at file position %s: %v", inside, err)
		return
	}
	// Event time runs on the content clock, so it needs an anchor that says what
	// it measured and which stretch of the buffer it covers. This fixture's
	// buffer starts at zero, so buffer time and content time are the same
	// numbers here; the measurement is what makes the anchor usable at all.
	timelineAnchor := dvr.Anchor{
		Source:         it.source.ID,
		Epoch:          mid.SourceMediaEpoch,
		BufferInstance: mid.BufferInstance,
		BufferStart:    mid.StartBufferTime,
		MediaStart:     mid.StartBufferTime,
		Interval:       dvr.Range{Start: mid.StartBufferTime, End: mid.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	event, err := mid.EventTime(inside, timelineAnchor, 0)
	if err != nil {
		logf(scenario, "event time at file position %s: %v", inside, err)
		return
	}
	logf(scenario, "mid-segment: asked %s · file starts %s · request sits at %s · container time %s · event time %s (offset 0)",
		mid.RequestedBufferTime, mid.StartBufferTime, inside, at, event)
	if at != request || event != request {
		logf(scenario, "mid-segment: the mapping disagrees with the request (%s, %s vs %s)", at, event, request)
	}
	// Core fixes media = event + offset, so a positive offset pulls event time
	// back: content 3s at +0.5s is 2.5s on the shared clock.
	for _, offset := range []time.Duration{500 * time.Millisecond, -2 * time.Second} {
		shifted, err := mid.EventTime(inside, timelineAnchor, offset)
		if err != nil {
			logf(scenario, "event time with offset %s: %v", offset, err)
			continue
		}
		logf(scenario, "offset %+dms: content %s -> event %s", offset.Milliseconds(), mid.StartBufferTime+inside, shifted)
	}
	if _, err := mid.SourceMediaTime(0, dvr.Anchor{}); err == nil {
		logf(scenario, "a recording without an anchor was mapped anyway, which it must not be")
	} else {
		logf(scenario, "no anchor: %v (ErrNoAnchor=%v)", err, errors.Is(err, dvr.ErrNoAnchor))
	}
	keptMid := filepath.Join(dir, "contract-mid.ts")
	if err := copyFile(mid.Path, keptMid); err != nil {
		logf(scenario, "keep the mid-segment recording: %v", err)
	} else {
		logf(scenario, "kept the mid-segment recording at %s", keptMid)
	}

	// A request exactly at a segment boundary is the one case where the file
	// does start where it was asked.
	var boundary time.Duration
	for _, segment := range chosen.Segments {
		if segment.Start > request {
			boundary = segment.Start
			break
		}
	}
	if boundary == 0 {
		logf(scenario, "the picked range has no segment after the request, so the boundary case is skipped")
	} else {
		edge, err := runner.replay(boundary)
		if err != nil {
			logf(scenario, "boundary request %s: %v", boundary, err)
		} else {
			logf(scenario, "boundary: %s", edge)
			logf(scenario, "boundary: asks %s and starts %s, coincide=%v",
				edge.RequestedBufferTime, edge.StartBufferTime,
				edge.RequestedBufferTime == edge.StartBufferTime)
			logf(scenario, "boundary: a second cut published %s beside %s", edge.ID, mid.ID)
			keptEdge := filepath.Join(dir, "contract-edge.ts")
			if err := copyFile(edge.Path, keptEdge); err != nil {
				logf(scenario, "keep the boundary recording: %v", err)
			} else {
				logf(scenario, "kept the boundary recording at %s", keptEdge)
			}
		}
		// A published recording is immutable: the second cut must leave the
		// first one intact for whoever already has it open.
		logf(scenario, "the earlier mid recording after a second cut: %v", mid.Valid())
	}

	// A time the buffer does not hold is refused instead of being clamped.
	if _, err := runner.replay(chosen.Start - 2*time.Second); err != nil {
		logf(scenario, "outside the window: %s -> %v (ErrNoRange=%v)",
			chosen.Start-2*time.Second, err, errors.Is(err, dvr.ErrNoRange))
	} else {
		logf(scenario, "outside the window was accepted, which it must not be")
	}
}

// waitForRange waits for a stored range at least length long.
func waitForRange(runner *dvrRunner, length, limit time.Duration) (dvr.Range, bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ranges := runner.buffer.Ranges()
		if len(ranges) > 0 {
			last := ranges[len(ranges)-1]
			if last.End-last.Start >= length {
				return last, true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return dvr.Range{}, false
}

// logRange spells a range for the log.
func logRange(r dvr.Range) string {
	return r.String()
}

func runCrash(window *mygo.Window, a *appState, dir string) {
	go crashBody(window, a, dir)
}

func crashBody(window *mygo.Window, a *appState, dir string) {
	scenario := "crash"
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source to fill the buffer, got %s", shortURI(it.uri))
		return
	}

	// A state worth missing: a different page and a different frame from the one
	// the workspace file holds. If any of this turned up after the restart, it
	// would mean the killed process had written after all.
	mygoRunOnMain(func() {
		window.SetBounds(mygo.Rectangle{X: 320, Y: 200, Width: 940, Height: 700})
		a.workspace = workspaceMixer
	})
	time.Sleep(500 * time.Millisecond)

	// The buffer writes into this run's own session directory, which is where a
	// sweep after the kill has to find it: segments, a marker and a lock the
	// process no longer holds.
	runner, err := startDVR(it.uri, dvrFromMedia(
		a.dvrDirFor("crash"), a.dvrRecordingsDirFor("crash"),
		120*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	if held, ok := waitForRange(runner, 4*time.Second, 60*time.Second); ok {
		logf(scenario, "the buffer holds %s in %s", logRange(held), a.dvrDirFor("crash"))
	} else {
		logf(scenario, "the buffer held nothing within 60s; killing this process would still leave a session directory behind")
	}

	mygoRunOnMain(func() {
		b := window.Bounds()
		logf(scenario, "the window is on %s at %d,%d %dx%d and nothing has been saved",
			a.workspace.title(), b.X, b.Y, b.Width, b.Height)
	})
	logf(scenario, "the driver can kill this process now")
	for i := 0; ; i++ {
		time.Sleep(5 * time.Second)
		logf(scenario, "still running after %d tick(s); %s", i+1, runner.state())
	}
}

func runFaults(window *mygo.Window, a *appState, dir string) {
	go faultsBody(window, a, dir)
}

func faultsBody(window *mygo.Window, a *appState, dir string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			failures++
			logf("faults", "FAIL: "+format, args...)
		}
	}

	// Segments that never arrive without a discontinuity: the buffer splits into
	// two ranges that belong to the same epoch.
	gap, gapFinish := faultRunner(a, dir, 0, "gap")
	if gap != nil {
		ranges := railOf(gap)
		if len(ranges) >= 2 {
			logf("faults", "gap: %d ranges, epochs %s", len(ranges), epochList(ranges))
			check(ranges[0].Epoch == ranges[len(ranges)-1].Epoch,
				"the skipped segments moved the epoch: %s", epochList(ranges))
			bridged, err := bridge(gap, ranges)
			check(err != nil, "a window over the skipped segments was authorised: %v", bridged)
			logf("faults", "gap: a window from %s to %s is refused: %v",
				ranges[0].Start, ranges[len(ranges)-1].End, err)
			past, err := overreach(gap, ranges)
			check(err != nil, "a window past the stored media was authorised: %v", past)
			logf("faults", "gap: a window to %s is refused: %v", ranges[len(ranges)-1].End+4*time.Second, err)
		} else {
			check(false, "the missing segments did not split the buffer: %d range(s)", len(ranges))
		}
		gapFinish()
	}

	// A declared discontinuity: the new media is a different clock, so an anchor
	// measured before it cannot describe the media after it.
	disc, discFinish := faultRunner(a, dir, 1, "disc")
	if disc != nil {
		ranges := railOf(disc)
		if len(ranges) >= 2 {
			logf("faults", "disc: %d ranges, epochs %s", len(ranges), epochList(ranges))
			check(ranges[0].Epoch != ranges[len(ranges)-1].Epoch,
				"the discontinuity did not move the epoch: %s", epochList(ranges))
			bridged, err := bridge(disc, ranges)
			check(err != nil, "a window across the discontinuity was authorised: %v", bridged)
			logf("faults", "disc: a window from %s to %s is refused: %v",
				ranges[0].Start, ranges[len(ranges)-1].End, err)
		} else {
			check(false, "the discontinuity did not split the buffer: %d range(s)", len(ranges))
		}
		discFinish()
	}

	logf("faults", "checks failed: %d", failures)
	// The buffers are what this scenario is about, but an empty stage reads as a
	// session that never played: show a source and let it produce a picture.
	mygoRunOnMain(func() {
		if err := a.showVideo(0); err != nil {
			logf("faults", "show the first source: %v", err)
			return
		}
		if it, err := a.at(0); err == nil && it.player != nil {
			it.player.Play()
		}
	})
	if !waitPlayers(a, 20*time.Second) {
		logf("faults", "not every player was ready within 20s")
	}
	time.Sleep(2 * time.Second)
	mygoRunOnMain(func() {
		if err := capture(window, filepath.Join(dir, "1-faults.png")); err != nil {
			logf("faults", "capture: %v", err)
		}
		a.release()
		mygo.App.Quit()
	})
}

// waitPlayers waits until every source has a ready player, so the screenshot
// shows a session that played rather than empty cards.
func waitPlayers(a *appState, timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if runOnMainValue(func() bool {
			if len(a.items) == 0 {
				return false
			}
			for _, it := range a.items {
				if it.player == nil || !it.player.Ready() {
					return false
				}
			}
			return true
		}) {
			return true
		}
	}
	return false
}

// faultRunner starts a buffer on the source at index and waits until it holds a
// window. It returns nil when the fixture is not usable, logging why.
func faultRunner(a *appState, dir string, index int, scenario string) (*dvrRunner, func()) {
	noop := func() {}
	it, err := a.at(index)
	if err != nil {
		logf("faults", "%s: %v", scenario, err)
		return nil, noop
	}
	if !isNetworkAddress(it.uri) {
		logf("faults", "%s: needs a network source, got %s", scenario, shortURI(it.uri))
		return nil, noop
	}
	runner, err := startDVR(it.uri, dvrFromMedia(
		filepath.Join(dir, scenario, "segments"), filepath.Join(dir, scenario, "recordings"),
		120*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf("faults", "%s: open the buffer: %v", scenario, err)
		return nil, noop
	}
	a.trackRunner(it.uri, runner)
	if windowOf, ok := waitForRange(runner, 6*time.Second, 60*time.Second); ok {
		logf("faults", "%s: the buffer holds %s", scenario, logRange(windowOf))
	} else {
		logf("faults", "%s: the buffer did not hold 6s within 60s", scenario)
		return nil, noop
	}
	// Measure the clock before the window checks, so those checks answer for
	// themselves instead of waiting behind a measurement.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := runner.waitAnchor(ctx); err != nil {
		logf("faults", "%s: measuring the clock: %v", scenario, err)
	}
	return runner, func() {
		if err := runner.Close(); err != nil {
			logf("faults", "%s: close the buffer: %v", scenario, err)
		}
	}
}

func railOf(runner *dvrRunner) []dvr.Range {
	return runner.buffer.Ranges()
}

func epochList(ranges []dvr.Range) string {
	out := make([]string, 0, len(ranges))
	for _, span := range ranges {
		out = append(out, fmt.Sprintf("%d", span.Epoch))
	}
	return strings.Join(out, ",")
}

// bridge asks for a window that starts in the first range and ends in the last,
// which is exactly the stretch no single anchor may cover.
func bridge(runner *dvrRunner, ranges []dvr.Range) (dvr.Anchor, error) {
	window := dvr.Range{Start: ranges[0].Start, End: ranges[len(ranges)-1].End}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runner.waitBufferAnchor(ctx, window)
}

// overreach asks for a window that reaches past what the buffer stored.
func overreach(runner *dvrRunner, ranges []dvr.Range) (dvr.Anchor, error) {
	last := ranges[len(ranges)-1]
	window := dvr.Range{Start: last.Start, End: last.End + 4*time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runner.waitBufferAnchor(ctx, window)
}

func runFrames(window *mygo.Window, a *appState, dir string) {
	go framesBody(window, a, dir, "frames")
}

func framesBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	m, finish := attachMidFlight(a, dir, scenario)
	defer finish()
	if m == nil {
		return
	}
	logf(scenario, "the file runs %s of media", sceneClock(m.length))

	// Keep the bytes the player is reading, so the frame structure can be read
	// without the buffer, the player or MyGo in the way.
	joined := filepath.Join(dir, "joined.ts")
	if err := copyFile(m.recording.Path, joined); err != nil {
		logf(scenario, "keep a copy of the joined file: %v", err)
		check(false, "keep a copy of the joined file: %v", err)
	} else {
		logf(scenario, "kept the joined file at %s (%s of media, buffer %s..%s)",
			joined, sceneClock(m.length), sceneClock(m.recording.StartBufferTime), sceneClock(m.recording.EndBufferTime))
	}

	samples := []time.Duration{0, 2 * time.Second, 4 * time.Second, 6 * time.Second, 8 * time.Second, 10 * time.Second}
	for index, target := range samples {
		target = m.clamp(target)
		position, settled := m.seekSettle(a, scenario, target)
		check(settled, "sample %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		shot := "sample-" + strconv.Itoa(index+1) + ".png"
		before, after, ready, buffering := m.captureSample(window, a, dir, scenario, shot)
		check(ready, "sample %d captured while the player was not ready", index+1)
		check(!buffering, "sample %d captured while the player was buffering", index+1)
		check(absDuration(after-before) <= 40*time.Millisecond,
			"sample %d moved from %s to %s inside one capture", index+1, sceneClock(before), sceneClock(after))
		logf(scenario, "sample %d: target %s | captured between %s and %s | ready=%t buffering=%t",
			index+1, sceneClock(target), sceneClock(before), sceneClock(after), ready, buffering)

		// A paused player may not paint the frame it seeks to: ask it to play
		// for a moment, then pause again, and capture what the window shows
		// then. If this picture differs from the one above, the first capture
		// was a stale surface, not a wrong clock.
		mygoRunOnMain(func() {
			if m.it.player != nil {
				m.it.player.Play()
			}
		})
		time.Sleep(600 * time.Millisecond)
		mygoRunOnMain(func() {
			m.it.player.Pause()
		})
		time.Sleep(300 * time.Millisecond)
		woke := "woken-" + strconv.Itoa(index+1) + ".png"
		wokenBefore, wokenAfter, wokenReady, wokenBuffering := m.captureSample(window, a, dir, scenario, woke)
		check(wokenReady, "woken sample %d captured while the player was not ready", index+1)
		logf(scenario, "woken sample %d: target %s | captured between %s and %s | ready=%t buffering=%t",
			index+1, sceneClock(target), sceneClock(wokenBefore), sceneClock(wokenAfter), wokenReady, wokenBuffering)
	}

	// A zero point that is only right when approached from the left is not a
	// zero point: ask for the same positions in the other order.
	backwards := []time.Duration{10 * time.Second, 6 * time.Second, 0, 6 * time.Second}
	for index, target := range backwards {
		target = m.clamp(target)
		position, settled := m.seekSettle(a, scenario, target)
		check(settled, "reverse seek %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		logf(scenario, "reverse seek %d: asked for %s, player reported %s", index+1, sceneClock(target), sceneClock(position))
	}

	logf(scenario, "checks failed: %d", failures)
}

func runInterleave(window *mygo.Window, a *appState, dir string) {
	go interleaveBody(window, a, dir)
}

func interleaveBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "interleave"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", index+1, shortURI(it.uri))
			return
		}
		uris[index] = it.uri
	}

	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := "interleave-" + shortName(index)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
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
	}
	step(window, a, dir, scenario, "", "confirm both sources and show the first", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				continue
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, 0); err != nil {
					logf(scenario, "source %d: align %s: %v", index+1, track, err)
				}
			}
		}
		a.showVideo(0)
		a.apply()
	})

	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[index], 0); err != nil {
			logf(scenario, "source %d: start its first recording: %v", index+1, err)
			return
		}
		if !waitRecording(a, index, 0, 40*time.Second) {
			logf(scenario, "source %d never showed the recording cut at 0s", index+1)
			return
		}
	}
	time.Sleep(time.Second)
	mygoRunOnMain(func() {
		if err := a.anchor(0); err != nil {
			logf(scenario, "anchor source 1: %v", err)
		}
		// The second source needs an origin of its own before the adapter will
		// read it: the fixtures carry no content offset, so a confirmed zero is
		// the honest statement here.
		if note, err := a.nudge(1, 0); err != nil {
			logf(scenario, "nudge source 2: %v", err)
		} else {
			logf(scenario, "source 2 placed: %s", note)
		}
	})

	first, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	start, _ := a.leaseStats()
	firstGen := runOnMainValue(func() int { return first.generation })
	logf(scenario, "starting from generation %d with %d lease(s) open", firstGen, start)
	// Let both sources play for a moment: a replacement player needs a few frames
	// before it accepts a position.
	time.Sleep(2 * time.Second)

	// 1. A single-source switch commits while the transaction is preparing. The
	// hook runs after every replacement is ready and before the commit step, so
	// the switch is committed by the time the transaction re-reads the item.
	a.beforeSeekCommit = func() {
		logf(scenario, "order 1: a single-source switch commits while the transaction waits")
		it, err := a.at(0)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if err := a.playRecording(it, runners[0], 2*time.Second); err != nil {
			logf(scenario, "order 1: start the switch: %v", err)
			return
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			gen := runOnMainValue(func() int { return it.generation })
			if gen != firstGen {
				logf(scenario, "order 1: the switch committed at generation %d", gen)
				return
			}
			if !time.Now().Before(deadline) {
				logf(scenario, "order 1: the switch did not commit")
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	err = a.startSeekTxn(a.items, 7*time.Second)
	check(err == nil, "order 1: the transaction did not start: %v", err)
	waitSeekGone(a, 60*time.Second)
	a.beforeSeekCommit = nil

	genNow := runOnMainValue(func() int { return first.generation })
	check(genNow == firstGen+1, "order 1: generation = %d, want only the switch's %d", genNow, firstGen+1)
	recording := runOnMainValue(func() *time.Duration {
		if first.replay == nil {
			return nil
		}
		at := first.replay.StartBufferTime
		return &at
	})
	if recording == nil {
		check(false, "order 1: the source lost its recording")
	} else {
		check(*recording == 2*time.Second, "order 1: the source shows a recording cut at %s, want the switch's 2s", sceneClock(*recording))
	}
	open, _ := a.leaseStats()
	check(open == start, "order 1: %d lease(s) open, want the %d the run started with", open, start)

	// 2. A transaction commits while a single-source switch is still waiting for
	// its picture. The switch gives way: it closes what it opened and gives its
	// lease back exactly once.
	second, err := a.at(1)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	secondGen := runOnMainValue(func() int { return second.generation })
	beforeSecond, _ := a.leaseStats()
	a.replayStarted = func(*ui.VideoPlayer) bool { return false }
	if err := a.playRecording(second, runners[1], 2*time.Second); err != nil {
		logf(scenario, "order 2: start the waiting switch: %v", err)
		return
	}
	logf(scenario, "order 2: a switch waits for a picture while the transaction runs")
	err = a.startSeekTxn(a.items, 9*time.Second)
	check(err == nil, "order 2: the transaction did not start: %v", err)
	waitSeekGone(a, 60*time.Second)
	// Give the waiting switch a moment to notice the newer generation.
	time.Sleep(2 * time.Second)
	a.replayStarted = nil

	secondNow := runOnMainValue(func() int { return second.generation })
	check(secondNow == secondGen+1, "order 2: generation = %d, want only the transaction's %d", secondNow, secondGen+1)
	secondCut := runOnMainValue(func() *time.Duration {
		if second.replay == nil {
			return nil
		}
		at := second.replay.StartBufferTime
		return &at
	})
	if secondCut == nil {
		check(false, "order 2: the source lost its recording")
	} else {
		check(*secondCut == 8*time.Second, "order 2: the source shows a recording cut at %s, want the transaction's 8s", sceneClock(*secondCut))
	}
	open, _ = a.leaseStats()
	check(open == beforeSecond, "order 2: %d lease(s) open, want the %d before the transaction", open, beforeSecond)

	// 3. Aborting a transaction that already owns an open player gives the file
	// back exactly once; this is the path shutdown takes.
	beforeThird, _ := a.leaseStats()
	thirdGen := runOnMainValue(func() int { return first.generation })
	err = a.startSeekTxn(a.items, 11*time.Second)
	check(err == nil, "order 3: the transaction did not start: %v", err)
	txn := awaitSeekTxn(a, 20*time.Second)
	if txn == nil {
		logf(scenario, "order 3: the transaction never appeared")
	} else {
		// Close the session the way shutdown does: stop the transaction and wait
		// for the work it started to give its players and leases back.
		a.shutdownReplayTxn()
		waitSeekGone(a, 30*time.Second)
		open, _ = a.leaseStats()
		check(open == beforeThird, "order 3: %d lease(s) open after the abort, want %d", open, beforeThird)
		check(runOnMainValue(func() int { return first.generation }) == thirdGen, "order 3: the aborted transaction changed a generation")
	}
	step(window, a, dir, scenario, "1-after-interleaving.png", "the stage after both orders", func(a *appState) {
		a.showVideo(0)
	})
	logf(scenario, "checks failed: %d", failures)
}

// waitSeekGone waits until no transaction is in flight.
func waitSeekGone(a *appState, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running == nil {
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitSeekTxn returns the transaction in flight, waiting for one to appear.
func awaitSeekTxn(a *appState, timeout time.Duration) *seekTxn {
	deadline := time.Now().Add(timeout)
	for {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running != nil {
			return running
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runLease(window *mygo.Window, a *appState, dir string) {
	const scenario = "lease"
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source to buffer, got %s", shortURI(it.uri))
		return
	}

	// The budget holds two of this fixture's snapshots (2 MiB each), so every cut
	// after the second has to drop an unleased one to make room.
	const budget = int64(5 << 20)
	bufferDir := filepath.Join(dir, "buffer")
	recordingsDir := filepath.Join(dir, "recordings")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, recordingsDir, 20*time.Second, 0, budget, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()

	windowOf, ok := waitForRange(runner, 8*time.Second, 45*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 8s of media within 45s")
		return
	}
	logf(scenario, "the buffer holds %s", logRange(windowOf))

	recording, leaseErr := cutAndHold(runner, windowOf.Start)
	if leaseErr != nil {
		logf(scenario, "cut the recording: %v", leaseErr)
		return
	}
	logf(scenario, "leased %s (%d bytes) before handing it to a consumer", recording.FileName(), recording.Size)
	logRecordings(scenario, "after the cut", bufferDir, recordingsDir)

	// The consumer: an open handle the process keeps for the whole check, which is
	// exactly the state Windows refuses to delete and the buffer must respect.
	held, err := os.Open(recording.Path)
	if err != nil {
		logf(scenario, "open the recording as a consumer: %v", err)
		return
	}
	defer held.Close()

	// Every further cut has to make room by dropping an unleased snapshot; the
	// leased one must survive each of them.
	for i := 1; i <= 3; i++ {
		var cut dvr.Recording
		var cutErr error
		mygoRunOnMain(func() {
			cut, cutErr = runner.replay(windowOf.Start + time.Duration(i)*time.Second)
		})
		if cutErr != nil {
			logf(scenario, "cut %d: %v", i, cutErr)
			continue
		}
		logf(scenario, "cut %d: %s (%d bytes); the leased file is still valid: %v",
			i, cut.FileName(), cut.Size, recording.Valid())
		logf(scenario, "budget: %s", runner.budgetLine())
	}
	logRecordings(scenario, "after three cuts", bufferDir, recordingsDir)

	// Closing the buffer with the lease held is the strongest case: the segments
	// go away, the recording the consumer reads does not.
	var closeErr error
	mygoRunOnMain(func() { closeErr = runner.Close() })
	logf(scenario, "closed the buffer while the lease was held: %v", closeErr)
	logf(scenario, "after the close the leased file is still valid: %v", recording.Valid())
	logRecordings(scenario, "after the close", bufferDir, recordingsDir)

	// A decode of the file the consumer still holds shows the bytes survived: the
	// segments the buffer evicted and the close did not cut the file short.
	if err := decodeRecording(recording); err != nil {
		logf(scenario, "decoding the leased recording: %v", err)
	} else {
		logf(scenario, "the leased recording still decodes after the buffer closed")
	}

	// The consumer lets go first; only then may the buffer take the file back.
	// A retry now finds nothing to do: the close skipped the file because the
	// lease held it, so it was never queued for cleanup.
	if left := runner.retryCleanup(); len(left) != 0 {
		logf(scenario, "retry while the lease is held = %v, want nothing queued", left)
	} else {
		logf(scenario, "retry while the lease is held finds nothing queued")
	}
	if err := held.Close(); err != nil {
		logf(scenario, "close the consumer handle: %v", err)
	}
	pending, releaseErr := runner.releaseHold()
	logf(scenario, "released the lease after the consumer closed: err=%v pending=%v", releaseErr, pending)
	if err := recording.Valid(); err == nil {
		logf(scenario, "the recording outlived its release: Valid still passes")
	} else {
		logf(scenario, "the recording is gone as soon as the consumer let it go: %v", err)
	}
	logRecordings(scenario, "after the release", bufferDir, recordingsDir)

	// A file whose handle closes after the buffer did is what the shutdown retry
	// exists for: the close reports it, the first retry still cannot take it, and
	// the retry after the handle lets go collects it along with the directory.
	lateDir := filepath.Join(dir, "late")
	lateRecordings := filepath.Join(dir, "late-recordings")
	late, err := startDVR(it.uri, dvrFromMedia(lateDir, lateRecordings, 20*time.Second, 0, 4<<20, it.source.ID))
	if err != nil {
		logf(scenario, "open the late buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, late)
	defer func() { _ = late.Close() }()
	lateWindow, ok := waitForRange(late, 4*time.Second, 45*time.Second)
	if !ok {
		logf(scenario, "the late buffer did not hold 4s of media within 45s")
		return
	}
	var lateRec dvr.Recording
	mygoRunOnMain(func() {
		lateRec, err = late.replay(lateWindow.Start)
	})
	if err != nil {
		logf(scenario, "cut the late recording: %v", err)
		return
	}
	handle, err := os.Open(lateRec.Path)
	if err != nil {
		logf(scenario, "open the late recording: %v", err)
		return
	}
	lateErr := late.Close()
	logf(scenario, "closed the late buffer with the file held: %v", lateErr)
	if left := late.retryCleanup(); len(left) != 1 {
		logf(scenario, "the first retry = %v, want the held recording", left)
	} else {
		logf(scenario, "the first retry still cannot take %s", left[0])
	}
	if err := handle.Close(); err != nil {
		logf(scenario, "close the late handle: %v", err)
	}
	if left := late.retryCleanup(); len(left) != 0 {
		logf(scenario, "the retry after the handle closed = %v, want nothing", left)
	} else {
		logf(scenario, "the retry after the handle closed collected the recording")
	}

	// A budget that cannot hold a cut is refused instead of taking space from a
	// file somebody is already reading.
	tinyDir := filepath.Join(dir, "tiny")
	tinyRecordings := filepath.Join(dir, "tiny-recordings")
	tiny, err := startDVR(it.uri, dvrFromMedia(tinyDir, tinyRecordings, 20*time.Second, 0, 1, it.source.ID))
	if err != nil {
		logf(scenario, "open the tiny buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, tiny)
	defer func() { _ = tiny.Close() }()
	if windowOf, ok := waitForRange(tiny, 4*time.Second, 45*time.Second); ok {
		var refused error
		mygoRunOnMain(func() {
			_, refused = tiny.replay(windowOf.Start)
		})
		logf(scenario, "a one-byte budget refuses the cut: %v (ErrNoSpace=%v)",
			refused, errors.Is(refused, dvr.ErrNoSpace))
	}
}

// cutAndHold cuts a recording at from and leases it on the drawing thread, where
// the buffer's owner runs.
func cutAndHold(runner *dvrRunner, from time.Duration) (dvr.Recording, error) {
	var recording dvr.Recording
	var err error
	mygoRunOnMain(func() {
		recording, err = runner.replay(from)
		if err != nil {
			return
		}
		if _, leaseErr := runner.hold(recording.ID); leaseErr != nil {
			err = fmt.Errorf("lease %s: %w", recording.ID, leaseErr)
		}
	})
	return recording, err
}

// decodeRecording runs ffmpeg over the recording and reports whether it decodes
// at all, so a file that was emptied or half-deleted cannot pass as valid.
func decodeRecording(recording dvr.Recording) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not on PATH: %w", err)
	}
	out, err := exec.Command(ffmpeg, "-v", "error", "-i", recording.Path,
		"-f", "null", "-").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, firstLine(string(out)))
	}
	return nil
}

// firstLine trims a tool's output to the first non-empty line.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// logRecordings states what the buffer directory holds at this point, so the run
// shows the files as well as what the buffer says about them.
func logRecordings(scenario, when, dir, recordingsDir string) {
	logDir(scenario, when+" segments", dir)
	logDir(scenario, when+" recordings", recordingsDir)
}

func logDir(scenario, when, dir string) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		logf(scenario, "%s: %s is gone", when, filepath.Base(dir))
		return
	}
	if err != nil {
		logf(scenario, "%s: read %s: %v", when, dir, err)
		return
	}
	var names []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		names = append(names, fmt.Sprintf("%s %d", entry.Name(), info.Size()))
	}
	if len(names) == 0 {
		logf(scenario, "%s: %s holds nothing", when, filepath.Base(dir))
		return
	}
	logf(scenario, "%s: %s holds %s", when, filepath.Base(dir), strings.Join(names, ", "))
}

func runLifecycle(window *mygo.Window, a *appState, dir string) {
	scenario := "lifecycle"
	go func() {
		time.Sleep(300 * time.Millisecond)
		start := time.Now()

		// 1. Every source the command line named must be ready before the cases
		// change anything, so a later failure cannot be blamed on start-up.
		for i := range a.items {
			sample := a.sampleOnMain(i, start)
			logf(scenario, "start: %s", sample.line())
		}

		// 2. A burst of reloads: three replacements 150ms apart. Only the last
		// one may own the source, and the handovers must not close a player that
		// a later one draws.
		if len(a.items) > 0 {
			for round := 1; round <= 3; round++ {
				mygoRunOnMain(func() { a.reopenPlayer(a.items[0]) })
				logf(scenario, "reload burst: asked for replacement %d", round)
				time.Sleep(150 * time.Millisecond)
			}
			ready := waitPlayerReady("lifecycle", rawPlayer(a.items[0].player), 25*time.Second)
			sample := a.sampleOnMain(0, start)
			logf(scenario, "reload burst: ready=%t generation=%d %s", ready, a.generationOf(0), sample.line())
			step(window, a, dir, scenario, "1-after-burst.png", "after the reload burst", nil)
		}

		// 3. Remove the second source while the window runs; the first keeps
		// drawing and the session keeps exactly one source.
		if len(a.items) > 1 {
			var err error
			mygoRunOnMain(func() { err = a.remove(1) })
			if err != nil {
				logf(scenario, "remove source 2 failed: %v", err)
			} else {
				logf(scenario, "remove source 2: %d source(s) left in the window, %d in the session, shown=%s",
					len(a.items), len(a.session.Sources()), a.sceneLabel())
			}
			time.Sleep(700 * time.Millisecond)
			step(window, a, dir, scenario, "2-after-remove.png", "after removing the second source", nil)
		}

		// 4. Add a source while the window runs: the case P8 left open. The
		// result is logged whichever way it goes.
		var added *item
		var addErr error
		mygoRunOnMain(func() {
			added, addErr = a.add(a.lifecycleAddition())
			if addErr == nil {
				a.items = append(a.items, added)
				a.apply()
			}
		})
		if addErr != nil {
			logf(scenario, "adding a source at runtime failed: %v", addErr)
		} else {
			index := len(a.items) - 1
			mygoRunOnMain(func() {
				if err := a.showVideo(index); err != nil {
					logf(scenario, "showing the added source failed: %v", err)
				}
			})
			ready := waitPlayerReady("lifecycle", rawPlayer(added.player), 15*time.Second)
			sample := a.sampleOnMain(index, start)
			logf(scenario, "added a source at runtime: ready=%t %s", ready, sample.line())
			step(window, a, dir, scenario, "3-added-at-runtime.png", "a source added at runtime", nil)
			mygoRunOnMain(func() {
				if err := a.remove(index); err != nil {
					logf(scenario, "removing the added source failed: %v", err)
				}
			})
			logf(scenario, "removed the added source: %d source(s) left", len(a.items))
		}

		// 5. An address that cannot open must report an error and leave the live
		// source drawing. The open call itself is asynchronous in this engine, so
		// the error is read from the player instead of from the call's return.
		bad, openErr := openPlayerOnMain("http://127.0.0.1:9/not-there.m3u8")
		if openErr != nil {
			logf(scenario, "opening an unreachable address: open returned %v", openErr)
		} else if bad != nil {
			var state engineState
			deadline := time.Now().Add(6 * time.Second)
			for {
				state = readPlayer(bad)
				if state.Ready || state.Err != "" || time.Now().After(deadline) {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			logf(scenario, "opening an unreachable address: no error on the call; ready=%t err=%s", state.Ready, emptyWord(state.Err))
			mygoRunOnMain(func() { bad.Close() })
		}
		if len(a.items) > 0 {
			sample := a.sampleOnMain(0, start)
			logf(scenario, "after the failed open: %s", sample.line())
		}

		// 6. Close everything; the process must exit rather than hang on a
		// player it cannot release.
		mygoRunOnMain(func() {
			for len(a.items) > 0 {
				if err := a.remove(0); err != nil {
					logf(scenario, "closing source %d: %v", 1, err)
					break
				}
			}
			logf(scenario, "closed every source: %d left, %d event(s)", len(a.items), len(a.session.Events()))
			a.release()
			mygo.App.Quit()
		})
	}()
}

// generationOf reads one source's generation on the drawing thread.
func (a *appState) generationOf(index int) int {
	generation := 0
	mygoRunOnMain(func() { generation = a.items[index].generation })
	return generation
}

// lifecycleAddition is the local file the lifecycle scenario adds while the
// window runs. It falls back to the first source's address when that is itself
// local, so the run needs no extra argument.
func (a *appState) lifecycleAddition() string {
	for _, it := range a.items {
		if !isNetworkAddress(it.uri) {
			return it.uri
		}
	}
	return a.items[0].uri
}

type readySample struct {
	Index   int
	Ready   bool
	After   time.Duration
	Playing bool
	Buf     bool
	Pos     time.Duration
	Dur     time.Duration
	Err     string
	Frames  int
}

func (s readySample) line() string {
	state := "not_ready"
	if s.Ready {
		state = "ready"
	}
	return fmt.Sprintf("source %d: %s after %s playing=%t buffering=%t pos=%s dur=%s frames=%d err=%s",
		s.Index+1, state, s.After.Round(time.Millisecond), s.Playing, s.Buf,
		s.Pos.Round(time.Millisecond), s.Dur.Round(time.Millisecond), s.Frames, emptyWord(s.Err))
}

// sampleOnMain reads one source on the drawing thread.
func (a *appState) sampleOnMain(index int, since time.Time) readySample {
	sample := readySample{Index: index, After: time.Since(since)}
	mygoRunOnMain(func() {
		it := a.items[index]
		sample.Frames = a.frameCount
		if it.player == nil {
			sample.Err = "no player"
			return
		}
		state := readPlayer(rawPlayer(it.player))
		sample.Ready = state.Ready
		sample.Playing = state.Playing
		sample.Buf = state.Buf
		sample.Pos = state.Pos
		sample.Dur = state.Dur
		sample.Err = state.Err
	})
	return sample
}

// waitForMatrixReady polls every source until each reports ready, an error, or
// the timeout passes, so the log records what the players did instead of what
// the scenario hoped.
func waitForMatrixReady(a *appState, start time.Time, timeout time.Duration) []readySample {
	deadline := time.Now().Add(timeout)
	samples := make([]readySample, len(a.items))
	for {
		all := true
		for i := range a.items {
			samples[i] = a.sampleOnMain(i, start)
			if !samples[i].Ready && samples[i].Err == "" && samples[i].Err != "no player" {
				all = false
			}
		}
		if all || time.Now().After(deadline) {
			return samples
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// runMatrix records the baseline the later P14 work is compared against.
func runMatrix(window *mygo.Window, a *appState, dir string) {
	scenario := "matrix"
	start := time.Now()
	go func() {
		time.Sleep(300 * time.Millisecond)
		logf(scenario, "opened %d source(s), waiting up to 25s for each to report ready", len(a.items))
		samples := waitForMatrixReady(a, start, 25*time.Second)
		for _, sample := range samples {
			logf(scenario, sample.line())
		}

		// Walk the picture across the sources, pausing long enough for the
		// picture to actually change between captures.
		for i := range a.items {
			mygoRunOnMain(func() {
				if err := a.showVideo(i); err != nil {
					logf(scenario, "show source %d failed: %v", i+1, err)
				}
			})
			time.Sleep(2 * time.Second)
			sample := a.sampleOnMain(i, start)
			logf(scenario, "picture on source %d: %s", i+1, sample.line())
			if i == 0 || i == len(a.items)-1 {
				step(window, a, dir, scenario, fmt.Sprintf("%d-picture-%d.png", i+1, i+1),
					fmt.Sprintf("source %d on the stage", i+1), nil)
			}
		}

		// Pause: the positions must stop moving.
		mygoRunOnMain(func() { logGroup(scenario, "pause", a.pauseAll()) })
		before := a.sampleOnMain(0, start)
		time.Sleep(2 * time.Second)
		after := a.sampleOnMain(0, start)
		logf(scenario, "while paused: pos %s -> %s (moved %s)",
			before.Pos.Round(time.Millisecond), after.Pos.Round(time.Millisecond),
			(after.Pos - before.Pos).Round(time.Millisecond))

		// Resume: the positions must move again.
		mygoRunOnMain(func() { logGroup(scenario, "play", a.playAll()) })
		resumed := a.sampleOnMain(0, start)
		time.Sleep(2 * time.Second)
		later := a.sampleOnMain(0, start)
		logf(scenario, "after resuming: pos %s -> %s (moved %s)",
			resumed.Pos.Round(time.Millisecond), later.Pos.Round(time.Millisecond),
			(later.Pos - resumed.Pos).Round(time.Millisecond))

		// Two sources at once: picture from the first, sound from the second.
		if len(a.items) > 1 {
			mygoRunOnMain(func() {
				if err := a.showVideo(0); err != nil {
					logf(scenario, "show source 1 failed: %v", err)
				}
				if err := a.hearAudio(1, 1); err != nil {
					logf(scenario, "hear source 2 failed: %v", err)
				}
			})
			time.Sleep(2 * time.Second)
			logf(scenario, "picture 1 with sound 2: %s", a.sceneLabel())
			step(window, a, dir, scenario, "picture-1-sound-2.png",
				"picture from the first source, sound from the second", nil)
			mygoRunOnMain(func() {
				if err := a.showVideo(1); err != nil {
					logf(scenario, "show source 2 failed: %v", err)
				}
				if err := a.hearAudio(0, 1); err != nil {
					logf(scenario, "hear source 1 failed: %v", err)
				}
			})
			time.Sleep(2 * time.Second)
			logf(scenario, "picture 2 with sound 1: %s", a.sceneLabel())
		}

		logf(scenario, "the matrix ends with %d source(s) and %d event(s)",
			len(a.items), len(a.session.Events()))
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}

type midFlight struct {
	it        *item
	runner    *dvrRunner
	recording *dvr.Recording
	anchor    *dvr.Anchor
	length    time.Duration
}

// runMidJoin proves the case the buffer is really for: the source was already
// running when MuxShift attached, so the buffer's clock does not start at the
// source's zero, and neither does the file cut from it. Four numbers are logged
// for every sample -- the cut's StartBufferTime, the anchor's MediaStart, the
// player's Position(), and the burned-in timecode in the captured picture -- so
// the fixed offset and the drift over play time can be told apart.
func runMidJoin(window *mygo.Window, a *appState, dir string) {
	go midJoinBody(window, a, dir, "midjoin")
}

// attachMidFlight starts a buffer on the source at index 0, waits until it holds
// a window, cuts a recording from the first stored segment and opens it on the
// stage of that source. finish must run before the caller returns.
func attachMidFlight(a *appState, dir, scenario string) (flight *midFlight, finish func()) {
	finish = func() {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return nil, finish
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a live network source, got %s", shortURI(it.uri))
		return nil, finish
	}
	runner, err := startDVR(it.uri, dvrFromMedia(
		filepath.Join(dir, "segments"), filepath.Join(dir, "recordings"),
		120*time.Second, 0, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return nil, finish
	}
	a.trackRunner(it.uri, runner)

	windowOf, ok := waitForRange(runner, 8*time.Second, 90*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 8s of the live source within 90s")
		return nil, finish
	}
	logf(scenario, "the buffer attached mid-flight and holds %s; %s", logRange(windowOf), runner.budgetLine())

	// Cut from the first segment the buffer stored -- the point where MuxShift
	// attached -- and open that file. Its own zero has to be its own first
	// frame, which is somewhere inside the source's timeline, not at zero.
	base := windowOf.Start
	if err := a.playRecording(it, runner, base); err != nil {
		logf(scenario, "open a recording of the live buffer: %v", err)
		return nil, finish
	}
	if !waitRecording(a, 0, base, 20*time.Second) {
		logf(scenario, "the recording never reached the stage")
		return nil, finish
	}
	recording := showing(it)
	anchor := it.replayAnchor
	if recording == nil || anchor == nil {
		logf(scenario, "the stage shows no measured recording: recording=%v anchor=%v", recording, anchor)
		return nil, finish
	}
	logf(scenario, "the recording %s was cut at buffer %s; its anchor measured media %s at buffer %s",
		recording.FileName(), sceneClock(recording.StartBufferTime), sceneClock(anchor.MediaStart), sceneClock(anchor.BufferStart))
	if anchor.MediaStart <= time.Second {
		logf(scenario, "the live source's media clock starts at %s, so this run cannot show a mid-flight attach", sceneClock(anchor.MediaStart))
		return nil, finish
	}
	if recording.StartBufferTime != base {
		logf(scenario, "the cut starts at buffer %s, not at the first stored segment %s",
			sceneClock(recording.StartBufferTime), sceneClock(base))
		return nil, finish
	}
	return &midFlight{
		it:        it,
		runner:    runner,
		recording: recording,
		anchor:    anchor,
		length:    recording.EndBufferTime - recording.StartBufferTime,
	}, finish
}

// seekSettle pauses the player, asks for target and waits until the player
// reports that position while ready and not buffering. A seek issued while a
// player is still opening is dropped, so it asks again a few times.
func (m *midFlight) seekSettle(a *appState, scenario string, target time.Duration) (time.Duration, bool) {
	target = m.clamp(target)
	for attempt := 0; attempt < 4; attempt++ {
		mygoRunOnMain(func() {
			if m.it.player != nil {
				m.it.player.Pause()
				m.it.player.Seek(target)
			}
		})
		deadline := time.Now().Add(4 * time.Second)
		settled := 0
		for time.Now().Before(deadline) {
			time.Sleep(150 * time.Millisecond)
			position, ready, buffering := playerSnapshot(a, m.it)
			if ready && !buffering && absDuration(position-target) <= 40*time.Millisecond {
				settled++
				if settled >= 2 {
					return position, true
				}
				continue
			}
			settled = 0
		}
		logf(scenario, "target %s: attempt %d did not settle", sceneClock(target), attempt+1)
	}
	position, _, _ := playerSnapshot(a, m.it)
	return position, false
}

// clamp keeps a sample inside the file, away from the very end where the
// player may have nothing left to show.
func (m *midFlight) clamp(target time.Duration) time.Duration {
	if target < 0 {
		return 0
	}
	if target > m.length-500*time.Millisecond {
		return m.length - 500*time.Millisecond
	}
	return target
}

// captureSample reads the position before the capture, takes the picture, then
// reads the position again -- all in one drawing-thread pass -- so the two
// numbers bracket the moment the raster was asked for.
func (m *midFlight) captureSample(window *mygo.Window, a *appState, dir, scenario, name string) (before, after time.Duration, ready, buffering bool) {
	mygoRunOnMain(func() {
		before, ready, buffering = playerSnapshot(a, m.it)
		_ = capture(window, filepath.Join(dir, name))
		after, _, _ = playerSnapshot(a, m.it)
	})
	logf(scenario, "%s: captured between Position() %s and %s (ready=%t buffering=%t)",
		name, sceneClock(before), sceneClock(after), ready, buffering)
	return before, after, ready, buffering
}

// playerSnapshot reads one player's position and state on the drawing thread.
func playerSnapshot(a *appState, it *item) (position time.Duration, ready, buffering bool) {
	if it.player == nil {
		return 0, false, false
	}
	return it.player.Position(), it.player.Ready(), it.player.Buffering()
}

func midJoinBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	m, finish := attachMidFlight(a, dir, scenario)
	defer finish()
	if m == nil {
		return
	}
	logf(scenario, "the file runs %s of media", sceneClock(m.length))

	samples := []time.Duration{0, m.length / 2, m.length - 2*time.Second}
	for index, target := range samples {
		position, ok := m.seekSettle(a, scenario, target)
		logf(scenario, "sample %d: asked for %s | StartBufferTime %s | Anchor.MediaStart %s | Position() %s | drift %s",
			index+1, sceneClock(target), sceneClock(m.recording.StartBufferTime), sceneClock(m.anchor.MediaStart),
			sceneClock(position), sceneClock(position-target))
		check(ok, "sample %d asked for %s and the player settled at %s", index+1, sceneClock(target), sceneClock(position))
		shot := "sample-" + strconv.Itoa(index+1) + ".png"
		m.captureSample(window, a, dir, scenario, shot)
	}
	logf(scenario, "checks failed: %d", failures)
}

func runOffline(window *mygo.Window, a *appState, dir string) {
	go offlineBody(window, a, dir)
}

func offlineBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "offline"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	if len(a.items) < 1 {
		logf(scenario, "needs a network source, got none")
		return
	}
	source, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(source.uri) {
		logf(scenario, "source 1 is not a network address: %s", shortURI(source.uri))
		return
	}

	runner, err := startDVR(source.uri, dvrFromMedia(
		a.dvrDirFor("offline"), a.dvrRecordingsDirFor("offline"),
		dvrRetention, 0, dvrRecordingBytes, source.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(source.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			if err := runner.Close(); err != nil {
				logf(scenario, "close the buffer: %v", err)
			}
		})
	}()

	rng, ok := waitForRange(runner, 8*time.Second, 90*time.Second)
	if !ok {
		logf(scenario, "the buffer never held a range the test could watch")
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}
	logf(scenario, "the buffer holds %s..%s at %s; the source may be taken away now",
		rng.Start, rng.End, time.Now().Format("15:04:05"))

	// Watch the end of the buffer. While the source is reachable it moves; when
	// it is not, it stands still. A stall counts only after it lasts a while, so
	// an ordinary two-second gap between segments is not mistaken for an outage.
	lastEnd := rng.End
	stallSince := time.Time{}
	stalled := false
	cutAt := time.Now()
	for time.Since(cutAt) < 90*time.Second {
		time.Sleep(time.Second)
		ranges := runner.buffer.Ranges()
		if len(ranges) == 0 {
			continue
		}
		end := ranges[len(ranges)-1].End
		if end > lastEnd+200*time.Millisecond {
			lastEnd = end
			stallSince = time.Time{}
			continue
		}
		if stallSince.IsZero() {
			stallSince = time.Now()
			continue
		}
		if time.Since(stallSince) >= 6*time.Second {
			stalled = true
			logf(scenario, "the buffer stopped growing at %s while the source was away", end)
			break
		}
	}
	check(stalled, "the buffer never stopped growing; the source was not taken away")

	// The two ways a source can go away do not read the same. A refused
	// connection is a read failure and must be named. A publisher that stops
	// while its server keeps answering is not a read failure: the buffer stands
	// still because nothing new arrives, and inventing an error there would be a
	// lie. The mode says which one this run arranged, and each is checked for
	// what it should say, not for the other's.
	expectReadError := outageMode == "server"
	usage := runner.usage()
	logf(scenario, "%s outage: the page's buffer line says %q", outageMode, usage.readErr)
	if expectReadError {
		check(usage.readErr != "", "a refused connection left the buffer line without a reason")
	} else {
		check(usage.readErr == "", "a stalled publisher was reported as a read failure: %s", usage.readErr)
	}
	step(window, a, dir, scenario, "1-during-the-outage.png", "the source is away and the page says what it can", func(a *appState) {
		a.workspace = workspaceDiagnostics
	})
	if !stalled {
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
		return
	}

	resumed := false
	back := time.Now()
	for time.Since(back) < 120*time.Second {
		time.Sleep(500 * time.Millisecond)
		ranges := runner.buffer.Ranges()
		if len(ranges) == 0 {
			continue
		}
		if end := ranges[len(ranges)-1].End; end >= lastEnd+4*time.Second {
			logf(scenario, "the buffer grew again to %s after the source came back", end)
			lastEnd = end
			resumed = true
			break
		}
	}
	check(resumed, "the buffer did not grow again within two minutes of the source coming back")
	if back := runner.usage(); back.readErr != "" {
		check(false, "the buffer still says the source cannot be read after it came back: %s", back.readErr)
	} else {
		logf(scenario, "the buffer reads fine again; its row no longer mentions a failure")
	}
	var playerUp bool
	var state string
	mygoRunOnMain(func() {
		it, err := a.at(0)
		if err == nil {
			playerUp = it.player != nil
		}
		state = runner.state()
	})
	check(playerUp, "the source lost its player during the outage")
	check(state != "closed", "the buffer reports itself closed after the outage")
	logf(scenario, "the source's buffer reports %q when the watch ended", state)

	step(window, a, dir, scenario, "2-after-the-source-came-back.png", "the source came back and the session is standing", func(a *appState) {
		a.workspace = workspaceDiagnostics
	})

	logf(scenario, "offline: done: %d check(s) failed", failures)
	mygoRunOnMain(func() {
		a.release()
		mygo.App.Quit()
	})
	logf(scenario, "window closed")
}

func runPlayThrough(window *mygo.Window, a *appState, dir string) {
	go playThroughBody(window, a, dir, "playthrough")
}

func playThroughBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		finishScenario(a)
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if takeSnapshot(a, it).ready {
			break
		}
		if time.Now().After(deadline) {
			logf(scenario, "the player never became ready")
			check(false, "the player never became ready")
			finishScenario(a)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	length := runOnMainValue(func() time.Duration {
		if it.player == nil {
			return 0
		}
		return it.player.Duration()
	})
	logf(scenario, "the file runs %s", sceneClock(length))

	// Steady playback: let it run and sample the position and the picture
	// together, the way a viewer would notice the two drifting apart.
	mygoRunOnMain(func() {
		if it.player != nil {
			it.player.Play()
		}
	})
	const samples = 20
	for index := 0; index < samples; index++ {
		time.Sleep(400 * time.Millisecond)
		name := fmt.Sprintf("p-%02d.png", index)
		before, after, ready, buffering := bracketCapture(window, a, it, dir, name, scenario)
		check(ready, "sample %d captured while the player was not ready", index+1)
		check(!buffering, "sample %d captured while the player was buffering", index+1)
		check(after >= before, "sample %d went backwards from %s to %s", index+1, sceneClock(before), sceneClock(after))
	}

	// A seek that is then resumed: how long until the picture catches up with
	// the position the player already reports?
	target := insideFile(6*time.Second, length)
	position, settled := settle(a, it, target)
	check(settled, "the seek to %s left the player at %s", sceneClock(target), sceneClock(position))
	logf(scenario, "paused at %s and asked for %s before resuming", sceneClock(position), sceneClock(target))
	mygoRunOnMain(func() {
		if it.player != nil {
			it.player.Play()
		}
	})
	const resumeSamples = 20
	for index := 0; index < resumeSamples; index++ {
		name := fmt.Sprintf("r-%02d.png", index)
		before, _, ready, buffering := bracketCapture(window, a, it, dir, name, scenario)
		check(ready, "resume sample %d captured while the player was not ready", index+1)
		check(!buffering, "resume sample %d captured while the player was buffering", index+1)
		check(before >= target-100*time.Millisecond, "resume sample %d was still at %s, behind the seek to %s",
			index+1, sceneClock(before), sceneClock(target))
		time.Sleep(100 * time.Millisecond)
	}
	logf(scenario, "checks failed: %d", failures)
	finishScenario(a)
}

func runProbeClose(window *mygo.Window, a *appState, dir string) {
	go probeCloseBody(window, a, dir)
}

func probeCloseBody(window *mygo.Window, a *appState, dir string) {
	var started, closed bool
	var attempt, after uint64
	var pid int
	var failed int
	check := func(ok bool, format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if ok {
			logf("probeclose", "ok: %s", line)
			return
		}
		failed++
		logf("probeclose", "FAIL: %s", line)
	}

	// A comparison needs a duration, and a player reports one only after the
	// playlist and its first segments have arrived. Starting before that fails
	// before any decoder exists, which is the opposite of what this check is for.
	ready := func() bool {
		return runOnMainValue(func() bool {
			if len(a.items) < 2 {
				return false
			}
			for _, it := range a.items[:2] {
				if it.player == nil || it.player.Duration() <= 0 {
					return false
				}
			}
			return true
		})
	}
	for deadline := time.Now().Add(30 * time.Second); !ready() && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		mygoRunOnMain(func() { window.Invalidate() })
	}

	mygoRunOnMain(func() {
		if len(a.items) < 2 {
			return
		}
		started = true
		attempt = a.startProbe(0, 1)
	})
	if !started {
		logf("probeclose", "the session needs two sources; it has %d", runOnMainValue(func() int { return len(a.items) }))
		mygo.App.Quit()
		return
	}

	// The comparison reads the session on the drawing thread and only then starts
	// ffmpeg, so the close has to wait for the pid; otherwise it would race the
	// decoder and prove nothing about a running one.
	deadline := time.Now().Add(30 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		mygoRunOnMain(func() { window.Invalidate() })
		pid = a.probePID()
	}
	check(pid != 0, "the decoder started before the close (pid %d)", pid)
	if pid != 0 {
		path := filepath.Join(dir, "decoder.pid")
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
			logf("probeclose", "could not write %s: %v", path, err)
		}
		logf("probeclose", "the decoder runs as pid %d; the window closes when %s says so", pid, filepath.Join(dir, "close")) // The process outside has to see the pid alive before the close, or the
		// evidence would again describe a decoder that may already be gone.
		closeFile := filepath.Join(dir, "close")
		waitUntil := time.Now().Add(30 * time.Second)
		for time.Now().Before(waitUntil) {
			if _, err := os.Stat(closeFile); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
			mygoRunOnMain(func() { window.Invalidate() })
		}
		if _, err := os.Stat(closeFile); err != nil {
			logf("probeclose", "FAIL: nothing asked the close to start")
		}
		logf("probeclose", "closing the window while decoder %d is still the comparison in flight", pid)
	}

	mygoRunOnMain(func() {
		a.release()
		closed = true
		after = a.probeAttempt
	})
	if !closed {
		logf("probeclose", "FAIL: the close never ran")
		mygo.App.Quit()
		return
	}
	time.Sleep(1500 * time.Millisecond)

	var running bool
	mygoRunOnMain(func() { running = a.probeRunning })
	check(!running, "the comparison is not marked as running after the close")
	check(after > attempt, "the close moved the attempt counter on (%d -> %d), so a late result is stale", attempt, after)
	check(closed, "the close returned without waiting for the decoder")

	// The pid is the session's record of the process, and the record has to follow
	// the process: once the decoder has ended, a reader must not be told about a
	// pid that no longer runs.
	left := pid
	for deadline := time.Now().Add(15 * time.Second); left != 0 && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		left = a.probePID()
	}
	check(left == 0, "the decoder record is empty once it ended (pid %d)", left)

	logf("probeclose", "done: %d check(s) failed", failed)
	mygoRunOnMain(func() { mygo.App.Quit() })
}
