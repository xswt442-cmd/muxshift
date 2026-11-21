# Desktop

The MyGo desktop client: one window, several media sources, one Core session.

```sh
go build .
./desktop.exe -media <local file or http(s) address> [-media <second source> ...]
```

It is a separate Go module (`github.com/xswt442-cmd/muxshift/desktop`), so run its tests from this directory with `go test ./...`. The root module's `go test ./...` does not reach it.

## Source layout

The window is one Go package because the shell, the command path and the state
are one object; the files below are grouped by the job they do:

- `main.go`, `session.go`, `controller.go`, `workspace.go`: the process, the
  session state, the command path and the page switch.
- `workbench.go`, `dock.go`, `workspace.go`, `viewing.go`, `uikit.go`, `tokens.go`:
  the shell, the console, the pages, the watching modes and the shared controls.
- `player.go`, `live.go`, `sync.go`, `align.go`, `replay_*.go`, `dvr.go`,
  `mixer.go`, `sourcememory.go`: playback, timing, replay and sound.
- `settings.go`, `i18n.go`, `addsource.go`, `stage_edit*.go`: settings,
  language, the add-source chooser and the editing mode.
- `*_scenario.go`: the scripted checks, grouped by domain (align, playback,
  replay, UI, sources, Win32-only). They only run under `-scenario`.
- `*_group_test.go`: the package tests, grouped by domain.
- `internal/win`: Win32 registry, locale, proxy and window icon helpers.
- `internal/version`: the build stamp read by `-version`.

The scenarios and tests were 121 parallel files before this layout; merging
them keeps the package boundary intact and makes the directory readable. The
remaining product files stay in one package because a package split would need
an exported app/UI boundary, not a file move.

## What the window does

- **Shell**: a scene rail on the left, the canvas in the middle and a console under it. The rail holds only scenes plus `Add scene`; Settings and Diagnostics sit at its bottom. The picture's corner has a compact icon cluster (layout, edit, cinema, full screen) whose names are tooltips.
- **Scenes**: a scene is the set of pictures and sounds the stage draws. Rows add, switch and delete (the last scene cannot be deleted); switching does not reopen a player or change its volume or offset. Scenes live for the session: Core has no persistence API yet, and the window does not pretend otherwise.
- **Console**: `Sources / Mixer / Sync` are visible at the same time by default. Drag its top edge to give it more room, fold it to a bar, hide one panel, or drag the seam between panels; below 900 DIP the same panels become three tabs. Settings → Console layout switches to one row per source (the B layout) without changing state.
- **Sources**: import a local file, a media address or a website address while the session runs. Each row's eye puts the picture on the stage or takes it off without closing its player; the arrows change the layer order; the mixer owns the level and mute. Diagnostics lists the resolution the player reports and says "frame rate unknown / codec unknown" where the engine exposes no value.
- **Sync**: one source is the reference; every other source has a 136×28 offset stepper, a reset when its offset is non-zero, and a read-only suggestion that is written in Diagnostics. Manual offsets go through the same command as the rest of the window, and an offset is only stored once the player accepted the seek.
- **Layouts**: `Focus`, `Split`, `PiP`, `Grid`, `Hero`, cycled from the canvas's corner button. A layout only decides where pictures go; it never reopens a player, changes the sound, or touches the shared clock. A source that does not fit the chosen layout is taken off the stage instead of being drawn over it, and a slot whose first frame has not arrived shows "waiting for the first picture".
- **Editing**: in edit mode, a picture can be dragged, resized from eight handles, kept to its aspect ratio or freed from it, placed by typing exact numbers, nudged with the arrow keys, and raised or lowered in the stacking order. Edits apply only while editing, so watching cannot move a picture by accident. Boxes are stored as fractions of the stage, so the arrangement survives a resize; they live for the session, not across restarts.
- **Watching**: `Cinema` keeps the window frame and drops the editing chrome, F11 (or a double click on the picture) goes to real full screen, `Compact` shrinks the window to a small one that can stay on top. Switching between them does not reopen the players and does not jump the clock; the overlay with the transport fades out after 2.5 seconds and comes back on pointer movement, Tab, or an error. Esc always leaves the deepest state first and never closes the application.
- **Settings**: start page, default volume, nudge step, mute-unaligned, master volume, console layout, dock open. Settings are saved to the user config directory (`%AppData%\MuxShift\settings.json` on Windows, override with `-settings <path>`); a damaged file is reported and the defaults are used without touching it.
- Keyboard: `Ctrl+S` show, `Ctrl+H` hear, `Ctrl+R` retry, `Ctrl+1..5` stage, sources, diagnostics, settings, mixer, `F11` full screen, `Esc` one step back, `Tab` reach the overlay, `Space` play/pause, `M` mute the first shown source, arrow keys nudge the selected picture while editing.

## Scenarios

`desktop/*_scenario.go` registers the scripted runs used to verify behaviour that needs a real window (playback, replay switching, buffer ownership, settings, layouts, editing, watching modes). Run one with `-scenario <name>`; most take `-dir <output directory>` for screenshots and logs. Scenarios drive the same command entry point the buttons use — they do not synthesize clicks.

## Limits

- Only windows that are actually drawn advance their pictures; a minimised window stalls its sources until it is drawn again.
- A picture is composed onto the stage; this build has no crop or mask, and no per-picture opacity.
- Positions and layer order live for one session: they are not written to settings yet.
- Replay stops at "switch to a recording": a replay window is not yet an authorized seek range.
- No capture, no mixing, no automatic cross-source synchronization.
