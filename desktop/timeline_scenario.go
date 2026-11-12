package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// runTimeline checks the read-only timeline view (P17.1) in a real window: it
// must say "not aligned" where it knows nothing, show a real zero offset once a
// person anchors, keep the three times apart, and change nothing by drawing.
func runTimeline(window *mygo.Window, a *appState, dir string) {
	go timelineBody(window, a, dir)
}

func timelineBody(window *mygo.Window, a *appState, dir string) {
	checks := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			checks++
			logf("timeline", "CHECK FAILED: "+format, args...)
		}
	}

	if len(a.items) < 2 {
		logf("timeline", "this check needs two sources")
		finishScenario(a)
		return
	}
	if !waitPlayers(a, 30*time.Second) {
		logf("timeline", "the players never came up")
		finishScenario(a)
		return
	}

	step(window, a, dir, "timeline", "1-unaligned.png", "before anybody aligns anything", func(a *appState) {
		lines := a.timelineLines()
		for _, line := range lines {
			logf("timeline", "before aligning: %s", line)
			check(strings.Contains(line, "not aligned"), "an unaligned source does not read as unknown: %q", line)
			check(!strings.Contains(line, "offset"), "an unaligned source claims an offset: %q", line)
		}
		check(len(lines) == 2, "want two rows, got %d", len(lines))
	})

	// A person anchors: that is the real command path, not a view-only edit.
	if err := a.anchor(0); err != nil {
		logf("timeline", "anchor: %v", err)
		checks++
	}
	if _, err := a.nudge(1, 0); err != nil {
		logf("timeline", "the nudge on the second source: %v", err)
	}

	step(window, a, dir, "timeline", "2-aligned.png", "after anchoring the first source", func(a *appState) {
		lines := a.timelineLines()
		joined := strings.Join(lines, "\n")
		for _, line := range lines {
			logf("timeline", "after anchoring: %s", line)
		}
		check(strings.Contains(joined, "offset +0.0s"), "the anchored source does not show its real zero offset: %q", joined)
		check(strings.Contains(joined, "manual"), "the anchored source does not say where its offset came from: %q", joined)
		check(strings.Contains(joined, "ref"), "the anchored reference track is not marked: %q", joined)
		check(strings.Contains(joined, "media "), "the view does not show the mapped media time: %q", joined)
		check(strings.Contains(joined, "player "), "the view does not show what the player reports: %q", joined)
	})

	// Drawing is read-only: a second read must produce the same rows and leave
	// the timeline and playhead exactly where the person left them.
	before := a.sync.timeline.Shifts()
	eventBefore := a.sync.playhead.Event()
	first := strings.Join(a.timelineLines(), "\n")
	second := strings.Join(a.timelineLines(), "\n")
	after := a.sync.timeline.Shifts()
	check(first == second, "the view changed between two reads:\n%s\n---\n%s", first, second)
	check(len(before) == len(after), "the view changed the number of shifts: %d -> %d", len(before), len(after))
	check(eventBefore == a.sync.playhead.Event(), "the view moved the playhead: %s -> %s", eventBefore, a.sync.playhead.Event())

	logf("timeline", "timeline: %d row(s), checks failed: %d", len(a.items), checks)
	a.release()
	mygo.App.Quit()
}
