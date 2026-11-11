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

// Confirmed reports whether the offset has a stated origin.
func (a Alignment) Confirmed() bool { return a.Basis != BasisNone }

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
	// Content is the buffer's own time for the target, and Position is where
	// that time sits in the recording file.
	Content  time.Duration
	Position time.Duration
}

// SeekPlan is an immutable decision: the event time that was authorised and,
// for every source, the file position that would show it. It names the
// identities it was made from, so a later state can be compared against it
// before anything is opened.
type SeekPlan struct {
	Event time.Duration
	Hops  []SeekHop
}

// Hop returns the hop for one source.
func (p SeekPlan) Hop(source core.SourceID) (SeekHop, bool) {
	for _, hop := range p.Hops {
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
	plan := SeekPlan{Event: event, Hops: make([]SeekHop, 0, len(sources))}
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
		plan.Hops = append(plan.Hops, hop)
	}
	return plan, nil
}

// Matches reports whether the sources still support this plan. It is the check
// a caller runs just before opening anything: a buffer rebuilt, an epoch moved,
// another recording published or the alignment withdrawn all make the old
// decision stale, and the answer names what changed instead of reusing it.
func (p SeekPlan) Matches(sources []SeekSource) error {
	if len(sources) == 0 {
		return fmt.Errorf("%w: no source was offered", ErrNotAuthorized)
	}
	fresh, err := PlanSeek(p.Event, sources)
	if err != nil {
		return fmt.Errorf("the plan no longer holds: %w", err)
	}
	if len(fresh.Hops) != len(p.Hops) {
		return fmt.Errorf("the plan no longer holds: it named %d sources, %d are offered", len(p.Hops), len(fresh.Hops))
	}
	for _, was := range p.Hops {
		now, ok := fresh.Hop(was.Source)
		if !ok {
			return fmt.Errorf("the plan no longer holds: %s is no longer offered", was.Source)
		}
		if now != was {
			return fmt.Errorf("the plan no longer holds: %s changed from %s in %s to %s in %s",
				was.Source, was.Position, was.RecordingID, now.Position, now.RecordingID)
		}
	}
	return nil
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
	recording := source.Recording
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
		Content:     content,
		Position:    content - recording.StartBufferTime,
	}, nil
}

// sameClock checks that the anchor and the recording describe the same buffer,
// the same epoch and the same source. Any of the three moving means the
// numbers were measured in a different clock and must not be mixed.
func sameClock(source SeekSource) error {
	recording := source.Recording
	if recording.Source != source.Source {
		return fmt.Errorf("the recording belongs to %s, not to %s", recording.Source, source.Source)
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
