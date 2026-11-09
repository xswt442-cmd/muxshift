# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains a project scaffold with a Go Core session configuration model, an event timeline, HLS playlist metadata reading, a bounded replay buffer for live segments that can join its window into one playable recording, and two clients: a desktop experiment that registers several sources into one Core session and shows them through MyGo's native video path, and a web client that plays HLS in the browser through hls.js. Both let one scene decide which picture is shown and which sound is heard. Media capture, cross-source synchronization, mixing, and audio separation are not implemented; the web client neither aligns sources nor keeps history. A recording plays on its own, but showing the replay inside a running session window is not implemented yet.

[简体中文](README.md)

## Layout

- `core/`: shared Go Core session configuration model and event timeline.
- `core/hls/`: reads HLS playlists, tracks, and first sample times; no transcoding and no serving.
- `core/dvr/`: keeps one live source's segments in a bounded directory, reports the ranges that can really be replayed, and joins one contiguous range into a playable recording file.
- `desktop/`: MyGo desktop experiment that registers several local files or http(s) addresses into one Core session, selects picture and sound per scene, owns the player lifecycle, and keeps a bounded replay window for live sources.
- `web/`: web client: a loopback-only Go session service plus a page that plays HLS through hls.js and demonstrates several sources, a scene, volumes, and playback control inside one session.
- `extension/`: browser extension and its native host, which hand one address the user confirmed to the running session.
- `docs/development/`: engineering architecture documents.
- `testplace/`: Git-ignored local experiments, drafts, and agent working material.

See the [architecture](docs/development/ARCHITECTURE.en.md) for current module status and dependency direction.

## Verification

With Go 1.27 or newer, from the repository root:

```sh
go test ./...
```

This runs the Core session and timeline tests, the `core/hls` tests against recorded samples, and the `core/dvr` buffer tests; it does not validate media functionality.

The desktop application is built and run on Windows, where it opens a real window:

```sh
cd desktop
go build .
./desktop.exe -media <local file or http(s) address> [-media <second> ...]
```

The buttons select which source's picture is shown and which sources are heard, set their volume and mute state, and align one source against another in 0.1s steps; a source that was never placed on the timeline stays silent until a button lets its sound through. Pause all and Play all go to the members of the sync group and leave sources outside it alone. `-dvr-dir`, `-dvr-retention`, and `-dvr-max-bytes` open the replay buffer, and `-replay <file>` plays one joined recording as the only source, with the window title saying that it is a recording. `-scenario` runs the scripted checks in `desktop/scenarios.go` (`sync` for manual alignment, `syncmap` for one-source position measurement, `syncfail` for a failing group member, `dvr` for the replay window). Its results come from real window captures, logs, and level samples of the output device, and are not unit-tested. The replay buffer, the common window, the cache cleanup, and a recording playing in a process of its own are verified; showing a recording inside a running session window is not implemented.

The web client is built from the same Go module, binds to the loopback address only, and requires the token printed at startup:

```sh
go run ./web -media-root testplace/media \
  -media /media/hls-vod/index.m3u8 -media /media/hls-sync-a/index.m3u8
```

It prints `http://127.0.0.1:<port>/?token=<token>`. The page reads the session through `GET /api/session` and switches picture and sound through `POST /api/scene`; the same process serves the media under `/media/`, limited to `-media-root`, and the token travels in a cookie because media elements and hls.js cannot add a header to every segment. Requests without the token get 403, and the responses carry no CORS header, so another page cannot read this session. Browsers only allow sound after a click, so every source starts muted.

The web client plays one HLS or MP4 source per media element, does not align sources, and keeps no history; seeking stays inside what the browser has buffered. The hls.js 1.7.4 light build and its Apache-2.0 license live in `web/vendor/`.

The browser extension hands the one address the user picked to the local session and copies no cookies, headers, or keys:

```sh
go build -o testplace/bin/muxshift-host.exe ./extension/host
```

Load `extension/` unpacked in Edge, register the `com.muxshift.bridge` host as described in the [extension notes](extension/README.md), and start the web session. The session writes its address and token to `%LOCALAPPDATA%\MuxShift\bridge.json` (`-token-file` changes it); the host uses that token to call `POST /api/import`, so the extension never sees it. The extension lists only `http(s)` addresses the page already loaded: a `blob:` address belongs to the page and is refused with a note, a cross-origin address plays under that site's CORS rules because nothing is proxied, and protected media is never touched. `activeTab`, `scripting`, and `nativeMessaging` are the whole permission set, and without a host permission the extension cannot read any page until the user clicks it. The page re-reads the session every two seconds, so an imported source appears without a reload.

## License
