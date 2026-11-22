package main

// The browser extension is shipped beside muxshift.exe in a release and one
// level above the binary in a checkout. Loading an unpacked extension is a
// browser action only the person can take, so this file answers two questions
// honestly: where the folder is, and whether opening it worked. It never
// pretends the window can install the extension itself.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xswt442-cmd/muxshift/desktop/internal/win"
)

const extensionFolderName = "extension"

// extensionFolder is the directory this build loads the extension from. A
// candidate counts only when it holds manifest.json, so a build that carries no
// extension says so instead of pointing at a path that does not exist.
func (a *appState) extensionFolder() (string, error) {
	if a.extensionDir != "" {
		return a.extensionDir, nil
	}
	dir, err := a.executableDir()
	if err != nil {
		return "", err
	}
	tried := []string{filepath.Join(dir, extensionFolderName)}
	// A run from the checkout has the binary in desktop\ and the extension at
	// the repository root.
	if parent := filepath.Dir(dir); parent != dir {
		tried = append(tried, filepath.Join(parent, extensionFolderName))
	}
	for _, candidate := range tried {
		if info, err := os.Stat(filepath.Join(candidate, "manifest.json")); err == nil && info.Mode().IsRegular() {
			a.extensionDir = candidate
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no %s folder with manifest.json beside %s or one level up", extensionFolderName, dir)
}

// executableDir is where this process was started from. A test replaces it
// through exeDir, because a test binary does not sit in a release layout.
func (a *appState) executableDir() (string, error) {
	if a.exeDir != "" {
		return a.exeDir, nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not tell where this build runs from: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Dir(path), nil
}

// openExtensionFolder shows the folder in the system shell. The shell is the
// seam a test replaces; without one the attempt fails and says why.
func (a *appState) openExtensionFolder(dir string) error {
	open := a.openFolder
	if open == nil {
		open = systemOpenFolder
	}
	if err := open(dir); err != nil {
		return fmt.Errorf("opening %s failed: %w", dir, err)
	}
	return nil
}

// systemOpenFolder is the shell behind the seam.
func systemOpenFolder(dir string) error { return win.OpenFolder(dir) }
