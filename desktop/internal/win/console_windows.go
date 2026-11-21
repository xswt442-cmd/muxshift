//go:build windows

package win

import (
	"os"
	"syscall"
)

const (
	attachParentProcess = ^uintptr(0)
	stdOutputHandle     = ^uintptr(11)
	stdErrorHandle      = ^uintptr(12)
)

var (
	kernel32Console   = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = kernel32Console.NewProc("AttachConsole")
	procGetStdHandle  = kernel32Console.NewProc("GetStdHandle")
)

// AttachConsole gives a GUI-subsystem build a parent console when it is started
// from a terminal. A start from Explorer has no parent console, so this is a
// no-op there and no console window is created.
func AttachConsole() {
	hOut, _, _ := procGetStdHandle.Call(stdOutputHandle)
	hErr, _, _ := procGetStdHandle.Call(stdErrorHandle)
	if (hOut != 0 && hOut != ^uintptr(0)) || (hErr != 0 && hErr != ^uintptr(0)) {
		// The launcher already supplied standard handles, as a redirected
		// release check does. Replacing them with a console would discard the
		// stream the caller is reading.
		return
	}
	ok, _, _ := procAttachConsole.Call(attachParentProcess)
	if ok == 0 {
		return
	}
	for _, item := range []struct {
		handle uintptr
		file   **os.File
	}{
		{stdOutputHandle, &os.Stdout},
		{stdErrorHandle, &os.Stderr},
	} {
		h, _, _ := procGetStdHandle.Call(item.handle)
		if h == 0 || h == ^uintptr(0) {
			continue
		}
		*item.file = os.NewFile(h, "console")
	}
}
