// The service worker talks to the desktop window over a loopback address. No
// native host is installed: the window prints a pairing code and this extension
// is given a token for it, once. Media never travels this way 鈥?only the address
// the user selected.
const PORT = 8731;
const FALLBACK_BASE = `http://127.0.0.1:${PORT}`;

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type === "scan") {
    scan().then(respond, (error) => respond({ error: String(error?.message || error) }));
    return true;
  }
  if (message?.type === "pair") {
    pair(message.code).then(respond, (error) => respond({ ok: false, reason: String(error?.message || error) }));
    return true;
  }
  if (message?.type === "status") {
    status().then(respond, (error) => respond({ paired: false, reason: String(error?.message || error) }));
    return true;
  }
  if (message?.type === "import") {
    importAddress(message.url).then(respond, (error) =>
      respond({ ok: false, reason: String(error?.message || error) }),
    );
    return true;
  }
  if (message?.type === "resolve") {
    resolveLive().then(respond, (error) => respond({ error: String(error?.message || error) }));
    return true;
  }
  return false;
});

// scan asks the active tab what media it is presenting. The injected function
// only reads the document and the resource timings it already has; it does not
// fetch anything, read cookies, or look at headers.
async function scan() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id) {
    return { error: "no active tab" };
  }
  const results = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: collectMedia,
  });
  const found = results?.[0]?.result || { sources: [], notes: [] };
  return { tab: { id: tab.id, title: tab.title, url: tab.url }, ...found };
}

// collectMedia runs inside the page.
function collectMedia() {
  const notes = [];
  const sources = [];
  const seen = new Set();

  const add = (url, kind, from) => {
    if (!url || seen.has(url)) return;
    let parsed;
    try {
      parsed = new URL(url, location.href);
    } catch {
      return;
    }
    if (parsed.protocol === "blob:") {
      seen.add(url);
      notes.push({
        kind: "blob",
        detail: "A media element is playing a Media Source blob; the page keeps the segments, so there is no address to import.",
      });
      return;
    }
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      seen.add(url);
      notes.push({ kind: parsed.protocol, detail: "Only http and https addresses can be imported." });
      return;
    }
    if (parsed.username || parsed.password) {
      seen.add(url);
      notes.push({ kind: "credentials", detail: "The address carries credentials, so it is refused." });
      return;
    }
    seen.add(url);
    sources.push({ url: parsed.href, kind, from });
  };

  for (const element of document.querySelectorAll("video, audio")) {
    const tag = element.tagName.toLowerCase();
    const current = element.currentSrc || element.src || "";
    const kind = current.includes(".m3u8")
      ? "hls"
      : current.startsWith("blob:")
        ? "blob"
        : current.includes(".mpd")
          ? "dash"
          : tag === "audio"
            ? "audio"
            : "file";
    add(current, kind, `${tag} element`);
    for (const source of element.querySelectorAll("source")) {
      add(source.src, source.src.includes(".m3u8") ? "hls" : source.type || "file", `${tag} > source`);
    }
  }

  // Resource timings name every piece the player fetched. A segment is a piece
  // of a stream, not a stream: handing one over would import a fragment that
  // cannot play on its own, so segments are counted and never offered.
  const extensions = [".m3u8", ".mpd", ".mp4", ".webm", ".m4s", ".ts"];
  let segments = 0;
  for (const entry of performance.getEntriesByType("resource")) {
    const name = entry.name || "";
    const clean = name.split("?")[0].toLowerCase();
    if (!extensions.some((ending) => clean.endsWith(ending))) continue;
    if (new URL(name, location.href).protocol === "blob:") continue;
    if (clean.endsWith(".ts") || clean.endsWith(".m4s")) {
      segments += 1;
      continue;
    }
    const kind = clean.endsWith(".m3u8") ? "hls" : clean.endsWith(".mpd") ? "dash" : "file";
    add(name, kind, "resource timing");
  }
  if (segments) {
    notes.push({
      kind: "segments",
      detail: `The page fetched ${segments} media segment(s). A segment is part of a stream, so it is not offered on its own; a playlist on the page is what can be handed over.`,
    });
  }

  if (!sources.length) {
    notes.push({
      kind: "none",
      detail: "No importable address was found. A player that keeps its segments in a blob, or one whose media is protected, cannot be handed over.",
    });
  }
  if (document.querySelector("video[src^='blob:'], video source[src^='blob:']")) {
    notes.push({
      kind: "blob",
      detail: "This page plays through Media Source: the picture is presented, but there are no original segments to import.",
    });
  }
  return { sources, notes, page: { title: document.title, url: location.href } };
}

// stored returns the paired address and token, or null when this browser has
// not paired with a window yet.
async function stored() {
  const { handoff } = await chrome.storage.local.get("handoff");
  if (!handoff?.token || !handoff?.address) return null;
  return handoff;
}

// base turns a paired address into one fetch can use. An address stored before
// the window answered with a scheme is still a loopback address.
function base(address) {
  return /^https?:\/\//i.test(address) ? address : `http://${address}`;
}

// request sends one JSON request to the window and returns its JSON answer.
async function request(base, path, body, token) {
  const headers = { "Content-Type": "application/json" };
  if (token) headers["X-MuxShift-Handoff"] = token;
  const response = await fetch(`${base}${path}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body || {}),
  });
  const text = await response.text();
  let parsed = {};
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    parsed = { reason: text };
  }
  if (!response.ok) {
    const error = new Error(parsed.reason || parsed.error || `the window answered ${response.status}`);
    error.status = response.status;
    throw error;
  }
  return parsed;
}

// pair exchanges the code shown in the window for a token. The code is used
// once and then replaced by the window.
async function pair(code) {
  const cleaned = String(code || "").trim();
  if (!/^\d{6}$/.test(cleaned)) {
    throw new Error("the pairing code is six digits, as shown in the window");
  }
  const answer = await request(FALLBACK_BASE, "/pair", { code: cleaned });
  if (!answer.token) {
    throw new Error("the window did not return a token");
  }
  const address = answer.address || FALLBACK_BASE;
  await chrome.storage.local.set({ handoff: { address, token: answer.token } });
  return { ok: true, address };
}

// status reports whether this browser is paired and whether the window answers.
async function status() {
  const handoff = await stored();
  if (!handoff) {
    return { paired: false, reason: "this browser has not paired with a window yet" };
  }
  try {
    const response = await fetch(`${base(handoff.address)}/health`);
    if (!response.ok) throw new Error(`the window answered ${response.status}`);
  } catch (error) {
    return { paired: true, reachable: false, reason: "the window is not answering; is MuxShift running?" };
  }
  return { paired: true, reachable: true, address: handoff.address };
}

// resolveLive asks the page to trade its own session for one address the window
// can fetch by itself. The trade happens inside the page: the site's own API is
// called with the tab's cookies, and only the resulting playlist address leaves
// the browser. No cookie, storage item, or request header is read or exported.
async function resolveLive() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id) {
    return { error: "no active tab" };
  }
  const results = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: resolveLivePage,
  });
  const found = results?.[0]?.result || { error: "the page did not answer" };
  return { tab: { id: tab.id, title: tab.title, url: tab.url }, ...found };
}

// resolveLivePage runs inside the page. It knows exactly two sites, and says so
// rather than guessing on any other: a resolver is a small piece of site
// knowledge, and a wrong guess hands the window an address that cannot play.
async function resolveLivePage() {
  const host = location.hostname;
  const site = host.endsWith("bilibili.com")
    ? "bilibili"
    : host === "twitch.tv" || host.endsWith(".twitch.tv")
      ? "twitch"
      : "";
  if (!site) {
    return {
      url: "",
      note:
        "This extension resolves live addresses for Bilibili and Twitch only. This page is neither, so scan it for a playlist instead.",
    };
  }
  try {
    return site === "bilibili" ? await resolveBilibiliLive() : await resolveTwitchLive();
  } catch (error) {
    return { url: "", note: `The page could not trade its session for an address: ${String(error?.message || error)}` };
  }

  // resolveBilibiliLive asks the live API for the stream this room is presenting.
  // The h5 platform is asked for on purpose: it answers with HLS, which the
  // window can read, while the web platform answers with a raw FLV address that
  // only plays when a Referer is supplied by whatever fetches it.
  async function resolveBilibiliLive() {
    const fromPath = (location.pathname.match(/^\/(?:blanc\/)?(\d+)/) || [])[1];
    const bootstrap = window.__NEPTUNE_IS_MY_WAIFU__;
    const room = fromPath || String(bootstrap?.roomInitRes?.data?.room_id || "");
    if (!room) {
      return { url: "", note: "This page does not name a live room, so there is no room to ask about." };
    }
    const endpoint = `https://api.live.bilibili.com/room/v1/Room/playUrl?cid=${room}&qn=10000&platform=h5`;
    const answer = await fetch(endpoint, { credentials: "include" });
    if (!answer.ok) {
      return { url: "", note: `Bilibili answered ${answer.status} for room ${room}.` };
    }
    const body = await answer.json();
    if (body?.code !== 0) {
      return { url: "", note: `Bilibili answered code ${body?.code}: ${body?.message || "no reason given"}` };
    }
    const urls = [];
    const streams = body?.data?.playurl_info?.playurl?.stream || [];
    for (const stream of streams) {
      for (const format of stream.format || []) {
        for (const codec of format.codec || []) {
          urls.push(`${codec?.url_info?.host || ""}${codec?.base_url || ""}`);
        }
      }
    }
    for (const piece of body?.data?.durl || []) {
      urls.push(piece?.url || "");
    }
    const mirrors = urls.filter((candidate) => candidate.includes(".m3u8"));
    if (!mirrors.length) {
      return {
        url: "",
        note: `Room ${room} answered ${urls.length} address(es) but none is HLS; this build does not import FLV.`,
      };
    }
    const reachable = await firstReachable(mirrors);
    return {
      url: reachable,
      note:
        mirrors.length > 1
          ? `Live room ${room}, HLS address signed by the site (${mirrors.length} mirrors, the first that answered was kept).`
          : `Live room ${room}, HLS address signed by the site.`,
    };
  }

  // firstReachable asks each mirror for its playlist without reading the answer.
  // A mirror that does not resolve rejects the fetch, which is the only signal a
  // cross-origin page can get; an answer of any kind means the host is there.
  async function firstReachable(candidates) {
    for (const candidate of candidates) {
      const answered = await Promise.race([
        fetch(candidate, { mode: "no-cors", cache: "no-store" }).then(
          () => true,
          () => false,
        ),
        new Promise((resolve) => setTimeout(() => resolve(false), 4000)),
      ]);
      if (answered) return candidate;
    }
    return candidates[0] || "";
  }

  // resolveTwitchLive asks Twitch's own GraphQL for the playback token. The page
  // cannot be scanned for it: the player fetches the stream from a worker, so no
  // address ever appears in the document or in its resource timings.
  async function resolveTwitchLive() {
    const login = (location.pathname.match(/^\/([^/?#]+)/) || [])[1] || "";
    if (!login || ["directory", "videos", "settings", "search"].includes(login)) {
      return { url: "", note: "This page is not a channel, so there is no stream to ask about." };
    }
    const query =
      "query($login:String!){streamPlaybackAccessToken(channelName:$login,params:{platform:\"web\",playerBackend:\"mediaplayer\",playerType:\"site\"}){value signature}}";
    // No credentials on purpose. Twitch's GraphQL endpoint accepts a
    // cross-origin POST without them (200) and refuses one with them (the
    // browser reports "Failed to fetch"), so a logged-in token cannot be asked
    // for from a page. The token that comes back is anonymous, which is enough
    // for a public channel and not enough for one that needs the viewer's
    // entitlement; that case is refused at the window, by probing the address.
    const answer = await fetch("https://gql.twitch.tv/gql", {
      method: "POST",
      credentials: "omit",
      headers: {
        "Client-ID": "kimne78kx3ncx6brgo4mv6wki5h1ko",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ query, variables: { login } }),
    });
    if (!answer.ok) {
      return { url: "", note: `Twitch answered ${answer.status} for channel ${login}.` };
    }
    const body = await answer.json();
    const token = body?.data?.streamPlaybackAccessToken;
    if (!token?.value || !token?.signature) {
      return { url: "", note: `Twitch says ${login} is not live right now.` };
    }
    const params = new URLSearchParams({
      allow_source: "true",
      allow_audio_only: "true",
      fast_bread: "true",
      p: "1234567",
      player: "twitchweb",
      playlist_include_framerate: "true",
      segment_queue_len: "0",
      sig: token.signature,
      token: token.value,
      supported_codecs: "av1,h264",
    });
    return {
      url: `https://usher.ttvnw.net/api/channel/hls/${encodeURIComponent(login)}.m3u8?${params.toString()}`,
      note: `Live channel ${login}, HLS address carrying Twitch's own token.`,
    };
  }
}

// importAddress hands one selected address to the window's own add command.
// A pairing the window no longer knows (it restarted, or the code was replaced)
// is dropped here, so the popup can ask for a fresh code instead of repeating
// a request that cannot succeed.
async function importAddress(url) {
  if (!url) throw new Error("no address was selected");
  const handoff = await stored();
  if (!handoff) {
    throw new Error("pair with the window first: open its Diagnostics page and type its code");
  }
  let answer;
  try {
    answer = await request(base(handoff.address), "/import", { url }, handoff.token);
  } catch (error) {
    if (error.status === 401 || error.status === 403) {
      await chrome.storage.local.remove("handoff");
      throw new Error("this browser is no longer paired with the window; open its Diagnostics page for a new code");
    }
    if (error instanceof TypeError) {
      throw new Error("the window is not answering; start MuxShift and pair again if it asks");
    }
    throw error;
  }
  return { ok: true, message: answer.message || "the window accepted the address" };
}
