# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains a project scaffold with a Go Core session configuration model and event timeline. No working player, media capture, synchronization, or audio separation is implemented.

[简体中文](README.md)

## Layout

- `core/`: shared Go Core session configuration model.
- `desktop/`: placeholder for a MyGo-based desktop client.
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

This runs the Core session tests; it does not validate media functionality.

## License

See [LICENSE](LICENSE).
