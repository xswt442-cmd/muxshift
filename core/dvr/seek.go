package dvr

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// ErrNotAuthorized is wrapped by every refusal to authorise a replay seek. A
// caller may test for it to tell "this seek is not allowed, and the reason is
// named" from a programming mistake such as an empty source list.
var ErrNotAuthorized = errors.New("the seek is not authorised")

// Basis names how one track's offset on the shared timeline was established. A
// zero offset is legal, but only when something established it: the same
// numbers can come from a calibration that measured the content or from a
// default nobody ever checked, and those are not the same claim.
type Basis string

const (
	// BasisNone means the offset was never established. It refuses, even when
	// it is zero: a buffer that started at its own first frame cannot vouch
	// for where that frame sits in the event.
	BasisNone Basis = ""
	// BasisManual means a person placed the track against the others.
	BasisManual Basis = "manual"
	// BasisMeasured means the content itself was measured, for example by
	// cross-correlating envelopes.
	BasisMeasured Basis = "measured"
	// BasisSharedStart means the caller established that the buffers began on
	// the same frame, so their content clocks share an origin.
	BasisSharedStart Basis = "shared-start"
)

// Alignment is one track's offset together with where that offset came from.
type Alignment struct {
	Offset time.Duration
	Basis  Basis
}

// Confirmed reports whether the offset has a stated origin. Only the three
// named origins count: a basis nobody defined is a claim that was never
// checked, and a zero carrying it is not a confirmed zero.
func (a Alignment) Confirmed() bool {
	switch a.Basis {
	case BasisManual, BasisMeasured, BasisSharedStart:
		return true
	default:
		return false
	}
}

// SeekSource is one source's state at the moment a replay seek is asked for:
// the buffer it is playing from, the anchor measured for that buffer, the
// alignment its track holds, and the recording that would have to open.
type SeekSource struct {
	// Source is the Core source the track belongs to.
	Source core.SourceID
	// Epoch and Instance name the buffer the target would be replayed from.
	Epoch    uint64
	Instance string
	// Range is the single contiguous range of content the buffer holds. Asking
	// for a time outside it is refused rather than clamped.
	Range Range
	// Anchor is the measured mapping from this buffer's content clock to the
	// container timestamps of its files.
	Anchor Anchor
	// Alignment is the track's offset on the shared timeline and its origin.
	Alignment Alignment
	// Recording is the file that was really cut from this buffer.
	Recording Recording
	// PlayerGeneration names the player that was showing this source when the
	// state was taken. A plan binds it, so replacing the player invalidates a
	// decision that was made about the old one even if the buffer did not move.
	PlayerGeneration uint64
}

// SeekHop is one source's part of an authorised seek: where in its recording
// the target event time lives. A hop is a statement about the file, not about
// the player: a position that a player accepts is not proof that the frame was
// shown.
type SeekHop struct {
	Source      core.SourceID
	Epoch       uint64
	Instance    string
	RecordingID string
	Path        string
	Basis       Basis
	// Generation is the player the decision was made for.
	Generation uint64
	// Content is the buffer's own time for the target, and Position is where
	// that time sits in the recording file.
	Content  time.Duration
	Position time.Duration
}

// SeekPlan is an immutable decision: the event time that was authorised and,
// for every source, the file position that would show it. It names the
// identities it was made from, so a later state can be compared against it
// before anything is opened. The hops are private: only Hop and Hops hand them
// out, and Hops returns a copy, so a caller cannot rewrite a decision after it
// was made.
type SeekPlan struct {
	event      time.Duration
	hops       []SeekHop
	identities []sourceIdentity
}

// Event returns the event time the plan was made for. It is read-only: the
// field is private, so a plan that was decided for one time cannot be
// rewritten to claim another one.
func (p SeekPlan) Event() time.Duration {
	return p.event
}

// Hops returns a copy of every hop in the plan.
func (p SeekPlan) Hops() []SeekHop {
	return append([]SeekHop(nil), p.hops...)
}

// Hop returns the hop for one source.
func (p SeekPlan) Hop(source core.SourceID) (SeekHop, bool) {
	for _, hop := range p.hops {
		if hop.Source == source {
			return hop, true
		}
	}
	return SeekHop{}, false
}

// PlanSeek decides whether a replay seek to one event time is allowed, and
// where each source's recording would have to start. Every refusal names the
// source and the condition that failed; nothing is clamped, guessed or filled
// in with a default offset.
//
// The plan proves the numbers are internally consistent: the offset is
// confirmed, the event maps into the range the buffer holds, the anchor was
// measured for that buffer and epoch, and the recording covers the target. It
// does not by itself prove that two sources show the same content — the basis
// records what the caller claimed, and P16.4b re-checks the identities before
// anything is opened.
func PlanSeek(event time.Duration, sources []SeekSource) (SeekPlan, error) {
	if len(sources) == 0 {
		return SeekPlan{}, fmt.Errorf("%w: no source was offered", ErrNotAuthorized)
	}
	plan := SeekPlan{
		event:      event,
		hops:       make([]SeekHop, 0, len(sources)),
		identities: make([]sourceIdentity, 0, len(sources)),
	}
	seen := make(map[core.SourceID]bool, len(sources))
	for _, source := range sources {
		if source.Source == "" {
			return SeekPlan{}, fmt.Errorf("%w: a source has no identity", ErrNotAuthorized)
		}
		if seen[source.Source] {
			return SeekPlan{}, fmt.Errorf("%w: %s was offered twice", ErrNotAuthorized, source.Source)
		}
		seen[source.Source] = true
		hop, err := planHop(event, source)
		if err != nil {
			return SeekPlan{}, fmt.Errorf("%w: %s: %w", ErrNotAuthorized, source.Source, err)
		}
		plan.hops = append(plan.hops, hop)
		plan.identities = append(plan.identities, identityOf(source))
	}
	return plan, nil
}

// Matches reports whether the sources still support this plan. It is the check
// a caller runs just before opening anything: a buffer rebuilt, an epoch moved,
// another recording published, the alignment withdrawn, the anchor re-measured
// or the player replaced all make the old decision stale. It compares the
// identities the decision was made from, not just the positions they project
// to: two different anchors can land on the same position.
func (p SeekPlan) Matches(sources []SeekSource) error {
	if len(sources) == 0 {
		return fmt.Errorf("%w: no source was offered", ErrNotAuthorized)
	}
	fresh, err := PlanSeek(p.event, sources)
	if err != nil {
		return fmt.Errorf("the plan no longer holds: %w", err)
	}
	if len(fresh.identities) != len(p.identities) {
		return fmt.Errorf("the plan no longer holds: it named %d sources, %d are offered",
			len(p.identities), len(fresh.identities))
	}
	if len(fresh.hops) != len(p.hops) {
		return fmt.Errorf("the plan no longer holds: it decided %d hops, %d are needed",
			len(p.hops), len(fresh.hops))
	}
	for _, was := range p.identities {
		now, ok := fresh.identity(was.Source)
		if !ok {
			return fmt.Errorf("the plan no longer holds: %s is no longer offered", was.Source)
		}
		if now != was {
			return fmt.Errorf("the plan no longer holds: %s %s", was.Source, explainIdentity(was, now))
		}
	}
	for index, was := range p.hops {
		if fresh.hops[index] != was {
			return fmt.Errorf("the plan no longer holds: the hop for %s was recalculated", was.Source)
		}
	}
	return nil
}

// identity returns the identity the plan recorded for one source.
func (p SeekPlan) identity(source core.SourceID) (sourceIdentity, bool) {
	for _, identity := range p.identities {
		if identity.Source == source {
			return identity, true
		}
	}
	return sourceIdentity{}, false
}

// planHop maps one event time into one recording, or says why it cannot.
func planHop(event time.Duration, source SeekSource) (SeekHop, error) {
	if !source.Alignment.Confirmed() {
		return SeekHop{}, fmt.Errorf("the track's offset was never established; a zero that was never confirmed is not evidence")
	}
	content, ok := addTime(event, source.Alignment.Offset)
	if !ok {
		return SeekHop{}, fmt.Errorf("event time %s plus offset %s cannot be represented", event, source.Alignment.Offset)
	}
	if content < 0 {
		return SeekHop{}, fmt.Errorf("event time %s is %s before this source's content begins",
			event, -content)
	}
	if !source.Range.Contains(content) {
		return SeekHop{}, fmt.Errorf("event time %s maps to content %s, outside the contiguous range this buffer holds (%s); a gap or a skipped segment separates them",
			event, content, source.Range)
	}
	if _, err := source.Anchor.ContentTime(content); err != nil {
		return SeekHop{}, fmt.Errorf("no usable anchor for content %s: %w", content, err)
	}
	if err := sameClock(source); err != nil {
		return SeekHop{}, err
	}
	if !source.Range.Contains(source.Anchor.Interval.Start) {
		return SeekHop{}, fmt.Errorf("the anchor was measured at %s, which this buffer no longer holds (%s); the media it measured was evicted",
			source.Anchor.Interval.Start, source.Range)
	}
	recording := source.Recording
	if recording.ID == "" || recording.Path == "" {
		return SeekHop{}, fmt.Errorf("the recording has no identity to open (id %q, path %q)", recording.ID, recording.Path)
	}
	if content < recording.StartBufferTime || content >= recording.EndBufferTime {
		return SeekHop{}, fmt.Errorf("content %s is outside the recording's %s..%s; that file was cut around another moment",
			content, recording.StartBufferTime, recording.EndBufferTime)
	}
	return SeekHop{
		Source:      source.Source,
		Epoch:       source.Epoch,
		Instance:    source.Instance,
		RecordingID: recording.ID,
		Path:        recording.Path,
		Basis:       source.Alignment.Basis,
		Generation:  source.PlayerGeneration,
		Content:     content,
		Position:    content - recording.StartBufferTime,
	}, nil
}

// sameClock checks that the anchor, the range and the recording all describe
// the same buffer, the same epoch and the same source. Any of them moving means
// the numbers were measured in a different clock and must not be mixed. The
// anchor's own source is checked too: its interval alone can cover the same
// numbers while having been measured somewhere else.
func sameClock(source SeekSource) error {
	recording := source.Recording
	if source.Instance == "" {
		return fmt.Errorf("the buffer has no name, so an anchor and a recording cannot be proven to come from it")
	}
	if recording.Source != source.Source {
		return fmt.Errorf("the recording belongs to %s, not to %s", recording.Source, source.Source)
	}
	if source.Anchor.Source == "" {
		return fmt.Errorf("the anchor names no source, so it cannot vouch for %s", source.Source)
	}
	if source.Anchor.Source != source.Source {
		return fmt.Errorf("the anchor belongs to %s, not to %s", source.Anchor.Source, source.Source)
	}
	if source.Range.Epoch != source.Epoch {
		return fmt.Errorf("the range was taken in epoch %d, but the buffer is in epoch %d",
			source.Range.Epoch, source.Epoch)
	}
	if source.Anchor.Epoch != source.Epoch {
		return fmt.Errorf("the anchor was measured in epoch %d, but the buffer is in epoch %d",
			source.Anchor.Epoch, source.Epoch)
	}
	if source.Anchor.BufferInstance != source.Instance {
		return fmt.Errorf("the anchor was measured on buffer %s, but the buffer is %s",
			orUnknown(source.Anchor.BufferInstance), orUnknown(source.Instance))
	}
	if recording.SourceMediaEpoch != source.Epoch {
		return fmt.Errorf("the recording was cut in epoch %d, but the buffer is in epoch %d",
			recording.SourceMediaEpoch, source.Epoch)
	}
	if recording.BufferInstance != source.Instance {
		return fmt.Errorf("the recording came from buffer %s, but the buffer is %s",
			orUnknown(recording.BufferInstance), orUnknown(source.Instance))
	}
	return nil
}

// addTime adds two durations and reports whether the result fits. The Core
// timeline refuses overflow the same way; a seek must not wrap around.
func addTime(one, two time.Duration) (time.Duration, bool) {
	sum := one + two
	if (two > 0 && sum < one) || (two < 0 && sum > one) {
		return 0, false
	}
	if one > 0 && two > 0 && one > math.MaxInt64-two {
		return 0, false
	}
	return sum, true
}

// subTime subtracts one duration from another and reports whether the result
// fits, so a mapping cannot wrap around and look like an ordinary time.
func subTime(one, two time.Duration) (time.Duration, bool) {
	difference := one - two
	if (two > 0 && difference > one) || (two < 0 && difference < one) {
		return 0, false
	}
	if one < 0 && two > 0 && one < math.MinInt64+two {
		return 0, false
	}
	return difference, true
}

// sourceIdentity is everything a decision leaned on, in a comparable shape.
// Matches recomputes it from the offered state and compares the whole record:
// comparing only the projected position would accept a re-measured anchor, a
// replaced recording or a new player as long as the arithmetic still landed on
// the same number.
type sourceIdentity struct {
	Source              core.SourceID
	Epoch               uint64
	Instance            string
	PlayerGeneration    uint64
	Offset              time.Duration
	Basis               Basis
	RangeStart          time.Duration
	RangeEnd            time.Duration
	RangeEpoch          uint64
	AnchorSource        core.SourceID
	AnchorEpoch         uint64
	AnchorInstance      string
	AnchorBufferStart   time.Duration
	AnchorMediaStart    time.Duration
	AnchorIntervalEnd   time.Duration
	AnchorIntervalStart time.Duration
	MeasuredAt          int64
	RecordingID         string
	RecordingPath       string
	RecordingSource     core.SourceID
	RecordingStart      time.Duration
	RecordingEnd        time.Duration
	RecordingEpoch      uint64
	RecordingInstance   string
}

// identityOf captures the state a plan was made from.
func identityOf(source SeekSource) sourceIdentity {
	measured := int64(0)
	if !source.Anchor.MeasuredAt.IsZero() {
		measured = source.Anchor.MeasuredAt.UnixNano()
	}
	return sourceIdentity{
		Source:              source.Source,
		Epoch:               source.Epoch,
		Instance:            source.Instance,
		PlayerGeneration:    source.PlayerGeneration,
		Offset:              source.Alignment.Offset,
		Basis:               source.Alignment.Basis,
		RangeStart:          source.Range.Start,
		RangeEnd:            source.Range.End,
		RangeEpoch:          source.Range.Epoch,
		AnchorSource:        source.Anchor.Source,
		AnchorEpoch:         source.Anchor.Epoch,
		AnchorInstance:      source.Anchor.BufferInstance,
		AnchorBufferStart:   source.Anchor.BufferStart,
		AnchorMediaStart:    source.Anchor.MediaStart,
		AnchorIntervalStart: source.Anchor.Interval.Start,
		AnchorIntervalEnd:   source.Anchor.Interval.End,
		MeasuredAt:          measured,
		RecordingID:         source.Recording.ID,
		RecordingPath:       source.Recording.Path,
		RecordingSource:     source.Recording.Source,
		RecordingStart:      source.Recording.StartBufferTime,
		RecordingEnd:        source.Recording.EndBufferTime,
		RecordingEpoch:      source.Recording.SourceMediaEpoch,
		RecordingInstance:   source.Recording.BufferInstance,
	}
}

// explainIdentity names the first thing that changed, so a refusal says which
// part of the decision went stale instead of only that something did.
func explainIdentity(was, now sourceIdentity) string {
	switch {
	case was.PlayerGeneration != now.PlayerGeneration:
		return fmt.Sprintf("was planned for player generation %d, the player is now generation %d",
			was.PlayerGeneration, now.PlayerGeneration)
	case was.AnchorMediaStart != now.AnchorMediaStart || was.AnchorBufferStart != now.AnchorBufferStart ||
		was.AnchorIntervalStart != now.AnchorIntervalStart || was.AnchorIntervalEnd != now.AnchorIntervalEnd || was.MeasuredAt != now.MeasuredAt:
		return fmt.Sprintf("the anchor was re-measured (media %s at buffer %s over %s), it is now media %s at buffer %s over %s",
			was.AnchorMediaStart, was.AnchorBufferStart, Range{Start: was.AnchorIntervalStart, End: was.AnchorIntervalEnd},
			now.AnchorMediaStart, now.AnchorBufferStart, Range{Start: now.AnchorIntervalStart, End: now.AnchorIntervalEnd})
	case was.RecordingID != now.RecordingID || was.RecordingPath != now.RecordingPath:
		return fmt.Sprintf("was planned on %q (%s), the recording is now %q (%s)",
			was.RecordingID, was.RecordingPath, now.RecordingID, now.RecordingPath)
	case was.RangeStart != now.RangeStart || was.RangeEnd != now.RangeEnd:
		return fmt.Sprintf("the buffer held %s..%s, it now holds %s..%s",
			was.RangeStart, was.RangeEnd, now.RangeStart, now.RangeEnd)
	case was.Offset != now.Offset || was.Basis != now.Basis:
		return fmt.Sprintf("the offset was %s (%s), it is now %s (%s)",
			was.Offset, was.Basis, now.Offset, now.Basis)
	case was.Epoch != now.Epoch || was.Instance != now.Instance:
		return fmt.Sprintf("was planned on epoch %d of %s, the buffer is now epoch %d of %s",
			was.Epoch, was.Instance, now.Epoch, now.Instance)
	default:
		return "the state it was planned from has changed"
	}
}
