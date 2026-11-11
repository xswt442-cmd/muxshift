//go:build !windows

package dvr

// shortForm returns the path itself: file systems without 8.3 names spell an
// entry one way only, so the path is already the comparable form.
func shortForm(path string) string { return path }
