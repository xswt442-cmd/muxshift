package main

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fakePlayer stands in for MyGo's player in the adapter tests.
type fakePlayer struct {
	ready      bool
	playing    bool
	buf        bool
	pos        time.Duration
	dur        time.Duration
	volume     float64
	muted      bool
	err        error
	seekOffset time.Duration
}

// seekOffset lets a test pretend the engine lands a little off the request.
func (f *fakePlayer) Play()  {}
func (f *fakePlayer) Pause() {}
func (f *fakePlayer) Seek(d time.Duration) {
	f.pos = d + f.seekOffset
}
func (f *fakePlayer) SetVolume(v float64) *ui.VideoPlayer { f.volume = v; return nil }
func (f *fakePlayer) SetMuted(m bool) *ui.VideoPlayer     { f.muted = m; return nil }
func (f *fakePlayer) Close()                              {}

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

func TestSeekAndMeasureReportsWhatThePlayerDid(t *testing.T) {
	player := &fakePlayer{dur: 30 * time.Second, seekOffset: 120 * time.Millisecond}
	outcome := seekAndMeasure(player, 5*time.Second)
	if outcome.Requested != 5*time.Second {
		t.Fatalf("requested = %s", outcome.Requested)
	}
	if outcome.Actual != 5*time.Second+120*time.Millisecond {
		t.Fatalf("actual = %s", outcome.Actual)
	}
	if outcome.Residual != 120*time.Millisecond {
		t.Fatalf("residual = %s", outcome.Residual)
	}
	if !outcome.Window.Known || outcome.Window.Start != 0 || outcome.Window.End != 30*time.Second {
		t.Fatalf("window = %s", outcome.Window)
	}
	if outcome.RequestedAt.IsZero() || outcome.ReadAt.Before(outcome.RequestedAt) {
		t.Fatalf("wall clock: requested=%v read=%v", outcome.RequestedAt, outcome.ReadAt)
	}
}

func TestSeekAndMeasureOnAnAbsentPlayer(t *testing.T) {
	outcome := seekAndMeasure(nil, 4*time.Second)
	if outcome.Actual != 0 || outcome.Residual != 0 {
		t.Fatalf("absent player outcome = %+v", outcome)
	}
	if outcome.Window.Known {
		t.Fatalf("absent player reported a window: %s", outcome.Window)
	}
}

func TestSeekAndMeasureLeavesLiveWindowsUnknown(t *testing.T) {
	player := &fakePlayer{dur: 0}
	outcome := seekAndMeasure(player, time.Second)
	if outcome.Window.Known {
		t.Fatalf("a player without a duration reported a window: %s", outcome.Window)
	}
	if outcome.Window.String() != "unknown" {
		t.Fatalf("unknown window renders as %q", outcome.Window.String())
	}
}
