# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains a project scaffold with a Go Core session configuration model, an event timeline, HLS playlist metadata reading, a bounded replay buffer for live segments that can join its window into one playable recording, and a desktop experiment that registers several sources into one Core session, where a scene decides which picture is shown and which sound is heard through MyGo's native video path. Media capture, cross-source synchronization, mixing, and audio separation are not implemented; a recording plays on its own, but showing the replay inside a running session window is not implemented yet.

[简体中文](README.md)

## Layout

- `core/`: shared Go Core session configuration model and event timeline.
- `core/hls/`: reads HLS playlists, tracks, and first sample times; no transcoding and no serving.
- `core/dvr/`: keeps one live source's segments in a bounded directory, reports the ranges that can really be replayed, and joins one contiguous range into a playable recording file.
- `desktop/`: MyGo desktop experiment that registers several local files or http(s) addresses into one Core session, selects picture and sound per scene, owns the player lifecycle, and keeps a bounded replay window for live sources.
- `web/`: placeholder for a web client.
- `extension/`: placeholder for a browser extension.
- `docs/development/`: engineering architecture documents.
- `testplace/`: Git-ignored local experiments, drafts, and agent working material.

See the [architecture](docs/development/ARCHITECTURE.en.md) for current module status and dependency direction.

## Verification

With Go 1.27 or newer, from the repository root:

```sh
go test ./...
```

This runs the Core session and timeline tests, the `core/hls` tests against recorded samples, and the `core/dvr` buffer tests; it does not validate media functionality.

The desktop application is built and run on Windows, where it opens a real window:

```sh
cd desktop
go build .
./desktop.exe -media <local file or http(s) address> [-media <second> ...]
```

The buttons select which source's picture is shown and which sources are heard, set their volume and mute state, and align one source against another in 0.1s steps; a source that was never placed on the timeline stays silent until a button lets its sound through. Pause all and Play all go to the members of the sync group and leave sources outside it alone. `-dvr-dir`, `-dvr-retention`, and `-dvr-max-bytes` open the replay buffer. `-scenario` runs the scripted checks in `desktop/scenarios.go` (`sync` for manual alignment, `syncmap` for one-source position measurement, `syncfail` for a failing group member, `dvr` for the replay window). Its results come from real window captures, logs, and level samples of the output device, and are not unit-tested; the replay buffer, the common window, the cache cleanup, and a joined recording playing as the only source are recorded, while showing that recording inside a running session window is not, which the P8 evidence in the ignored `testplace/` directory records in detail.

## License
