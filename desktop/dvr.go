package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/dvr"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// dvrRunner polls one live source's playlist into a bounded buffer and turns
// the buffered part into a playlist a player opens from disk.
//
// The buffer is opened as a local file rather than through a loopback endpoint:
// Media Foundation read the playlist and the segments from a loopback endpoint
// but never presented a picture, while the same recording opened from the file
// system plays. The cache directory therefore has to stay on this machine, which
// it does: it is created under the configured directory and removed on Close.
type dvrRunner struct {
	uri    string
	buffer *dvr.Buffer
	client *http.Client

	mu      sync.Mutex
	next    int
	started bool
	lastErr error
	polls   int
	// budget is the byte limit the recordings may take, kept for reporting.
	budget int64
	// rec is the last recording this runner cut. It carries the media-time
	// contract that ties a position in the file to the source's clock.
	rec dvr.Recording
	// lease keeps the recording a player is reading from being dropped. The
	// player takes it when it opens the file and gives it back when it closes.
	lease *dvr.Lease

	stop chan struct{}
	done chan struct{}
	// closeOnce makes Close idempotent: the runner is closed both when a scenario
	// finishes the buffer early and when the session shuts down.
	closeOnce sync.Once
	closeErr  error
}

// startDVR opens a buffer for one source and starts polling its playlist.
func startDVR(uri string, opts dvr.Options) (*dvrRunner, error) {
	buffer, err := dvr.New(opts)
	if err != nil {
		return nil, err
	}
	runner := &dvrRunner{
		uri:    uri,
		buffer: buffer,
		client: &http.Client{Timeout: 10 * time.Second},
		budget: opts.RecordingBytes,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go runner.poll()
	log.Printf("dvr: %s buffers into %s", shortURI(uri), opts.Dir)
	return runner, nil
}

// poll reads the playlist once a second and stores every segment the buffer has
// not seen yet. Reading continues while the picture is paused: the two are
// separate states, and the window only helps if it keeps growing while output
// is stopped.
func (r *dvrRunner) poll() {
	defer close(r.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.pollOnce()
		}
	}
}

// pollOnce advances the buffer by one playlist read.
func (r *dvrRunner) pollOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := hls.Read(ctx, r.uri, hls.Options{})
	if err != nil {
		r.mu.Lock()
		r.lastErr = err
		r.mu.Unlock()
		log.Printf("dvr: read playlist of %s: %v", shortURI(r.uri), err)
		return
	}
	r.mu.Lock()
	r.polls++
	next := r.next
	r.mu.Unlock()
	added := 0
	for _, segment := range info.Segments {
		r.mu.Lock()
		started, next := r.started, r.next
		r.mu.Unlock()
		if started && segment.Sequence < next {
			continue
		}
		if segment.Gap {
			continue
		}
		if _, err := r.buffer.Add(ctx, r.client, segment); err != nil {
			log.Printf("dvr: %s: %v", shortURI(r.uri), err)
		}
		added++
		r.mu.Lock()
		r.started = true
		r.next = segment.Sequence + 1
		r.mu.Unlock()
	}
	if added == 0 && len(info.Segments) > 0 {
		log.Printf("dvr: %s: poll %d added nothing: %d segments %d..%d, next %d",
			shortURI(r.uri), next, len(info.Segments),
			info.Segments[0].Sequence, info.Segments[len(info.Segments)-1].Sequence, r.next)
	}
	removed := r.buffer.Evict(func(segment dvr.Segment) bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		// The newest segment is what the live edge will play next. Older
		// segments stay until the retention or byte limit reaches them, so a
		// replay of the buffered window still finds its files.
		return segment.Sequence == r.next-1
	})
	if len(removed) > 0 {
		log.Printf("dvr: %s evicted segments %v", shortURI(r.uri), removed)
	}
}

// state is what the window currently holds, for the status line and the log.
func (r *dvrRunner) state() string {
	ranges := r.buffer.Ranges()
	window := "empty"
	if len(ranges) > 0 {
		parts := make([]string, 0, len(ranges))
		for _, current := range ranges {
			parts = append(parts, current.String())
		}
		window = strings.Join(parts, " + ")
	}
	r.mu.Lock()
	polls, lastErr := r.polls, r.lastErr
	r.mu.Unlock()
	state := fmt.Sprintf("dvr %s · %s · %d bytes · %d polls",
		shortURI(r.uri), window, r.buffer.Bytes(), polls)
	if gaps := r.buffer.Gaps(); len(gaps) > 0 {
		reasons := make([]string, 0, len(gaps))
		for _, gap := range gaps {
			reasons = append(reasons, gap.String())
		}
		state += " · " + strings.Join(reasons, "; ")
	}
	if lastErr != nil {
		state += " · last error: " + lastErr.Error()
	}
	return state
}

// replay writes the buffered window that starts at from into the cache
// directory as one new transport stream file and returns the recording, which
// states where the file really starts on the buffer's clock and how a position
// inside it maps back. Each call publishes its own file: a recording a player
// already has open is never replaced. A start the buffer does not hold is
// refused by the buffer, so a replay never begins at a time that was never
// stored.
func (r *dvrRunner) replay(from time.Duration) (dvr.Recording, error) {
	recording, err := r.buffer.Record(from)
	if err != nil {
		return dvr.Recording{}, err
	}
	r.mu.Lock()
	r.rec = recording
	r.mu.Unlock()
	return recording, nil
}

// lastRecording is the most recent recording this runner cut, if any.
func (r *dvrRunner) lastRecording() (dvr.Recording, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec.Path == "" {
		return dvr.Recording{}, false
	}
	return r.rec, true
}

// hold leases the recording with this id so the buffer keeps its bytes while a
// player reads it. A second call releases the lease it replaces.
func (r *dvrRunner) hold(id string) (dvr.Recording, error) {
	lease, err := r.buffer.Lease(id)
	if err != nil {
		return dvr.Recording{}, err
	}
	r.mu.Lock()
	old := r.lease
	r.lease = lease
	r.mu.Unlock()
	if old != nil {
		_ = old.Release()
	}
	return lease.Recording(), nil
}

// releaseHold gives the recording back and reports what the buffer still has to
// clean up, so a file Windows would not release is visible instead of assumed
// deleted.
func (r *dvrRunner) releaseHold() ([]string, error) {
	r.mu.Lock()
	lease := r.lease
	r.lease = nil
	r.mu.Unlock()
	var err error
	if lease != nil {
		err = lease.Release()
	}
	return r.buffer.PendingCleanup(), err
}

// budgetLine states how much of the recording budget is in use and by how many
// snapshots, so a run can show what a new cut had to drop.
func (r *dvrRunner) budgetLine() string {
	limit := "unbounded"
	if r.budget > 0 {
		limit = fmt.Sprintf("%d bytes", r.budget)
	}
	return fmt.Sprintf("%d recording(s) taking %d bytes of %s",
		len(r.buffer.Recordings()), r.buffer.RecordingBytes(), limit)
}

// Close stops polling and removes the cache directory. A recording a lease holds
// stays on disk; its release removes it. Closing twice is harmless: a scenario
// that closes the buffer itself and then releases the session closes it again.
func (r *dvrRunner) Close() error {
	r.closeOnce.Do(func() {
		close(r.stop)
		<-r.done
		r.closeErr = r.buffer.Close()
	})
	return r.closeErr
}

// dvrFromMedia builds the buffer options from the flags that configure it. The
// segments go under dir and the published recordings under recordingsDir, so a
// session can keep the two kinds of file apart; the source names the owner of
// the media times the buffer records, the budget bounds the disk the recordings
// may take, and zero leaves them unbounded.
func dvrFromMedia(dir, recordingsDir string, retention time.Duration, maxBytes, recordingBytes int64, source core.SourceID) dvr.Options {
	return dvr.Options{
		Dir:            dir,
		RecordingsDir:  recordingsDir,
		Retention:      retention,
		MaxBytes:       maxBytes,
		RecordingBytes: recordingBytes,
		Source:         source,
	}
}
