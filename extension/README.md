# MuxShift bridge

A Manifest V3 extension that hands one address a page already plays over to a
local MuxShift desktop or web session. It reads the page only after the user
clicks, and it never copies cookies, request headers, or keys.

## What it does

- The popup scans the active tab after a click and lists the `src` of the page's
  `video`, `audio`, and `source` elements, plus media-looking resource entries.
- Choosing one address and pressing **Import the selected address** sends
  `{type: "import", url: ...}` to the native host `com.muxshift.bridge`.
- The host forwards the address to `POST /api/import` on the running session,
  using the bridge token from the bridge file as the `X-MuxShift-Bridge` header,
  so neither the extension nor any page ever holds it. The token a page uses for
  its own session is a different value, and it never authorizes an import.

The extension does not capture a tab's stream. A capture produces a live
`MediaStream` that only the browser can decode, and the local session takes
playable addresses and files; the popup says so instead of pretending.

## Limits that are part of the design

- `blob:` and `mediasource:` addresses belong to the page. They are listed with
  a note and refused, because nothing outside the page can read them.
- An address with credentials in it is refused by the host and by the session.
  The session's log keeps only the scheme, host, and path of an imported
  address, because a signed address carries its credential in the query.
- The host checks the caller origin in its first argument against the ID in
  `allowed_origins`. That is what the browser enforces between an extension and
  its host; it is not authentication against another process on this machine
  that decides to start the host itself. The bridge token is what keeps such a
  process from importing anything the user did not choose.
- A cross-origin address is played by the page under the normal CORS rules of
  that origin. When the origin does not allow the page to read its segments,
  playback fails and the page reports the hls.js error; the extension does not
  proxy around it.
- Protected media is not touched: no DRM keys, no decryption, no header
  replay.
- Permissions are `activeTab`, `scripting`, and `nativeMessaging`. There is no
  host permission, so the extension cannot read any page until the user clicks
  it.

## Extension ID

`manifest.json` carries a `key`, so the ID is fixed whatever directory the
extension is loaded from: `agacmebmmcbkclbblmekgdkfcehehccl`. The native host
manifest allows exactly that origin.

## Installing the native host on Windows

1. Build the host from the repository root:

   ```sh
   go build -o testplace/bin/muxshift-host.exe ./extension/host
   ```

2. Write a host manifest with the absolute host path and this `allowed_origins`:

   ```json
   {
     "name": "com.muxshift.bridge",
     "description": "Forwards one media address to a local MuxShift session",
     "path": "C:\\path\\to\\muxshift-host.exe",
     "type": "stdio",
     "allowed_origins": ["chrome-extension://agacmebmmcbkblmekgdkfcehehccl/"]
   }
   ```

   Keep it outside the repository; `testplace/ext-host/` is one working
   example.

3. Register the manifest for Edge (or Chrome, under
   `HKCU\Software\Google\Chrome\NativeMessagingHosts`):

   ```powershell
   reg add "HKCU\Software\Microsoft\Edge\NativeMessagingHosts\com.muxshift.bridge" `
     /ve /t REG_SZ /d "C:\path\to\com.muxshift.bridge.json" /f
   ```

4. Start the web session. It writes the bridge file the host reads
   (`%LOCALAPPDATA%\MuxShift\bridge.json`, or `-token-file`), holding the
   session URL and token with owner-only permissions.

## Loading the extension

Edge and Chrome load an unpacked extension from `edge://extensions` or
`chrome://extensions` with developer mode on, or Playwright can start a browser
with `--load-extension` (see `testplace/.playwright/cli-ext.config.json`).
