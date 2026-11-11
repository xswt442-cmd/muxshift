// Checks for opening, replacing and retrying a player.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// runSingle plays one source, pauses it, resumes it, reloads it, mutes it, and
// closes it, capturing the window at each change.
func runSingle(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "single", "1-playing.png", "playing", nil)
		step(window, a, dir, "single", "2-paused.png", "paused", func(a *appState) {
			a.first().player.Pause()
		})
		wait(2 * time.Second)
		step(window, a, dir, "single", "3-still-paused.png", "still paused", nil)
		step(window, a, dir, "single", "", "resumed", func(a *appState) {
			a.first().player.Play()
		})
		wait(700 * time.Millisecond)
		step(window, a, dir, "single", "4-resumed.png", "resumed after 0.7s", nil)
		wait(700 * time.Millisecond)
		step(window, a, dir, "single", "", "reloaded", func(a *appState) {
			a.reopenPlayer(a.first())
		})
		wait(1500 * time.Millisecond)
		step(window, a, dir, "single", "5-reloaded.png", "reloaded after 1.5s", nil)
		wait(2500 * time.Millisecond)
		step(window, a, dir, "single", "5b-reloaded-later.png", "reloaded after 4s", nil)
		wait(1 * time.Second)
		step(window, a, dir, "single", "", "muted", func(a *appState) { a.first().player.SetMuted(true) })
		wait(1 * time.Second)
		step(window, a, dir, "single", "", "unmuted", func(a *appState) { a.first().player.SetMuted(false) })
		wait(1 * time.Second)
		step(window, a, dir, "single", "", "closed", func(a *appState) { a.close(a.first()) })
		wait(1 * time.Second)
		step(window, a, dir, "single", "6-after-close.png", "after close", nil)
		mygoRunOnMain(func() { mygo.App.Quit() })
	}()
}

// runReopen closes the only source and opens it again, giving the second
// player six seconds to show a picture, which is where a reopen behaves
// differently from a first open in this MyGo build.
func runReopen(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "reopen", "1-first-open.png", "first open after 3s", nil)
		step(window, a, dir, "reopen", "", "reopened with the old player still drawn", func(a *appState) {
			a.reopenPlayer(a.first())
		})
		wait(6 * time.Second)
		step(window, a, dir, "reopen", "2-reopened.png", "reopened after 6s", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runReopenStrict closes the only player first and opens the address again,
// which is the order this MyGo build cannot serve; it records the failure that
// reopenPlayer avoids.
func runReopenStrict(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "reopen-strict", "1-first-open.png", "first open after 3s", nil)
		step(window, a, dir, "reopen-strict", "", "closed", func(a *appState) { a.close(a.first()) })
		wait(1 * time.Second)
		step(window, a, dir, "reopen-strict", "", "reopened", func(a *appState) { a.reopenPlayer(a.first()) })
		wait(6 * time.Second)
		step(window, a, dir, "reopen-strict", "2-reopened.png", "reopened after 6s", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runHandoverFail reopens a working source onto an address that never answers,
// so the replacement never gets a picture. The handover has to keep the player
// that still works, close the replacement, and say why in the source line; then
// reopening the real address has to work again.
func runHandoverFail(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "handover-fail", "1-before.png", "playing before the failed reopen", nil)
		step(window, a, dir, "handover-fail", "", "reopened onto an address that never answers", func(a *appState) {
			it := a.first()
			it.uri = "http://10.255.255.1/hls/index.m3u8"
			a.reopenPlayer(it)
		})
		wait(handoverWait + 2*time.Second)
		step(window, a, dir, "handover-fail", "2-kept-previous.png", "after the handover gave up", nil)
		step(window, a, dir, "handover-fail", "", "reopened onto the working address again", func(a *appState) {
			it := a.first()
			it.uri = a.session.Sources()[0].URI
			a.reopenPlayer(it)
		})
		wait(6 * time.Second)
		step(window, a, dir, "handover-fail", "3-recovered.png", "after reopening the working address", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runReloadRapid presses Reload several times faster than the handover can
// finish. Every task has to stay with the player it started for, so the last
// reload owns the source at the end, no player is closed twice, and the picture
// still arrives.
func runReloadRapid(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "reload-rapid", "1-before.png", "playing before the burst", nil)
		for i := 0; i < 5; i++ {
			step(window, a, dir, "reload-rapid", "", "reload "+itoa(i+1)+" of 5", func(a *appState) {
				a.reopenPlayer(a.first())
			})
			wait(150 * time.Millisecond)
		}
		wait(8 * time.Second)
		step(window, a, dir, "reload-rapid", "2-after-burst.png", "after the burst", func(a *appState) {
			logf("reload-rapid", "live players=%s previous=%s handover=%s",
				itoa(a.livePlayers()), boolString(a.first().previous != nil), boolString(a.first().handover != nil))
		})
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runPair plays two sources at once: the first shows its picture muted, the
// second keeps its sound with its video out of the view. It records whether the
// hidden player still moves.
func runPair(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "pair", "", "both playing", nil)
		step(window, a, dir, "pair", "1-a-muted-b-shown.png", "a muted", func(a *appState) {
			a.items[0].player.SetMuted(true)
		})
		wait(1 * time.Second)
		step(window, a, dir, "pair", "", "a muted b hidden", func(a *appState) {
			// Stop drawing b's video: does its sound keep playing?
			a.items[1].shown = false
		})
		wait(2 * time.Second)
		step(window, a, dir, "pair", "2-b-hidden.png", "b hidden for 2s", nil)
		wait(2 * time.Second)
		step(window, a, dir, "pair", "3-b-hidden-4s.png", "b hidden for 4s", nil)
		step(window, a, dir, "pair", "", "b shown again", func(a *appState) {
			a.items[1].shown = true
		})
		wait(1 * time.Second)
		step(window, a, dir, "pair", "4-b-shown-again.png", "b shown again", nil)
		step(window, a, dir, "pair", "", "a unmuted", func(a *appState) {
			a.items[0].player.SetMuted(false)
		})
		wait(1 * time.Second)
		step(window, a, dir, "pair", "5-both-audible.png", "both audible", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runReplaySwap swaps the picture of the session to a recording that was
// registered as an ordinary source. It answers one question: with the player
// created on the drawing thread, can a recording be shown in the session's own
// window, which is where a person would want to watch it.
func runReplaySwap(window *mygo.Window, a *appState, dir string) {
	go func() {
		time.Sleep(2 * time.Second)
		step(window, a, dir, "replay-swap", "", "open with the live picture", nil)
		index := len(a.items) - 1
		step(window, a, dir, "replay-swap", "1-live.png", "show the recording in the session window", func(a *appState) {
			if err := a.showVideo(index); err != nil {
				logf("replay-swap", "%v", err)
				return
			}
			it, err := a.at(index)
			if err != nil {
				logf("replay-swap", "%v", err)
				return
			}
			logGroup("replay-swap", "scene", nil)
			logf("replay-swap", "showing %s (open=%v)", shortURI(it.uri), errorString(it.openErr))
		})
		it, err := a.at(index)
		if err != nil {
			logf("replay-swap", "%v", err)
			mygoRunOnMain(func() { mygo.App.Quit() })
			return
		}
		if !waitPlayerReady("replay-swap", rawPlayer(it.player), 30*time.Second) {
			logf("replay-swap", "the recording did not start within 30s")
		}
		time.Sleep(time.Second)
		step(window, a, dir, "replay-swap", "2-recording.png", "capture the recording picture", nil)
		step(window, a, dir, "replay-swap", "", "jump into the recording", func(a *appState) {
			target := it.player.Position() + 5*time.Second
			it.player.Seek(target)
			logf("replay-swap", "seek to %s (asked %s)", it.player.Position(), target)
		})
		time.Sleep(2 * time.Second)
		step(window, a, dir, "replay-swap", "3-recording-later.png", "capture after the jump", nil)
		mygoRunOnMain(func() { mygo.App.Quit() })
	}()
}

// runSwitch shows one source, then the other, then the first again while both
// keep playing, and closes each one in turn.
func runSwitch(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "switch", "1-a-shown.png", "a shown", nil)
		step(window, a, dir, "switch", "", "switch to b", func(a *appState) {
			a.items[0].shown = false
			a.items[1].shown = true
		})
		wait(2 * time.Second)
		step(window, a, dir, "switch", "2-b-shown.png", "b shown for 2s", nil)
		step(window, a, dir, "switch", "", "close a", func(a *appState) { a.close(a.items[0]) })
		wait(1 * time.Second)
		step(window, a, dir, "switch", "", "switch back to a (closed)", func(a *appState) {
			a.reopenPlayer(a.items[0])
			a.items[0].shown = true
			a.items[1].shown = false
		})
		wait(2 * time.Second)
		step(window, a, dir, "switch", "3-a-shown-again.png", "a shown again", nil)
		step(window, a, dir, "switch", "", "close b", func(a *appState) { a.close(a.items[1]) })
		wait(1 * time.Second)
		step(window, a, dir, "switch", "4-b-closed.png", "b closed", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runRapid opens and closes the source several times in a row, the way a view
// switched by a person would, and records every pass.
func runRapid(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		for pass := 1; pass <= 5; pass++ {
			step(window, a, dir, "rapid", "", "reopen "+itoa(pass), func(a *appState) { a.reopenPlayer(a.first()) })
			wait(400 * time.Millisecond)
			step(window, a, dir, "rapid", "", "close "+itoa(pass), func(a *appState) { a.close(a.first()) })
			wait(200 * time.Millisecond)
		}
		step(window, a, dir, "rapid", "1-after-5-passes.png", "after five passes", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runRetry closes the source, opens an address that fails, and opens the good
// address again, recording the error the view shows and the recovery.
func runRetry(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "retry", "1-playing.png", "playing", nil)
		step(window, a, dir, "retry", "", "open a missing address", func(a *appState) {
			a.first().uri = missingAddress(a.first().uri)
			a.reopenPlayer(a.first())
		})
		wait(3 * time.Second)
		step(window, a, dir, "retry", "2-failed.png", "after the failure", nil)
		step(window, a, dir, "retry", "", "open the good address again", func(a *appState) {
			a.first().uri = goodAddress(a.first().uri)
			a.reopenPlayer(a.first())
		})
		wait(4 * time.Second)
		step(window, a, dir, "retry", "3-recovered.png", "after the retry", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runReplayProbe opens a recorded playlist while the live players are paused,
// then resumes them, and reports the replay player at each step: it isolates
// whether a replay player added mid-run starts on its own.
func runReplayProbe(window *mygo.Window, a *appState, dir string) {
	const scenario = "replayprobe"
	url := replayProbeURL
	if url == "" {
		url = "http://127.0.0.1:8791/replay-from1/index.m3u8"
	}
	step(window, a, dir, scenario, "", "live sources at start", nil)
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "", "pause the live output", func(a *appState) {
		logGroup(scenario, "pause", a.pauseAll())
	})
	time.Sleep(2 * time.Second)
	index := -1
	step(window, a, dir, scenario, "", "add and show the recorded source while paused", func(a *appState) {
		it, err := a.add(url)
		if err != nil {
			logf(scenario, "open replay: %v", err)
			return
		}
		a.items = append(a.items, it)
		index = len(a.items) - 1
		if err := a.showVideo(index); err != nil {
			logf(scenario, "show replay: %v", err)
		}
	})
	if index < 0 {
		return
	}
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "", "recorded source three seconds after showing while paused", nil)
	// Sample for a while: a recorded source added mid-run has been seen to take
	// far longer than a startup source before it presents its first picture.
	for i := 1; i <= 8; i++ {
		time.Sleep(5 * time.Second)
		step(window, a, dir, scenario, "", fmt.Sprintf("recorded source %d0 seconds after showing", i*5), nil)
	}
	step(window, a, dir, scenario, "", "resume the live group", func(a *appState) {
		logGroup(scenario, "play", a.playAll())
	})
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "1-replay-after-resume.png", "recorded source after the live group resumed", nil)
	step(window, a, dir, scenario, "", "seek inside the recorded source", func(a *appState) {
		it, err := a.at(index)
		if err != nil || it.player == nil {
			logf(scenario, "seek: %v", err)
			return
		}
		it.player.Seek(4 * time.Second)
	})
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "2-replay-after-seek.png", "recorded source after a four second seek", nil)
	mygoRunOnMain(func() { mygo.App.Quit() })
}

// syncAt and syncOffset are the syncmap measurement points, set by flags.
var (
	syncAt     time.Duration
	syncOffset time.Duration
)
