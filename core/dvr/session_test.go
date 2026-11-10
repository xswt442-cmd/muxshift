package dvr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSessionCreatesItsOwnMarkedDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	session, err := OpenSession(root)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close()

	if _, err := os.Stat(filepath.Join(session.Dir, MarkerName)); err != nil {
		t.Fatalf("the session has no marker: %v", err)
	}
	for _, dir := range []string{session.SegmentsDir(), session.RecordingsDir()} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("session subdirectory %s: %v", dir, err)
		}
	}
	record, err := readMarker(filepath.Join(session.Dir, MarkerName))
	if err != nil {
		t.Fatalf("read the marker: %v", err)
	}
	if record.ID != session.ID || record.PID != os.Getpid() {
		t.Fatalf("marker = %+v, want id %q and pid %d", record, session.ID, os.Getpid())
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(session.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the closed session left its directory behind: %v", err)
	}
}

func TestTwoSessionsDoNotSweepEachOther(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	first, err := OpenSession(root)
	if err != nil {
		t.Fatalf("first session: %v", err)
	}
	defer first.Close()
	second, err := OpenSession(root)
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	defer second.Close()
	if first.Dir == second.Dir {
		t.Fatalf("both sessions share %s", first.Dir)
	}

	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("the sweep removed live sessions: %v", result.Removed)
	}
	if len(result.Live) != 2 {
		t.Fatalf("live sessions = %v, want both", result.Live)
	}
	for _, dir := range []string{first.Dir, second.Dir} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("a live session directory is gone: %v", err)
		}
	}
}

func TestSweepRemovesOnlyASessionWhoseLockIsFree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	live, err := OpenSession(root)
	if err != nil {
		t.Fatalf("live session: %v", err)
	}
	defer live.Close()
	dead, err := OpenSession(root)
	if err != nil {
		t.Fatalf("dead session: %v", err)
	}
	// A process that ends without closing leaves its directory and marker but
	// the system drops its lock; that is exactly what this models.
	if err := dead.lock.release(); err != nil {
		t.Fatalf("drop the dead session's lock: %v", err)
	}
	dead.close = true

	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != dead.Dir {
		t.Fatalf("removed = %v, want only %s", result.Removed, dead.Dir)
	}
	if len(result.Live) != 1 || result.Live[0] != live.Dir {
		t.Fatalf("live = %v, want %s", result.Live, live.Dir)
	}
	if _, err := os.Stat(dead.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale directory is still there: %v", err)
	}
	if _, err := os.Stat(live.Dir); err != nil {
		t.Fatalf("the sweep touched a live session: %v", err)
	}
}

func TestSweepKeepsDirectoriesItCannotConfirm(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("root: %v", err)
	}
	unmarked := filepath.Join(root, sessionPrefix+"aaaa")
	damaged := filepath.Join(root, sessionPrefix+"bbbb")
	mismatched := filepath.Join(root, sessionPrefix+"cccc")
	foreign := filepath.Join(root, "someone-elses-cache")
	for _, dir := range []string{unmarked, damaged, mismatched, foreign} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(damaged, MarkerName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("damage a marker: %v", err)
	}
	if err := writeMarker(filepath.Join(mismatched, MarkerName), marker{Version: markerVersion, ID: "dddd"}); err != nil {
		t.Fatalf("write a mismatched marker: %v", err)
	}

	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("the sweep removed what it could not confirm: %v", result.Removed)
	}
	if len(result.Kept) != 3 {
		t.Fatalf("kept = %+v, want the unmarked, damaged and mismatched directories", result.Kept)
	}
	for _, dir := range []string{unmarked, damaged, mismatched, foreign} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s is gone: %v", dir, err)
		}
	}
}

func TestSweepWithoutARootIsNotAnError(t *testing.T) {
	result, err := SweepSessions(filepath.Join(t.TempDir(), "nothing-here"))
	if err != nil {
		t.Fatalf("sweep a missing root: %v", err)
	}
	if len(result.Removed)+len(result.Live)+len(result.Kept) != 0 {
		t.Fatalf("a missing root reported %+v", result)
	}
}
