// Checks for the replay buffer, its cuts and its leases.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// runDVR checks the replay window: the buffer keeps filling while the output is
// paused, two sources agree on the part of history both can play, and a player
// opens that part through the loopback endpoint and jumps inside it.
func runDVR(window *mygo.Window, a *appState, dir string) {
	const scenario = "dvr"
	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	// The last card is the spare the recording takes over: a video element
	// added while the window draws freezes its repaint, so the card exists from
	// startup and the replay reopens it in place.
	liveCount := len(a.items) - 1
	uris := make([]string, liveCount)
	for i := 0; i < liveCount; i++ {
		it := a.items[i]
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address; the window needs live playlists", i+1)
			return
		}
		if it.video == "" {
			logf(scenario, "source %d did not open: %s", i+1, errorString(it.openErr))
			return
		}
		uris[i] = it.uri
	}
	spare := a.items[liveCount]
	if spare.player == nil {
		logf(scenario, "the spare card did not open: %s", errorString(spare.openErr))
		return
	}

	sources := make([]replaySource, 0, liveCount)
	bufferDirs := make([]string, 0, liveCount)
	for index := 0; index < liveCount; index++ {
		bufferDir := a.dvrDirFor(fmt.Sprintf("source-%d", index+1))
		recordingsDir := a.dvrRecordingsDirFor(fmt.Sprintf("source-%d", index+1))
		runner, err := startDVR(uris[index], dvrFromMedia(bufferDir, recordingsDir, dvrRetention, dvrMaxBytes, dvrRecordingBytes, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open replay buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		sources = append(sources, replaySource{index: index, runner: runner})
		bufferDirs = append(bufferDirs, bufferDir)
	}
	defer func() {
		// The buffer owns its directory: closing it must leave nothing behind.
		mygoRunOnMain(func() {
			for i, source := range sources {
				if err := source.runner.Close(); err != nil {
					logf(scenario, "source %d: close buffer: %v", i+1, err)
				}
				if _, err := os.Stat(bufferDirs[i]); err == nil {
					logf(scenario, "source %d: the cache directory survived the close", i+1)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	// Wait for the pollers to collect a few segments of each stream.
	time.Sleep(8 * time.Second)
	for i, source := range sources {
		logf(scenario, "buffered at %s: %s", shortURI(uris[i]), source.runner.state())
	}

	// The second stream is the same content two seconds later, so it needs a
	// -2s offset to sit on the first one's clock.
	step(window, a, dir, scenario, "1-live.png", "both live sources playing", func(a *appState) {
		for _, source := range sources {
			it, err := a.at(source.index)
			if err != nil {
				logf(scenario, "%v", err)
				continue
			}
			offset := time.Duration(0)
			if source.index == 1 {
				offset = -2000 * time.Millisecond
			}
			for _, track := range []core.TrackID{it.video, it.audio} {
				if track == "" {
					continue
				}
				if err := a.sync.timeline.SetShift(track, offset); err != nil {
					logf(scenario, "align %s: %v", track, err)
				}
			}
		}
		a.apply()
	})

	// Pausing the output must not pause the input: the window only helps if it
	// keeps growing while the picture stands still. Both sources join the scene
	// first, so the sync group that pause acts on covers both of them.
	step(window, a, dir, scenario, "2-paused.png", "pause the output", func(a *appState) {
		if err := a.showVideo(0); err != nil {
			logf(scenario, "show source 1: %v", err)
		}
		if err := a.hearAudio(1, 1); err != nil {
			logf(scenario, "hear source 2: %v", err)
		}
		logGroup(scenario, "pause", a.pauseAll())
	})
	for i, source := range sources {
		logf(scenario, "while paused, at %s: %s", shortURI(uris[i]), source.runner.state())
	}
	time.Sleep(6 * time.Second)
	for i, source := range sources {
		logf(scenario, "six seconds paused, at %s: %s", shortURI(uris[i]), source.runner.state())
	}

	var start, end time.Duration
	var windowErr error
	mygoRunOnMain(func() {
		start, end, windowErr = a.commonReplay(sources)
	})
	if windowErr != nil {
		logf(scenario, "common window: %v", windowErr)
	} else {
		// commonReplay converts each source's buffer window through its measured
		// anchor and then subtracts the confirmed offsets, but the numbers it
		// returns are container timestamps, not a seekable range: P16.4 may not
		// use them until the player clock is verified across segments.
		logf(scenario, "common window in container timestamps: %s..%s", start, end)
	}

	// The buffer proved it keeps growing while the output is paused, and the
	// recording now takes the picture in the session's own window: a player
	// created on the drawing thread can be shown at any time. The runs of this
	// scenario before players moved to the drawing thread measured "never ready".
	step(window, a, dir, scenario, "", "resume the group before opening the recording", func(a *appState) {
		logGroup(scenario, "resume", a.playAll())
	})
	time.Sleep(2 * time.Second)

	replayIndex := -1
	step(window, a, dir, scenario, "", "open the recording in the session window", func(a *appState) {
		source := sources[0]
		it, err := a.at(source.index)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		shift, ok := a.sync.timeline.Shift(it.audio)
		if !ok {
			shift, _ = a.sync.timeline.Shift(it.video)
		}
		media := start + shift.Offset
		recording, err := source.runner.replay(media)
		if err != nil {
			logf(scenario, "source 1: replay from %s: %v", media, err)
			return
		}
		path := recording.Path
		// The contract says where the file really starts: a request inside a
		// segment begins at that segment's first frame, not at the request.
		logf(scenario, "source 1: %s", recording)
		// The card that will hold the recording exists from startup: a video
		// element added while the window is drawing freezes its repaint, and a
		// reopened player keeps the element where it already is.
		replayed := a.items[len(a.items)-1]
		if replayed.player == nil {
			logf(scenario, "source %d did not open, so there is no card to replay in", len(a.items))
			return
		}
		replayed.uri = path
		a.reopenPlayer(replayed)
		replayIndex = len(a.items) - 1
		if err := a.showVideo(replayIndex); err != nil {
			logf(scenario, "show the recording: %v", err)
		}
		logf(scenario, "source 1: replay %s from %s in the session window", shortURI(path), media)
		// The recording is kept beside the run so a later run can open it on its
		// own; the buffer removes its own directory when it closes.
		kept := filepath.Join(dir, "replay-kept.ts")
		if err := copyFile(path, kept); err != nil {
			logf(scenario, "keep the recording: %v", err)
		} else {
			logf(scenario, "kept the recording at %s", kept)
		}
	})
	if replayIndex < 0 {
		return
	}
	replayed, err := a.at(replayIndex)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !waitPlayerReady(scenario, rawPlayer(replayed.player), 30*time.Second) {
		logf(scenario, "the replay did not report itself ready within 30s")
	}
	// Three captures five seconds apart show whether the recording advances at
	// the speed it was recorded.
	for _, sample := range []struct {
		name string
		wait time.Duration
	}{
		{"3-replay-start.png", 0},
		{"3b-replay-plus5.png", 5 * time.Second},
		{"3c-replay-plus10.png", 5 * time.Second},
	} {
		time.Sleep(sample.wait)
		step(window, a, dir, scenario, sample.name, "record the replay picture", nil)
	}

	// Jumping inside the recording is a seek into buffered media, not a new stream.
	step(window, a, dir, scenario, "", "jump six seconds into the recording", func(a *appState) {
		target := replayed.player.Position() + 6*time.Second
		replayed.player.Seek(target)
		logf(scenario, "seek replay to %s (asked %s)", replayed.player.Position(), target)
	})
	time.Sleep(3 * time.Second)
	step(window, a, dir, scenario, "5-replay-later.png", "record the replay picture after the jump", nil)
}
