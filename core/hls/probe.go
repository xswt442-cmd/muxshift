package hls

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	gohlslib "github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"

	"github.com/xswt442-cmd/muxshift/core"
)

// TrackInfo describes one track a stream announced and the first sample it
// delivered.
type TrackInfo struct {
	// Index is the position of the track in the stream's track list. It stays
	// the same across Probe and Attach, so callers can map one to the other.
	Index int
	// Kind is video or audio. Tracks with no Core kind (KLV metadata) are not
	// reported.
	Kind core.MediaKind
	// Codec is the codec name, as "h264" or "mpeg4-audio".
	Codec string
	// ClockRate is the clock in ticks per second that the stream's timestamps
	// are measured in. In MPEG-TS both video and audio use 90000, so this is
	// the container timescale rather than an audio sample rate; audio sample
	// rates belong to decoding and are not reported here.
	ClockRate int
	// Name and Language come from an audio rendition.
	Name     string
	Language string
	// IsDefault reports a rendition the playlist marks as default.
	IsDefault bool

	// FirstPTS and FirstDTS are the first sample's timestamps, converted to
	// the track's own media time.
	FirstPTS time.Duration
	FirstDTS time.Duration
	// HasSample reports whether a sample arrived before the probe stopped. A
	// track without one gives no trustworthy timestamp.
	HasSample bool

	// ProgramDateTime is the wall clock the stream states for the sample,
	// available only when the playlist carries EXT-X-PROGRAM-DATE-TIME.
	ProgramDateTime    time.Time
	HasProgramDateTime bool

	// firstSeen lets the probe record a track once and ignore later samples.
	firstSeen bool
}

// Stream is what an address exposed before playback: its playlist as read and the
// tracks it carries with their first timestamps.
type Stream struct {
	// URI is the playlist address, without credentials.
	URI string
	// Playlist is the playlist description. It is empty for a multivariant
	// playlist, which Probe follows to a default variant instead.
	Playlist Info
	// Tracks are the usable tracks in stream order.
	Tracks []TrackInfo
}

// Probe reads the playlist at rawURL and the first sample of every usable
// track. It starts a gohlslib client, waits for the tracks and their first
// samples, then closes the client: no segment beyond the first is kept and
// nothing is written to disk. ctx cancels the wait and releases the client.
func Probe(ctx context.Context, rawURL string, opts Options) (Stream, error) {
	endpoint, err := checkURI(rawURL)
	if err != nil {
		return Stream{}, err
	}

	info, err := Read(ctx, endpoint.String(), opts)
	if err != nil && !errors.Is(err, ErrMasterPlaylist) {
		return Stream{}, err
	}

	state := &probeState{
		byTrack: make(map[*gohlslib.Track]*TrackInfo),
		ready:   make(chan struct{}),
	}

	client := &gohlslib.Client{
		URI:           endpoint.String(),
		HTTPClient:    opts.client(),
		StartDistance: opts.StartDistance,
		MaxDistance:   opts.MaxDistance,
	}
	client.OnTracks = func(tracks []*gohlslib.Track) error {
		return state.onTracks(client, tracks)
	}
	if err := client.Start(); err != nil {
		return Stream{}, fmt.Errorf("start hls client for %s: %w", endpoint.Redacted(), err)
	}
	defer client.Close()

	settled := make(chan error, 1)
	go func() { settled <- client.Wait2() }()

	timer := time.NewTimer(opts.timeout())
	defer timer.Stop()

	select {
	case <-state.ready:
	case err := <-settled:
		return Stream{}, fmt.Errorf("read %s: %w", endpoint.Redacted(), err)
	case <-ctx.Done():
		return Stream{}, ctx.Err()
	case <-timer.C:
		return Stream{}, fmt.Errorf("%w: %s", ErrTimeout, endpoint.Redacted())
	}

	stream := Stream{URI: endpoint.Redacted(), Playlist: info, Tracks: state.snapshot()}
	for i := range stream.Tracks {
		if !stream.Tracks[i].HasSample {
			continue
		}
		if stamp, ok := client.AbsoluteTime(state.tracks[i]); ok {
			stream.Tracks[i].ProgramDateTime = stamp
			stream.Tracks[i].HasProgramDateTime = true
		}
	}
	if len(stream.Tracks) == 0 {
		return Stream{}, fmt.Errorf("%w: %s", ErrNoTracks, endpoint.Redacted())
	}
	return stream, nil
}

// probeState collects tracks and their first samples from the client callbacks.
type probeState struct {
	mu      sync.Mutex
	infos   []*TrackInfo
	tracks  []*gohlslib.Track
	byTrack map[*gohlslib.Track]*TrackInfo
	pending int
	ready   chan struct{}
	closed  bool
}

// onTracks records the announced tracks and subscribes to their first samples.
// It runs on the client goroutine, so it takes the lock.
func (p *probeState) onTracks(client *gohlslib.Client, tracks []*gohlslib.Track) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for index, track := range tracks {
		kind, codec, register := describeCodec(track.Codec)
		if register == nil {
			continue
		}
		info := &TrackInfo{
			Index:     index,
			Kind:      kind,
			Codec:     codec,
			ClockRate: track.ClockRate,
			Name:      track.Name,
			Language:  track.Language,
			IsDefault: track.IsDefault,
		}
		p.infos = append(p.infos, info)
		p.tracks = append(p.tracks, track)
		p.byTrack[track] = info
		p.pending++
		register(client, track, func(pts, dts int64) { p.sample(info, pts, dts) })
	}
	p.settleLocked()
	return nil
}

// sample records the first sample of a track and releases the wait once every
// announced track has one.
func (p *probeState) sample(info *TrackInfo, pts, dts int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if info.firstSeen {
		return
	}
	info.firstSeen = true
	info.HasSample = true
	info.FirstPTS = ticksToDuration(pts, info.ClockRate)
	info.FirstDTS = ticksToDuration(dts, info.ClockRate)
	p.pending--
	p.settleLocked()
}

// settleLocked closes the ready channel when nothing is left to wait for.
func (p *probeState) settleLocked() {
	if p.closed || p.pending > 0 {
		return
	}
	p.closed = true
	close(p.ready)
}

// snapshot copies the collected tracks in stream order.
func (p *probeState) snapshot() []TrackInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]TrackInfo, 0, len(p.infos))
	for _, info := range p.infos {
		copied := *info
		copied.firstSeen = false
		out = append(out, copied)
	}
	return out
}

// ticksToDuration converts a track timestamp to media time. A track without a
// clock rate keeps its ticks as nanoseconds rather than inventing a rate.
func ticksToDuration(ticks int64, clockRate int) time.Duration {
	if clockRate <= 0 {
		return time.Duration(ticks)
	}
	return time.Duration(float64(ticks) / float64(clockRate) * float64(time.Second))
}

// describeCodec maps a stream codec to the Core media kind, a stable name, and
// the callback that delivers its samples. A codec with no Core kind returns a
// nil callback and is skipped instead of being forced into one.
func describeCodec(codec codecs.Codec) (core.MediaKind, string, func(*gohlslib.Client, *gohlslib.Track, func(int64, int64))) {
	switch codec.(type) {
	case *codecs.H264:
		return core.MediaVideo, "h264", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataH26x(t, func(pts, dts int64, _ [][]byte) { capture(pts, dts) })
		}
	case *codecs.H265:
		return core.MediaVideo, "h265", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataH26x(t, func(pts, dts int64, _ [][]byte) { capture(pts, dts) })
		}
	case *codecs.AV1:
		return core.MediaVideo, "av1", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataAV1(t, func(pts int64, _ [][]byte) { capture(pts, 0) })
		}
	case *codecs.VP9:
		return core.MediaVideo, "vp9", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataVP9(t, func(pts int64, _ []byte) { capture(pts, 0) })
		}
	case *codecs.MPEG4Audio:
		return core.MediaAudio, "mpeg4-audio", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataMPEG4Audio(t, func(pts int64, _ [][]byte) { capture(pts, 0) })
		}
	case *codecs.Opus:
		return core.MediaAudio, "opus", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataOpus(t, func(pts int64, _ [][]byte) { capture(pts, 0) })
		}
	case *codecs.FLAC:
		return core.MediaAudio, "flac", func(c *gohlslib.Client, t *gohlslib.Track, capture func(int64, int64)) {
			c.OnDataFLAC(t, func(pts int64, _ []byte) { capture(pts, 0) })
		}
	}
	return 0, "", nil
}
