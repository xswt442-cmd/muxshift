package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// dvrUsage is what the diagnostics page reports about one replay buffer: what is
// on disk now, as opposed to the limits the settings allow. The two are stated
// apart on purpose, because a budget of 256 MiB says nothing about how much this
// session actually took.
type dvrUsage struct {
	bufferBytes    int64
	segments       int
	recordings     int
	recordingBytes int64
	pending        int
	// readErr is the playlist read that failed last, if the source is out of
	// reach right now. It is cleared by the next read that works, so the page
	// claims an outage only while there is one.
	readErr string
}

// usage reads the buffer's own accounting rather than walking the directory: the
// buffer already knows which segments and recordings it owns, and a second
// opinion on file ownership is the thing this stage is not supposed to add.
func (r *dvrRunner) usage() dvrUsage {
	r.mu.Lock()
	readErr := r.lastErr
	r.mu.Unlock()
	usage := dvrUsage{
		bufferBytes:    r.buffer.Bytes(),
		segments:       len(r.buffer.Segments()),
		recordings:     len(r.buffer.Recordings()),
		recordingBytes: r.buffer.RecordingBytes(),
		pending:        len(r.buffer.PendingCleanup()),
	}
	if readErr != nil {
		usage.readErr = readErr.Error()
	}
	return usage
}

// usageLine states one buffer's live numbers in a row the diagnostics page can
// print. The instance is named by a short digest: it tells one buffer from the
// next without filling the row with a timestamp.
func (r *dvrRunner) usageLine(uri string) string {
	usage := r.usage()
	line := fmt.Sprintf("%s · cache %s in %d segment(s) · recordings %d (%s) · pending cleanup %d · buffer %s",
		shortURI(uri), byteLine(usage.bufferBytes), usage.segments,
		usage.recordings, byteLine(usage.recordingBytes), usage.pending, instanceName(r.buffer.Instance()))
	if usage.readErr != "" {
		line += " · the source cannot be read: " + usage.readErr
	}
	return line
}

// instanceName shortens a buffer instance to something a row can carry. The head
// is the moment the buffer was made, which no reader can compare by eye, so a
// digest of the whole id goes with it: two buffers made in the same second would
// otherwise shorten to the same name and the page would claim they are one.
func instanceName(instance string) string {
	if instance == "" {
		return "buffer"
	}
	sum := sha256.Sum256([]byte(instance))
	digest := hex.EncodeToString(sum[:])[:4]
	head := instance
	const readable = 12
	if len(head) > readable {
		head = head[:readable]
	}
	return head + "-" + digest
}

// dvrCleanup is what one pass over a buffer did, in the form the page and the log
// both use.
type dvrCleanup struct {
	removed int
	freed   int64
	kept    []string
}

// cleanupRecordings removes the recordings nothing is reading. Files the system
// would not release earlier are retried first; files a player holds stay, and the
// result says so instead of counting them as freed.
//
// The bytes freed are measured before and after the pass rather than summed from
// the removal list: a retried file leaves the budget without passing through the
// list, and a page that says "freed" must mean the number on disk went down.
func (r *dvrRunner) cleanupRecordings() dvrCleanup {
	before := r.buffer.RecordingBytes()
	beforeCount := len(r.buffer.Recordings())
	left := r.buffer.RetryCleanup()
	result := r.buffer.DropUnleased()
	cleanup := dvrCleanup{
		removed: beforeCount - len(r.buffer.Recordings()),
		freed:   before - r.buffer.RecordingBytes(),
	}
	seen := map[string]bool{}
	for _, kept := range result.Kept {
		name := kept.Recording.FileName()
		seen[name] = true
		cleanup.kept = append(cleanup.kept, fmt.Sprintf("%s: %s", name, kept.Reason))
	}
	for _, path := range left {
		name := filepath.Base(path)
		if seen[name] {
			continue
		}
		seen[name] = true
		cleanup.kept = append(cleanup.kept, fmt.Sprintf("%s: the system still holds the file", name))
	}
	return cleanup
}

// line summarises a pass in one sentence.
func (c dvrCleanup) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "removed %d recording(s), %s freed", c.removed, byteLine(c.freed))
	if len(c.kept) > 0 {
		fmt.Fprintf(&b, "; kept %d (%s)", len(c.kept), strings.Join(c.kept, "; "))
	}
	return b.String()
}

// cleanupDVR runs one pass over every buffer this run opened and records the
// outcome. It never closes or clears a buffer: the files a session still owns
// stay its own until the session ends.
func (a *appState) cleanupDVR() dvrCleanup {
	total := dvrCleanup{}
	for _, tracked := range a.runners {
		one := tracked.runner.cleanupRecordings()
		total.removed += one.removed
		total.freed += one.freed
		total.kept = append(total.kept, one.kept...)
	}
	a.dvrNote = total.line()
	log.Printf("dvr: cleanup: %s", a.dvrNote)
	return total
}

// budgetLine states the limits in force and where they came from, so the page can
// show them next to the live numbers without either being mistaken for the other.
func (a *appState) budgetLine() string {
	return fmt.Sprintf("limits in force: %s (from %s) — a limit, not what is in use", a.budget.line(), a.budgetFrom)
}

// dvrRows builds the diagnostics lines for every buffer this run opened.
func (a *appState) dvrRows() []string {
	rows := make([]string, 0, len(a.runners))
	for _, tracked := range a.runners {
		rows = append(rows, tracked.runner.usageLine(tracked.uri))
	}
	return rows
}

// byteLine writes a byte count the way the rest of the interface does: whole
// numbers for small sizes, one decimal for the units above.
func byteLine(bytes int64) string {
	switch {
	case bytes == 0:
		return "0 bytes"
	case bytes < 1024:
		return fmt.Sprintf("%d bytes", bytes)
	case bytes < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
	case bytes < 1<<30:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
	default:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
	}
}

// recordingLine names one published recording, used by the checks that have to
// tell a held file from a dropped one.
func recordingLine(recording dvr.Recording) string {
	return fmt.Sprintf("%s (%s)", recording.FileName(), byteLine(recording.Size))
}
