# Architecture

[简体中文](ARCHITECTURE.zh-CN.md)

This repository contains a scaffold with three implemented pieces: a Go Core session configuration model, its event timeline, and HLS playlist metadata reading. Decoding after ingestion, buffering, timing-based synchronization, mixing, and playback are not implemented.

## Modules and ownership

| Directory | Current state | Responsibility boundary |
| --- | --- | --- |
| `core/` | Session configuration model | Shared media-session configuration and control contracts |
| `core/hls/` | HLS metadata reading | Reads playlists, tracks, and first sample times; no transcoding and no serving |
| `desktop/` | Documentation placeholder | MyGo desktop adapter, without shared business logic in the UI |
| `web/` | Documentation placeholder | Browser interface and browser media capability adapters |
| `extension/` | Documentation placeholder | User-authorized integration with an existing browser session |
| `testplace/` | Ignored local directory | Experiments, temporary notes, and agent working material |

The intended client-to-core dependency direction is `desktop → core ← web`; the browser extension integrates through an explicit protocol instead of depending on the desktop UI. These are ownership constraints, not existing runtime calls.

## Core session model

`core.Session` holds configuration only, never playback state. A session owns the sources it reads, the tracks found in them, sync groups that bind tracks to one reference track, scenes that decide what is shown and heard, and outputs that hand a scene to a capability. Each track belongs to at most one sync group. Every operation either applies completely or leaves the session unchanged, a refused operation records no event, and removing a source removes its tracks and every reference to them.

## Core event timeline

`core.Timeline` maps one shared event time to each track's media time with `targetMediaTime = sharedEventTime + mappedOffset`. A positive offset marks a track whose media clock runs ahead, so following it live means holding it back by that offset; a negative offset marks a track that has not reached the current event yet. Setting an offset replaces the previous one, so applying the same calibration twice does not accumulate. A track without an offset is reported as `not_aligned` instead of being placed at a guessed time.

`core.Playhead` is the shared event clock: it starts paused at zero, advances only while playing, and seeking keeps the paused or playing state. Pause, resume, and seek move this clock and every track's target together; they never change a stored offset. Neither type decodes media, seeks a player, or talks to a network.

## HLS metadata reading

`core/hls` does exactly two things: read the description of one HLS playlist, and read the audio and video tracks it carries with each track's first sample time. It uses `gohlslib` to parse the playlist and the segments, does not hand-write a parser, and does not download segments it does not need. `Read` accepts a media playlist only, leaving variant choice to the caller; `Probe` accepts a multivariant playlist, where the library picks the default variant.

A track's PTS is meaningful within its own stream: the library normalizes timestamps to that stream's first sample, so comparing across streams needs the `EXT-X-PROGRAM-DATE-TIME` the playlist declares. `Align` anchors on the earliest of those dates and gives each track its offset; when the date is missing it returns `core.ErrNotAligned` instead of guessing an exact match. `Attach` turns a probed stream into Core sources and tracks and rolls back on failure; `Apply` validates first, then stores the offsets on `core.Timeline`.

Addresses must be http or https and must not carry credentials; a redirect from a public address may not land on a local or private one. Timeouts, cancellation, and non-200 responses come back as distinguishable errors, and credentials are never written to a log.

## Boundaries

- Support independent sources and multiple synchronization groups; do not hard-code two sources as a Core limit.
- Sources, tracks, synchronization relationships, and output scenes are separate concepts. `core` fixes their shape, the event timeline, and HLS metadata reading; media handling is not implemented.
- Browser session access and media capabilities vary by platform. Web cannot assume access to Edge cookies or arbitrary third-party streams.
- Keep the Go Core separate from the MyGo UI lifecycle. Media engines, audio inference, and transport protocols are undecided; candidates are not installed dependencies.
- `testplace/` stays untracked; formal documentation records stable, verifiable repository facts.

The only available basic check is `go test ./...` from the repository root, which runs the `core` session and timeline tests plus the `core/hls` tests against recorded samples.
