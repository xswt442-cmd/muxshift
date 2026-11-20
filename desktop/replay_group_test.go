package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/align"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"github.com/xswt442-cmd/muxshift/core/hls"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests were grouped from alignreport_test.go, anchor_epoch_test.go, dvr_read_test.go, dvrusage_test.go, replay_clock_test.go, replay_enter_test.go, replay_enter_txn_test.go, replay_entry_test.go, replay_report_test.go, replay_seek_test.go, replay_txn_test.go, replay_wiring_test.go, timebase_test.go.

func suggestionFor(reference, target string) alignSuggestion {
	return alignSuggestion{
		Reference: alignWindow{Source: "source-1", Index: 0, URI: reference, Track: "track-1", Start: 0, Length: 30 * time.Second},
		Target:    alignWindow{Source: "source-2", Index: 1, URI: target, Track: "track-2", Start: 4 * time.Second, Length: 26 * time.Second},
		Snapshot: probeSnapshot{
			Track:   "track-2",
			Offset:  time.Second,
			Aligned: true,
		},
		Result: align.Result{
			Offset:       0,
			Peak:         0.918,
			RunnerUp:     0.152,
			RunnerOffset: 2400 * time.Millisecond,
			Confidence:   0.861,
			Overlap:      25900 * time.Millisecond,
			Accepted:     true,
		},
		CoreOffset: 2 * time.Second,
		Current:    time.Second,
		Delta:      time.Second,
		Elapsed:    387 * time.Millisecond,
	}
}

func TestTheReportShowsEveryReasonToTrustOrDoubtIt(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	lines := strings.Join(app.alignLines(), "\n")
	for _, want := range []string{
		"source 2 (track-2) compared with source 1 (track-1)",
		"windows 4s..30s · 0s..30s · matched 25.9s of 26s",
		"peak 0.918 · next 0.152 at +2.400s · confidence 0.861",
		"suggested +2.000s · now +1.000s · difference +1.000s · took 387ms",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("the report does not say %q:\n%s", want, lines)
		}
	}
	if state := app.reportOf(app.lastProbe).state(); state != "proposed" {
		t.Fatalf("a fresh accepted report is in state %q, want proposed", state)
	}
}

func TestARefusedReportSaysWhyAndProposesNothing(t *testing.T) {
	app := alignApp()
	suggestion := suggestionFor("a.m3u8", "b.m3u8")
	suggestion.Result.Accepted = false
	suggestion.Result.Reason = "peak 0.092 is below the 0.250 a match needs"
	app.lastProbe = suggestion
	lines := strings.Join(app.alignLines(), "\n")
	if !strings.Contains(lines, "no suggestion: peak 0.092 is below the 0.250 a match needs") {
		t.Fatalf("a refused report does not say why:\n%s", lines)
	}
	if strings.Contains(lines, "suggested ") {
		t.Fatalf("a refused report proposes something:\n%s", lines)
	}
	if state := app.reportOf(app.lastProbe).state(); state != "refused" {
		t.Fatalf("state %q, want refused", state)
	}
}

// TestARecentFailureSetsTheInspectorTone pins the colour contract: a failed
// comparison is the news and is coloured as a failure, even while an older
// proposal is still valid enough to be shown below it.
func TestARecentFailureSetsTheInspectorTone(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	if got := app.alignState(); got != "proposed" {
		t.Fatalf("a fresh proposal reads as %q, want proposed", got)
	}

	app.lastProbeErr = "c.m3u8 has no audio track to compare"
	app.lastProbeFail = &probeFailure{
		Reference: alignWindow{Index: 0, Source: "source-1", Track: "track-2", URI: "a.m3u8"},
		Target:    alignWindow{Index: 2, Source: "source-3", URI: "c.m3u8"},
		Reason:    app.lastProbeErr,
	}
	if got := app.alignState(); got != "failed" {
		t.Fatalf("a failed comparison reads as %q, want failed", got)
	}
	if got := alignTone(app.alignState()); got != colorWarn {
		t.Fatalf("the failure is coloured %v, want the warning colour", got)
	}
	if got := app.reportOf(app.lastProbe).state(); got == "failed" {
		t.Fatalf("the older proposal's own state was rewritten as a failure")
	}

	// Clearing the failure lets the older proposal speak for itself again.
	app.lastProbeErr = ""
	app.lastProbeFail = nil
	if got := app.alignState(); got != "proposed" {
		t.Fatalf("after the failure cleared the block reads as %q, want proposed", got)
	}
}

// A probe that finishes after somebody nudged the target no longer describes
// the session; the report has to say so rather than show the old proposal as
// if it were current.
func TestAReportGoesStaleWhenTheSessionMoves(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	if expired := app.reportOf(app.lastProbe).Expired; expired != "" {
		t.Fatalf("a report read from a matching session is already expired: %s", expired)
	}

	app.sync.timeline.SetShift("track-2", 750*time.Millisecond)
	report := app.reportOf(app.lastProbe)
	if report.Expired != "the offset of the target changed since the probe started" {
		t.Fatalf("expiry reads %q", report.Expired)
	}
	if state := report.state(); state != "expired" {
		t.Fatalf("state %q, want expired", state)
	}
	if !strings.Contains(strings.Join(report.lines(), "\n"), "expired: the offset of the target changed") {
		t.Fatalf("the expired report does not say it expired:\n%s", strings.Join(report.lines(), "\n"))
	}
}

func TestShowingTheReportChangesNothing(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	before, ok := app.sync.timeline.Shift("track-2")
	_ = app.alignLines()
	_ = app.alignLine()
	_ = app.reportOf(app.lastProbe).state()
	after, still := app.sync.timeline.Shift("track-2")
	if ok != still || before != after {
		t.Fatalf("showing the report changed the target's shift: %+v/%t -> %+v/%t", before, ok, after, still)
	}
	if _, has := app.sync.timeline.Shift("track-1"); has {
		t.Fatalf("showing the report aligned a track nobody aligned")
	}
}

// alignApp is a session as a finished probe saw it: two sources present with the
// generation the probe recorded, and the target's offset where it was.
func alignApp() *appState {
	app := &appState{sync: newSyncState()}
	app.items = []*item{
		{source: core.Source{ID: "source-1"}},
		{source: core.Source{ID: "source-2"}},
	}
	app.sync.timeline.SetShift("track-2", time.Second)
	return app
}

// TestTheReportNamesTheTrackItCompared keeps the identity honest: a proposal
// describes one track's offset, and the report has to say which.
func TestTheReportNamesTheTrackItCompared(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	first := app.alignLines()[0]
	if !strings.Contains(first, "source 2 (track-2)") || !strings.Contains(first, "source 1 (track-1)") {
		t.Fatalf("the report does not name the tracks it compared: %q", first)
	}
}

// TestAFailedComparisonSaysSoFirst covers the newest attempt failing while an
// older proposal still exists: the failure is the news, it is named after the
// sources it actually compared, and the older proposal may only be shown as what
// the failure would have replaced.
func TestAFailedComparisonSaysSoFirst(t *testing.T) {
	app := alignApp()
	app.lastProbe = suggestionFor("a.m3u8", "b.m3u8")
	app.lastProbeErr = "c.m3u8 has no audio track to compare"
	app.lastProbeFail = &probeFailure{
		Reference: alignWindow{Index: 0, Source: "source-1", Track: "track-2", URI: "a.m3u8"},
		Target:    alignWindow{Index: 2, Source: "source-3", Track: "", URI: "c.m3u8"},
		Reason:    app.lastProbeErr,
	}
	lines := app.alignLines()
	if !strings.Contains(lines[0], "the last comparison did not finish:") {
		t.Fatalf("a failed comparison is not the first line: %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "c.m3u8") {
		t.Fatalf("the failure is not named after the source it compared: %q", lines[0])
	}
	if strings.Contains(lines[0], "b.m3u8") {
		t.Fatalf("the failure borrowed the older proposal's target: %q", lines[0])
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "an earlier comparison") {
		t.Fatalf("the older proposal is not marked as an earlier one:\n%s", joined)
	}
	if line := app.alignLine(); !strings.Contains(line, "c.m3u8") {
		t.Fatalf("the status bar names another source for the failure: %q", line)
	}
}

// TestAFailedComparisonAloneStillShowsSomething covers the first attempt.
func TestAFailedComparisonAloneStillShowsSomething(t *testing.T) {
	app := alignApp()
	app.lastProbeErr = "the two sources could not be decoded"
	lines := app.alignLines()
	if len(lines) != 1 || !strings.Contains(lines[0], "did not finish") {
		t.Fatalf("a single failed comparison shows %q", strings.Join(lines, "\n"))
	}
}

// TestASupersededComparisonDoesNotBecomeTheAccount pins the attempt number: a
// result that finished after a newer comparison started must be dropped, not
// written over the newest attempt.
func TestASupersededComparisonDoesNotBecomeTheAccount(t *testing.T) {
	app := alignApp()
	app.probeAttempt = 2
	app.probeDone = 1
	// Attempt 1 finishes late with a proposal; it must not be published.
	app.applyProbeResult(1, suggestionFor("a.m3u8", "b.m3u8"), nil,
		alignWindow{}, nil, alignWindow{}, nil)
	if hasProbe(app.lastProbe) {
		t.Fatal("an earlier attempt's proposal became the session's account")
	}
	if app.probeRunning {
		t.Fatal("an earlier attempt cleared the in-flight flag")
	}
	// The attempt the session is waiting for publishes normally.
	app.applyProbeResult(2, suggestionFor("a.m3u8", "b.m3u8"), nil,
		alignWindow{Index: 0, Source: "source-1", Track: "track-2", URI: "a.m3u8"}, nil,
		alignWindow{Index: 1, Source: "source-2", Track: "track-3", URI: "b.m3u8"}, nil)
	if !hasProbe(app.lastProbe) {
		t.Fatal("the newest attempt's proposal was dropped")
	}
	if app.probeDone != 2 {
		t.Fatalf("probeDone = %d, want 2", app.probeDone)
	}
}

func fixtureBuffer(t *testing.T) (*dvr.Buffer, func(sequence int, discontinuity bool)) {
	t.Helper()
	return fixtureBufferOptions(t, dvr.Options{})
}

// fixtureBufferOptions is fixtureBuffer with the caller's limits, which is how a
// test stages eviction: Retention makes the oldest segment removable.
func fixtureBufferOptions(t *testing.T, opts dvr.Options) (*dvr.Buffer, func(sequence int, discontinuity bool)) {
	t.Helper()
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the fixture segment: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	opts.Dir = t.TempDir()
	opts.RecordingsDir = t.TempDir()
	if opts.Source == "" {
		opts.Source = "fixture"
	}
	buffer, err := dvr.New(opts)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	t.Cleanup(func() { _ = buffer.Close() })
	add := func(sequence int, discontinuity bool) {
		t.Helper()
		if _, err := buffer.Add(context.Background(), server.Client(), hls.Segment{
			Sequence:      sequence,
			URI:           server.URL + "/seg.ts",
			Duration:      2 * time.Second,
			Discontinuity: discontinuity,
		}); err != nil {
			t.Fatalf("add segment %d: %v", sequence, err)
		}
	}
	add(1, false)
	return buffer, add
}

// stubProbe replaces the measurement for the duration of one test.
func stubProbe(t *testing.T, media time.Duration, during func()) {
	t.Helper()
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		if during != nil {
			during()
		}
		return media, nil
	}
}

// TestMeasureAnchorKeepsTheEpochOfItsSegment runs the race the epoch check has
// to survive: the playlist declares a discontinuity while the segment is being
// measured. The anchor belongs to the media it measured, so it has to name the
// clock that media was stored in, and asking for a window afterwards has to be
// refused instead of silently pairing old media with the new clock.
func TestMeasureAnchorKeepsTheEpochOfItsSegment(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	point, err := buffer.OldestSegment()
	if err != nil {
		t.Fatalf("oldest segment: %v", err)
	}
	jumped := false
	stubProbe(t, 55*time.Second, func() {
		if !jumped {
			jumped = true
			add(2, true)
		}
	})

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	anchor, err := runner.measureAnchor(point)
	if err != nil {
		t.Fatalf("measure the anchor: %v", err)
	}
	if anchor.Epoch != 1 {
		t.Fatalf("the anchor names epoch %d, want the epoch of the segment it measured (1)", anchor.Epoch)
	}
	if got := buffer.Epoch(); got != 2 {
		t.Fatalf("the buffer's epoch is %d, want 2 after the jump", got)
	}
	// The pair measured at the old point describes a clock the buffer has left,
	// so it must not be handed out for the new one; a fresh measurement of the
	// new point is what restores a usable anchor.
	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil {
		t.Fatalf("the anchor measured in epoch 1 was used for the new clock: %+v", anchor)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fresh, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 2 * time.Second, End: 4 * time.Second})
	if err != nil {
		t.Fatalf("measuring the new clock: %v", err)
	}
	if fresh.Epoch != 2 {
		t.Fatalf("the new anchor names epoch %d, want 2", fresh.Epoch)
	}
}

// TestFailedAnchorRetriesAfterNextSegmentInSameEpoch pins the retry trigger. The
// point an anchor describes does not move while a range grows, so "the buffer
// stored another segment" is a different question from "which point is
// measured". A failed measurement must be retried once new media arrives, must
// not be retried on every poll, and a measurement that succeeded must stand.
func TestFailedAnchorRetriesAfterNextSegmentInSameEpoch(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	refused := errors.New("ffprobe could not read the segment")

	var mu sync.Mutex
	calls := 0
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			return 0, refused
		}
		return 100 * time.Second, nil
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runner.refreshAnchor()
	if _, err := runner.waitAnchor(ctx); !errors.Is(err, refused) {
		t.Fatalf("waiting on a failed measurement reports %v, want the probe's error", err)
	}
	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil {
		t.Fatalf("a failed measurement produced an anchor: %+v", anchor)
	}

	// Nothing new has arrived, so nothing new can be learned: polling must not
	// start another measurement.
	after := count()
	for i := 0; i < 5; i++ {
		runner.refreshAnchor()
	}
	time.Sleep(50 * time.Millisecond)
	if got := count(); got != after {
		t.Fatalf("the buffer measured %d times without a new segment, want %d", got, after)
	}

	// A second segment in the same epoch is the trigger.
	add(2, false)
	anchor, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 0, End: 2 * time.Second})
	if err != nil {
		t.Fatalf("the retry after new media did not produce an anchor: %v", err)
	}
	if anchor.Epoch != 1 {
		t.Fatalf("the retried anchor names epoch %d, want 1", anchor.Epoch)
	}
	if anchor.BufferStart != 0 {
		t.Fatalf("the retried anchor starts at buffer %s, want the measured segment's 0s", anchor.BufferStart)
	}
	if got := count(); got != after+1 {
		t.Fatalf("the retry measured %d times, want one more than %d", got, after)
	}

	// A measurement that succeeded stands: growing the range does not make it
	// stale, and re-measuring would only cost another ffprobe.
	add(3, false)
	runner.refreshAnchor()
	time.Sleep(50 * time.Millisecond)
	if got := count(); got != after+1 {
		t.Fatalf("a successful acquisition was measured again (%d calls, want %d)", got, after+1)
	}
}

// TestFailedAnchorFallsBackToTheNewestSegment runs the other half of the retry
// policy: after the same segment fails twice, the runner stops insisting on it
// and measures what the buffer has, because the older media may simply be
// unreadable. The replacement anchor starts where that segment started, so every
// window before it has to be refused instead of extrapolated backwards.
func TestFailedAnchorFallsBackToTheNewestSegment(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		if strings.HasSuffix(path, "seg-00000003.ts") {
			return 100 * time.Second, nil
		}
		return 0, errors.New("ffprobe could not read the segment")
	}

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runner.refreshAnchor()
	if _, err := runner.waitAnchor(ctx); err == nil {
		t.Fatal("the first measurement reported no error, want the probe's")
	}
	add(2, false)
	runner.refreshAnchor()
	if _, err := runner.waitAnchor(ctx); err == nil {
		t.Fatal("the retry reported no error, want the probe's")
	}

	// Third attempt: the first segment could not be read twice, so the newest
	// segment becomes the measurement point.
	add(3, false)
	runner.refreshAnchor()
	anchor, err := runner.waitAnchor(ctx)
	if err != nil {
		t.Fatalf("falling back to the newest segment: %v", err)
	}
	if anchor.BufferStart != 4*time.Second {
		t.Fatalf("the replacement anchor starts at buffer %s, want the newest segment's 4s", anchor.BufferStart)
	}
	if anchor.MediaStart != 100*time.Second {
		t.Fatalf("the replacement anchor measured %s, want 100s", anchor.MediaStart)
	}
	if got, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 4 * time.Second}); err == nil {
		t.Fatalf("a window before the replacement anchor was authorised: %+v", got)
	}
	if _, err := runner.bufferAnchor(dvr.Range{Start: 4 * time.Second, End: 6 * time.Second}); err != nil {
		t.Fatalf("a window at the replacement anchor: %v", err)
	}
}

// TestBufferAnchorRefusesAWindowAcrossASkippedSegment is the same-epoch hazard:
// the playlist skips segments, the buffer splits into two ranges, and the epoch
// does not move. A window that reaches across the hole can only be authorised if
// one continuous range holds all of it, which is exactly what the split denies.
func TestBufferAnchorRefusesAWindowAcrossASkippedSegment(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	// Segment 1 fills 0s..2s; segment 4 skips 2 and 3, which ends that range and
	// starts the next one where the skipped media would have been.
	add(4, false)
	ranges := buffer.Ranges()
	if len(ranges) != 2 {
		t.Fatalf("ranges = %v, want two after the skipped segments", ranges)
	}
	stubProbe(t, 50*time.Second, nil)

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The measurement follows the buffer's current range, so the anchor belongs
	// to the media after the skipped segment.
	anchor, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 2 * time.Second, End: 4 * time.Second})
	if err != nil {
		t.Fatalf("the current range: %v", err)
	}
	if anchor.BufferStart != 2*time.Second {
		t.Fatalf("the anchor starts at buffer %s, want the current range's 2s", anchor.BufferStart)
	}

	// Nothing measured before the gap can explain the hole, so a window that
	// spans it is refused rather than extended over it.
	crossed, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 4 * time.Second})
	if err == nil {
		t.Fatalf("a window across the skipped segments was authorised: %+v", crossed)
	}
	if !strings.Contains(err.Error(), "before the measured segment") &&
		!strings.Contains(err.Error(), "continuous range") {
		t.Fatalf("the refusal reads %q, want it to point at the range", err)
	}
	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 2 * time.Second, End: 4 * time.Second}); err != nil {
		t.Fatalf("a window inside the measured range: %v (anchor %+v)", err, anchor)
	}
}

// TestMeasuredRangeStopsAuthorisingWhenEvicted keeps an old measurement from
// outliving the media it measured. Once the measured range is gone the window it
// used to explain has no media behind it, so it must be refused — the anchor is a
// statement about stored segments, not a licence to keep replaying their times.
func TestMeasuredRangeStopsAuthorisingWhenEvicted(t *testing.T) {
	buffer, add := fixtureBufferOptions(t, dvr.Options{Retention: 3 * time.Second})
	stubProbe(t, 100*time.Second, nil)
	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := runner.waitAnchor(ctx); err != nil {
		t.Fatalf("measuring the first range: %v", err)
	}

	add(2, false)
	if removed := buffer.Evict(nil); len(removed) != 1 || removed[0] != 1 {
		t.Fatalf("evicted %v, want the measured segment 1", removed)
	}
	if ranges := buffer.Ranges(); len(ranges) != 1 || ranges[0].Start != 2*time.Second {
		t.Fatalf("ranges after eviction = %v, want just 2s..4s", ranges)
	}

	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil {
		t.Fatalf("an evicted range was still authorised: %+v", anchor)
	}
	if anchor, err := runner.bufferAnchor(dvr.Range{Start: 2 * time.Second, End: 4 * time.Second}); err == nil {
		t.Fatalf("the new range used the anchor of the evicted one: %+v", anchor)
	}
}

// TestOldRangeReplaysUnderItsOwnIdentity keeps history usable without letting it
// borrow a newer clock: a recording cut before the discontinuity stays replayable
// through the anchor measured on its own media, and the anchor measured after the
// jump must not be used for it.
func TestOldRangeReplaysUnderItsOwnIdentity(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	add(2, false)
	stubProbe(t, 100*time.Second, nil)
	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 0, End: 4 * time.Second})
	if err != nil {
		t.Fatalf("measuring before the jump: %v", err)
	}
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("cut a recording: %v", err)
	}
	if _, err := replayFileFor(recording, before); err != nil {
		t.Fatalf("the recording does not replay under its own anchor: %v", err)
	}

	// The playlist declares a discontinuity: the epoch moves and a new anchor is
	// measured for the media after it.
	add(3, true)
	after, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 4 * time.Second, End: 6 * time.Second})
	if err != nil {
		t.Fatalf("measuring after the jump: %v", err)
	}
	if after.Epoch == before.Epoch {
		t.Fatalf("both anchors name epoch %d, want different clocks", after.Epoch)
	}
	if _, err := replayFileFor(recording, after); err == nil {
		t.Fatal("the recording accepted the anchor measured after the discontinuity")
	}
}

// TestCurrentSegmentFollowsTheClock guards the choice of measurement point: after
// a jump the oldest segment is old media, so the buffer has to offer the first
// segment of the range it is filling now.
func TestCurrentSegmentFollowsTheClock(t *testing.T) {
	buffer, add := fixtureBuffer(t)
	add(2, true)
	current, err := buffer.CurrentSegment()
	if err != nil {
		t.Fatalf("current segment: %v", err)
	}
	if current.Epoch != 2 {
		t.Fatalf("the current point is in epoch %d, want 2", current.Epoch)
	}
	oldest, err := buffer.OldestSegment()
	if err != nil {
		t.Fatalf("oldest segment: %v", err)
	}
	if oldest.Epoch != 1 {
		t.Fatalf("the oldest point is in epoch %d, want 1", oldest.Epoch)
	}
}

// TestBufferAnchorDoesNotWaitForTheMeasurement keeps ffprobe off the drawing
// thread: a caller asking while the measurement runs is told to come back, and
// the answer arrives once it is done.
func TestBufferAnchorDoesNotWaitForTheMeasurement(t *testing.T) {
	buffer, _ := fixtureBuffer(t)
	release := make(chan struct{})
	original := probeFirstMediaTime
	t.Cleanup(func() { probeFirstMediaTime = original })
	probeFirstMediaTime = func(ctx context.Context, path string) (time.Duration, error) {
		<-release
		return 100 * time.Second, nil
	}

	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	runner.refreshAnchor()
	if _, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second}); err == nil ||
		!strings.Contains(err.Error(), "still measuring") {
		t.Fatalf("an unfinished measurement reports %v, want a come-back-later error", err)
	}
	close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	anchor, err := runner.waitBufferAnchor(ctx, dvr.Range{Start: 0, End: 2 * time.Second})
	if err != nil {
		t.Fatalf("waiting for the measurement: %v", err)
	}
	if anchor.MediaStart != 100*time.Second || anchor.Interval.End != 2*time.Second {
		t.Fatalf("the anchor measures %s and covers to %s", anchor.MediaStart, anchor.Interval.End)
	}
}

const aPlaylistOverOneSegment = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
#EXT-X-PROGRAM-DATE-TIME:2026-01-01T00:00:00.000Z
seg0.ts
#EXT-X-ENDLIST
`

// TestAFailedSegmentReadIsNamedAndClearsItself keeps an outage visible when the
// playlist is fine but the segment is not. The page used to clear the failure as
// soon as the playlist parsed, so a source whose segments were refused read as
// healthy; the row now keeps the failure until a segment is actually stored.
func TestAFailedSegmentReadIsNamedAndClearsItself(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the segment fixture: %v", err)
	}
	var serving atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte(aPlaylistOverOneSegment))
		case "/seg0.ts":
			if !serving.Load() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := startDVR(server.URL+"/index.m3u8", dvr.Options{
		Dir:         t.TempDir(),
		Source:      "fixture",
		SourceEpoch: 1,
	})
	if err != nil {
		t.Fatalf("start the buffer: %v", err)
	}
	defer func() { _ = runner.Close() }()

	// The playlist reads; the segment does not. The outage must be named.
	usage := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool { return u.readErr != "" })
	if !strings.Contains(usage.readErr, "404") {
		t.Fatalf("the failure reads %q, want the refused segment", usage.readErr)
	}
	if usage.segments != 0 {
		t.Fatalf("a refused segment was counted as stored: %d segment(s)", usage.segments)
	}
	if !strings.Contains(runner.usageLine("fixture/index.m3u8"), "cannot be read") {
		t.Fatalf("the row does not name the failure: %q", runner.usageLine("fixture/index.m3u8"))
	}

	// The same segment starts working: the failure clears only now.
	serving.Store(true)
	served := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool {
		return u.readErr == "" && u.segments == 1
	})
	if served.segments != 1 {
		t.Fatalf("the segment landed %d times, want once", served.segments)
	}
	if strings.Contains(runner.usageLine("fixture/index.m3u8"), "cannot be read") {
		t.Fatalf("the row still names a failure after a segment landed: %q",
			runner.usageLine("fixture/index.m3u8"))
	}
}

// waitForUsage polls the runner until want agrees, and fails the test with the
// last reading when the deadline passes first.
func waitForUsage(t *testing.T, runner *dvrRunner, within time.Duration, want func(dvrUsage) bool) dvrUsage {
	t.Helper()
	deadline := time.Now().Add(within)
	var usage dvrUsage
	for {
		usage = runner.usage()
		if want(usage) {
			return usage
		}
		if time.Now().After(deadline) {
			t.Fatalf("the buffer never got there: %+v", usage)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// aPlaylistOverTwoSegments offers a second segment after the first, so a poll
// that skips a refused segment can be told apart from one that stops there.
const aPlaylistOverTwoSegments = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
seg0.ts
#EXTINF:2.000000,
seg1.ts
#EXT-X-ENDLIST
`

// TestAMixedPlaylistDoesNotSkipTheSegmentItCouldNotRead is the counterexample the
// review found: the first segment was refused, the second was stored, and the
// cursor moved past the refused one, so it was never fetched again even after it
// became readable. A poll now stops at the first failure instead.
func TestAMixedPlaylistDoesNotSkipTheSegmentItCouldNotRead(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the segment fixture: %v", err)
	}
	var serving atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte(aPlaylistOverTwoSegments))
		case "/seg0.ts":
			if !serving.Load() {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		case "/seg1.ts":
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := startDVR(server.URL+"/index.m3u8", dvr.Options{
		Dir:         t.TempDir(),
		Source:      "fixture",
		SourceEpoch: 1,
	})
	if err != nil {
		t.Fatalf("start the buffer: %v", err)
	}
	defer func() { _ = runner.Close() }()

	// The first segment is refused while the second is fine. Storing the second
	// would move the cursor past the first, so nothing may be stored at all.
	usage := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool { return u.readErr != "" })
	if !strings.Contains(usage.readErr, "404") {
		t.Fatalf("the failure reads %q, want the refused segment", usage.readErr)
	}
	if usage.segments != 0 {
		t.Fatalf("the poll stored %d segment(s) after a refusal, so the refused one was skipped", usage.segments)
	}

	// Once the first segment is readable again both land, in order.
	serving.Store(true)
	served := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool {
		return u.readErr == "" && u.segments == 2
	})
	if served.segments != 2 {
		t.Fatalf("the buffer holds %d segment(s), want both", served.segments)
	}
}

// TestAWindowThatMovedOnDoesNotStallTheBuffer follows the Core's gap rule: a
// segment that dropped out of the live window cannot be fetched again, so the
// buffer continues from what the window now offers instead of waiting forever.
func TestAWindowThatMovedOnDoesNotStallTheBuffer(t *testing.T) {
	body, err := os.ReadFile("../core/hls/testdata/vod/seg0.ts")
	if err != nil {
		t.Fatalf("read the segment fixture: %v", err)
	}
	var phase atomic.Int32
	first := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
seg0.ts
#EXTINF:2.000000,
seg1.ts
`
	later := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:2
#EXTINF:2.000000,
seg2.ts
#EXTINF:2.000000,
seg3.ts
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			if phase.Load() == 0 {
				_, _ = w.Write([]byte(first))
				return
			}
			_, _ = w.Write([]byte(later))
		case "/seg0.ts", "/seg2.ts", "/seg3.ts":
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := startDVR(server.URL+"/index.m3u8", dvr.Options{
		Dir:         t.TempDir(),
		Source:      "fixture",
		SourceEpoch: 1,
	})
	if err != nil {
		t.Fatalf("start the buffer: %v", err)
	}
	defer func() { _ = runner.Close() }()

	// seg0 is stored, seg1 is refused: the cursor sits on seg1.
	waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool {
		return u.readErr != "" && u.segments == 1
	})

	// The window moves on: seg1 is gone and seg2 is what the source offers now.
	phase.Store(1)
	served := waitForUsage(t, runner, 15*time.Second, func(u dvrUsage) bool {
		return u.readErr == "" && u.segments >= 3
	})
	if served.segments < 3 {
		t.Fatalf("the buffer holds %d segment(s), want the window to continue", served.segments)
	}
	if !strings.Contains(runner.state(), "gap") {
		t.Fatalf("the skipped segment left no gap behind: %q", runner.state())
	}
}

// TestASignedAddressIsNotWrittenOut is the security case: a signed address carries
// its token in the query, and the failure it produces quotes the whole address.
// The reason is kept, the token is not — not in the row, and not in the text that
// goes to the log.
func TestASignedAddressIsNotWrittenOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "what the server says", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	signed := server.URL + "/index.m3u8?token=TOP_SECRET_123#key"
	runner, err := startDVR(signed, dvr.Options{
		Dir:         t.TempDir(),
		Source:      "fixture",
		SourceEpoch: 1,
	})
	if err != nil {
		t.Fatalf("start the buffer: %v", err)
	}
	defer func() { _ = runner.Close() }()

	usage := waitForUsage(t, runner, 10*time.Second, func(u dvrUsage) bool { return u.readErr != "" })
	if strings.Contains(usage.readErr, "TOP_SECRET_123") || strings.Contains(usage.readErr, "key") {
		t.Fatalf("the failure repeats the secret: %q", usage.readErr)
	}
	if !strings.Contains(usage.readErr, "503") {
		t.Fatalf("the failure lost the reason: %q", usage.readErr)
	}

	line := runner.usageLine("fixture/index.m3u8")
	if strings.Contains(line, "TOP_SECRET_123") {
		t.Fatalf("the row repeats the secret: %q", line)
	}
	if strings.Contains(line, "token=") {
		t.Fatalf("the row repeats the query: %q", line)
	}
	if got := shortURI(signed); strings.Contains(got, "TOP_SECRET_123") || strings.Contains(got, "token=") {
		t.Fatalf("the short address repeats the secret: %q", got)
	}
	reason := redactSecrets(`Get "` + signed + `": dial tcp: refused`)
	if strings.Contains(reason, "TOP_SECRET_123") || strings.Contains(reason, "key") {
		t.Fatalf("redaction left the secret: %q", reason)
	}
	if !strings.Contains(reason, "refused") {
		t.Fatalf("redaction lost the reason: %q", reason)
	}
}

func TestByteLineMovesUnitsWhereTheyChange(t *testing.T) {
	for _, c := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 bytes"},
		{512, "512 bytes"},
		{2048, "2.0 KiB"},
		{3 << 20, "3.0 MiB"},
		{2 << 30, "2.0 GiB"},
	} {
		if got := byteLine(c.bytes); got != c.want {
			t.Errorf("byteLine(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

// TestACleanupThatKeptAFileSaysWhy is the honesty requirement of the cleanup
// action: a pass that left a file on disk says which file and why, instead of
// reporting a clean sweep.
func TestACleanupThatKeptAFileSaysWhy(t *testing.T) {
	cleanup := dvrCleanup{kept: []string{"rec-1.ts: a player still holds it"}}
	line := cleanup.line()
	if !strings.Contains(line, "removed 0 recording(s)") {
		t.Fatalf("line = %q, want the removal count", line)
	}
	if !strings.Contains(line, "kept 1 (rec-1.ts: a player still holds it)") {
		t.Fatalf("line = %q, want the file and its reason", line)
	}
	if strings.Contains(line, "freed 3") {
		t.Fatalf("line = %q claims bytes it did not free", line)
	}
}

// TestTheBudgetLineIsStatedAsALimit keeps the page from presenting a limit as if
// it were the current usage.
func TestTheBudgetLineIsStatedAsALimit(t *testing.T) {
	a := &appState{budget: builtinDVRBudget(), budgetFrom: "the built-in defaults"}
	line := a.budgetLine()
	if !strings.Contains(line, "keeps 60s") {
		t.Fatalf("line = %q, want the retention it states", line)
	}
	if !strings.Contains(line, "a limit, not what is in use") {
		t.Fatalf("line = %q, want the caveat", line)
	}
	if !strings.Contains(line, "the built-in defaults") {
		t.Fatalf("line = %q, want where the limit came from", line)
	}
}

// TestTwoBuffersMadeInTheSameSecondReadApart keeps the page from claiming two
// buffers are one: instance ids carry a nanosecond stamp, and a short name that
// only keeps the head would cut them down to the same string.
func TestTwoBuffersMadeInTheSameSecondReadApart(t *testing.T) {
	// The counterexample the review named: the same prefix, two counters. The id
	// ends in the Core's per-process counter, so the name keeps it verbatim
	// instead of hoping a digest of the whole id never collides.
	first := "buf-1794510388264000000-265"
	second := "buf-1794510388491000000-492"
	if got, want := instanceName(first), "buf-1794510388-265"; got != want {
		t.Fatalf("the first instance reads %q, want %q", got, want)
	}
	if got, want := instanceName(second), "buf-1794510388-492"; got != want {
		t.Fatalf("the second instance reads %q, want %q", got, want)
	}
	if instanceName(first) == instanceName(second) {
		t.Fatalf("two instances read as one name: %q", instanceName(first))
	}
	// A buffer made in the same nanosecond differs only by that counter, and the
	// two must still be told apart.
	next := "buf-1794510388264000000-266"
	if instanceName(first) == instanceName(next) {
		t.Fatalf("two buffers made in the same nanosecond read as one name: %q", instanceName(first))
	}
	for _, instance := range []string{first, second, next} {
		if name := instanceName(instance); len(name) > 24 {
			t.Fatalf("the name is not short enough for a row: %q (%d characters)", name, len(name))
		}
	}
	if instanceName("") != "buffer" {
		t.Fatalf("an empty instance reads %q", instanceName(""))
	}
	if instanceName("noseparator") != "noseparator" {
		t.Fatalf("an id without a counter reads %q", instanceName("noseparator"))
	}
}

func fixtureRecording(t *testing.T) dvr.Recording {
	t.Helper()
	path := filepath.Join("..", "core", "hls", "testdata", "vod", "seg0.ts")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the test fixture is gone: %v", err)
	}
	return dvr.Recording{
		Path:             path,
		Size:             info.Size(),
		Source:           "fixture",
		SourceMediaEpoch: 3,
		BufferInstance:   "buf-fixture",
		StartBufferTime:  5 * time.Second,
		EndBufferTime:    9 * time.Second,
	}
}

func TestMeasureRecordingAnchorReadsTheFilesFirstFrame(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed, so the first frame cannot be measured")
	}
	recording := fixtureRecording(t)
	anchor, err := measureRecordingAnchor(recording)
	if err != nil {
		t.Fatalf("measuring %s: %v", recording.FileName(), err)
	}
	if !anchor.Measured() {
		t.Fatal("a measured anchor says it was not measured")
	}
	if anchor.Source != recording.Source || anchor.Epoch != recording.SourceMediaEpoch || anchor.BufferInstance != recording.BufferInstance {
		t.Fatalf("the anchor names %s epoch %d instance %q, not the recording's %s epoch %d instance %q",
			anchor.Source, anchor.Epoch, anchor.BufferInstance,
			recording.Source, recording.SourceMediaEpoch, recording.BufferInstance)
	}
	if anchor.BufferStart != recording.StartBufferTime {
		t.Fatalf("the anchor puts buffer time %s at the file's first frame, not %s",
			anchor.BufferStart, recording.StartBufferTime)
	}
	if anchor.Interval.Start != recording.StartBufferTime || anchor.Interval.End != recording.EndBufferTime {
		t.Fatalf("the anchor explains %s, not the recording's %s",
			logRange(anchor.Interval), logRange(dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime}))
	}
	// The measured pair must reproduce itself: the first frame the recording
	// starts at converts to exactly the media time the container states.
	at, err := recording.SourceMediaTime(0, anchor)
	if err != nil {
		t.Fatalf("converting the measured position: %v", err)
	}
	if at != anchor.MediaStart {
		t.Fatalf("the measured start converts to %s, not back to the measured %s", at, anchor.MediaStart)
	}
	// And the content clock is buffer time, not the container's: the measured
	// start is the content time the recording itself names.
	if content, err := anchor.ContentTime(anchor.BufferStart); err != nil || content != recording.StartBufferTime {
		t.Fatalf("the measured start is content %s (err %v), want %s",
			content, err, recording.StartBufferTime)
	}
	if _, err := anchor.ContentTime(recording.EndBufferTime); !errors.Is(err, dvr.ErrNoAnchor) {
		t.Fatalf("a time past the recording gave %v, want ErrNoAnchor", err)
	}
}

func TestReplayEventTimeRefusesWhatItCannotProve(t *testing.T) {
	recording := dvr.Recording{
		Source:           "fixture",
		SourceMediaEpoch: 1,
		BufferInstance:   "buf-fixture",
		StartBufferTime:  5 * time.Second,
		EndBufferTime:    9 * time.Second,
	}
	anchor := dvr.Anchor{
		Source:         "fixture",
		Epoch:          1,
		BufferInstance: "buf-fixture",
		BufferStart:    5 * time.Second,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: 5 * time.Second, End: 9 * time.Second},
		MeasuredAt:     time.Now(),
	}

	cases := []struct {
		name  string
		it    item
		track core.TrackID
		want  string
	}{
		{
			name: "no recording",
			it:   item{uri: "http://example.invalid/live.m3u8"},
			want: "not showing a recording",
		},
		{
			name: "anchor never measured",
			it:   item{uri: "http://example.invalid/live.m3u8", replay: &recording, replayAnchorErr: errors.New("ffprobe is not on PATH")},
			want: "no measured anchor",
		},
		{
			name: "no track",
			it:   item{uri: "http://example.invalid/live.m3u8", replay: &recording, replayAnchor: &anchor},
			want: "has no track in the session",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &appState{sync: newSyncState()}
			_, err := a.replayEventTime(&tc.it, 0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}

	// A track that exists but has no confirmed offset is refused, not treated as
	// an offset of zero.
	it := item{uri: "http://example.invalid/live.m3u8", replay: &recording, replayAnchor: &anchor, video: "track-1"}
	a := &appState{sync: newSyncState()}
	if _, err := a.replayEventTime(&it, time.Second); !errors.Is(err, core.ErrNotAligned) {
		t.Fatalf("an unaligned track gave %v, want ErrNotAligned", err)
	}

	// With a confirmed offset the conversion runs through the buffer's content
	// clock and Core's sign: event = content - offset. Position 1s inside a
	// recording that starts at buffer 5s is content 6s, and minus the 400ms
	// offset 5.6s. The container's own timestamp (100s) never enters it.
	if err := a.sync.timeline.SetShift("track-1", 400*time.Millisecond); err != nil {
		t.Fatalf("aligning the track: %v", err)
	}
	event, err := a.replayEventTime(&it, time.Second)
	if err != nil {
		t.Fatalf("converting a measured position: %v", err)
	}
	if want := 5600 * time.Millisecond; event != want {
		t.Fatalf("position 1s converted to event %s, want %s", event, want)
	}
}

func TestReplayFileRefusesAnAnchorForAnotherFile(t *testing.T) {
	recording := dvr.Recording{
		Source:           "fixture",
		SourceMediaEpoch: 2,
		BufferInstance:   "buf-1",
		StartBufferTime:  5 * time.Second,
		EndBufferTime:    9 * time.Second,
	}
	anchor := dvr.Anchor{
		Source:         "fixture",
		Epoch:          2,
		BufferInstance: "buf-1",
		BufferStart:    5 * time.Second,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: 5 * time.Second, End: 9 * time.Second},
		MeasuredAt:     time.Now(),
	}
	file, err := replayFileFor(recording, anchor)
	if err != nil {
		t.Fatalf("binding the anchor to its own file: %v", err)
	}
	if file.epoch != 2 || file.instance != "buf-1" || file.clock.Player(100*time.Second) != 0 {
		t.Fatalf("the contract lost what it was given: %+v", file)
	}

	cases := []struct {
		name      string
		recording dvr.Recording
		anchor    dvr.Anchor
		want      string
	}{
		{"another source", recording, func() dvr.Anchor { a := anchor; a.Source = "other"; return a }(), "names other"},
		{"another buffer", recording, func() dvr.Anchor { a := anchor; a.BufferInstance = "buf-2"; return a }(), "buf-2"},
		{"another epoch", recording, func() dvr.Anchor { a := anchor; a.Epoch = 3; return a }(), "epoch 3"},
		{"another cut point", recording, func() dvr.Anchor { a := anchor; a.BufferStart = 6 * time.Second; return a }(), "starts at buffer"},
		{"never measured", recording, func() dvr.Anchor { a := anchor; a.MeasuredAt = time.Time{}; return a }(), "was not measured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := replayFileFor(tc.recording, tc.anchor); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestBufferAnchorWidensOnlyForward(t *testing.T) {
	buffer, _ := fixtureBuffer(t)
	stubProbe(t, 100*time.Second, nil)
	runner := &dvrRunner{buffer: buffer, source: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := runner.waitAnchor(ctx); err != nil {
		t.Fatalf("waiting for the measurement: %v", err)
	}

	// A window inside the measured range is explained as it stands: the interval
	// already covers the media the buffer holds.
	got, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 2 * time.Second})
	if err != nil {
		t.Fatalf("a window at the measured segment was refused: %v", err)
	}
	if got.Interval.Start != 0 || got.Interval.End != 2*time.Second {
		t.Fatalf("the returned interval is %s..%s, want the stored 0s..2s", got.Interval.Start, got.Interval.End)
	}

	// A window that starts before it cannot be blamed on the measurement.
	if _, err := runner.bufferAnchor(dvr.Range{Start: -time.Second, End: 2 * time.Second}); err == nil ||
		!strings.Contains(err.Error(), "before the measured segment") {
		t.Fatalf("a window before the measured segment reports %v, want a refusal", err)
	}

	// A window that reaches past what the buffer stored has no media behind it,
	// even in the same epoch, so it is refused rather than extended.
	if _, err := runner.bufferAnchor(dvr.Range{Start: 0, End: 12 * time.Second}); err == nil ||
		!strings.Contains(err.Error(), "continuous range") {
		t.Fatalf("a window past the stored media reports %v, want a refusal", err)
	}
}

func TestMediaClockKeepsAPtsShiftOutOfThePlayerClock(t *testing.T) {
	// The two files hold the same frames; only their container timestamps differ,
	// which is what the d-3.0 fixture did. The same content therefore has to land
	// on the same player position in both, and the shift must stay in the
	// conversion instead of turning into a track offset.
	first := dvr.Anchor{Source: "a", MeasuredAt: time.Now(), MediaStart: 1466667 * time.Microsecond}
	second := dvr.Anchor{Source: "b", MeasuredAt: time.Now(), MediaStart: 5466667 * time.Microsecond}
	content := 6 * time.Second

	if got, want := clockFor(first).Player(first.MediaStart+content), content; got != want {
		t.Fatalf("the unshifted file shows content %s at player %s, want %s", content, got, want)
	}
	if got, want := clockFor(second).Player(second.MediaStart+content), content; got != want {
		t.Fatalf("the shifted file shows content %s at player %s, want %s", content, got, want)
	}
	shift := second.MediaStart - first.MediaStart
	if shift != 4*time.Second {
		t.Fatalf("the fixture's shift is %s, want 4s", shift)
	}
	if got := clockFor(second).Player(second.MediaStart+content) - clockFor(first).Player(first.MediaStart+content); got != 0 {
		t.Fatalf("the same content sits %s apart on the two player clocks, want 0", got)
	}
	if got, want := clockFor(first).Container(content), first.MediaStart+content; got != want {
		t.Fatalf("container time for player %s is %s, want %s", content, got, want)
	}
	// The buffer window is the clock a player is handed: both sources buffered
	// 0s..30s, and a player opening a file cut at the buffer start reports 0s
	// there, so the same content lands on the same positions even though the
	// container origins differ by the fixture's shift.
	window := playerWindow(second, dvr.Range{Start: 0, End: 30 * time.Second})
	if window.Start != 0 || window.End != 30*time.Second {
		t.Fatalf("the converted window is %s..%s, want 0s..30s", window.Start, window.End)
	}
	shifted := playerWindow(second, dvr.Range{Start: second.BufferStart + 5*time.Second, End: second.BufferStart + 15*time.Second})
	if shifted.Start != 5*time.Second || shifted.End != 15*time.Second {
		t.Fatalf("a window cut later converts to %s..%s, want 5s..15s", shifted.Start, shifted.End)
	}
}

// replayFixture builds an item showing a recording that starts at buffer 0 and
// carries the container timestamp media at its first frame.
func replayFixture(track core.TrackID, instance string, media time.Duration) *item {
	source := core.SourceID(instance)
	return &item{
		uri:   "http://example.invalid/" + instance + ".m3u8",
		video: track,
		replay: &dvr.Recording{
			Source:           source,
			SourceMediaEpoch: 1,
			BufferInstance:   instance,
			StartBufferTime:  0,
			EndBufferTime:    30 * time.Second,
		},
		replayAnchor: &dvr.Anchor{
			Source:         source,
			Epoch:          1,
			BufferInstance: instance,
			BufferStart:    0,
			MediaStart:     media,
			Interval:       dvr.Range{Start: 0, End: 30 * time.Second},
			MeasuredAt:     time.Now(),
		},
	}
}

// TestOneEventTimeHoldsAcrossContainerStarts is the invariant the replay clock
// exists for: two files carrying the same frames, but a different mux delay in
// their container timestamps, must give the same event time for the same
// content. Converting through the container instead put the fixture's four
// seconds straight into the event clock.
func TestOneEventTimeHoldsAcrossContainerStarts(t *testing.T) {
	first := replayFixture("track-1", "buf-a", 1466667*time.Microsecond)
	second := replayFixture("track-2", "buf-b", 5466667*time.Microsecond)
	a := &appState{sync: newSyncState()}
	// The content is the same, so both tracks are confirmed at offset zero; only
	// the containers differ.
	for _, track := range []core.TrackID{"track-1", "track-2"} {
		if err := a.sync.timeline.SetShift(track, 0); err != nil {
			t.Fatalf("confirming %s: %v", track, err)
		}
	}

	const content = 6 * time.Second
	firstEvent, err := a.replayEventTime(first, content)
	if err != nil {
		t.Fatalf("converting the first file: %v", err)
	}
	secondEvent, err := a.replayEventTime(second, content)
	if err != nil {
		t.Fatalf("converting the second file: %v", err)
	}
	if firstEvent != secondEvent {
		t.Fatalf("content %s is event %s in one file and %s in the other, %s apart",
			content, firstEvent, secondEvent, secondEvent-firstEvent)
	}

	// And back: the one event time has to name the same place in both files.
	for _, tc := range []struct {
		name string
		it   *item
	}{{"first", first}, {"second", second}} {
		position, err := a.replayPosition(tc.it, firstEvent)
		if err != nil {
			t.Fatalf("%s file: going back from event %s: %v", tc.name, firstEvent, err)
		}
		if position != content {
			t.Fatalf("the %s file put event %s at %s, want %s", tc.name, firstEvent, position, content)
		}
	}
}

// TestContentTimeCountsFromTheBufferStart covers a buffer that joined a stream
// mid-flight: its clock starts where its first segment was, so a file cut from it
// later carries that start, and a position inside the file is measured from it.
func TestContentTimeCountsFromTheBufferStart(t *testing.T) {
	it := replayFixture("track-1", "buf-a", 120*time.Second)
	it.replay.StartBufferTime = 20 * time.Second
	it.replay.EndBufferTime = 40 * time.Second
	it.replayAnchor.BufferStart = 20 * time.Second
	it.replayAnchor.Interval = dvr.Range{Start: 20 * time.Second, End: 40 * time.Second}
	a := &appState{sync: newSyncState()}
	if err := a.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatalf("confirming the track: %v", err)
	}

	const position = 6 * time.Second
	event, err := a.replayEventTime(it, position)
	if err != nil {
		t.Fatalf("converting the position: %v", err)
	}
	// The buffer joined the stream at 20s of its own clock, so 6s into the file
	// is content 26s, not 6s.
	if want := 26 * time.Second; event != want {
		t.Fatalf("a recording cut %s into its buffer put position %s at event %s, want %s",
			20*time.Second, position, event, want)
	}
	back, err := a.replayPosition(it, event)
	if err != nil {
		t.Fatalf("going back from the event: %v", err)
	}
	if back != position {
		t.Fatalf("event %s went back to %s, want %s", event, back, position)
	}
	if _, err := a.replayPosition(it, 46*time.Second); err == nil {
		t.Fatal("an event past the recording was mapped anyway")
	}
}

func entryFixture(name string, start, end time.Duration) entrySourceState {
	anchor := dvr.Anchor{
		Source:         core.SourceID(name),
		Epoch:          2,
		BufferInstance: "buf-" + name,
		BufferStart:    start,
		MediaStart:     100*time.Second + start,
		Interval:       dvr.Range{Start: start, End: end, Epoch: 2},
		MeasuredAt:     time.Unix(1_700_000_000, 0),
	}
	return entrySourceState{
		Source:         core.SourceID(name),
		Name:           name,
		Track:          "track-1",
		Generation:     3,
		Instance:       "buf-" + name,
		Epoch:          2,
		Window:         dvr.Range{Start: start, End: end, Epoch: 2},
		Anchor:         anchor,
		Offset:         2 * time.Second,
		OriginInstance: "buf-" + name,
		OriginEpoch:    2,
	}
}

func TestEntryPlansTheFirstWholeSecondBothSourcesCanReach(t *testing.T) {
	first := entryFixture("source-1", 0, 30*time.Second)
	second := entryFixture("source-2", 4*time.Second, 34*time.Second)

	plan, err := planEntry([]entrySourceState{first, second}, 0, false)
	if err != nil {
		t.Fatalf("a shared window was not planned: %v", err)
	}
	// The first window is enterable from -2s, the second from 2s, so the first
	// shared whole second is 2s.
	if plan.Event != 2*time.Second {
		t.Fatalf("the default target is %s, want 2s (the later window's start)", plan.Event)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("the plan has %d steps, want one per source", len(plan.Steps))
	}
	if got, want := plan.Steps[0].Cut, 4*time.Second; got != want {
		t.Fatalf("source-1 would be cut at %s, want %s", got, want)
	}
	if plan.Steps[0].InFile != 0 {
		t.Fatalf("source-1 would start %s into its file, but a recording begins at the cut", plan.Steps[0].InFile)
	}
	if plan.Steps[0].Media != 104*time.Second {
		t.Fatalf("source-1's container media is %s, want 1m44s", plan.Steps[0].Media)
	}
}

func TestEntryRefusesWhenNoInstantIsShared(t *testing.T) {
	early := entryFixture("source-1", 0, 10*time.Second)
	late := entryFixture("source-2", 12*time.Second, 22*time.Second)

	if _, err := planEntry([]entrySourceState{early, late}, 0, false); err == nil {
		t.Fatal("a plan was made for sources whose windows never overlap")
	} else if !strings.Contains(err.Error(), "no shared instant") {
		t.Fatalf("the refusal does not explain the gap: %v", err)
	}
}

func TestEntryNamesASourceWithoutARecordedOrigin(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	source.OriginInstance = ""

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "source-1") || !strings.Contains(err.Error(), "another buffer") {
		t.Fatalf("a missing origin was not refused by name: %v", err)
	}
}

func TestEntryNamesASourceWithoutAMeasuredAnchor(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	source.Anchor = dvr.Anchor{}

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "source-1") || !strings.Contains(err.Error(), "no measured anchor") {
		t.Fatalf("an unmeasured anchor was not refused by name: %v", err)
	}
}

func TestEntryRefusesAnAnchorFromAnotherEpoch(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	source.Anchor.Epoch = 3

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "another buffer or epoch") {
		t.Fatalf("an anchor from another epoch was accepted: %v", err)
	}
}

func TestEntryDoesNotCrossAGapBetweenRanges(t *testing.T) {
	// The buffer holds [0s, 10s) and would hold more after the gap, but the
	// plan's window is the one continuous range the anchor explains.
	source := entryFixture("source-1", 0, 10*time.Second)
	source.Anchor.Interval = dvr.Range{Start: 0, End: 2 * time.Second, Epoch: 2}

	// Entering at 8s would be content 10s: exactly at the end of the range,
	// which belongs to whatever comes next.
	if _, err := planEntry([]entrySourceState{source}, 8*time.Second, true); err == nil {
		t.Fatal("a target at the edge of a range was accepted")
	}
	if _, err := planEntry([]entrySourceState{source}, 7*time.Second, true); err != nil {
		t.Fatalf("a target inside the range was refused: %v", err)
	}
}

func TestEntryKeepsAnExplicitTargetExactly(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)

	plan, err := planEntry([]entrySourceState{source}, 12_300*time.Millisecond, true)
	if err != nil {
		t.Fatalf("an explicit target inside the window was refused: %v", err)
	}
	if plan.Event != 12_300*time.Millisecond {
		t.Fatalf("the explicit target became %s, want 12.3s unchanged", plan.Event)
	}
	if !plan.Explicit {
		t.Fatal("the plan does not record that the target was explicit")
	}
}

func TestEntryRefusesAnExplicitTargetOutsideTheWindow(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)

	_, err := planEntry([]entrySourceState{source}, 140*time.Second, true)
	if err == nil || !strings.Contains(err.Error(), "source-1") || !strings.Contains(err.Error(), "cannot enter replay at") {
		t.Fatalf("a target outside the window was not refused by name: %v", err)
	}
}

func TestEntryRefusesADefaultTargetAtTheEndBoundary(t *testing.T) {
	// The content window is [2.4s, 3s), so the event window is [0.4s, 1s):
	// rounding the start up to a whole second lands on the end.
	source := entryFixture("source-1", 2_400*time.Millisecond, 3_000*time.Millisecond)

	_, err := planEntry([]entrySourceState{source}, 0, false)
	if err == nil || !strings.Contains(err.Error(), "no shared instant") {
		t.Fatalf("a default target at the end boundary was accepted: %v", err)
	}
}

func TestRecheckEntryRefusesAChangedGeneration(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	plan, err := planEntry([]entrySourceState{source}, 0, false)
	if err != nil {
		t.Fatalf("the plan was refused: %v", err)
	}

	moved := source
	moved.Generation++
	err = recheckEntry(plan, []entrySourceState{moved})
	if err == nil || !strings.Contains(err.Error(), "player was replaced") {
		t.Fatalf("a replaced player did not stop the commit: %v", err)
	}
}

func TestRecheckEntryRefusesToRemeasuredClock(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	plan, err := planEntry([]entrySourceState{source}, 0, false)
	if err != nil {
		t.Fatalf("the plan was refused: %v", err)
	}

	remeasured := source
	remeasured.Anchor.MeasuredAt = source.Anchor.MeasuredAt.Add(time.Second)
	err = recheckEntry(plan, []entrySourceState{remeasured})
	if err == nil || !strings.Contains(err.Error(), "measured again") {
		t.Fatalf("a re-measured clock did not stop the commit: %v", err)
	}
}

func TestRecheckEntryRefusesAChangedOffset(t *testing.T) {
	source := entryFixture("source-1", 0, 30*time.Second)
	plan, err := planEntry([]entrySourceState{source}, 0, false)
	if err != nil {
		t.Fatalf("the plan was refused: %v", err)
	}

	same := source
	if err := recheckEntry(plan, []entrySourceState{same}); err != nil {
		t.Fatalf("an unchanged session was refused: %v", err)
	}
	moved := source
	moved.Offset += 10 * time.Millisecond
	err = recheckEntry(plan, []entrySourceState{moved})
	if err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("a changed offset did not stop the commit: %v", err)
	}
}

func TestEntryAccountsAreLabelledAsEntering(t *testing.T) {
	enter := (&seekTxn{event: 5 * time.Second, id: 7, kind: replayKindEnter}).reportFor(replayPreparing, "")
	if line := enter.Line(); !strings.HasPrefix(line, "enter replay 7 at 5s: preparing") {
		t.Fatalf("an entry account reads %q", line)
	}
	seek := (&seekTxn{event: 5 * time.Second, id: 8}).reportFor(replayPreparing, "")
	if line := seek.Line(); !strings.HasPrefix(line, "replay 8 for event 5s: preparing") {
		t.Fatalf("a seek account reads %q", line)
	}
}

func enterReplaySession(t *testing.T) (replaySeekState, *[]*fakeSeekPlayer) {
	t.Helper()
	session, opened := seekTxnFixture(t)
	for _, it := range session.items {
		runner := session.app.runnerFor(it)
		if runner == nil {
			t.Fatalf("%s has no runner to take a clock from", it.source.ID)
		}
		point, err := runner.buffer.CurrentSegment()
		if err != nil {
			t.Fatalf("%s: the buffer has no current segment: %v", it.source.ID, err)
		}
		anchor := *it.replayAnchor
		done := make(chan struct{})
		close(done)
		runner.anchorMu.Lock()
		runner.attempt = &anchorAttempt{point: point, done: done, anchor: &anchor}
		runner.anchorMu.Unlock()

		it.replay = nil
		it.replayAnchor = nil
		it.replayAnchorErr = nil
		it.replayLease = nil
	}
	return session, opened
}

func TestEnterReplayTxnCommitsEverySource(t *testing.T) {
	session, opened := enterReplaySession(t)

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != len(session.items) {
		t.Fatalf("opened %d players, want %d", len(*opened), len(session.items))
	}
	for _, it := range session.items {
		if it.replay == nil {
			t.Fatalf("%s still has no recording after the commit", it.source.ID)
		}
		if it.replayLease == nil {
			t.Fatalf("%s has no lease on the file it plays", it.source.ID)
		}
		if it.replayAnchor == nil {
			t.Fatalf("%s has no measured anchor after the commit", it.source.ID)
		}
		if it.generation != 3 {
			t.Fatalf("%s: generation = %d, want 3", it.source.ID, it.generation)
		}
	}
	line := session.app.notice.Line()
	if !strings.Contains(line, "enter replay") || !strings.Contains(line, "committed") {
		t.Fatalf("the account does not describe an entry that committed: %s", line)
	}
	if !strings.Contains(line, "position settled, frame not confirmed") {
		t.Fatalf("the account claims more than a settled position: %s", line)
	}
}

func TestEnterReplayTxnLeavesTheStageAloneWhenAPlayerCannotOpen(t *testing.T) {
	session, _ := enterReplaySession(t)
	inner := session.app.openSeek
	opens := 0
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opens++
		if opens == 2 {
			return nil, fmt.Errorf("the engine refused the file")
		}
		return inner(recording)
	}

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused before it started: %v", err)
	}
	waitSeekTxn(t, session.app)

	for _, it := range session.items {
		if it.replay != nil || it.replayLease != nil {
			t.Fatalf("%s moved to a replay file even though the entry failed", it.source.ID)
		}
		if it.generation != 2 {
			t.Fatalf("%s: generation = %d, want the unchanged 2", it.source.ID, it.generation)
		}
	}
	line := session.app.notice.Line()
	if !strings.Contains(line, "rolled back") {
		t.Fatalf("the account does not say the entry rolled back: %s", line)
	}
	if !strings.Contains(line, "the engine refused the file") {
		t.Fatalf("the account does not name the failure: %s", line)
	}
}

func TestEnterReplayTxnConfirmsThePlanRatherThanAReplaySeat(t *testing.T) {
	session, _ := enterReplaySession(t)
	// Move the source after settlement but before the commit: the frozen plan
	// no longer describes the stage, so the commit has to refuse. A seek would
	// say the player was replaced while its recording was prepared; an entry
	// has to say the player was replaced after the entry was planned.
	session.app.beforeSeekCommit = func() {
		session.items[0].generation++
	}

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused before it started: %v", err)
	}
	waitSeekTxn(t, session.app)

	line := session.app.notice.Line()
	if !strings.Contains(line, "rolled back") {
		t.Fatalf("the account does not say the entry rolled back: %s", line)
	}
	if !strings.Contains(line, "after the entry was planned") {
		t.Fatalf("the commit did not confirm the entry plan: %s", line)
	}
	if session.items[1].replay != nil {
		t.Fatal("the second source was installed even though the first one moved")
	}
}

func TestEnterReplayTxnRefusesASecondRequest(t *testing.T) {
	session, _ := enterReplaySession(t)
	release := make(chan struct{})
	opened := make(chan struct{}, 4)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened <- struct{}{}
		<-release
		return inner(recording)
	}

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("the first entry did not start: %v", err)
	}
	<-opened
	err := session.app.startEnterReplayTxn(session.items, 0, false)
	if err == nil {
		t.Fatal("a second entry started while the first was still preparing")
	}
	if !strings.Contains(err.Error(), "already being prepared") {
		t.Fatalf("the refusal did not explain itself: %v", err)
	}
	close(release)
	waitSeekTxn(t, session.app)
}

func TestEnterReplayTxnUsesTheSharedWindowStart(t *testing.T) {
	session, _ := enterReplaySession(t)

	if err := session.app.startEnterReplayTxn(session.items, 0, false); err != nil {
		t.Fatalf("entering replay was refused: %v", err)
	}
	waitSeekTxn(t, session.app)

	line := session.app.notice.Line()
	// Both fixtures buffer from content zero, so the first whole second both
	// windows hold is zero; the plan must take that instead of inventing a
	// target further in.
	if got := session.app.notice.Event; got != 0 {
		t.Fatalf("entry target = %s, want the shared window start 0s", got)
	}
	if !strings.Contains(line, "committed") {
		t.Fatalf("the account does not describe an entry that committed: %s", line)
	}
}

func TestTheSeekFieldReadsSeconds(t *testing.T) {
	cases := []struct {
		text string
		want time.Duration
	}{
		{"0", 0},
		{"3.5", 3500 * time.Millisecond},
		{"12", 12 * time.Second},
		{" 12s ", 12 * time.Second},
	}
	for _, c := range cases {
		got, err := parseSeekOffset(c.text)
		if err != nil {
			t.Fatalf("parseSeekOffset(%q): %v", c.text, err)
		}
		if got != c.want {
			t.Fatalf("parseSeekOffset(%q) = %s, want %s", c.text, got, c.want)
		}
	}
	for _, text := range []string{"", "   ", "abc", "NaN", "Inf", "1,5"} {
		_, err := parseSeekOffset(text)
		if err == nil {
			t.Fatalf("parseSeekOffset(%q) was accepted", text)
		}
		if !strings.Contains(err.Error(), "is not a number of seconds") && !strings.Contains(err.Error(), "type how many seconds") {
			t.Fatalf("the refusal for %q should say what is wrong: %v", text, err)
		}
	}
}

// A second request while one transaction is preparing is refused before the
// window or the field is even read: a disabled button is a courtesy, not the
// only guard.
func TestTheEntryRefusesWhileAReplayIsPreparing(t *testing.T) {
	a := &appState{seek: &seekTxn{}, seekText: "0"}
	got := a.requestReplaySeek()
	if !strings.Contains(got, "already being prepared") {
		t.Fatalf("a second request was not refused: %q", got)
	}
}

// A source that is not buffering cannot take part, and the entry says so
// instead of offering a window that only some sources could honour.
func TestTheEntryNamesTheSourceThatCannotTakePart(t *testing.T) {
	a := &appState{items: []*item{{}}}
	got := a.requestReplaySeek()
	if !strings.Contains(got, "is not buffering") {
		t.Fatalf("the entry should name what is missing: %q", got)
	}
}

// TestTheWindowIsOfferedAsACandidate guards the wording of the entry: the
// window it shows is the intersection the buffers can offer, not a range the
// plan has authorised, and the note says a target inside it can still be
// refused for a source whose anchor does not explain it.
func TestTheWindowIsOfferedAsACandidate(t *testing.T) {
	caption := seekWindowCaption(0, 30*time.Second)
	if caption != "Candidate common replay window: 0.0s..30.0s" {
		t.Errorf("the window reads %q, want %q", caption, "Candidate common replay window: 0.0s..30.0s")
	}
	if !strings.Contains(seekAuthorizationNote, "per-source anchor authorization") {
		t.Errorf("the note does not say a target needs per-source authorisation: %q", seekAuthorizationNote)
	}
	if !strings.Contains(seekAuthorizationNote, "may be refused") {
		t.Errorf("the note does not say a target can be refused: %q", seekAuthorizationNote)
	}
	for _, claim := range []string{"the window is authorised", "authorised window", "authorised range"} {
		if strings.Contains(strings.ToLower(seekAuthorizationNote), claim) {
			t.Errorf("the note claims the window as a whole is authorised: %q", seekAuthorizationNote)
		}
	}
}

func waitForReport(t *testing.T, app *appState, phase replayPhase) replayReport {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		report, ok := app.lastReport()
		if ok && report.Phase == phase {
			return report
		}
		time.Sleep(20 * time.Millisecond)
	}
	report, _ := app.lastReport()
	t.Fatalf("no %s report was published; the newest is %s", phase, report.Line())
	return replayReport{}
}

// TestCommittedReportPublishesPositionsButNotAFrame keeps the two meanings
// apart: a committed transaction may publish the positions its players
// reported, and may never publish a confirmed frame.
func TestCommittedReportPublishesPositionsButNotAFrame(t *testing.T) {
	session, _ := seekTxnFixture(t)
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	report := waitForReport(t, session.app, replayCommitted)
	if !report.Published() {
		t.Fatal("a committed report did not publish")
	}
	if report.FrameConfirmed() {
		t.Fatal("a committed report claimed a confirmed frame")
	}
	if report.Event != 7*time.Second {
		t.Fatalf("the report names event %s, want 7s", report.Event)
	}
	if len(report.Sources) != 2 {
		t.Fatalf("the report covers %d sources, want 2", len(report.Sources))
	}
	for _, source := range report.Sources {
		if !source.Settled {
			t.Fatalf("%s has no settled position in a committed report", source.Source)
		}
		if source.Residual != source.Reported-source.Requested {
			t.Fatalf("%s residual %s does not follow requested %s and reported %s",
				source.Source, source.Residual, source.Requested, source.Reported)
		}
	}
	if line := report.Line(); !strings.Contains(line, "position settled, frame not confirmed") {
		t.Fatalf("the committed line hides what it does not know: %s", line)
	}
}

// TestRefusedReportExplainsItself covers a request that never became a
// transaction: it is a refusal with a reason, not a silent no-op.
func TestRefusedReportExplainsItself(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.sync.origins = map[core.TrackID]origin{}
	err := session.app.startSeekTxn(session.items, 7*time.Second)
	if err == nil {
		t.Fatal("a request without a recorded origin was accepted")
	}
	report := waitForReport(t, session.app, replayRefused)
	if report.Published() {
		t.Fatal("a refused request published as a success")
	}
	if !strings.Contains(report.Reason, "origin") {
		t.Fatalf("the refusal does not say why: %q", report.Reason)
	}
}

// TestRolledBackReportKeepsItsMeasurementsButNotSuccess is the case the phase
// exists for: the players were measured, then the commit was refused. The
// numbers stay, the success does not.
func TestRolledBackReportKeepsItsMeasurementsButNotSuccess(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.beforeSeekCommit = func() {
		session.items[1].generation++
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	report := waitForReport(t, session.app, replayRolledBack)
	if report.Published() {
		t.Fatal("a rolled back transaction published as a success")
	}
	measured := 0
	for _, source := range report.Sources {
		if source.Settled {
			measured++
		}
	}
	if measured == 0 {
		t.Fatal("the rolled back report dropped the positions it had measured")
	}
	if line := report.Line(); strings.Contains(line, "position settled, frame not confirmed") {
		t.Fatalf("a rolled back line reads like success: %s", line)
	}
}

// TestReportIsASnapshot makes sure a caller cannot edit the session's account.
func TestReportIsASnapshot(t *testing.T) {
	session, _ := seekTxnFixture(t)
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	waitForReport(t, session.app, replayCommitted)

	first, _ := session.app.lastReport()
	if len(first.Sources) == 0 {
		t.Fatal("the committed report has no sources")
	}
	first.Sources[0].Reported = 99 * time.Second
	second, _ := session.app.lastReport()
	if second.Sources[0].Reported == 99*time.Second {
		t.Fatal("editing a returned report changed the session's account")
	}
}

// TestReportKeepsAZeroPositionAsEvidence covers the case where the player
// really reports zero: a small target inside the tolerance is a measurement,
// and the report must keep it as one instead of reading the zero as "nothing
// was measured".
func TestReportKeepsAZeroPositionAsEvidence(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		return &fakeSeekPlayer{name: recording.FileName(), ready: true}, nil
	}
	if err := session.app.startSeekTxn(session.items, 10*time.Millisecond); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	report := waitForReport(t, session.app, replayCommitted)
	if !report.Published() || report.FrameConfirmed() {
		t.Fatalf("a committed report published=%v frame=%v", report.Published(), report.FrameConfirmed())
	}
	for _, source := range report.Sources {
		if !source.Settled {
			t.Fatalf("%s reported a real zero position and the report dropped it", source.Source)
		}
		if source.Requested != 10*time.Millisecond || source.Reported != 0 || source.Residual != -10*time.Millisecond {
			t.Fatalf("%s requested %s reported %s residual %s, want 10ms/0s/-10ms",
				source.Source, source.Requested, source.Reported, source.Residual)
		}
	}
	if line := report.Line(); !strings.Contains(line, "reported 0s (residual -10ms)") {
		t.Fatalf("the line lost the millisecond evidence: %s", line)
	}
}

// TestLastReportDoesNotRegressToAnOlderAttempt holds the newest attempt even
// when an older transaction publishes after it.
func TestLastReportDoesNotRegressToAnOlderAttempt(t *testing.T) {
	session, _ := seekTxnFixture(t)
	session.app.publishReport(replayReport{ID: 2, Event: 7 * time.Second, Phase: replayRefused, Reason: "outside the buffer"})
	session.app.publishReport(replayReport{ID: 1, Event: 7 * time.Second, Phase: replayCommitted})

	report, ok := session.app.lastReport()
	if !ok {
		t.Fatal("no report was kept")
	}
	if report.ID != 2 || report.Phase != replayRefused {
		t.Fatalf("the account went back to attempt %d (%s), want 2 (refused)", report.ID, report.Phase)
	}
}

func TestTheDockSaysOnlyWhatTheAccountKnows(t *testing.T) {
	app := &appState{}
	app.publishReport(replayReport{ID: 1, Event: time.Second, Phase: replayRefused, Reason: "no origin"})
	lines := strings.Join(app.noticeLines(), "\n")
	if !strings.Contains(lines, "refused") || !strings.Contains(lines, "no origin") {
		t.Fatalf("a refused account does not say why: %q", lines)
	}
	if strings.Contains(lines, "position settled") {
		t.Fatalf("a refused account claims a settled position: %q", lines)
	}

	app.publishReport(replayReport{ID: 2, Event: time.Second, Phase: replayCommitted, Sources: []replayReportSource{
		{Source: "source-1", Requested: 10 * time.Millisecond, Reported: 0, Residual: -10 * time.Millisecond, Settled: true},
	}})
	lines = strings.Join(app.noticeLines(), "\n")
	if !strings.Contains(lines, "reported 0s (residual -10ms)") {
		t.Fatalf("the dock does not keep a real zero position: %q", lines)
	}
	if !strings.Contains(lines, "the frame is not confirmed") {
		t.Fatalf("a committed account does not say the frame is unconfirmed: %q", lines)
	}

	app.publishReport(replayReport{ID: 3, Event: time.Second, Phase: replayRolledBack, Reason: "the player moved", Sources: []replayReportSource{
		{Source: "source-1", Requested: time.Second, Reported: time.Second, Settled: true},
	}})
	lines = strings.Join(app.noticeLines(), "\n")
	if strings.Contains(lines, "position settled") || !strings.Contains(lines, "rolled back") {
		t.Fatalf("a rolled-back account reads as a success: %q", lines)
	}
	if !strings.Contains(lines, "source-1: asked 1s, reported 1s") {
		t.Fatalf("a rolled-back account lost its measurements: %q", lines)
	}
}

type replaySeekState struct {
	app    *appState
	items  []*item
	tracks []core.TrackID
	// adds push another segment into the same buffer a source is reading from,
	// which is how a test moves the state while a snapshot is being taken.
	adds []func(sequence int, discontinuity bool)
}

func replaySeekItem(t *testing.T, app *appState, source core.SourceID, track core.TrackID, media time.Duration) (*item, func(int, bool)) {
	t.Helper()
	buffer, add := fixtureBufferOptions(t, dvr.Options{Source: source})
	add(2, false)
	add(3, false)
	add(4, false)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("cut %s at 0s: %v", source, err)
	}
	state := buffer.State()
	anchor := &dvr.Anchor{
		Source:         source,
		Epoch:          state.Epoch,
		BufferInstance: state.Instance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     media,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	uri := "http://example.invalid/" + string(source) + "/index.m3u8"
	app.runners = append(app.runners, trackedRunner{
		uri:    uri,
		runner: &dvrRunner{source: source, buffer: buffer},
	})
	it := &item{
		uri:          uri,
		source:       core.Source{ID: source},
		video:        track,
		replay:       &recording,
		replayAnchor: anchor,
		generation:   2,
	}
	if err := app.sync.timeline.SetShift(track, 0); err != nil {
		t.Fatalf("place %s: %v", track, err)
	}
	app.sync.place(track, dvr.BasisManual, state.Instance, state.Epoch)
	return it, add
}

// replaySeekSession is the ordinary state: two sources of one event, both
// confirmed at offset zero, each with its own buffer and recording.
func replaySeekSession(t *testing.T) replaySeekState {
	t.Helper()
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	first, firstAdd := replaySeekItem(t, app, "source-1", "track-1", 1466667*time.Microsecond)
	second, secondAdd := replaySeekItem(t, app, "source-2", "track-2", 5466667*time.Microsecond)
	app.items = []*item{first, second}
	return replaySeekState{
		app:    app,
		items:  []*item{first, second},
		tracks: []core.TrackID{"track-1", "track-2"},
		adds:   []func(int, bool){firstAdd, secondAdd},
	}
}

func TestPlanReplaySeekReadsTheSessionState(t *testing.T) {
	session := replaySeekSession(t)
	plan, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if plan.Event() != 6*time.Second {
		t.Fatalf("target = %s, want 6s", plan.Event())
	}
	for _, it := range session.items {
		hop, ok := plan.Hop(it.source.ID)
		if !ok {
			t.Fatalf("the plan has no hop for %s", it.source.ID)
		}
		if hop.Content != 6*time.Second {
			t.Fatalf("%s: content = %s, want 6s", it.source.ID, hop.Content)
		}
		if want := 6*time.Second - it.replay.StartBufferTime; hop.Position != want {
			t.Fatalf("%s: position = %s, want %s", it.source.ID, hop.Position, want)
		}
		if hop.Instance != it.replay.BufferInstance || hop.Generation != 2 {
			t.Fatalf("%s: hop names instance %q generation %d", it.source.ID, hop.Instance, hop.Generation)
		}
	}
	if err := session.app.confirmReplaySeek(plan, session.items); err != nil {
		t.Fatalf("confirming an unchanged session: %v", err)
	}
}

func TestConfirmReplaySeekRefusesAStateThatMoved(t *testing.T) {
	session := replaySeekSession(t)
	plan, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	// The same source now reads from a different buffer: another instance, so the
	// anchor and the recording the plan named cannot describe it any more.
	rebuilt, _ := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
	session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: rebuilt}

	err = session.app.confirmReplaySeek(plan, session.items)
	if err == nil {
		t.Fatal("a rebuilt buffer confirmed a plan made from the old one")
	}
	if !containsAll(err.Error(), "source-1", "buffer") {
		t.Fatalf("the refusal %q does not name the source and its buffer", err)
	}
}

func TestPlanReplaySeekRefusesAnOffsetWithoutOrigin(t *testing.T) {
	session := replaySeekSession(t)
	// The timeline still holds a zero, but nothing remembers placing it.
	delete(session.app.sync.origins, "track-2")

	_, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("an offset of unknown origin authorised a seek")
	}
	if !containsAll(err.Error(), "source-2", "origin") {
		t.Fatalf("the refusal %q does not name the source and the missing origin", err)
	}
}

func TestPlanReplaySeekRefusesATargetOutsideTheRange(t *testing.T) {
	session := replaySeekSession(t)
	// Every fixture buffer holds 8s of media, so an event far past it has no range.
	_, err := session.app.planReplaySeek(10*time.Minute, session.items)
	if err == nil {
		t.Fatal("a target outside the buffer was accepted")
	}
}

func TestPlanningAReplaySeekTouchesNothing(t *testing.T) {
	session := replaySeekSession(t)
	before := make([]string, 0, len(session.app.runners))
	for _, tracked := range session.app.runners {
		before = append(before, tracked.runner.buffer.Instance())
	}
	plan, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if err := session.app.confirmReplaySeek(plan, session.items); err != nil {
		t.Fatalf("confirming: %v", err)
	}
	for index, it := range session.items {
		if it.player != nil {
			t.Fatalf("%s opened a player", it.source.ID)
		}
		if it.generation != 2 {
			t.Fatalf("%s generation moved to %d", it.source.ID, it.generation)
		}
		if after := session.app.runners[index].runner.buffer.Instance(); after != before[index] {
			t.Fatalf("%s buffer was replaced during a decision: %s -> %s", it.source.ID, before[index], after)
		}
	}
	if len(session.app.pending) != 0 {
		t.Fatalf("a decision queued %d players for closing", len(session.app.pending))
	}
}

func TestConfirmReplaySeekRefusesARangeThatWasEvicted(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	buffer, add := fixtureBufferOptions(t, dvr.Options{Source: "source-1", Retention: 2 * time.Second})
	add(2, false)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("cut at 0s: %v", err)
	}
	state := buffer.State()
	anchor := &dvr.Anchor{
		Source:         "source-1",
		Epoch:          state.Epoch,
		BufferInstance: state.Instance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     100 * time.Second,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	uri := "http://example.invalid/source-1/index.m3u8"
	app.runners = append(app.runners, trackedRunner{uri: uri, runner: &dvrRunner{source: "source-1", buffer: buffer}})
	it := &item{uri: uri, source: core.Source{ID: "source-1"}, video: "track-1", replay: &recording, replayAnchor: anchor, generation: 1}
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatalf("place the track: %v", err)
	}
	app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)

	plan, err := app.planReplaySeek(1*time.Second, []*item{it})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	// The oldest segment goes away, so the range the decision named is gone.
	if removed := buffer.Evict(nil); len(removed) == 0 {
		t.Fatal("nothing was evicted, so the range did not move")
	}
	if err := app.confirmReplaySeek(plan, []*item{it}); err == nil {
		t.Fatal("a plan whose range was evicted still held")
	}
	if it.player != nil {
		t.Fatal("a refused confirmation opened a player")
	}
}

// containsAll reports whether every part appears in text.
func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for index := 0; index+len(part) <= len(text); index++ {
			if text[index:index+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestPlanReplaySeekRefusesABufferWithoutARecording(t *testing.T) {
	app, err := newAppState(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	buffer, add := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
	add(1, false)
	uri := "http://example.invalid/source-1/index.m3u8"
	app.runners = append(app.runners, trackedRunner{uri: uri, runner: &dvrRunner{source: "source-1", buffer: buffer}})
	it := &item{uri: uri, source: core.Source{ID: "source-1"}, video: "track-1", generation: 1}
	if err := app.sync.timeline.SetShift("track-1", 0); err != nil {
		t.Fatalf("place the track: %v", err)
	}
	state := buffer.State()
	app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)
	if cut := len(buffer.Recordings()); cut != 0 {
		t.Fatalf("the fixture already cut %d recording(s)", cut)
	}

	// The source is buffering, but nothing has been cut from it yet. No plan may
	// be made by inventing an id or a path, and no file may be created to fill
	// the contract in.
	if _, err := app.planReplaySeek(1*time.Second, []*item{it}); err == nil {
		t.Fatal("a buffer without a recording produced a plan")
	} else if !containsAll(err.Error(), "no usable recording") {
		t.Fatalf("the refusal %q does not say there is no recording", err)
	}
	if cut := len(buffer.Recordings()); cut != 0 {
		t.Fatalf("refusing cut %d recording(s) to satisfy the contract", cut)
	}
	if it.player != nil {
		t.Fatal("refusing opened a player")
	}
	if len(app.pending) != 0 {
		t.Fatalf("refusing queued %d player(s) for closing", len(app.pending))
	}
}

func TestPlanReplaySeekRefusesASpliceOfTwoMoments(t *testing.T) {
	session := replaySeekSession(t)
	runner := session.app.runners[0].runner
	defer func() { replaySnapshotHook = nil }()
	replaySnapshotHook = func() {
		// The playlist declares a discontinuity while the snapshot is being read,
		// so the buffer the decision is being made from moves to another epoch.
		_ = runner
		session.adds[0](9, true)
	}
	_, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("a state that moved during the read produced a plan")
	}
	if !containsAll(err.Error(), "changed while its state was read") {
		t.Fatalf("the refusal %q does not say the state moved", err)
	}
}

func TestReplaySeekRefusesAnOriginFromAnotherBuffer(t *testing.T) {
	session := replaySeekSession(t)
	if _, err := session.app.planReplaySeek(6*time.Second, session.items); err != nil {
		t.Fatalf("planning on the first buffers: %v", err)
	}

	// The same source is left and joined again: the session now reads a buffer
	// built later, with its own content zero, its own recording and a measured
	// anchor that matches that buffer. The manual placement was made against the
	// old buffer's clock, so it says nothing about where this one starts.
	replacement, add := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
	add(2, false)
	add(3, false)
	add(4, false)
	recording, err := replacement.Record(0)
	if err != nil {
		t.Fatalf("cut the replacement: %v", err)
	}
	state := replacement.State()
	session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: replacement}
	session.items[0].replay = &recording
	session.items[0].replayAnchor = &dvr.Anchor{
		Source:         "source-1",
		Epoch:          state.Epoch,
		BufferInstance: state.Instance,
		BufferStart:    recording.StartBufferTime,
		MediaStart:     30 * time.Second,
		Interval:       dvr.Range{Start: recording.StartBufferTime, End: recording.EndBufferTime},
		MeasuredAt:     time.Now(),
	}
	session.items[0].generation++

	_, err = session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("an offset placed against the old buffer authorised the new one")
	}
	if !containsAll(err.Error(), "source-1", "calibrated against buffer") {
		t.Fatalf("the refusal %q does not say which buffer the offset belongs to", err)
	}

	// Placing the new buffer on the timeline is what makes it usable again: the
	// old number is not re-labelled, a new relation is established.
	session.app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)
	if _, err := session.app.planReplaySeek(6*time.Second, session.items); err != nil {
		t.Fatalf("planning after placing the new buffer: %v", err)
	}
}

func TestReplaySeekRefusesARunnerReplacedDuringSnapshot(t *testing.T) {
	session := replaySeekSession(t)
	defer func() { replaySnapshotHook = nil }()
	replaySnapshotHook = func() {
		// The session swaps the runner under the item while the snapshot is
		// being taken. The buffer the adapter is reading stays perfectly stable,
		// so reading it twice cannot notice; only asking the session again can.
		replacement, _ := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
		session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: replacement}
	}
	_, err := session.app.planReplaySeek(6*time.Second, session.items)
	if err == nil {
		t.Fatal("a runner replaced during the read produced a plan")
	}
	if !containsAll(err.Error(), "source-1", "replaced while its state was read") {
		t.Fatalf("the refusal %q does not say the buffer was replaced", err)
	}
}

func TestConfirmReplaySeekRefusesEveryKindOfDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(session replaySeekState)
	}{
		{"buffer rebuilt", func(session replaySeekState) {
			// A new buffer keeps the source id but moves the instance and the
			// epoch together, which is exactly why a plan names both.
			rebuilt, _ := fixtureBufferOptions(t, dvr.Options{Source: "source-1"})
			session.app.runners[0].runner = &dvrRunner{source: "source-1", buffer: rebuilt}
		}},
		{"epoch moved", func(session replaySeekState) {
			// A discontinuity moves the clock without replacing the buffer.
			session.adds[0](9, true)
		}},
		{"anchor re-measured", func(session replaySeekState) {
			it := session.items[0]
			replaced := *it.replayAnchor
			replaced.MediaStart += 5 * time.Second
			it.replayAnchor = &replaced
		}},
		{"offset changed", func(session replaySeekState) {
			if _, err := session.app.sync.timeline.AdjustShift("track-1", 200*time.Millisecond); err != nil {
				t.Fatalf("moving the offset: %v", err)
			}
			state := session.app.runnerFor(session.items[0]).buffer.State()
			session.app.sync.place("track-1", dvr.BasisManual, state.Instance, state.Epoch)
		}},
		{"basis withdrawn", func(session replaySeekState) {
			delete(session.app.sync.origins, "track-1")
		}},
		{"player replaced", func(session replaySeekState) {
			session.items[0].generation++
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := replaySeekSession(t)
			plan, err := session.app.planReplaySeek(6*time.Second, session.items)
			if err != nil {
				t.Fatalf("planning: %v", err)
			}
			tc.mutate(session)
			if err := session.app.confirmReplaySeek(plan, session.items); err == nil {
				t.Fatalf("%s: the old plan still held", tc.name)
			}
			for _, it := range session.items {
				if it.player != nil {
					t.Fatalf("%s: a refused confirmation opened a player", it.source.ID)
				}
			}
			if len(session.app.pending) != 0 {
				t.Fatalf("%s: a refused confirmation queued a player for closing", tc.name)
			}
		})
	}
}

func TestReplaySeekRefusesAnOriginPlacedOnTheLivePlayer(t *testing.T) {
	session := replaySeekSession(t)
	// A live player runs beside a DVR buffer, but no one has shown that its
	// position is that buffer's content clock. Such a placement records no
	// identity, and a replay may not lean on it.
	session.app.sync.place("track-1", dvr.BasisManual, "", 0)

	_, err := session.app.planReplaySeek(6*time.Second, session.items[:1])
	if err == nil {
		t.Fatal("an offset placed on the live player authorised a replay")
	}
	if !containsAll(err.Error(), "was not reading this buffer's recording") {
		t.Fatalf("the refusal did not say which clock was missing: %v", err)
	}
}

func TestACalibrationCountsOnTheBufferClockOfTheFileItReads(t *testing.T) {
	recording := &dvr.Recording{
		Source:           "source-1",
		BufferInstance:   "buf-one",
		SourceMediaEpoch: 2,
		StartBufferTime:  2 * time.Second,
		EndBufferTime:    12 * time.Second,
	}
	matching := dvr.BufferState{Instance: "buf-one", Epoch: 2}

	content, instance, epoch, ok := calibrationAt(recording, matching, time.Second)
	if !ok || content != 3*time.Second || instance != "buf-one" || epoch != 2 {
		t.Fatalf("a file cut 2s into its buffer puts position 1s at content %s (ok=%v, %s epoch %d)", content, ok, instance, epoch)
	}

	foreign := dvr.BufferState{Instance: "buf-other", Epoch: 2}
	if _, _, _, ok := calibrationAt(recording, foreign, time.Second); ok {
		t.Fatal("a recording of another buffer placed this buffer's content clock")
	}
	moved := dvr.BufferState{Instance: "buf-one", Epoch: 3}
	if _, _, _, ok := calibrationAt(recording, moved, time.Second); ok {
		t.Fatal("a recording from before the epoch moved placed the new clock")
	}
	if _, _, _, ok := calibrationAt(nil, matching, time.Second); ok {
		t.Fatal("no recording placed a buffer clock")
	}
}

func TestARecordingPlayerIsAskedForItsOwnZero(t *testing.T) {
	session := replaySeekSession(t)
	it := session.items[0]
	position, err := session.app.playerPositionFor(it, 6*time.Second)
	if err != nil {
		t.Fatalf("mapping a content position onto the file: %v", err)
	}
	if want := 6*time.Second - it.replay.StartBufferTime; position != want {
		t.Fatalf("content 6s asked for position %s, want %s", position, want)
	}

	live := *it
	live.replay = nil
	position, err = session.app.playerPositionFor(&live, 6*time.Second)
	if err != nil || position != 6*time.Second {
		t.Fatalf("a live player was asked for %s (err %v), want its own 6s", position, err)
	}
}

type fakeSeekPlayer struct {
	pauses, plays, seeks int
	seekStarted          chan struct{}
	seekRelease          chan struct{}
	readyStarted         chan struct{}
	readyRelease         chan struct{}
	readyOnce            sync.Once
	name                 string
	ready                bool
	closed               bool

	// honourSeek decides whether the player reports the position it was asked
	// for. A player that does not move is what a failed seek looks like from
	// here: the transaction must notice and give it back.
	honourSeek bool
	position   time.Duration

	// ignoreSeeks is the number of requests the player drops on the floor, as
	// the engine does when a seek arrives before the player is settled.
	ignoreSeeks int

	// missBy makes an honoured seek land short of the target: the player moved,
	// it just did not arrive where it was asked to go.
	missBy time.Duration
}

func (p *fakeSeekPlayer) Ready() bool {
	if p.readyStarted != nil {
		p.readyOnce.Do(func() { close(p.readyStarted) })
		<-p.readyRelease
	}
	return p.ready
}
func (p *fakeSeekPlayer) Playing() bool   { return false }
func (p *fakeSeekPlayer) Buffering() bool { return false }

func (p *fakeSeekPlayer) Position() time.Duration { return p.position }
func (p *fakeSeekPlayer) Duration() time.Duration { return 0 }
func (p *fakeSeekPlayer) Volume() float64         { return 0 }
func (p *fakeSeekPlayer) Muted() bool             { return true }
func (p *fakeSeekPlayer) Err() error              { return nil }
func (p *fakeSeekPlayer) Play()                   { p.plays++ }
func (p *fakeSeekPlayer) Pause()                  { p.pauses++ }
func (p *fakeSeekPlayer) Seek(target time.Duration) {
	p.seeks++
	if p.seekStarted != nil {
		close(p.seekStarted)
		<-p.seekRelease
	}
	if p.ignoreSeeks > 0 {
		p.ignoreSeeks--
		return
	}
	if p.honourSeek {
		p.position = target - p.missBy
	}
}
func (p *fakeSeekPlayer) SetVolume(float64) *ui.VideoPlayer { return nil }
func (p *fakeSeekPlayer) SetMuted(bool) *ui.VideoPlayer     { return nil }
func (p *fakeSeekPlayer) Close()                            { p.closed = true }

// seekTxnFixture is the two-source session the transaction tests run against,
// with the player and thread seams pointed at test doubles.
func seekTxnFixture(t *testing.T) (replaySeekState, *[]*fakeSeekPlayer) {
	t.Helper()
	session := replaySeekSession(t)
	prevProbe := probeFirstMediaTime
	probeFirstMediaTime = func(context.Context, string) (time.Duration, error) {
		return 100 * time.Second, nil
	}
	t.Cleanup(func() { probeFirstMediaTime = prevProbe })

	opened := &[]*fakeSeekPlayer{}
	session.app.seekMain = func(fn func()) { fn() }
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true, honourSeek: true}
		*opened = append(*opened, player)
		return player, nil
	}
	session.app.seekStarted = func(player desktopPlayer) bool { return player.Ready() }
	return session, opened
}

// waitSeekTxn waits for the transaction to be over, so the test reads a settled
// session rather than a race.
func waitSeekTxn(t *testing.T, app *appState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		app.seekMu.Lock()
		running := app.seek
		app.seekMu.Unlock()
		if running == nil {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the replay transaction did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSeekTxnCutsAndCommitsEverySource(t *testing.T) {
	session, opened := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != 2 {
		t.Fatalf("opened %d players, want 2", len(*opened))
	}
	for index, it := range session.items {
		if it.replay == before[index] {
			t.Fatalf("%s still shows the recording it had", it.source.ID)
		}
		if it.replay.StartBufferTime != 6*time.Second {
			t.Fatalf("%s: the new recording starts at %s, want 6s", it.source.ID, it.replay.StartBufferTime)
		}
		if it.replayAnchor == nil {
			t.Fatalf("%s: the new recording has no anchor", it.source.ID)
		}
		if it.player == nil {
			t.Fatalf("%s: the new player was not committed", it.source.ID)
		}
		if it.generation != 3 {
			t.Fatalf("%s: generation = %d, want 3", it.source.ID, it.generation)
		}
	}
	open, given := session.app.leaseStats()
	if open != 2 || given != 2 {
		t.Fatalf("leases open=%d given=%d, want 2 and 2", open, given)
	}
}

func TestSeekTxnRollsBackWhenAPlayerCannotOpen(t *testing.T) {
	session, _ := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
	}
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		return nil, fmt.Errorf("this build cannot open a file")
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for index, it := range session.items {
		if it.replay != before[index] {
			t.Fatalf("%s: the failed transaction replaced its recording", it.source.ID)
		}
		if it.player != nil || it.generation != 2 {
			t.Fatalf("%s: player=%v generation=%d after a rollback", it.source.ID, it.player, it.generation)
		}
	}
	if open, given := session.app.leaseStats(); open != 0 || given != 1 {
		t.Fatalf("leases open=%d given=%d after a rollback, want 0 and 1", open, given)
	}
}

func TestSeekTxnRollsBackWhenTheStateMovesBeforeTheCommit(t *testing.T) {
	session, opened := seekTxnFixture(t)
	session.app.beforeSeekCommit = func() {
		// A discontinuity arrives while the recordings are waiting for their
		// pictures: the buffer's clock moves, so the plan no longer describes it.
		session.adds[0](9, true)
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for _, it := range session.items {
		if it.player != nil || it.generation != 2 {
			t.Fatalf("%s: a moved buffer still committed (player=%v generation=%d)", it.source.ID, it.player, it.generation)
		}
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open by a rolled back transaction", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a rollback, want 0", open)
	}
}

func TestSeekTxnRollsBackWhenAPlayerNeverShows(t *testing.T) {
	session, opened := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 150 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })
	session.app.seekStarted = func(desktopPlayer) bool { return false }
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != 2 {
		t.Fatalf("opened %d players, want 2", len(*opened))
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open when the picture never came", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a failed handover, want 0", open)
	}
}

func TestSeekTxnSettlesAPositionInsideTheFile(t *testing.T) {
	session, opened := seekTxnFixture(t)
	// A target that is not on a segment boundary: the file is cut at 6s, so
	// 7s sits one second into it. The transaction must ask the player for that
	// offset, not for the start of the file.
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	if len(*opened) != 2 {
		t.Fatalf("opened %d players, want 2", len(*opened))
	}
	for _, player := range *opened {
		if player.position != time.Second {
			t.Fatalf("%s reports %s, want the target at 1s into its file", player.name, player.position)
		}
	}
	for _, it := range session.items {
		if it.replay.StartBufferTime != 6*time.Second {
			t.Fatalf("%s: the file starts at %s, want 6s", it.source.ID, it.replay.StartBufferTime)
		}
	}
}

func TestSeekTxnRollsBackWhenAPlayerNeverSettles(t *testing.T) {
	session, opened := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 300 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		// A player that opens and draws but never moves.
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true}
		*opened = append(*opened, player)
		return player, nil
	}
	before := make([]*dvr.Recording, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for index, it := range session.items {
		if it.replay != before[index] || it.player != nil {
			t.Fatalf("%s: a player that never settled still committed", it.source.ID)
		}
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open after it never settled", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a failed settle, want 0", open)
	}
}

func TestSeekTxnCommitsNothingWhenTheLastSourceMoves(t *testing.T) {
	session, opened := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	players := make([]desktopPlayer, len(session.items))
	for index, it := range session.items {
		before[index] = it.replay
		players[index] = it.player
	}
	session.app.beforeSeekCommit = func() {
		// The second source is replaced after the first one has already been
		// validated. A commit that validated and installed source by source
		// would leave the first one changed; this one must change neither.
		session.items[1].generation++
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	for index, it := range session.items {
		if it.replay != before[index] {
			t.Fatalf("%s: its recording changed although the transaction was refused", it.source.ID)
		}
		if it.player != players[index] {
			t.Fatalf("%s: its player changed although the transaction was refused", it.source.ID)
		}
		// Only the hook's own change is allowed through: the refused commit
		// must not have bumped anything itself.
		want := 2
		if index == 1 {
			want = 3
		}
		if it.generation != want {
			t.Fatalf("%s: generation=%d after a refusal, want %d", it.source.ID, it.generation, want)
		}
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s was left open by a refused commit", player.name)
		}
	}
	if open, _ := session.app.leaseStats(); open != 0 {
		t.Fatalf("leases open=%d after a refused commit, want 0", open)
	}
}

func TestSeekTxnRecordsThePositionThePlayerReported(t *testing.T) {
	session, opened := seekTxnFixture(t)
	// A player that opens and draws but never moves. Asked for 10ms it reports
	// 0s: inside the tolerance, so the commit is allowed, but the transaction
	// must record 0s and the 10ms it is short rather than claiming the target.
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true}
		*opened = append(*opened, player)
		return player, nil
	}
	if err := session.app.startSeekTxn(session.items, 2*time.Second+10*time.Millisecond); err != nil {
		t.Fatalf("starting: %v", err)
	}
	waitSeekTxn(t, session.app)

	landings := session.app.seekLandings()
	if len(landings) != 2 {
		t.Fatalf("recorded %d landings, want 2", len(landings))
	}
	for _, landing := range landings {
		if landing.requested != 10*time.Millisecond {
			t.Fatalf("%s requested %s, want 10ms", landing.source, landing.requested)
		}
		if landing.reported != 0 {
			t.Fatalf("%s reported %s, want the position the player really gave (0s)", landing.source, landing.reported)
		}
		if landing.residual != -10*time.Millisecond {
			t.Fatalf("%s residual %s, want -10ms", landing.source, landing.residual)
		}
	}
	for _, it := range session.items {
		if it.replay == nil || it.replay.StartBufferTime != 2*time.Second {
			t.Fatalf("%s: the file start is %v, want 2s", it.source.ID, it.replay)
		}
	}
}

func TestStartSeekTxnRefusesASecondRequest(t *testing.T) {
	session, _ := seekTxnFixture(t)
	release := make(chan struct{})
	opened := make(chan struct{}, 4)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened <- struct{}{}
		<-release
		return inner(recording)
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the first transaction did not start: %v", err)
	}
	<-opened // the first one is inside its preparation
	err := session.app.startSeekTxn(session.items, 6*time.Second)
	if err == nil {
		t.Fatal("a second transaction started while the first was still preparing")
	}
	if !strings.Contains(err.Error(), "already being prepared") {
		t.Fatalf("the refusal did not explain itself: %v", err)
	}
	close(release)
	waitSeekTxn(t, session.app)
}

// Two requests that arrive together must not both find the transaction field
// empty and both plan; the start decision is serialised.
func TestStartSeekTxnHandlesRequestsThatArriveTogether(t *testing.T) {
	session, _ := seekTxnFixture(t)
	release := make(chan struct{})
	opened := make(chan struct{}, 8)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		opened <- struct{}{}
		<-release
		return inner(recording)
	}

	var started, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
				refused.Add(1)
				return
			}
			started.Add(1)
		}()
	}
	<-opened
	close(release)
	wg.Wait()
	if started.Load() != 1 {
		t.Fatalf("%d transactions started, want exactly one", started.Load())
	}
	if refused.Load() != 3 {
		t.Fatalf("%d requests were refused, want three", refused.Load())
	}
	waitSeekTxn(t, session.app)
}

// A runner replaced between the plan and the commit is a different buffer
// underneath the same item; the commit must refuse and change nothing.
func TestSeekTxnRefusesWhenTheRunnerIsReplacedBeforeTheCommit(t *testing.T) {
	session, opened := seekTxnFixture(t)
	before := make([]*dvr.Recording, len(session.items))
	beforePlayers := make([]desktopPlayer, len(session.items))
	for i, it := range session.items {
		before[i] = it.replay
		beforePlayers[i] = it.player
	}
	// The buffer is created outside the hook: the hook runs on the transaction's
	// goroutine, where t.Fatalf would end that goroutine without failing the test.
	second, err := dvr.New(dvr.Options{Dir: t.TempDir(), Source: session.items[0].source.ID})
	if err != nil {
		t.Fatalf("a second buffer: %v", err)
	}
	session.app.beforeSeekCommit = func() {
		session.app.runners[0].runner = &dvrRunner{source: session.items[0].source.ID, buffer: second}
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	waitSeekTxn(t, session.app)
	for i, it := range session.items {
		if it.replay != before[i] {
			t.Fatalf("source %d: its recording changed although the runner was replaced", i+1)
		}
		if it.player != beforePlayers[i] {
			t.Fatalf("source %d: its player changed although the runner was replaced", i+1)
		}
	}
	// openPlayers counts through the window, which a unit test does not have;
	// the players the transaction opened are all this test needs to look at.
	for _, player := range *opened {
		if !player.closed {
			t.Fatalf("%s is still open after the refusal", player.name)
		}
	}
}

// A transaction that was replaced while it was preparing must not commit, must
// give its files and players back, and must not clear the transaction that took
// the session over.
func TestSeekTxnGivesWayWhenAnotherTakesOver(t *testing.T) {
	session, opened := seekTxnFixture(t)
	release := make(chan struct{})
	openedOnce := make(chan struct{}, 4)
	inner := session.app.openSeek
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		openedOnce <- struct{}{}
		<-release
		return inner(recording)
	}
	before := make([]*dvr.Recording, len(session.items))
	for i, it := range session.items {
		before[i] = it.replay
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the first transaction did not start: %v", err)
	}
	<-openedOnce

	newer := &seekTxn{event: 9 * time.Second}
	session.app.seekMu.Lock()
	session.app.seek = newer
	session.app.seekMu.Unlock()
	close(release)

	deadline := time.Now().Add(10 * time.Second)
	for {
		allClosed := true
		for _, player := range *opened {
			if !player.closed {
				allClosed = false
			}
		}
		if allClosed && len(*opened) > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the replaced transaction kept its players")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, it := range session.items {
		if it.replay != before[i] {
			t.Fatalf("source %d: the replaced transaction committed its recording", i+1)
		}
	}
	session.app.seekMu.Lock()
	holder := session.app.seek
	session.app.seekMu.Unlock()
	if holder != newer {
		t.Fatal("the replaced transaction cleared the one that took over")
	}
}

// A transaction that has lost the session may not install anything, even when
// it is already inside the commit step.
func TestSeekTxnCommitsNothingOnceOwnershipIsGone(t *testing.T) {
	session, opened := seekTxnFixture(t)
	newer := &seekTxn{event: 9 * time.Second}
	session.app.beforeSeekCommit = func() {
		session.app.seekMu.Lock()
		session.app.seek = newer
		session.app.seekMu.Unlock()
	}
	before := make([]*dvr.Recording, len(session.items))
	for i, it := range session.items {
		before[i] = it.replay
	}

	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(*opened) == 0 || !allClosed(*opened) {
		if !time.Now().Before(deadline) {
			t.Fatal("the transaction that lost the session kept its players")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, it := range session.items {
		if it.replay != before[i] || it.generation != 2 {
			t.Fatalf("source %d: a transaction that lost the session still installed itself", i+1)
		}
	}
	session.app.seekMu.Lock()
	holder := session.app.seek
	session.app.seekMu.Unlock()
	if holder != newer {
		t.Fatal("the transaction cleared the one that took over")
	}
}

// Rolling an old transaction back must not clear the transaction that replaced
// it, even when the abort arrives after the replacement.
func TestSeekTxnAbortLeavesANewerTransactionAlone(t *testing.T) {
	session, _ := seekTxnFixture(t)
	old := &seekTxn{event: 6 * time.Second}
	newer := &seekTxn{event: 9 * time.Second}
	session.app.seekMu.Lock()
	session.app.seek = newer
	session.app.seekMu.Unlock()

	session.app.abortSeekTxn(old, "test abort")

	session.app.seekMu.Lock()
	holder := session.app.seek
	session.app.seekMu.Unlock()
	if holder != newer {
		t.Fatal("aborting an old transaction cleared the newer one")
	}
}
func allClosed(players []*fakeSeekPlayer) bool {
	for _, player := range players {
		if !player.closed {
			return false
		}
	}
	return len(players) > 0
}

// settleTxnFor is a transaction with one prepared step: the settle step only
// needs the player it landed, the item it names and the offset it wants.
func settleTxnFor(session replaySeekState, player desktopPlayer, offset time.Duration) *seekTxn {
	return &seekTxn{
		event: 6 * time.Second,
		steps: []*seekTxnStep{{item: session.items[0], player: player, offset: offset}},
	}
}

// TestSettleSeekRetriesAnIgnoredRequest covers the engine behaviour the retry
// exists for: the first request is dropped, the second one takes.
func TestSettleSeekRetriesAnIgnoredRequest(t *testing.T) {
	session, _ := seekTxnFixture(t)
	player := &fakeSeekPlayer{ready: true, honourSeek: true, ignoreSeeks: 1}
	start := time.Now()
	if err := session.app.settleSeekTxn(settleTxnFor(session, player, time.Second)); err != nil {
		t.Fatalf("a dropped request that was re-issued did not settle: %v", err)
	}
	if player.position != time.Second {
		t.Fatalf("the player ended at %s, want the requested 1s", player.position)
	}
	if elapsed := time.Since(start); elapsed < 1200*time.Millisecond {
		t.Fatalf("the request was honoured after %s, so no re-issue happened", elapsed)
	}
}

// TestSettleSeekGivesUpAfterTheBudget keeps the re-issue bounded: a player
// that drops every request is a failed landing, not a longer wait.
func TestSettleSeekGivesUpAfterTheBudget(t *testing.T) {
	session, _ := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 300 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })

	player := &fakeSeekPlayer{ready: true, honourSeek: true, ignoreSeeks: 100}
	err := session.app.settleSeekTxn(settleTxnFor(session, player, time.Second))
	if err == nil || !strings.Contains(err.Error(), "could not settle at") {
		t.Fatalf("a player that never moved returned %v, want an unreportable position", err)
	}
	if !strings.Contains(err.Error(), "nothing was switched") || !strings.Contains(err.Error(), "The target is authorised") {
		t.Fatalf("the refusal does not say the target was authorised and nothing was switched: %v", err)
	}
	if player.position != 0 {
		t.Fatalf("the player moved to %s although every request was dropped", player.position)
	}
}

// TestSettleSeekTellsAMissedTargetFromAnUnreportableOne keeps the two failures
// apart: a player that moved and missed is a settle timeout, while a player that
// never moved at all is a position the engine would not report, which the entry
// shows as an execution refusal rather than a wrong window.
func TestSettleSeekTellsAMissedTargetFromAnUnreportableOne(t *testing.T) {
	session, _ := seekTxnFixture(t)
	previous := handoverWait
	handoverWait = 300 * time.Millisecond
	t.Cleanup(func() { handoverWait = previous })

	player := &fakeSeekPlayer{ready: true, honourSeek: true, missBy: 500 * time.Millisecond}
	err := session.app.settleSeekTxn(settleTxnFor(session, player, time.Second))
	if err == nil || !strings.Contains(err.Error(), "did not settle on the target") {
		t.Fatalf("a player that moved but missed returned %v, want a settle timeout", err)
	}
	if strings.Contains(err.Error(), "could not settle at") {
		t.Fatalf("a player that moved was called unreportable: %v", err)
	}
}

// TestShutdownWaitsForAnOpeningPlayer is the case the interleaving scenario
// did not cover: the session closes while a replacement player is still being
// opened. The player must not outlive the session, and shutdown must not
// return before the work it stopped has given its resources back.
func TestShutdownWaitsForAnOpeningPlayer(t *testing.T) {
	session, opened := seekTxnFixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		entered <- struct{}{}
		<-release
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true, honourSeek: true}
		*opened = append(*opened, player)
		return player, nil
	}
	if err := session.app.startSeekTxn(session.items, 6*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	<-entered

	closed := make(chan struct{})
	go func() {
		if !session.app.shutdownReplayTxn() {
			t.Error("the closing session gave up waiting for the preparation to return")
		}
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("the session closed while a player was still being opened")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not close after the player opened")
	}

	if !allClosed(*opened) {
		t.Fatal("a player opened after the session asked to close was left open")
	}
	if owner := session.app.seekInFlight(); owner != nil {
		t.Fatalf("a transaction is still registered after the session closed: %s", owner.event)
	}
	if pending := session.app.releaseLeases(); len(pending) > 0 {
		t.Fatalf("leases were left open after the session closed: %v", pending)
	}
}

// TestShutdownWaitsPastTheBudgetForAnOpeningPlayer pins the closing rule: the
// budget is when the wait gets reported, not when the buffers may be destroyed.
// A preparation that is still inside an engine call keeps running, and the
// session waits for it to return instead of closing the buffer it is reading.
func TestShutdownWaitsPastTheBudgetForAnOpeningPlayer(t *testing.T) {
	session, opened := seekTxnFixture(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	session.app.openSeek = func(recording dvr.Recording) (desktopPlayer, error) {
		close(entered)
		<-release
		player := &fakeSeekPlayer{name: recording.FileName(), ready: true}
		*opened = append(*opened, player)
		return player, nil
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	<-entered

	closed := make(chan struct{})
	go func() {
		session.app.shutdownReplayTxn()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("the close returned while a player was still opening")
	case <-time.After(seekShutdownWait + 400*time.Millisecond):
	}

	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the close did not return after the opening finished")
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatal("a player opened after the close began was left open")
		}
	}
}

// TestAClosedSessionDoesNotWaitForADrawingThreadItIsBlocking is the deadlock
// case: a window closes while a transaction is preparing, so the drawing thread
// stops the transaction and waits for it, while the transaction is waiting for
// the drawing thread to run its next step. Bounded dispatch breaks the cycle:
// the step gives up, the preparation ends, and the close can finish.
func TestAClosedSessionDoesNotWaitForADrawingThreadItIsBlocking(t *testing.T) {
	session, opened := seekTxnFixture(t)
	blocked := make(chan struct{})
	defer close(blocked)
	session.app.seekMain = func(func()) {
		// This is what mygoRunOnMain does: it waits for the drawing thread to
		// run the callback. Here the drawing thread is inside the close that
		// waits for this transaction, so the post itself never returns.
		<-blocked
	}
	if err := session.app.startSeekTxn(session.items, 7*time.Second); err != nil {
		t.Fatalf("the transaction did not start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(*opened) == 0 {
		if !time.Now().Before(deadline) {
			t.Fatal("no replacement player was opened")
		}
		time.Sleep(20 * time.Millisecond)
	}

	start := time.Now()
	if !session.app.shutdownReplayTxn() {
		t.Fatal("the close reported that it could not stop the transaction")
	}
	if elapsed := time.Since(start); elapsed > 3*seekDispatchWait {
		t.Fatalf("the close waited %s for a step the drawing thread could not run", elapsed)
	}
	for _, player := range *opened {
		if !player.closed {
			t.Fatal("a player opened by the stopped transaction stayed open")
		}
	}
	if report, ok := session.app.lastReport(); !ok || report.Phase != replayRolledBack {
		t.Fatalf("the stopped transaction ended as %q, want rolled back", report.Phase)
	}
}

// TestAStoppedTransactionDoesNotTouchAQueuedStep is the first edge: a step is
// posted, the session stops the transaction and closes its players, and only
// then does the posted callback run. It must not pause, seek or play a player
// that has been given back.
func TestAStoppedTransactionDoesNotTouchAQueuedStep(t *testing.T) {
	session, _ := seekTxnFixture(t)
	player := &fakeSeekPlayer{ready: true, honourSeek: true}
	txn := settleTxnFor(session, player, time.Second)
	session.app.seek = txn

	var queued []func()
	session.app.seekMain = func(fn func()) { queued = append(queued, fn) }
	previousWait := handoverWait
	handoverWait = 200 * time.Millisecond
	defer func() { handoverWait = previousWait }()
	err := session.app.settleSeekTxn(txn)
	if err == nil || !strings.Contains(err.Error(), "did not run the seek step") {
		t.Fatalf("a step the session never ran produced %v", err)
	}

	session.app.abortSeekTxn(txn, "the session is closing")
	for _, fn := range queued {
		fn()
	}
	if player.pauses != 0 || player.seeks != 0 || player.plays != 0 {
		t.Fatalf("a queued step touched a stopped transaction's player: paused %d, seeks %d, plays %d",
			player.pauses, player.seeks, player.plays)
	}
	if !player.closed {
		t.Fatal("the stopped transaction's player was left open")
	}
}

// TestAStopWaitsForAStepThatIsUsingItsPlayer is the second edge: the step is
// already inside the player when the session decides to stop. The stop waits
// for the call rather than closing the player under it.
func TestAStopWaitsForAStepThatIsUsingItsPlayer(t *testing.T) {
	session, _ := seekTxnFixture(t)
	player := &fakeSeekPlayer{
		ready:       true,
		honourSeek:  true,
		seekStarted: make(chan struct{}),
		seekRelease: make(chan struct{}),
	}
	txn := settleTxnFor(session, player, time.Second)
	session.app.seek = txn
	previousWait := handoverWait
	handoverWait = 5 * time.Second
	defer func() { handoverWait = previousWait }()

	done := make(chan struct{})
	go func() {
		session.app.settleSeekTxn(txn)
		close(done)
	}()
	<-player.seekStarted

	stopped := make(chan struct{})
	go func() {
		session.app.abortSeekTxn(txn, "the session is closing")
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("the stop returned while a step was still inside the player")
	case <-time.After(200 * time.Millisecond):
	}

	close(player.seekRelease)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not return after the step finished")
	}
	if !player.closed {
		t.Fatal("the stopped transaction's player was left open")
	}
}

// TestAStopWaitsForAPictureCheckThatIsReading is the third edge: the picture
// check asks the player whether it is ready, and a stop must not close the
// player while that question is being asked. The check runs inside the same
// lock a stop takes, so the stop waits for the answer.
func TestAStopWaitsForAPictureCheckThatIsReading(t *testing.T) {
	session, _ := seekTxnFixture(t)
	player := &fakeSeekPlayer{
		ready:        true,
		readyStarted: make(chan struct{}),
		readyRelease: make(chan struct{}),
	}
	txn := settleTxnFor(session, player, time.Second)
	session.app.seek = txn
	// Use the player's own answer: this test is about reading it under the
	// lock, not about the seam that stands in for a picture.
	session.app.seekStarted = nil
	previousWait := handoverWait
	handoverWait = 10 * time.Second
	defer func() { handoverWait = previousWait }()

	checked := make(chan error, 1)
	go func() { checked <- session.app.waitSeekPlayers(txn) }()
	select {
	case err := <-checked:
		t.Fatalf("the picture check returned before reading the player: %v", err)
	case <-player.readyStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the picture check never asked the player whether it was ready")
	}

	stopped := make(chan struct{})
	go func() {
		session.app.abortSeekTxn(txn, "the session is closing")
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("the stop returned while the picture check was reading the player")
	case <-time.After(200 * time.Millisecond):
	}

	close(player.readyRelease)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not return after the check finished")
	}
	if !player.closed {
		t.Fatal("the stopped transaction's player was left open")
	}
	// The answer itself was read while the player was still open, so it may
	// well be a success; what matters is that the stop waited for it and then
	// gave the player back.
	<-checked
}

// TestWaitingForAPictureAsksForFrames covers the repair for a paused stage.
// The replacement only produces its first frame when the window draws, and a
// paused stage stops the window from drawing on its own; the wait therefore
// asks for a frame on every poll and stops asking the moment the picture
// arrives.
func TestWaitingForAPictureAsksForFrames(t *testing.T) {
	session, _ := seekTxnFixture(t)
	var frames atomic.Int64
	session.app.requestFrame = func() { frames.Add(1) }
	player := &fakeSeekPlayer{ready: true, readyStarted: make(chan struct{}), readyRelease: make(chan struct{})}
	txn := settleTxnFor(session, player, time.Second)
	session.app.seek = txn
	session.app.seekStarted = nil

	done := make(chan error, 1)
	go func() { done <- session.app.waitSeekPlayers(txn) }()
	select {
	case <-player.readyStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the picture check never asked the player whether it was ready")
	}
	if frames.Load() == 0 {
		t.Fatal("the picture check asked for no frame while it waited")
	}
	close(player.readyRelease)
	if err := <-done; err != nil {
		t.Fatalf("the picture arrived, yet the check failed: %v", err)
	}
	settled := frames.Load()
	time.Sleep(500 * time.Millisecond)
	if frames.Load() != settled {
		t.Fatalf("the check asked for %d more frames after it returned", frames.Load()-settled)
	}
}

// TestAFailedPictureWaitStopsAskingForFrames is the other end of the same
// lifecycle: when the budget runs out the wait stops, it does not keep the
// window awake for the rest of the session.
func TestAFailedPictureWaitStopsAskingForFrames(t *testing.T) {
	session, _ := seekTxnFixture(t)
	var frames atomic.Int64
	session.app.requestFrame = func() { frames.Add(1) }
	player := &fakeSeekPlayer{ready: false}
	txn := settleTxnFor(session, player, time.Second)
	session.app.seek = txn
	session.app.seekStarted = nil
	previousWait := handoverWait
	handoverWait = 400 * time.Millisecond
	defer func() { handoverWait = previousWait }()

	if err := session.app.waitSeekPlayers(txn); err == nil {
		t.Fatal("the picture never arrived, yet the check passed")
	}
	waited := frames.Load()
	if waited == 0 {
		t.Fatal("the failing wait asked for no frame at all")
	}
	time.Sleep(500 * time.Millisecond)
	if frames.Load() != waited {
		t.Fatalf("the failed wait asked for %d more frames after it returned", frames.Load()-waited)
	}
}

func TestEnteringReplayIsNotWiredIntoTheProduct(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files were found, so the guard checked nothing")
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_scenario.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		// A call is spelled through the receiver; the declaration in
		// replay_enter.go is the function itself and must stay.
		if strings.Contains(string(source), "a.startEnterReplayTxn(") {
			t.Errorf("%s calls startEnterReplayTxn; entering replay from live is not a product capability (P16.4g)", path)
		}
	}
}

// The same closure says there is no default lag: a refused entry must not leave
// a transaction behind. This pins the observable half of that rule — the refusal
// is a sentence, and nothing started.
func TestARefusedSeekStartsNothing(t *testing.T) {
	a := &appState{items: []*item{{}}, seekText: "0"}
	note := a.requestReplaySeek()
	if note == "" {
		t.Fatal("a refused request produced no sentence")
	}
	if !strings.Contains(note, "is not buffering") {
		t.Fatalf("the refusal should name what is missing: %q", note)
	}
	a.seekMu.Lock()
	started := a.seek != nil
	a.seekMu.Unlock()
	if started {
		t.Fatal("a refused request left a transaction behind")
	}
}

// A zero offset is a claim like any other, so nothing may substitute one when
// the evidence is absent: an entry with no buffer, and an entry with no sources,
// must both be refused rather than planned against a default target.
func TestZeroIsNotADefaultTarget(t *testing.T) {
	if _, err := (&appState{}).planReplayEntry(nil, 0, true); err == nil {
		t.Fatal("planning an entry with no sources succeeded")
	}
	if _, err := (&appState{items: []*item{{}}}).planReplayEntry([]*item{{}}, 0, false); err == nil {
		t.Fatal("planning an entry with no buffer succeeded")
	}
}

func TestMediaFromEventMovesByTheOffset(t *testing.T) {
	cases := []struct {
		name   string
		event  time.Duration
		offset time.Duration
		want   time.Duration
	}{
		{"on the event", 10 * time.Second, 0, 10 * time.Second},
		{"content ahead", 10 * time.Second, -2 * time.Second, 8 * time.Second},
		{"content behind", 10 * time.Second, 3 * time.Second, 13 * time.Second},
		{"at the origin", 0, 0, 0},
	}
	for _, test := range cases {
		got, err := mediaFromEvent(test.event, test.offset)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if got != test.want {
			t.Fatalf("%s: %s, want %s", test.name, got, test.want)
		}
	}
}

func TestMediaFromEventRefusesBeforeTheStart(t *testing.T) {
	_, err := mediaFromEvent(time.Second, -2*time.Second)
	if !errors.Is(err, errBeforeStart) {
		t.Fatalf("err = %v, want the before-start refusal", err)
	}
}

func TestMediaFromEventRefusesOverflow(t *testing.T) {
	_, err := mediaFromEvent(time.Duration(1<<62), time.Duration(1<<62))
	if err == nil {
		t.Fatal("an overflowing target must be refused")
	}
}

func TestEventFromMediaIsTheInverse(t *testing.T) {
	media, err := mediaFromEvent(9*time.Second, -2*time.Second)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	event, err := eventFromMedia(media, -2*time.Second)
	if err != nil {
		t.Fatalf("backward: %v", err)
	}
	if event != 9*time.Second {
		t.Fatalf("round trip = %s, want 9s", event)
	}
}

func TestEventFromMediaKeepsNegativeMediaOnTheClock(t *testing.T) {
	// A window that starts before the media origin is still a real window on a
	// stream that began earlier; the conversion reports where it sits rather
	// than hiding the range.
	event, err := eventFromMedia(-time.Second, -3*time.Second)
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if event != 2*time.Second {
		t.Fatalf("event = %s, want 2s", event)
	}
}
