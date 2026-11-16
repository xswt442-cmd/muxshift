# MuxShift bridge

A Manifest V3 extension that hands one address a page already plays over to the
MuxShift desktop window. It reads the page only after the user clicks, and it
never copies cookies, request headers, or keys.

## What it does

- The popup scans the active tab after a click and lists the `src` of the page's
  `video`, `audio`, and `source` elements, plus media-looking resource entries.
- Choosing one address and pressing **Import the selected address** sends
  `{url: ...}` to the window's own loopback listener, `POST http://127.0.0.1:8731/import`,
  with the window's token in `X-MuxShift-Handoff`.
- The window runs the same add command its own Sources page runs, so an address
  the extension hands over lands in the same session, with the same errors, as
  one typed in by hand.

There is no native messaging host. The window prints a six-digit pairing code
on the Diagnostics page; typing it in the popup once exchanges it for a token
kept in `chrome.storage.local`. A pairing code is used once and replaced by the
window. The token and the code live only in the window's memory, so closing the
window ends the pairing and the next launch shows a new code.

The extension does not capture a tab's stream. A capture produces a live
`MediaStream` that only the browser can decode, and the desktop takes playable
addresses and files; the popup says so instead of pretending.

## Limits that are part of the design

- `blob:` and `mediasource:` addresses belong to the page. They are listed with
  a note and refused, because nothing outside the page can read them.
- An address with credentials in it is refused by the window, and the log keeps
  only the scheme, host, and path of an imported address, because a signed
  address carries its credential in the query.
- The listener only accepts its own `Host`, an empty `Origin` or one starting
  with `chrome-extension://`, and a JSON body under 8 KiB. None of that is
  authentication against another process on this machine; the token is what
  keeps such a process from importing anything the user did not choose.
- A cross-origin address is played by the window under that origin's normal
  rules. When the origin does not allow reading its segments, playback fails and
  the window reports why; the extension does not proxy around it.
- Protected media is not touched: no DRM keys, no decryption, no header replay.
- Permissions are `activeTab`, `scripting`, and `storage`, plus
  `host_permissions` for `http://127.0.0.1:8731/*` only. The extension cannot
  read any page until the user clicks it, and it cannot reach any other host.

## Extension ID

`manifest.json` carries a `key`, so the ID is fixed whatever directory the
extension is loaded from: `agacmebmmcbkclbblmekgdkfcehehccl`. This version does
not use a native host, so nothing else depends on that ID; it is kept so a
profile that already trusts the extension keeps the same identity.

## Pairing, once per window

1. Start the desktop window. Its Diagnostics page shows
   `127.0.0.1:8731 · pairing code 123456 (expires in 2m)`.
2. Load the extension unpacked (below), open its popup and type that code.
3. Pick an address in the popup and press **Import the selected address**.

If the window is closed or restarted, the extension reports that the window is
not answering; a new window shows a new code, so pair again.

## Loading the extension

Edge and Chrome load an unpacked extension from `edge://extensions` or
`chrome://extensions` with developer mode on, or Playwright can start a browser
with `--load-extension` (see `testplace/.playwright/cli-ext.config.json`).

## Source layout

- `manifest.json`, `background.js`, `popup.html`, `popup.js`: the extension.
- `icons/`: the toolbar icons.
- `host/main.go`: the older native-messaging host. This version of the extension
  does not use it and does not request `nativeMessaging`; it is kept in the tree
  for the web session's bridge-file path and can be deleted when that path stops
  using it.
