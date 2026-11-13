// P16.4f-b on a real window: entering replay from the live stage. The scenario
// starts the buffers, anchors the two live players on the event clock, and then
// calls the internal entry the way the transaction will be asked for it. While
// the replacement players wait for their first picture, one screenshot is taken:
// it has to show the live picture, not the target. The commit that follows must
// move every source in one step, and an ordinary seek has to work immediately
// afterwards.
package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
)

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
