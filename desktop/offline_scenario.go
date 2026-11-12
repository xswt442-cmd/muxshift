package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runOffline watches a replay buffer while something outside the window takes the
// source away and brings it back. It changes nothing: the point is what a reader
// sees when the network goes and comes back, not a recovery mechanism of our own.
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

	// A page that only shows a number standing still is not saying why. While the
	// source is away, the buffer's own row should name the read that failed.
	if usage := runner.usage(); usage.readErr == "" {
		check(false, "the buffer line does not say the source cannot be read")
	} else {
		logf(scenario, "the page's buffer line says: %s", usage.readErr)
	}
	step(window, a, dir, scenario, "1-during-the-outage.png", "the source is away and the page says so", func(a *appState) {
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
	if usage := runner.usage(); usage.readErr != "" {
		check(false, "the buffer still says the source cannot be read after it came back: %s", usage.readErr)
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
