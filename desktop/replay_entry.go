package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// replayWindowForEntry is the window the inspector offers a target from. It is
// the same intersection commonReplay computes, narrowed to the sources that can
// take part: a source without a buffer, or one still showing live, makes the
// whole answer refuse rather than shrinking the window behind the user's back.
//
// The window is in event time, which is what a seek transaction asks for; the
// per-source conversion to a player position stays inside the transaction.
func (a *appState) replayWindowForEntry() (start, end time.Duration, err error) {
	if len(a.items) == 0 {
		return 0, 0, fmt.Errorf("no source is in the session")
	}
	sources := make([]replaySource, 0, len(a.items))
	for index, it := range a.items {
		runner := a.runnerFor(it)
		if runner == nil {
			return 0, 0, fmt.Errorf("%s is not buffering, so it cannot be seeked", a.name(it))
		}
		if it.replay == nil {
			return 0, 0, fmt.Errorf("%s is live; only sources showing a recording can be seeked together", a.name(it))
		}
		sources = append(sources, replaySource{index: index, runner: runner})
	}
	return a.commonReplay(sources)
}

// parseSeekOffset reads how far into the window to go. A bare number is
// seconds, and a trailing "s" is accepted because the field is small and the
// habit of typing one is common.
func parseSeekOffset(text string) (time.Duration, error) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimSuffix(trimmed, "s")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return 0, fmt.Errorf("type how many seconds into the window to go")
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%q is not a number of seconds", text)
	}
	return time.Duration(value * float64(time.Second)), nil
}

// requestReplaySeek is what the inspector's Seek button calls. It checks the
// window, converts the typed offset into an event time, and hands the request to
// the transaction the rest of the phase already uses: the authorisation, the
// identity check at commit and the account all stay where they are.
//
// A request made while another transaction is preparing is refused here as well
// as by startSeekTxn, so a disabled button is a courtesy rather than the only
// guard.
func (a *appState) requestReplaySeek() string {
	a.seekMu.Lock()
	busy := a.seek != nil
	a.seekMu.Unlock()
	if busy {
		return "a replay is already being prepared; wait for it to finish"
	}
	start, end, err := a.replayWindowForEntry()
	if err != nil {
		return err.Error()
	}
	offset, err := parseSeekOffset(a.seekText)
	if err != nil {
		return err.Error()
	}
	target := start + offset
	if target < start || target >= end {
		return fmt.Sprintf("%s is outside the window %s..%s", formatEvent(target), formatEvent(start), formatEvent(end))
	}
	if err := a.startSeekTxn(a.items, target); err != nil {
		return err.Error()
	}
	return fmt.Sprintf("asked every replaying source for %s", formatEvent(target))
}
