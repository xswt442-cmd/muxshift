# Verification index

What exists to check this repository, what each check covers, and what it cannot cover. Statuses below are "what the last run on the maintainer's Windows machine showed"; they are not a promise about your machine.

Two Go modules, two test commands:

```sh
go test ./...            # root module: core, core/hls, core/dvr, web, tools
cd desktop && go test ./...   # the MyGo client is its own module and is NOT reached by the root command
go vet ./...             # in each module
```

## 1. Package tests (no window, no media, no network)

| Area | Command | What it proves | Status |
| --- | --- | --- | --- |
| Session model, events, scene/output bookkeeping | `go test ./core` | sources, tracks, sync groups, scenes and events behave as documented, including the removal that used to leave a track behind | passing |
| Event timeline | `go test ./core` | `media = event + offset`, `AdjustShift` replaces instead of accumulating, a paused/seeked playhead keeps its state | passing |
| HLS metadata | `go test ./core/hls` | playlist parsing, track probing, first-sample times, the refusal of credentialed URLs and public-to-private redirects | passing |
| Replay buffer | `go test ./core/dvr` | segment validation, gaps kept and measured, retention and byte budgets, `Join`, `Record`/`RecordAndLease` immutability, leases and `PendingCleanup`, session markers and the sweep | passing |
| Recording time contract | `go test ./core/dvr` | `BufferTime`, `SourceMediaTime` and `EventTime`, the measured-anchor refusals, epoch and buffer-instance identity | passing |
| Alignment probe math | `go test ./core/align` | envelope correlation, peak/confidence thresholds, overlap reporting | passing |
| Web session service | `go test ./web` | loopback binding, same-origin and JSON checks, path root confinement, scene rollback | passing |
| Replay clock and replay file | `cd desktop && go test ./...` | container↔player conversion, `playerWindow`, `replayFile` refusals, ffprobe measurement against a test segment | passing |
| Settings | `cd desktop && go test ./...` | defaults, validation ranges, save/load round trip, damaged files, live application of a draft | passing |

## 2. Scenarios (real window, real media)

`desktop -scenario <name> [-dir <output>] [-media <file or URL> ...]` drives the same command entry point the buttons call. They need a **visible** window: a minimised or hidden window does not advance players, so captures and readiness readings are meaningless there. Each writes logs to `<output>/err.log` and screenshots to `<output>/`.

| Group | Scenarios | What it proves |
| --- | --- | --- |
| Playback lifecycle | `single`, `reopen`, `reopen-strict`, `handover-fail`, `reload-rapid`, `pair`, `replay-swap`, `switch`, `rapid`, `retry`, `lifecycle` | a source opens, reopens and is replaced without losing the window; a card moves between live and a recording; a burst of reloads, a removal, a runtime add, a failing address and closing the last player all end in a stated state |
| Scene and audio | `scene`, `four`, `audio`, `sync`, `syncgroup`, `syncmap`, `syncfail`, `align-probe`, `align-async` | the scene picks picture and sound, volumes are independent, unaligned sources are refused, the probe never writes the timeline |
| Buffer and replay | `dvr`, `contract`, `lease`, `sweep`, `replayprobe`, `replay`, `swap`, `swap-two`, `common`, `midjoin`, `frames`, `clock`, `playthrough`, `faults`, `final`, `seektxn`, `interleave`, `replayui` | segments are cached and gaps reported, cuts are immutable and leased, a replay switch commits only after the new player draws, ownership is released once, two sources of one event agree on a window, a recording cut from a live buffer is a linear span, and the frame a player presents can be compared with the position it reports; `faults` replays a playlist with missing segments and one with `EXT-X-DISCONTINUITY` and checks that no anchor bridges them; `final` puts the shared window, the three refusals, a rebuilt buffer and the closing lease and player counts into one run; `seektxn` performs the replay transaction on a window: two sources already replaying a recording, a confirmed calibration, one commit that replaces both recordings and both players, each replacement asked for the target's offset inside its own file and reporting it before the commit, and the lease counts before and after; `interleave` runs a single-source switch and a multi-source transaction against each other in both orders, and closes the session while a transaction owns players, checking that each path gives its player and lease back once; `replayui` drives the inspector's replay block through a refused request, a commit and a rollback, and checks the block never claims a settled position it does not have |
| Alignment report | `alignreport` | a probe shows both sources, both windows, the overlap, the peak against the next candidate and the confidence; the suggested offset is compared with the one the timeline holds; a nudge afterwards turns the same account into `expired` without writing a shift |
| Timeline view | `timeline` | the dock says `not aligned` before anybody aligns anything, shows a real zero offset and where it came from after anchoring, keeps the mapped media time and the player's own clock apart, and reads without changing a shift, the playhead or a player |
| Listening overlay | `mixer`, `mutehold` | `mixer` walks the overlay in the shape this build has (one routed source held by the unaligned protection, one outside the scene), refuses a solo of an unrouted source, and checks that solo, master mute and their removal are reflected in the players' mute state; `mutehold` only goes heard -> master muted -> heard so the peak of the default render endpoint can be compared with the check's timestamps |
| Interface | `commands`, `workspaces`, `workbench`, `matrix`, `sources`, `settings`, `settings-restart`, `keys`, `narrow`, `sizes`, `sizes-empty`, `dpi`, `windows`, `pages`, `workspace`, `workspace-restore`, `realdpi`, `focus`, `probeclose`, `crash`, `budget`, `sourcememory`, `sourcememory-restore`, `sourcememory-clear`, `replaybuffers`, `background`, `offline`, `hidden`, `webview` | the pages work, importing/selecting/removing a source works (an imported card does come up), settings persist across a restart, the frame/page/panes come back when the memory is on, the layout folds at its thresholds and at 100/150/200% scaling and in the smallest window, the pages stay reachable once the rail folds, the smallest window stays inside the work area at the real scale and edge clicks land on their controls, a real Tab walks the window's stops, closing the window cancels a comparison still decoding, kills the decoder it started (a driven check sees the pid alive before the close and gone after, its record reaches 0 when it ends, and a non-zero exit or a failed check fails the driver), and drops its late result, a real close closes once and its save is what the next start restores, a killed run writes nothing and the next start sweeps only dead sessions, the DVR budget comes from an explicit flag, the settings file or the built-in defaults and is written back in a shape the next start reads, a remembered source comes back at its level and mute, with that level in the scene and in the player, while a command-line source is never written, an unrelated Apply keeps the addresses remembered after the draft opened, a draft that pressed Forget empties the file, the diagnostics page reports each buffer's live cache and recording bytes apart from the limits, and its cleanup drops only the recordings no lease holds (the held one stays, with its reason, and the bytes it reports are the ones actually freed, a save that cannot land is counted for the run, the diagnostics page states the count and the newest failure, and clearing the status bar's message leaves the count where it was while a write that lands adds nothing, a taken-away source stops the buffer growing and the page names the failed read, and the same source coming back lets it grow again and quietly clears that line; the check runs both ways — a stopped server must name a refused read, a stopped publisher must stall without inventing one — a poll stops at the first segment it cannot store, and a signed address never writes its token into the row or the log), hidden pages stop the picture |

## 3. Checks that need a person or a specific machine

| Check | Why it is not automated |
| --- | --- |
| Browser extension toolbar click (`activeTab`) | Playwright cannot deliver the real toolbar gesture; the flow is verified by hand and recorded as `Not run` until it is |
| Real 150%/200% display scaling and a second monitor | Changing the host's DPI is out of scope for a test script; `dpi` simulates the layout consequence with `SetZoom`, which is not the same thing |
| Audio output on a real device | Scenario evidence uses a level meter, which shows signal, not what a listener hears |
| Long-running sessions (hours) | Every scenario is minutes long; drift over hours is unmeasured |
| `tools/separate` separation quality | Needs the ONNX weights, which the repository does not ship |

## 4. Evidence

Scenario captures, logs and notes live in the Git-ignored `testplace/` directory (`testplace/evidence/...`). They are the maintainer's machine-local record: this document and the architecture state the conclusions, and `testplace/` is where the raw runs that support them were kept. A fresh clone has the code and these documents; it does not have the captures.

## 5. Known gaps in coverage

- CI runs both modules' checks (`go vet`, `go build` and `go test` at the root and inside `desktop/`, on Ubuntu and Windows); no scenario runs there, because they need a window and media that CI does not have.
- No test covers a session open for hours, or a second source opened while another one is being switched. Both belong to P16's own continuous-run acceptance, not to the P16.5 hygiene phase, which only recorded the gap.
- Windows is the only platform with real-window evidence; the other platforms are covered by package tests only.
