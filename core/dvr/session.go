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
	// sessionPrefix marks the directories this package creates. Anything else
	// in the cache root belongs to someone else and is left alone.
	sessionPrefix = "session-"
)

// marker is the record a session leaves next to its cache. The id must match
// the directory name: a directory whose marker describes another session is
// treated as damaged rather than deleted.
type marker struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
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

	lock  *fileLock
	close bool
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
	return &Session{Dir: dir, ID: id, lock: lock}, nil
}

// SegmentsDir is where this session's rolling HLS segments belong.
func (s *Session) SegmentsDir() string { return filepath.Join(s.Dir, SegmentsName) }

// RecordingsDir is where this session's published recordings belong.
func (s *Session) RecordingsDir() string { return filepath.Join(s.Dir, RecordingsName) }

// Close releases the lock and removes the session's directory. A failure to
// remove it leaves the directory in place, marked, for a later sweep: the next
// run only collects a directory once no process holds its lock.
func (s *Session) Close() error {
	if s == nil || s.close {
		return nil
	}
	s.close = true
	if s.lock != nil {
		if err := s.lock.release(); err != nil {
			return fmt.Errorf("%w: release session lock: %v", ErrNoSpace, err)
		}
	}
	if err := os.RemoveAll(s.Dir); err != nil {
		return fmt.Errorf("%w: remove session directory: %v", ErrNoSpace, err)
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
	record, err := readMarker(filepath.Join(dir, MarkerName))
	if err != nil {
		return err.Error(), false, nil
	}
	if sessionPrefix+record.ID != filepath.Base(dir) {
		return "the marker names another directory", false, nil
	}
	lock, err := lockFile(filepath.Join(dir, lockName))
	if err != nil {
		if errors.Is(err, errLocked) {
			return "", true, nil
		}
		return "the lock cannot be taken: " + err.Error(), false, nil
	}
	// Nothing holds the lock, so the session that owned this directory is gone.
	if err := lock.release(); err != nil {
		return "the stale lock cannot be released: " + err.Error(), false, nil
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
