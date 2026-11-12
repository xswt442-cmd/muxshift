package dvr

// CleanupResult is what one pass over a buffer's recordings did. Every recording
// the pass left on disk comes back in Kept with the reason it stayed: a caller
// that shows "cleaned" while a file is still there would be lying.
type CleanupResult struct {
	// Removed lists the recordings whose files are gone.
	Removed []Recording
	// Kept lists the recordings that stayed, and why.
	Kept []KeptRecording
	// Freed is the number of bytes the pass removed from the disk budget.
	Freed int64
}

// KeptRecording is a recording a cleanup pass left alone.
type KeptRecording struct {
	Recording Recording
	// Leased is true when a player still holds the recording, which is the only
	// reason the buffer itself refuses to delete one.
	Leased bool
	// Reason says why the file is still there, in one line.
	Reason string
}

// DropUnleased removes the published recordings no lease holds. It never takes a
// file a player is reading: the buffer's own lease count decides, so a recording
// that is still handed to a player is reported in Kept rather than deleted.
//
// A file the system refuses to remove (Windows keeps a handle on a file another
// process still reads) is reported in Kept with the reason and listed by
// PendingCleanup, so the caller can say what happened instead of claiming space
// that did not come back.
func (b *Buffer) DropUnleased() CleanupResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result CleanupResult
	for i := range b.recordings {
		rec := &b.recordings[i]
		if rec.gone {
			continue
		}
		if rec.refs > 0 {
			result.Kept = append(result.Kept, KeptRecording{
				Recording: rec.Recording,
				Leased:    true,
				Reason:    "a player still holds it",
			})
			continue
		}
		before := b.recordingBytes
		b.removeRecordingLocked(i)
		if rec.gone {
			result.Removed = append(result.Removed, rec.Recording)
			result.Freed += before - b.recordingBytes
			continue
		}
		reason := "the file could not be removed"
		if b.lastRemoveErr != nil {
			reason = b.lastRemoveErr.Error()
		}
		result.Kept = append(result.Kept, KeptRecording{Recording: rec.Recording, Reason: reason})
	}
	return result
}
