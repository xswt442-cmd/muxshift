//go:build !windows

package win

import "runtime"

// SystemInfo has no native Windows version outside Windows; the caller still
// gets the Go runtime's operating-system name.
func SystemInfo() (string, uint64) {
	return runtime.GOOS, 0
}
