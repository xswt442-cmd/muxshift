//go:build windows

package win

import "os/exec"

// OpenFolder shows a directory in Explorer. It asks the shell rather than
// starting a process of its own, so no console window appears and the caller
// does not have to wait for the folder to close.
func OpenFolder(path string) error {
	return exec.Command("explorer.exe", path).Start()
}
