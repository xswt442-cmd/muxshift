# MuxShift engineering guide

Read this file and any nearer module-specific `AGENTS.md` before changing code. Actual source and tests take precedence over speculative designs. See the [architecture](docs/development/ARCHITECTURE.en.md), [engineering standards](.github/engineering/ENGINEERING_STANDARD.md), and [development/review policy](.github/REVIEW_POLICY.md).

## Current repository boundaries

- This repository is a working early prototype, not a scaffold. Go Core holds the session model and event timeline, `core/hls` reads HLS metadata, `core/dvr` keeps a bounded replay buffer with recording cuts, and `desktop/` is a runnable MyGo client (sources, scenes, manual alignment, replay, settings). Do not describe media capture, mixing, audio separation, automatic cross-source synchronization, authorized seek ranges, or a finished product as implemented.
- `core/` is the shared Go boundary. Desktop, web, and extension adapters may depend on Core contracts, but Core must not import any client-specific UI or browser runtime.
- `desktop/` is the MyGo desktop client; `web/` and `extension/` are separate clients. Do not introduce Wails or assume a shared browser authentication context.
- Keep sources, media tracks, synchronization groups, and output scenes conceptually distinct when adding real domain code. Avoid introducing speculative abstractions ahead of a concrete use case.
- Treat URLs, cookies, request headers, browser messages, and local file paths as untrusted inputs. Never log or commit credentials or session material; do not bypass media access controls.

## Engineering workflow

- Inspect the target file, relevant docs, and Git working tree before editing. Preserve unrelated changes.
- Follow the small top-level module structure. Avoid empty packages, fake implementations, generated build files, and placeholder tests that claim unimplemented behavior.
- Document only behavior and constraints that actually exist. Keep `README.md` and `README.en.md` consistent; pair detailed Chinese/English development guides when maintained.
- Keep tracked files portable: no personal absolute paths, local service addresses, private keys, logs, temporary investigations, or per-task notes.
- Put local drafts, experiments, handoffs, and non-product agent notes in the Git-ignored `testplace/` directory.
- Run `go test ./...` for Go source changes. `desktop/` is a separate Go module, so run `go test ./...` inside it too; what each check covers is in [the verification index](docs/development/VERIFICATION.md). Run client-specific checks only after the corresponding clients and scripts exist; report tests not run or unavailable accurately.
- Daily work belongs on `dev`; `main` is updated only through reviewed PRs. Follow `.github/REVIEW_POLICY.md` for phase pushes, the combined three-request limit for manual bot reviews per PR, and rate-limit handling.
- Do not commit, push, switch branches, reset, restore, publish, or alter remote Git state without explicit authorization. A scoped phase task may authorize focused commits and one push per completed phase; it does not authorize a PR merge or deployment.
