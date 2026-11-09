# Architecture

[简体中文](ARCHITECTURE.zh-CN.md)

This repository contains a scaffold with one implemented piece: a Go Core session configuration model. Media ingestion, decoding, buffering, timing-based synchronization, mixing, and playback are not implemented.

## Modules and ownership

| Directory | Current state | Responsibility boundary |
| --- | --- | --- |
| `core/` | Session configuration model | Shared media-session configuration and control contracts |
| `desktop/` | Documentation placeholder | MyGo desktop adapter, without shared business logic in the UI |
| `web/` | Documentation placeholder | Browser interface and browser media capability adapters |
| `extension/` | Documentation placeholder | User-authorized integration with an existing browser session |
| `testplace/` | Ignored local directory | Experiments, temporary notes, and agent working material |

The intended client-to-core dependency direction is `desktop → core ← web`; the browser extension integrates through an explicit protocol instead of depending on the desktop UI. These are ownership constraints, not existing runtime calls.

## Core session model

`core.Session` holds configuration only, never playback state. A session owns the sources it reads, the tracks found in them, sync groups that bind tracks to one reference track, scenes that decide what is shown and heard, and outputs that hand a scene to a capability. Each track belongs to at most one sync group. Every operation either applies completely or leaves the session unchanged, a refused operation records no event, and removing a source removes its tracks and every reference to them.

## Boundaries

- Support independent sources and multiple synchronization groups; do not hard-code two sources as a Core limit.
- Sources, tracks, synchronization relationships, and output scenes are separate concepts. `core` fixes their shape; time mapping and media handling are not implemented.
- Browser session access and media capabilities vary by platform. Web cannot assume access to Edge cookies or arbitrary third-party streams.
- Keep the Go Core separate from the MyGo UI lifecycle. Media engines, audio inference, and transport protocols are undecided; candidates are not installed dependencies.
- `testplace/` stays untracked; formal documentation records stable, verifiable repository facts.

The only available basic check is `go test ./...` from the repository root, which runs the `core` session tests.
