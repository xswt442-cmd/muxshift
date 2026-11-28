<div align="center">
  <img src="web/icon.svg" alt="MuxShift" width="88" height="88">
  <h1>MuxShift</h1>
  <p>Several media sources in one session: a scene picks what is seen and heard, and one event timeline aligns them</p>
  <p>
    <a href="README.md">中文</a> ·
    <a href="https://github.com/xswt442-cmd/muxshift/releases">Download</a> ·
    <a href="CHANGELOG.md">Changelog</a>
  </p>
  <p>
    <a href="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/xswt442-cmd/muxshift/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
    <a href="https://github.com/xswt442-cmd/muxshift/releases"><img alt="release" src="https://img.shields.io/badge/release-v0.0.5-5d77f5"></a>
    <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-5d77f5"></a>
    <a href="go.mod"><img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-5d77f5"></a>
  </p>
</div>

## Download

[Releases](https://github.com/xswt442-cmd/muxshift/releases) ship a Windows amd64 installer (`muxshift-0.0.5-windows-amd64-setup.exe`) and a portable ZIP (`muxshift-0.0.5-windows-amd64.zip`). The installer installs per user, creates Start Menu and optional desktop shortcuts, and registers an uninstall entry; the ZIP runs `muxshift.exe` after unpacking. Both give the same layout: the program and `VERSION.txt` at the top, the browser extension under `extension\`, and the documents under `docs\`. Both are GUI programs, so a normal start carries no console window; they need Windows 10/11 and the Media Foundation that comes with it. Building from source is under [quick start](#quick-start).

## What it does

- **Several sources in one session**. Each source keeps its own audio and video tracks, and a scene picks which picture is shown and which sound is played. Volume is set per source.
- **Session recovery**. Startup restores the stage arrangement and opens recoverable sources used by the active stage. Session files omit HTTP(S) addresses with credentials, a query, or a fragment, while retaining their sources and stage assignments for address selection in Stage → Sources.
- **Temporary sessions**. Launching with `-media` preserves the saved session. When multiple windows use the same session file, only the window holding its lock can save it.
- **Per-source controls**. Use Stage → Sources to show, play, pause, or refresh an individual source. A live master playlist with multiple playable variants offers a quality choice for that source.
- **Manual alignment**: nudge one source against another in steps of 0.1 s. A playlist that carries PDT also yields a cross-source suggestion; the automatic result is only a suggestion and never writes the timeline.
- **Replay of a live stream**: live segments stay in a bounded directory, the remaining range is reported, and one span can be cut into a recording you can play back.
- **Seeking inside a replay**: a stage that is already replaying can request one position by its second inside the common window. Each source is authorized separately, and an out-of-window request or a second one before the first finishes is refused.
- **Stage layouts**: choose Focus, Split, PiP, Grid, Hero, or Free.
- **Free placement**: select a picture to move, resize, lock, or position it precisely. Each stage saves its positions and stacking order.
- **Four viewing modes**: editing, cinema, F11 full screen, and a small always-on-top window. Switching between them does not reload a player.
- **Appearance**. Theme, text size, font, compact spacing, and “Show the timeline under the stage” apply and save immediately. Select Apply to save Console layout in Appearance or Language in General.
- **About**. The About section in settings shows the current version and build commit, Windows version and architecture, logical processors, physical memory, and the settings file path. Opening it checks GitHub for the latest release. You can also retry the check.
- **Web client**: binds to loopback only, plays with hls.js, and can switch scenes.
- **Browser extension**: hands the media address a page has already loaded to the desktop app, without cookies, request headers, or keys.

## Quick start

The root module requires Go 1.27 or newer. The desktop module requires Go 1.27.3 or newer.

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

Media metadata reads verify the actual connection address for public sources. Plain HTTP playlists support direct connections or SOCKS5. HTTPS playlists also support CONNECT through an HTTP or HTTPS proxy.

## Adding a source

Select `+` in the stage's Sources panel or the add button on an empty stage to open the three import options.

- **Local file** opens the system file picker and adds the chosen file to the current session; cancelling changes nothing.
- **Media link** opens an HTTP(S) HLS playlist or a directly readable media file. The desktop checks the response and reports a web page, a 404, or an unsupported media type.
- **Website address** opens a page in the system browser. Once paired, the extension can send media addresses from that page to the desktop.

In Settings → About → Browser extension, choose "Open the folder", or open the `extension` folder in the install directory. Enable developer mode in `edge://extensions` or `chrome://extensions` and load the unpacked extension. Enter the one-time code from the desktop's Diagnostics page in the popup to pair without a Native Host. Closing the desktop window ends the pairing. Use a new code the next time you open it.

Use the keyboard to select and send a candidate, or expand the arrow at the end of its row to copy the full link. Supported live pages offer “Resolve and send”. After navigating or reloading, reopen the popup to scan again. Once the desktop accepts the request, check Stage → Sources for playback status.

The extension can send a resolved HTTP(S) address from Bilibili live rooms and supported Twitch, Kick, and YouTube pages. The address may be signed. An opaque cross-origin response can show that a mirror answered. The desktop still reads the playlist to determine whether it can open it.

The desktop refuses encryption declared by `#EXT-X-KEY` or `#EXT-X-SESSION-KEY`. `METHOD=NONE` indicates an unencrypted playlist. A media playlist without `#EXT-X-ENDLIST` opens as live and starts automatically. The desktop refuses masters with a separate audio playlist because the player opens one address at a time.

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
