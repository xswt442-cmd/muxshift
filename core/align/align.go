// Package align estimates the time offset between two recordings of the same
// event from their content alone.
//
// It compares log-energy envelopes rather than samples: level, codec and mixing
// differences are common between two recordings of one event, while the shape
// of the energy over time is not. Nothing here uses network arrival time, player
// position or wall clock, because none of those are event time.
//
// A result is only a suggestion. The package never writes a timeline and never
// moves a player; callers decide whether an accepted offset is worth showing.
package align

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// DefaultHop is the envelope resolution. Ten milliseconds is small enough to
// put two recordings on the same frame and large enough to keep the search
// cheap for a minute of audio.
const DefaultHop = 10 * time.Millisecond

// Envelope is a log-energy series sampled every Hop.
type Envelope struct {
	Hop    time.Duration
	Values []float64
}

// Duration is how much audio the envelope covers.
func (e Envelope) Duration() time.Duration {
	return time.Duration(len(e.Values)) * e.Hop
}

// FromFloat32 turns mono float samples in [-1, 1] into a log-energy envelope.
// Samples outside the range are clamped so a decoded stream with a bad peak
// cannot dominate the envelope.
func FromFloat32(samples []float32, rate int, hop time.Duration) (Envelope, error) {
	if rate <= 0 {
		return Envelope{}, fmt.Errorf("%w: sample rate %d", core.ErrInvalid, rate)
	}
	if hop <= 0 {
		return Envelope{}, fmt.Errorf("%w: envelope hop %s", core.ErrInvalid, hop)
	}
	perFrame := int(time.Duration(rate) * hop / time.Second)
	if perFrame < 1 {
		return Envelope{}, fmt.Errorf("%w: hop %s is shorter than one sample at %d Hz", core.ErrInvalid, hop, rate)
	}
	values := make([]float64, 0, len(samples)/perFrame+1)
	for start := 0; start+perFrame <= len(samples); start += perFrame {
		var energy float64
		for _, sample := range samples[start : start+perFrame] {
			if sample > 1 {
				sample = 1
			} else if sample < -1 {
				sample = -1
			}
			energy += float64(sample) * float64(sample)
		}
		values = append(values, math.Log1p(math.Sqrt(energy/float64(perFrame))*20))
	}
	if len(values) == 0 {
		return Envelope{}, fmt.Errorf("%w: %d samples are shorter than one %s frame", core.ErrInvalid, len(samples), hop)
	}
	return Envelope{Hop: hop, Values: values}, nil
}

// FromInt16 is FromFloat32 for signed 16-bit PCM, which is what ffmpeg writes
// with "-f s16le".
func FromInt16(samples []int16, rate int, hop time.Duration) (Envelope, error) {
	converted := make([]float32, len(samples))
	for index, sample := range samples {
		converted[index] = float32(sample) / 32768
	}
	return FromFloat32(converted, rate, hop)
}

// Limits decide when an offset is reported as accepted.
type Limits struct {
	// MaxLag bounds the search. A candidate at the edge is reported as
	// unconfirmed because the true offset may lie outside the window.
	MaxLag time.Duration
	// MinPeak is the lowest correlation that counts as shared content.
	MinPeak float64
	// MinConfidence is how far the best candidate must stand above the
	// runner-up, as a fraction of the peak.
	MinConfidence float64
}

// DefaultLimits are the values the offline probe uses.
func DefaultLimits() Limits {
	return Limits{MaxLag: 60 * time.Second, MinPeak: 0.25, MinConfidence: 0.5}
}

func (l Limits) withDefaults() Limits {
	if l.MaxLag <= 0 {
		l.MaxLag = DefaultLimits().MaxLag
	}
	if l.MinPeak <= 0 {
		l.MinPeak = DefaultLimits().MinPeak
	}
	if l.MinConfidence <= 0 {
		l.MinConfidence = DefaultLimits().MinConfidence
	}
	return l
}

// Result is one comparison. Accepted means the evidence supports the offset;
// every field is filled even when it does not, so a caller can show why.
type Result struct {
	Offset   time.Duration
	Peak     float64
	RunnerUp float64
	// RunnerOffset is where the second candidate sits, for diagnostics.
	RunnerOffset time.Duration
	Confidence   float64
	// Overlap is how much audio the two windows share at Offset.
	Overlap  time.Duration
	Accepted bool
	Reason   string
}

// Compare correlates two envelopes and reports the best offset. The returned
// offset follows the timeline convention targetMediaTime = sharedEventTime +
// offset: a positive offset means b's content runs later than a's, so b has to
// be moved forward to line up.
func Compare(a, b Envelope, limits Limits) (Result, error) {
	limits = limits.withDefaults()
	if a.Hop != b.Hop {
		return Result{}, fmt.Errorf("%w: envelopes use %s and %s hops", core.ErrInvalid, a.Hop, b.Hop)
	}
	if len(a.Values) < 8 || len(b.Values) < 8 {
		return Result{}, fmt.Errorf("%w: %d and %d envelope frames are too few to compare", core.ErrInvalid, len(a.Values), len(b.Values))
	}

	best := -math.MaxFloat64
	bestLag := 0
	maxLag := int(limits.MaxLag / a.Hop)
	shortest := len(a.Values)
	if len(b.Values) < shortest {
		shortest = len(b.Values)
	}
	// A candidate that shares only a handful of frames proves nothing: two
	// unrelated envelopes will agree by chance over eight frames. Require a
	// quarter of the shorter window to overlap.
	minOverlap := shortest / 4
	if minOverlap < 8 {
		minOverlap = 8
	}
	scores := make([]float64, 0, 2*maxLag+1)
	lags := make([]int, 0, 2*maxLag+1)

	for lag := -maxLag; lag <= maxLag; lag++ {
		score, overlap, ok := correlation(a.Values, b.Values, lag)
		if !ok || overlap < minOverlap {
			continue
		}
		scores = append(scores, score)
		lags = append(lags, lag)
		if score > best {
			best, bestLag = score, lag
		}
	}
	if best == -math.MaxFloat64 {
		return Result{}, fmt.Errorf("%w: no lag produced an overlap of eight frames", ErrNoSuggestion)
	}

	// Lags close to the best one describe the same match: a speech envelope
	// stays similar over tens of milliseconds, so a candidate 30 ms away is the
	// same peak seen slightly off, not a rival explanation.
	neighbourRadius := int(500 * time.Millisecond / a.Hop)
	if neighbourRadius < 2 {
		neighbourRadius = 2
	}
	runnerUp := -math.MaxFloat64
	runnerLag := 0
	for index, score := range scores {
		if abs(lags[index]-bestLag) <= neighbourRadius {
			continue
		}
		if score > runnerUp {
			runnerUp = score
			runnerLag = lags[index]
		}
	}
	if runnerUp == -math.MaxFloat64 {
		runnerUp = 0
	}
	confidence := 1.0
	if best > 0 {
		confidence = 1 - runnerUp/best
	}
	overlapFrames := len(a.Values) - abs(bestLag)
	result := Result{
		Offset:       time.Duration(bestLag) * a.Hop,
		Peak:         best,
		RunnerUp:     runnerUp,
		RunnerOffset: time.Duration(runnerLag) * a.Hop,
		Confidence:   confidence,
		Overlap:      time.Duration(overlapFrames) * a.Hop,
		Accepted:     true,
	}
	switch {
	case best < limits.MinPeak:
		result.Accepted = false
		result.Reason = fmt.Sprintf("the best peak is %.3f, below %.2f: no shared content", best, limits.MinPeak)
	case confidence < limits.MinConfidence:
		result.Accepted = false
		result.Reason = fmt.Sprintf("the next candidate is only %.3f below the peak, so the match is ambiguous", best-runnerUp)
	case abs(bestLag) == maxLag:
		result.Accepted = false
		result.Reason = fmt.Sprintf("the candidate sits at the edge of the %s search window", limits.MaxLag)
	default:
		result.Reason = "a single candidate stands out"
	}
	return result, nil
}

// correlation sums the products of two normalised envelopes at one integer lag.
// Both series are normalised before the sum so that a level difference does not
// change the score.
func correlation(a, b []float64, lag int) (float64, int, bool) {
	var left, right []float64
	switch {
	case lag >= 0:
		if lag >= len(a) || lag >= len(b) {
			return 0, 0, false
		}
		left, right = a[:len(a)-lag], b[lag:]
	default:
		offset := -lag
		if offset >= len(a) || offset >= len(b) {
			return 0, 0, false
		}
		left, right = a[offset:], b[:len(b)-offset]
	}
	if len(left) == 0 || len(right) == 0 {
		return 0, 0, false
	}
	return normalisedDot(left, right)
}

func normalisedDot(a, b []float64) (float64, int, bool) {
	count := len(a)
	if len(b) < count {
		count = len(b)
	}
	if count == 0 {
		return 0, 0, false
	}
	// Subtract the mean first: the envelopes of two different recordings often
	// sit at a similar level, and a plain dot product would report that shared
	// level as a match at every lag.
	var meanA, meanB float64
	for index := 0; index < count; index++ {
		meanA += a[index]
		meanB += b[index]
	}
	meanA /= float64(count)
	meanB /= float64(count)
	var dot, sumA, sumB float64
	for index := 0; index < count; index++ {
		left := a[index] - meanA
		right := b[index] - meanB
		dot += left * right
		sumA += left * left
		sumB += right * right
	}
	if sumA == 0 || sumB == 0 {
		return 0, count, false
	}
	return dot / math.Sqrt(sumA*sumB), count, true
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// ErrNoSuggestion marks a comparison that could not produce a candidate at all,
// as opposed to one that produced candidates and refused them.
var ErrNoSuggestion = errors.New("no candidate offset could be computed")
