package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runMixer checks the listening overlay (P17.4a) in a real window. This build
// routes one source's sound at a time (Hear is exclusive), so the scenario uses
// the shape the product actually has: one routed source held silent by the
// unaligned protection until a person anchors it, a second source that is not in
// the scene at all, then solo, master mute and back.
func runMixer(window *mygo.Window, a *appState, dir string) {
	go mixerBody(window, a, dir)
}

func mixerBody(window *mygo.Window, a *appState, dir string) {
	checks := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			checks++
			logf("mixer", "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < 2 {
		logf("mixer", "this check needs two sources")
		finishScenario(a)
		return
	}
	if !waitPlayers(a, 30*time.Second) {
		logf("mixer", "the players never came up")
		finishScenario(a)
		return
	}

	step(window, a, dir, "mixer", "1-mixer.png", "before anybody aligns anything", func(a *appState) {
		a.workspace = workspaceMixer
		joined := strings.Join(mixerLines(a), "\n")
		for _, line := range mixerLines(a) {
			logf("mixer", "before: %s", line)
		}
		check(strings.Contains(joined, "the unaligned protection"),
			"a routed source is not held back by the protection until it is aligned: %q", joined)
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			logf("mixer", "player %d: muted=%v volume=%.2f", i+1,
				rawPlayer(it.player).Muted(), rawPlayer(it.player).Volume())
		}
	})
	mixerPhase(a, "before aligning")

	// Anchor the first source: the protection lifts and its sound reaches the
	// device. This is the real command path, on the drawing thread.
	var anchorErr error
	mygoRunOnMain(func() { anchorErr = a.anchor(0) })
	check(anchorErr == nil, "anchor: %v", anchorErr)

	step(window, a, dir, "mixer", "2-heard.png", "the routed source heard once it is aligned", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "after anchoring: %s", line)
		}
		check(a.heard(a.items[0]), "the aligned source is still silent: %s", a.mixerLine(a.items[0]))
		if a.items[0].player != nil {
			check(!rawPlayer(a.items[0].player).Muted(), "the heard source's player is muted")
		}
	})
	mixerPhase(a, "heard")
	time.Sleep(5 * time.Second)

	// A source the scene does not route cannot be soloed: refusing is the point,
	// because a solo there would only silence what is playing for nothing.
	mygoRunOnMain(func() {
		result := a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 1)})
		check(result.Err != nil, "soloing a source the scene does not route was accepted")
		check(strings.Contains(result.Err.Error(), "not in the scene") ||
			strings.Contains(result.Err.Error(), "not routed"),
			"the refusal does not say the source is unrouted: %v", result.Err)
		logf("mixer", "solo on the unrouted source: %v", result.Err)
		// The refusal is expected here; it must not linger as a session error.
		a.lastErr = nil
	})

	// Solo the routed source: the overlay keeps it heard and holds the rest.
	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 0)}))
	})

	step(window, a, dir, "mixer", "3-soloed.png", "the routed source soloed", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo on: %s", line)
		}
		check(a.solo == sourceAt(a, 0), "solo does not name the first source: %q", a.solo)
		check(a.heard(a.items[0]), "solo made its own source silent: %s", a.mixerLine(a.items[0]))
		check(strings.Contains(a.mixerLine(a.items[0]), "solo"),
			"the row does not say it is soloed: %q", a.mixerLine(a.items[0]))
	})
	mixerPhase(a, "solo on")
	time.Sleep(6 * time.Second)

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandMasterMute}))
	})

	step(window, a, dir, "mixer", "4-muted.png", "master mute, a silent window for the meter", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "master mute on: %s", line)
		}
		check(a.masterMute, "the master mute did not take")
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			check(rawPlayer(it.player).Muted(), "source %d still plays under the master mute", i+1)
		}
	})
	mixerPhase(a, "master mute on")
	time.Sleep(6 * time.Second)

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandMasterMute}))
		if solo := a.solo; solo != "" {
			a.report(a.runCommand(command{Kind: commandSolo, Source: solo}))
		}
	})

	step(window, a, dir, "mixer", "5-restored.png", "overlay cleared", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "restored: %s", line)
		}
		check(a.solo == "", "a source is still soloed: %q", a.solo)
		check(!a.masterMute, "the master mute is still on")
		check(a.heard(a.items[0]), "the routed source did not come back: %s", a.mixerLine(a.items[0]))
	})
	mixerPhase(a, "overlay cleared")
	time.Sleep(4 * time.Second)

	// Changing a level is not routing: the unrouted source must be refused, and
	// the routed one's slider must end up showing what the scene now holds.
	mygoRunOnMain(func() {
		result := a.runCommand(command{Kind: commandVolume, Source: sourceAt(a, 1), Volume: 0.35})
		check(result.Err != nil, "changing the level of an unrouted source was accepted")
		check(result.Err != nil && strings.Contains(result.Err.Error(), "use Hear"),
			"the refusal does not point at routing: %v", result.Err)
		logf("mixer", "volume on the unrouted source: %v", result.Err)
		a.lastErr = nil

		if level, routed := a.sceneLevel(a.items[1]); routed {
			check(false, "the refused volume routed the source at %.2f", level.Volume)
		}

		a.report(a.runCommand(command{Kind: commandVolume, Source: sourceAt(a, 0), Volume: 0.25}))
	})

	step(window, a, dir, "mixer", "6-volume.png", "the routed source at a quarter", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "after the volume change: %s", line)
		}
		level, routed := a.sceneLevel(a.items[0])
		check(routed && level.Volume == 0.25, "the scene holds %.2f for the routed source, want 0.25", level.Volume)
		check(a.items[0].volume == 0.25, "the item still carries %.2f", a.items[0].volume)
		check(strings.Contains(a.mixerLine(a.items[0]), "25%"),
			"the row does not show the level the scene holds: %q", a.mixerLine(a.items[0]))
	})

	// The window's own Hear is exclusive, but the scene can hold two sources at
	// once (hearBoth). Solo only means something with more than one audible
	// source, so the switch and the return are checked here rather than claimed
	// from the single-routed state above. The second source is aligned by
	// nothing, so it must be allowed explicitly first: the protection wins over
	// routing, and a solo is not a way around it.
	var allowErr, bothErr error
	mygoRunOnMain(func() {
		allowErr = a.allowAudio(1)
		bothErr = a.hearBoth(0, 1, 0.8, 0.6)
	})
	check(allowErr == nil, "allowing the second source: %v", allowErr)
	check(bothErr == nil, "routing both sources: %v", bothErr)

	step(window, a, dir, "mixer", "7-both-heard.png", "two sources heard at their own levels", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "both routed: %s", line)
		}
		for i, it := range a.items {
			if it.player == nil {
				continue
			}
			check(!rawPlayer(it.player).Muted(), "source %d is silent although it is routed", i+1)
		}
		check(a.heard(a.items[0]) && a.heard(a.items[1]),
			"the rows do not both say heard: %q / %q", a.mixerLine(a.items[0]), a.mixerLine(a.items[1]))
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 0)}))
	})

	step(window, a, dir, "mixer", "8-solo-switch.png", "solo moved from one routed source to the other", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo first: %s", line)
		}
		check(a.solo == sourceAt(a, 0), "solo does not name the first source: %q", a.solo)
		if a.items[0].player != nil {
			check(!rawPlayer(a.items[0].player).Muted(), "the soloed source is muted")
		}
		if a.items[1].player != nil {
			check(rawPlayer(a.items[1].player).Muted(), "the other routed source still plays under solo")
		}
		check(strings.Contains(a.mixerLine(a.items[1]), "another source is soloed"),
			"the second row does not say why it is silent: %q", a.mixerLine(a.items[1]))
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 1)}))
	})

	step(window, a, dir, "mixer", "9-solo-switched.png", "solo switched to the second source", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo switched: %s", line)
		}
		check(a.solo == sourceAt(a, 1), "solo did not switch: %q", a.solo)
		if a.items[1].player != nil {
			check(!rawPlayer(a.items[1].player).Muted(), "the newly soloed source is muted")
		}
		if a.items[0].player != nil {
			check(rawPlayer(a.items[0].player).Muted(), "the source solo left still plays")
		}
	})

	mygoRunOnMain(func() {
		a.report(a.runCommand(command{Kind: commandSolo, Source: sourceAt(a, 1)}))
	})

	step(window, a, dir, "mixer", "10-both-back.png", "leaving solo restores both sources and their levels", func(a *appState) {
		for _, line := range mixerLines(a) {
			logf("mixer", "solo off: %s", line)
		}
		check(a.solo == "", "solo is still on: %q", a.solo)
		for i, want := range []float64{0.8, 0.6} {
			if level, routed := a.sceneLevel(a.items[i]); !routed || level.Volume != want {
				check(false, "source %d holds %.2f in the scene, want %.2f", i+1, level.Volume, want)
			}
			if a.items[i].player != nil {
				check(!rawPlayer(a.items[i].player).Muted(), "source %d stayed muted after solo", i+1)
			}
			check(strings.Contains(a.mixerLine(a.items[i]), "heard"),
				"source %d does not read as heard again: %q", i+1, a.mixerLine(a.items[i]))
		}
	})

	logf("mixer", "mixer: %d source(s), checks failed: %d", len(a.items), checks)
	finishScenario(a)
}

// mixerLines is the page's account of every source, which is what the rows show.
func mixerLines(a *appState) []string {
	lines := make([]string, 0, len(a.items))
	for _, it := range a.items {
		lines = append(lines, a.mixerLine(it))
	}
	return lines
}

// mixerPhase writes a timestamped boundary the meter can be read against.
func mixerPhase(a *appState, where string) {
	logf("mixer", "phase %s at %s", where, time.Now().Format("15:04:05.000"))
	for _, line := range mixerLines(a) {
		logf("mixer", "  %s", line)
	}
}
