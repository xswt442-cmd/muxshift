# Desktop

The MyGo desktop client: one window, several media sources, one Core session.

```sh
go build .
./desktop.exe -media <local file or http(s) address> [-media <second source> ...]
```

It is a separate Go module (`github.com/xswt442-cmd/muxshift/desktop`), so run its tests from this directory with `go test ./...`. The root module's `go test ./...` does not reach it.

## What the window does

- **Stage**: the picture of the source the scene shows, a transport row, and a status line that reports the last command and its result.
- **Sources**: import a local file or an HLS address while the session runs, select a card to inspect it, show or hear it, retry a source whose player failed, remove it.
- **Settings**: start page, default volume, nudge step, mute-unaligned, master volume, dock open. Settings are saved to the user config directory (`%AppData%\MuxShift\settings.json` on Windows, override with `-settings <path>`); a damaged file is reported and the defaults are used without touching it.
- Keyboard: `Ctrl+S` show, `Ctrl+H` hear, `Ctrl+R` retry, `Ctrl+1..4` the four pages.

## Scenarios

`desktop/scenarios.go` registers the scripted runs used to verify behaviour that needs a real window (playback, replay switching, buffer ownership, settings, layout). Run one with `-scenario <name>`; most take `-dir <output directory>` for screenshots and logs. Scenarios drive the same command entry point the buttons use — they do not synthesize clicks.

## Limits

- Only the source being drawn advances; another page stalls the stage until it is drawn again.
- Replay stops at "switch to a recording": a replay window is not yet an authorized seek range.
- No capture, no mixing, no automatic cross-source synchronization.
