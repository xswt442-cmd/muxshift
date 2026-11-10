package dvr

import (
	"fmt"
	"os"
	"sync"

	"github.com/xswt442-cmd/muxshift/core"
)

// Lease holds one recording open for a player. While a lease exists, the buffer
// will not delete that recording, no matter how much new media arrives, how far
// the segment cache is trimmed, or when the buffer is closed: the file a player
// was handed keeps the bytes it was given.
//
// Release returns the lease. The last release of a recording whose buffer has
// closed removes the file; a file that cannot be removed then (Windows keeps a
// handle on a file another process still reads) stays listed by
// PendingCleanup instead of being reported as gone.
type Lease struct {
	buffer    *Buffer
	id        string
	recording Recording

	mu       sync.Mutex
	released bool
}

// Recording is the file this lease holds.
func (l *Lease) Recording() Recording {
	return l.recording
}

// Release gives the recording back. It is safe to call twice; the second call
// does nothing and reports nil.
func (l *Lease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	return l.buffer.release(l.id)
}

// Lease opens the recording with this id for a player. A recording that was
// already removed, or that never existed, is refused with ErrNoRange, and a
// closed buffer hands out no new leases: the leases it already issued stay
// usable until they are released, but nothing new starts reading a file the
// buffer has stopped owning.
func (b *Buffer) Lease(id string) (*Lease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("%w: the buffer is closed", core.ErrClosed)
	}
	for i := range b.recordings {
		if b.recordings[i].ID != id {
			continue
		}
		if b.recordings[i].gone {
			return nil, fmt.Errorf("%w: recording %s is gone", ErrNoRange, id)
		}
		b.recordings[i].refs++
		return &Lease{buffer: b, id: id, recording: b.recordings[i].Recording}, nil
	}
	return nil, fmt.Errorf("%w: there is no recording %q", ErrNoRange, id)
}

// release drops one reference and removes the file when the buffer is closed and
// nobody else holds it. The caller holds the lease's own lock, so this takes the
// buffer's.
func (b *Buffer) release(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.recordings {
		if b.recordings[i].ID != id {
			continue
		}
		if b.recordings[i].refs > 0 {
			b.recordings[i].refs--
		}
		if b.recordings[i].refs == 0 && b.closed && !b.recordings[i].gone {
			b.removeRecordingLocked(i)
			// The buffer was closed while this file was leased, so the directory
			// removal it skipped happens here, once nothing the buffer owns is
			// left on disk.
			if b.lastRemoveErr == nil && b.dirOwned && b.emptyLocked() {
				if err := os.RemoveAll(b.opts.Dir); err != nil {
					b.lastRemoveErr = fmt.Errorf("remove the buffer directory: %w", err)
				}
			}
		}
		return b.lastRemoveErr
	}
	return nil
}

// removeRecordingLocked deletes one recording's file and marks it gone. A file
// that cannot be deleted stays listed for PendingCleanup, because a lease may
// still be reading it on Windows. The caller holds the lock.
func (b *Buffer) removeRecordingLocked(index int) {
	rec := &b.recordings[index]
	if rec.gone {
		return
	}
	if err := os.Remove(rec.Path); err != nil && !os.IsNotExist(err) {
		rec.gone = false
		b.lastRemoveErr = fmt.Errorf("recording %s is still open: %w", rec.ID, err)
		if !contains(b.pendingCleanup, rec.Path) {
			b.pendingCleanup = append(b.pendingCleanup, rec.Path)
		}
		return
	}
	rec.gone = true
	b.recordingBytes -= rec.Size
	if rec.Size < 0 {
		b.recordingBytes = 0
	}
	b.lastRemoveErr = nil
}

// Recordings returns the recordings this buffer published, oldest first.
func (b *Buffer) Recordings() []Recording {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Recording, 0, len(b.recordings))
	for _, rec := range b.recordings {
		if rec.gone {
			continue
		}
		out = append(out, rec.Recording)
	}
	return out
}

// RecordingBytes is how much disk the published recordings take.
func (b *Buffer) RecordingBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recordingBytes
}

// PendingCleanup lists files the buffer wanted to remove but could not. They stay
// on disk and are reported instead of being counted as deleted.
func (b *Buffer) PendingCleanup() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.pendingCleanup...)
}

// RetryCleanup makes one more attempt at the files a lease or a Windows handle
// kept the buffer from removing, and returns what is still there. A recording
// that goes away now leaves the pending list and its bytes go back to the budget.
// Retrying is safe between closes: nothing is retried while a lease holds the
// recording, so a file a player reads is never taken from under it.
func (b *Buffer) RetryCleanup() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pendingCleanup) == 0 {
		return nil
	}
	left := b.pendingCleanup[:0]
	for _, path := range b.pendingCleanup {
		index := b.recordingAtLocked(path)
		if index < 0 {
			// The recording is no longer tracked: the file is somebody
			// else's problem now, so the buffer stops claiming it.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				left = append(left, path)
			}
			continue
		}
		if b.recordings[index].refs > 0 {
			left = append(left, path)
			continue
		}
		b.removeRecordingLocked(index)
		if !b.recordings[index].gone {
			left = append(left, path)
		}
	}
	b.pendingCleanup = left
	if len(left) == 0 {
		b.lastRemoveErr = nil
		// Nothing the buffer owns is left, so a directory it created itself can
		// go now even though the close that failed had to keep it.
		if b.dirOwned && b.emptyLocked() {
			_ = os.RemoveAll(b.opts.Dir)
		}
	}
	return append([]string(nil), left...)
}

// recordingAtLocked finds a recording by the file it published. The caller holds
// the lock.
func (b *Buffer) recordingAtLocked(path string) int {
	for i := range b.recordings {
		if b.recordings[i].Path == path {
			return i
		}
	}
	return -1
}

// dropOldestRecordingLocked tries to remove the oldest recording nobody holds,
// to make room under the recording budget. It reports whether it found a
// candidate, not whether the file went away: a file the system refuses to delete
// is listed for cleanup and skipped by later calls, so a budget loop moves on to
// the next candidate instead of retrying the same file forever. The caller holds
// the lock.
func (b *Buffer) dropOldestRecordingLocked() bool {
	for i := range b.recordings {
		rec := &b.recordings[i]
		if rec.gone || rec.refs > 0 {
			continue
		}
		if contains(b.pendingCleanup, rec.Path) {
			continue
		}
		b.removeRecordingLocked(i)
		return true
	}
	return false
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
