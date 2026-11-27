# Desktop

The MyGo desktop client: one window, several media sources, one Core session.

```sh
go build .
./desktop.exe -media <local file or http(s) address> [-media <second source> ...]
```

It is a separate Go module (`github.com/xswt442-cmd/muxshift/desktop`), so run its tests from this directory with `go test ./...`. The root module's `go test ./...` does not reach it.

## What the window does

- **Navigation**. The application has Stage, Diagnostics, and Settings. The stage contains the scene list, canvas, transport, and console. On narrow windows the page menu stays available from all three pages.
- **Scenes**. Scenes save picture order, audio routing, layout, and picture positions. Switching scenes opens sources used by the new stage and stops those it leaves behind.
- **Console**. Sources, Mixer, and Sync are visible together in wide windows. Drag the top edge or seams to resize them, fold the console, or hide individual panels. Below 900 DIP, the visible panels become tabs. To use one row per source, change Settings → Appearance → Console layout and select Apply.
- **Sources**. Import a local file, a media address, or a website address while the session runs. Each row shows whether its picture is visible and whether its player is running. The eye changes picture visibility, the crosshair selects a primary picture, and the transport plays or pauses that source. Sync group members use the group transport.
- **Shared source catalog**. An imported source belongs to its importing stage until you select Share source. Removing an unshared source from that stage deletes it. A shared source remains in the catalog when unlinked, and each stage keeps its own placement and routing.
- **Sync**. The current reference appears above the source rows. Estimate alignment offset displays a suggestion or failure at the top of the Sync panel. A manual nudge validates the target before updating the offset.
- **Layouts**. The corner control cycles through `Focus`, `Split`, `PiP`, `Grid`, `Hero`, and `Free`. Free gives every shown source a place to start, with saved manual positions taking precedence.
- **Editing**. Edit selects a visible picture if the current selection is outside the layout or empty. Drag the picture or its resize handles, use the arrow keys or exact values to place it, and use the layer controls to change its stacking order. Positions are saved as fractions of the stage so they survive resizing and restarting.
- **Watching**. `Cinema` keeps the window frame and hides the editing controls. F11 or a double-click on the picture enters full screen. `Compact` shrinks the window to a small one that can stay on top. Switching modes keeps players open and preserves the current position. The transport fades out after 2.5 seconds and returns on pointer movement, Tab, or an error. Esc always leaves the deepest state first and never closes the application.
- **Settings**. Theme, text size, font, compact spacing, and “Show the timeline under the stage” apply immediately. Apply and Cancel govern the remaining draft settings. About shows build and system information and checks GitHub for the latest release. Windows stores settings in `%AppData%\MuxShift\settings.json` by default. Use `-settings <path>` to override the path. If a file is damaged, the app uses defaults and preserves the original.
- **Workbench shortcuts**. `Ctrl+1` through `Ctrl+3` open Stage, Diagnostics, and Settings. `Ctrl+S` toggles the selected picture, `Ctrl+H` routes its sound, and `Ctrl+R` retries the source. `F11` enters full screen and arrow keys nudge the selected picture in edit mode. A text field or modal dialog takes precedence over these shortcuts.
- **Viewing shortcuts**. `F11` toggles full screen, `Esc` steps back, `Tab` reveals the hidden controls, and `Space` plays or pauses the stage. `M` toggles source mute on the first visible source. `Up` and `Down` adjust its volume when it has routed audio.

## Scenarios

`desktop/*_scenario.go` registers the scripted runs used to verify behaviour that needs a real window (playback, replay switching, buffer ownership, settings, layouts, editing, watching modes). Run one with `-scenario <name>`; most take `-dir <output directory>` for screenshots and logs. Scenarios drive the same command entry point the buttons use — they do not synthesize clicks.

## Limits

- Only windows that are actually drawn advance their pictures; a minimised window stalls its sources until it is drawn again.
- A picture is composed onto the stage; this build has no crop or mask, and no per-picture opacity.
- A replay stage can seek inside its recording; entering replay from a live stage is not offered.
- Live playback starts at the live edge. A bounded HLS buffer can produce a recording for separate playback.
- No capture, no mixing, no automatic cross-source synchronization.
