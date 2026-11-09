package hls

import (
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

func probeStream(tracks ...TrackInfo) Stream {
	return Stream{URI: "https://example.com/live.m3u8", Tracks: tracks}
}

func datedPTS(base time.Time, pts time.Duration) TrackInfo {
	return TrackInfo{
		ProgramDateTime:    base.Add(pts),
		HasProgramDateTime: true,
		FirstPTS:           pts,
		HasSample:          true,
	}
}

// TestAttachCreatesSourceAndTracks checks that a probed stream lands in the
// session as one source with one Core track per usable track, labelled by codec.
func TestAttachCreatesSourceAndTracks(t *testing.T) {
	session := core.NewSession("attach")
	t.Cleanup(func() { session.Close() })

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	video := datedPTS(base, 0)
	video.Index, video.Kind, video.Codec, video.ClockRate = 0, core.MediaVideo, "h264", 90000
	audio := datedPTS(base, 45*time.Millisecond)
	audio.Index, audio.Kind, audio.Codec, audio.ClockRate, audio.Language = 1, core.MediaAudio, "mpeg4-audio", 90000, "en"

	src, attachments, err := probeStream(video, audio).Attach(session)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if src.URI != "https://example.com/live.m3u8" {
		t.Fatalf("source uri = %q", src.URI)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(attachments))
	}
	if got := attachments[0]; got.Index != 0 || got.Track.Kind != core.MediaVideo || got.Track.Label != "h264" || got.Track.Source != src.ID {
		t.Fatalf("video attachment = %+v", got)
	}
	if got := attachments[1]; got.Index != 1 || got.Track.Kind != core.MediaAudio || got.Track.Label != "mpeg4-audio en" {
		t.Fatalf("audio attachment = %+v", got)
	}

	if sources := session.Sources(); len(sources) != 1 {
		t.Fatalf("session sources = %d, want 1", len(sources))
	}
	if tracks := session.Tracks(); len(tracks) != 2 {
		t.Fatalf("session tracks = %d, want 2", len(tracks))
	}
}

// TestAttachLeavesSessionUnchangedOnFailure checks the rollback: a track Core
// cannot represent must not leave a source behind with half its tracks.
func TestAttachLeavesSessionUnchangedOnFailure(t *testing.T) {
	session := core.NewSession("attach-failure")
	t.Cleanup(func() { session.Close() })

	video := TrackInfo{Index: 0, Kind: core.MediaVideo, Codec: "h264"}
	audio := TrackInfo{Index: 1, Kind: core.MediaKind(99), Codec: "mystery"}

	if _, _, err := probeStream(video, audio).Attach(session); !errorsIs(err, core.ErrMediaKind) {
		t.Fatalf("attach error = %v, want ErrMediaKind", err)
	}
	if sources := session.Sources(); len(sources) != 0 {
		t.Fatalf("sources after failed attach = %d, want none", len(sources))
	}
	if tracks := session.Tracks(); len(tracks) != 0 {
		t.Fatalf("tracks after failed attach = %d, want none", len(tracks))
	}
}

// TestAlignAnchorsOnEarliestDate checks the offset sign: a track whose content
// starts later than the earliest track has not reached the shared event yet, so
// its offset is negative.
func TestAlignAnchorsOnEarliestDate(t *testing.T) {
	session := core.NewSession("align")
	t.Cleanup(func() { session.Close() })

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	early := datedPTS(base, 0)
	early.Index, early.Kind, early.Codec = 0, core.MediaVideo, "h264"
	late := datedPTS(base, 3200*time.Millisecond)
	late.Index, late.Kind, late.Codec = 1, core.MediaAudio, "mpeg4-audio"

	stream := probeStream(late, early)
	alignments, err := stream.Align()
	if err != nil {
		t.Fatalf("align: %v", err)
	}
	if len(alignments) != 2 {
		t.Fatalf("alignments = %d, want 2", len(alignments))
	}
	if got := alignments[0]; got.Index != 1 || got.Offset != -3200*time.Millisecond {
		t.Fatalf("late track alignment = %+v, want index 1 at -3.2s", got)
	}
	if got := alignments[1]; got.Index != 0 || got.Offset != 0 {
		t.Fatalf("early track alignment = %+v, want index 0 at zero", got)
	}

	src, attachments, err := stream.Attach(session)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	_ = src
	line := core.NewTimeline()
	if err := stream.Apply(line, attachments); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// At event time 10s the early track plays 10s of its media, while the late
	// track plays 6.8s of its own.
	for _, attachment := range attachments {
		want := 10 * time.Second
		if attachment.Track.Kind == core.MediaAudio {
			want = 6800 * time.Millisecond
		}
		got, err := line.MediaTime(attachment.Track.ID, 10*time.Second)
		if err != nil {
			t.Fatalf("media time: %v", err)
		}
		if got != want {
			t.Fatalf("track %s media time at 10s = %s, want %s", attachment.Track.Label, got, want)
		}
	}
}

// TestAlignWithoutProgramDateTimeRefusesToGuess checks that an unlabelled
// stream yields no offsets and changes no timeline.
func TestAlignWithoutProgramDateTimeRefusesToGuess(t *testing.T) {
	session := core.NewSession("align-unlabelled")
	t.Cleanup(func() { session.Close() })

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	labelled := datedPTS(base, 0)
	labelled.Index, labelled.Kind, labelled.Codec = 0, core.MediaVideo, "h264"
	unlabelled := TrackInfo{Index: 1, Kind: core.MediaAudio, Codec: "mpeg4-audio", FirstPTS: 45 * time.Millisecond, HasSample: true}

	stream := probeStream(labelled, unlabelled)
	if _, err := stream.Align(); !errorsIs(err, core.ErrNotAligned) {
		t.Fatalf("align error = %v, want ErrNotAligned", err)
	}

	_, attachments, err := stream.Attach(session)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	line := core.NewTimeline()
	if err := stream.Apply(line, attachments); !errorsIs(err, core.ErrNotAligned) {
		t.Fatalf("apply error = %v, want ErrNotAligned", err)
	}
	if shifts := line.Shifts(); len(shifts) != 0 {
		t.Fatalf("timeline changed despite an unlabelled track: %+v", shifts)
	}
	for _, attachment := range attachments {
		if line.Aligned(attachment.Track.ID) {
			t.Fatalf("track %s was aligned without a date", attachment.Track.Label)
		}
	}
}

// TestApplyRejectsBadAttachments checks the pre-checks: a duplicate index, an
// attachment without a track, and a probe track with no Core track are all
// refused before any offset is stored.
func TestApplyRejectsBadAttachments(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	video := datedPTS(base, 0)
	video.Index, video.Kind, video.Codec = 0, core.MediaVideo, "h264"
	audio := datedPTS(base, 0)
	audio.Index, audio.Kind, audio.Codec = 1, core.MediaAudio, "mpeg4-audio"
	stream := probeStream(video, audio)

	newSession := func(t *testing.T) (*core.Session, []Attachment) {
		t.Helper()
		session := core.NewSession("apply-checks")
		t.Cleanup(func() { session.Close() })
		_, attachments, err := stream.Attach(session)
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		return session, attachments
	}

	t.Run("nil timeline", func(t *testing.T) {
		_, attachments := newSession(t)
		if err := stream.Apply(nil, attachments); !errorsIs(err, core.ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})

	t.Run("duplicate index", func(t *testing.T) {
		_, attachments := newSession(t)
		duplicated := append(append([]Attachment{}, attachments...), attachments[0])
		if err := stream.Apply(core.NewTimeline(), duplicated); !errorsIs(err, core.ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})

	t.Run("missing track", func(t *testing.T) {
		_, attachments := newSession(t)
		broken := append([]Attachment{}, attachments...)
		broken[1].Track = core.Track{}
		if err := stream.Apply(core.NewTimeline(), broken); !errorsIs(err, core.ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})

	t.Run("probe track without core track", func(t *testing.T) {
		_, attachments := newSession(t)
		if err := stream.Apply(core.NewTimeline(), attachments[:1]); !errorsIs(err, core.ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})
}
