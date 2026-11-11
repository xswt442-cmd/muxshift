// Checks for the scene, per-track levels and synchronization groups.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

// runScene is the P6 check on two sources: the first keeps its picture, the
// second supplies the sound, each level is set on its own, then the picture
// switches to the second source, and finally the source being played is
// removed from the session while its player is open.
func runScene(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(2 * time.Second)
		step(window, a, dir, "scene", "1-a-video-a-audio.png", "a video with a audio", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf("scene", "show 1: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("scene", "hear 1: %v", err)
			}
		})
		step(window, a, dir, "scene", "2-a-video-b-audio.png", "a video with b audio", func(a *appState) {
			if err := a.hearAudio(1, 1); err != nil {
				logf("scene", "hear 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "3-b-audio-lowered.png", "b audio at 0.25", func(a *appState) {
			if err := a.setVolume(1, 0.25); err != nil {
				logf("scene", "volume 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "4-both-audio.png", "both audio at their own levels", func(a *appState) {
			if err := a.hearBoth(0, 1, 0.5, 1); err != nil {
				logf("scene", "hear both: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "5-b-video-a-audio.png", "b video with a audio", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				logf("scene", "show 2: %v", err)
			}
			if err := a.hearAudio(0, 1); err != nil {
				logf("scene", "hear 1: %v", err)
			}
		})
		// The acceptance case: A's picture with B's sound, B having been shown
		// once so its engine is running.
		wait(1 * time.Second)
		step(window, a, dir, "scene", "6-a-video-b-audio-warm.png", "a video with b audio after b was shown", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				logf("scene", "show 1: %v", err)
			}
			if err := a.hearAudio(1, 1); err != nil {
				logf("scene", "hear 2: %v", err)
			}
		})
		wait(2 * time.Second)
		step(window, a, dir, "scene", "7-b-audio-2s-later.png", "a video with b audio, 2s later", nil)
		wait(1 * time.Second)
		step(window, a, dir, "scene", "", "remove source 1 while playing", func(a *appState) {
			if err := a.remove(0); err != nil {
				logf("scene", "remove 1: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "scene", "8-after-removal.png", "after removing the playing source", nil)
		step(window, a, dir, "scene", "", "events", func(a *appState) {
			logf("scene", "events: %s", eventList(a))
		})
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runFour opens four sources, shows one picture with another's sound, and
// removes two of them in turn, recording that every player and every session
// object stayed consistent.
func runFour(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "four", "1-four-open.png", "four sources open", nil)
		step(window, a, dir, "four", "2-video1-audio2.png", "video 1 with audio 2", func(a *appState) {
			for _, call := range []struct {
				name string
				fn   func() error
			}{
				{"show 1", func() error { return a.showVideo(0) }},
				{"hear 2", func() error { return a.hearAudio(1, 1) }},
			} {
				if err := call.fn(); err != nil {
					logf("four", "%s: %v", call.name, err)
				}
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "3-video3-audio4.png", "video 3 with audio 4", func(a *appState) {
			if err := a.showVideo(2); err != nil {
				logf("four", "show 3: %v", err)
			}
			if err := a.hearBoth(1, 3, 1, 0.5); err != nil {
				logf("four", "hear 2 and 4: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "", "remove source 2 while it is heard", func(a *appState) {
			if err := a.remove(1); err != nil {
				logf("four", "remove 2: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "4-after-removing-2.png", "after removing source 2", nil)
		step(window, a, dir, "four", "", "remove source 3 while it is shown", func(a *appState) {
			if err := a.remove(1); err != nil {
				logf("four", "remove 3: %v", err)
			}
		})
		wait(1 * time.Second)
		step(window, a, dir, "four", "5-after-removing-3.png", "after removing source 3", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runAudio plays one source with its audio heard, then mutes it, then unmutes
// it, logging a marker at each change. A separate meter sampling the default
// output device records which windows had sound, so audibility can be checked
// without a person listening.
func runAudio(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "audio", "1-audible.png", "audible window starts", nil)
		wait(4 * time.Second)
		step(window, a, dir, "audio", "", "muted window starts", func(a *appState) {
			a.muteAll(true)
		})
		wait(4 * time.Second)
		step(window, a, dir, "audio", "2-muted.png", "unmuted window starts", func(a *appState) {
			a.muteAll(false)
		})
		wait(4 * time.Second)
		step(window, a, dir, "audio", "3-unmuted-again.png", "end", nil)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSync aligns two sources that carry the same picture 2.5 seconds apart.
//
// It pauses the group on the first source, draws the second while paused so the
// two timecodes can be compared, applies offsets through the panel the window
// uses, and captures after each one. The paused group is what makes the
// comparison possible: both players hold their position, so a difference in the
// burnt-in timecode is the difference the offsets have to remove.
func runSync(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the second one is shown once before anything is measured.
		step(window, a, dir, "sync", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(2 * time.Second)
		// The case the app is for: the first source's picture with the second
		// source's sound. Both are in the scene, so a group command reaches both
		// players even though only one picture is drawn.
		step(window, a, dir, "sync", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(4 * time.Second)

		// The way out of the conservative mute is visible and reversible: the
		// button lets a person who can see both pictures allow the sound, and
		// pressing it again puts the hold back.
		step(window, a, dir, "sync", "", "second sound allowed while its offset is unknown", func(a *appState) {
			if err := a.allowAudio(1); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "", "second sound held again", func(a *appState) {
			if err := a.allowAudio(1); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)

		step(window, a, dir, "sync", "", "paused, event anchored on the first source", func(a *appState) {
			// Pause first: the event time has to be the position the group stops
			// at, not a sample taken while the players are still moving.
			logGroup("sync", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "1-paused-a.png", "first picture while paused", nil)

		step(window, a, dir, "sync", "", "second picture while paused", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
			// A seek is what makes a paused player hand over a new picture, so a
			// zero nudge is used to draw the frame this offset puts on screen.
			// It also places the second source on the timeline, which is what
			// lets its sound through: until then it is held silent.
			a.nudgeAndRecord(1, 0)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "2-paused-b-before.png", "second picture before any offset", nil)

		step(window, a, dir, "sync", "", "second delayed by 2s", func(a *appState) {
			a.nudgeAndRecord(1, -2000*time.Millisecond)
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "3-paused-b-after.png", "second picture after -2s", nil)

		step(window, a, dir, "sync", "", "second delayed 0.1s more", func(a *appState) {
			a.nudgeAndRecord(1, -100*time.Millisecond)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "sync", "4-b-minus-0.1.png", "second picture after another -0.1s", nil)

		step(window, a, dir, "sync", "", "second moved 0.1s back", func(a *appState) {
			a.nudgeAndRecord(1, 100*time.Millisecond)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "sync", "5-b-plus-0.1.png", "second picture after +0.1s", nil)

		step(window, a, dir, "sync", "", "delay past the start of the stream is refused", func(a *appState) {
			a.nudgeAndRecord(1, -60*time.Second)
		})
		step(window, a, dir, "sync", "6-refused.png", "after the refused delay", nil)

		step(window, a, dir, "sync", "", "sound back on the first source", func(a *appState) {
			if err := a.hearAudio(0, 1); err != nil {
				a.lastErr = err
			}
		})

		step(window, a, dir, "sync", "", "group resumed", func(a *appState) {
			logGroup("sync", "play", a.playAll())
		})
		wait(2 * time.Second)
		step(window, a, dir, "sync", "", "group paused again", func(a *appState) {
			// Pause first: the event time has to be the position the group stops
			// at, not a sample taken while the players are still moving.
			logGroup("sync", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "sync", "7-paused-again.png", "second picture after resume and pause", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

func runSyncGroup(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		// A source that was never drawn does not advance in this MyGo build, so
		// the second source is shown once before it can be measured.
		step(window, a, dir, "syncgroup", "", "second source shown once", func(a *appState) {
			if err := a.showVideo(1); err != nil {
				a.lastErr = err
			}
		})
		wait(2 * time.Second)
		step(window, a, dir, "syncgroup", "", "first picture with second sound", func(a *appState) {
			if err := a.showVideo(0); err != nil {
				a.lastErr = err
			}
			if err := a.hearAudio(1, 1); err != nil {
				a.lastErr = err
			}
		})
		wait(3 * time.Second)
		step(window, a, dir, "syncgroup", "", "group created by the first sync command", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
			if err := a.anchor(0); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after creation %s", a.groupState())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "1-group-paused.png", "group paused", nil)

		step(window, a, dir, "syncgroup", "", "group resumed", func(a *appState) {
			logGroup("syncgroup", "play", a.playAll())
		})
		wait(2 * time.Second)

		// The third source joins the picture but not the group: the scene now
		// shows a source that is not being synchronized.
		step(window, a, dir, "syncgroup", "", "third source shown, outside the group", func(a *appState) {
			if err := a.showVideo(2); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after scene change %s", a.groupState())
		})
		wait(2 * time.Second)
		step(window, a, dir, "syncgroup", "", "pause the group while the third source plays", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "2-third-still-playing.png", "group paused, third source outside it", nil)
		wait(1500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "", "third source a second and a half later", nil)

		step(window, a, dir, "syncgroup", "", "group resumed, second source removed", func(a *appState) {
			logGroup("syncgroup", "play", a.playAll())
			if err := a.remove(1); err != nil {
				a.lastErr = err
			}
			logf("syncgroup", "after removal %s", a.groupState())
		})
		wait(time.Second)
		step(window, a, dir, "syncgroup", "3-after-removal.png", "second source removed from the session", nil)

		step(window, a, dir, "syncgroup", "", "pause the group after its member left", func(a *appState) {
			logGroup("syncgroup", "pause", a.pauseAll())
		})
		wait(500 * time.Millisecond)
		step(window, a, dir, "syncgroup", "4-paused-after-removal.png", "group paused with the surviving member", nil)

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSyncMap measures one source at a known target time. It pauses the source,
// seeks it to `-at` plus `-offset`, plays a moment so the captured picture cannot
// be a leftover frame, and logs the position the player reports at capture time.
// Reading the burnt-in timecode of that picture says what the player's position
// means for this source, which is the ground truth an alignment claim needs.
func runSyncMap(window *mygo.Window, a *appState, dir string) {
	go func() {
		wait := time.Sleep
		wait(3 * time.Second)
		step(window, a, dir, "syncmap", "", "paused", func(a *appState) {
			logGroup("syncmap", "pause", a.pauseAll())
		})

		target := syncAt + syncOffset
		step(window, a, dir, "syncmap", "", "seek to "+target.String(), func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Seek(target)
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "syncmap", "", "play a moment", func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Play()
		})
		wait(400 * time.Millisecond)
		step(window, a, dir, "syncmap", "map.png", "captured while paused at the target", func(a *appState) {
			it := a.items[0]
			if it.player == nil {
				a.lastErr = fmt.Errorf("no player")
				return
			}
			it.player.Pause()
			logf("syncmap", "at=%s offset=%s target=%s reported=%s", syncAt, syncOffset, target, it.player.Position())
		})

		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}

// runSyncFail pauses a group whose second member stops playing mid-session, to
// show that one failing member is reported instead of pretending the group
// stopped together.
func runSyncFail(window *mygo.Window, a *appState, dir string) {
	go func() {
		time.Sleep(4 * time.Second)
		step(window, a, dir, "syncfail", "", "both sources playing", func(a *appState) {
			if err := a.hearAudio(1, 1); err != nil {
				logf("syncfail", "could not put the second source in the scene: %v", err)
			}
		})
		// Fault injection with the player factory the app really uses: the second
		// player is pointed at an address that answers 404, which is what a
		// source that breaks mid-session looks like to the group.
		step(window, a, dir, "syncfail", "", "second player pointed at a missing stream", func(a *appState) {
			it := a.items[1]
			broken, err := openPlayer("http://127.0.0.1:8790/missing.m3u8")
			if err != nil {
				a.lastErr = err
				return
			}
			old := it.player
			it.player = broken
			it.generation++
			if old != nil {
				old.Close()
			}
		})
		time.Sleep(2 * time.Second)
		step(window, a, dir, "syncfail", "", "pause with a failing member", func(a *appState) {
			logGroup("syncfail", "pause", a.pauseAll())
		})
		time.Sleep(time.Second)
		step(window, a, dir, "syncfail", "", "resume with a failing member", func(a *appState) {
			logGroup("syncfail", "play", a.playAll())
		})
		time.Sleep(time.Second)
		mygoRunOnMain(func() {
			a.release()
			mygo.App.Quit()
		})
	}()
}
