// Package hls reads HLS media input for the Core session model.
//
// It exposes two things: the media playlist an address publishes, parsed by
// bluenviron/gohlslib rather than a local parser, and the tracks that playlist
// actually carries together with the first timestamp of each. Neither reading
// downloads more than the first samples, converts media, or serves media
// onward. Playback stays with the client's media engine: this package hands the
// URL and the real time information to Core and nothing else.
//
// A media playlist is the only input Read accepts. A multivariant (master)
// playlist is rejected there because choosing a variant is the client's
// decision; Probe accepts one because gohlslib picks the default variant the
// same way it will when playback starts.
package hls
