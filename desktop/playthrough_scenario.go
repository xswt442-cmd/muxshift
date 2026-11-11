package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// runPlayThrough watches a file the way a person watches it: playing. The frames
// check asks what the glass shows after a paused seek, where a player may report
// the new position before it has painted the new frame. This check samples while
// the player runs, so the answer says whether steady playback stays together and
// how long a resume takes to reach the frame it was asked for.
func runPlayThrough(window *mygo.Window, a *appState, dir string) {
	go playThroughBody(window, a, dir, "playthrough")
}

func playThroughBody(window *mygo.Window, a *appState, dir, scenario string) {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		finishScenario(a)
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if takeSnapshot(a, it).ready {
			break
		}
		if time.Now().After(deadline) {
			logf(scenario, "the player never became ready")
			check(false, "the player never became ready")
			finishScenario(a)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	length := runOnMainValue(func() time.Duration {
		if it.player == nil {
			return 0
		}
		return it.player.Duration()
	})
	logf(scenario, "the file runs %s", sceneClock(length))

	// Steady playback: let it run and sample the position and the picture
	// together, the way a viewer would notice the two drifting apart.
	mygoRunOnMain(func() {
		if it.player != nil {
			it.player.Play()
		}
	})
	const samples = 20
	for index := 0; index < samples; index++ {
		time.Sleep(400 * time.Millisecond)
		name := fmt.Sprintf("p-%02d.png", index)
		before, after, ready, buffering := bracketCapture(window, a, it, dir, name, scenario)
		check(ready, "sample %d captured while the player was not ready", index+1)
		check(!buffering, "sample %d captured while the player was buffering", index+1)
		check(after >= before, "sample %d went backwards from %s to %s", index+1, sceneClock(before), sceneClock(after))
	}

	// A seek that is then resumed: how long until the picture catches up with
	// the position the player already reports?
	target := insideFile(6*time.Second, length)
	position, settled := settle(a, it, target)
	check(settled, "the seek to %s left the player at %s", sceneClock(target), sceneClock(position))
	logf(scenario, "paused at %s and asked for %s before resuming", sceneClock(position), sceneClock(target))
	mygoRunOnMain(func() {
		if it.player != nil {
			it.player.Play()
		}
	})
	const resumeSamples = 20
	for index := 0; index < resumeSamples; index++ {
		name := fmt.Sprintf("r-%02d.png", index)
		before, _, ready, buffering := bracketCapture(window, a, it, dir, name, scenario)
		check(ready, "resume sample %d captured while the player was not ready", index+1)
		check(!buffering, "resume sample %d captured while the player was buffering", index+1)
		check(before >= target-100*time.Millisecond, "resume sample %d was still at %s, behind the seek to %s",
			index+1, sceneClock(before), sceneClock(target))
		time.Sleep(100 * time.Millisecond)
	}
	logf(scenario, "checks failed: %d", failures)
	finishScenario(a)
}
