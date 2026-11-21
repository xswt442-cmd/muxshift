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
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.2-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

An early prototype. One window plays several media sources at once; a scene decides which picture is shown and which sound is heard; you can align two sources by hand and jump to a position inside a replay. Capture, mixing, and automatic cross-source sync are not built.

## Download

[Releases](https://github.com/xswt442-cmd/muxshift/releases) ship a Windows amd64 ZIP (`muxshift-0.0.2-windows-amd64.zip`); unzip it and run `muxshift.exe`. It needs Windows 10/11 and the Media Foundation that comes with it. The archive also carries `VERSION.txt`, `THIRD-PARTY.md`, and the checksum. Building from source is under [quick start](#quick-start).

## What it does

- **Several sources in one session**: each source keeps its own audio and video tracks, and a scene picks which picture is shown and which sound is played. Volume is set per source.
- **Manual alignment**: nudge one source against another in steps of 0.1 s. A playlist that carries PDT also yields a cross-source suggestion; the automatic result is only a suggestion and never writes the timeline.
- **Replay of a live stream**: live segments stay in a bounded directory, the remaining range is reported, and one span can be cut into a recording you can play back.
- **Seeking inside a replay**: a stage that is already replaying can request one position by its second inside the common window. Each source is authorized separately, and an out-of-window request or a second one before the first finishes is refused.
- **Five stage layouts**: Focus shows one source, Split puts two side by side, PiP keeps a small second picture, Grid is 2×2, and Hero is a wide main picture with the rest stacked beside it.
- **Free placement**: in editing mode a picture can be dragged, resized, locked to its aspect ratio, placed by exact numbers, and raised or lowered in the stacking order. Positions are stored as fractions of the stage, so resizing the window keeps the arrangement.
- **Four viewing modes**: editing, cinema, F11 full screen, and a small always-on-top window. Switching between them does not reload a player.
- **Appearance**: five colour themes, three text sizes, font selection, interface language, and tighter spacing are chosen in settings and preview immediately.
- **Web client**: binds to loopback only, plays with hls.js, and can switch scenes.
- **Browser extension**: hands the media address a page has already loaded to the desktop app, without cookies, request headers, or keys.

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

It prints a `http://127.0.0.1:<port>/?token=<token>` URL. The token appears once; the page trades it for an HttpOnly cookie and removes it from the address bar.

Behind a proxy: the desktop app reads `HTTP_PROXY`/`HTTPS_PROXY` first, then the Windows Internet Settings proxy. Loopback addresses are always reached directly.

## Adding a source

`+ Source` opens one panel with three choices:

- **Local file** opens the system file picker and adds the chosen file to the current session; cancelling changes nothing.
- **Media link** takes an http(s) HLS playlist or a directly readable media file. The desktop reads what the address actually serves rather than trusting its extension, so a web page, a 404, or an unsupported type is reported as an error.
- **Website address** opens the page in the system browser. For the page's own address to reach the desktop, load the extension in `extension/` (unpacked; no Native Host): take the one-time pairing code from the Diagnostics page, paste it into the popup once, then pick the address the page has loaded.

On Bilibili, Twitch, Kick, and YouTube live pages, the popup adds `Use this live stream`. The desktop receives one signed URL and no cookie; a stream with `#EXT-X-KEY` is refused, and a stream without `#EXT-X-ENDLIST` opens as live and starts on its own.

Whether a page can hand anything over is up to the page. `blob:`/MSE players, pages behind a login or DRM, and cross-origin restrictions are not worked around, only explained. Only http(s) addresses are accepted, and no cookies, request headers, or keys are passed on.

## Documentation

| Document | Contents |
| --- | --- |
| [Architecture](docs/development/ARCHITECTURE.en.md) | Module boundaries, data flow, the replay clock contract, and known limits |
| [Verification index](docs/development/VERIFICATION.md) | What each check covers, and what only a real window can show |
| [Changelog](CHANGELOG.md) | What each version added, improved, and fixed |
| [Release process](docs/development/RELEASE.en.md) | Maintainers: version, tag, build verification, and publishing |

## Limits

- **Entering a replay from live is not supported**: there is no entry point in the interface.
- **Only those four live pages work**: on-demand pages that feed segments to MSE, Bilibili among them, expose no single replayable address, and the YouTube sample asks for a login. A YouTube live `hlsManifestUrl` is tied to the exit IP that asked for it, so the playlist reads while media segments can be refused with `403`. All of these only report the problem; nothing bypasses the login, cross-origin rules, or DRM.
- **Live plays only what is happening now, and the web client does less**: live has no replay buffer and no cross-source alignment, and an expired signed URL has to be fetched from the extension again. The web client does no alignment and keeps no history; its seeks stay inside what the browser has buffered.
- **The desktop app only advances the source it is drawing**: switching to another page stops the stage, and coming back catches up after a few seconds.
- **The first seek at a replay edge can fail**: right after entering a replay, a jump to the very edge of the buffer sometimes fails and keeps the previous picture. The rate has not been measured.

## Layout

| Path | Contents |
| --- | --- |
| `core/` | session model, event timeline, HLS intake, and replay buffer |
| `desktop/` | MyGo desktop app and scripted checks |
| `web/`, `extension/` | loopback web session and browser extension (no native host) |
| `tools/` | separation, alignment, and design token generation |
| `docs/development/` | architecture, verification index, release process |

## License

MIT, see [LICENSE](LICENSE).
