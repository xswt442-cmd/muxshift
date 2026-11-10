package dvr

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
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
	t.Cleanup(func() { _ = session.releaseLock() })
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

	// The lock is still held by this process, so a sweep must treat the session
	// as alive even though its marker says it is going away: the lock is the
	// only proof of life, and a sweep has no business deleting files that the
	// process that owns them may still retry.
	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 || len(result.Live) != 1 {
		t.Fatalf("a sweep ran on a held session: %+v", result)
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("release the segment: %v", err)
	}
	if err := os.Remove(held); err != nil {
		t.Fatalf("a buffer empties the directory: %v", err)
	}
	// The retry runs in this process, under the lock the first attempt kept.
	if err := session.Close(); err != nil {
		t.Fatalf("the retry after the handle closed: %v", err)
	}
	if _, err := os.Stat(session.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the directory survived the retry: %v", err)
	}
}

// A marker that cannot be rewritten stops the close before anything is deleted:
// a directory that cannot say what it is any more must not lose its files.
func TestCloseStopsWhenTheMarkerCannotBeWritten(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	session, err := OpenSession(root)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	t.Cleanup(func() { _ = session.releaseLock() })
	segment := filepath.Join(session.SegmentsDir(), "keep.ts")
	if err := os.WriteFile(segment, []byte("held by a buffer"), 0o600); err != nil {
		t.Fatalf("write a segment: %v", err)
	}
	markerPath := filepath.Join(session.Dir, MarkerName)
	if err := os.Remove(markerPath); err != nil {
		t.Fatalf("remove the marker: %v", err)
	}
	// A directory in place of the marker makes the rename of the new marker
	// fail on every platform.
	if err := os.MkdirAll(filepath.Join(markerPath, "occupied"), 0o700); err != nil {
		t.Fatalf("block the marker: %v", err)
	}

	if err := session.Close(); err == nil {
		t.Fatalf("close succeeded with an unwritable marker")
	}
	if _, err := os.Stat(segment); err != nil {
		t.Fatalf("the failed close removed a segment: %v", err)
	}
	if _, err := os.Stat(filepath.Join(session.Dir, lockName)); err != nil {
		t.Fatalf("the failed close removed the lock file: %v", err)
	}
	// The lock was kept, so a second attempt reports the same problem instead of
	// refusing outright because the session no longer holds its lock.
	err = session.Close()
	if err == nil {
		t.Fatalf("the retry succeeded with an unwritable marker")
	}
	if errors.Is(err, core.ErrInvalid) {
		t.Fatalf("the retry lost the lock: %v", err)
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

// A state this version does not know is not a reason to delete anything: a
// newer build may have left the directory in the middle of something.
func TestSweepKeepsAMarkerWithAnUnknownState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	dir := sessionPrefix + "0badc0de0badc0de"
	full := filepath.Join(root, dir)
	if err := os.MkdirAll(full, 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}
	record := marker{Version: markerVersion, App: markerApp, ID: "0badc0de0badc0de", State: "moving"}
	if err := writeMarker(filepath.Join(full, MarkerName), record); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(full, lockName), nil, 0o600); err != nil {
		t.Fatalf("make the lock file: %v", err)
	}
	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Kept) != 1 || len(result.Removed) != 0 {
		t.Fatalf("sweep kept %+v, want one kept directory", result)
	}
	if !strings.Contains(result.Kept[0].Reason, "not understood") {
		t.Fatalf("kept reason = %q, want the unknown state", result.Kept[0].Reason)
	}
}

// A session that wrote "removing" and then died before it could finish leaves
// no lock: the kernel released it with the process. The next boot finishes the
// job instead of keeping the leftovers forever.
func TestSweepFinishesACleanupThatStoppedHalfway(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	id := "5eedface5eedface"
	full := filepath.Join(root, sessionPrefix+id)
	if err := os.MkdirAll(filepath.Join(full, SegmentsName, "source-1"), 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}
	record := marker{Version: markerVersion, App: markerApp, ID: id, State: markerRemoving}
	if err := writeMarker(filepath.Join(full, MarkerName), record); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 1 {
		t.Fatalf("sweep left %+v, want the half-cleaned session removed", result)
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the directory survived the sweep: %v", err)
	}
}

// The same missing lock on a marker that never asked to be removed says nothing
// about the process that owns the data, so it stays.
func TestSweepKeepsAnActiveMarkerWithoutItsLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	id := "0a0a0a0a0a0a0a0a"
	full := filepath.Join(root, sessionPrefix+id)
	if err := os.MkdirAll(full, 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}
	record := marker{Version: markerVersion, App: markerApp, ID: id}
	if err := writeMarker(filepath.Join(full, MarkerName), record); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Kept) != 1 || len(result.Removed) != 0 {
		t.Fatalf("sweep %+v, want the directory kept", result)
	}
}

// A junction wears a directory name but leads somewhere else. Following it
// would delete the tree it points at.
func TestSweepDoesNotFollowAJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows feature")
	}
	root := filepath.Join(t.TempDir(), "cache")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(filepath.Join(outside, SegmentsName, "source-1"), 0o700); err != nil {
		t.Fatalf("make the outside directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, SegmentsName, "source-1", "seg-00000000.ts"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write the outside file: %v", err)
	}
	id := "1234abcd1234abcd"
	link := filepath.Join(root, sessionPrefix+id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("make the root: %v", err)
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	record := marker{Version: markerVersion, App: markerApp, ID: id}
	if err := writeMarker(filepath.Join(outside, MarkerName), record); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, lockName), nil, 0o600); err != nil {
		t.Fatalf("make the lock file: %v", err)
	}
	result, err := SweepSessions(root)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("sweep removed %+v, want the junction kept", result.Removed)
	}
	if _, err := os.Stat(filepath.Join(outside, SegmentsName, "source-1", "seg-00000000.ts")); err != nil {
		t.Fatalf("the sweep followed the junction: %v", err)
	}
}
