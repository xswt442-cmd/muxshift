# Architecture

[简体中文](ARCHITECTURE.zh-CN.md)

This repository contains a scaffold with four implemented pieces: a Go Core session configuration model, its event timeline, HLS playlist metadata reading, and a desktop experiment that plays one source at a time through MyGo's native video path. Cross-source synchronization, mixing, DVR, and controlled multi-source playback are not implemented.

## Modules and ownership

| Directory | Current state | Responsibility boundary |
| --- | --- | --- |
| `core/` | Session configuration model | Shared media-session configuration and control contracts |
| `core/hls/` | HLS metadata reading | Reads playlists, tracks, and first sample times; no transcoding and no serving |
| `desktop/` | MyGo desktop experiment | Plays one source through MyGo's native video path and owns the player lifecycle; no shared business logic |
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

A track's PTS is meaningful within its own stream: the library normalizes timestamps to that stream's first sample. The wall clock an `EXT-X-PROGRAM-DATE-TIME` entry declares can give a candidate cross-stream reference, but the same wall clock in two different live sources does not guarantee the same match event: different direction, transcoding, and buffering all change how content relates to time. `Align` therefore produces candidate offsets and does not claim content-level alignment. When the declaration is missing it returns `core.ErrNotAligned` instead of guessing an exact match. `Attach` turns a probed stream into Core sources and tracks and rolls back on failure; `Apply` validates first, then stores the offsets on `core.Timeline`.

Addresses must be http or https and must not carry credentials; a redirect from a public address may not land on a local or private one. Timeouts, cancellation, and non-200 responses come back as distinguishable errors, and credentials are never written to a log.

## Desktop adapter (MyGo)

`desktop/` is a MyGo experiment: it opens a local file or an http(s) address, plays it through the native `ui/video` path, offers play and pause, mute, reload, and close, and reports in its status line what the player itself says. One player per source is the desktop adapter's current strategy, not a Core rule: Core still describes only sources and tracks.

One component creates, shows, hides, and closes players, in that order. Switching sources first stops the window from drawing the old player, lets a frame pass, and only then closes it on the thread that draws; after closing it never calls that instance's play, seek, volume, or picture methods again. Asynchronous work returns to the main thread rather than assuming that clearing a pointer made anything safe.

This MyGo build has two gaps on the network path, which the application compensates for and records. A network source does not deliver its first picture by itself; one forward seek brings it in, so opening a network source retries a seek until the picture appears. Two videos in the same window, or a video that has not delivered a frame yet, make `CapturePage` panic while reading an empty buffer, so captures are wrapped in `recover` and only log the failure. Reopening the same network source after closing it does not resume playback and leaves `Err` nil; the application does not work around that gap.

## Boundaries

- Support independent sources and multiple synchronization groups; do not hard-code two sources as a Core limit.
- Sources, tracks, synchronization relationships, and output scenes are separate concepts. `core` fixes their shape, the event timeline, and HLS metadata reading; the desktop application already plays one source, while cross-source synchronization and mixing are not implemented.
- Browser session access and media capabilities vary by platform. Web cannot assume access to Edge cookies or arbitrary third-party streams.
- Keep the Go Core separate from the MyGo UI lifecycle. The desktop application uses MyGo's native playback; other media engines, audio inference, and transport protocols are undecided, and candidates are not installed dependencies.
- `testplace/` stays untracked; formal documentation records stable, verifiable repository facts.

`go test ./...` from the repository root runs the `core` session and timeline tests plus the `core/hls` tests against recorded samples. The desktop application is built and checked on Windows separately with `cd desktop && go build .`; its playback results are recorded in the P5 evidence and are not unit-tested.
