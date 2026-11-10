package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core/dvr"
)

// replaySwap is one attempt to put a recording of a source on the stage.
//
// It owns the replacement player and the lease on the file that player reads.
// Until the attempt commits, the item keeps drawing the player it had and keeps
// its own lease: a failed swap can therefore give everything it took back and
// leave the picture, the address and the lease exactly as they were. The lease
// is what keeps the buffer from deleting the file while a player reads it, and
// it is acquired in the same buffer lock that publishes the file
// (dvr.Buffer.RecordAndLease), so no other cut can take the new file between
// the moment it appears and the moment this player holds it.
type replaySwap struct {
	item       *item
	recording  dvr.Recording
	lease      *dvr.Lease
	player     *ui.VideoPlayer
	generation int

	// finished makes the player and its lease be closed and given back by
	// exactly one path: a rollback, or a close of the item while the attempt was
	// still waiting. Whichever runs first owns them; the other finds the work
	// done and does not touch the lease counters again.
	finished sync.Once
}

// playRecording cuts the buffer at from and starts a replacement player on the
// new file, without touching what the item shows. The switch happens in
// settleReplay once that player has a picture.
//
// The recording belongs to the source the buffer was started for; the item keeps
// its own address and Core source. The item records the file and its window in
// replay, so the window and the logs can say "this is history of source 1"
// rather than pretending the source's address changed.
func (a *appState) playRecording(it *item, runner *dvrRunner, from time.Duration) error {
	if runner == nil {
		return fmt.Errorf("there is no replay buffer to cut from")
	}
	if it.source.ID != runner.source {
		return fmt.Errorf("the buffer belongs to %s, not to %s", runner.source, it.source.ID)
	}
	recording, lease, err := runner.recordAndHold(from)
	if err != nil {
		return fmt.Errorf("cut %s: %w", shortDuration(from), err)
	}
	player, err := a.openReplayFile(recording.Path)
	if err != nil {
		_ = lease.Release()
		return fmt.Errorf("open the recording %s: %w", recording.FileName(), err)
	}
	a.tookLease()
	swap := &replaySwap{
		item:       it,
		recording:  recording,
		lease:      lease,
		player:     player,
		generation: it.generation,
	}
	mygoRunOnMain(func() { it.swap = swap })
	logf("replay", "waiting for %s to show a picture (generation %d, %s)", recording.FileName(), swap.generation, shortURI(it.uri))
	go a.settleReplay(swap)
	return nil
}

// settleReplay waits for the replacement to show a picture and then commits the
// switch, or rolls the whole attempt back. It never blocks the drawing thread:
// the two things it has to look at on that thread, the item's generation and the
// player's state, are read in short main-thread calls.
func (a *appState) settleReplay(swap *replaySwap) {
	deadline := time.Now().Add(handoverWait)
	for {
		var current, started bool
		mygoRunOnMain(func() {
			current = swap.item.swap == swap && swap.item.generation == swap.generation
			if current {
				started = a.replayIsStarted(swap.player)
			}
		})
		if !current {
			a.abandonReplay(swap, "a newer switch took the item over")
			return
		}
		if started {
			a.commitReplay(swap)
			return
		}
		if !time.Now().Before(deadline) {
			a.abandonReplay(swap, "the recording never showed a picture")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// commitReplay makes the replacement the item's player. It runs in one
// main-thread step: from the commit on, the item draws the new player, keeps the
// new lease and remembers which recording it is showing; the old player and the
// old lease are released after it, in that order, so the picture a viewer sees
// is never left without a player behind it.
func (a *appState) commitReplay(swap *replaySwap) {
	it := swap.item
	var (
		old      desktopPlayer
		oldLease *dvr.Lease
		ok       bool
	)
	mygoRunOnMain(func() {
		if it.swap != swap || it.generation != swap.generation {
			return
		}
		old = it.player
		oldLease = it.replayLease
		it.swap = nil
		it.player = swap.player
		it.replayLease = swap.lease
		recording := swap.recording
		it.replay = &recording
		it.generation++
		ok = true
		logf("replay", "committed %s on %s: shows %s..%s of buffer time",
			recording.FileName(), shortURI(it.uri), shortDuration(recording.StartBufferTime), shortDuration(recording.EndBufferTime))
	})
	if !ok {
		a.abandonReplay(swap, "the item changed while the switch was committing")
		return
	}
	if old != nil {
		a.releasePlayer(rawPlayer(old))
	}
	if oldLease != nil {
		if err := oldLease.Release(); err != nil {
			logf("replay", "release the lease of the previous recording: %v", err)
		}
		a.leftLease()
	}
}

// abandonReplay gives back everything a failed or superseded attempt took: the
// item keeps whatever it was showing and the lease that protects it, and the
// replacement player and its lease are released here.
func (a *appState) abandonReplay(swap *replaySwap, why string) {
	it := swap.item
	mygoRunOnMain(func() {
		if it.swap == swap {
			it.swap = nil
		}
	})
	a.finishSwap(swap)
	logf("replay", "rolled back the switch to %s: %s", swap.recording.FileName(), why)
}

// finishSwap closes the replacement player and gives its lease back once. A
// close of the item and a rollback can both end the same attempt; the lease
// counters must still move exactly once.
func (a *appState) finishSwap(swap *replaySwap) {
	swap.finished.Do(func() {
		a.releasePlayer(swap.player)
		if err := swap.lease.Release(); err != nil {
			logf("replay", "release the lease of %s: %v", swap.recording.FileName(), err)
		}
		a.leftLease()
		logf("replay", "finished %s: the replacement player and its lease went back once", swap.recording.FileName())
	})
}

// openReplayFile opens the file a recording published. The scenario seam exists
// so a run can stand in for a player that cannot open a file at all; production
// leaves it nil and opens the recording the normal way, on the drawing thread.
func (a *appState) openReplayFile(path string) (*ui.VideoPlayer, error) {
	if a.openReplay != nil {
		return a.openReplay(path)
	}
	return openPlayerOnMain(path)
}

// replayStarted reports whether a replacement player has a picture. The scenario
// seam stands in for a player that never gets one; production asks the player.
func (a *appState) replayIsStarted(player *ui.VideoPlayer) bool {
	if a.replayStarted != nil {
		return a.replayStarted(player)
	}
	return player.Ready() && !player.Buffering()
}

// shortDuration renders a buffer time the way the logs and the window show it.
func shortDuration(at time.Duration) string {
	if at < 0 {
		at = 0
	}
	minutes := int(at / time.Minute)
	seconds := int((at % time.Minute) / time.Second)
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// replayLine is the one-line description of the recording an item shows, for the
// window and for the logs.
func replayLine(it *item) string {
	if it.replay == nil {
		return ""
	}
	return fmt.Sprintf("%s · replay %s..%s of %s",
		filepath.Base(it.replay.Path),
		shortDuration(it.replay.StartBufferTime),
		shortDuration(it.replay.EndBufferTime),
		shortURI(it.uri))
}

// tookLease records a lease the replay transaction now owns.
func (a *appState) tookLease() {
	a.leaseMu.Lock()
	a.leasesOpen++
	a.leasesGiven++
	a.leaseMu.Unlock()
}

// leftLease records a lease given back, whether the switch committed or rolled
// back.
func (a *appState) leftLease() {
	a.leaseMu.Lock()
	if a.leasesOpen > 0 {
		a.leasesOpen--
	}
	a.leaseMu.Unlock()
}

// leaseStats reports how many leases are open now and how many were taken over
// the whole run, so a scenario can show nothing is left holding a file.
func (a *appState) leaseStats() (open, given int) {
	a.leaseMu.Lock()
	defer a.leaseMu.Unlock()
	return a.leasesOpen, a.leasesGiven
}
