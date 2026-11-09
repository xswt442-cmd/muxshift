# Web client

This directory holds a minimal web client and the loopback-only Go service that
serves it. It is one Go package, `main`, plus static files, so the whole client
builds with the rest of the module:

```sh
go run ./web -media-root <directory> -media /media/<playlist or file> [-media ...]
```

The service prints `http://127.0.0.1:<port>/?token=<token>` and offers:

- `GET /api/session` — the Core session the page reads: sources, their tracks,
  and the scene that currently decides the picture and the heard sound.
- `POST /api/scene` — `{"video": "<source>", "audio": "<source>", "volume": 1,
  "muted": false}`; it applies the change through `Session.SetSceneVideo` and
  `Session.SetSceneAudio` and returns the new scene.
- `/media/...` — the addresses given with `-media`, read from `-media-root` and
  refused outside it.

Every request needs the token, as `?token=`, as the `X-MuxShift-Token` header,
or as the cookie the page sets. The service binds to `127.0.0.1` and its
responses carry no CORS header, so a page from another origin cannot read or
drive it.

Playback uses [hls.js](https://github.com/video-dev/hls.js) (Apache-2.0, light
build 1.7.4, vendored in `vendor/`) on top of Media Source Extensions; MP4 files
go straight into a media element. Browsers require a click before sound, so all
sources start muted and the first click unmutes the source the session says is
heard.

What this client does not do, and does not pretend to do:

- it does not align two live sources on a shared timeline;
- it does not buffer or replay history;
- it cannot seek beyond what the browser has buffered, and a live playlist
  starts at the live edge;
- it reports playback errors that hls.js or the media element surface, and
  nothing more.

The static files and their limits are described in the top-level README and the
architecture document.
