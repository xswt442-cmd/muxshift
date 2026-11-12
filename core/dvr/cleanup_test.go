package dvr

import (
	"os"
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

// TestDropUnleasedReportsAFileItCannotRemove keeps the page honest: when the
// system will not release a file, the pass must not count it as freed.
func TestDropUnleasedReportsAFileItCannotRemove(t *testing.T) {
	buffer, _, _ := recBuffer(t, "source-1")
	recording, err := buffer.Record(buffer.Segments()[0].Start)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// Holding the file open makes Windows refuse to delete it; on systems that
	// allow the delete anyway the result is simply a successful removal, which is
	// why this check asserts the accounting rather than the platform's opinion.
	file, err := os.Open(recording.Path)
	if err != nil {
		t.Fatalf("open the recording: %v", err)
	}
	defer file.Close()

	result := buffer.DropUnleased()
	if len(result.Removed) == 0 && len(result.Kept) == 0 {
		t.Fatal("the pass neither removed nor kept the recording")
	}
	for _, kept := range result.Kept {
		if kept.Reason == "" {
			t.Fatalf("a kept recording came back with no reason: %+v", kept)
		}
	}
	if len(result.Removed) > 0 && result.Freed != recording.Size {
		t.Fatalf("removed and freed %d bytes, want %d", result.Freed, recording.Size)
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
