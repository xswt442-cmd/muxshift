package main

import (
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo"
)

// runKeys keeps the window up long enough for a person (or a script driving the
// real keyboard) to press the shortcuts, and logs what each press produced. It
// does not synthesise key events: the point is to see the window's own shortcut
// handling fire, so the keys must come from outside.
func runKeys(window *mygo.Window, a *appState, dir string) {
	go func() {
		defer a.release()
		defer mygo.App.Quit()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			var ready bool
			done := make(chan struct{})
			mygoRunOnMain(func() {
				ready = len(a.items) > 0 && a.items[0].player != nil
				close(done)
			})
			<-done
			if ready {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		mygoRunOnMain(func() {
			if len(a.items) > 0 {
				a.selected = a.items[0].source.ID
			}
			a.workspace = workspaceSources
			log.Printf("keys: selected %q on the Sources page; press Ctrl+S, Ctrl+H, Ctrl+R, Ctrl+1..4", a.selected)
		})
		var lastAt time.Time
		deadline := time.Now().Add(14 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			mygoRunOnMain(func() {
				if a.lastCommand.At.Equal(lastAt) {
					return
				}
				lastAt = a.lastCommand.At
				log.Printf("keys: last command %s", a.lastCommand)
				log.Printf("keys: workspace %d, selected %q", a.workspace, a.selected)
				capture(window, fmt.Sprintf("%s/last.png", dir))
			})
		}
	}()
}
