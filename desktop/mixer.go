package main

import (
	"fmt"
	"log"
)

// The mixer state in this build is a listening overlay, not a mixing bus.
//
// Three things can silence a source without touching any other one's state:
//
//   - a manual mute on the source itself,
//   - master mute, which silences everything for as long as it is on,
//   - solo, which silences everything except the soloed source.
//
// None of them edits the scene's routing or the level stored for a track: the
// scene keeps saying who is routed and at what level, and the overlay decides
// what is actually heard right now. Exiting solo or master mute therefore
// cannot overwrite a change somebody made while it was on, and every source
// keeps its own volume and mute exactly as it was.
//
// The unaligned protection still wins over solo: a source whose place on the
// shared timeline is unknown stays silent unless the person audible-marks it,
// so entering solo must never quietly take that protection off.

// toggleMute turns a source's own mute on or off. It changes nothing else: the
// volume stored for the source and its place in the scene's routing stay put.
func (a *appState) toggleMute(index int) (string, error) {
	it, err := a.at(index)
	if err != nil {
		return "", err
	}
	if it.audio == "" {
		return "", fmt.Errorf("source %d has no audio track", index+1)
	}
	it.muted = !it.muted
	a.apply()
	if it.muted {
		return fmt.Sprintf("source %d muted; its volume and routing are unchanged", index+1), nil
	}
	return fmt.Sprintf("source %d unmuted at %.0f%%; the rest is unchanged", index+1, it.volume*100), nil
}

// toggleMasterMute silences everything for as long as it is on, without
// changing what any source's own mute, volume or routing says.
func (a *appState) toggleMasterMute() string {
	a.masterMute = !a.masterMute
	a.apply()
	if a.masterMute {
		return "master mute on: every source keeps its own volume, mute and routing"
	}
	return "master mute off: each source is back to what it says"
}

// toggleSolo makes one source the only source heard, or leaves solo when it is
// already the soloed one. A second solo switches the target rather than
// stacking.
func (a *appState) toggleSolo(index int) (string, error) {
	it, err := a.at(index)
	if err != nil {
		return "", err
	}
	if it.audio == "" {
		return "", fmt.Errorf("source %d has no audio track", index+1)
	}
	if a.solo == it.source.ID {
		a.solo = ""
		a.apply()
		return fmt.Sprintf("solo off: source %d and the rest of the mix are back to what they each said", index+1), nil
	}
	if !a.routed(it) {
		return "", fmt.Errorf("source %d is not in the scene, so soloing it would only silence the others; use Hear to route it first", index+1)
	}
	a.solo = it.source.ID
	a.apply()
	switch {
	case a.protectedSilence(it):
		return fmt.Sprintf("soloing source %d, but the unaligned protection keeps it silent until you Allow it", index+1), nil
	case it.muted:
		return fmt.Sprintf("soloing source %d, but it is muted itself: unmute it to hear it", index+1), nil
	}
	return fmt.Sprintf("soloing source %d; the other sources keep their volume and routing", index+1), nil
}

// dropSoloIfGone clears a solo whose source has left the session. The scene is
// the authority on routing, so a target that is no longer routed is dropped
// too: a solo over nothing would only silence the rest.
func (a *appState) dropSoloIfGone() {
	if a.solo == "" {
		return
	}
	it := a.itemOf(a.solo)
	reason := ""
	switch {
	case it == nil:
		reason = "left the session"
	case !a.routed(it):
		reason = "left the scene's audio routing"
	}
	if reason == "" {
		return
	}
	log.Printf("mixer: solo cleared: source %s %s", a.solo, reason)
	a.solo = ""
}

// routed reports whether a source's audio is in the scene at all.
func (a *appState) routed(it *item) bool {
	if it.audio == "" {
		return false
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return false
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio {
			return true
		}
	}
	return false
}

// protectedSilence reports whether the unaligned protection alone would keep
// this source silent with every overlay off.
func (a *appState) protectedSilence(it *item) bool {
	if !a.settings.MuteUnaligned || a.audible[it.audio] || a.aligned(it) {
		return false
	}
	return true
}

// silences lists every reason a source is silent right now, in the order a
// person would check them.
func (a *appState) silences(it *item) []string {
	var reasons []string
	if !a.routed(it) {
		reasons = append(reasons, "not in the scene")
	}
	if it.muted {
		reasons = append(reasons, "muted")
	}
	if a.masterMute {
		reasons = append(reasons, "master mute")
	}
	if a.solo != "" && it.source.ID != a.solo {
		reasons = append(reasons, "another source is soloed")
	}
	if a.protectedSilence(it) {
		reasons = append(reasons, "the unaligned protection")
	}
	return reasons
}

// heard reports whether a source's audio would reach the output with the
// current overlay, which is what a mute button must reflect.
func (a *appState) heard(it *item) bool {
	return len(a.silences(it)) == 0
}

// shownLevel is the level the controls should display for a source: the one the
// scene holds when the source is routed, otherwise the level it would be given
// when somebody routes it.
func (a *appState) shownLevel(it *item) float64 {
	if level, routed := a.sceneLevel(it); routed {
		return level.Volume
	}
	return it.volume
}

// mixerLine describes one source's place in the current listening overlay.
func (a *appState) mixerLine(it *item) string {
	state := "silent"
	if a.heard(it) {
		state = "heard"
	}
	line := fmt.Sprintf("%s %s · %.0f%%", it.source.ID, state, a.shownLevel(it)*100)
	if a.solo == it.source.ID {
		line += " · solo"
	}
	if reasons := a.silences(it); len(reasons) > 0 {
		line += " · " + joinWords(reasons)
	}
	return line
}

// joinWords joins a short list the way the status lines read.
func joinWords(words []string) string {
	out := ""
	for i, word := range words {
		switch {
		case i == 0:
			out = word
		case i == len(words)-1:
			out += " and " + word
		default:
			out += ", " + word
		}
	}
	return out
}
