//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("the file lock is held by another process")

// Lock is an exclusively held lock file. The kernel drops the lock when the
// process ends, however it ends.
type Lock struct {
	file *os.File
}

// Acquire takes the lock without waiting. With create false the file must exist.
func Acquire(path string, create bool) (*Lock, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, ErrLocked
		}
		file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

// openExistingLock opens a lock file that is already there. A sweep must never
// create one: a missing lock is an unconfirmed state, not a dead session.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if cerr := l.file.Close(); err == nil {
		err = cerr
	}
	l.file = nil
	return err
}
