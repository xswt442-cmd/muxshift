//go:build !windows

package win

// Locale is empty off Windows: there is no registry to read.
func Locale() string { return "" }
