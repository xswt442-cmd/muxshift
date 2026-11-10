package main

import (
	"errors"
	"testing"
	"time"
)

func TestMediaFromEventMovesByTheOffset(t *testing.T) {
	cases := []struct {
		name   string
		event  time.Duration
		offset time.Duration
		want   time.Duration
	}{
		{"on the event", 10 * time.Second, 0, 10 * time.Second},
		{"content ahead", 10 * time.Second, -2 * time.Second, 8 * time.Second},
		{"content behind", 10 * time.Second, 3 * time.Second, 13 * time.Second},
		{"at the origin", 0, 0, 0},
	}
	for _, test := range cases {
		got, err := mediaFromEvent(test.event, test.offset)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if got != test.want {
			t.Fatalf("%s: %s, want %s", test.name, got, test.want)
		}
	}
}

func TestMediaFromEventRefusesBeforeTheStart(t *testing.T) {
	_, err := mediaFromEvent(time.Second, -2*time.Second)
	if !errors.Is(err, errBeforeStart) {
		t.Fatalf("err = %v, want the before-start refusal", err)
	}
}

func TestMediaFromEventRefusesOverflow(t *testing.T) {
	_, err := mediaFromEvent(time.Duration(1<<62), time.Duration(1<<62))
	if err == nil {
		t.Fatal("an overflowing target must be refused")
	}
}

func TestEventFromMediaIsTheInverse(t *testing.T) {
	media, err := mediaFromEvent(9*time.Second, -2*time.Second)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	event, err := eventFromMedia(media, -2*time.Second)
	if err != nil {
		t.Fatalf("backward: %v", err)
	}
	if event != 9*time.Second {
		t.Fatalf("round trip = %s, want 9s", event)
	}
}

func TestEventFromMediaKeepsNegativeMediaOnTheClock(t *testing.T) {
	// A window that starts before the media origin is still a real window on a
	// stream that began earlier; the conversion reports where it sits rather
	// than hiding the range.
	event, err := eventFromMedia(-time.Second, -3*time.Second)
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if event != 2*time.Second {
		t.Fatalf("event = %s, want 2s", event)
	}
}
