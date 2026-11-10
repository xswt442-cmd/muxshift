package dvr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A session keeps segments and recordings apart; a buffer must follow that
// split instead of dropping a published file next to the rolling ones.
func TestSegmentsAndRecordingsLiveInTheirOwnDirectories(t *testing.T) {
	root := t.TempDir()
	segmentsDir := filepath.Join(root, "segments", "source-1")
	recordingsDir := filepath.Join(root, "recordings", "source-1")
	buffer, err := New(Options{Dir: segmentsDir, RecordingsDir: recordingsDir, Source: "source-1"})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	addFixture(t, buffer)

	recording, err := buffer.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := filepath.Dir(recording.Path); got != recordingsDir {
		t.Fatalf("recording lives in %s, want %s", got, recordingsDir)
	}
	for _, segment := range buffer.Segments() {
		if got := filepath.Dir(segment.Path); got != segmentsDir {
			t.Fatalf("segment lives in %s, want %s", got, segmentsDir)
		}
	}
	if err := buffer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for _, dir := range []string{segmentsDir, recordingsDir} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the buffer left %s behind: %v", dir, err)
		}
	}
}

// Two buffers on one session share recordings/ but each owns only its own
// subtree, so one closing must not take the other's recordings with it.
func TestABufferLeavesTheSharedRecordingsDirectoryToItsOwner(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "recordings")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("make the shared directory: %v", err)
	}
	first, err := New(Options{Dir: filepath.Join(root, "segments", "source-1"), RecordingsDir: filepath.Join(shared, "source-1"), Source: "source-1"})
	if err != nil {
		t.Fatalf("first buffer: %v", err)
	}
	second, err := New(Options{Dir: filepath.Join(root, "segments", "source-2"), RecordingsDir: filepath.Join(shared, "source-2"), Source: "source-2"})
	if err != nil {
		t.Fatalf("second buffer: %v", err)
	}
	addFixture(t, first)
	addFixture(t, second)
	firstRecording, err := first.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	secondRecording, err := second.Record(2 * time.Second)
	if err != nil {
		t.Fatalf("second record: %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close the first buffer: %v", err)
	}
	if _, err := os.Stat(firstRecording.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the closed buffer kept its own recording: %v", err)
	}
	if _, err := os.Stat(secondRecording.Path); err != nil {
		t.Fatalf("closing the first buffer removed the second's recording: %v", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("the shared recordings directory is gone: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close the second buffer: %v", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("the last buffer removed the owner's directory: %v", err)
	}
}
