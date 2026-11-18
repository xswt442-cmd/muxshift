//go:build !windows

package main

// windowsProxy has nothing to read outside Windows: the setting it names is a
// Windows Internet Options value, not a portable concept.
func windowsProxy() (string, bool) { return "", false }
