//go:build windows

package filelock

import (
	"errors"
	"syscall"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("the file lock is held by another process")

// Win32 codes syscall does not name. 32 is
// ERROR_SHARING_VIOLATION and 33 is ERROR_LOCK_VIOLATION; the last two mean
// somebody else already has the file open.
const (
	errorSharingViolation = syscall.Errno(32)
	errorLockViolation    = syscall.Errno(33)
)

// Lock is an exclusively held lock file. On Windows it is opened with no
// sharing at all, so the system itself releases it when the process ends,
// however it ends.
type Lock struct {
	handle syscall.Handle
}

// Acquire takes the lock without waiting. With create false the file must exist.
func Acquire(path string, create bool) (*Lock, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	mode := uint32(syscall.OPEN_EXISTING)
	if create {
		mode = syscall.OPEN_ALWAYS
	}
	handle, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing: the handle is the lock
		nil,
		mode,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		if err == errorSharingViolation || err == errorLockViolation {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{handle: handle}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	err := syscall.CloseHandle(l.handle)
	l.handle = 0
	return err
}
