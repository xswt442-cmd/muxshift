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

Every request needs the token. The page's bootstrap address carries it once as
`?token=`, the server turns it into an HttpOnly `SameSite=Strict` cookie, and the
page strips it from the address bar immediately; after that the cookie
authorizes the page, and the `X-MuxShift-Token` header is what the native host
uses. Changing the scene additionally requires a JSON body and an `Origin` that
equals the request host, so a form post or a page on another origin cannot drive
the session with the cookie the browser happens to hold. Every request must also
name a loopback `Host`, which keeps a name that resolves to this machine from
reaching the session. `-addr` is checked at startup and refused unless it is a
loopback address. The media path is opened through `os.OpenRoot`, so neither
`..` nor a symbolic link or junction inside `-media-root` can escape it. A scene
change is applied in full or rejected in full, and a rejected volume leaves the
previous scene in place.

Playback uses [hls.js](https://github.com/video-dev/hls.js) (Apache-2.0, light
build 1.7.4, vendored in `vendor/`) on top of Media Source Extensions; MP4 files
go straight into a media element. The exact source, size, and SHA-256 of the
vendored build are recorded in `vendor/README.md`. Browsers require a click
before sound, so all sources start muted and the first click unmutes the source
the session says is heard.

The page keeps the stage and the session side by side:

- the stage holds the picture of the source the scene selects, with a transport
  that plays, pauses, steps ten seconds, scrubs inside the buffered range, and
  shows the buffer on the progress bar;
- each source is a card with a live thumbnail, a state dot, `PICTURE`/`SOUND`
  badges, and Show, Hear, Peek, and Retry buttons;
- `peek` floats another source over the stage as a small window that can be
  dragged and made more or less transparent; it moves the video element, so the
  player keeps running, and it changes nothing in the session;
- the scene slots under the cards name the picture and the sound the Go session
  currently holds;
- an events list records what the page did, and the keyboard drives the same
  transport (`space`, `←`/`→`, `M`, `P`, `1`–`9`).

What this client does not do, and does not pretend to do:

- it does not align two live sources on a shared timeline;
- it does not buffer or replay history;
- it cannot seek beyond what the browser has buffered, and a live playlist
  starts at the live edge;
- it reports playback errors that hls.js or the media element surface, and
  nothing more.

The static files and their limits are described in the top-level README and the
architecture document.
