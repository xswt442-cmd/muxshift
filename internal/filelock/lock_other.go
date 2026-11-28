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
	var lockErr error
	for {
		lockErr = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(lockErr, syscall.EINTR) {
			break
		}
	}
	if lockErr != nil {
		file.Close()
		if errors.Is(lockErr, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, lockErr
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
