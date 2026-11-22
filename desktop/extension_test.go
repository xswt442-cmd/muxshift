package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExtension lays out the folder a release ships: a directory holding
// manifest.json is what the resolution treats as the extension.
func writeExtension(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write manifest.json in %s: %v", dir, err)
	}
}

func TestExtensionFolderIsTheOneBesideTheBinary(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "extension")
	writeExtension(t, want)
	a := &appState{exeDir: root}
	got, err := a.extensionFolder()
	if err != nil {
		t.Fatalf("resolving the extension folder: %v", err)
	}
	if got != want {
		t.Fatalf("resolved %q, want %q", got, want)
	}
}

func TestExtensionFolderFallsBackToTheCheckoutRoot(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "extension")
	writeExtension(t, want)
	// A checkout runs desktop\desktop.exe with extension\ at the repository root.
	a := &appState{exeDir: filepath.Join(root, "desktop")}
	got, err := a.extensionFolder()
	if err != nil {
		t.Fatalf("resolving the extension folder one level up: %v", err)
	}
	if got != want {
		t.Fatalf("resolved %q, want %q", got, want)
	}
}

func TestExtensionFolderWithoutAManifestIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "extension"), 0o755); err != nil {
		t.Fatalf("create the directory: %v", err)
	}
	a := &appState{exeDir: root}
	if dir, err := a.extensionFolder(); err == nil {
		t.Fatalf("a folder without manifest.json was accepted as %q", dir)
	}
}

func TestOpenExtensionCommandAsksTheShellForTheFolder(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "extension")
	writeExtension(t, want)
	var opened []string
	a := &appState{exeDir: root, openFolder: func(dir string) error {
		opened = append(opened, dir)
		return nil
	}}
	result := a.runCommand(command{Kind: commandOpenExtension})
	if result.Err != nil {
		t.Fatalf("opening the extension folder: %v", result.Err)
	}
	if len(opened) != 1 || opened[0] != want {
		t.Fatalf("asked the shell to open %v, want [%s]", opened, want)
	}
	if result.Message != want {
		t.Fatalf("the result says %q, want the folder %q", result.Message, want)
	}
}

func TestOpenExtensionCommandRefusesWhenThereIsNoFolder(t *testing.T) {
	opened := false
	a := &appState{exeDir: t.TempDir(), openFolder: func(string) error {
		opened = true
		return nil
	}}
	result := a.runCommand(command{Kind: commandOpenExtension})
	if result.Err == nil {
		t.Fatal("a build without the extension folder reported success")
	}
	if opened {
		t.Fatal("the shell was asked to open a folder that does not exist")
	}
}
