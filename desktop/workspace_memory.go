// Remembering the shape of the window is part of P18.1's "workspace memory":
// a restart should come back to the frame, the page and the panes a person
// left behind, and it should say so rather than doing it silently.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo"
)

// workspaceMemory is what a restart can put back. Page and the pane modes are
// names rather than numbers so a file written by one build stays readable by
// the next one, and an unknown name is a validation error instead of a silent
// jump to the stage.
type workspaceMemory struct {
	// On is a person's answer to "keep the window as it is"; off means the
	// start page and the size rule decide, exactly as before this existed.
	On bool `json:"on"`

	// Page is the page the window was on. Empty means "use startPage".
	Page string `json:"page,omitempty"`

	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`

	// Rail, Inspector and Dock are "auto", "open" or "closed": auto follows
	// the window-size rule, the other two are a person's explicit choice.
	Rail      string `json:"rail"`
	Inspector string `json:"inspector"`
	Dock      string `json:"dock"`
}

// paneModeName is the stored spelling of a pane mode.
func paneModeName(m paneMode) string {
	switch m {
	case paneOpen:
		return "open"
	case paneClosed:
		return "closed"
	}
	return "auto"
}

// parsePaneMode reads a stored spelling. The second result says whether the
// name is one this build understands.
func parsePaneMode(name string) (paneMode, bool) {
	switch name {
	case "auto":
		return paneAuto, true
	case "open":
		return paneOpen, true
	case "closed":
		return paneClosed, true
	}
	return paneAuto, false
}

// workspaceNames maps the pages that may be remembered to their ids. Stage is
// in the list even though it is the zero value: a person who leaves on the
// stage means it, and that is different from "no page was recorded".
var workspaceNames = []struct {
	id    string
	index workspace
}{
	{"stage", workspaceStage},
	{"sources", workspaceSources},
	{"diagnostics", workspaceDiagnostics},
	{"settings", workspaceSettings},
	{"mixer", workspaceMixer},
}

// workspaceID is the stored spelling of a page.
func workspaceID(w workspace) (string, bool) {
	for _, name := range workspaceNames {
		if name.index == w {
			return name.id, true
		}
	}
	return "", false
}

// parseWorkspaceID reads a stored page name.
func parseWorkspaceID(id string) (workspace, bool) {
	for _, name := range workspaceNames {
		if name.id == id {
			return name.index, true
		}
	}
	return workspaceStage, false
}

// validateWorkspaceMemory rejects a remembered shape the window cannot honour.
// A frame smaller than the workbench's own minimum, or a page or pane name this
// build does not know, would leave the next start in a state nobody chose.
func validateWorkspaceMemory(w workspaceMemory) error {
	if !w.On {
		return nil
	}
	if w.Page != "" {
		if _, ok := parseWorkspaceID(w.Page); !ok {
			return fmt.Errorf("workspace page %q is not a page this build has", w.Page)
		}
	}
	for name, value := range map[string]string{
		"rail": w.Rail, "inspector": w.Inspector, "dock": w.Dock,
	} {
		if _, ok := parsePaneMode(value); !ok {
			return fmt.Errorf("workspace %s %q is not auto, open or closed", name, value)
		}
	}
	if w.Width < minWorkspaceWidth || w.Width > maxWorkspaceSide {
		return fmt.Errorf("workspace width %d is not between %d and %d", w.Width, minWorkspaceWidth, maxWorkspaceSide)
	}
	if w.Height < minWorkspaceHeight || w.Height > maxWorkspaceSide {
		return fmt.Errorf("workspace height %d is not between %d and %d", w.Height, minWorkspaceHeight, maxWorkspaceSide)
	}
	if w.X < -maxWorkspaceSide || w.X > maxWorkspaceSide || w.Y < -maxWorkspaceSide || w.Y > maxWorkspaceSide {
		return fmt.Errorf("workspace position %d,%d is outside any screen", w.X, w.Y)
	}
	return nil
}

const (
	// The smallest frame this build is willing to restore. It is smaller than
	// the workbench's content minimum on purpose: the fold rules decide what
	// fits, and refusing a size a person actually had would be worse.
	minWorkspaceWidth  = 320
	minWorkspaceHeight = 240
	maxWorkspaceSide   = 20000
)

// clampWorkspace keeps a remembered frame reachable on the screens that exist
// now. The rule is the one a person would use: a frame whose title bar is on a
// display is kept exactly as it was, even if it hangs off an edge, unless it is
// larger than that display — then it is shrunk to fit, because the buttons on
// the far side would otherwise be unreachable. A frame with no title bar on any
// display is put back on the primary display.
func clampWorkspace(r mygo.Rectangle, displays []mygo.Display) mygo.Rectangle {
	if len(displays) == 0 || r.Width <= 0 || r.Height <= 0 {
		return r
	}
	strip := mygo.Rectangle{X: r.X, Y: r.Y, Width: r.Width, Height: 40}
	for _, d := range displays {
		// A strip of the title bar has to be on screen, not the whole frame:
		// a window deliberately hanging off an edge is still usable.
		if !intersects(strip, d.WorkArea) {
			continue
		}
		area := d.WorkArea
		if r.Width > area.Width {
			r.Width = area.Width
		}
		if r.Height > area.Height {
			r.Height = area.Height
		}
		return r
	}
	primary := displays[0]
	for _, d := range displays {
		if d.Primary {
			primary = d
			break
		}
	}
	area := primary.WorkArea
	if r.Width > area.Width {
		r.Width = area.Width
	}
	if r.Height > area.Height {
		r.Height = area.Height
	}
	r.X = clampInt(r.X, area.X, area.X+area.Width-40)
	r.Y = clampInt(r.Y, area.Y, area.Y+area.Height-40)
	return r
}

// clampInt limits v to [lo, hi]; a hi below lo (a display no wider than 40
// pixels) yields lo rather than an inverted range.
func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// workspaceFromWindow is the remembered shape of the window right now. on is
// the person's answer, kept separate so the same read can be used both to
// remember on exit and to fill in a draft before it is applied.
func (a *appState) workspaceFromWindow(window *mygo.Window, on bool) workspaceMemory {
	if window == nil {
		return a.settings.Workspace
	}
	b := window.Bounds()
	page, ok := workspaceID(a.workspace)
	if !ok {
		page = ""
	}
	return workspaceMemory{
		On:        on,
		Page:      page,
		X:         b.X,
		Y:         b.Y,
		Width:     b.Width,
		Height:    b.Height,
		Rail:      paneModeName(a.rail),
		Inspector: paneModeName(a.inspector),
		Dock:      paneModeName(a.dock),
	}
}

// rememberWorkspace reads the window and the app's own navigation state into
// the settings, so saveSettings writes what the window actually was.
func (a *appState) rememberWorkspace(window *mygo.Window) {
	a.settings.Workspace = a.workspaceFromWindow(window, a.settings.Workspace.On)
}

// saveWorkspaceOnExit writes the remembered shape, and says why when it cannot.
// A settings file that did not load is never overwritten: it is the person's
// file, and a start that could not read it has no business replacing it.
func (a *appState) saveWorkspaceOnExit(window *mygo.Window) {
	if !a.settings.Workspace.On {
		return
	}
	if !a.settingsTrusted {
		log.Printf("workspace: not saved, the settings file was not loaded")
		return
	}
	a.rememberWorkspace(window)
	// The window closes while a remembered preference may still be on its way
	// to disk. Both writes go through the same lock and the same ticket order,
	// so the one built last wins instead of the one scheduled last.
	if err := a.saveSettingsSerialized(cloneSettings(a.settings), a.saveTicket()); err != nil {
		log.Printf("workspace: not saved: %v", err)
		return
	}
	w := a.settings.Workspace
	log.Printf("workspace: saved %s at %d,%d %dx%d, rail %s, inspector %s, dock %s",
		orNone(w.Page), w.X, w.Y, w.Width, w.Height, w.Rail, w.Inspector, w.Dock)
}

// restoreWorkspace puts a remembered shape back before the first frame. Only
// what the file claims is restored: an off means nothing here is touched.
func (a *appState) restoreWorkspace(window *mygo.Window, displays []mygo.Display) {
	remembered := a.settings.Workspace
	if !remembered.On || window == nil {
		return
	}
	if page, ok := parseWorkspaceID(remembered.Page); ok && remembered.Page != "" {
		a.workspace = page
	}
	if mode, ok := parsePaneMode(remembered.Rail); ok {
		a.rail = mode
	}
	if mode, ok := parsePaneMode(remembered.Inspector); ok {
		a.inspector = mode
	}
	if mode, ok := parsePaneMode(remembered.Dock); ok {
		a.dock = mode
	}
	frame := clampWorkspace(mygo.Rectangle{
		X: remembered.X, Y: remembered.Y, Width: remembered.Width, Height: remembered.Height,
	}, displays)
	if frame != (mygo.Rectangle{X: remembered.X, Y: remembered.Y, Width: remembered.Width, Height: remembered.Height}) {
		log.Printf("workspace: %dx%d at %d,%d is off screen; using %dx%d at %d,%d",
			remembered.Width, remembered.Height, remembered.X, remembered.Y,
			frame.Width, frame.Height, frame.X, frame.Y)
	}
	window.SetBounds(frame)
	log.Printf("workspace: restored %s at %d,%d %dx%d, rail %s, inspector %s, dock %s",
		orNone(remembered.Page), frame.X, frame.Y, frame.Width, frame.Height,
		remembered.Rail, remembered.Inspector, remembered.Dock)
}

// orNone names an empty page in a log line without pretending it is a page.
func orNone(page string) string {
	if strings.TrimSpace(page) == "" {
		return "no page"
	}
	return page
}
