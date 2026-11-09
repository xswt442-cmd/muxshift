# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains a project scaffold with a Go Core session configuration model, an event timeline, HLS playlist metadata reading, and a desktop experiment that plays one source through MyGo's native video path. Media capture, cross-source synchronization, mixing, and audio separation are not implemented.

[简体中文](README.md)

## Layout

- `core/`: shared Go Core session configuration model and event timeline.
- `core/hls/`: reads HLS playlists, tracks, and first sample times; no transcoding and no serving.
- `desktop/`: MyGo desktop experiment that plays one local file or http(s) address and owns the player lifecycle.
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
./desktop.exe -media <local file or http(s) address>
```

Its playback results come from watching the window and are not unit-tested.

## License

See [LICENSE](LICENSE).
