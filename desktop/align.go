package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"path"
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
	// Track is the track of this source that was decoded. A proposal describes
	// that track's offset, so a report has to name it.
	Track  core.TrackID
	Start  time.Duration
	Length time.Duration
}

func (w alignWindow) String() string {
	return fmt.Sprintf("source %d from %s for %s", w.Index+1, w.Start, w.Length)
}

// alignSuggestion is one probe result together with the state it was computed
// from. A caller must check Expired (or fresh) before showing it as current.
type alignSuggestion struct {
	Reference  alignWindow
	Target     alignWindow
	Snapshot   probeSnapshot
	Result     align.Result
	CoreOffset time.Duration
	Current    time.Duration
	Delta      time.Duration
	Group      core.SyncGroupID
	GroupShift time.Duration
	Elapsed    time.Duration
	Expired    string
}

// probeSnapshot is the session state the probe started from: both windows, the
// target track's offset and the sync group as they were before the decode began.
// The result is checked against this snapshot rather than against the session as
// it looks when the decode finishes, so a change made while the probe ran shows
// up as an expired result instead of being folded into the suggestion.
type probeSnapshot struct {
	Reference alignWindow
	Target    alignWindow
	Track     core.TrackID
	Offset    time.Duration
	Aligned   bool
	Group     core.SyncGroupID
	HasGroup  bool
}

// probeLength bounds how much audio is decoded for one comparison: a minute at
// 16 kHz mono is small enough to hold in memory and long enough for the envelope
// correlation to separate the peak from its surroundings.
const probeLength = 60 * time.Second

// suggestAlign compares two sources and returns a proposal. It reports an error
// only when the comparison could not run at all; a comparison that ran but found
// nothing convincing comes back with Result.Accepted false and a reason.
//
// The context belongs to the session: cancelling it (see stopProbe) ends the
// decoders, and the result of a cancelled comparison is dropped before it can
// touch the session it no longer describes.
func (a *appState) suggestAlign(ctx context.Context, reference, target int) (alignSuggestion, error) {
	started := time.Now()
	snapshot, err := a.snapshotOnMain(reference, target)
	if err != nil {
		return alignSuggestion{}, err
	}
	ref, tgt := snapshot.Reference, snapshot.Target

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	first, err := decodeEnvelope(ctx, ref.URI, ref.Start, ref.Length, a.noteProbeChild)
	if err != nil {
		return alignSuggestion{}, fmt.Errorf("%s: %w", shortURI(ref.URI), err)
	}
	second, err := decodeEnvelope(ctx, tgt.URI, tgt.Start, tgt.Length, a.noteProbeChild)
	if err != nil {
		return alignSuggestion{}, fmt.Errorf("%s: %w", shortURI(tgt.URI), err)
	}

	result, err := align.Compare(first, second, align.DefaultLimits())
	if err != nil {
		return alignSuggestion{}, err
	}

	suggestion := alignSuggestion{
		Reference: ref,
		Target:    tgt,
		Snapshot:  snapshot,
		Result:    result,
		Current:   snapshot.Offset,
		Elapsed:   time.Since(started),
	}
	if result.Accepted {
		suggestion.CoreOffset = tgt.Start - ref.Start + result.Offset
		suggestion.Delta = suggestion.CoreOffset - snapshot.Offset
	}
	// Reading the session to decide whether the result is still current needs the
	// UI thread even when the comparison above ran on its own goroutine.
	finish := func() { a.finishSuggestion(&suggestion) }
	mygoRunOnMain(finish)
	return suggestion, nil
}

// snapshotOnMain captures the windows and the offset the probe starts from, on
// the UI thread, before any decoding happens.
func (a *appState) snapshotOnMain(reference, target int) (probeSnapshot, error) {
	var snapshot probeSnapshot
	var err error
	mygoRunOnMain(func() {
		if snapshot.Reference, err = a.alignWindowAt(reference); err != nil {
			return
		}
		if snapshot.Target, err = a.alignWindowAt(target); err != nil {
			return
		}
		track := a.trackOfAudio(a.itemOf(snapshot.Target.Source))
		snapshot.Track = track
		if shift, ok := a.sync.timeline.Shift(track); ok {
			snapshot.Offset = shift.Offset
			snapshot.Aligned = true
		}
		if group, ok := a.existingGroup(); ok {
			snapshot.Group = group.ID
			snapshot.HasGroup = true
		}
	})
	return snapshot, err
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

// finishSuggestion checks the finished comparison against the snapshot it
// started from and records why it can no longer be shown as current. It only
// reads the session: nothing here writes an offset or moves a player.
func (a *appState) finishSuggestion(s *alignSuggestion) {
	s.Expired = a.expiredAgainst(*s)
	if s.Expired != "" {
		return
	}
	if group, ok := a.existingGroup(); ok {
		s.Group = group.ID
		s.GroupShift = s.Current
	}
}

// expiredAgainst reports why a suggestion no longer describes the session, or ""
// when it still does.
func (a *appState) expiredAgainst(s alignSuggestion) string {
	for _, window := range []alignWindow{s.Reference, s.Target} {
		it := a.itemOf(window.Source)
		if it == nil {
			return fmt.Sprintf("source %d is gone", window.Index+1)
		}
		if it.generation != window.Generation {
			return fmt.Sprintf("%s was reopened since the probe", a.name(it))
		}
	}
	current, ok := a.sync.timeline.Shift(s.Snapshot.Track)
	if ok != s.Snapshot.Aligned || (ok && current.Offset != s.Snapshot.Offset) {
		return "the offset of the target changed since the probe started"
	}
	group, has := a.existingGroup()
	if s.Snapshot.HasGroup && (!has || group.ID != s.Snapshot.Group) {
		return "the sync group changed since the probe started"
	}
	return ""
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
	track := a.trackOfAudio(it)
	if it.audio == "" {
		// The comparison decodes `0:a:0`. Without an audio track nothing is
		// compared, and the window must say so rather than name a picture.
		return alignWindow{}, fmt.Errorf("%s has no audio track to compare", a.name(it))
	}
	return alignWindow{
		Source:     it.source.ID,
		Index:      index,
		URI:        it.uri,
		Generation: it.generation,
		Track:      track,
		Start:      0,
		Length:     length,
	}, nil
}

// decodeProgram is the decoder a comparison runs. It is a variable so a check can
// point it at a program whose pid it can watch die; the product always runs
// ffmpeg from PATH.
var decodeProgram = "ffmpeg"

// decodeEnvelope turns a slice of one source into the envelope the matcher uses.
// The desktop cannot ask MyGo for PCM, so this decodes the same address with
// ffmpeg; the offset comes from the bytes of the stream, not from the player.
//
// It is started by hand rather than through Output so the caller learns the
// child's pid while it runs: a session that closes during a comparison has to
// show the process is gone, and only the pid can show that.
func decodeEnvelope(ctx context.Context, uri string, start, length time.Duration, onStart func(int)) (align.Envelope, error) {
	command := exec.CommandContext(ctx, decodeProgram, "-v", "error",
		"-ss", seconds(start), "-t", seconds(length),
		"-i", uri, "-map", "0:a:0", "-ac", "1", "-ar", "16000", "-f", "s16le", "-")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return align.Envelope{}, fmt.Errorf("cannot read the decoder's output: %w", err)
	}
	if err := command.Start(); err != nil {
		return align.Envelope{}, fmt.Errorf("cannot start the decoder: %w", err)
	}
	if onStart != nil {
		onStart(command.Process.Pid)
	}
	raw, readErr := io.ReadAll(stdout)
	waitErr := command.Wait()
	if onStart != nil {
		// The child is gone once Wait returns, whether it exited on its own or was
		// killed. Say so, or the session keeps naming a process that no longer
		// exists and every later reader has to guess whether it is still running.
		onStart(0)
	}
	if waitErr != nil {
		// A cancelled comparison stopped because the session closed; say that,
		// not the signal the kill happened to leave behind.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return align.Envelope{}, fmt.Errorf("the decoder stopped (%v): %w", ctxErr, waitErr)
		}
		return align.Envelope{}, fmt.Errorf("cannot decode audio: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return align.Envelope{}, fmt.Errorf("cannot read the decoder's output: %w", readErr)
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
	return a.expiredAgainst(s)
}

// suggestionLine renders a proposal for the status bar. It is deliberately
// verbose: the user must be able to see why a value was proposed or refused.
func (a *appState) suggestionLine(s alignSuggestion) string {
	common := s.Reference.Length
	if s.Target.Length < common {
		common = s.Target.Length
	}
	expired := s.Expired
	if expired == "" {
		expired = a.fresh(s)
	}
	if !s.Result.Accepted {
		line := fmt.Sprintf("no suggestion for %s: %s (peak %.3f, next %.3f, %s)",
			shortURI(s.Target.URI), s.Result.Reason, s.Result.Peak, s.Result.RunnerUp, s.Elapsed.Round(time.Millisecond))
		if expired != "" {
			line += " (expired: " + expired + ")"
		}
		return line
	}
	line := fmt.Sprintf("suggested %+.3fs for %s (now %+.3fs, difference %+.3fs); peak %.3f vs %.3f, matched %s of %s, %s",
		s.CoreOffset.Seconds(), shortURI(s.Target.URI), s.Current.Seconds(), s.Delta.Seconds(),
		s.Result.Peak, s.Result.RunnerUp,
		s.Result.Overlap.Round(10*time.Millisecond), common.Round(10*time.Millisecond),
		s.Elapsed.Round(time.Millisecond))
	if expired != "" {
		line += " (expired: " + expired + ")"
	}
	return line
}

// probeAndReport runs a suggestion and records it in the status bar and the log.
// It is the only place the button and the scenario share, so both exercise the
// same read-only path.
func (a *appState) probeAndReport(reference, target int) alignSuggestion {
	a.syncNote = ""
	suggestion, err := a.suggestAlign(context.Background(), reference, target)
	if err != nil {
		a.syncNote = fmt.Sprintf("alignment probe failed: %v", err)
		logf("align", "probe failed: %v", err)
		return suggestion
	}
	a.syncNote = a.suggestionLine(suggestion)
	logf("align", "%s", a.syncNote)
	return suggestion
}

// probeFailure names the comparison that did not finish after its own sources:
// the older proposal is history, not a label for a newer attempt.
type probeFailure struct {
	Reference alignWindow
	Target    alignWindow
	Reason    string
}

// probeAsync is the button's path: the comparison decodes two files, so it runs
// off the UI thread and only the finished text is handed back to it. The running
// flag lets a scenario (and the button) tell a comparison in flight from a
// finished one without reading the wording of the status line.
func (a *appState) probeAsync(reference, target int) {
	a.startProbe(reference, target)
}

// startProbe starts one comparison and returns its attempt number. It must run
// on the drawing thread: it reads the two windows before the work starts so the
// result can be named after what was actually compared.
func (a *appState) startProbe(reference, target int) uint64 {
	if a.probeRunning {
		a.syncNote = "a comparison is already running"
		return a.probeAttempt
	}
	refWindow, refErr := a.alignWindowAt(reference)
	tgtWindow, tgtErr := a.alignWindowAt(target)
	a.probeAttempt++
	attempt := a.probeAttempt
	a.probeRunning = true
	a.probeChild.Store(0)
	ctx, cancel := context.WithCancel(context.Background())
	a.probeCtx, a.probeCancel = ctx, cancel
	a.syncNote = fmt.Sprintf("comparing source %d with source %d…", reference+1, target+1)
	go func() {
		suggestion, err := a.suggestAlign(ctx, reference, target)
		cancel()
		mygoRunOnMain(func() {
			a.applyProbeResult(attempt, suggestion, err, refWindow, refErr, tgtWindow, tgtErr)
		})
	}()
	return attempt
}

// noteProbeChild records the decoder's pid, and clears it when the decoder has
// ended (pid 0): a close can then show the process ended instead of assuming
// that cancelling a context was enough, and no reader mistakes a pid that once
// ran for one that still does.
func (a *appState) noteProbeChild(pid int) {
	a.probeChild.Store(int64(pid))
	if pid == 0 {
		return
	}
	logf("align", "the decoder runs as pid %d", pid)
}

// probePID is the pid of the decoder in flight, or 0 when none was started.
func (a *appState) probePID() int {
	return int(a.probeChild.Load())
}

// stopProbe ends the comparison in flight and makes sure its result can never
// touch the session again: the attempt counter moves on, so a result that lands
// after this returns is dropped as stale. It does not wait for the decoder — the
// context cancels it — because the close path must not block on a callback only
// the drawing thread could run.
func (a *appState) stopProbe(why string) bool {
	if !a.probeRunning && a.probeCancel == nil {
		return false
	}
	a.probeAttempt++
	a.probeRunning = false
	cancel := a.probeCancel
	a.probeCancel = nil
	a.probeCtx = nil
	if cancel != nil {
		cancel()
	}
	logf("align", "the comparison in flight was cancelled: %s", why)
	return true
}

// applyProbeResult records a finished comparison. A result from an attempt that
// has been superseded is dropped: the session's account belongs to the newest
// attempt, not to whichever goroutine finished last.
func (a *appState) applyProbeResult(attempt uint64, suggestion alignSuggestion, err error, ref alignWindow, refErr error, tgt alignWindow, tgtErr error) {
	if attempt != a.probeAttempt {
		logf("align", "an earlier comparison finished after a newer one started; it is not the session's account")
		return
	}
	a.probeRunning = false
	a.probeDone = attempt
	if cancel := a.probeCancel; cancel != nil {
		cancel()
		a.probeCancel = nil
	}
	a.probeCtx = nil
	if err != nil {
		reason := trimFailure(err.Error(), tgt.URI)
		a.lastProbeErr = reason
		if refErr == nil && tgtErr == nil {
			a.lastProbeFail = &probeFailure{Reference: ref, Target: tgt, Reason: reason}
		} else {
			a.lastProbeFail = nil
		}
		a.syncNote = fmt.Sprintf("alignment probe failed: %s", reason)
		logf("align", "probe failed: %s", reason)
		return
	}
	a.lastProbeErr = ""
	a.lastProbeFail = nil
	a.lastProbe = suggestion
	a.syncNote = a.suggestionLine(suggestion)
	logf("align", "%s", a.syncNote)
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

// trimFailure keeps a failed comparison readable: the first line of the tool's
// message, without repeating the address the report already names, shortened to
// something that fits a row.
func trimFailure(reason, uri string) string {
	if line, _, found := strings.Cut(reason, "\n"); found {
		reason = line
	}
	reason = strings.TrimSpace(reason)
	if uri != "" {
		reason = strings.TrimSpace(strings.TrimPrefix(reason, uri+":"))
		// ffmpeg names the file by its base name, not by the address the report
		// already carries.
		if base := path.Base(uri); base != uri {
			reason = strings.TrimSpace(strings.TrimPrefix(reason, base+":"))
		}
	}
	reason = strings.Join(strings.Fields(reason), " ")
	const limit = 150
	if len(reason) > limit {
		reason = reason[:limit] + "…"
	}
	return reason
}
