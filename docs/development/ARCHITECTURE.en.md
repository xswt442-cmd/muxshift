# Architecture

[简体中文](ARCHITECTURE.zh-CN.md)

This repository contains only a scaffold. Media ingestion, decoding, buffering, audiovisual synchronization, mixing, and playback interfaces are not implemented. The existing Go code only establishes the `core` package boundary.

## Modules and ownership

| Directory | Current state | Responsibility boundary |
| --- | --- | --- |
| `core/` | Go package placeholder | Shared media-session, timeline, and control contracts |
| `desktop/` | Documentation placeholder | MyGo desktop adapter, without shared business logic in the UI |
| `web/` | Documentation placeholder | Browser interface and browser media capability adapters |
| `extension/` | Documentation placeholder | User-authorized integration with an existing browser session |
| `testplace/` | Ignored local directory | Experiments, temporary notes, and agent working material |

The intended client-to-core dependency direction is `desktop → core ← web`; the browser extension integrates through an explicit protocol instead of depending on the desktop UI. These are ownership constraints, not existing runtime calls.

## Boundaries

- Support independent sources and multiple synchronization groups; do not hard-code two sources as a Core limit.
- Sources, tracks, synchronization relationships, and output scenes are separate concepts. Their exact data contracts are not yet defined.
- Browser session access and media capabilities vary by platform. Web cannot assume access to Edge cookies or arbitrary third-party streams.
- Keep the Go Core separate from the MyGo UI lifecycle. Media engines, audio inference, and transport protocols are undecided; candidates are not installed dependencies.
- `testplace/` stays untracked; formal documentation records stable, verifiable repository facts.

The only available basic check is `go test ./...` from the repository root, which currently checks compilation of the existing Go package.
