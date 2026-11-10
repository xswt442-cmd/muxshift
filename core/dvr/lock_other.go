//go:build !windows

package dvr

import (
	"errors"
	"os"
	"syscall"
)

// errLocked says another process holds the session lock.
var errLocked = errors.New("the session lock is held by another process")

// fileLock is an exclusively held lock file. The kernel drops the lock when the
// process ends, however it ends.
type fileLock struct {
	file *os.File
}

func lockFile(path string) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, errLocked
		}
		file.Close()
		return nil, err
	}
	return &fileLock{file: file}, nil
}

// openExistingLock opens a lock file that is already there. A sweep must never
// create one: a missing lock is an unconfirmed state, not a dead session.
func openExistingLock(path string) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoLock
		}
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, err
	}
	return &fileLock{file: file}, nil
}

func (l *fileLock) release() error {
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
