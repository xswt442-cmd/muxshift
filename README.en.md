# MuxShift

MuxShift is an open-source project for composing multiple audio/video sources and controlling their timelines.

The repository currently contains a project scaffold with a Go Core session configuration model, an event timeline, HLS playlist metadata reading, a bounded replay buffer for live segments that can join its window into one playable recording, and two clients: a desktop experiment that registers several sources into one Core session and shows them through MyGo's native video path, and a web client that plays HLS in the browser through hls.js. Both let one scene decide which picture is shown and which sound is heard. Media capture, cross-source synchronization, and mixing are not implemented; audio separation exists only as an experimental script outside the playback path. The web client neither aligns sources nor keeps history. A recording plays on its own, but showing the replay inside a running session window is not implemented yet.

[简体中文](README.md)

## Layout

- `core/`: shared Go Core session configuration model and event timeline.
- `core/hls/`: reads HLS playlists, tracks, and first sample times; no transcoding and no serving.
- `core/dvr/`: keeps one live source's segments in a bounded directory, reports the ranges that can really be replayed, and joins one contiguous range into a playable recording file.
- `desktop/`: MyGo desktop experiment that registers several local files or http(s) addresses into one Core session, selects picture and sound per scene, owns the player lifecycle, and keeps a bounded replay window for live sources.
- `web/`: web client: a loopback-only Go session service plus a page that plays HLS through hls.js and demonstrates several sources, a scene, volumes, and playback control inside one session.
- `extension/`: browser extension and its native host, which hand one address the user confirmed to the running session.
- `tools/separate/`: experimental vocal/non-vocal separation script that runs chunked ONNX inference on a model you supply; not wired into playback.
- `tools/align/`: experimental content-offset probe that compares audio envelopes or frame thumbnails and reports a confidence; not wired into playback and it never moves the timeline.
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

It prints `http://127.0.0.1:<port>/?token=<token>`. The page reads the session through `GET /api/session` and switches picture and sound through `POST /api/scene`. The token appears once in that bootstrap address, becomes an HttpOnly cookie, and the page strips it from the address bar; the native host sends it as `X-MuxShift-Token`, and the import endpoint takes a different bridge token (`X-MuxShift-Bridge`) that only the local bridge file holds, so a page cannot import with the cookie it has. A scene change also requires a JSON body and an `Origin` equal to the request host, so another page cannot drive the session with the cookie the browser already holds; every request must name a loopback host, and `-addr` is refused unless it is a loopback address. The same process serves the media under `/media/` through `os.OpenRoot`, so `..`, a symbolic link, or a junction inside `-media-root` cannot escape it; a scene change applies in full or is rejected in full. Requests without the token get 403, and the responses carry no CORS header. Browsers only allow sound after a click, so every source starts muted.

The web client plays one HLS or MP4 source per media element, does not align sources, and keeps no history; seeking stays inside what the browser has buffered. The hls.js 1.7.4 light build and its Apache-2.0 license live in `web/vendor/`.

The browser extension hands the one address the user picked to the local session and copies no cookies, headers, or keys:

```sh
go build -o testplace/bin/muxshift-host.exe ./extension/host
```

Load `extension/` unpacked in Edge, register the `com.muxshift.bridge` host as described in the [extension notes](extension/README.md), and start the web session. The session writes its address and bridge token to `%LOCALAPPDATA%\MuxShift\bridge.json` (`-token-file` changes it, mode 0600); the host sends that token as `X-MuxShift-Bridge` to `POST /api/import`, so the extension never sees it, and the import log keeps only the scheme, host, and path of an address, since a signed address carries its credential in the query. The extension lists only `http(s)` addresses the page already loaded: a `blob:` address belongs to the page and is refused with a note, a cross-origin address plays under that site's CORS rules because nothing is proxied, and protected media is never touched. `activeTab`, `scripting`, and `nativeMessaging` are the whole permission set, and without a host permission the extension cannot read any page until the user clicks it. The page re-reads the session every two seconds, so an imported source appears without a reload.

`tools/separate/` is an experiment: it splits one recording into a vocal stem and a non-vocal stem with chunked UVR MDX-Net inference and writes both as playable WAV files. It is **not** on the playback path, and neither client calls it yet; voice separation is not a product feature.

```sh
python tools\separate\separate.py --model testplace\models\Kim_Vocal_2.onnx \
  --input testplace\media\p11\sample-mixed.wav --out-dir testplace\p11-stems
```

Model weights are not distributed with the repository and their redistribution license is unconfirmed, so the script only reads an explicit `--model` path. On this RTX 4060 Laptop, 44.1 kHz stereo with 5.92-second chunks: DirectML takes about 0.26 s per chunk (roughly 7.8x realtime, 6.2 GiB of video memory) and plain CPU takes about 5.75 s per chunk (slower than realtime). The 5.92 s figure is the length of one model input chunk, not a measured end-to-end delay: overlap-add scheduling, encoding, buffering and player startup all add to what a listener waits for, and none of that is measured. On a synthetic sample the vocal stem correlates 0.99 with the commentary source and the non-vocal stem 0.83 with the crowd source; on real recordings (a LibriVox public-domain reading mixed with public-domain applause from Wikimedia, 20 s) the vocal stem correlates 0.93 with the reading and the non-vocal stem 0.94 with the applause. Commentary inside a real stadium broadcast is still unverified, and the two WAV files are stems of one recording rather than two independent live sources. See [tools/separate/README.md](tools/separate/README.md) for details.

`tools/align/` is a second experiment: it estimates the relative offset of two recordings from their content alone — `match.py` compares log-energy audio envelopes and `frame_hash.py` compares 16x16 grey frame thumbnails — and reports the best candidate, the runner-up, and how far the best stands above the median lag, refusing the match when that gap is too small. It is **not** wired into playback and it **never** moves the timeline: the desktop's manual nudge is still the only thing that changes a playback position. The desktop's "Try align" button takes the same read-only path: it compares two sources with `core/align`, converts the local match offset with `CoreOffset = (MediaStart_B − MediaStart_A) + LocalMatchOffset`, and shows either the proposed offset with its difference from the current one, or the reason it was refused; when the answer comes back it re-reads the source identity, the player generation, and the target offset, and reports the proposal as expired if any of them moved. Neither answer writes the timeline or moves a player.

```sh
python tools\align\match.py --a testplace\media\p12\ref.wav --b testplace\media\p12\shift-15.wav --max-lag 60
```

On 180-second readings, prepared offsets of 2, 15, and 45 seconds are all estimated exactly (no error), an inserted 3-second silence is measured as +3 s, and a pair with a different mix is measured as 0 s; a different recording, a looped replay, and a 1.05x speed change are all refused instead of answered confidently and wrongly. An audio pair takes about 0.1 s. The video fingerprint finds the right lag on the synthetic test pattern, but because that pattern looks similar at every lag the confidence rule refuses it, and the public-domain match footage download was blocked by HTTP 429, so the video path is implemented but unvalidated. See [tools/align/README.md](tools/align/README.md) for details.

## License
