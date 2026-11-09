# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains a project scaffold with a Go Core session configuration model, an event timeline, HLS playlist metadata reading, and a desktop experiment that registers several sources into one Core session, where a scene decides which picture is shown and which sound is heard through MyGo's native video path. Media capture, cross-source synchronization, mixing, and audio separation are not implemented.

[简体中文](README.md)

## Layout

- `core/`: shared Go Core session configuration model and event timeline.
- `core/hls/`: reads HLS playlists, tracks, and first sample times; no transcoding and no serving.
- `desktop/`: MyGo desktop experiment that registers several local files or http(s) addresses into one Core session, selects picture and sound per scene, and owns the player lifecycle.
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

This runs the Core session and timeline tests plus the `core/hls` tests against recorded samples; it does not validate media functionality.

The desktop application is built and run on Windows, where it opens a real window:

```sh
cd desktop
go build .
./desktop.exe -media <local file or http(s) address> [-media <second> ...]
```

The buttons select which source's picture is shown and which sources are heard, set their volume and mute state, and align one source against another in 0.1s steps; a source that was never placed on the timeline stays silent until a button lets its sound through. Pause all and Play all go to the members of the scene's group and leave sources outside it alone. `-scenario` runs the scripted checks in `desktop/scenarios.go` (`sync` for manual alignment, `syncmap` for one-source position measurement, `syncfail` for a failing group member). Its results come from real window captures, logs, and level samples of the output device, and are not unit-tested.

## License
