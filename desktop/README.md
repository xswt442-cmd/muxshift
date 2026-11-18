# Desktop

The MyGo desktop client: one window, several media sources, one Core session.

```sh
go build .
./desktop.exe -media <local file or http(s) address> [-media <second source> ...]
```

It is a separate Go module (`github.com/xswt442-cmd/muxshift/desktop`), so run its tests from this directory with `go test ./...`. The root module's `go test ./...` does not reach it.

## What the window does

- **Stage**: the pictures the scene shows, drawn side by side in one of five layouts, plus a transport row and a status line that reports the last command and its result. The stage owns the window: the rail, inspector and dock only open by themselves once there is room to spare (1600 DIP), and any of them can be toggled by hand.
- **Layouts**: `Focus`, `Split`, `PiP`, `Grid`, `Hero` on the bar above the stage. A layout only decides where pictures go; it never reopens a player, changes the sound, or touches the shared clock. A source that does not fit the chosen layout is taken off the stage instead of being drawn over it, and a slot whose first frame has not arrived shows "waiting for the first picture".
- **Editing**: with the inspector open, a picture can be dragged, resized from eight handles, kept to its aspect ratio or freed from it, placed by typing exact numbers, nudged with the arrow keys, and raised or lowered in the stacking order. Edits apply only while editing, so watching cannot move a picture by accident. Boxes are stored as fractions of the stage, so the arrangement survives a resize; they live for the session, not across restarts.
- **Watching**: `Cinema` keeps the window frame and drops the editing chrome, F11 (or a double click on the picture) goes to real full screen, `Compact` shrinks the window to a small one that can stay on top. Switching between them does not reopen the players and does not jump the clock; the overlay with the transport fades out after 2.5 seconds and comes back on pointer movement, Tab, or an error. Esc always leaves the deepest state first and never closes the application.
- **Sources**: import a local file, a media address or a website address while the session runs; select a card to inspect it, show or hear it, retry a source whose player failed, remove it.
- **Settings**: start page, default volume, nudge step, mute-unaligned, master volume, dock open. Settings are saved to the user config directory (`%AppData%\MuxShift\settings.json` on Windows, override with `-settings <path>`); a damaged file is reported and the defaults are used without touching it.
- Keyboard: `Ctrl+S` show, `Ctrl+H` hear, `Ctrl+R` retry, `Ctrl+1..4` the four pages, `F11` full screen, `Esc` one step back, `Tab` reach the overlay, `Space` play/pause, `M` mute the first shown source, arrow keys nudge the selected picture while editing.

## Scenarios

`desktop/scenarios.go` registers the scripted runs used to verify behaviour that needs a real window (playback, replay switching, buffer ownership, settings, layouts, editing, watching modes). Run one with `-scenario <name>`; most take `-dir <output directory>` for screenshots and logs. Scenarios drive the same command entry point the buttons use — they do not synthesize clicks.

## Limits

- Only windows that are actually drawn advance their pictures; a minimised window stalls its sources until it is drawn again.
- A picture is composed onto the stage; this build has no crop or mask, and no per-picture opacity.
- Positions and layer order live for one session: they are not written to settings yet.
- Replay stops at "switch to a recording": a replay window is not yet an authorized seek range.
- No capture, no mixing, no automatic cross-source synchronization.
