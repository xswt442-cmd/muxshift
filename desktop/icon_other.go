//go:build !windows

package main

import "github.com/egoist/mygo"

// installWindowIcon is a no-op where the desktop experiment does not run: the
// window icon is installed through Win32 calls and there is no equivalent here.
func installWindowIcon(*mygo.Window) {}
