//go:build windows

package dvr

import (
	"syscall"
	"unsafe"
)

// shortForm returns the 8.3 spelling of a path when the volume has one.
//
// Names are compared through this spelling because a short name and the long
// name it stands for are the same directory entry: comparing the strings would
// call an ordinary session directory a link. A volume with 8.3 names turned off
// answers with the path it was given, which is the spelling the caller used
// anyway.
func shortForm(path string) string {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	getShort := syscall.NewLazyDLL("kernel32.dll").NewProc("GetShortPathNameW")
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	// The call reports its length, not its success: the error syscall hands back
	// is the thread's last-error value and is set even when the call worked, so
	// only a zero length means the volume has no short form for this path.
	n, _, _ := getShort.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) > len(buf) {
		return path
	}
	return syscall.UTF16ToString(buf[:n])
}
