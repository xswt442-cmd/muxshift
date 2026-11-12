// The folded-rail page switch: below the rail threshold the rail disappears,
// and the bar's page button is the only visible way from one page to the next.
// This walk watches the page change while an automation client presses that
// button with real control activation.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
)

func runPages(window *mygo.Window, a *appState, dir string) {
	go pagesBody(window, a, dir)
}

func pagesBody(window *mygo.Window, a *appState, dir string) {
	scenario := "pages"
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if ok {
			logf(scenario, "ok: "+format, args...)
			return
		}
		failures++
		logf(scenario, "CHECK FAILED: "+format, args...)
	}

	// 641x560 pixels at 100% is the smallest content the workbench supports
	// (626 DIPs wide), which is exactly where the rail folds.
	step(window, a, dir, scenario, "", "the smallest window", func(a *appState) {
		a.zoom = 1
		window.SetSize(641, 560)
	})
	var folded bool
	mygoRunOnMain(func() {
		folded = !a.railOpen(a.lastWidth)
		a.workspace = workspaceStage
		window.Invalidate()
	})
	check(folded, "the rail folds at the smallest window, which is what this walk needs")
	logf(scenario, "the rail is folded; the bar's page button is the visible way between pages")

	// Watch the page for as long as the client needs to press the button once
	// per page: the walk itself is driven from outside, the way a person would.
	visited := []workspace{}
	last := workspace(-1)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var current workspace
		mygoRunOnMain(func() { current = a.workspace })
		if current != last {
			visited = append(visited, current)
			last = current
			logf(scenario, "page is now %s", current.title())
		}
		time.Sleep(200 * time.Millisecond)
	}
	step(window, a, dir, scenario, "pages.png", "after pressing the page button", nil)

	check(len(visited) >= 5, "the page button visited %d pages in sequence (%v)", len(visited), visited)
	if len(visited) > 0 {
		check(visited[0] == workspaceStage, "the walk started on Stage (%s)", visited[0].title())
	}
	if len(visited) == int(workspaceCount) {
		// A full cycle ends where it started; one extra press proves the button
		// keeps working after the last page.
		check(visited[len(visited)-1] == workspaceStage,
			"the full cycle came back to Stage (%s)", visited[len(visited)-1].title())
	}
	for index := 1; index < len(visited); index++ {
		want := (visited[index-1] + 1) % workspaceCount
		check(visited[index] == want, "the button went from %s to %s, and the next page is %s",
			visited[index-1].title(), visited[index].title(), want.title())
	}
	logf(scenario, "the page button walked: %s", pagePath(visited))

	step(window, a, dir, scenario, "", "back to a normal window", func(a *appState) {
		window.SetSize(1426, 862)
	})
	logf(scenario, "checks failed: %d", failures)
	a.release()
	mygo.App.Quit()
}

func pagePath(pages []workspace) string {
	out := ""
	for index, page := range pages {
		if index > 0 {
			out += " -> "
		}
		out += page.title()
	}
	return out
}

// pageSummary is the one-line state the log keeps beside the walk, so a failure
// says what the bar was told it was showing.
func (a *appState) pageSummary() string {
	return fmt.Sprintf("%s at %.1f DIPs wide", a.workspace.title(), a.lastWidth)
}
