package dvr

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDropUnleasedKeepsWhatAPlayerHolds is the property the diagnostics page
// leans on: a cleanup pass removes the recordings nobody reads, reports the one a
// player holds with that reason, and accounts for the bytes it freed.
func TestDropUnleasedKeepsWhatAPlayerHolds(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-1")

	held, lease, err := buffer.RecordAndLease(buffer.Segments()[0].Start)
	if err != nil {
		t.Fatalf("record and lease: %v", err)
	}
	loose, err := buffer.Record(buffer.Segments()[1].Start)
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	result := buffer.DropUnleased()
	if len(result.Removed) != 1 || result.Removed[0].ID != loose.ID {
		t.Fatalf("removed = %v, want only %s", idsOf(result.Removed), loose.ID)
	}
	if result.Freed != loose.Size {
		t.Fatalf("freed = %d bytes, want the %d the loose recording took", result.Freed, loose.Size)
	}
	if len(result.Kept) != 1 || result.Kept[0].Recording.ID != held.ID {
		t.Fatalf("kept = %v, want %s", idsOfKept(result.Kept), held.ID)
	}
	if !result.Kept[0].Leased || result.Kept[0].Reason != "a player still holds it" {
		t.Fatalf("kept reason = %q (leased %v), want the player's hold", result.Kept[0].Reason, result.Kept[0].Leased)
	}
	if _, err := os.Stat(loose.Path); !os.IsNotExist(err) {
		t.Fatalf("the removed recording is still on disk: %v", err)
	}
	if _, err := os.Stat(held.Path); err != nil {
		t.Fatalf("the held recording went away: %v", err)
	}

	// Releasing the lease makes the file fair game; a second pass takes it and
	// frees its bytes.
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second := buffer.DropUnleased()
	if len(second.Removed) != 1 || second.Removed[0].ID != held.ID {
		t.Fatalf("second pass removed = %v, want %s", idsOf(second.Removed), held.ID)
	}
	if second.Freed != held.Size {
		t.Fatalf("second pass freed = %d, want %d", second.Freed, held.Size)
	}
	if left := buffer.RecordingBytes(); left != 0 {
		t.Fatalf("recording bytes = %d after everything was dropped, want 0", left)
	}
	if len(buffer.Recordings()) != 0 {
		t.Fatalf("recordings still listed: %v", idsOf(buffer.Recordings()))
	}
}

// blockRemoval puts a directory where the recording's file was, so the operating
// system refuses to delete it on every platform: os.Remove cannot take a
// directory that still holds something. The returned function puts the file back
// so the retry has something real to remove.
func blockRemoval(t *testing.T, path string) func() {
	t.Helper()
	aside := path + ".held-aside"
	if err := os.Rename(path, aside); err != nil {
		t.Fatalf("move the recording aside: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("put a directory where the recording was: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "held"), []byte("x"), 0o644); err != nil {
		t.Fatalf("fill the directory: %v", err)
	}
	return func() {
		if err := os.RemoveAll(path); err != nil {
			t.Fatalf("clear the blocking directory: %v", err)
		}
		if err := os.Rename(aside, path); err != nil {
			t.Fatalf("put the recording back: %v", err)
		}
	}
}

// TestDropUnleasedReportsAFileItCannotRemove keeps the page honest: when the
// system will not release a file, the pass must not count it as freed.
func TestDropUnleasedReportsAFileItCannotRemove(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-1")
	recording, err := buffer.Record(buffer.Segments()[0].Start)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	restore := blockRemoval(t, recording.Path)
	defer restore()

	result := buffer.DropUnleased()
	if len(result.Removed) != 0 {
		t.Fatalf("removed %v although the system held the file", idsOf(result.Removed))
	}
	if len(result.Kept) != 1 || result.Kept[0].Recording.ID != recording.ID {
		t.Fatalf("kept = %v, want %s", idsOfKept(result.Kept), recording.ID)
	}
	if result.Kept[0].Leased {
		t.Fatal("the recording was kept as if a player held it; nothing did")
	}
	if result.Kept[0].Reason == "" {
		t.Fatal("a kept recording came back with no reason")
	}
	if result.Freed != 0 {
		t.Fatalf("freed %d bytes for a file that is still there", result.Freed)
	}
	if got := buffer.RecordingBytes(); got != recording.Size {
		t.Fatalf("recording bytes = %d after a failed removal, want %d", got, recording.Size)
	}
	if _, err := os.Stat(recording.Path); err != nil {
		t.Fatalf("the kept file is gone: %v", err)
	}
	if pending := buffer.PendingCleanup(); len(pending) != 1 || pending[0] != recording.Path {
		t.Fatalf("pending cleanup = %v, want the file that stayed", pending)
	}
}

// TestRetryCleanupTakesWhatItCouldNotBefore covers the other half: once the
// system lets go, a retry removes the file, frees its bytes and empties the
// pending list, so the page's numbers move with the disk.
func TestRetryCleanupTakesWhatItCouldNotBefore(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-1")
	recording, err := buffer.Record(buffer.Segments()[0].Start)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	restore := blockRemoval(t, recording.Path)
	if kept := buffer.DropUnleased(); len(kept.Kept) != 1 {
		t.Fatalf("first pass kept %v, want the file the system held", idsOfKept(kept.Kept))
	}
	if pending := buffer.PendingCleanup(); len(pending) != 1 {
		t.Fatalf("pending cleanup = %v, want the held file", pending)
	}

	restore()
	if left := buffer.RetryCleanup(); len(left) != 0 {
		t.Fatalf("retry still reports %v after the system let go", left)
	}
	if _, err := os.Stat(recording.Path); !os.IsNotExist(err) {
		t.Fatalf("the retried file is still on disk: %v", err)
	}
	if got := buffer.RecordingBytes(); got != 0 {
		t.Fatalf("recording bytes = %d after the retry took the file, want 0", got)
	}
	if pending := buffer.PendingCleanup(); len(pending) != 0 {
		t.Fatalf("pending cleanup still lists %v", pending)
	}
	if recordings := buffer.Recordings(); len(recordings) != 0 {
		t.Fatalf("recordings still listed: %v", idsOf(recordings))
	}
}

func idsOf(recordings []Recording) []string {
	out := make([]string, 0, len(recordings))
	for _, recording := range recordings {
		out = append(out, recording.ID)
	}
	return out
}

func idsOfKept(kept []KeptRecording) []string {
	out := make([]string, 0, len(kept))
	for _, item := range kept {
		out = append(out, item.Recording.ID)
	}
	return out
}
