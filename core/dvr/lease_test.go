package dvr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

func TestLeaseKeepsARecordingThroughClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "buffer")
	buffer, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	lease, err := buffer.Lease(recording.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if got := lease.Recording().ID; got != recording.ID {
		t.Fatalf("lease holds %q, want %q", got, recording.ID)
	}

	// Closing the session must not pull the file out from under the player.
	if err := buffer.Close(); err != nil {
		t.Fatalf("close with a lease: %v", err)
	}
	if err := recording.Valid(); err != nil {
		t.Fatalf("the leased recording did not survive the close: %v", err)
	}
	if pending := buffer.PendingCleanup(); len(pending) != 0 {
		t.Fatalf("nothing should be waiting for cleanup: %v", pending)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the directory was removed while a lease held a file: %v", err)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(recording.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file survived the release: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the directory survived the last release: %v", err)
	}
}

func TestCloseKeepsOnlyLeasedRecordings(t *testing.T) {
	buffer, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	first, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	second, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	lease, err := buffer.Lease(first.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := first.Valid(); err != nil {
		t.Fatalf("the leased recording was removed: %v", err)
	}
	if _, err := os.Stat(second.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the unleased recording survived: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(first.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leased recording survived its release: %v", err)
	}
}

func TestLeaseRefusesUnknownAndGoneRecordings(t *testing.T) {
	buffer, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	if _, err := buffer.Lease("rec-nothing"); !errors.Is(err, ErrNoRange) {
		t.Fatalf("unknown recording = %v, want ErrNoRange", err)
	}
	addFixture(t, buffer)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	lease, err := buffer.Lease(recording.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// A closed buffer refuses new consumers outright; the lease it issued earlier
	// was released above, so the file is gone and there is nothing left to hand
	// out either.
	if _, err := buffer.Lease(recording.ID); !errors.Is(err, core.ErrClosed) {
		t.Fatalf("leasing from a closed buffer = %v, want ErrClosed", err)
	}
}

func TestLeaseRefusesARecordingTheBudgetDropped(t *testing.T) {
	probe, err := New(Options{Dir: filepath.Join(t.TempDir(), "probe")})
	if err != nil {
		t.Fatalf("new probe buffer: %v", err)
	}
	addFixture(t, probe)
	sample, err := probe.Record(0)
	if err != nil {
		t.Fatalf("probe record: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close the probe buffer: %v", err)
	}

	buffer, err := New(Options{Dir: filepath.Join(t.TempDir(), "buffer"), RecordingBytes: sample.Size})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	dropped, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	kept, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	if _, err := buffer.Lease(dropped.ID); !errors.Is(err, ErrNoRange) {
		t.Fatalf("the dropped recording was leased again: %v", err)
	}
	if _, err := buffer.Lease(kept.ID); err != nil {
		t.Fatalf("the surviving recording was refused: %v", err)
	}
}

func TestRecordingBudgetDropsOnlyUnleasedSnapshots(t *testing.T) {
	buffer, err := New(Options{Dir: t.TempDir(), RecordingBytes: 1 << 20})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	first, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	lease, err := buffer.Lease(first.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}

	// Fill the budget until a new cut has to make room; the leased snapshot must
	// never be the one dropped.
	for i := 0; i < 12; i++ {
		if _, err := buffer.Record(time.Duration(i%2) * time.Second); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if err := first.Valid(); err != nil {
			t.Fatalf("record %d removed the leased recording: %v", i, err)
		}
	}
	if got := buffer.RecordingBytes(); got > 1<<20 {
		t.Fatalf("the budget is %d bytes over its 1 MiB limit", got-(1<<20))
	}
	if len(buffer.Recordings()) == 0 {
		t.Fatalf("every recording was dropped")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestRecordingBudgetRefusesWhenEverythingIsLeased(t *testing.T) {
	probe, err := New(Options{Dir: filepath.Join(t.TempDir(), "probe")})
	if err != nil {
		t.Fatalf("new probe buffer: %v", err)
	}
	addFixture(t, probe)
	sample, err := probe.Record(0)
	if err != nil {
		t.Fatalf("probe record: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close the probe buffer: %v", err)
	}

	// The first cut exactly fills the budget and a player takes it. A second cut
	// then has nothing it is allowed to delete, so it must be refused rather than
	// pulling the file out from under the player.
	buffer, err := New(Options{Dir: filepath.Join(t.TempDir(), "buffer"), RecordingBytes: sample.Size})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	first, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	lease, err := buffer.Lease(first.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if _, err := buffer.Record(0); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("record beside a full leased budget = %v, want ErrNoSpace", err)
	}
	if got := buffer.RecordingBytes(); got != sample.Size {
		t.Fatalf("the refused cut changed the accounting to %d bytes", got)
	}
	if err := first.Valid(); err != nil {
		t.Fatalf("the leased file was taken by the refused cut: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestRecordingBudgetRefusesOneCutUnderATinyBudget(t *testing.T) {
	buffer, err := New(Options{Dir: t.TempDir(), RecordingBytes: 1})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	// A one-byte budget cannot hold a cut, and there is nothing to drop, so the
	// buffer must refuse rather than exceed the limit.
	if _, err := buffer.Record(0); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("record under a one-byte budget = %v, want ErrNoSpace", err)
	}
	if got := buffer.RecordingBytes(); got != 0 {
		t.Fatalf("a refused cut still took %d bytes", got)
	}
}

func TestANegativeRecordingBudgetIsRefused(t *testing.T) {
	if _, err := New(Options{Dir: t.TempDir(), RecordingBytes: -1}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("a negative recording budget = %v, want ErrInvalid", err)
	}
}

func TestPendingCleanupReportsAFileWindowsStillHolds(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete a file another handle has open")
	}
	dir := filepath.Join(t.TempDir(), "buffer")
	buffer, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	held, err := os.Open(recording.Path)
	if err != nil {
		t.Fatalf("open the recording: %v", err)
	}
	defer held.Close()

	if err := buffer.Close(); err == nil {
		t.Fatalf("closing with an undeletable recording reported success")
	}
	if pending := buffer.PendingCleanup(); len(pending) != 1 || pending[0] != recording.Path {
		t.Fatalf("pending cleanup = %v, want the recording path", pending)
	}
	if _, err := os.Stat(recording.Path); err != nil {
		t.Fatalf("the undeletable file is gone anyway: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the directory was removed with a file still inside: %v", err)
	}
}

// A retry collects the file once the handle is gone, and reports it while the
// handle is still there. It never takes a recording a lease holds.
func TestRetryCleanupCollectsWhatItCanAndReportsWhatItCannot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete a file another handle has open")
	}
	buffer, err := New(Options{Dir: filepath.Join(t.TempDir(), "buffer")})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	held, err := os.Open(recording.Path)
	if err != nil {
		t.Fatalf("open the recording: %v", err)
	}
	if err := buffer.Close(); err == nil {
		t.Fatalf("closing with an undeletable recording reported success")
	}

	if left := buffer.RetryCleanup(); len(left) != 1 || left[0] != recording.Path {
		t.Fatalf("retry while the handle is open = %v, want the recording path", left)
	}
	if _, err := os.Stat(recording.Path); err != nil {
		t.Fatalf("the retry deleted a file that was still held: %v", err)
	}

	if err := held.Close(); err != nil {
		t.Fatalf("release the handle: %v", err)
	}
	if left := buffer.RetryCleanup(); len(left) != 0 {
		t.Fatalf("retry after the handle closed = %v, want nothing left", left)
	}
	if _, err := os.Stat(recording.Path); !os.IsNotExist(err) {
		t.Fatalf("the retry left the file behind: %v", err)
	}
	if pending := buffer.PendingCleanup(); len(pending) != 0 {
		t.Fatalf("pending cleanup = %v, want empty", pending)
	}
	if bytes := buffer.RecordingBytes(); bytes != 0 {
		t.Fatalf("recording bytes = %d, want 0 after the retry", bytes)
	}
	if _, err := os.Stat(filepath.Join(buffer.opts.Dir, RecordingsName)); !os.IsNotExist(err) {
		t.Fatalf("the retry left the recordings directory behind: %v", err)
	}
}

func TestAClosedBufferHandsOutNoNewLease(t *testing.T) {
	buffer, err := New(Options{Dir: filepath.Join(t.TempDir(), "buffer")})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	recording, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	lease, err := buffer.Lease(recording.ID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatalf("close with a lease: %v", err)
	}
	// The recording is still on disk because the lease holds it, but the buffer
	// has stopped owning it: nothing new may start reading it.
	if _, err := buffer.Lease(recording.ID); !errors.Is(err, core.ErrClosed) {
		t.Fatalf("lease after close = %v, want ErrClosed", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("the lease issued before the close no longer works: %v", err)
	}
}

func TestABudgetLoopDoesNotRetryAFileTheSystemWillNotDelete(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete a file another handle has open")
	}
	// Measure one cut first, so the budget below is exactly two of them: the
	// third cut then has to drop one recording to fit.
	probe, err := New(Options{Dir: filepath.Join(t.TempDir(), "probe")})
	if err != nil {
		t.Fatalf("new probe buffer: %v", err)
	}
	addFixture(t, probe)
	sample, err := probe.Record(0)
	if err != nil {
		t.Fatalf("probe record: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close the probe buffer: %v", err)
	}

	// Two recordings fit, the oldest held open by a second handle but leased by
	// nobody: a third cut must skip the file it cannot delete, drop the younger
	// one, and finish instead of looping on the file the system keeps.
	buffer, err := New(Options{Dir: filepath.Join(t.TempDir(), "buffer"), RecordingBytes: 2 * sample.Size})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)
	first, err := buffer.Record(0)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if _, err := buffer.Record(0); err != nil {
		t.Fatalf("second record: %v", err)
	}
	held, err := os.Open(first.Path)
	if err != nil {
		t.Fatalf("open the first recording: %v", err)
	}
	defer held.Close()

	done := make(chan error, 1)
	go func() { _, err := buffer.Record(0); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the third cut = %v, want it to drop the younger recording", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the budget loop is still retrying a file that cannot be deleted")
	}
	if pending := buffer.PendingCleanup(); len(pending) != 1 || pending[0] != first.Path {
		t.Fatalf("pending cleanup = %v, want the file the system holds", pending)
	}
	if err := first.Valid(); err != nil {
		t.Fatalf("the held file was lost anyway: %v", err)
	}
	if got := buffer.RecordingBytes(); got > 2*sample.Size {
		t.Fatalf("the budget went over: %d bytes", got)
	}
}

// addFixture stores the test playlist's two segments in the buffer.
func addFixture(t *testing.T, buffer *Buffer) {
	t.Helper()
	server := vodServer(t)
	info := readPlaylist(t, server.URL+"/vod/index.m3u8")
	for _, segment := range info.Segments {
		if _, err := buffer.Add(context.Background(), server.Client(), segment); err != nil {
			t.Fatalf("add segment %d: %v", segment.Sequence, err)
		}
	}
}
