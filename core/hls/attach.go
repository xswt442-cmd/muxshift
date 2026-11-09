package hls

import (
	"fmt"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// Label names a probed track in the session: its codec, then the language or
// the rendition name when the stream states one.
func (t TrackInfo) Label() string {
	label := t.Codec
	switch {
	case t.Language != "":
		return label + " " + t.Language
	case t.Name != "":
		return label + " " + t.Name
	}
	return label
}

// Attachment is the Core track created from one probed track.
type Attachment struct {
	// Index is the TrackInfo.Index the track was created from.
	Index int
	// Track is the Core track.
	Track core.Track
}

// Attach adds a source for the probed address and one Core track per probed
// track. It adds all of them or leaves the session unchanged, so a stream that
// cannot be represented does not leave a half-configured source behind.
func (s Stream) Attach(session *core.Session) (core.Source, []Attachment, error) {
	src, err := session.AddSource(s.URI)
	if err != nil {
		return core.Source{}, nil, err
	}

	attachments := make([]Attachment, 0, len(s.Tracks))
	for _, info := range s.Tracks {
		track, err := session.AddTrack(src.ID, info.Kind, info.Label())
		if err != nil {
			if rollback := session.RemoveSource(src.ID); rollback != nil {
				return core.Source{}, nil, fmt.Errorf("%w (rollback failed: %v)", err, rollback)
			}
			return core.Source{}, nil, err
		}
		attachments = append(attachments, Attachment{Index: info.Index, Track: track})
	}
	return src, attachments, nil
}

// Alignment is the event-timeline offset a probed track needs.
type Alignment struct {
	// Index is the TrackInfo.Index the offset belongs to.
	Index int
	// Offset goes into core.Timeline.SetShift: the track's media time at
	// event time zero.
	Offset time.Duration
}

// Align returns the offset of every probed track against the earliest program
// date time among them: a track whose content starts later than that one gets
// a negative offset, because it has not reached the shared event yet. PTS alone
// is not usable here, since a stream's timestamps are normalized to its own
// first sample; a track without a program date time returns core.ErrNotAligned
// instead of a guessed offset.
func (s Stream) Align() ([]Alignment, error) {
	if len(s.Tracks) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoTracks, s.URI)
	}

	var base time.Time
	for _, track := range s.Tracks {
		if !track.HasProgramDateTime {
			return nil, fmt.Errorf("track %d (%s): %w", track.Index, track.Label(), core.ErrNotAligned)
		}
		if base.IsZero() || track.ProgramDateTime.Before(base) {
			base = track.ProgramDateTime
		}
	}

	alignments := make([]Alignment, 0, len(s.Tracks))
	for _, track := range s.Tracks {
		alignments = append(alignments, Alignment{
			Index:  track.Index,
			Offset: -track.ProgramDateTime.Sub(base),
		})
	}
	return alignments, nil
}

// Apply stores the probe's offsets on the timeline, matching attachments by
// track index. It checks every offset before storing any, so a probe with a
// missing track or a missing program date time changes nothing.
func (s Stream) Apply(line *core.Timeline, attachments []Attachment) error {
	if line == nil {
		return fmt.Errorf("%w: no timeline", core.ErrInvalid)
	}
	alignments, err := s.Align()
	if err != nil {
		return err
	}

	byIndex := make(map[int]core.Track, len(attachments))
	for _, attachment := range attachments {
		if _, duplicate := byIndex[attachment.Index]; duplicate {
			return fmt.Errorf("%w: attachment index %d listed twice", core.ErrInvalid, attachment.Index)
		}
		if attachment.Track.ID == "" {
			return fmt.Errorf("%w: attachment index %d has no track", core.ErrInvalid, attachment.Index)
		}
		byIndex[attachment.Index] = attachment.Track
	}
	for _, alignment := range alignments {
		if _, ok := byIndex[alignment.Index]; !ok {
			return fmt.Errorf("%w: probe track %d has no Core track", core.ErrInvalid, alignment.Index)
		}
	}

	for _, alignment := range alignments {
		if err := line.SetShift(byIndex[alignment.Index].ID, alignment.Offset); err != nil {
			return err
		}
	}
	return nil
}
