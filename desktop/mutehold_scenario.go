package main

import (
	"time"

	"github.com/egoist/mygo"
)

// runMuteHold is the plainest audibility check this build can make: open the
// sources, mute everything through the same path the Mixer page uses, and hold
// that state long enough for a meter sampling the default output device to say
// whether the device went quiet.
func runMuteHold(window *mygo.Window, a *appState, dir string) {
	go func() {
		if !waitPlayers(a, 30*time.Second) {
			logf("mutehold", "the players never came up")
			finishScenario(a)
			return
		}
		// Heard first, so the meter has a window with sound to compare against.
		mygoRunOnMain(func() { a.anchor(0) })
		logf("mutehold", "heard at %s", time.Now().Format("15:04:05.000"))
		time.Sleep(8 * time.Second)

		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandMasterMute}))
		})
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			logf("mutehold", "source %d muted=%v", i+1, rawPlayer(it.player).Muted())
		}
		logf("mutehold", "muted at %s", time.Now().Format("15:04:05.000"))
		time.Sleep(10 * time.Second)

		mygoRunOnMain(func() {
			a.report(a.runCommand(command{Kind: commandMasterMute}))
		})
		logf("mutehold", "unmuted at %s", time.Now().Format("15:04:05.000"))
		time.Sleep(6 * time.Second)

		step(window, a, dir, "mutehold", "1-after.png", "after the mute was lifted", nil)
		finishScenario(a)
	}()
}
