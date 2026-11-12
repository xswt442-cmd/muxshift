// Command muxshift-desktop: the focus and keyboard walk (P18.1-focus-keys).
//
// The walk itself happens outside the process: testplace/p18/focusclient
// speaks the automation protocol and sends real Tab presses, text and key
// combinations, so that what this check observes is what the window did with
// keys a person could press, not a call into a command function. This side
// only watches the three things the window knows about itself: whether a text
// field holds the keyboard focus, which page is showing, and whether a
// shortcut fired — and it insists that no shortcut fires while a text field
// has the focus.
package main

import (
	"time"

	"github.com/egoist/mygo" //nolint:depguard // the window is this build's whole surface
)

// runFocus starts the keyboard walk. It returns at once: the body sleeps while
// the client presses keys.
func runFocus(window *mygo.Window, a *appState, dir string) {
	go focusBody(window, a, dir)
}

func focusBody(window *mygo.Window, a *appState, dir string) {
	const scenario = "focus"
	logf(scenario, "the walk is outside this process: testplace\\p18\\focusclient sends Tab, text and key chords")

	deadline := time.Now().Add(70 * time.Second)
	samples := 0
	seenPages := map[string]bool{}
	lastPage := ""
	for {
		time.Sleep(200 * time.Millisecond)
		samples++
		var (
			typing      bool
			seen        int
			typingCount int
			fires       int
			whileTyping int
			closes      int
			page        string
		)
		mygoRunOnMain(func() {
			typing = a.keysTyping
			seen = a.keysSeen
			typingCount = a.typingEdges
			fires = a.shortcutFires
			whileTyping = a.firesWhileTyping
			closes = a.closes
			page = a.workspace.title()
		})
		seenPages[page] = true
		if page != lastPage {
			logf(scenario, "the page under the keyboard is now %s", page)
			lastPage = page
		}
		if samples%5 == 0 {
			logf(scenario, "sample %d: a text field has the focus %v (seen in %d samples of %d), shortcuts fired %d (while typing %d), closes %d",
				samples, typing, typingCount, seen, fires, whileTyping, closes)
		}
		if closes > 0 {
			logf(scenario, "the window closed; stopping the watch")
			break
		}
		if time.Now().After(deadline) {
			logf(scenario, "the deadline passed without a close")
			break
		}
	}

	var typingCount, fires, whileTyping, closes int
	mygoRunOnMain(func() {
		typingCount = a.typingEdges
		fires = a.shortcutFires
		whileTyping = a.firesWhileTyping
		closes = a.closes
	})

	failed := false
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failed = true
		logf(scenario, "CHECK FAILED: "+format, args...)
	}
	check(typingCount > 0, "Tab reached a text field: the focus entered one %d time(s)", typingCount)
	check(whileTyping == 0, "no shortcut fired while a text field had the focus (fired %d time(s) overall, %d while typing)", fires, whileTyping)
	check(seenPages["Mixer"], "the keyboard reached the mixer page")
	check(closes <= 1, "the window closed at most once (%d)", closes)
	if failed {
		logf(scenario, "checks failed: 1")
	} else {
		logf(scenario, "checks failed: 0")
	}
	a.release()
	mygo.App.Quit()
}
