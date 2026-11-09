# Architecture

[简体中文](ARCHITECTURE.zh-CN.md)

This repository contains a scaffold with four implemented pieces: a Go Core session configuration model, its event timeline, HLS playlist metadata reading, and a desktop experiment that registers several sources into one Core session and lets a scene decide which picture is shown and which sound is heard through MyGo's native video path. Cross-source synchronization, mixing, DVR, and controlled multi-source playback are not implemented.

## Modules and ownership

| Directory | Current state | Responsibility boundary |
| --- | --- | --- |
| `core/` | Session configuration model | Shared media-session configuration and control contracts |
| `core/hls/` | HLS metadata reading | Reads playlists, tracks, and first sample times; no transcoding and no serving |
| `desktop/` | MyGo desktop experiment | Registers several sources into one Core session, shows and hears what its scene selects, and owns the player lifecycle; no shared business logic |
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

`desktop/` is a MyGo experiment. It registers every local file or http(s) address given on the command line as a Core source: a network source is probed with `core/hls` first so its real tracks are registered, while a local file is registered as one video track and one audio track because MyGo does not enumerate tracks of a local file. The session holds one scene and one desktop-window output; the scene decides which video track is shown, which audio tracks are heard, and at what volume and mute state. The buttons change the scene, and `apply()` then sets each player's visibility, mute, and volume from it.

One player per source is the desktop adapter's current strategy, not a Core rule: Core only describes sources, tracks, and scenes. A video the scene does not select keeps its player alive and is hidden rather than removed from the view.

One component creates, shows, hides, and closes players. Closing marks the source as hidden and invalidates its player pointer in a single main-thread callback, waits for the window to actually draw three frames, and only then calls `Close()` on the main thread. Asynchronous first-frame work carries a generation number and stops as soon as it no longer matches. Reopening a source hands over instead of closing first: the new player opens while the old one keeps drawing, and the old one is closed once the new one is ready, because this build delivers no first frame when the window briefly has no video element at all.

The handover has an explicit failure edge. When the new player shows no picture before the timeout, the old player keeps drawing and keeps playing, the replacement that never started is the one that gets closed, and the source line reports why; a player is released only once a replacement has a picture. Every handover is bound to the instance generation, so a burst of reloads leaves the last one owning the source, the handover object moves with the generation, and no player is closed twice or closed by an older task.

This MyGo build has several gaps, which the application compensates for and records. A network source does not deliver its first picture by itself; one forward seek brings it in. That seek happens once, while the player is opening, the position after it is read back from the player instead of assumed, and if it does not work the player stays not ready and reports the real error: no background task keeps moving the playhead. It is a first-frame compensation, never a time-alignment mechanism. Two videos in the same window, or a video that has not delivered a frame yet, make `CapturePage` panic while reading an empty buffer, so captures are wrapped in `recover` and only log the failure. A player that was never drawn does not advance, so "A's picture with B's sound" only holds once B has been drawn at least once. Replacing the video element that is being drawn stops the window from repainting, so every player's video element stays in the tree and only its visibility changes.

Sound is not judged by ear: `IAudioMeterInformation` samples the default output device read-only, showing a level while playing, zero while muted, and a higher level when two sources sound at once. Those results are recorded in the P6 evidence. The meter cannot tell which process is sounding, so each sampling window runs only the observed application and aligns its log in time.


Manual alignment lives in `desktop/sync.go`: a `Timeline` holds one offset per track and a `Playhead` holds the shared event time. Pause and play are sent to the members of **one explicit Core sync group**: the first sync command — pause, play, anchor or nudge — creates that group from the sources the scene selects at that moment and fixes its membership, so a later scene change does not change who pauses; a source outside the group keeps playing, and a member that fails is reported by source name. The group is used rather than the scene because one scene may later show two unrelated matches, each with its own sync group, and a scene-based rule would pause both. A ±0.1s press moves that source's offset by a step and then seeks the player to `event time + offset`; a target before the start of the stream is refused with a reason instead of slowing the other source down. Anchoring adopts the reference player's current position as the event time, so anchoring again cannot make the target drift. A source that has never been placed on the timeline stays silent by default, and a visible button allows or withdraws that exception — the visible, reversible path against spoilers.

A player's `Position()` is that stream's own time, not content time: two streams starting at different points show different content at the same position, so a cross-source comparison has to use a content mark such as burnt-in timecode. The measured residual is about 0.1s, which comes from the seek landing point and the reported position; both are recorded in the P7 evidence.

## Boundaries

- Support independent sources and multiple synchronization groups; do not hard-code two sources as a Core limit.
- Sources, tracks, synchronization relationships, and output scenes are separate concepts. `core` fixes their shape, the event timeline, and HLS metadata reading; the desktop application already registers several sources in one session, selects picture and sound per scene, adjusts each level, and aligns one source against another by hand, while automatic cross-source synchronization and mixing are not implemented.
- Browser session access and media capabilities vary by platform. Web cannot assume access to Edge cookies or arbitrary third-party streams.
- Keep the Go Core separate from the MyGo UI lifecycle. The desktop application uses MyGo's native playback; other media engines, audio inference, and transport protocols are undecided, and candidates are not installed dependencies.
- `testplace/` stays untracked; formal documentation records stable, verifiable repository facts.

`go test ./...` from the repository root runs the `core` session and timeline tests plus the `core/hls` tests against recorded samples. The desktop application is built and checked on Windows separately with `cd desktop && go build .`; its playback, multi-source, and sound results are recorded in the P5 and P6 evidence and are not unit-tested.
