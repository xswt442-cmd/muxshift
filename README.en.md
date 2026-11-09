# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains only a project scaffold. No working player, media capture, synchronization, or audio separation is implemented.

[简体中文](README.md)

## Layout

- `core/`: package boundary for the shared Go Core.
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

For now this only checks that the existing Go package builds; it does not validate media functionality.

## License

See [LICENSE](LICENSE).
