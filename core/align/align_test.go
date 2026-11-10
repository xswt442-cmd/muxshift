package align

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

const testRate = 16000

// samples builds a repeatable, non-periodic signal: white noise shaped by a
// slowly varying random level, so a shifted copy correlates with itself while
// no other lag does.
func samples(seconds float64, seed int64) []float32 {
	count := int(seconds * testRate)
	out := make([]float32, count)
	source := rand.New(rand.NewSource(seed))
	level := 0.2 + source.Float64()
	for index := 0; index < count; index++ {
		if index%160 == 0 {
			level = 0.2 + source.Float64()
		}
		out[index] = float32((source.Float64() - 0.5) * 0.8 * level)
	}
	return out
}

func envelope(t *testing.T, values []float32) Envelope {
	t.Helper()
	got, err := FromFloat32(values, testRate, DefaultHop)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return got
}

func TestCompareFindsPreparedOffsets(t *testing.T) {
	base := samples(30, 7)
	envelopeA := envelope(t, base)
	for _, lag := range []time.Duration{-2 * time.Second, -1500 * time.Millisecond, 3 * time.Second} {
		frames := int(float64(lag) / float64(time.Second) * testRate)
		var shifted []float32
		if frames >= 0 {
			shifted = append(make([]float32, frames), base...)
		} else {
			shifted = base[-frames:]
		}
		result, err := Compare(envelopeA, envelope(t, shifted), Limits{MaxLag: 10 * time.Second})
		if err != nil {
			t.Fatalf("compare at %s: %v", lag, err)
		}
		if !result.Accepted {
			t.Fatalf("offset %s was refused: %s (peak %.3f, runner %s at %.3f)", lag, result.Reason, result.Peak, result.RunnerOffset, result.RunnerUp)
		}
		if result.Offset != lag {
			t.Fatalf("offset %s was measured as %s (runner %s at %.3f)", lag, result.Offset, result.RunnerOffset, result.RunnerUp)
		}
		if result.Confidence < 0.5 {
			t.Fatalf("offset %s has confidence %.3f", lag, result.Confidence)
		}
	}
}

func TestCompareRefusesDifferentContent(t *testing.T) {
	result, err := Compare(envelope(t, samples(20, 1)), envelope(t, samples(20, 9)), Limits{MaxLag: 5 * time.Second})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if result.Accepted {
		t.Fatalf("unrelated recordings were accepted with offset %s and peak %.3f", result.Offset, result.Peak)
	}
	if result.Reason == "" {
		t.Fatal("a refusal must say why")
	}
}

func TestCompareRefusesAmbiguousMatch(t *testing.T) {
	// A constant tone correlates with itself at every lag inside a period, so
	// there is no single candidate to trust.
	flat := make([]float32, 20*testRate)
	for index := range flat {
		flat[index] = 0.3
	}
	result, err := Compare(envelope(t, flat), envelope(t, flat), Limits{MaxLag: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if result.Accepted {
		t.Fatalf("a flat signal was accepted at offset %s", result.Offset)
	}
}

func TestCompareRefusesCandidateAtWindowEdge(t *testing.T) {
	// The copy starts eight seconds into the recording while the search window
	// stops at seven, so the only candidate sits on the boundary.
	base := samples(24, 3)
	shifted := append(make([]float32, 0), base[8*testRate:]...)
	result, err := Compare(envelope(t, base), envelope(t, shifted), Limits{MaxLag: 7 * time.Second})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if result.Accepted {
		t.Fatalf("a candidate at the window edge was accepted as %s (peak %.3f)", result.Offset, result.Peak)
	}
	if result.Reason == "" {
		t.Fatal("a refusal must say why")
	}
}

func TestCompareReportsOverlapOfUnequalWindows(t *testing.T) {
	// A short copy of the start of a longer recording: the two windows share only
	// the shorter one, so that is what the result has to report.
	base := samples(40, 11)
	short := base[:15*testRate]
	result, err := Compare(envelope(t, base), envelope(t, short), Limits{MaxLag: time.Second})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if want := 15 * time.Second; result.Overlap != want {
		t.Fatalf("overlap = %s, want %s", result.Overlap, want)
	}
	if result.Offset != 0 {
		t.Fatalf("offset = %s, want 0", result.Offset)
	}
	if !result.Accepted {
		t.Fatalf("the same content was refused: %s", result.Reason)
	}
}

func TestCompareRefusesShortOverlap(t *testing.T) {
	// The candidate is real, but only six seconds of the windows would line up:
	// that is too little to stand for an event, so it must be refused with the
	// matched length rather than guessed.
	base := samples(20, 13)
	short := base[:6*testRate]
	result, err := Compare(envelope(t, base), envelope(t, short), Limits{MaxLag: 2 * time.Second})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if result.Accepted {
		t.Fatalf("six seconds of overlap were accepted as %s", result.Offset)
	}
	if !strings.Contains(result.Reason, "overlap") {
		t.Fatalf("the refusal should name the overlap, got %q", result.Reason)
	}
}

func TestCompareRejectsMismatchedHops(t *testing.T) {
	_, err := Compare(Envelope{Hop: 10 * time.Millisecond, Values: make([]float64, 10)}, Envelope{Hop: 20 * time.Millisecond, Values: make([]float64, 10)}, Limits{})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("mismatched hops returned %v", err)
	}
}

func TestCompareRejectsEmptyEnvelopes(t *testing.T) {
	_, err := Compare(Envelope{Hop: DefaultHop}, Envelope{Hop: DefaultHop}, Limits{})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty envelopes returned %v", err)
	}
	if !errors.Is(ErrNoSuggestion, ErrNoSuggestion) {
		t.Fatal("ErrNoSuggestion must exist for callers that distinguish the two failures")
	}
}

func TestFromFloat32RejectsBadRate(t *testing.T) {
	if _, err := FromFloat32([]float32{0.1}, 0, DefaultHop); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("rate 0 returned %v", err)
	}
	if _, err := FromFloat32([]float32{0.1}, testRate, 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("hop 0 returned %v", err)
	}
	if _, err := FromFloat32([]float32{0.1}, testRate, DefaultHop); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("a sample shorter than one frame returned %v", err)
	}
}

func TestFromInt16MatchesFloat32(t *testing.T) {
	values := samples(2, 5)
	ints := make([]int16, len(values))
	for index, value := range values {
		ints[index] = int16(value * 32767)
	}
	fromInts, err := FromInt16(ints, testRate, DefaultHop)
	if err != nil {
		t.Fatalf("int16 envelope: %v", err)
	}
	fromFloats := envelope(t, values)
	if len(fromInts.Values) != len(fromFloats.Values) {
		t.Fatalf("int16 made %d frames, float32 made %d", len(fromInts.Values), len(fromFloats.Values))
	}
	for index := range fromInts.Values {
		if math.Abs(fromInts.Values[index]-fromFloats.Values[index]) > 0.01 {
			t.Fatalf("frame %d differs: int16 %.4f, float32 %.4f", index, fromInts.Values[index], fromFloats.Values[index])
		}
	}
}
