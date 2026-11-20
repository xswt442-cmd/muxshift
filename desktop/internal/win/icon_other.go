//go:build !windows

package win

import "github.com/egoist/mygo"

// InstallWindowIcon is a no-op where the desktop experiment does not run: the
// window icon is installed through Win32 calls and there is no equivalent here.
func InstallWindowIcon(*mygo.Window) {}
