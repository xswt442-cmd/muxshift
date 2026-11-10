package main

import (
	"errors"
	"testing"
	"time"
)

// fakePlayer stands in for MyGo's player in the adapter tests.
type fakePlayer struct {
	ready   bool
	playing bool
	buf     bool
	pos     time.Duration
	dur     time.Duration
	volume  float64
	muted   bool
	err     error
}

func (f fakePlayer) Ready() bool             { return f.ready }
func (f fakePlayer) Playing() bool           { return f.playing }
func (f fakePlayer) Buffering() bool         { return f.buf }
func (f fakePlayer) Position() time.Duration { return f.pos }
func (f fakePlayer) Duration() time.Duration { return f.dur }
func (f fakePlayer) Volume() float64         { return f.volume }
func (f fakePlayer) Muted() bool             { return f.muted }
func (f fakePlayer) Err() error              { return f.err }

func TestStateFromCopiesEveryField(t *testing.T) {
	want := fakePlayer{
		ready: true, playing: true, buf: false,
		pos: 3*time.Second + 250*time.Millisecond, dur: 30 * time.Second,
		volume: 0.4, muted: true,
	}
	state := engineStateFrom(want)
	if !state.Ready || !state.Playing || state.Buf {
		t.Fatalf("flags: ready=%t playing=%t buffering=%t", state.Ready, state.Playing, state.Buf)
	}
	if state.Pos != want.pos || state.Dur != want.dur {
		t.Fatalf("times: pos=%s dur=%s", state.Pos, state.Dur)
	}
	if state.Volume != 0.4 || !state.Muted {
		t.Fatalf("audio: volume=%v muted=%t", state.Volume, state.Muted)
	}
	if state.Err != "" {
		t.Fatalf("unexpected error text %q", state.Err)
	}
	if !state.ready() {
		t.Fatal("a ready player without an error must be ready")
	}
}

func TestStateFromKeepsTheEngineError(t *testing.T) {
	state := engineStateFrom(fakePlayer{ready: true, err: errors.New("no first frame")})
	if state.Err != "no first frame" {
		t.Fatalf("error text = %q", state.Err)
	}
	if state.ready() {
		t.Fatal("a player that reports an error is not ready")
	}
}

func TestStateOfAnAbsentPlayer(t *testing.T) {
	state := readPlayer(nil)
	if state.Ready || state.Playing || state.Pos != 0 || state.Err != "" {
		t.Fatalf("absent player = %+v", state)
	}
	if state.ready() {
		t.Fatal("an absent player is never ready")
	}
}
