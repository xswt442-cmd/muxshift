// P16.4c on a real window: the workbench shows the newest replay account, and
// only a committed one claims a settled position. The run drives three states —
// refused, committed, rolled back — and records what the dock says in each.
package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runReplayUI is P16.4c: what a replay may say about itself, on a window.
func runReplayUI(window *mygo.Window, a *appState, dir string) {
	go replayUIBody(window, a, dir)
}

func replayUIBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "replayui"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	say := func(where string) {
		for _, line := range a.noticeLines() {
			logf(scenario, "%s: %s", where, line)
		}
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
		name := "replayui-" + shortName(index)
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

	step(window, a, dir, scenario, "", "put both sources on the event clock", func(a *appState) {
		for index := 0; index < 2; index++ {
			it, err := a.at(index)
			if err != nil {
				logf(scenario, "%v", err)
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

	mygoRunOnMain(func() { _ = a.anchor(0) })
	mygoRunOnMain(func() { a.nudge(1, 0) })
	time.Sleep(time.Second)

	// 1. A target outside anything the buffers measured: the plan must refuse
	// and say so, and the dock must not describe it as a replay.
	if err := a.startSeekTxn(a.items, 10*time.Minute); err != nil {
		logf(scenario, "the refused request was reported at once: %v", err)
	}
	waitForNotice(a, func(report replayReport) bool { return report.Phase == replayRefused }, 10*time.Second)
	step(window, a, dir, scenario, "1-refused.png", "the dock after a refused request", func(a *appState) {
		say("refused")
		lines := strings.Join(a.noticeLines(), "\n")
		check(strings.Contains(lines, "refused"), "the dock does not say the request was refused: %q", lines)
		check(!strings.Contains(lines, "position settled"), "a refused request claims a settled position: %q", lines)
		a.showVideo(0)
	})

	// 2. A target inside the measured window: the account is published with the
	// positions each player reported, and still says the frame is unconfirmed.
	if err := a.startSeekTxn(a.items, 7*time.Second); err != nil {
		logf(scenario, "start the replay transaction: %v", err)
		return
	}
	waitForSeekTxn(a, 90*time.Second)
	waitForNotice(a, func(report replayReport) bool { return report.Phase == replayCommitted }, 10*time.Second)
	step(window, a, dir, scenario, "2-committed.png", "the dock after a committed replay", func(a *appState) {
		say("committed")
		lines := strings.Join(a.noticeLines(), "\n")
		check(strings.Contains(lines, "committed"), "the dock does not say the replay committed: %q", lines)
		check(strings.Contains(lines, "frame is not confirmed"), "the dock claims a confirmed frame: %q", lines)
		a.showVideo(0)
	})

	// 3. A commit that finds its second source replaced: nothing is installed,
	// and the account keeps its measurements without reporting success.
	a.beforeSeekCommit = func() {
		a.seekMu.Lock()
		a.items[1].generation++
		a.seekMu.Unlock()
	}
	if err := a.startSeekTxn(a.items, 7*time.Second); err != nil {
		logf(scenario, "start the rolled-back transaction: %v", err)
		return
	}
	// The hook has to stay in place until the preparation reaches it; clearing
	// it right after the start would race the goroutine and let the commit
	// through.
	waitForSeekTxn(a, 90*time.Second)
	a.beforeSeekCommit = nil
	waitForNotice(a, func(report replayReport) bool { return report.Phase == replayRolledBack }, 10*time.Second)
	step(window, a, dir, scenario, "3-rolledback.png", "the dock after a rolled-back replay", func(a *appState) {
		say("rolled back")
		lines := strings.Join(a.noticeLines(), "\n")
		check(strings.Contains(lines, "rolled back"), "the dock does not say the replay rolled back: %q", lines)
		check(!strings.Contains(lines, "position settled"), "a rolled-back replay claims a settled position: %q", lines)
		a.showVideo(0)
	})

	logf(scenario, "checks failed: %d", failures)
}

// waitForNotice waits until the newest account satisfies want. A missing account
// reads as "not yet", so a phase that never arrives fails on the caller's
// timeout rather than on a guess.
func waitForNotice(a *appState, want func(replayReport) bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if report, ok := a.lastReport(); ok && want(report) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForSeekTxn waits until no transaction is registered any more.
func waitForSeekTxn(a *appState, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.seekMu.Lock()
		running := a.seek
		a.seekMu.Unlock()
		if running == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
