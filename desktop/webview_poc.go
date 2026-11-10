// P13.3: can a MyGo web view control the same session the native stage draws?
//
// The window keeps its native toolbar and its native video stage; a small page
// sits below them and reads the session through a bound Go value. The page can
// ask for the same commands the buttons ask for, through runCommand, and it
// renders the snapshot the boundary copies — it never gets a second Session.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core"
)

//go:embed webpanel
var panelFiles embed.FS

// panelStateEvent carries a rendered snapshot from Go to the page. The page
// listens for it with mygo.on("panel-state", ...).
var panelStateEvent = mygo.NewEvent[string]("panel-state")

// Panel is the value bound for the page: every method goes through the same
// command boundary as the native buttons.
type Panel struct {
	a *appState
}

// timeMilliseconds turns the page's integer into a duration.
func timeMilliseconds(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// panelSource is one source as the page shows it.
type panelSource struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Clock string `json:"clock"`
	Shown bool   `json:"shown"`
	Heard bool   `json:"heard"`
	Ready bool   `json:"ready"`
}

// panelView is the page's whole model.
type panelView struct {
	Summary string        `json:"summary"`
	Sources []panelSource `json:"sources"`
}

// describeOnMain renders the snapshot on the drawing thread.
func (p *Panel) describeOnMain() string {
	var view panelView
	mygoRunOnMain(func() {
		snapshot := p.a.snapshot()
		picture := map[core.TrackID]bool{}
		for _, track := range snapshot.Picture {
			picture[track] = true
		}
		heard := map[core.TrackID]bool{}
		for _, sound := range snapshot.Sound {
			if !sound.Muted {
				heard[sound.Track] = true
			}
		}
		for i, source := range snapshot.Sources {
			clock := source.Position.Round(100 * 1e6).String()
			if source.Duration > 0 {
				clock += " / " + source.Duration.Round(100*1e6).String()
			}
			view.Sources = append(view.Sources, panelSource{
				ID:    string(source.ID),
				Name:  fmt.Sprintf("source %d", i+1),
				Clock: clock,
				Shown: picture[source.Video] || picture[source.Audio],
				Heard: heard[source.Audio],
				Ready: source.Ready,
			})
		}
		view.Summary = fmt.Sprintf("event %s · playing %v · %s",
			snapshot.Event.Round(100*1e6), snapshot.Playing, snapshot.LastCommand)
	})
	data, err := json.Marshal(view)
	if err != nil {
		return `{"summary":"cannot render the snapshot","sources":[]}`
	}
	return string(data)
}

// State is what the page asks for on load and once a second.
func (p *Panel) State(ctx context.Context) (string, error) {
	p.a.panelCalls++
	log.Printf("panel: the page asked for the state (%d)", p.a.panelCalls)
	return p.describeOnMain(), nil
}

// commandOnMain runs one command through the boundary and returns the new view.
func (p *Panel) commandOnMain(cmd command) (string, error) {
	var result commandResult
	mygoRunOnMain(func() { result = p.a.runCommand(cmd) })
	p.a.report(result)
	if result.Err != nil {
		return p.describeOnMain(), result.Err
	}
	return p.describeOnMain(), nil
}

// Show makes a source's picture the one on the stage.
func (p *Panel) Show(ctx context.Context, source string) (string, error) {
	return p.commandOnMain(command{Kind: commandShow, Source: core.SourceID(source)})
}

// Hear puts a source's audio on the scene.
func (p *Panel) Hear(ctx context.Context, source string) (string, error) {
	return p.commandOnMain(command{Kind: commandHear, Source: core.SourceID(source)})
}

// Nudge moves one source by the given milliseconds.
func (p *Panel) Nudge(ctx context.Context, source string, milliseconds int) (string, error) {
	return p.commandOnMain(command{
		Kind:   commandNudge,
		Source: core.SourceID(source),
		Delta:  timeMilliseconds(milliseconds),
	})
}

// PauseAll and PlayAll drive the synchronised group.
func (p *Panel) PauseAll(ctx context.Context) (string, error) {
	return p.commandOnMain(command{Kind: commandPause})
}

func (p *Panel) PlayAll(ctx context.Context) (string, error) {
	return p.commandOnMain(command{Kind: commandPlay})
}

// Note lets the page say what it rendered, so a log shows the page's own view
// and not only the commands it sends.
func (p *Panel) Note(ctx context.Context, text string) (string, error) {
	log.Printf("panel: the page says %q", text)
	return "", nil
}

// broadcastPanel pushes the current view to the page.
func (a *appState) broadcastPanel() {
	if a.panel == nil {
		return
	}
	panelStateEvent.Broadcast(a.panel.describeOnMain())
}

// startPanelFrontend makes the embedded page the app's frontend and binds the
// value the page calls. It must run before the window is created.
func startPanelFrontend(a *appState) (*Panel, error) {
	sub, err := fs.Sub(panelFiles, "webpanel")
	if err != nil {
		return nil, err
	}
	mygo.SetFrontend(sub)
	panel := &Panel{a: a}
	mygo.Bind(panel)
	a.panel = panel
	return panel, nil
}
