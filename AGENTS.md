# MuxShift engineering guide

Read this file and any nearer module-specific `AGENTS.md` before changing code. Actual source and tests take precedence over speculative designs. See the [architecture](docs/development/ARCHITECTURE.en.md).

## Current repository boundaries

- This repository is an initial scaffold; do not describe media capture, synchronization, playback, audio separation, or a functioning desktop/web application as implemented.
- `core/` is the shared Go boundary. Future desktop, web, and extension adapters may depend on Core contracts, but Core must not import any client-specific UI or browser runtime.
- `desktop/` is reserved for MyGo; `web/` and `extension/` are separate clients. Do not introduce Wails or assume a shared browser authentication context.
- Keep sources, media tracks, synchronization groups, and output scenes conceptually distinct when adding real domain code. Avoid introducing speculative abstractions ahead of a concrete use case.
- Treat URLs, cookies, request headers, browser messages, and local file paths as untrusted inputs. Never log or commit credentials or session material; do not bypass media access controls.

## Engineering workflow

- Inspect the target file, relevant docs, and Git working tree before editing. Preserve unrelated changes.
- Follow the small top-level module structure. Avoid empty packages, fake implementations, generated build files, and placeholder tests that claim unimplemented behavior.
- Document only behavior and constraints that actually exist. Keep `README.md` and `README.en.md` consistent; pair detailed Chinese/English development guides when maintained.
- Keep tracked files portable: no personal absolute paths, local service addresses, private keys, logs, temporary investigations, or per-task notes.
- Put local drafts, experiments, handoffs, and non-product agent notes in the Git-ignored `testplace/` directory.
- Run `go test ./...` for Go source changes. Run client-specific checks only after the corresponding clients and scripts exist; report tests not run or unavailable accurately.
- Do not commit, push, switch branches, reset, restore, publish, or alter remote Git state without explicit authorization.
