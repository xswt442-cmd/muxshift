// Package core is the shared Go boundary for MuxShift.
//
// It holds a Session's configuration: the sources MuxShift reads, the tracks
// it finds in them, the sync groups that bind tracks to one event clock, the
// scenes that decide what is shown and heard, and the outputs that hand a
// scene to a capability. It holds no decoder, no player and no playback
// state; a media layer reports positions and buffering to it separately.
package core
