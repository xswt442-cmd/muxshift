//go:build !windows

package win

// AttachConsole is unnecessary outside Windows: a normal console build already
// has the parent's standard handles.
func AttachConsole() {}
