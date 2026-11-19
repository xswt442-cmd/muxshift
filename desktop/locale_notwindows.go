//go:build !windows

package main

// windowsLocale is empty off Windows: there is no registry to read.
func windowsLocale() string { return "" }
