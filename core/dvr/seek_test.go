package dvr

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// seekRecording is a recording of one buffer, with the buffer time of its first
// and last frame. The file it names never has to exist here: the plan reasons
// about identities and numbers, not about bytes.
func seekRecording(id string, source core.SourceID, epoch uint64, instance string, start, end time.Duration) Recording {
	return Recording{
		ID:               id,
		Path:             `C:\dvr\` + id + `.ts`,
		Source:           source,
		SourceMediaEpoch: epoch,
		BufferInstance:   instance,
		StartBufferTime:  start,
		EndBufferTime:    end,
		JoinedAt:         time.Now(),
	}
}

// seekSource is the ordinary, fully consistent state of one source: a measured
// anchor, a confirmed offset, one range and the recording cut from it.
func seekSource(source core.SourceID, epoch uint64, instance string, media time.Duration, start, end time.Duration) SeekSource {
	recording := seekRecording("rec-"+string(source), source, epoch, instance, start, end)
	return SeekSource{
		Source:    source,
		Epoch:     epoch,
		Instance:  instance,
		Range:     Range{Start: start, End: end, Epoch: epoch},
		Anchor:    anchorFor(recording, start, media),
		Alignment: Alignment{Basis: BasisMeasured},
		Recording: recording,
	}
}

func TestPlanSeekPutsOneEventOnBothContentClocks(t *testing.T) {
	// Two files of one event whose container timestamps start 4s apart. Their
	// content clocks both begin at the same frame, so the same event time must
	// land on the same file position.
	first := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	second := seekSource("source-2", 1, "buf-b", 5466667*time.Microsecond, 0, 30*time.Second)

	plan, err := PlanSeek(6*time.Second, []SeekSource{first, second})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	if plan.Event() != 6*time.Second {
		t.Fatalf("event = %s, want 6s", plan.Event())
	}
	for _, source := range []core.SourceID{"source-1", "source-2"} {
		hop, ok := plan.Hop(source)
		if !ok {
			t.Fatalf("%s has no hop", source)
		}
		if hop.Content != 6*time.Second || hop.Position != 6*time.Second {
			t.Errorf("%s: content %s position %s, want 6s and 6s", source, hop.Content, hop.Position)
		}
		if hop.Basis != BasisMeasured {
			t.Errorf("%s: basis %q, want %q", source, hop.Basis, BasisMeasured)
		}
		if hop.Path == "" || hop.RecordingID == "" {
			t.Errorf("%s: hop does not name a file: %+v", source, hop)
		}
	}
}

func TestPlanSeekKeepsAnOffsetOutOfTheContentClock(t *testing.T) {
	// A confirmed offset is a statement about content, so it moves the event
	// time, and by subtraction it moves the file position back.
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Alignment = Alignment{Offset: 2 * time.Second, Basis: BasisManual}

	plan, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	hop, _ := plan.Hop("source-1")
	if hop.Content != 8*time.Second || hop.Position != 8*time.Second {
		t.Fatalf("content %s position %s, want 8s and 8s", hop.Content, hop.Position)
	}
}

func TestPlanSeekStartsAFileCutMidBuffer(t *testing.T) {
	// Asking for 8s from a buffer whose file was cut at 6s gives position 2s,
	// not 8s: the file starts at the first frame it could keep.
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 6*time.Second, 30*time.Second)

	plan, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	hop, _ := plan.Hop("source-1")
	if hop.Position != 0 {
		t.Fatalf("position %s at the recording's first frame, want 0s", hop.Position)
	}

	plan, err = PlanSeek(8*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek at 8s: %v", err)
	}
	hop, _ = plan.Hop("source-1")
	if hop.Position != 2*time.Second {
		t.Fatalf("position %s two seconds into the recording, want 2s", hop.Position)
	}
}

func TestPlanSeekRefusesAnUnmeasuredAnchor(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Anchor.MeasuredAt = time.Time{}

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "not measured") {
		t.Errorf("err = %v, want it to name the missing measurement", err)
	}
}

func TestPlanSeekRefusesAnUnconfirmedOffset(t *testing.T) {
	// The numbers line up — both buffers report 0s..30s — but nothing
	// established that zero. Equal content clocks are not evidence that two
	// buffers show the same frames.
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Alignment = Alignment{Offset: 0, Basis: BasisNone}

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "never established") {
		t.Errorf("err = %v, want it to say the offset was never established", err)
	}
}

func TestPlanSeekRefusesAGapInsideTheWindow(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	// The buffer holds 10s..30s; 6s was never stored, so nothing may be
	// authorised there even though the anchor could name a container time.
	source.Range = Range{Start: 10 * time.Second, End: 30 * time.Second, Epoch: 1}

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "gap or a skipped segment") {
		t.Errorf("err = %v, want it to name the gap", err)
	}
}

func TestPlanSeekRefusesAnotherEpoch(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	// The buffer moved to epoch 2 while the anchor still describes epoch 1.
	source.Epoch = 2
	source.Recording.SourceMediaEpoch = 2

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "epoch 1") || !strings.Contains(err.Error(), "epoch 2") {
		t.Errorf("err = %v, want it to name both epochs", err)
	}
}

func TestPlanSeekRefusesAnotherBufferInstance(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Instance = "buf-rebuilt"

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "buf-a") || !strings.Contains(err.Error(), "buf-rebuilt") {
		t.Errorf("err = %v, want it to name both buffers", err)
	}
}

func TestPlanSeekRefusesAnEventOutsideTheRecording(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Range = Range{Start: 0, End: 60 * time.Second, Epoch: 1}
	source.Anchor = anchorFor(source.Recording, 0, 1466667*time.Microsecond)
	source.Anchor.Interval = Range{Start: 0, End: 60 * time.Second}

	// The recording's last frame is the end of the half-open interval, so the
	// frame at 30s is not in the file anymore.
	if _, err := PlanSeek(30*time.Second, []SeekSource{source}); err == nil {
		t.Fatal("PlanSeek accepted the recording's end frame")
	}

	// A target inside the file is fine.
	if _, err := PlanSeek(29*time.Second, []SeekSource{source}); err != nil {
		t.Fatalf("PlanSeek at 29s: %v", err)
	}
}

func TestSeekPlanGoesStaleWhenTheBufferChanges(t *testing.T) {
	first := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	second := seekSource("source-2", 1, "buf-b", 5466667*time.Microsecond, 0, 30*time.Second)
	plan, err := PlanSeek(6*time.Second, []SeekSource{first, second})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	if err := plan.Matches([]SeekSource{first, second}); err != nil {
		t.Fatalf("the unchanged state does not match: %v", err)
	}

	// The second source was rebuilt: same source id, another buffer.
	second.Instance = "buf-rebuilt"
	second.Recording = seekRecording("rec-source-2", "source-2", 1, "buf-rebuilt", 0, 30*time.Second)
	second.Anchor = anchorFor(second.Recording, 0, 5466667*time.Microsecond)

	err = plan.Matches([]SeekSource{first, second})
	if err == nil {
		t.Fatal("the plan still matched after the buffer was rebuilt")
	}
	if !strings.Contains(err.Error(), "no longer holds") {
		t.Errorf("err = %v, want it to say the plan no longer holds", err)
	}
}

func TestPlanSeekRefusesOverflow(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Alignment = Alignment{Offset: 2 * time.Second, Basis: BasisManual}
	source.Range = Range{Start: 0, End: math.MaxInt64 - time.Second, Epoch: 1}
	source.Anchor.Interval = Range{Start: 0, End: math.MaxInt64 - time.Second}

	_, err := PlanSeek(math.MaxInt64-time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "cannot be represented") {
		t.Errorf("err = %v, want it to name the overflow", err)
	}
}

func TestPlanSeekRefusesTheSameSourceTwice(t *testing.T) {
	first := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	if _, err := PlanSeek(6*time.Second, []SeekSource{first, first}); err == nil {
		t.Fatal("PlanSeek accepted the same source twice")
	}
	if _, err := PlanSeek(6*time.Second, nil); err == nil {
		t.Fatal("PlanSeek accepted an empty list")
	}
}

func TestPlanSeekRefusesMidJoinWindowsThatOnlyLookAligned(t *testing.T) {
	// Two buffers of one live stream joined at different moments. Both report
	// content 0s..12s from their own first segment, which looks like a shared
	// window and is not one. Nothing established their content offset, so the
	// equal numbers must not authorise a seek.
	first := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 12*time.Second)
	second := seekSource("source-2", 1, "buf-b", 5466667*time.Microsecond, 0, 12*time.Second)
	first.Alignment = Alignment{Basis: BasisNone}
	second.Alignment = Alignment{Basis: BasisNone}

	if _, err := PlanSeek(6*time.Second, []SeekSource{first, second}); err == nil {
		t.Fatal("PlanSeek authorised two buffers that were never aligned")
	}

	// With the offset confirmed, the numbers still have to hold: a target past
	// the shorter buffer is refused rather than clamped to its end.
	first.Alignment = Alignment{Basis: BasisMeasured}
	second.Alignment = Alignment{Basis: BasisMeasured}
	_, err := PlanSeek(20*time.Second, []SeekSource{first, second})
	if err == nil {
		t.Fatal("PlanSeek authorised a target past the buffer")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("err = %v, want it to name the window", err)
	}
}

func TestPlanSeekRefusesAnAnchorForAnotherSource(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	// The numbers were measured on another source's buffer clock.
	source.Anchor.Source = "source-2"

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "source-2") || !strings.Contains(err.Error(), "source-1") {
		t.Errorf("err = %v, want it to name both sources", err)
	}
}

func TestPlanSeekRefusesARangeFromAnotherEpoch(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	// The stretch of media was taken in epoch 2, but the buffer is in epoch 1.
	source.Range.Epoch = 2

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "epoch") {
		t.Errorf("err = %v, want it to name the epoch", err)
	}
}

func TestConfirmedAcceptsOnlyNamedBases(t *testing.T) {
	for _, basis := range []Basis{BasisManual, BasisMeasured, BasisSharedStart} {
		if !(Alignment{Offset: 0, Basis: basis}).Confirmed() {
			t.Errorf("basis %q was not accepted although it names an origin", basis)
		}
	}
	for _, basis := range []Basis{BasisNone, Basis("invented"), Basis("manual ")} {
		if (Alignment{Offset: 0, Basis: basis}).Confirmed() {
			t.Errorf("basis %q was accepted although nobody defined it", basis)
		}
	}
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Alignment = Alignment{Basis: Basis("invented")}
	if _, err := PlanSeek(6*time.Second, []SeekSource{source}); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
}

func TestPlanSeekRefusesWhenTheMeasuredSegmentIsGone(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	// The buffer evicted the media before 2s, so the anchor's measurement at
	// 0s no longer describes anything the buffer holds.
	source.Range.Start = 2 * time.Second

	_, err := PlanSeek(5*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "evicted") {
		t.Errorf("err = %v, want it to say the measured media is gone", err)
	}
}

func TestPlanSeekRefusesAnUnidentifiedRecording(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.Recording.ID = ""
	source.Recording.Path = ""

	_, err := PlanSeek(6*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "no identity") {
		t.Errorf("err = %v, want it to say the file has no identity", err)
	}
}

func TestSeekPlanGoesStaleWhenTheAnchorIsRemeasured(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	plan, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	// Same buffer, same file, same projected position, but the mapping was
	// measured again against a different container start.
	source.Anchor.MediaStart += time.Second

	// The projections are unchanged, which is exactly why comparing them would
	// have accepted the stale plan.
	moved, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek after re-measuring: %v", err)
	}
	wasHop, _ := plan.Hop("source-1")
	nowHop, _ := moved.Hop("source-1")
	if wasHop != nowHop {
		t.Fatalf("the projection changed (%+v -> %+v), so this case no longer tests identity", wasHop, nowHop)
	}

	err = plan.Matches([]SeekSource{source})
	if err == nil {
		t.Fatal("Matches accepted a plan whose anchor was re-measured")
	}
	if !strings.Contains(err.Error(), "re-measured") {
		t.Errorf("err = %v, want it to say the anchor was re-measured", err)
	}
}

func TestSeekPlanGoesStaleWhenThePlayerIsReplaced(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	source.PlayerGeneration = 3
	plan, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	source.PlayerGeneration = 4

	err = plan.Matches([]SeekSource{source})
	if err == nil {
		t.Fatal("Matches accepted a plan made for a player that was replaced")
	}
	if !strings.Contains(err.Error(), "player generation") {
		t.Errorf("err = %v, want it to name the player generation", err)
	}
	if hop, ok := plan.Hop("source-1"); !ok || hop.Generation != 3 {
		t.Errorf("hop generation = %d, want the 3 the plan was made for", hop.Generation)
	}
}

func TestSeekPlanHopsAreACopy(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 1466667*time.Microsecond, 0, 30*time.Second)
	plan, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	before, _ := plan.Hop("source-1")
	hops := plan.Hops()
	hops[0].Position = 99 * time.Second
	hops[0].Content = 99 * time.Second
	hops = append(hops, SeekHop{Source: "source-9"})

	after, _ := plan.Hop("source-1")
	if after != before {
		t.Fatalf("the plan changed through its copy: %+v -> %+v", before, after)
	}
	if len(plan.Hops()) != 1 {
		t.Fatalf("plan grew to %d hops through the copy", len(plan.Hops()))
	}
}

func TestSeekPlanEventCannotBeChanged(t *testing.T) {
	source := seekSource("source-1", 1, "buf-a", 2*time.Second, 0, 30*time.Second)
	plan, err := PlanSeek(5*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek: %v", err)
	}
	if plan.Event() != 5*time.Second {
		t.Fatalf("event = %s, want 5s", plan.Event())
	}
	hop, ok := plan.Hop("source-1")
	if !ok {
		t.Fatal("the plan has no hop for source-1")
	}
	if hop.Content != 5*time.Second || hop.Position != 5*time.Second {
		t.Fatalf("hop = content %s position %s, want 5s/5s", hop.Content, hop.Position)
	}

	// The target is not a field a caller can substitute: a plan decided for
	// another event is a different decision with its own hops.
	other, err := PlanSeek(6*time.Second, []SeekSource{source})
	if err != nil {
		t.Fatalf("PlanSeek 6s: %v", err)
	}
	if other.Event() == plan.Event() {
		t.Fatalf("two decisions report the same target %s", plan.Event())
	}
	if plan.identities == nil || other.identities == nil {
		t.Fatal("a decided plan must name its identities")
	}

	// A plan that was never decided cannot be used as one.
	var forged SeekPlan
	if err := forged.Matches([]SeekSource{source}); err == nil {
		t.Fatal("the zero plan matched")
	}
}

func TestPlanSeekRefusesAnUnnamedBufferInstance(t *testing.T) {
	// Three blanks are equal, but nothing proves they are the same buffer.
	source := seekSource("source-1", 1, "", 2*time.Second, 0, 30*time.Second)
	_, err := PlanSeek(5*time.Second, []SeekSource{source})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
	if !strings.Contains(err.Error(), "no name") {
		t.Fatalf("err = %v, want a refusal naming the missing buffer", err)
	}
}
