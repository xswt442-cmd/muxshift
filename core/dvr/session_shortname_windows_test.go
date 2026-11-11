//go:build windows

package dvr

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfirmOwnDirectoryAcceptsAShortNamedParent covers the shape the Windows
// CI runner has: the temporary directory arrives as C:\Users\RUNNER~1\... while
// resolution answers C:\Users\runneradmin\.... That difference is a spelling of
// the same directory, not a link, and a sweep that called it one would leave
// every session behind instead of collecting the dead ones.
func TestConfirmOwnDirectoryAcceptsAShortNamedParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "a-long-parent-name-that-has-a-short-form")
	session := filepath.Join(parent, sessionPrefix+"0123456789abcdef")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatalf("make the session directory: %v", err)
	}
	short := shortForm(session)
	if samePath(short, session) {
		t.Skip("this volume gives no short names, so there is nothing to check")
	}
	if !samePath(shortForm(short), shortForm(session)) {
		t.Fatalf("the short and long spellings disagree: %s vs %s", shortForm(short), shortForm(session))
	}
	if reason := confirmOwnDirectory(short); reason != "" {
		t.Fatalf("a session reached through its short-named parent was refused: %s", reason)
	}
	if reason := confirmOwnDirectory(session); reason != "" {
		t.Fatalf("the long-named path was refused: %s", reason)
	}
}

// TestConfirmOwnDirectoryRefusesAJunction keeps the check meaningful: a
// directory entry that leads somewhere else must still be refused, whatever
// spelling it arrives in.
func TestConfirmOwnDirectoryRefusesAJunction(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatalf("make the target: %v", err)
	}
	link := filepath.Join(root, sessionPrefix+"0123456789abcdef")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("this machine does not let an unprivileged process make a link: %v", err)
	}
	if reason := confirmOwnDirectory(link); reason == "" {
		t.Fatal("an entry that leads elsewhere was accepted")
	}
}
