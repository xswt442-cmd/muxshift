//go:build !windows

package win

import "errors"

// OpenFolder exists so the desktop client keeps building on the other
// platforms its tests run on. Only the Windows shell is implemented.
func OpenFolder(string) error {
	return errors.New("opening a folder is only implemented on Windows")
}
