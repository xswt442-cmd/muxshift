package main

import (
	"fmt"
	"log"
	"strings"
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
	a.noteSourcePreference(it)
	if it.muted {
		return fmt.Sprintf("%s muted; its volume and scene routing are unchanged", sourceDisplayName(it)), nil
	}
	return fmt.Sprintf("%s unmuted at %.0f%%; its scene routing is unchanged", sourceDisplayName(it), it.volume*100), nil
}

// toggleMasterMute silences everything for as long as it is on, without
// changing what any source's own mute, volume or routing says.
func (a *appState) toggleMasterMute() string {
	a.masterMute = !a.masterMute
	a.apply()
	if a.masterMute {
		return "master mute on: every source keeps its own volume, source mute and scene routing"
	}
	return "master mute off: each source is back to its own state"
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
		return fmt.Sprintf("solo off: %s and the other sources are back to their own states", sourceDisplayName(it)), nil
	}
	if !a.routed(it) {
		return "", fmt.Errorf("%s is not in the scene's audio routing; use Route only before soloing it", sourceDisplayName(it))
	}
	a.solo = it.source.ID
	a.apply()
	switch {
	case a.protectedSilence(it):
		return fmt.Sprintf("soloing %s, but alignment protection keeps it silent until you allow its audio", sourceDisplayName(it)), nil
	case it.muted:
		return fmt.Sprintf("soloing %s, but its source mute is on", sourceDisplayName(it)), nil
	}
	return fmt.Sprintf("soloing %s; other sources keep their volume and scene routing", sourceDisplayName(it)), nil
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

// clearSceneMute clears only this source's mute bit in the active scene. It
// preserves the route, level, every neighboring track and the source's own
// mute, including when the scene mute came from an ambiguous older snapshot.
func (a *appState) clearSceneMute(it *item) error {
	if it == nil || it.audio == "" {
		return fmt.Errorf("source has no audio track to unmute in the scene")
	}
	level, routed := a.sceneLevel(it)
	if !routed {
		return fmt.Errorf("%s is not routed into this scene", sourceDisplayName(it))
	}
	if !level.Muted {
		return nil
	}
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, level.Volume, false); err != nil {
		return err
	}
	a.apply()
	if err := a.saveSession(); err != nil {
		return err
	}
	a.invalidate()
	return nil
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
	if it.openErr != nil {
		// A source whose player never opened is silent whatever the overlay says;
		// a row that called it heard would be lying about the one thing a mixer is
		// supposed to report.
		reasons = append(reasons, "its player did not open")
	}
	if it.muted {
		reasons = append(reasons, "muted")
	}
	if level, routed := a.sceneLevel(it); routed && level.Muted {
		// The scene can hold a mute of its own (another surface, a restored
		// layout). The player is muted from it, so a row that ignored it would
		// claim a silent source is heard.
		reasons = append(reasons, "the scene's own mute")
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
	if a.shownLevel(it) <= 0 {
		reasons = append(reasons, "volume is zero")
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
	line := fmt.Sprintf("%s · %s · %.0f%%", sourceDisplayName(it), state, a.shownLevel(it)*100)
	if a.solo != "" && a.solo == it.source.ID {
		line += " · solo"
	}
	if reasons := a.silences(it); len(reasons) > 0 {
		line += " · " + joinWords(reasons)
	}
	return line
}

// localizedMixerLine is the user-facing counterpart of mixerLine. Diagnostic
// and scenario code keeps using the stable English reason tokens above.
func (a *appState) localizedMixerLine(it *item) string {
	state := a.tr("silent", "无声")
	if a.heard(it) {
		state = a.tr("audible", "可听")
	}
	line := fmt.Sprintf("%s · %s · %.0f%%", sourceDisplayName(it), state, a.shownLevel(it)*100)
	if a.solo != "" && a.solo == it.source.ID {
		line += " · " + a.tr("solo", "独听")
	}
	if reasons := a.silences(it); len(reasons) > 0 {
		localized := make([]string, 0, len(reasons))
		for _, reason := range reasons {
			localized = append(localized, a.localizedSilenceReason(reason))
		}
		line += " · " + strings.Join(localized, a.tr(", ", "、"))
	}
	return line
}

func (a *appState) localizedSilenceReason(reason string) string {
	switch reason {
	case "not in the scene":
		return a.tr("not routed into this scene", "未接入此场景")
	case "its player did not open":
		return a.tr("player did not open", "播放器未能打开")
	case "muted":
		return a.tr("source mute is on", "本路静音已开启")
	case "the scene's own mute":
		return a.tr("scene mute is on", "场景静音已开启")
	case "master mute":
		return a.tr("master mute is on", "总静音已开启")
	case "another source is soloed":
		return a.tr("another source is soloed", "另一来源正在独听")
	case "the unaligned protection":
		return a.tr("alignment protection is holding audio", "对齐保护正在拦截声音")
	case "volume is zero":
		return a.tr("volume is zero", "音量为零")
	default:
		return reason
	}
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
