<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>Several media sources in one session: a scene picks what is seen and heard, and one event timeline aligns them</p>
  <p>
    <a href="README.md">中文</a> ·
    <a href="https://github.com/xswt442-cmd/muxshift/releases">Download</a> ·
    <a href="CHANGELOG.md">Changelog</a> ·
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.4-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

## Download

[Releases](https://github.com/xswt442-cmd/muxshift/releases) ship a Windows amd64 installer (`muxshift-0.0.4-windows-amd64-setup.exe`) and a portable ZIP (`muxshift-0.0.4-windows-amd64.zip`). The installer installs per user, creates Start Menu and optional desktop shortcuts, and registers an uninstall entry; the ZIP runs `muxshift.exe` after unpacking. Both give the same layout: the program and `VERSION.txt` at the top, the browser extension under `extension\`, and the documents under `docs\`. Both are GUI programs, so a normal start carries no console window; they need Windows 10/11 and the Media Foundation that comes with it. Building from source is under [quick start](#quick-start).

## What it does

- **Several sources in one session**: each source keeps its own audio and video tracks, and a scene picks which picture is shown and which sound is played. Volume is set per source.
- A normal desktop launch restores the stage arrangement and opens the sources used by the current stage. A temporary launch with `-media` preserves the saved session.
- Use the Sources panel to show, play, pause, or refresh an individual source. A live master playlist with multiple playable variants offers a quality choice for that source.
- **Manual alignment**: nudge one source against another in steps of 0.1 s. A playlist that carries PDT also yields a cross-source suggestion; the automatic result is only a suggestion and never writes the timeline.
- **Replay of a live stream**: live segments stay in a bounded directory, the remaining range is reported, and one span can be cut into a recording you can play back.
- **Seeking inside a replay**: a stage that is already replaying can request one position by its second inside the common window. Each source is authorized separately, and an out-of-window request or a second one before the first finishes is refused.
- **Stage layouts**: choose Focus, Split, PiP, Grid, Hero, or Free.
- **Free placement**: select a picture to move, resize, lock, or position it precisely. Each stage saves its positions and stacking order.
- **Four viewing modes**: editing, cinema, F11 full screen, and a small always-on-top window. Switching between them does not reload a player.
- **Appearance**: theme, text size, font, spacing, and docking changes take effect and save immediately. Select Apply to change the interface language.
- **About**: the About section in settings shows the current version and build commit, Windows version and architecture, logical processors, physical memory, and the settings file path; opening it asks GitHub for the latest release, and the check can be retried manually.
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

Behind a proxy: the desktop app reads `HTTP_PROXY`/`HTTPS_PROXY` first, then the Windows Internet Settings proxy, applying the HTTP and HTTPS entries separately. The `ProxyOverride` exclusion list is not applied yet; loopback addresses are always reached directly.

## Adding a source

`+ Source` opens one panel with three choices:

- **Local file** opens the system file picker and adds the chosen file to the current session; cancelling changes nothing.
- **Media link** takes an http(s) HLS playlist or a directly readable media file. The desktop reads what the address actually serves rather than trusting its extension, so a web page, a 404, or an unsupported type is reported as an error.
- **Website address** opens the page in the system browser. Once paired, the browser extension can hand media addresses on the page to the desktop.

In Settings → About → Browser extension, choose "Open the folder", or open the `extension` folder in the install directory. Enable developer mode in `edge://extensions` or `chrome://extensions` and load the unpacked extension. Enter the one-time code from the desktop's Diagnostics page in the popup to pair without a Native Host.

Ordinary candidates appear in the popup's main view and support keyboard selection. Supported live pages offer “Resolve and send”. After navigating or reloading, reopen the popup to scan again. Check Stage → Sources in the desktop app for the import result.

On Bilibili, Twitch, Kick, and YouTube live pages, the popup adds a “Resolve and send” action. The desktop receives one signed URL and no cookie; a stream with `#EXT-X-KEY` is refused, and a stream without `#EXT-X-ENDLIST` opens as live and starts on its own. A master with a separate audio rendition is also refused, because the current player opens one address and cannot keep the video plus a second audio playlist. The extension's mirror pick is best-effort: a cross-origin opaque response cannot prove a mirror plays, so the desktop still refuses an address by its playlist contents. After the popup says the desktop accepted the request, the desktop's Sources page remains the source of truth for media opening and failure status.

Whether a page can hand anything over is up to the page. `blob:`/MSE players, pages behind a login or DRM, and cross-origin restrictions are not worked around, only explained. Only http(s) addresses are accepted, and no cookies, request headers, or keys are passed on.

## Documentation

| Document | Contents |
| --- | --- |
| [Architecture](docs/development/ARCHITECTURE.en.md) | Module boundaries, data flow, the replay clock contract, and known limits |
| [Verification index](docs/development/VERIFICATION.md) | What each check covers, and what only a real window can show |
| [Changelog](CHANGELOG.md) | What each version added, improved, and fixed |
| [Release process](docs/development/RELEASE.en.md) | Maintainers: version, tag, build verification, and publishing |

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
