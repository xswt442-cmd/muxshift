package dvr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

const (
	// MarkerName is the file that says a directory belongs to a MuxShift
	// session. A directory without it is never touched by a sweep.
	MarkerName = "session.json"
	// SegmentsName is the subdirectory for the rolling HLS segments.
	SegmentsName = "segments"
	// RecordingsName is the subdirectory for the published recordings.
	RecordingsName = "recordings"
	lockName       = "session.lock"
	// markerVersion lets a later build recognise a marker it cannot read
	// instead of guessing.
	markerVersion = 1
	// markerRemoving is written into the marker before a session starts
	// deleting its files. A directory in that state is one its owner already
	// gave up on, so a half-finished removal can be retried without guessing.
	markerRemoving = "removing"
	// sessionPrefix marks the directories this package creates. Anything else
	// in the cache root belongs to someone else and is left alone.
	sessionPrefix = "session-"
)

// errNoLock says a session directory has no lock file. The sweep cannot tell
// whether that session is live, so it keeps the directory: creating a lock to
// find out would turn an unknown directory into a dead one by accident.
var errNoLock = errors.New("the session has no lock file")

// marker is the record a session leaves next to its cache. The id must match
// the directory name: a directory whose marker describes another session is
// treated as damaged rather than deleted.
type marker struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	// State is empty while the session runs and markerRemoving once it has
	// started to clean up.
	State string `json:"state,omitempty"`
}

// Session is one run's cache root: a private directory holding this run's
// rolling segments and its published recordings. The directory is guarded by an
// exclusively held lock file, so a sweep can tell a live session from the
// leftovers of one that ended badly.
type Session struct {
	// Dir is the session's own directory. Everything this session writes lives
	// under it.
	Dir string
	// ID is the random id in the directory name and the marker.
	ID string

	lock   *fileLock
	record marker
	close  bool
}

// OpenSession creates a new session directory under root and takes its lock.
// root is created when missing. Two sessions may share a root: each gets its
// own directory and neither sweeps the other.
func OpenSession(root string) (*Session, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: session needs a root directory", core.ErrInvalid)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("%w: session root: %v", ErrNoSpace, err)
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, sessionPrefix+id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: session directory: %v", ErrNoSpace, err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, sub := range []string{SegmentsName, RecordingsName} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o700); err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: session directory: %v", ErrNoSpace, err)
		}
	}
	lock, err := lockFile(filepath.Join(dir, lockName))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("%w: session lock: %v", ErrNoSpace, err)
	}
	record := marker{Version: markerVersion, ID: id, PID: os.Getpid(), StartedAt: time.Now().UTC()}
	if err := writeMarker(filepath.Join(dir, MarkerName), record); err != nil {
		lock.release()
		cleanup()
		return nil, err
	}
	return &Session{Dir: dir, ID: id, lock: lock, record: record}, nil
}

// SegmentsDir is where this session's rolling HLS segments belong.
func (s *Session) SegmentsDir() string { return filepath.Join(s.Dir, SegmentsName) }

// RecordingsDir is where this session's published recordings belong.
func (s *Session) RecordingsDir() string { return filepath.Join(s.Dir, RecordingsName) }

// Close removes everything the session owns. The order matters: the marker is
// rewritten first to say the session is going away, then the media is removed,
// and only a directory that is already empty loses its marker and its lock. A
// failure part way through therefore leaves a directory the next sweep can
// still identify and finish, rather than an unnamed orphan.
func (s *Session) Close() error {
	if s == nil || s.close {
		return nil
	}
	s.close = true
	markerPath := filepath.Join(s.Dir, MarkerName)
	going := s.record
	going.State = markerRemoving
	markErr := writeMarker(markerPath, going)
	if err := s.clearContent(); err != nil {
		_ = s.releaseLock() // the lock file stays: it is part of the identity
		return err
	}
	if markErr == nil {
		if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = s.releaseLock()
			return fmt.Errorf("%w: remove session marker: %v", ErrNoSpace, err)
		}
	}
	if err := s.releaseLock(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.Dir, lockName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: remove session lock: %v", ErrNoSpace, err)
	}
	if err := os.Remove(s.Dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: remove session directory: %v", ErrNoSpace, err)
	}
	return nil
}

// clearContent empties the segment and recording directories, then removes the
// directories themselves once they are empty. A failure leaves whatever could
// not be removed, and the marker, for the next sweep to retry.
func (s *Session) clearContent() error {
	for _, dir := range []string{s.SegmentsDir(), s.RecordingsDir()} {
		if err := clearDirContents(dir); err != nil {
			return fmt.Errorf("%w: remove session content in %s: %v", ErrNoSpace, filepath.Base(dir), err)
		}
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: remove session directory %s: %v", ErrNoSpace, filepath.Base(dir), err)
		}
	}
	return nil
}

func (s *Session) releaseLock() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.release()
	s.lock = nil
	if err != nil {
		return fmt.Errorf("%w: release session lock: %v", ErrNoSpace, err)
	}
	return nil
}

// clearDirContents removes everything inside dir and leaves dir itself. A
// missing dir is already clear.
func clearDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("%s: %v", entry.Name(), err)
		}
	}
	return nil
}

// SweepResult reports what a sweep found. Removed directories were confirmed
// stale; Live ones are held by a running session; Kept ones were left alone and
// say why.
type SweepResult struct {
	Removed []string
	Live    []string
	Kept    []KeptDirectory
}

// KeptDirectory is a directory a sweep declined to remove.
type KeptDirectory struct {
	Path   string
	Reason string
}

// SweepSessions removes session directories under root that no running session
// holds a lock on. A directory is only removed when it carries a readable
// marker that names its own path; unmarked or damaged directories are kept and
// reported.
func SweepSessions(root string) (SweepResult, error) {
	var result SweepResult
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("%w: read cache root: %v", ErrNoSpace, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if len(entry.Name()) < len(sessionPrefix) || entry.Name()[:len(sessionPrefix)] != sessionPrefix {
			continue // not ours
		}
		dir := filepath.Join(root, entry.Name())
		reason, live, err := sweepOne(dir)
		if err != nil {
			return result, err
		}
		switch {
		case live:
			result.Live = append(result.Live, dir)
		case reason != "":
			result.Kept = append(result.Kept, KeptDirectory{Path: dir, Reason: reason})
		default:
			result.Removed = append(result.Removed, dir)
		}
	}
	return result, nil
}

// sweepOne inspects one candidate directory. live means a process still holds
// the lock; a non-empty reason means the directory was kept on purpose.
func sweepOne(dir string) (reason string, live bool, err error) {
	markerPath := filepath.Join(dir, MarkerName)
	record, err := readMarker(markerPath)
	if err != nil {
		return err.Error(), false, nil
	}
	if sessionPrefix+record.ID != filepath.Base(dir) {
		return "the marker names another directory", false, nil
	}
	lockPath := filepath.Join(dir, lockName)
	lock, err := openExistingLock(lockPath)
	switch {
	case errors.Is(err, errNoLock):
		return "the session has no lock file, so it cannot be confirmed dead", false, nil
	case errors.Is(err, errLocked):
		return "", true, nil
	case err != nil:
		return "the lock cannot be taken: " + err.Error(), false, nil
	}
	// The lock is free and this process holds it, so nobody else can start the
	// same cleanup while it runs. Content goes before the marker: a removal
	// that fails half way keeps the directory's identity for the next boot.
	for _, sub := range []string{SegmentsName, RecordingsName} {
		subDir := filepath.Join(dir, sub)
		if err := clearDirContents(subDir); err != nil {
			lock.release()
			return "the " + sub + " cannot be removed: " + err.Error(), false, nil
		}
		if err := os.Remove(subDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			lock.release()
			return "the " + sub + " directory cannot be removed: " + err.Error(), false, nil
		}
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		lock.release()
		return "the marker cannot be removed: " + err.Error(), false, nil
	}
	if err := lock.release(); err != nil {
		return "the stale lock cannot be released: " + err.Error(), false, nil
	}
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "the lock file cannot be removed: " + err.Error(), false, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return "the directory cannot be removed: " + err.Error(), false, nil
	}
	return "", false, nil
}

func newSessionID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("%w: session id: %v", core.ErrInvalid, err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func writeMarker(path string, record marker) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: marker: %v", core.ErrInvalid, err)
	}
	temp := path + ".part"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("%w: marker: %v", ErrNoSpace, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("%w: marker: %v", ErrNoSpace, err)
	}
	return nil
}

func readMarker(path string) (marker, error) {
	var record marker
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return record, errors.New("the directory has no marker")
	}
	if err != nil {
		return record, fmt.Errorf("the marker cannot be read: %v", err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("the marker is damaged: %v", err)
	}
	if record.Version != markerVersion {
		return record, fmt.Errorf("the marker version %d is not understood", record.Version)
	}
	if record.ID == "" {
		return record, errors.New("the marker has no session id")
	}
	return record, nil
}
