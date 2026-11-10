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
