// P16.4e, first slice: what happens when a player is asked for 0s inside a
// recording cut at a segment boundary. This scenario only measures; it does not
// change the settle rules. It asks the same player for a ladder of positions
// around the 766ms it reports, at two cut points, and records what the player
// answers over time, so "refused", "ignored" and "advanced past" can be told
// apart instead of guessed.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

func runSeekEdge(window *mygo.Window, a *appState, dir string) {
	go seekEdgeBody(window, a, dir)
}

func seekEdgeBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "seekedge"
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		name := "seekedge-" + shortName(index)
		runner, err := startDVR(it.uri, dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, it.source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(it.uri, runner)
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for _, runner := range runners {
				_ = runner.Close()
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

	cuts := []time.Duration{0, 4 * time.Second}
	asks := []time.Duration{0, 300 * time.Millisecond,
		766 * time.Millisecond, 900 * time.Millisecond, 1200 * time.Millisecond}

	for index, runner := range runners {
		it, err := a.at(index)
		if err != nil {
			logf(scenario, "%v", err)
			continue
		}
		for _, cut := range cuts {
			for _, ask := range asks {
				// Every request gets a player of its own: a seek observed on a
				// player that has already refused one would mix the two, and
				// the point here is what a fresh player does with one ask.
				if err := a.playRecording(it, runner, cut); err != nil {
					logf(scenario, "source %d cut %s: open: %v", index+1, cut, err)
					break
				}
				if !waitRecording(a, index, cut, 40*time.Second) {
					logf(scenario, "source %d cut %s: never showed a picture", index+1, cut)
					break
				}
				time.Sleep(600 * time.Millisecond)
				rec := runOnMainValue(func() *dvr.Recording { return it.replay })
				before := runOnMainValue(func() time.Duration { return it.player.Position() })
				runOnMainValue(func() int {
					it.player.Pause()
					it.player.Seek(ask)
					return 0
				})
				var trail []string
				for step := 0; step < 8; step++ {
					time.Sleep(200 * time.Millisecond)
					frame := runOnMainValue(func() edgeSample {
						return edgeSample{position: it.player.Position(), ready: it.player.Ready(), buffering: it.player.Buffering(), err: it.player.Err()}
					})
					trail = append(trail, fmt.Sprintf("%dms=%s%s",
						(step+1)*200, sceneClock(frame.position), edgeFlag(frame)))
				}
				name := "no file"
				if rec != nil && ask == asks[0] && dir != "" {
					kept := filepath.Join(dir, fmt.Sprintf("cut-%dms.ts", cut.Milliseconds()))
					if err := copyFile(rec.Path, kept); err != nil {
						logf(scenario, "source %d cut %s: keep the file: %v", index+1, cut, err)
					} else {
						logf(scenario, "source %d cut %s: kept %s", index+1, cut, kept)
					}
				}
				if rec != nil {
					name = fmt.Sprintf("%d bytes, %s", rec.Size, probeEdge(rec.Path))
				}
				logf(scenario, "source %d cut %s: opened at %s, asked %s → %s | %s",
					index+1, cut, sceneClock(before), ask, strings.Join(trail, " "), name)
				// Then play from the same ask for a second: a refused seek
				// shows up as movement starting from somewhere else.
				runOnMainValue(func() int {
					it.player.Play()
					return 0
				})
				time.Sleep(time.Second)
				played := runOnMainValue(func() time.Duration { return it.player.Position() })
				logf(scenario, "source %d cut %s: asked %s then played 1s → %s", index+1, cut, ask, sceneClock(played))
			}
			// Which of the two happened to a 0s request: ignored, or carried
			// out and then clamped to the earliest position the player can
			// report? Seeking to 3s first, then to 0s, tells them apart: an
			// ignored request leaves the player at 3s, a clamp moves it down
			// to the floor.
			if err := a.playRecording(it, runner, cut); err == nil && waitRecording(a, index, cut, 40*time.Second) {
				time.Sleep(600 * time.Millisecond)
				runOnMainValue(func() int {
					it.player.Pause()
					it.player.Seek(3 * time.Second)
					return 0
				})
				time.Sleep(1200 * time.Millisecond)
				afterThree := runOnMainValue(func() time.Duration { return it.player.Position() })
				runOnMainValue(func() int {
					it.player.Seek(0)
					return 0
				})
				time.Sleep(1500 * time.Millisecond)
				afterZero := runOnMainValue(func() time.Duration { return it.player.Position() })
				logf(scenario, "source %d cut %s: seek 3s → %s, then seek 0s → %s",
					index+1, cut, sceneClock(afterThree), sceneClock(afterZero))
			}
		}
	}
	if dir != "" {
		step(window, a, dir, scenario, "1-edge.png", "the stage while the edge is measured", func(a *appState) {
			a.showVideo(0)
		})
	}
	logf(scenario, "done")
}

// edgeSample is what the player reports at one instant while a seek is being
// observed; kept small so the trail can say whether a still position is a
// refusal, a buffering stall or a player that has not become ready yet.
type edgeSample struct {
	position  time.Duration
	ready     bool
	buffering bool
	err       error
}

// flag names what the player was doing, so a trail of equal positions is not
// mistaken for a frozen player.
func edgeFlag(frame edgeSample) string {
	switch {
	case frame.err != nil:
		return " err=" + frame.err.Error()
	case !frame.ready:
		return " not-ready"
	case frame.buffering:
		return " buffering"
	}
	return ""
}

// probeEdge asks ffprobe what the cut file says about its own start: the first
// stream timestamps and the first frames' presentation times.
func probeEdge(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=start_time,duration:frame=pts_time,best_effort_timestamp_time",
		"-read_intervals", "%+#3", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		return "ffprobe: " + err.Error()
	}
	trimmed := strings.TrimSpace(strings.ReplaceAll(string(out), "\n", " | "))
	return trimmed
}
