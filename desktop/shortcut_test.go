// Tests for what a window shortcut does before it reaches a player.
package main

import (
	"strings"
	"testing"
)

func TestAShortcutWithoutASourceSaysWhatToDo(t *testing.T) {
	a, _ := bareState(t)
	a.selected = ""

	a.shortcutCommand(commandShow)

	if a.lastErr == nil || !strings.Contains(a.lastErr.Error(), "no source is selected") {
		t.Fatalf("lastErr = %v, want it to ask for a source", a.lastErr)
	}
	if a.lastCommand.Message != "nothing selected" {
		t.Fatalf("message = %q, want nothing selected", a.lastCommand.Message)
	}
	if a.shortcutFires != 1 || a.firesWhileTyping != 0 {
		t.Fatalf("fires = %d, while typing = %d, want 1 and 0", a.shortcutFires, a.firesWhileTyping)
	}
}

func TestAShortcutNoticesWhenATextFieldHeldTheFocus(t *testing.T) {
	a, _ := bareState(t)
	a.keysTyping = true

	a.shortcutCommand(commandHear)

	if a.firesWhileTyping != 1 {
		t.Fatalf("fires while typing = %d, want 1", a.firesWhileTyping)
	}
}

func TestSelectedSourceIsWhatAShortcutActsOn(t *testing.T) {
	a, it := bareState(t)
	a.selected = it.source.ID

	_, index, ok := a.selectedSource()
	if !ok || index != 0 {
		t.Fatalf("selectedSource() = %d, %v, want the only source", index, ok)
	}
}
