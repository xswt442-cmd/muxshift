// P14.2: the thin player adapter. The window has one engine — MyGo's
// ui.VideoPlayer — and this file is the seam the rest of the desktop code
// reaches it through: one operation surface, one normalized state value, and one
// place that records the thread rule the engine imposes.
package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// playback is the read-only surface of a player. *ui.VideoPlayer satisfies it
// as is; the interface exists for the snapshot code and its tests, not to
// pretend another engine runs here.
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

// desktopPlayer is the operation surface the adapter exposes: the calls the
// window really makes, and nothing beyond them. MyGo's setters are fluent
// (they return *ui.VideoPlayer), so the interface names that return type: it is
// the engine this adapter exists for, and hiding it behind a second abstraction
// would only move the dependency somewhere less visible.
//
// The engine's thread rule survives the adapter: a player is created, drawn, and
// closed on the drawing thread (mygoRunOnMain), and the adapter does not hide a
// call that must run there.
type desktopPlayer interface {
	playback

	Play()
	Pause()
	Seek(time.Duration)
	SetVolume(float64) *ui.VideoPlayer
	SetMuted(bool) *ui.VideoPlayer
	Close()
}

// The only implementation is the engine's own player; this assertion fails to
// build if a MyGo update drops or renames one of the calls above.
var _ desktopPlayer = (*ui.VideoPlayer)(nil)

// rawPlayer unwraps the engine player for drawing and for the handover, which
// deals in concrete players because it closes them on the drawing thread.
func rawPlayer(p desktopPlayer) *ui.VideoPlayer {
	if p == nil {
		return nil
	}
	video, _ := p.(*ui.VideoPlayer)
	return video
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
