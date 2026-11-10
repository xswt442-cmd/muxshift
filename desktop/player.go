// P14.2: the thin player adapter. The window has one engine — MyGo's
// ui.VideoPlayer — and this file is the seam the rest of the desktop code reads
// it through, so no caller repeats the same five getters and so a fake can
// stand in for tests without opening a window.
package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// playback is the read-only surface of a player. *ui.VideoPlayer satisfies it
// as is; the interface exists for the snapshot code and its tests, not to
// pretend another engine runs here. Control methods (play, pause, seek, volume,
// close) stay on the concrete pointer, because MyGo's setters are fluent and
// returning *ui.VideoPlayer would leak the engine type back through an
// interface that is meant to describe state only.
type playback interface {
	Ready() bool
	Playing() bool
	Buffering() bool
	Position() time.Duration
	Duration() time.Duration
	Volume() float64
	Muted() bool
	Err() error
}

// engineState is one player's state at one moment: a nil player reads as the
// zero state instead of panicking, and a nil error reads as no error at all.
type engineState struct {
	Ready   bool
	Playing bool
	Buf     bool
	Pos     time.Duration
	Dur     time.Duration
	Volume  float64
	Muted   bool
	Err     string
}

// stateFrom normalizes whatever the engine reports.
func engineStateFrom(p playback) engineState {
	var state engineState
	state.Ready = p.Ready()
	state.Playing = p.Playing()
	state.Buf = p.Buffering()
	state.Pos = p.Position()
	state.Dur = p.Duration()
	state.Volume = p.Volume()
	state.Muted = p.Muted()
	if err := p.Err(); err != nil {
		state.Err = err.Error()
	}
	return state
}

// stateOf reads a player that may be absent, which is how a source without a
// live player is described.
func readPlayer(p *ui.VideoPlayer) engineState {
	if p == nil {
		return engineState{}
	}
	return engineStateFrom(p)
}

// ready reports whether a source has a picture that is safe to show.
func (s engineState) ready() bool {
	return s.Ready && s.Err == ""
}
