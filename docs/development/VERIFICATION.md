# Verification index

What exists to check this repository, what each check covers, and what it cannot cover. Statuses below are "what the last run on the maintainer's Windows machine showed"; they are not a promise about your machine.

Two Go modules, two test commands:

```sh
go test ./...            # root: core, core/align, core/hls, core/dvr, internal/filelock, internal/netguard, web, tools
cd desktop && go test ./...   # the MyGo client is its own module and is NOT reached by the root command
go vet ./...             # in each module
```

## 1. Package tests (including loopback fixtures)

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
| Settings | `cd desktop && go test ./...` | defaults, validation ranges, save/load round trip, damaged files, live application of a draft, and the About version comparison | passing |
| Browser extension location | `cd desktop && go test ./...` | the About page finds the shipped `extension\` folder beside the binary and one level up in a checkout, reports a folder without `manifest.json`, and the open-folder command asks the shell for that folder and refuses when there is none | passing |
| Replay seek and internal entry | desktop module suite, replay tests in desktop/replay_group_test.go | Seek authorization, commit and rollback, queued steps after cancellation, and recording ownership while a replacement player opens. The internal live-to-replay transaction has no user-facing entry. | passing |
| Player lifecycle | desktop module suite, desktop/player_lifecycle_test.go | Generation guards, source detach and reload interleaving, lease release and recording cleanup at shutdown, and original workbench bounds when leaving viewing mode. | passing |
| Session recovery and membership | desktop module suite, desktop/session_store_test.go, desktop/stage_members_test.go, and desktop/startup_video_test.go | Single writer, preservation of invalid snapshots, address reselection, ordered pending routes, hidden members, and first startup picture. | passing |
| Stage transport and workbench | desktop module suite, including `transport_stage_test.go`, `workbench_polish_test.go`, and `p27_regression_test.go` | Scope of the active sync group, wide and narrow navigation, draft save recovery, and refusal of negative replay positions before offset changes. | passing |
| HLS destination and quality controls | `go test ./internal/netguard ./core/hls` and desktop tests in `live_variants_test.go` and `source_media_test.go` | Literal-IP dialing after one DNS lookup, mixed-address rejection, HTTP CONNECT and SOCKS5 pinning, HTTP forwarding refusal, redirect policy, playlist limits, signed URL redaction, and stale quality callbacks. | passing |
| Alignment rows | desktop module suite, including `align_row_test.go` | Result ownership and expiry when the reference, target, or offsets change. | passing |
| Core session snapshots and file locks | go test ./core ./internal/filelock | Session event ordering, snapshot and return-value isolation, exclusive lock contention and release. | passing |

## 2. Scenarios (real window, real media)

`desktop -scenario <name> [-dir <output>] [-media <file or URL> ...]` drives the same command entry point the buttons call. They need a **visible** window: a minimised or hidden window does not advance players, so captures and readiness readings are meaningless there. Each writes logs to `<output>/err.log` and screenshots to `<output>/`.

| Group | Scenarios | What it proves |
| --- | --- | --- |
| Playback lifecycle | `single`, `reopen`, `reopen-strict`, `handover-fail`, `reload-rapid`, `pair`, `replay-swap`, `switch`, `rapid`, `retry`, `lifecycle` | a source opens, reopens and is replaced without losing the window; a card moves between live and a recording; a burst of reloads, a removal, a runtime add, a failing address and closing the last player all end in a stated state |
| Scene and audio | `scene`, `four`, `audio`, `sync`, `syncgroup`, `syncmap`, `syncfail`, `align-probe`, `align-async` | the scene picks picture and sound, volumes are independent, unaligned sources are refused, the probe never writes the timeline |
| Buffer and replay | `dvr`, `contract`, `lease`, `sweep`, `replayprobe`, `replay`, `swap`, `swap-two`, `common`, `midjoin`, `frames`, `clock`, `playthrough`, `faults`, `final`, `seektxn`, `interleave`, `replayui`, `seekentry`, `seekclose` | segments are cached and gaps reported, cuts are immutable and leased, a replay switch commits only after the new player draws, ownership is released once, two sources of one event agree on a window, a recording cut from a live buffer is a linear span, and the frame a player presents can be compared with the position it reports; `faults` replays a playlist with missing segments and one with `EXT-X-DISCONTINUITY` and checks that no anchor bridges them; `final` puts the shared window, the three refusals, a rebuilt buffer and the closing lease and player counts into one run; `seektxn` performs the replay transaction on a window: two sources already replaying a recording, a confirmed calibration, one commit that replaces both recordings and both players, each replacement asked for the target's offset inside its own file and reporting it before the commit, and the lease counts before and after; `interleave` runs a single-source switch and a multi-source transaction against each other in both orders, and closes the session while a transaction owns players, checking that each path gives its player and lease back once; `replayui` drives the inspector's replay block through a refused request, a commit and a rollback, and checks the block never claims a settled position it does not have |
| Alignment report | `alignreport` | a probe shows both sources, both windows, the overlap, the peak against the next candidate and the confidence; the suggested offset is compared with the one the timeline holds; a nudge afterwards turns the same account into `expired` without writing a shift |
| Timeline view | `timeline` | the dock says `not aligned` before anybody aligns anything, shows a real zero offset and where it came from after anchoring, keeps the mapped media time and the player's own clock apart, and reads without changing a shift, the playhead or a player |
| Listening overlay | `mixer`, `mutehold` | `mixer` walks the overlay in the shape this build has (one routed source held by the unaligned protection, one outside the scene), refuses a solo of an unrouted source, and checks that solo, master mute and their removal are reflected in the players' mute state; `mutehold` only goes heard -> master muted -> heard so the peak of the default render endpoint can be compared with the check's timestamps |
| Interface | `commands`, `workspaces`, `workbench`, `matrix`, `sources`, `settings`, `settings-restart`, `keys`, `narrow`, `sizes`, `sizes-empty`, `dpi`, `windows`, `pages`, `workspace`, `workspace-restore`, `realdpi`, `focus`, `probeclose`, `crash`, `budget`, `sourcememory`, `sourcememory-restore`, `sourcememory-clear`, `replaybuffers`, `background`, `offline`, `hidden`, `webview` | the pages work, importing/selecting/removing a source works (an imported card does come up), settings persist across a restart, the frame/page/panes come back when the memory is on, the layout folds at its thresholds and at 100/150/200% scaling and in the smallest window, the pages stay reachable once the rail folds, the smallest window stays inside the work area at the real scale and edge clicks land on their controls, a real Tab walks the window's stops, closing the window cancels a comparison still decoding, kills the decoder it started (a driven check sees the pid alive before the close and gone after, its record reaches 0 when it ends, and a non-zero exit or a failed check fails the driver), and drops its late result, a real close closes once and its save is what the next start restores, a killed run writes nothing and the next start sweeps only dead sessions, the DVR budget comes from an explicit flag, the settings file or the built-in defaults and is written back in a shape the next start reads, a remembered source comes back at its level and mute, with that level in the scene and in the player, while a command-line source is never written, an unrelated Apply keeps the addresses remembered after the draft opened, a draft that pressed Forget empties the file, the diagnostics page reports each buffer's live cache and recording bytes apart from the limits, and its cleanup drops only the recordings no lease holds (the held one stays, with its reason, and the bytes it reports are the ones actually freed, a save that cannot land is counted for the run, the diagnostics page states the count and the newest failure, and clearing the status bar's message leaves the count where it was while a write that lands adds nothing, a taken-away source stops the buffer growing and the page names the failed read, and the same source coming back lets it grow again and quietly clears that line; the check runs both ways — a stopped server must name a refused read, a stopped publisher must stall without inventing one — a poll stops at the first segment it cannot store, and a signed address never writes its token into the row or the log; a source that fails to open stores and shows the same stripped reason, keeping the error chain for `errors.Is`), hidden pages stop the picture |

## 3. Checks that need a person or a specific machine

| Check | Why it is not automated |
| --- | --- |
| Browser extension | A controlled Chromium native popup covers scanning, pairing, candidate state and handoff. Controlled site responses cover tab invalidation and resolver behavior. Check a physical toolbar click for the browser's active-tab grant and use actual live sites to verify their current responses. |
| Real 150%/200% display scaling and a second monitor | Changing the host's DPI is out of scope for a test script; `dpi` simulates the layout consequence with `SetZoom`, which is not the same thing |
| Audio output on a real device | Scenario evidence uses a level meter, which shows signal, not what a listener hears |
| Long-running sessions (hours) | Every scenario is minutes long; drift over hours is unmeasured |
| `tools/separate` separation quality | Needs the ONNX weights, which the repository does not ship |

## 3a. Release build

The desktop binary is built by a script rather than by hand, so the version and
the commit it names come from git and the third-party notice comes from what the
binary links:

```powershell
scripts\release.ps1
```

The version comes from `VERSION` at the repository root; the script refuses
anything that is not `x.y.z`, and refuses a directory whose Windows version
block disagrees with it. The Windows binary is linked as a GUI subsystem
program, so a normal start opens no console; `-version` is read through a
redirected standard output by the release check. The output is `dist\muxshift-<version>-windows-amd64\`
holding `muxshift.exe`, `LICENSE` and `VERSION.txt` (version, commit, build time,
sha256, and what `-version` prints) at the top, the browser extension under
`extension\` (`manifest.json`, `background.js`, `popup.html`, `popup.js`,
`icons\`), and the documents under `docs\` (`README.md`, `README.en.md`,
`RUNNING.txt` and `THIRD-PARTY.md`, the modules the binary links with their
licence texts plus hls.js). The script refuses a working tree with changes
unless `-AllowDirty` is passed, and then names the build `+dirty` instead of
pretending it is the commit. If the notice generator fails, the release stops
instead of shipping a half-written `THIRD-PARTY.md`.

`scripts\verify-release.ps1` proves the build does not depend on the checkout it
was made in: it clones the repository into a temporary directory, builds there
with the same script, runs `-version`, and checks the binary names the clone's
commit, carries no path from the building machine (`-trimpath`, cgo off), carries the icon and the version block from `desktop\rsrc_windows_amd64.syso` (read from the PE resource directory, not scanned for), and that two clones in different directories build the same bytes. It also checks the release directory carries `extension\` and `docs\`.

`desktop\rsrc_windows_amd64.syso` is generated, not written by hand:
`scripts\winres.ps1` rebuilds it from `desktop\internal\win\icon.ico` and `VERSION`, and the
result is committed so a build needs neither the tool nor a network. The steps
from a green commit to a published tag are in
[RELEASE.md](RELEASE.md); this section only says what the build produces.

`scripts\build-installer.ps1` wraps an existing release directory in an Inno
Setup per-user installer; it creates Start Menu shortcuts, an optional desktop
shortcut and an uninstall entry. It needs Inno Setup 6 on the building machine
and does not rebuild the binary. `release.yml` runs it and attaches the setup
beside the ZIP; `installer\muxshift.iss` refuses with `#error` a release
directory that lacks `extension\manifest.json`, so a setup cannot be built from
a tree that forgot to package the extension.

What a release does **not** carry: no sample media, no vocal-separation weights,
no ffmpeg. `RUNNING.txt` says what the machine has to provide.
## 4. Evidence

Scenario captures and logs are machine-local. The Git-ignored local directory holds raw evidence. The repository records the check entry points and supported conclusions.

## 5. Known gaps in coverage

- CI runs both modules' checks (`go vet`, `go build` and `go test` at the root and inside `desktop/`, on Ubuntu and Windows); no scenario runs there, because they need a window and media that CI does not have.
- No test covers a session open for hours, or a second source opened while another one is being switched.
- Windows is the only platform with real-window evidence; the other platforms are covered by package tests only.
- Audio continuity across a replay switch is not covered by a state assertion.
