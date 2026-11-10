package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// runContract checks the recording time contract against a source that carries
// a burnt-in timecode: the recording states where its file really starts, a
// position inside the file maps back to the source's media clock, the shared
// event clock follows from a track offset, a time inside a gap is refused, and a
// replaced file stops describing anything.
func runContract(window *mygo.Window, a *appState, dir string) {
	const scenario = "contract"
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source with burnt-in timecode, got %s", shortURI(it.uri))
		return
	}
	step(window, a, dir, scenario, "1-live.png", "the live source before the buffer is cut", nil)

	bufferDir := filepath.Join(dvrDir, "contract")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, dvrRetention, dvrMaxBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	defer func() {
		mygoRunOnMain(func() {
			if err := runner.Close(); err != nil {
				logf(scenario, "close the buffer: %v", err)
			}
			a.release()
			mygo.App.Quit()
		})
	}()

	// A range of six seconds holds a middle segment, a boundary after the
	// request, and enough room to ask for a time outside it.
	chosen, ok := waitForRange(runner, 6*time.Second, 30*time.Second)
	if !ok {
		logf(scenario, "no six-second range arrived within 30s: %s", runner.state())
		return
	}
	logf(scenario, "window %s", runner.state())
	logf(scenario, "range %s picked for the cut", chosen)

	// The request falls inside a segment, so the file cannot start there: a
	// transport stream segment cannot be cut. The recording has to say both the
	// requested time and the first frame it actually holds.
	request := chosen.Start + 3*time.Second
	mid, err := runner.replay(request)
	if err != nil {
		logf(scenario, "mid-segment request %s: %v", request, err)
		return
	}
	logf(scenario, "mid-segment: %s", mid)
	inside, err := mid.Position(request)
	if err != nil {
		logf(scenario, "position of the request: %v", err)
		return
	}
	at, err := mid.SourceMediaTime(inside)
	if err != nil {
		logf(scenario, "media time at file position %s: %v", inside, err)
		return
	}
	event, err := mid.EventTime(inside, 0)
	if err != nil {
		logf(scenario, "event time at file position %s: %v", inside, err)
		return
	}
	logf(scenario, "mid-segment: asked %s · file starts %s · request sits at %s · media time %s · event time %s (offset 0)",
		mid.RequestedMediaTime, mid.ContentStart, inside, at, event)
	if at != request || event != request {
		logf(scenario, "mid-segment: the mapping disagrees with the request (%s, %s vs %s)", at, event, request)
	}
	keptMid := filepath.Join(dir, "contract-mid.ts")
	if err := copyFile(mid.Path, keptMid); err != nil {
		logf(scenario, "keep the mid-segment recording: %v", err)
	} else {
		logf(scenario, "kept the mid-segment recording at %s", keptMid)
	}

	// A request exactly at a segment boundary is the one case where the file
	// does start where it was asked.
	var boundary time.Duration
	for _, segment := range chosen.Segments {
		if segment.Start > request {
			boundary = segment.Start
			break
		}
	}
	if boundary == 0 {
		logf(scenario, "the picked range has no segment after the request, so the boundary case is skipped")
	} else {
		edge, err := runner.replay(boundary)
		if err != nil {
			logf(scenario, "boundary request %s: %v", boundary, err)
		} else {
			logf(scenario, "boundary: %s", edge)
			logf(scenario, "boundary: asks %s and starts %s, coincide=%v",
				edge.RequestedMediaTime, edge.ContentStart, edge.RequestedMediaTime == edge.ContentStart)
			keptEdge := filepath.Join(dir, "contract-edge.ts")
			if err := copyFile(edge.Path, keptEdge); err != nil {
				logf(scenario, "keep the boundary recording: %v", err)
			} else {
				logf(scenario, "kept the boundary recording at %s", keptEdge)
			}
		}
		// Cutting the boundary recording replaced the mid-segment file, so the
		// earlier recording no longer describes anything on disk.
		logf(scenario, "the replaced mid recording: %v", mid.Valid())
	}

	// A time the buffer does not hold is refused instead of being clamped.
	if _, err := runner.replay(chosen.Start - 2*time.Second); err != nil {
		logf(scenario, "outside the window: %s -> %v (ErrNoRange=%v)",
			chosen.Start-2*time.Second, err, errors.Is(err, dvr.ErrNoRange))
	} else {
		logf(scenario, "outside the window was accepted, which it must not be")
	}
}

// waitForRange waits for a stored range at least length long.
func waitForRange(runner *dvrRunner, length, limit time.Duration) (dvr.Range, bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ranges := runner.buffer.Ranges()
		if len(ranges) > 0 {
			last := ranges[len(ranges)-1]
			if last.End-last.Start >= length {
				return last, true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return dvr.Range{}, false
}

// logRange spells a range for the log.
func logRange(r dvr.Range) string {
	return fmt.Sprintf("%s (%d segments)", r.String(), len(r.Segments))
}
