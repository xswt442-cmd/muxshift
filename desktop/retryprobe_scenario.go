//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// The retry probe is investigation-only: it drives the same cut recording
// through the entry in two orders and records what each transaction saw. It
// changes no production behaviour and is not a passing target — the numbers it
// prints are the evidence for REPLAY-RETRY-AFTER-ROLLBACK.
//
//	-scenario retryprobe -retry-probe both     : direct 1s, then boundary, then 1s
//	-scenario retryprobe -retry-probe direct   : only the direct 1s request
//	-scenario retryprobe -retry-probe after    : only boundary, then 1s
//
// The stage can be paused before a phase with -retry-probe-pause. The recording
// is cut once from a mid-join live buffer, so every phase reads the same file.
func runRetryProbe(window *mygo.Window, a *appState, dir string) {
	go retryProbeBody(window, a, dir, "retryprobe")
}

func retryProbeBody(window *mygo.Window, a *appState, dir, scenario string) {
	if len(a.items) == 0 {
		logf(scenario, "the scenario needs a source")
		return
	}
	flight, finish := attachMidFlight(a, dir, scenario)
	if flight == nil {
		return
	}
	defer finish()

	if err := a.playRecording(flight.it, flight.runner, 0); err != nil {
		logf(scenario, "play the recording cut at 0s: %v", err)
		return
	}
	if !waitRecording(a, 0, 0, 40*time.Second) {
		logf(scenario, "the recording cut at 0s never showed a picture")
		return
	}
	time.Sleep(2 * time.Second)

	step(window, a, dir, scenario, "0-window.png", "confirm the source on the event clock", func(a *appState) {
		for _, track := range []core.TrackID{flight.it.video, flight.it.audio} {
			if track == "" {
				continue
			}
			if err := a.sync.timeline.SetShift(track, 0); err != nil {
				logf(scenario, "align %s: %v", track, err)
			}
		}
		a.showVideo(0)
		a.apply()
	})
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	if anchorErr != nil {
		logf(scenario, "anchor the source: %v", anchorErr)
		return
	}
	var nudgeNote string
	mygoRunOnMain(func() { nudgeNote, _ = a.nudge(0, 10*time.Millisecond) })
	logf(scenario, "the source was nudged by 10ms: %s", nudgeNote)
	time.Sleep(time.Second)

	// Record what each replacement player actually opened, independent of the
	// engine's opinion: the file, its length, and whether ffmpeg can decode the
	// first frame at the offsets the phases use. A decodable first frame is not
	// a presented frame; the two are logged separately.
	base := a.openSeek
	opened := 0
	a.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened++
		info, statErr := os.Stat(recording.Path)
		size := int64(-1)
		if statErr == nil {
			size = info.Size()
		}
		logf(scenario, "openSeekPlayer #%d opened %s (%d bytes)", opened, filepath.Base(recording.Path), size)
		for _, offset := range []time.Duration{0, time.Second, 4 * time.Second} {
			logf(scenario, "  ffmpeg can decode a frame at %s: %s", offset, probeFrame(recording.Path, offset))
		}
		if base != nil {
			return base(recording)
		}
		player, openErr := a.openReplayFile(recording.Path)
		return player, openErr
	}

	phase := func(name string, text string, pause bool) {
		var start, end time.Duration
		mygoRunOnMain(func() {
			var windowErr error
			start, end, windowErr = a.replayWindowForEntry()
			if windowErr != nil {
				logf(scenario, "%s: the entry has no window: %v", name, windowErr)
			}
		})
		var pauseNote string
		if pause {
			step(window, a, dir, scenario, name+"-paused.png", "pause the stage before asking", func(a *appState) {
				if len(a.items) > 0 && a.items[0].player != nil {
					a.items[0].player.Pause()
					pauseNote = fmt.Sprintf("stage paused at %s", a.items[0].player.Position())
				}
			})
			logf(scenario, "%s: %s", name, pauseNote)
		}
		before := a.items[0].generation
		asked := time.Now()
		var note string
		mygoRunOnMain(func() {
			a.seekText = text
			note = a.requestReplaySeek()
		})
		logf(scenario, "%s: window %s..%s, request %q said: %s", name, start, end, text, note)
		if !waitSeekDone(a, 120*time.Second) {
			logf(scenario, "%s: the transaction did not finish within 120s", name)
			return
		}
		account, ok := a.lastReport()
		elapsed := time.Since(asked).Round(100 * time.Millisecond)
		logf(scenario, "%s: after %s the account says: %s (published=%v)", name, elapsed, account.Line(), ok)
		logf(scenario, "%s: generation %d → %d; stage playing=%v position=%s", name, before, a.items[0].generation,
			a.items[0].player != nil && a.items[0].player.Playing(), positionOf(a, 0))
		step(window, a, dir, scenario, name+".png", "the stage after "+name, func(a *appState) {
			a.showVideo(0)
		})
	}

	mode := strings.ToLower(strings.TrimSpace(retryProbeMode))
	pause := retryProbePause
	logf(scenario, "mode=%q pause-stage=%v", mode, pause)
	switch mode {
	case "direct":
		phase("direct-1s", retryProbeRetry, pause)
	case "after":
		phase("boundary", retryProbeBoundary, pause)
		phase("retry-1s", retryProbeRetry, pause)
	case "forcedraw":
		var note0 string
		step(window, a, dir, scenario, "0-paused.png", "pause the stage before asking", func(a *appState) {
			if len(a.items) > 0 && a.items[0].player != nil {
				a.items[0].player.Pause()
				note0 = fmt.Sprintf("stage paused at %s", a.items[0].player.Position())
			}
		})
		logf(scenario, "%s", note0)
		stop := make(chan struct{})
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					mygoRunOnMain(func() {
						if a.window != nil {
							a.window.Invalidate()
						}
					})
				}
			}
		}()
		var note string
		mygoRunOnMain(func() {
			a.seekText = retryProbeRetry
			note = a.requestReplaySeek()
		})
		logf(scenario, "forcedraw: request said: %s (invalidating every 100ms while the stage stays paused)", note)
		if !waitSeekDone(a, 60*time.Second) {
			logf(scenario, "forcedraw: the transaction did not finish")
			close(stop)
			return
		}
		close(stop)
		time.Sleep(500 * time.Millisecond)
		account, _ := a.lastReport()
		logf(scenario, "forcedraw: the account says: %s", account.Line())
		logf(scenario, "forcedraw: generation %d, stage playing=%v position=%s", a.items[0].generation,
			a.items[0].player != nil && a.items[0].player.Playing(), positionOf(a, 0))
	case "resume":
		var pauseNote string
		step(window, a, dir, scenario, "0-paused.png", "pause the stage before asking", func(a *appState) {
			if len(a.items) > 0 && a.items[0].player != nil {
				a.items[0].player.Pause()
				pauseNote = fmt.Sprintf("stage paused at %s", a.items[0].player.Position())
			}
		})
		logf(scenario, "%s", pauseNote)
		var note string
		mygoRunOnMain(func() {
			a.seekText = retryProbeRetry
			note = a.requestReplaySeek()
		})
		logf(scenario, "resume: request said: %s", note)
		time.Sleep(4 * time.Second)
		step(window, a, dir, scenario, "1-resumed.png", "resume the stage while the replacement is waiting", func(a *appState) {
			if len(a.items) > 0 && a.items[0].player != nil {
				a.items[0].player.Play()
				pauseNote = fmt.Sprintf("stage resumed at %s", a.items[0].player.Position())
			}
		})
		logf(scenario, "resume: %s", pauseNote)
		if !waitSeekDone(a, 120*time.Second) {
			logf(scenario, "resume: the transaction did not finish")
			return
		}
		time.Sleep(time.Second)
		account, _ := a.lastReport()
		logf(scenario, "resume: the account says: %s", account.Line())
		logf(scenario, "resume: generation %d, stage playing=%v position=%s", a.items[0].generation,
			a.items[0].player != nil && a.items[0].player.Playing(), positionOf(a, 0))
	case "paused":
		phase("paused-direct", retryProbeRetry, true)
	case "switch":
		if len(a.items) < 2 {
			logf(scenario, "the switch probe needs two sources")
			return
		}
		logf(scenario, "sleeping 15s so the stage reaches the end of the recording")
		time.Sleep(15 * time.Second)
		logf(scenario, "before switching: source 1 playing=%v position=%s; source 2 playing=%v position=%s",
			a.items[0].player != nil && a.items[0].player.Playing(), positionOf(a, 0),
			a.items[1].player != nil && a.items[1].player.Playing(), positionOf(a, 1))
		step(window, a, dir, scenario, "1-ended.png", "the ended recording before any switch", func(a *appState) {
			a.showVideo(0)
		})
		step(window, a, dir, scenario, "2-other-source.png", "show the second source after the first recording ended", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				logf(scenario, "show the second source: %v", err)
			}
		})
		time.Sleep(2 * time.Second)
		logf(scenario, "with the second source shown: source 1 ready=%v playing=%v position=%s; source 2 ready=%v playing=%v position=%s",
			a.items[0].player != nil && a.items[0].player.Ready(), a.items[0].player != nil && a.items[0].player.Playing(), positionOf(a, 0),
			a.items[1].player != nil && a.items[1].player.Ready(), a.items[1].player != nil && a.items[1].player.Playing(), positionOf(a, 1))
		step(window, a, dir, scenario, "3-back.png", "go back to the ended recording", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf(scenario, "show the recording again: %v", err)
			}
		})
		time.Sleep(2 * time.Second)
		logf(scenario, "back on the recording: source 1 ready=%v playing=%v position=%s; source 2 ready=%v playing=%v position=%s",
			a.items[0].player != nil && a.items[0].player.Ready(), a.items[0].player != nil && a.items[0].player.Playing(), positionOf(a, 0),
			a.items[1].player != nil && a.items[1].player.Ready(), a.items[1].player != nil && a.items[1].player.Playing(), positionOf(a, 1))
		phase("after-switch", retryProbeRetry, pause)
	case "replay-eof":
		logf(scenario, "sleeping 15s so the stage reaches the end, then replaying the recording")
		time.Sleep(15 * time.Second)
		if err := a.playRecording(flight.it, flight.runner, 0); err != nil {
			logf(scenario, "replay the recording: %v", err)
			return
		}
		if !waitRecording(a, 0, 0, 40*time.Second) {
			logf(scenario, "the replayed recording never showed a picture")
			return
		}
		time.Sleep(2 * time.Second)
		phase("after-replay", retryProbeRetry, pause)
	case "long":
		logf(scenario, "sleeping 20s so the recording is long enough to outlast a failed settle")
		time.Sleep(20 * time.Second)
		phase("long-boundary", retryProbeBoundary, pause)
		phase("long-retry", retryProbeRetry, pause)
	case "eof":
		logf(scenario, "sleeping 15s so the stage reaches the end of the recording")
		time.Sleep(15 * time.Second)
		phase("eof-direct", retryProbeRetry, pause)
	case "late":
		logf(scenario, "sleeping 20s so the buffer grows before the first request")
		time.Sleep(20 * time.Second)
		phase("late-direct", retryProbeRetry, pause)
	case "repeat":
		phase("direct-1", retryProbeRetry, pause)
		phase("direct-2", retryProbeRetry, pause)
		phase("direct-3", retryProbeRetry, pause)
	case "both":
		phase("direct-1s", retryProbeRetry, pause)
		phase("boundary", retryProbeBoundary, pause)
		phase("retry-1s", retryProbeRetry, pause)
	default:
		logf(scenario, "unknown -retry-probe %q", mode)
	}

	open, given := a.leaseStats()
	logf(scenario, "leases open=%d given=%d, players=%d", open, given, openPlayers(a))
	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, _ = a.leaseStats()
	logf(scenario, "after closing: leases open=%d players=%d", open, openPlayers(a))
}

// probeFrame asks ffprobe what it can decode at an offset, which is a different
// question from what the player has on screen.
func probeFrame(path string, offset time.Duration) string {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return "ffprobe is not on PATH"
	}
	cmd := exec.Command(ffprobe, "-v", "error",
		"-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64),
		"-i", path, "-frames:v", "1", "-show_entries", "frame=pts_time",
		"-of", "csv=p=0")
	out, runErr := cmd.Output()
	text := strings.TrimSpace(string(out))
	if runErr != nil {
		return "decode failed: " + runErr.Error()
	}
	if text == "" {
		return "no video frame decoded"
	}
	return "pts " + text
}
