package core

import "errors"

// Errors reported by Session operations. They are wrapped with the operation
// and object they concern, so callers match them with errors.Is.
var (
	// ErrClosed reports a mutation attempted on a closed Session.
	ErrClosed = errors.New("session is closed")
	// ErrNotFound reports an identifier that names no object in this Session.
	ErrNotFound = errors.New("object not found")
	// ErrEmptyURI reports a source without an address.
	ErrEmptyURI = errors.New("source URI is empty")
	// ErrCredentialsInURI reports a source URI that carries user information.
	// Session keeps configuration, never credentials.
	ErrCredentialsInURI = errors.New("source URI carries credentials")
	// ErrMediaKind reports a track whose media kind does not fit the place it
	// is asked to take, as an audio track shown in a scene's video slot.
	ErrMediaKind = errors.New("track media kind does not fit")
	// ErrDuplicateTrack reports the same track listed twice in one operation.
	ErrDuplicateTrack = errors.New("track listed twice")
	// ErrSyncConflict reports a track that already belongs to another
	// SyncGroup. A track is synchronized by at most one group.
	ErrSyncConflict = errors.New("track already belongs to a sync group")
	// ErrNotMember reports a reference track missing from the group's tracks.
	ErrNotMember = errors.New("reference track is not in the group")
	// ErrNotAligned reports a track with no known correspondence between its
	// media time and the shared event timeline. Callers must not guess one.
	ErrNotAligned = errors.New("track is not aligned to the event timeline")
	// ErrTimeRange reports a time outside the range time.Duration can hold.
	ErrTimeRange = errors.New("time is out of range")
	// ErrInvalid reports an argument outside its allowed range.
	ErrInvalid = errors.New("invalid argument")
)
