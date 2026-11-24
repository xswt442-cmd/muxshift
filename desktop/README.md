# Desktop

The MyGo desktop client: one window, several media sources, one Core session.

```sh
go build .
./desktop.exe -media <local file or http(s) address> [-media <second source> ...]
```

It is a separate Go module (`github.com/xswt442-cmd/muxshift/desktop`), so run its tests from this directory with `go test ./...`. The root module's `go test ./...` does not reach it.

## What the window does

- **Navigation**. The application has Stage, Diagnostics, and Settings. The stage contains the scene list, canvas, transport, and console. On narrow windows the page menu stays available from all three pages.
- **Scenes**. The desktop saves scenes with their picture order, audio routing, layout, and picture positions. A scene switch selects a different arrangement of the session's existing players. The last scene is required for the stage output.
- **Console**: `Sources / Mixer / Sync` are visible at the same time by default. Drag its top edge to give it more room, fold it to a bar, hide one panel, or drag the seam between panels; below 900 DIP the same panels become three tabs. Settings → Console layout switches to one row per source (the B layout) without changing state.
- **Sources**. Import a local file, a media address, or a website address while the session runs. Each row shows whether its picture is visible and whether its player is running. The eye changes picture visibility, the crosshair selects a primary picture, and the transport plays or pauses that source. Sync group members use the group transport.
- **Shared source catalog**. Use Add Existing to link a source to another stage. Removing it from a stage drops that stage's placement and routing. Once all stages unlink a source, use Delete in the catalog to remove it from the session.
- **Sync**: one source is the reference; every other source has a 136×28 offset stepper, a reset when its offset is non-zero, and a read-only suggestion that is written in Diagnostics. Manual offsets go through the same command as the rest of the window, and an offset is only stored once the player accepted the seek.
- **Layouts**: `Focus`, `Split`, `PiP`, `Grid`, `Hero`, cycled from the canvas's corner button. A layout only decides where pictures go; it never reopens a player, changes the sound, or touches the shared clock. A source that does not fit the chosen layout is taken off the stage instead of being drawn over it, and a slot whose first frame has not arrived shows "waiting for the first picture".
- **Editing**. Edit selects a visible picture if the current selection is outside the layout or empty. Drag the picture or its resize handles, use the arrow keys or exact values to place it, and use the layer controls to change its stacking order. Positions are saved as fractions of the stage so they survive resizing and restarting.
- **Watching**: `Cinema` keeps the window frame and drops the editing chrome, F11 (or a double click on the picture) goes to real full screen, `Compact` shrinks the window to a small one that can stay on top. Switching between them does not reopen the players and does not jump the clock; the overlay with the transport fades out after 2.5 seconds and comes back on pointer movement, Tab, or an error. Esc always leaves the deepest state first and never closes the application.
- **Settings**. Theme, text size, font, spacing, and the dock option take effect and save when changed. Apply and Cancel govern the remaining draft settings. About reports the build, system information, and settings location, and checks GitHub for the latest release. On Windows the default settings path is `%AppData%\MuxShift\settings.json`, which `-settings <path>` can override. A damaged file is reported and the default settings are used.
- **Keyboard**. `Ctrl+S` changes the selected picture's visibility, `Ctrl+H` routes its sound, and `Ctrl+R` retries it. `Ctrl+1..3` open Stage, Diagnostics, and Settings. `F11` enters full screen, `Esc` steps back, `Tab` opens the viewing overlay, `Space` plays or pauses, and `M` mutes the first visible picture's source. Arrow keys nudge the selected picture while editing.

## Scenarios

`desktop/*_scenario.go` registers the scripted runs used to verify behaviour that needs a real window (playback, replay switching, buffer ownership, settings, layouts, editing, watching modes). Run one with `-scenario <name>`; most take `-dir <output directory>` for screenshots and logs. Scenarios drive the same command entry point the buttons use — they do not synthesize clicks.

## Limits

- Only windows that are actually drawn advance their pictures; a minimised window stalls its sources until it is drawn again.
- A picture is composed onto the stage; this build has no crop or mask, and no per-picture opacity.
- A replay stage can seek inside its recording; entering replay from a live stage is not offered.
- Live sources play only what is happening now: no replay buffer and no cross-source alignment.
- No capture, no mixing, no automatic cross-source synchronization.
