package main

import (
	"errors"
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

	bufferDir := a.dvrDirFor("contract")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, a.dvrRecordingsDirFor("contract"), dvrRetention, dvrMaxBytes, dvrRecordingBytes, it.source.ID))
	if err != nil {
		logf(scenario, "open the buffer: %v", err)
		return
	}
	a.trackRunner(it.uri, runner)
	defer func() {
		mygoRunOnMain(func() {
			// release closes the buffer after the players and their leases, so
			// the recording is not deleted while anything still reads it.
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

	// The buffer's clock is its own. This fixture's playlist begins at media
	// zero and the buffer stored its first segment, so buffer zero is media zero
	// here; the burnt-in timecode is what confirms the claim afterwards. A live
	// buffer that joined mid-stream must measure its anchor instead.
	anchor := dvr.Anchor{Source: it.source.ID, BufferStart: 0, MediaStart: 0}
	logf(scenario, "anchor for %s: buffer %s is media %s", anchor.Source,
		anchor.BufferStart, anchor.MediaStart)

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
	at, err := mid.SourceMediaTime(inside, anchor)
	if err != nil {
		logf(scenario, "media time at file position %s: %v", inside, err)
		return
	}
	event, err := mid.EventTime(inside, anchor, 0)
	if err != nil {
		logf(scenario, "event time at file position %s: %v", inside, err)
		return
	}
	logf(scenario, "mid-segment: asked %s · file starts %s · request sits at %s · media time %s · event time %s (offset 0)",
		mid.RequestedBufferTime, mid.StartBufferTime, inside, at, event)
	if at != request || event != request {
		logf(scenario, "mid-segment: the mapping disagrees with the request (%s, %s vs %s)", at, event, request)
	}
	// Core fixes media = event + offset, so a positive offset pulls event time
	// back: 3s of media at +0.5s is 2.5s on the shared clock.
	for _, offset := range []time.Duration{500 * time.Millisecond, -2 * time.Second} {
		shifted, err := mid.EventTime(inside, anchor, offset)
		if err != nil {
			logf(scenario, "event time with offset %s: %v", offset, err)
			continue
		}
		logf(scenario, "offset %+dms: media %s -> event %s", offset.Milliseconds(), at, shifted)
	}
	if _, err := mid.SourceMediaTime(0, dvr.Anchor{}); err == nil {
		logf(scenario, "a recording without an anchor was mapped anyway, which it must not be")
	} else {
		logf(scenario, "no anchor: %v (ErrNoAnchor=%v)", err, errors.Is(err, dvr.ErrNoAnchor))
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
				edge.RequestedBufferTime, edge.StartBufferTime,
				edge.RequestedBufferTime == edge.StartBufferTime)
			logf(scenario, "boundary: a second cut published %s beside %s", edge.ID, mid.ID)
			keptEdge := filepath.Join(dir, "contract-edge.ts")
			if err := copyFile(edge.Path, keptEdge); err != nil {
				logf(scenario, "keep the boundary recording: %v", err)
			} else {
				logf(scenario, "kept the boundary recording at %s", keptEdge)
			}
		}
		// A published recording is immutable: the second cut must leave the
		// first one intact for whoever already has it open.
		logf(scenario, "the earlier mid recording after a second cut: %v", mid.Valid())
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
	return r.String()
}
