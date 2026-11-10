package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// runLease proves the ownership rule the DVR has to keep: once a recording is
// handed to a consumer, no new cut, segment eviction, buffer close or budget
// cleanup may make it fail until that consumer has really let it go.
//
// The consumer here is a real file handle held open the whole time, which is what
// Windows refuses to delete, plus a decode of the recording after the close to
// show the bytes a player would have read were never touched. The desktop replay
// player cannot stand in for that consumer yet: a recording opened on a card
// while the session runs reports playing but presents no picture (P8), so this
// check states file custody rather than claiming a picture.
func runLease(window *mygo.Window, a *appState, dir string) {
	const scenario = "lease"
	it, err := a.at(0)
	if err != nil {
		logf(scenario, "%v", err)
		return
	}
	if !isNetworkAddress(it.uri) {
		logf(scenario, "needs a network source to buffer, got %s", shortURI(it.uri))
		return
	}

	// The budget holds two of this fixture's snapshots (2 MiB each), so every cut
	// after the second has to drop an unleased one to make room.
	const budget = int64(5 << 20)
	bufferDir := filepath.Join(dir, "buffer")
	recordingsDir := filepath.Join(dir, "recordings")
	runner, err := startDVR(it.uri, dvrFromMedia(bufferDir, recordingsDir, 20*time.Second, 0, budget, it.source.ID))
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

	windowOf, ok := waitForRange(runner, 8*time.Second, 45*time.Second)
	if !ok {
		logf(scenario, "the buffer did not hold 8s of media within 45s")
		return
	}
	logf(scenario, "the buffer holds %s", logRange(windowOf))

	recording, leaseErr := cutAndHold(runner, windowOf.Start)
	if leaseErr != nil {
		logf(scenario, "cut the recording: %v", leaseErr)
		return
	}
	logf(scenario, "leased %s (%d bytes) before handing it to a consumer", recording.FileName(), recording.Size)
	logRecordings(scenario, "after the cut", bufferDir, recordingsDir)

	// The consumer: an open handle the process keeps for the whole check, which is
	// exactly the state Windows refuses to delete and the buffer must respect.
	held, err := os.Open(recording.Path)
	if err != nil {
		logf(scenario, "open the recording as a consumer: %v", err)
		return
	}
	defer held.Close()

	// Every further cut has to make room by dropping an unleased snapshot; the
	// leased one must survive each of them.
	for i := 1; i <= 3; i++ {
		var cut dvr.Recording
		var cutErr error
		mygoRunOnMain(func() {
			cut, cutErr = runner.replay(windowOf.Start + time.Duration(i)*time.Second)
		})
		if cutErr != nil {
			logf(scenario, "cut %d: %v", i, cutErr)
			continue
		}
		logf(scenario, "cut %d: %s (%d bytes); the leased file is still valid: %v",
			i, cut.FileName(), cut.Size, recording.Valid())
		logf(scenario, "budget: %s", runner.budgetLine())
	}
	logRecordings(scenario, "after three cuts", bufferDir, recordingsDir)

	// Closing the buffer with the lease held is the strongest case: the segments
	// go away, the recording the consumer reads does not.
	var closeErr error
	mygoRunOnMain(func() { closeErr = runner.Close() })
	logf(scenario, "closed the buffer while the lease was held: %v", closeErr)
	logf(scenario, "after the close the leased file is still valid: %v", recording.Valid())
	logRecordings(scenario, "after the close", bufferDir, recordingsDir)

	// A decode of the file the consumer still holds shows the bytes survived: the
	// segments the buffer evicted and the close did not cut the file short.
	if err := decodeRecording(recording); err != nil {
		logf(scenario, "decoding the leased recording: %v", err)
	} else {
		logf(scenario, "the leased recording still decodes after the buffer closed")
	}

	// The consumer lets go first; only then may the buffer take the file back.
	if err := held.Close(); err != nil {
		logf(scenario, "close the consumer handle: %v", err)
	}
	pending, releaseErr := runner.releaseHold()
	logf(scenario, "released the lease after the consumer closed: err=%v pending=%v", releaseErr, pending)
	if err := recording.Valid(); err == nil {
		logf(scenario, "the recording outlived its release: Valid still passes")
	} else {
		logf(scenario, "the recording is gone as soon as the consumer let it go: %v", err)
	}
	logRecordings(scenario, "after the release", bufferDir, recordingsDir)

	// A budget that cannot hold a cut is refused instead of taking space from a
	// file somebody is already reading.
	tinyDir := filepath.Join(dir, "tiny")
	tinyRecordings := filepath.Join(dir, "tiny-recordings")
	tiny, err := startDVR(it.uri, dvrFromMedia(tinyDir, tinyRecordings, 20*time.Second, 0, 1, it.source.ID))
	if err != nil {
		logf(scenario, "open the tiny buffer: %v", err)
		return
	}
	defer func() { _ = tiny.Close() }()
	if windowOf, ok := waitForRange(tiny, 4*time.Second, 45*time.Second); ok {
		var refused error
		mygoRunOnMain(func() {
			_, refused = tiny.replay(windowOf.Start)
		})
		logf(scenario, "a one-byte budget refuses the cut: %v (ErrNoSpace=%v)",
			refused, errors.Is(refused, dvr.ErrNoSpace))
	}
}

// cutAndHold cuts a recording at from and leases it on the drawing thread, where
// the buffer's owner runs.
func cutAndHold(runner *dvrRunner, from time.Duration) (dvr.Recording, error) {
	var recording dvr.Recording
	var err error
	mygoRunOnMain(func() {
		recording, err = runner.replay(from)
		if err != nil {
			return
		}
		if _, leaseErr := runner.hold(recording.ID); leaseErr != nil {
			err = fmt.Errorf("lease %s: %w", recording.ID, leaseErr)
		}
	})
	return recording, err
}

// decodeRecording runs ffmpeg over the recording and reports whether it decodes
// at all, so a file that was emptied or half-deleted cannot pass as valid.
func decodeRecording(recording dvr.Recording) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not on PATH: %w", err)
	}
	out, err := exec.Command(ffmpeg, "-v", "error", "-i", recording.Path,
		"-f", "null", "-").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, firstLine(string(out)))
	}
	return nil
}

// firstLine trims a tool's output to the first non-empty line.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// logRecordings states what the buffer directory holds at this point, so the run
// shows the files as well as what the buffer says about them.
func logRecordings(scenario, when, dir, recordingsDir string) {
	logDir(scenario, when+" segments", dir)
	logDir(scenario, when+" recordings", recordingsDir)
}

func logDir(scenario, when, dir string) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		logf(scenario, "%s: %s is gone", when, filepath.Base(dir))
		return
	}
	if err != nil {
		logf(scenario, "%s: read %s: %v", when, dir, err)
		return
	}
	var names []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		names = append(names, fmt.Sprintf("%s %d", entry.Name(), info.Size()))
	}
	if len(names) == 0 {
		logf(scenario, "%s: %s holds nothing", when, filepath.Base(dir))
		return
	}
	logf(scenario, "%s: %s holds %s", when, filepath.Base(dir), strings.Join(names, ", "))
}
