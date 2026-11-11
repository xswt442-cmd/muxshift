package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// runFaults replays real playlist faults instead of synthetic segment objects:
// the first source is missing two segments on the server (two ranges, one epoch),
// the second declares a real EXT-X-DISCONTINUITY (two ranges, two epochs). Both
// have to lose the same thing — an anchor that would bridge them.
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
