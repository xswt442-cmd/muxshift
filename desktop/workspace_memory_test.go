package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// TestValidateRejectsARememberedShapeItCannotHonour keeps a file from one build
// from steering the next one somewhere it cannot draw: an unknown page, an
// unknown pane mode, or a frame no screen could hold.
func TestValidateRejectsARememberedShapeItCannotHonour(t *testing.T) {
	base := func() workspaceMemory {
		return workspaceMemory{
			On: true, Page: "sources", X: 100, Y: 80, Width: 1180, Height: 840,
			Rail: "auto", Inspector: "auto", Dock: "auto",
		}
	}
	if err := validateWorkspaceMemory(base()); err != nil {
		t.Fatalf("a plain remembered shape does not validate: %v", err)
	}
	cases := []struct {
		name  string
		edit  func(*workspaceMemory)
		wants string
	}{
		{"page", func(w *workspaceMemory) { w.Page = "dvr" }, "workspace page"},
		{"rail", func(w *workspaceMemory) { w.Rail = "sometimes" }, "workspace rail"},
		{"dock", func(w *workspaceMemory) { w.Dock = "half" }, "workspace dock"},
		{"too small", func(w *workspaceMemory) { w.Width = 200 }, "workspace width"},
		{"too tall", func(w *workspaceMemory) { w.Height = 40000 }, "workspace height"},
		{"nowhere", func(w *workspaceMemory) { w.X = 50000 }, "workspace position"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := base()
			tc.edit(&w)
			err := validateWorkspaceMemory(w)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error %q does not mention %q", err, tc.wants)
			}
		})
	}
	off := base()
	off.On = false
	off.Page = "not-a-page"
	if err := validateWorkspaceMemory(off); err != nil {
		t.Fatalf("a memory that is off still validated its unused fields: %v", err)
	}
}

// TestPaneModeNamesRoundTrip pins the stored spellings: a name is part of the
// file format, so renaming one silently would make an old file meaningless.
func TestPaneModeNamesRoundTrip(t *testing.T) {
	for _, mode := range []paneMode{paneAuto, paneOpen, paneClosed} {
		name := paneModeName(mode)
		back, ok := parsePaneMode(name)
		if !ok || back != mode {
			t.Fatalf("%v named %q came back as %v (%v)", mode, name, back, ok)
		}
	}
	if _, ok := parsePaneMode("OPEN"); ok {
		t.Fatal("a pane name is case-sensitive and OPEN was accepted")
	}
}

// TestClampWorkspaceKeepsTheFrameReachable is the rule for a display that went
// away: keep the size when it fits, and bring the title bar back to a place a
// person can grab.
func TestClampWorkspaceKeepsTheFrameReachable(t *testing.T) {
	displays := []mygo.Display{
		{Primary: true, WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}},
		{WorkArea: mygo.Rectangle{X: 1920, Y: 0, Width: 1280, Height: 1024}},
	}
	onScreen := mygo.Rectangle{X: 100, Y: 80, Width: 1180, Height: 840}
	if got := clampWorkspace(onScreen, displays); got != onScreen {
		t.Fatalf("an on-screen frame moved: %+v", got)
	}
	onSecond := mygo.Rectangle{X: 2000, Y: 100, Width: 900, Height: 700}
	if got := clampWorkspace(onSecond, displays); got != onSecond {
		t.Fatalf("a frame on the second display moved: %+v", got)
	}
	// Just the title strip has to be reachable, so a window hanging off the
	// right edge is still fine.
	hanging := mygo.Rectangle{X: 1900, Y: 10, Width: 600, Height: 400}
	if got := clampWorkspace(hanging, displays); got != hanging {
		t.Fatalf("a deliberately overhanging frame moved: %+v", got)
	}
	gone := mygo.Rectangle{X: -4000, Y: -3000, Width: 1180, Height: 840}
	got := clampWorkspace(gone, displays)
	if got.Width != 1180 || got.Height != 840 {
		t.Fatalf("the size changed while only the position was unreachable: %+v", got)
	}
	if !intersects(mygo.Rectangle{X: got.X, Y: got.Y, Width: got.Width, Height: 40}, displays[0].WorkArea) {
		t.Fatalf("the frame was not brought back to the primary display: %+v", got)
	}
	huge := mygo.Rectangle{X: 0, Y: 0, Width: 3000, Height: 1400}
	got = clampWorkspace(huge, displays)
	if got.Width != 1920 || got.Height != 1040 {
		t.Fatalf("a frame larger than the screen was not shrunk to it: %+v", got)
	}
}

// TestWorkspaceMemorySurvivesTheSettingsFile is the whole point of the feature:
// what one start writes, the next start reads back as the same shape.
func TestWorkspaceMemorySurvivesTheSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := defaultSettings()
	want.Workspace = workspaceMemory{
		On: true, Page: "mixer", X: 220, Y: 140, Width: 1180, Height: 840,
		Rail: "closed", Inspector: "auto", Dock: "open",
	}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, note := loadSettings(path)
	if note != "" {
		t.Fatalf("load reported %q", note)
	}
	if loaded.Workspace != want.Workspace {
		t.Fatalf("workspace came back as %+v, want %+v", loaded.Workspace, want.Workspace)
	}
	// A file without the block is still a valid file: the memory is off.
	plain := defaultSettings()
	if err := saveSettings(path, plain); err != nil {
		t.Fatalf("save without the block: %v", err)
	}
	loaded, note = loadSettings(path)
	if note != "" || loaded.Workspace.On {
		t.Fatalf("a file without the block loaded as %+v (%q)", loaded.Workspace, note)
	}
}
