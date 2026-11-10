// P14.3: the one clock rule of the desktop adapter.
//
// A player reports media time on its own stream. The session shares one event
// clock, and a source sits on it at a fixed offset: media = event + offset.
// Every conversion in the adapter goes through the two functions here, so the
// window, the sync commands, and the replay buffer cannot disagree about which
// direction an offset moves a position.
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// errBeforeStart is the refusal at the start of a stream: a player has no
// history before zero, so a target there would be a guess.
var errBeforeStart = errors.New("before the start of the stream")

// mediaFromEvent is the seek target for a moment on the shared clock. It
// reports an overflow or a target before the start of the stream instead of
// clamping, because a clamped seek would silently move the content.
func mediaFromEvent(event, offset time.Duration) (time.Duration, error) {
	target, err := addDuration(event, offset)
	if err != nil {
		return 0, err
	}
	if target < 0 {
		return 0, fmt.Errorf("%w: target %s", errBeforeStart, target.Round(time.Millisecond))
	}
	return target, nil
}

// playableWindow is the part of a stream a player can reach right now: known
// only when the engine reports a duration, because a live list has no end.
type playableWindow struct {
	Known bool
	Start time.Duration
	End   time.Duration
}

func (w playableWindow) String() string {
	if !w.Known {
		return "unknown"
	}
	return w.Start.Round(time.Millisecond).String() + ".." + w.End.Round(time.Millisecond).String()
}

// seekOutcome is one seek in the terms that can be checked: what was requested,
// what the player reports afterwards, the playable window at that moment, the
// residual between requested and actual, and the wall clock of the request and
// of the reading. Wall clock is recorded, never used as the shared clock.
type seekOutcome struct {
	Requested   time.Duration
	Actual      time.Duration
	Residual    time.Duration
	Window      playableWindow
	RequestedAt time.Time
	ReadAt      time.Time
}

// seekAndMeasure seeks a player and reads back what it did, so a caller can
// report a residual instead of trusting the requested value.
func seekAndMeasure(p desktopPlayer, target time.Duration) seekOutcome {
	outcome := seekOutcome{Requested: target, RequestedAt: time.Now()}
	if p == nil {
		outcome.ReadAt = outcome.RequestedAt
		return outcome
	}
	before := engineStateFrom(p)
	if before.Dur > 0 {
		outcome.Window = playableWindow{Known: true, Start: 0, End: before.Dur}
	}
	p.Seek(target)
	after := engineStateFrom(p)
	outcome.Actual = after.Pos
	outcome.Residual = after.Pos - target
	outcome.ReadAt = time.Now()
	return outcome
}

// eventFromMedia is the inverse: where a buffered media range sits on the
// shared clock.
func eventFromMedia(media, offset time.Duration) (time.Duration, error) {
	event, err := subtractDuration(media, offset)
	if err != nil {
		return 0, err
	}
	return event, nil
}

// subtractDuration subtracts b from a, reporting a difference a duration cannot
// hold.
func subtractDuration(a, b time.Duration) (time.Duration, error) {
	next := a - b
	if (b < 0 && next < a) || (b > 0 && next > a) {
		return 0, fmt.Errorf("%w: time overflow", core.ErrTimeRange)
	}
	return next, nil
}
