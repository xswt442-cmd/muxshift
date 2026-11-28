package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireExclusiveAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	first, err := Acquire(path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if competing, err := Acquire(path, true); !errors.Is(err, ErrLocked) {
		_ = competing.Close()
		t.Fatalf("competing acquire = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(path, false)
	if err != nil {
		t.Fatalf("acquire after close: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireExistingMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.lock")
	if lock, err := Acquire(path, false); !errors.Is(err, os.ErrNotExist) {
		_ = lock.Close()
		t.Fatalf("acquire missing file = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acquire created the missing lock: %v", err)
	}
}
