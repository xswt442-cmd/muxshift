package dvr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
	if _, err := buffer.Lease(recording.ID); !errors.Is(err, ErrNoRange) {
		t.Fatalf("a removed recording was leased again: %v", err)
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
