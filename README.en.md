<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>Combine several media sources and control them on one timeline</p>
  <p>
    <a href="README.md">中文</a> ·
    <a href="docs/development/ARCHITECTURE.en.md">Architecture</a> ·
    <a href="docs/development/VERIFICATION.md">Verification index</a>
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
    <a href="VERSION"><img alt="release" src="https://img.shields.io/badge/release-v0.0.1-5d77f5"></a>
  </p>
</div>

Put several media sources into one session, let a scene decide which one is
seen and which ones are heard, and align them on one event timeline.

An early prototype, not a finished product: it can attach sources, switch
between them, align them by hand, and cut a recording and land on a position
inside a replay. Capture, mixing and automatic cross-source sync are not built.

## What works

- **One Core session**: sources, tracks, sync groups, scenes, outputs, and an
  event timeline — shared event time plus a track's own offset is the media time
  that track should play.
- **HLS intake**: reads the playlist and probes tracks and the first sample
  time. Cross-stream alignment needs a program date time; without one it refuses
  rather than guesses.
- **Desktop app**: a native MyGo window plays several sources, scenes choose
  picture and sound, each source has its own volume; manual nudges of ±0.1 s,
  and the automatic alignment only ever suggests, it never writes the timeline.
- **DVR buffer**: live segments are kept in a bounded directory, the really
  available range and its gaps are reported, and one continuous span is cut into
  a recording; a switch puts the new player's picture up before replacing the
  source, and rolls back when it cannot.
- **Seeking inside a replay**: once a stage is replaying, its inspector can ask
  for one position inside the candidate common window; each source is authorized
  separately, out-of-window and concurrent requests are refused, and a failed
  commit rolls back entirely.
- **Web client and extension**: the web client binds to loopback only, plays
  with hls.js and can switch scenes; the extension hands the one address the
  user picked to the local session, never cookies, headers or keys.

## Quick start

Go 1.27 or newer.

```sh
go test ./...
cd desktop && go test ./...
```

What each check covers, and what can only run in a real window, is in the
[verification index](docs/development/VERIFICATION.md).

Desktop app (Windows, opens a real window):

```sh
cd desktop
go build .
./desktop.exe -media <local file or http(s) URL> [-media <second source> ...]
```

Web client:

```sh
go run ./web -media-root <media dir> \
  -media /media/hls-vod/index.m3u8 -media /media/hls-vod/second.m3u8
```

It prints a `http://127.0.0.1:<port>/?token=<token>` URL; the token appears
once, the page trades it for an HttpOnly cookie and removes it from the address
bar.

## Releases

The current version is **v0.0.1**: a Windows amd64 ZIP built from a clean
commit, with `VERSION.txt`, `THIRD-PARTY.md` and a reproducible sha256. Building,
verifying, tagging and publishing are described in
[RELEASING.md](docs/development/RELEASING.md). Release notes list only what has
been verified; they do not promise a roadmap.

## Limits

- Sources can be imported while running: the card appears and the player shows
  its picture soon after; a bad address reports an error and leaves no card.
- **Entering a replay from live is not offered**: there is no toolbar button and
  no shortcut. The internal planning and transaction code exists, but a test
  keeps it out of the shipped paths.
- **Cross-source content correspondence is experimental**: V1 was rejected for
  authorizing use and V2 is recorded as ObservedSupport only; neither is on the
  playback path and neither writes the timeline.
- **"Position settled" is not "frame presented"**: a committed seek only proves
  the players reported the position they were asked for, and the account keeps
  saying `position settled, the frame is not confirmed`.
- The desktop app only advances the source it is drawing: switch to another page
  and the stage stops; come back and it catches up after a few seconds.
- A first seek at the very edge of a recording can fail intermittently and roll
  back safely; the rate has not been measured. The web client does not do
  alignment or replay, and its seeks stay inside what the browser buffered.

## Layout

| Path | Contents |
| --- | --- |
| `core/` | session model and event timeline |
| `core/hls/` | playlist intake and track probing |
| `core/dvr/` | DVR buffer, gaps and recording cuts |
| `desktop/` | MyGo desktop app and scripted checks |
| `web/` | loopback session server and page |
| `extension/` | extension and native host |
| `tools/` | separation, alignment and design tokens |
| `docs/development/` | architecture, verification index, release process |

Module states and dependency directions are in the
[architecture note](docs/development/ARCHITECTURE.en.md), including the web
client's token, origin and path limits.

## License

MIT, see [LICENSE](LICENSE).
