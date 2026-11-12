package main

import (
	"fmt"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// timelineRow is one source's line in the read-only timeline view (P17.1).
//
// The view says what the session knows and says "unknown" where it knows
// nothing. In particular a track with no shift is not aligned, which is not the
// same thing as being aligned with an offset of zero: the dock used to print
// "+0.0s" for both, and that made an unverified source look calibrated.
//
// The three times stay apart on purpose:
//
//   - Event is the shared time the playhead holds.
//   - Media is what the timeline maps that event to for this track. It is a
//     request, not a measurement, and it is unknown for an unaligned track.
//   - Position is what the player itself reports. When the player is showing a
//     recording, that number is a position inside that file, which is not the
//     same clock as the buffer's content time (P16), so the kind travels with
//     the number.
type timelineRow struct {
	Index      int
	Source     string
	Track      core.TrackID
	Reference  bool
	Aligned    bool
	Basis      string
	Offset     time.Duration
	Event      time.Duration
	Media      time.Duration
	MediaOK    bool
	Position   time.Duration
	PositionOK bool
	File       bool
}

// timelineRows reads the session into one row per source. It reads only:
// building rows never sets a shift, moves the playhead or touches a player.
func (a *appState) timelineRows() []timelineRow {
	reference := a.referenceTrack()
	event := a.sync.playhead.Event()
	rows := make([]timelineRow, 0, len(a.items))
	for index, it := range a.items {
		track := a.rowTrack(it, reference)
		row := timelineRow{Index: index, Source: it.uri, Track: track,
			Reference: track != "" && track == reference, Event: event}
		if track == "" {
			rows = append(rows, row)
			continue
		}
		if shift, ok := a.sync.timeline.Shift(track); ok {
			row.Aligned = true
			row.Offset = shift.Offset
			if origin, known := a.sync.originOf(track); known {
				row.Basis = string(origin.basis)
			} else {
				row.Basis = "unrecorded"
			}
			if media, err := a.sync.timeline.MediaTime(track, event); err == nil {
				row.Media, row.MediaOK = media, true
			}
		}
		if it.player != nil && it.player.Ready() {
			row.Position, row.PositionOK = it.player.Position(), true
			row.File = it.replay != nil
		}
		rows = append(rows, row)
	}
	return rows
}

// referenceTrack reports the track the session's sync group calls its
// reference, or "" when no group exists yet. It is a lookup, not a guess: a
// source being first in the window says nothing about who is the reference.
func (a *appState) referenceTrack() core.TrackID {
	if a.session == nil || a.sync.group == "" {
		return ""
	}
	for _, group := range a.session.SyncGroups() {
		if group.ID == a.sync.group {
			return group.Reference
		}
	}
	return ""
}

// line is what the dock draws for one row. It keeps the numbers apart: the
// mapped media time is labelled as a request, and a player position carries the
// clock it belongs to.
func (r timelineRow) line() string {
	name := fmt.Sprintf("%d %s", r.Index+1, shortURI(r.Source))
	track := "(no track)"
	if r.Track != "" {
		track = string(r.Track)
	}
	if r.Reference {
		track += " ref"
	}
	player := "not open"
	if r.PositionOK {
		clock := "stream"
		if r.File {
			clock = "file"
		}
		player = fmt.Sprintf("%s %s", clock, sceneClock(r.Position))
	}
	if !r.Aligned {
		return fmt.Sprintf("%s · %s · not aligned · media unknown · player %s", name, track, player)
	}
	media := "unknown"
	if r.MediaOK {
		media = sceneClock(r.Media)
	}
	basis := r.Basis
	if basis == "" {
		basis = "unrecorded"
	}
	return fmt.Sprintf("%s · %s · %s · offset %+.1fs · media %s · player %s",
		name, track, basis, r.Offset.Seconds(), media, player)
}

// timelineLines is the view as the dock renders it.
func (a *appState) timelineLines() []string {
	rows := a.timelineRows()
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.line())
	}
	return lines
}

// rowTrack picks the track a source's row describes. When the sync group's
// reference belongs to this source, the row describes that track: Core allows
// an audio track to be the reference, and a video-first choice would then hide
// the very track the group aligns to. Sources without the reference keep their
// video track, or their audio track when they have no video.
func (a *appState) rowTrack(it *item, reference core.TrackID) core.TrackID {
	if reference != "" {
		for _, track := range a.session.Tracks() {
			if track.ID == reference && track.Source == it.source.ID {
				return reference
			}
		}
	}
	if it.video != "" {
		return it.video
	}
	return it.audio
}
