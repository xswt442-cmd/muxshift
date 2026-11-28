package core

import (
	"fmt"
	"testing"
)

// TestSessionEventsAreCappedAndKeepTheNewest proves the history is bounded and
// that the events kept are the most recent ones, in order.
func TestSessionEventsAreCappedAndKeepTheNewest(t *testing.T) {
	s := NewSession("events")
	total := maxEventHistory + 250
	for i := 0; i < total; i++ {
		if _, err := s.AddSource(fmt.Sprintf("https://example.test/%d/index.m3u8", i)); err != nil {
			t.Fatalf("add source %d: %v", i, err)
		}
	}
	events := s.Events()
	if len(events) != maxEventHistory {
		t.Fatalf("event history length = %d, want %d", len(events), maxEventHistory)
	}
	firstWant := uint64(total - maxEventHistory + 1)
	if events[0].Seq != firstWant {
		t.Fatalf("oldest kept seq = %d, want %d", events[0].Seq, firstWant)
	}
	if events[len(events)-1].Seq != uint64(total) {
		t.Fatalf("newest seq = %d, want %d", events[len(events)-1].Seq, total)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Fatalf("event order broke at %d: %d after %d", i, events[i].Seq, events[i-1].Seq)
		}
	}
}

// TestSessionEventsReturnsACopy proves Events leaves the Session unchanged when
// the caller edits the returned slice.
func TestSessionEventsReturnsACopy(t *testing.T) {
	s := NewSession("copy")
	if _, err := s.AddSource("https://example.test/a/index.m3u8"); err != nil {
		t.Fatal(err)
	}
	events := s.Events()
	events[0].Object = "changed"
	if got := s.Events()[0].Object; got == "changed" {
		t.Fatal("Events returned the Session's own slice; a change leaked back in")
	}
}
