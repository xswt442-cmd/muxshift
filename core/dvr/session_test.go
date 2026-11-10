package dvr

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// The session does not own the files inside it: a close that finds one still
// there must report it and stop, not delete a recording a buffer may still hand
// to a player.
func TestCloseDoesNotDeleteFilesABufferStillOwns(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	session, err := OpenSession(root)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	source := filepath.Join(session.SegmentsDir(), "source-1")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatalf("make a source directory: %v", err)
	}
	owned := filepath.Join(source, "seg-00000000.ts")
	if err := os.WriteFile(owned, []byte("still buffered"), 0o600); err != nil {
		t.Fatalf("write a segment: %v", err)
	}

	err = session.Close()
	if err == nil {
		t.Fatalf("close succeeded with a buffer file still in place")
	}
	if !strings.Contains(err.Error(), "still holds") {
		t.Fatalf("close said %q, want it to name what is left", err)
	}
	if _, err := os.Stat(owned); err != nil {
		t.Fatalf("the close removed a file the buffer owns: %v", err)
	}
	record, err := readMarker(filepath.Join(session.Dir, MarkerName))
	if err != nil {
		t.Fatalf("the refused close removed the marker: %v", err)
	}
	if record.State != markerRemoving {
		t.Fatalf("marker state = %q, want %q", record.State, markerRemoving)
	}
	if _, err := os.Stat(filepath.Join(session.Dir, lockName)); err != nil {
		t.Fatalf("the refused close removed the lock file: %v", err)
	}

	if err := os.Remove(owned); err != nil {
		t.Fatalf("let the buffer go: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close after the file was gone: %v", err)
	}
	if _, err := os.Stat(session.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the second close left the directory: %v", err)
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

// A sweep must not create the lock it is about to look for: doing so would turn
// a directory whose state cannot be read into a session it just declared dead.
func TestSweepKeepsADirectoryWithoutALockFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	session, err := OpenSession(root)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer func() { session.close = true }()
	left := filepath.Join(session.SegmentsDir(), "left.ts")
	if err := os.WriteFile(left, []byte("a segment"), 0o600); err != nil {
		t.Fatalf("write a segment: %v", err)
	}
	// A lock file that went missing: the directory still claims to be a session
	// but nothing proves whether that session is over.
	if err := session.lock.release(); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	session.lock = nil
	lockPath := filepath.Join(session.Dir, lockName)
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove the lock file: %v", err)
	}

	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 || len(result.Live) != 0 {
		t.Fatalf("the sweep acted on an unconfirmed directory: %+v", result)
	}
	if len(result.Kept) != 1 || !strings.Contains(result.Kept[0].Reason, "no lock file") {
		t.Fatalf("kept = %+v, want the missing-lock reason", result.Kept)
	}
	if _, err := os.Stat(left); err != nil {
		t.Fatalf("the kept directory lost its contents: %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sweep created a lock file: %v", err)
	}
}

// A close that cannot remove a file must leave the marker and the lock behind,
// so the next boot can identify the directory and finish the job.
func TestCloseLeavesTheSessionIdentifiableWhenContentIsHeld(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete an open file")
	}
	root := filepath.Join(t.TempDir(), "cache")
	session, err := OpenSession(root)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	held := filepath.Join(session.SegmentsDir(), "held.ts")
	if err := os.WriteFile(held, []byte("still open"), 0o600); err != nil {
		t.Fatalf("write a segment: %v", err)
	}
	handle, err := os.Open(held)
	if err != nil {
		t.Fatalf("hold the segment open: %v", err)
	}
	if err := session.Close(); err == nil {
		handle.Close()
		t.Fatalf("close succeeded while a file was held open")
	}
	record, err := readMarker(filepath.Join(session.Dir, MarkerName))
	if err != nil {
		t.Fatalf("the failed close removed the marker: %v", err)
	}
	if record.State != markerRemoving {
		t.Fatalf("marker state = %q, want %q", record.State, markerRemoving)
	}
	if _, err := os.Stat(filepath.Join(session.Dir, lockName)); err != nil {
		t.Fatalf("the failed close removed the lock file: %v", err)
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("release the segment: %v", err)
	}
	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 1 {
		t.Fatalf("the retry removed %+v, want the finished session", result)
	}
	if _, err := os.Stat(session.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the directory survived the retry: %v", err)
	}
}

// A marker another program wrote is not this application's cache, even when it
// sits under the same root with an unlocked lock file.
func TestSweepKeepsAMarkerFromAnotherApplication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	dir := filepath.Join(root, sessionPrefix+"feedfacefeedface")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}
	foreign := marker{Version: markerVersion, App: "some-other-app", ID: "feedfacefeedface"}
	if err := writeMarker(filepath.Join(dir, MarkerName), foreign); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, lockName), nil, 0o600); err != nil {
		t.Fatalf("make the lock file: %v", err)
	}

	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("removed %v, want nothing", result.Removed)
	}
	if len(result.Kept) != 1 || !strings.Contains(result.Kept[0].Reason, "some-other-app") {
		t.Fatalf("kept = %v, want one entry naming the other application", result.Kept)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the foreign directory was removed: %v", err)
	}
}

// A link in the cache root may point anywhere; a sweep must not follow it.
func TestSweepDoesNotFollowALink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere")
	inner, err := OpenSession(outside)
	if err != nil {
		t.Fatalf("open the session being pointed at: %v", err)
	}
	defer inner.Close()

	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("make the root: %v", err)
	}
	link := filepath.Join(root, sessionPrefix+inner.ID)
	if err := os.Symlink(inner.Dir, link); err != nil {
		t.Skipf("this machine cannot create a symlink: %v", err)
	}

	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("removed %v, want nothing", result.Removed)
	}
	if len(result.Kept) != 1 || !strings.Contains(result.Kept[0].Reason, "link") {
		t.Fatalf("kept = %v, want one entry naming the link", result.Kept)
	}
	if _, err := os.Stat(inner.Dir); err != nil {
		t.Fatalf("the sweep followed the link and removed its target: %v", err)
	}
}
