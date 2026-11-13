package main

import (
	"strings"
	"testing"
	"time"
)

// The field is small and the only thing a user types into it is a number, so
// the reading rules are worth pinning down: a bare number is seconds, a
// trailing "s" is tolerated, and everything else is refused with a sentence
// rather than being guessed at.
func TestTheSeekFieldReadsSeconds(t *testing.T) {
	cases := []struct {
		text string
		want time.Duration
	}{
		{"0", 0},
		{"3.5", 3500 * time.Millisecond},
		{"12", 12 * time.Second},
		{" 12s ", 12 * time.Second},
	}
	for _, c := range cases {
		got, err := parseSeekOffset(c.text)
		if err != nil {
			t.Fatalf("parseSeekOffset(%q): %v", c.text, err)
		}
		if got != c.want {
			t.Fatalf("parseSeekOffset(%q) = %s, want %s", c.text, got, c.want)
		}
	}
	for _, text := range []string{"", "   ", "abc", "NaN", "Inf", "1,5"} {
		_, err := parseSeekOffset(text)
		if err == nil {
			t.Fatalf("parseSeekOffset(%q) was accepted", text)
		}
		if !strings.Contains(err.Error(), "is not a number of seconds") && !strings.Contains(err.Error(), "type how many seconds") {
			t.Fatalf("the refusal for %q should say what is wrong: %v", text, err)
		}
	}
}

// A second request while one transaction is preparing is refused before the
// window or the field is even read: a disabled button is a courtesy, not the
// only guard.
func TestTheEntryRefusesWhileAReplayIsPreparing(t *testing.T) {
	a := &appState{seek: &seekTxn{}, seekText: "0"}
	got := a.requestReplaySeek()
	if !strings.Contains(got, "already being prepared") {
		t.Fatalf("a second request was not refused: %q", got)
	}
}

// A source that is not buffering cannot take part, and the entry says so
// instead of offering a window that only some sources could honour.
func TestTheEntryNamesTheSourceThatCannotTakePart(t *testing.T) {
	a := &appState{items: []*item{{}}}
	got := a.requestReplaySeek()
	if !strings.Contains(got, "is not buffering") {
		t.Fatalf("the entry should name what is missing: %q", got)
	}
}
