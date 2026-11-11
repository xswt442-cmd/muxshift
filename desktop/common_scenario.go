package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// commonShift is the timestamp shift the second fixture of the "common"
// scenario was built with. It is a container shift, not a content offset: the
// frames are identical, so the two sources show the same event at the same
// content time. The scenario measures the shift to show the fixture is what it
// claims, and never hands it to the Core timeline as an offset.
var commonShift = 4 * time.Second

// runCommon is P16.3d-3.0, kept current with the content clock: two sources that
// really are the same event, buffered separately, must produce a non-empty
// shared window once each source's measured anchor puts it on the content clock
// and its confirmed offset puts it on the event clock. The fixture's four-second
// difference between container timestamps is not a content offset and must not
// reach the timeline.
//
// The fixture is built so the answer can be predicted without asking the code
// under test: the second playlist carries the same encoded frames as the first,
// every packet timestamp four seconds later. Two independent checks hold that
// claim up: ffprobe reports the first segment of the second playlist starting
// exactly 4s after the first, and the decoded frames of corresponding segments
// hash identically. That is what makes the shared window an observation rather
// than a restatement of the number the window was built from.
func runCommon(window *mygo.Window, a *appState, dir string) {
	go commonBody(window, a, dir)
}

func commonBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "common"
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

	// Wait for both pollers to collect enough of each playlist to overlap even
	// after the 4s the second one carries.
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

	// The measured anchors, before any offset is applied: what media time each
	// buffer's first stored second really is. They are printed on their own so a
	// reader can check the four second difference with ffprobe alone.
	anchors := make([]dvr.Anchor, 2)
	mediaStarts := make([]time.Duration, 2)
	for index, runner := range runners {
		// The measurement waits here, off the drawing thread: ffprobe runs in the
		// background and the loop below only reads what it produced.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		stored, err := runner.waitBufferAnchor(ctx, windows[index])
		cancel()
		if err != nil {
			logf(scenario, "source %d: measure the anchor: %v", index+1, err)
			return
		}
		anchors[index] = stored
		media, err := stored.MediaTime(windows[index].Start)
		if err != nil {
			logf(scenario, "source %d: place the window on media time: %v", index+1, err)
			return
		}
		mediaStarts[index] = media
		logf(scenario, "source %d measured: media %s at buffer %s (epoch %d, %s)",
			index+1, sceneClock(anchors[index].MediaStart), sceneClock(anchors[index].BufferStart),
			anchors[index].Epoch, anchors[index].BufferInstance)
	}

	measuredGap := mediaStarts[1] - mediaStarts[0]
	logf(scenario, "the two buffers start %s apart in media time; the fixture shifts the second playlist's timestamps by exactly %s",
		sceneClock(measuredGap), sceneClock(commonShift))
	check(absDuration(measuredGap-commonShift) <= 100*time.Millisecond,
		"the measured media gap is %s, not the %s the fixture was built with", sceneClock(measuredGap), sceneClock(commonShift))

	// The fixtures hold the same frames, so the content offset between them is
	// zero: the four seconds are the container's mux delay, and the content
	// clock (buffer time) does not carry it. Both sources get a confirmed zero
	// offset, which is the honest statement about where their content sits.
	step(window, a, dir, scenario, "", "align both sources on the shared event clock", func(a *appState) {
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
		failures++
		logf(scenario, "CHECK FAILED: no shared window for two sources of the same event: %v", windowErr)
		return
	}
	// The event start is the later of the two content starts. Content time is
	// buffer time, so this is a statement about the clock the timeline holds,
	// not about the container timestamps the anchors measured.
	contentStarts := make([]time.Duration, len(windows))
	wantStart := time.Duration(0)
	for index := range windows {
		content, err := anchors[index].ContentTime(windows[index].Start)
		if err != nil {
			failures++
			logf(scenario, "CHECK FAILED: source %d: content for the window start: %v", index+1, err)
			return
		}
		contentStarts[index] = content
		if index == 0 || content > wantStart {
			wantStart = content
		}
	}
	logf(scenario, "shared window on the event clock: %s..%s; the content clocks put the event start at %s (the containers would say %s and %s)",
		sceneClock(start), sceneClock(end), sceneClock(wantStart), sceneClock(mediaStarts[0]), sceneClock(mediaStarts[1]))
	check(absDuration(start-wantStart) <= 100*time.Millisecond,
		"the shared window starts at %s, but the measured anchors put the event at %s",
		sceneClock(start), sceneClock(wantStart))
	check(end-start >= 8*time.Second, "the shared window covers only %s of overlap", sceneClock(end-start))
	step(window, a, dir, scenario, "1-shared-window.png", "two sources of the same event on one clock", nil)

	// The pictures carry the proof the numbers cannot: both fixtures show the
	// same burned-in timecode from the source material. Both players are asked
	// for the same position, because a player's seek and Position() follow the
	// content clock, not the container timestamps the buffer records -- asking
	// the second one for 6s+shift would repeat the mistake this stage is about.
	const proofEvent = 6 * time.Second
	step(window, a, dir, scenario, "", "pause both players on the same content position", func(a *appState) {
		// The group only covers sources that joined the scene, and the second
		// one is deliberately background here, so pause the players themselves.
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
	var firstPos, secondPos time.Duration
	step(window, a, dir, scenario, "1a-source-1.png", "the first source at the shared content position", func(a *appState) {
		if err := a.showVideo(0); err != nil {
			logf(scenario, "show source 1: %v", err)
		}
	})
	firstPos = positionOf(a, 0)
	step(window, a, dir, scenario, "1b-source-2.png", "the second source at the same content position", func(a *appState) {
		if err := a.showVideo(1); err != nil {
			logf(scenario, "show source 2: %v", err)
		}
	})
	secondPos = positionOf(a, 1)
	logf(scenario, "the pictures were taken at player positions %s and %s, %s apart",
		sceneClock(firstPos), sceneClock(secondPos), sceneClock(secondPos-firstPos))
	// Reading the two captures is the check the numbers cannot make on their
	// own: the same content position has to read the same burned-in timecode in
	// both pictures even though the second file's container timestamps are four
	// seconds later. The tolerance is one frame at 30 fps, not the 1.4667s the
	// container delays this fixture by.
	check(absDuration(secondPos-firstPos) <= 40*time.Millisecond,
		"the two players sit %s apart at the same content position, more than one frame apart",
		sceneClock(secondPos-firstPos))
	for index := range a.items {
		if index >= len(anchors) {
			break
		}
		logf(scenario, "source %d: the buffer's measured container media at its start is %s (epoch %d, %s), while the player was asked for %s",
			index+1, sceneClock(anchors[index].MediaStart), anchors[index].Epoch, anchors[index].BufferInstance, sceneClock(proofEvent))
	}

	// The container window above is measured in the timestamps the files carry.
	// A player cannot use it as it stands: its position starts at zero on the
	// file's first frame. Converting both windows through their own measured
	// clock has to make the two sources of one event line up with no offset at
	// all -- the fixture only shifted timestamps, so the content offset is zero,
	// and the four seconds belong to the conversion, not to the Core timeline.
	step(window, a, dir, scenario, "", "put both sources on their own player clocks", func(a *appState) {
		for index := range a.items {
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
					logf(scenario, "clear %s: %v", track, err)
				}
			}
		}
		a.apply()
	})
	playerStarts := make([]time.Duration, len(windows))
	playerEnds := make([]time.Duration, len(windows))
	for index := range windows {
		window := playerWindow(anchors[index], windows[index])
		playerStarts[index] = window.Start
		playerEnds[index] = window.End
		logf(scenario, "source %d on its player clock: %s..%s (the buffered window was %s..%s; the container origin %s cancels against the buffer origin %s)",
			index+1, sceneClock(window.Start), sceneClock(window.End),
			sceneClock(windows[index].Start), sceneClock(windows[index].End),
			sceneClock(anchors[index].MediaStart), sceneClock(anchors[index].BufferStart))
	}
	check(absDuration(playerStarts[1]-playerStarts[0]) <= 100*time.Millisecond,
		"the same event starts %s apart on the two player clocks after conversion",
		sceneClock(playerStarts[1]-playerStarts[0]))
	check(absDuration((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])) <= 100*time.Millisecond,
		"the two player windows differ in length by %s",
		sceneClock((playerEnds[1]-playerStarts[1])-(playerEnds[0]-playerStarts[0])))
	step(window, a, dir, scenario, "", "the same event on two player clocks", nil)

	// The first negative: the same buffers, but the second source has no
	// confirmed offset at all. A window would be a guess, so the answer has to
	// be a refusal that names the unaligned source.
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

	// The second negative: a confirmed but wrong offset is not refused; it
	// moves the window by the difference. The offset used here is the container
	// gap, which is exactly the mistake the new contract rules out -- if the
	// four seconds were written into the Core timeline as a content offset, the
	// shared window would shrink by four seconds on the wrong assumption.
	step(window, a, dir, scenario, "", "confirm the container gap as if it were a content offset", func(a *appState) {
		it, err := a.at(1)
		if err != nil {
			logf(scenario, "%v", err)
			return
		}
		for _, track := range []core.TrackID{it.video, it.audio} {
			if track == "" {
				continue
			}
			if err := a.sync.timeline.SetShift(track, commonShift); err != nil {
				logf(scenario, "shift %s: %v", track, err)
			}
		}
		a.apply()
	})
	var wrongStart, wrongEnd time.Duration
	var wrongErr error
	mygoRunOnMain(func() {
		wrongStart, wrongEnd, wrongErr = a.commonReplay(sources)
	})
	logf(scenario, "with the container gap used as an offset the same buffers give %s..%s (%v)", sceneClock(wrongStart), sceneClock(wrongEnd), wrongErr)
	check(wrongErr == nil, "a confirmed offset failed instead of moving the window: %v", wrongErr)
	check(absDuration((end-wrongEnd)-commonShift) <= 100*time.Millisecond,
		"the invented offset changed the window end to %s, %s instead of %s",
		sceneClock(wrongEnd), sceneClock(end-wrongEnd), sceneClock(commonShift))
	step(window, a, dir, scenario, "3-wrong-offset.png", "the container gap mistaken for a content offset", nil)
	logf(scenario, "checks failed: %d", failures)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
