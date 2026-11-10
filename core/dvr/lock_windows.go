//go:build windows

package dvr

import (
	"errors"
	"syscall"
)

// errLocked says another process holds the session lock.
var errLocked = errors.New("the session lock is held by another process")

// Win32 codes syscall does not name. 32 is ERROR_SHARING_VIOLATION and 33 is
// ERROR_LOCK_VIOLATION; both mean somebody else already has the file open.
const (
	errorSharingViolation = syscall.Errno(32)
	errorLockViolation    = syscall.Errno(33)
)

// fileLock is an exclusively held lock file. On Windows it is opened with no
// sharing at all, so the system itself releases it when the process ends,
// however it ends.
type fileLock struct {
	handle syscall.Handle
}

func lockFile(path string) (*fileLock, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing: the handle is the lock
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		if err == errorSharingViolation || err == errorLockViolation {
			return nil, errLocked
		}
		return nil, err
	}
	return &fileLock{handle: handle}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	err := syscall.CloseHandle(l.handle)
	l.handle = 0
	return err
}
