package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// runFinal is P16.3d-3.4: the last run of the replay-clock stage. It does not
// add behaviour; it puts the positive case, the refusals and the resource
// bookkeeping of one session into a single piece of evidence, so a reader can
// see them agree with each other instead of trusting four separate runs.
//
// The two fixtures are the ones used since 3.0: the same encoded frames, with
// the second playlist's timestamps four seconds later. Two independent checks
// hold that claim up: ffprobe reads the second one four seconds later, and the
// decoded frames hash identically. That is what makes the shared window an
// observation rather than a restatement of the number it was built from.
func runFinal(window *mygo.Window, a *appState, dir string) {
	go finalBody(window, a, dir)
}

func finalBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "final"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			failures++
			logf(scenario, "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < 2 {
		logf(scenario, "needs two network sources, got %d", len(a.items))
		return
	}
	uris := make([]string, 2)
	for i := 0; i < 2; i++ {
		it, err := a.at(i)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		if !isNetworkAddress(it.uri) {
			logf(scenario, "source %d is not a network address: %s", i+1, shortURI(it.uri))
			return
		}
		uris[i] = it.uri
	}

	sources := make([]replaySource, 0, 2)
	runners := make([]*dvrRunner, 0, 2)
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("source-%d", index+1)
		runner, err := startDVR(uris[index], dvrFromMedia(
			a.dvrDirFor(name), a.dvrRecordingsDirFor(name), dvrRetention, 0, 0, a.items[index].source.ID))
		if err != nil {
			logf(scenario, "source %d: open the buffer: %v", index+1, err)
			return
		}
		a.trackRunner(uris[index], runner)
		sources = append(sources, replaySource{index: index, runner: runner})
		runners = append(runners, runner)
	}
	defer func() {
		mygoRunOnMain(func() {
			for i, runner := range runners {
				if err := runner.Close(); err != nil {
					logf(scenario, "source %d: close the buffer: %v", i+1, err)
				}
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	// 1. The positive case: two sources of one event, buffered separately.
	windows := make([]dvr.Range, 2)
	for index, runner := range runners {
		windowOf, ok := waitForRange(runner, 10*time.Second, 60*time.Second)
		if !ok {
			logf(scenario, "source %d did not buffer 10s within 60s", index+1)
			return
		}
		windows[index] = windowOf
		logf(scenario, "source %d buffers %s of %s", index+1, logRange(windowOf), shortURI(uris[index]))
	}
	anchors := make([]dvr.Anchor, 2)
	mediaStarts := make([]time.Duration, 2)
	contentStarts := make([]time.Duration, 2)
	for index, runner := range runners {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		stored, err := runner.waitBufferAnchor(ctx, windows[index])
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		anchors[index] = stored
		// Media time is the container's clock, which the files disagree on; the
		// content clock is what the timeline holds, and it must agree.
		media, err := stored.MediaTime(windows[index].Start)
		if err != nil {
			logf(scenario, "source %d: place the window on media time: %v", index+1, err)
			return
		}
		mediaStarts[index] = media
		content, err := stored.ContentTime(windows[index].Start)
		if err != nil {
			logf(scenario, "source %d: place the window on content time: %v", index+1, err)
			return
		}
		contentStarts[index] = content
		logf(scenario, "source %d measured: media %s (content %s) at buffer %s (epoch %d, %s)",
			index+1, sceneClock(anchors[index].MediaStart), sceneClock(content),
			sceneClock(anchors[index].BufferStart), anchors[index].Epoch, anchors[index].BufferInstance)
	}
	// The content offset of the fixture is zero: only the container timestamps
	// were shifted, and the player clock conversion below removes that. The
	// Core timeline therefore carries no offset at all, and the four seconds
	// remain a property of the fixtures rather than of the mapping.
	step(window, a, dir, scenario, "", "confirm the fixture's content offset is zero", func(a *appState) {
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
		a.apply()
	})

	var start, end time.Duration
	var windowErr error
	mygoRunOnMain(func() {
		start, end, windowErr = a.commonReplay(sources)
	})
	if windowErr != nil {
		check(false, "no shared window for two sources of the same event: %v", windowErr)
	} else {
		wantStart := contentStarts[0]
		if contentStarts[1] > wantStart {
			wantStart = contentStarts[1]
		}
		logf(scenario, "shared window on the event clock: %s..%s; the content clocks put the event start at %s (the containers would say %s and %s)",
			sceneClock(start), sceneClock(end), sceneClock(wantStart), sceneClock(mediaStarts[0]), sceneClock(mediaStarts[1]))
		check(absDuration(start-wantStart) <= 100*time.Millisecond,
			"the shared window starts at %s, but the content clocks put the event at %s", sceneClock(start), sceneClock(wantStart))
		check(end-start >= 8*time.Second, "the shared window covers only %s", sceneClock(end-start))

		// The invariant of this stage: one content position, one event time, even
		// though the two files state it four seconds apart in their containers.
		// Going through the container instead put exactly that difference into the
		// event time, which is what the previous run reported as a shared window.
		const proofContent = 6 * time.Second
		events := make([]time.Duration, len(anchors))
		for index := range anchors {
			content, err := anchors[index].ContentTime(windows[index].Start + proofContent)
			if err != nil {
				check(false, "source %d: content at %s: %v", index+1, sceneClock(proofContent), err)
				continue
			}
			event, err := eventFromMedia(content, 0)
			if err != nil {
				check(false, "source %d: event for content %s: %v", index+1, sceneClock(content), err)
				continue
			}
			events[index] = event
			// The reverse, as P16.4 will need it: content minus the file's start
			// is the position to ask this particular player for.
			position := content - windows[index].Start
			logf(scenario, "source %d: content %s is event %s; in a file cut at %s that is position %s",
				index+1, sceneClock(proofContent), sceneClock(event), sceneClock(windows[index].Start), sceneClock(position))
			check(absDuration(position-proofContent) <= time.Millisecond,
				"source %d: event %s went back to %s, want %s",
				index+1, sceneClock(event), sceneClock(position), sceneClock(proofContent))
		}
		check(events[0] == events[1],
			"the same content is event %s in one file and %s in the other", sceneClock(events[0]), sceneClock(events[1]))
	}

	// Each window has to become a player position through its own measured
	// clock, and the two conversions must agree although the container
	// timestamps differ by four seconds.
	playerStarts := make([]time.Duration, len(windows))
	playerEnds := make([]time.Duration, len(windows))
	for index := range windows {
		converted := playerWindow(anchors[index], windows[index])
		playerStarts[index] = converted.Start
		playerEnds[index] = converted.End
		logf(scenario, "source %d on its player clock: %s..%s (buffered %s..%s; container origin %s, buffer origin %s)",
			index+1, sceneClock(converted.Start), sceneClock(converted.End),
			sceneClock(windows[index].Start), sceneClock(windows[index].End),
			sceneClock(anchors[index].MediaStart), sceneClock(anchors[index].BufferStart))
	}
	check(absDuration(playerStarts[1]-playerStarts[0]) <= 100*time.Millisecond,
		"the same event starts %s apart on the two player clocks", sceneClock(playerStarts[1]-playerStarts[0]))
	check(absDuration((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])) <= 100*time.Millisecond,
		"the two player windows differ in length by %s",
		sceneClock((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])))

	// The pictures: both players are asked for the same content position,
	// because a player's seek follows the content clock, not the container
	// timestamps the buffer records. Asking the second one for 6s+4s would
	// repeat the mistake this stage is about.
	const proofEvent = 6 * time.Second
	step(window, a, dir, scenario, "", "pause both players on the same content position", func(a *appState) {
		for index, it := range a.items {
			if it.player == nil {
				continue
			}
			it.player.Pause()
			it.player.Seek(proofEvent)
			logf(scenario, "source %d paused and asked for %s", index+1, sceneClock(proofEvent))
		}
	})
	time.Sleep(2 * time.Second)
	step(window, a, dir, scenario, "1a-source-1.png", "the first source at the shared content position", func(a *appState) {
		if err := a.showVideo(0); err != nil {
			logf(scenario, "show source 1: %v", err)
		}
	})
	firstPos := positionOf(a, 0)
	step(window, a, dir, scenario, "1b-source-2.png", "the second source at the same content position", func(a *appState) {
		if err := a.showVideo(1); err != nil {
			logf(scenario, "show source 2: %v", err)
		}
	})
	secondPos := positionOf(a, 1)
	logf(scenario, "the pictures were taken at player positions %s and %s, %s apart",
		sceneClock(firstPos), sceneClock(secondPos), sceneClock(secondPos-firstPos))
	check(absDuration(secondPos-firstPos) <= 40*time.Millisecond,
		"the two players sit %s apart at the same content position, more than one frame apart",
		sceneClock(secondPos-firstPos))

	// 2. Refusals. An unconfirmed offset must not produce a window at all.
	step(window, a, dir, scenario, "", "forget the offset of the second source", func(a *appState) {
		it, err := a.at(1)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		for _, track := range []core.TrackID{it.video, it.audio} {
			if track == "" {
				continue
			}
			a.sync.timeline.Forget(track)
		}
		a.apply()
	})
	var unprovenStart, unprovenEnd time.Duration
	var unprovenErr error
	mygoRunOnMain(func() {
		unprovenStart, unprovenEnd, unprovenErr = a.commonReplay(sources)
	})
	logf(scenario, "with the second source unaligned the buffers give %s..%s and %v",
		sceneClock(unprovenStart), sceneClock(unprovenEnd), unprovenErr)
	check(unprovenErr != nil && errors.Is(unprovenErr, core.ErrNotAligned),
		"an unaligned source produced a window instead of ErrNotAligned (%v)", unprovenErr)
	step(window, a, dir, scenario, "2-unaligned.png", "the same buffers with no confirmed offset", nil)

	// The real faults are separate buffers: the scenario's own sources are
	// healthy, so the broken playlists are opened against the same server with
	// their own directories. Each has to lose the right thing.
	gap, gapFinish := finalRunner(a, dir, "gap", "d3-gap2/index.m3u8", "final-gap")
	if gap != nil {
		ranges := railOf(gap)
		logf(scenario, "gap: %d ranges, epochs %s", len(ranges), epochList(ranges))
		if len(ranges) >= 2 {
			check(ranges[0].Epoch == ranges[len(ranges)-1].Epoch,
				"the skipped segments moved the epoch: %s", epochList(ranges))
			bridged, err := bridge(gap, ranges)
			check(err != nil, "a window over the skipped segments was authorised: %v", bridged)
			logf(scenario, "gap: a window from %s to %s is refused: %v",
				ranges[0].Start, ranges[len(ranges)-1].End, err)
		} else {
			check(false, "the missing segments did not split the buffer: %d range(s)", len(ranges))
		}
		gapFinish()
	}
	disc, discFinish := finalRunner(a, dir, "disc", "d3-disc/index.m3u8", "final-disc")
	if disc != nil {
		ranges := railOf(disc)
		logf(scenario, "disc: %d ranges, epochs %s", len(ranges), epochList(ranges))
		if len(ranges) >= 2 {
			check(ranges[0].Epoch != ranges[len(ranges)-1].Epoch,
				"the discontinuity did not move the epoch: %s", epochList(ranges))
			bridged, err := bridge(disc, ranges)
			check(err != nil, "a window across the discontinuity was authorised: %v", bridged)
			logf(scenario, "disc: a window from %s to %s is refused: %v",
				ranges[0].Start, ranges[len(ranges)-1].End, err)
		} else {
			check(false, "the discontinuity did not split the buffer: %d range(s)", len(ranges))
		}
		discFinish()
	}

	// 3. Lifecycle: a second buffer of the same source is a different clock.
	// Its anchor must not be accepted for a recording the first one produced,
	// even though the source id is the same.
	first := runners[0]
	recording, err := first.buffer.Record(0)
	if err != nil {
		logf(scenario, "record from the first buffer: %v", err)
	} else {
		rebuilt, rebuiltFinish := finalRunnerOn(a, dir, "rebuilt", uris[0], a.items[0].source.ID)
		if rebuilt != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			windowOf, _ := waitForRange(rebuilt, 6*time.Second, 60*time.Second)
			newAnchor, anchorErr := rebuilt.waitBufferAnchor(ctx, windowOf)
			cancel()
			if anchorErr != nil {
				logf(scenario, "the rebuilt buffer did not measure an anchor: %v", anchorErr)
			} else {
				oldInstance := first.buffer.Instance()
				logf(scenario, "rebuilding the buffer: instance %s -> %s, epoch %d -> %d",
					oldInstance, rebuilt.buffer.Instance(), first.buffer.Epoch(), rebuilt.buffer.Epoch())
				check(rebuilt.buffer.Instance() != oldInstance,
					"the rebuilt buffer kept the instance name %s", oldInstance)
				_, fileErr := replayFileFor(recording, newAnchor)
				check(fileErr != nil,
					"a new buffer's anchor was accepted for a recording of %s", recording.ID)
				logf(scenario, "the recording %s is refused under the rebuilt anchor: %v", recording.ID, fileErr)
			}
			rebuiltFinish()
		}
	}

	// 4. Switching to a recording and closing it gives every lease back: the
	// player and the lease are two facts, so both are counted.
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
	} else {
		// The stage is showing the second source after the picture step, and a
		// player that is not drawn never leaves the fist frame: a switch there
		// would be rolled back for the wrong reason. The players were paused for
		// the picture comparison, so play this one again as well: a window with
		// no playing player stops redrawing, and the replacement would never get
		// a frame even though it is attached to the stage.
		mygoRunOnMain(func() {
			if err := a.showVideo(0); err != nil {
				logf(scenario, "show source 1 before the switch: %v", err)
			}
			if it, err := a.at(0); err == nil && it.player != nil {
				it.player.Play()
			}
		})
		time.Sleep(1500 * time.Millisecond)
		base := windows[0].Start
		if err := a.playRecording(it, first, base); err != nil {
			logf(scenario, "switch to a recording: %v", err)
		} else if waitRecording(a, 0, base, 20*time.Second) {
			open, given := a.leaseStats()
			logf(scenario, "the recording is on the stage | leases open=%d given=%d", open, given)
			check(open == 1, "the stage holds %d lease(s) for one recording", open)
			step(window, a, dir, scenario, "3-recording.png", "the replay of a recording in the same session", nil)
		} else {
			check(false, "the recording did not reach the stage")
		}
	}

	mygoRunOnMain(func() { a.release() })
	time.Sleep(500 * time.Millisecond)
	open, given := a.leaseStats()
	players := openPlayers(a)
	logf(scenario, "after the session closed: leases open=%d given=%d players=%d", open, given, players)
	check(open == 0, "the run ended with %d lease(s) open", open)
	check(players == 0, "the run ended with %d player(s) open", players)
	left := first.buffer.PendingCleanup()
	recordings := len(first.buffer.Recordings())
	logf(scenario, "after closing the first buffer: pending cleanup=%v recordings=%d", left, recordings)
	check(len(left) == 0, "the buffer could not remove %v", left)
	check(recordings == 0, "the closed buffer still owns %d recording(s)", recordings)

	logf(scenario, "checks failed: %d", failures)
}

// finalRunner opens a buffer on a playlist of the fixture server, under the
// scenario's own directory, and waits for it to hold media. The broken
// playlists are separate buffers on purpose: the scenario's sources are the
// healthy pair, and the faults must not be able to disturb the positive case.
func finalRunner(a *appState, dir, name, playlist, source string) (*dvrRunner, func()) {
	return finalRunnerOn(a, dir, name, "http://127.0.0.1:8791/"+playlist, core.SourceID(source))
}

func finalRunnerOn(a *appState, dir, name, uri string, source core.SourceID) (*dvrRunner, func()) {
	runner, err := startDVR(uri, dvrFromMedia(
		filepath.Join(dir, name, "segments"), filepath.Join(dir, name, "recordings"),
		120*time.Second, 0, dvrRecordingBytes, source))
	if err != nil {
		logf("final", "%s: open the buffer: %v", name, err)
		return nil, func() {}
	}
	a.trackRunner(uri, runner)
	if _, ok := waitForRange(runner, 6*time.Second, 60*time.Second); !ok {
		logf("final", "%s: the buffer did not hold 6s within 60s", name)
		return runner, func() {
			if err := runner.Close(); err != nil {
				logf("final", "%s: close the buffer: %v", name, err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := runner.waitAnchor(ctx); err != nil {
		logf("final", "%s: measuring the clock: %v", name, err)
	}
	return runner, func() {
		if err := runner.Close(); err != nil {
			logf("final", "%s: close the buffer: %v", name, err)
		}
	}
}
