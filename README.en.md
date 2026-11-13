<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>Several media sources in one session: a scene picks what is seen and heard, and one event timeline aligns them</p>
  <p>
    <a href="README.md">中文</a> ·
    <a href="https://github.com/xswt442-cmd/muxshift/releases">Download</a> ·
    <a href="CHANGELOG.md">Changelog</a> ·
    <a href="docs/development/RELEASE.en.md">Release process</a>
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.1-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

An early prototype: it attaches sources, switches between them, aligns them by
hand, and lands on a position inside a replay. Capture, mixing and automatic
cross-source sync are not built.

## Download

[Releases](https://github.com/xswt442-cmd/muxshift/releases) ship a Windows amd64
ZIP (`muxshift-0.0.1-windows-amd64.zip`); unzip it and run `muxshift.exe`. It
needs Windows 10/11 and the Media Foundation that ships with it. The archive
carries `VERSION.txt`, `THIRD-PARTY.md` and the checksum; building from source is
under [quick start](#quick-start).

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

## Documentation

| Document | Contents |
| --- | --- |
| [Architecture](docs/development/ARCHITECTURE.en.md) | Module boundaries, data flow, the replay clock contract and known limits |
| [Verification index](docs/development/VERIFICATION.md) | What each check covers, and what only a real window can show |
| [Changelog](CHANGELOG.md) | What each version added, improved and fixed |
| [Release process](docs/development/RELEASE.en.md) | Maintainers: version, tag, build verification and publishing |

## Limits

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
- A source imported while running joins playback once its player shows a
  picture; a bad address only reports an error and leaves no empty card.

## Layout

| Path | Contents |
| --- | --- |
| `core/` | session model, event timeline, HLS intake and DVR buffer |
| `desktop/` | MyGo desktop app and scripted checks |
| `web/`, `extension/` | loopback web session, browser extension and native host |
| `tools/` | separation, alignment and design tokens |
| `docs/development/` | architecture, verification index, release process |

## License

MIT, see [LICENSE](LICENSE).
