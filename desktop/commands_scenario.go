// The commands scenario drives every button once through runCommand and prints
// what the boundary recorded after each one. It exists because P13.2 claims a
// single door: this is the check that the real window goes through it.
package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

// snapshotLine is one readable line per snapshot, short enough for the log.
func snapshotLine(s sessionSnapshot) string {
	line := "picture=" + tracksLine(s.Picture)
	line += " sound="
	for i, track := range s.Sound {
		if i > 0 {
			line += ","
		}
		line += string(track.Track) + "@" + itoa(int(track.Volume*100)) + " muted=" + boolWord(track.Muted)
	}
	line += " sources="
	for i, source := range s.Sources {
		if i > 0 {
			line += " | "
		}
		line += string(source.ID) + " gen=" + itoa(source.Generation) +
			" shown=" + boolWord(source.Shown) + " player=" + boolWord(source.Player) +
			" ready=" + boolWord(source.Ready) + " pos=" + source.Position.String()
	}
	line += " event=" + s.Event.String() + " playing=" + boolWord(s.Playing) +
		" group=" + emptyWord(s.Group) + " last=" + s.LastCommand
	return line
}

func tracksLine(tracks []core.TrackID) string {
	if len(tracks) == 0 {
		return "none"
	}
	out := ""
	for i, track := range tracks {
		if i > 0 {
			out += ","
		}
		out += string(track)
	}
	return out
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func emptyWord(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// waitUntilReady polls the players without issuing any command, so the
// commands that follow run against a picture that already exists.
func waitUntilReady(a *appState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ready int
		mygoRunOnMain(func() {
			for _, it := range a.items {
				if it.player != nil && it.player.Ready() {
					ready++
				}
			}
		})
		if ready == len(a.items) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func runCommands(window *mygo.Window, a *appState, dir string) {
	if len(a.items) < 2 {
		logf("commands", "this scenario needs two sources, got %d", len(a.items))
		return
	}
	// A scenario runs on the UI goroutine, so anything slow has to move off it:
	// sleeping here stops the window from drawing the very picture this
	// scenario is about, and the players then report no picture at all.
	go func() {
		time.Sleep(2 * time.Second)
		first := a.items[0]
		second := a.items[1]

		run := func(cmd command) commandResult {
			var result commandResult
			mygoRunOnMain(func() { result = a.runCommand(cmd) })
			logf("commands", "command: %s", result.String())
			logf("commands", "snapshot: %s", snapshotLine(a.readSnapshotOnMain()))
			return result
		}

		logf("commands", "start: %s", snapshotLine(a.readSnapshotOnMain()))
		if !waitUntilReady(a, 30*time.Second) {
			logf("commands", "players did not report a picture before the first command: %s", snapshotLine(a.readSnapshotOnMain()))
		} else {
			logf("commands", "every player reports a picture: %s", snapshotLine(a.readSnapshotOnMain()))
		}

		run(command{Kind: commandShow, Source: first.source.ID})
		run(command{Kind: commandHear, Source: second.source.ID})
		run(command{Kind: commandVolume, Source: second.source.ID, Volume: 0.4})
		// A nudge before playback has produced any history is refused by design,
		// so the scene runs for a moment first and the accepted nudge is the one
		// the log keeps as evidence.
		time.Sleep(2 * time.Second)
		// The nudge is measured from the shared event time, which only means
		// anything once the playhead has been anchored to a real position.
		mygoRunOnMain(func() {
			if err := a.anchor(0); err != nil {
				logf("commands", "cannot anchor the event clock: %v", err)
			}
		})
		run(command{Kind: commandNudge, Source: second.source.ID, Delta: -100 * time.Millisecond})
		run(command{Kind: commandPause})
		run(command{Kind: commandPlay})
		run(command{Kind: commandReload, Source: first.source.ID})
		time.Sleep(3 * time.Second)
		// An identifier that is not in the session must be refused, not guessed by
		// row number.
		run(command{Kind: commandShow, Source: "source-99"})

		step(window, a, dir, "commands", "1-after-commands.png", "after running every command once", nil)
		mygoRunOnMain(func() { a.release(); mygo.App.Quit() })
	}()
}
