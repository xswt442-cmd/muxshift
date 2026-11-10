package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/align"
)

// The alignment probe reads the audio of two sources, compares their envelopes
// and offers an offset. It never writes the timeline and never moves a player:
// every result below is a proposal that the caller may show, ignore or reject.
//
// The offset it proposes follows the Core convention
//
//	targetMediaTime = sharedEventTime + mappedOffset
//
// so a negative value makes this source play earlier content at the same event
// time. The local match works on media time only; the media start times of the
// two windows are added back here, because two streams rarely begin at the same
// moment of the event:
//
//	CoreOffset = (MediaStart_B - MediaStart_A) + LocalMatchOffset

// alignWindow describes the media window that was actually compared.
type alignWindow struct {
	Source     core.SourceID
	Index      int
	URI        string
	Generation int
	Start      time.Duration
	Length     time.Duration
}

func (w alignWindow) String() string {
	return fmt.Sprintf("source %d from %s for %s", w.Index+1, w.Start, w.Length)
}

// alignSuggestion is one probe result together with the state it was computed
// from. A caller must check fresh() before showing it.
type alignSuggestion struct {
	Reference  alignWindow
	Target     alignWindow
	Result     align.Result
	CoreOffset time.Duration
	Current    time.Duration
	Delta      time.Duration
	Group      core.SyncGroupID
	GroupShift time.Duration
	Elapsed    time.Duration
}

// probeLength bounds how much audio is decoded for one comparison: a minute at
// 16 kHz mono is small enough to hold in memory and long enough for the envelope
// correlation to separate the peak from its surroundings.
const probeLength = 60 * time.Second

// suggestAlign compares two sources and returns a proposal. It reports an error
// only when the comparison could not run at all; a comparison that ran but found
// nothing convincing comes back with Result.Accepted false and a reason.
func (a *appState) suggestAlign(reference, target int) (alignSuggestion, error) {
	started := time.Now()
	ref, err := a.windowOnMain(reference)
	if err != nil {
		return alignSuggestion{}, err
	}
	tgt, err := a.windowOnMain(target)
	if err != nil {
		return alignSuggestion{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	first, err := decodeEnvelope(ctx, ref.URI, ref.Start, ref.Length)
	if err != nil {
		return alignSuggestion{}, fmt.Errorf("%s: %w", shortURI(ref.URI), err)
	}
	second, err := decodeEnvelope(ctx, tgt.URI, tgt.Start, tgt.Length)
	if err != nil {
		return alignSuggestion{}, fmt.Errorf("%s: %w", shortURI(tgt.URI), err)
	}

	result, err := align.Compare(first, second, align.DefaultLimits())
	if err != nil {
		return alignSuggestion{}, err
	}

	suggestion := alignSuggestion{Reference: ref, Target: tgt, Result: result, Elapsed: time.Since(started)}
	if result.Accepted {
		suggestion.CoreOffset = tgt.Start - ref.Start + result.Offset
	}
	// Reading the current offset needs the session, so it happens on the UI
	// thread even when the comparison above ran on its own goroutine.
	finish := func() { a.finishSuggestion(&suggestion) }
	mygoRunOnMain(finish)
	return suggestion, nil
}

// windowOnMain reads one player's state on the UI thread: the player and the
// session are owned there, so the goroutine that runs the comparison must not
// touch them directly.
func (a *appState) windowOnMain(index int) (alignWindow, error) {
	var window alignWindow
	var err error
	mygoRunOnMain(func() { window, err = a.alignWindowAt(index) })
	return window, err
}

// finishSuggestion fills in the parts of a proposal that describe the current
// session. It only reads, and it finds the source by identity: a source that was
// reordered since the probe must not be confused with another one.
func (a *appState) finishSuggestion(s *alignSuggestion) {
	track := a.trackOfAudio(a.itemOf(s.Target.Source))
	if s.Result.Accepted {
		if shift, ok := a.sync.timeline.Shift(track); ok {
			s.Current = shift.Offset
		}
		s.Delta = s.CoreOffset - s.Current
	}
	if group, ok := a.existingGroup(); ok {
		s.Group = group.ID
		if shift, ok := a.sync.timeline.Shift(track); ok {
			s.GroupShift = shift.Offset
		}
	}
}

// itemOf finds the source a probe was computed from, by identity rather than by
// position, and returns nil when it is gone.
func (a *appState) itemOf(id core.SourceID) *item {
	for _, it := range a.items {
		if it.source.ID == id {
			return it
		}
	}
	return nil
}

// trackOfAudio names the track whose shift a probe describes: the audio track
// when there is one, because that is what was compared.
func (a *appState) trackOfAudio(it *item) core.TrackID {
	if it == nil {
		return ""
	}
	if it.audio != "" {
		return it.audio
	}
	return it.video
}

// alignWindowAt captures the media window of one source. The generation is part
// of the window so a result computed for an older player can be discarded.
func (a *appState) alignWindowAt(index int) (alignWindow, error) {
	it, err := a.at(index)
	if err != nil {
		return alignWindow{}, err
	}
	if it.player == nil {
		return alignWindow{}, fmt.Errorf("%s has no player", a.name(it))
	}
	if err := it.player.Err(); err != nil {
		return alignWindow{}, fmt.Errorf("%s is not playable: %w", a.name(it), err)
	}
	// The length comes from the player that already decoded the stream, so the
	// probe does not need a second tool just to ask how long the media is.
	length := it.player.Duration()
	if length <= 0 {
		return alignWindow{}, fmt.Errorf("%s has no readable duration (a live stream cannot be compared yet)", shortURI(it.uri))
	}
	if length > probeLength {
		length = probeLength
	}
	return alignWindow{
		Source:     it.source.ID,
		Index:      index,
		URI:        it.uri,
		Generation: it.generation,
		Start:      0,
		Length:     length,
	}, nil
}

// decodeEnvelope turns a slice of one source into the envelope the matcher uses.
// The desktop cannot ask MyGo for PCM, so this decodes the same address with
// ffmpeg; the offset comes from the bytes of the stream, not from the player.
func decodeEnvelope(ctx context.Context, uri string, start, length time.Duration) (align.Envelope, error) {
	command := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
		"-ss", seconds(start), "-t", seconds(length),
		"-i", uri, "-map", "0:a:0", "-ac", "1", "-ar", "16000", "-f", "s16le", "-")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, err := command.Output()
	if err != nil {
		return align.Envelope{}, fmt.Errorf("cannot decode audio: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	if len(raw) < 2 {
		return align.Envelope{}, fmt.Errorf("the source has no audio track")
	}
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return align.FromInt16(samples, 16000, align.DefaultHop)
}

// seconds renders a duration for ffmpeg without a Go duration suffix.
func seconds(value time.Duration) string {
	return strconv.FormatFloat(value.Seconds(), 'f', 3, 64)
}

// alignTrack is the track whose shift a suggestion would describe: the audio
// track when the source has one, because that is what was compared.
func (a *appState) alignTrack(index int) core.TrackID {
	if it, err := a.at(index); err == nil {
		if it.audio != "" {
			return it.audio
		}
		return it.video
	}
	return ""
}

// fresh reports why a suggestion can no longer be applied, or "" when it still
// describes the current session. Nothing here changes state.
func (a *appState) fresh(s alignSuggestion) string {
	for _, window := range []alignWindow{s.Reference, s.Target} {
		it := a.itemOf(window.Source)
		if it == nil {
			return fmt.Sprintf("source %d is gone", window.Index+1)
		}
		if it.generation != window.Generation {
			return fmt.Sprintf("%s was reopened since the probe", a.name(it))
		}
	}
	if s.Result.Accepted {
		track := a.trackOfAudio(a.itemOf(s.Target.Source))
		current, ok := a.sync.timeline.Shift(track)
		switch {
		case !ok && s.Current != 0:
			return "the offset of the target changed since the probe"
		case ok && current.Offset != s.Current:
			return "the offset of the target changed since the probe"
		}
	}
	if group, ok := a.existingGroup(); ok {
		if s.Group != "" && group.ID != s.Group {
			return "the sync group changed since the probe"
		}
	} else if s.Group != "" {
		return "the sync group is gone since the probe"
	}
	return ""
}

// suggestionLine renders a proposal for the status bar. It is deliberately
// verbose: the user must be able to see why a value was proposed or refused.
func (a *appState) suggestionLine(s alignSuggestion) string {
	if !s.Result.Accepted {
		return fmt.Sprintf("no suggestion for %s: %s (peak %.3f, next %.3f, %s)",
			shortURI(s.Target.URI), s.Result.Reason, s.Result.Peak, s.Result.RunnerUp, s.Elapsed.Round(time.Millisecond))
	}
	line := fmt.Sprintf("suggested %+.3fs for %s (now %+.3fs, difference %+.3fs); peak %.3f vs %.3f, overlap %s, %s",
		s.CoreOffset.Seconds(), shortURI(s.Target.URI), s.Current.Seconds(), s.Delta.Seconds(),
		s.Result.Peak, s.Result.RunnerUp, s.Result.Overlap.Round(10*time.Millisecond), s.Elapsed.Round(time.Millisecond))
	if stalled := a.fresh(s); stalled != "" {
		return line + " (expired: " + stalled + ")"
	}
	return line
}

// probeAndReport runs a suggestion and records it in the status bar and the log.
// It is the only place the button and the scenario share, so both exercise the
// same read-only path.
func (a *appState) probeAndReport(reference, target int) alignSuggestion {
	a.syncNote = ""
	suggestion, err := a.suggestAlign(reference, target)
	if err != nil {
		a.syncNote = fmt.Sprintf("alignment probe failed: %v", err)
		logf("align", "probe failed: %v", err)
		return suggestion
	}
	a.syncNote = a.suggestionLine(suggestion)
	logf("align", "%s", a.syncNote)
	return suggestion
}

// probeAsync is the button's path: the comparison decodes two files, so it runs
// off the UI thread and only the finished text is handed back to it.
func (a *appState) probeAsync(reference, target int) {
	a.syncNote = fmt.Sprintf("comparing source %d with source %d…", reference+1, target+1)
	go func() {
		suggestion, err := a.suggestAlign(reference, target)
		mygoRunOnMain(func() {
			if err != nil {
				a.syncNote = fmt.Sprintf("alignment probe failed: %v", err)
				logf("align", "probe failed: %v", err)
				return
			}
			a.syncNote = a.suggestionLine(suggestion)
			logf("align", "%s", a.syncNote)
		})
	}()
}

// logStale re-reads the current session and reports whether an older suggestion
// still describes it. The scenario uses it to prove stale results are dropped.
func (a *appState) logStale(s alignSuggestion) {
	if reason := a.fresh(s); reason != "" {
		logf("align", "the earlier suggestion is stale: %s", reason)
		return
	}
	logf("align", "the earlier suggestion still matches the session")
}
