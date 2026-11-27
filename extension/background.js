// The service worker talks to the desktop window over a loopback address. No
// native host is installed: the window prints a pairing code and this extension
// is given a token for it, once. Media never travels this way — only the address
// the user selected.
const PORT = 8731;
const FALLBACK_BASE = `http://127.0.0.1:${PORT}`;
const DESKTOP_TIMEOUT = 6000;

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (sender?.id !== chrome.runtime.id || sender.tab) return false;
  if (message?.type === "scan") {
    scan().then(respond, (error) => respond({ error: safeReason(error?.message) || "The page could not be read." }));
    return true;
  }
  if (message?.type === "pair") {
    pair(message.code).then(respond, (error) => respond({ ok: false, reason: safeReason(error?.message) }));
    return true;
  }
  if (message?.type === "status") {
    status().then(respond, (error) => respond({ paired: false, reason: safeReason(error?.message) }));
    return true;
  }
  if (message?.type === "import") {
    importAddress(message.url, message.target).then(respond, (error) => respond({ ok: false, reason: safeReason(error?.message) }));
    return true;
  }
  if (message?.type === "resolve") {
    resolveLive(message.target).then(respond, (error) => respond({ error: safeReason(error?.message) || "The page resolver could not run." }));
    return true;
  }
  return false;
});

// activeTab finds the tab the popup acts on. Chromium may omit currentWindow
// when no browser window has focus, so the fallbacks stay narrow enough that a
// second active tab cannot become an accidental target.
async function activeTab() {
  let [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab) {
    [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  }
  if (!tab) {
    const active = await chrome.tabs.query({ active: true });
    if (active.length === 1) [tab] = active;
  }
  return tab;
}

// scan asks the active tab what media it is presenting. The injected function
// only reads the document and the resource timings it already has; it does not
// fetch anything, read cookies, or look at headers.
async function scan() {
  const tab = await activeTab();
  if (!tab?.id) {
    return { error: "no active tab" };
  }
  const results = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: pageMedia,
    args: ["scan"],
  });
  const entry = results?.[0];
  const found = entry?.result || { sources: [], notes: [] };
  const url = found.page?.url || tab.url;
  const current = await activeTab();
  if (current?.id !== tab.id || url !== current.url) return { failure: "stale-tab" };
  if (entry?.documentId) {
    try {
      const [stillThere] = await chrome.scripting.executeScript({
        target: { tabId: tab.id, documentIds: [entry.documentId] },
        func: () => location.href,
      });
      if (stillThere?.result !== url) return { failure: "stale-tab" };
    } catch {
      return { failure: "stale-tab" };
    }
  }
  const sources = found.sources.filter(source => pageMedia("address", source.url));
  return { tab: { id: tab.id, windowId: tab.windowId, title: tab.title, url, documentId: entry?.documentId }, ...found, sources };
}

function scriptTarget(target) {
  return target.documentId ? { tabId: target.id, documentIds: [target.documentId] } : { tabId: target.id };
}

async function sameActivePage(target) {
  if (!target?.id || !target?.url) return false;
  let tab = await activeTab();
  if (tab?.id !== target.id || tab.url !== target.url) return false;
  try {
    const [entry] = await chrome.scripting.executeScript({
      target: scriptTarget(target),
      func: () => location.href,
    });
    if (entry?.result !== target.url || (target.documentId && entry.documentId !== target.documentId)) return false;
  } catch {
    return false;
  }
  tab = await activeTab();
  return tab?.id === target.id && tab.url === target.url;
}

// executeScript runs a copy of this function. Its helpers must be local to it
// so both the worker and the two script worlds use the same address rules.
function pageMedia(action, ...args) {
  switch (action) {
    case "address": return mediaAddress(...args);
    case "fetch": return fetchWithin(...args);
    case "scan": return collectMedia();
    case "resolve": return resolveLivePage(...args);
    default: throw new Error("unknown media operation");
  }

  function mediaAddress(value, base) {
    if (typeof value !== "string" || !value.trim()) return null;
    try {
      const parsed = new URL(value, base);
      return isMediaAddress(parsed) ? parsed : null;
    } catch {
      return null;
    }
  }

  function isMediaAddress(parsed) {
    return ["http:", "https:"].includes(parsed.protocol) &&
      !!parsed.hostname && !parsed.username && !parsed.password;
  }

  // Keep the deadline active until the response body is consumed.
  async function fetchWithin(url, init, timeoutMs, read) {
    const timer = new AbortController();
    const timeout = setTimeout(() => timer.abort(), timeoutMs);
    const signal = init.signal ? AbortSignal.any([timer.signal, init.signal]) : timer.signal;
    try {
      const response = await fetch(url, { ...init, signal });
      return await read(response);
    } finally {
      clearTimeout(timeout);
    }
  }

  // collectMedia runs inside the page.
  function collectMedia() {
    const notes = [];
    const candidates = new Map();

    const add = (url, type, fallback, from) => {
      if (!url) return;
      let parsed;
      try {
        parsed = new URL(url, location.href);
      } catch {
        return;
      }
      const existing = candidates.get(parsed.href);
      if (existing === null) return;
      if (parsed.protocol === "blob:") {
        candidates.set(parsed.href, null);
        notes.push({
          kind: "blob",
          detail: "A media element is playing a Media Source blob; the page keeps the segments, so there is no address to import.",
        });
        return;
      }
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        candidates.set(parsed.href, null);
        notes.push({ kind: parsed.protocol, detail: "Only http and https addresses can be imported." });
        return;
      }
      if (!isMediaAddress(parsed)) {
        candidates.set(parsed.href, null);
        notes.push({ kind: "credentials", detail: "The address carries credentials, so it is refused." });
        return;
      }
      const pathname = parsed.pathname.toLowerCase();
      const mime = (type || "").split(";")[0].trim().toLowerCase();
      let kind;
      if (pathname.endsWith(".m3u8") || /^(application|audio)\/(vnd\.apple\.mpegurl|x-mpegurl|mpegurl)$/.test(mime)) kind = "hls";
      else if (pathname.endsWith(".mpd") || mime === "application/dash+xml") kind = "dash";
      else if (mime.startsWith("audio/")) kind = "audio";
      if (!existing) {
        candidates.set(parsed.href, { url: parsed.href, kind: kind || fallback, from });
      } else if (kind && existing.kind !== "hls" && existing.kind !== "dash") {
        existing.kind = kind;
      }
    };

    for (const element of document.querySelectorAll("video, audio")) {
      const tag = element.tagName.toLowerCase();
      const kind = tag === "audio" ? "audio" : "file";
      add(element.currentSrc || element.src, "", kind, `${tag} element`);
      for (const source of element.querySelectorAll("source")) {
        add(source.src, source.type, kind, `${tag} > source`);
      }
    }

    // Resource timings name every piece the player fetched. A segment is a piece
    // of a stream, not a stream: handing one over would import a fragment that
    // cannot play on its own, so segments are counted and never offered.
    const extensions = [".m3u8", ".mpd", ".mp4", ".webm", ".m4s", ".ts"];
    let segments = 0;
    for (const entry of performance.getEntriesByType("resource")) {
      const name = entry.name || "";
      const parsed = mediaAddress(name, location.href);
      if (!parsed) continue;
      const clean = parsed.pathname.toLowerCase();
      if (!extensions.some((ending) => clean.endsWith(ending))) continue;
      if (clean.endsWith(".ts") || clean.endsWith(".m4s")) {
        segments += 1;
        continue;
      }
      add(parsed.href, "", "file", "resource timing");
    }
    if (segments) {
      notes.push({
        kind: "segments",
        detail: `The page fetched ${segments} media segment(s). A segment is part of a stream, so it is not offered on its own; a playlist on the page is what can be handed over.`,
      });
    }

    const sources = [...candidates.values()].filter(Boolean);
    if (!sources.length) {
      notes.push({
        kind: "none",
        detail: "No importable address was found. A player that keeps its segments in a blob, or one whose media is protected, cannot be handed over.",
      });
    }
    return { sources, notes, page: { title: document.title, url: location.href } };
  }

  // resolveLivePage runs inside the page. It knows a handful of sites and says so
  // rather than guessing on any other: a resolver is a small piece of site
  // knowledge, and a wrong guess hands the window an address that cannot play.
  //
  // Every site is one entry in the table below. An entry says how to name what the
  // page is showing, how to get an answer (the site's own API, or what the page
  // already holds), and how to turn that answer into candidate addresses. The HLS
  // filter, the mirror race and the note are shared, so a new site is a short
  // description rather than another copy of the same procedure.
  async function resolveLivePage(expectedURL) {
    if (location.href !== expectedURL) return { failure: "stale-tab" };
    const controller = new AbortController();
    const cancelForNavigation = () => controller.abort();
    const SITES = [
      {
        name: "Bilibili",
        hosts: ["live.bilibili.com"],
        subdomains: false,
        missing: "This page does not name a live room, so there is no room to ask about.",
        subject: () => {
          const fromPath = (location.pathname.match(/^\/(?:blanc\/)?(\d+)/) || [])[1];
          const bootstrap = window.__NEPTUNE_IS_MY_WAIFU__;
          return fromPath || String(bootstrap?.roomInitRes?.data?.room_id || "") || "";
        },
        // The h5 platform is asked for on purpose: it answers with HLS, which the
        // window can read, while the web platform answers with a raw FLV address
        // that only plays when a Referer comes from whatever fetches it.
        request: (room) => ({
          url: `https://api.live.bilibili.com/room/v1/Room/playUrl?cid=${room}&qn=10000&platform=h5`,
          credentials: "include",
        }),
        addresses: (body) => {
          if (body?.code !== 0) {
            throw new Error(Number.isInteger(body?.code) ? `Bilibili answered code ${body.code}.` : "Bilibili did not return stream information.");
          }
          const listed = [];
          for (const stream of body?.data?.playurl_info?.playurl?.stream || []) {
            for (const format of stream.format || []) {
              for (const codec of format.codec || []) {
                if (!Array.isArray(codec?.url_info) || typeof codec.base_url !== "string") continue;
                for (const info of codec.url_info) {
                  if (typeof info?.host === "string") {
                    listed.push(info.host + codec.base_url + (typeof info.extra === "string" ? info.extra : ""));
                  }
                }
              }
            }
          }
          for (const piece of body?.data?.durl || []) listed.push(piece?.url || "");
          return listed;
        },
        noHls: (room, count) => `Room ${room} answered ${count} address(es) but none is HLS; this build does not import FLV.`,
        describe: (room, count) =>
          count > 1
            ? `Live room ${room}, HLS address signed by the site (${count} mirrors, the first that answered was kept).`
            : `Live room ${room}, HLS address signed by the site.`,
      },
      {
        name: "Twitch",
        hosts: ["twitch.tv"],
        missing: "This page is not a channel, so there is no stream to ask about.",
        subject: () => {
          const login = (location.pathname.match(/^\/([^/?#]+)/) || [])[1] || "";
          return ["", "directory", "videos", "settings", "search"].includes(login) ? "" : login;
        },
        // The page cannot be scanned for the address: the player fetches the
        // stream from a worker, so nothing appears in the document or in its
        // resource timings. The token is asked for without credentials on
        // purpose: Twitch's GraphQL refuses a credentialed cross-origin POST
        // before it is sent ("Failed to fetch"), so an anonymous token is the only
        // one a page can get. A public channel plays with it; one that needs the
        // viewer's entitlement is refused at the window, by probing the address.
        request: (login) => ({
          url: "https://gql.twitch.tv/gql",
          method: "POST",
          credentials: "omit",
          headers: { "Client-ID": "kimne78kx3ncx6brgo4mv6wki5h1ko", "Content-Type": "application/json" },
          body: JSON.stringify({
            query:
              "query($login:String!){streamPlaybackAccessToken(channelName:$login,params:{platform:\"web\",playerBackend:\"mediaplayer\",playerType:\"site\"}){value signature}}",
            variables: { login },
          }),
        }),
        addresses: (body, login) => {
          const token = body?.data?.streamPlaybackAccessToken;
          if (!token?.value || !token?.signature) return [];
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
          return [`https://usher.ttvnw.net/api/channel/hls/${encodeURIComponent(login)}.m3u8?${params.toString()}`];
        },
        noHls: (login) => `Twitch says ${login} is not live right now.`,
        describe: (login) => `Live channel ${login}, HLS address carrying Twitch's own token.`,
      },
      {
        name: "YouTube",
        hosts: ["youtube.com", "youtu.be"],
        missing: "This page does not name a video, so there is nothing to ask about.",
        subject: () => {
          const [section, id] = location.pathname.split("/").filter(Boolean);
          if (location.hostname === "youtu.be") return section || "";
          if (section === "live" || section === "shorts") return id || "";
          return section === "watch" ? new URL(location.href).searchParams.get("v") || "" : "";
        },
        // Nothing is requested: the page already holds the answer it was given.
        // A manifest is there only when the session this page plays as is allowed
        // to watch, so a page that cannot play simply has none; that is reported
        // rather than guessed at.
        read: (subject) => {
          const response = window.ytInitialPlayerResponse;
          return response?.videoDetails?.videoId === subject ? [response?.streamingData?.hlsManifestUrl] : [];
        },
        noHls: () =>
          "This page carries no live HLS manifest; YouTube adds one only for a stream this session is allowed to play.",
        describe: () => "Live video, HLS manifest taken from the page's own player response.",
      },
      {
        name: "Kick",
        hosts: ["kick.com"],
        missing: "This page is not a channel, so there is no stream to ask about.",
        subject: () => {
          const slug = (location.pathname.match(/^\/([^/?#]+)/) || [])[1] || "";
          return ["", "browse", "categories", "following", "search", "video"].includes(slug) ? "" : slug;
        },
        // The page's own API names the address; its answer is the same one the
        // player uses. A channel that is not live still returns a playback address
        // that no longer plays, so liveness is read from `livestream` first.
        request: (slug) => ({ url: `https://kick.com/api/v2/channels/${encodeURIComponent(slug)}` }),
        addresses: (body) => (body?.livestream ? [body.playback_url || ""] : []),
        noHls: (slug) => `Kick says ${slug} is not live right now.`,
        describe: (slug) => `Live channel ${slug}, HLS address carrying Kick's own token.`,
      },
    ];

    const host = location.hostname;
    const site = SITES.find((entry) => entry.hosts.some((known) =>
      host === known || (entry.subdomains !== false && host.endsWith(`.${known}`))));
    if (!site) {
      const names = SITES.map((entry) => entry.name);
      const list = names.length > 1 ? `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}` : names[0];
      return {
        url: "",
        note: `This extension resolves live addresses for ${list} only. This page is none of them, so scan it for a playlist instead.`,
      };
    }
    const navigationEvents = ["pagehide", "popstate", "hashchange"];
    for (const event of navigationEvents) addEventListener(event, cancelForNavigation, { once: true });
    const navigation = window.navigation;
    navigation?.addEventListener("currententrychange", cancelForNavigation);
    try {
      const subject = site.subject();
      if (!subject) {
        return { url: "", note: site.missing };
      }
      const listed = site.request ? site.addresses(await ask(site, subject), subject) : site.read(subject);
      const addresses = (listed || []).filter(Boolean);
      const hls = [...new Set(addresses.map(candidate => mediaAddress(candidate))
        .filter(candidate => candidate?.pathname.toLowerCase().endsWith(".m3u8"))
        .map(candidate => candidate.href))].slice(0, 4);
      if (!hls.length) {
        return { url: "", note: site.noHls ? site.noHls(subject, addresses.length) : `${site.name} listed ${addresses.length} address(es) but none is HLS.` };
      }
      const url = await firstReachable(hls);
      if (controller.signal.aborted || location.href !== expectedURL) return { failure: "stale-tab" };
      if (!url) return { url: "", note: "None of the playlist mirrors responded." };
      return { url, note: site.describe(subject, hls.length) };
    } catch (error) {
      if (controller.signal.aborted || location.href !== expectedURL) return { failure: "stale-tab" };
      if (error?.name === "AbortError") return { url: "", note: `${site.name} did not respond in time.` };
      return { url: "", note: `The page could not trade its session for an address: ${String(error?.message || error)}` };
    } finally {
      for (const event of navigationEvents) removeEventListener(event, cancelForNavigation);
      navigation?.removeEventListener("currententrychange", cancelForNavigation);
    }

    // ask performs the site's own request and hands back the parsed answer.
    async function ask(entry, subject) {
      const spec = entry.request(subject);
      return fetchWithin(spec.url, {
        method: spec.method || "GET",
        credentials: spec.credentials || "omit",
        redirect: "error",
        headers: spec.headers,
        body: spec.body,
        signal: controller.signal,
      }, 10000, answer => {
        if (!answer.ok) throw new Error(`${entry.name} answered HTTP ${answer.status}.`);
        return answer.json();
      });
    }

    // firstReachable asks each mirror for its playlist without reading the answer.
    // A mirror that does not resolve rejects the fetch, which is the only signal a
    // cross-origin page can get; an answer of any kind means the host is there.
    async function firstReachable(candidates) {
      for (const candidate of candidates) {
        if (controller.signal.aborted) return "";
        try {
          const reachable = await fetchWithin(candidate, {
            mode: "no-cors", credentials: "omit", cache: "no-store", signal: controller.signal,
          }, 3500, response => response.type === "opaque" || response.ok);
          if (reachable) return candidate;
        } catch {
          // Try the next candidate unless navigation cancelled the resolver.
        }
      }
      return "";
    }
  }
}

// stored returns the paired address and token, or null when this browser has
// not paired with a window yet.
async function stored() {
  const { handoff } = await chrome.storage.local.get("handoff");
  if (handoff?.address !== FALLBACK_BASE || typeof handoff.token !== "string" || !handoff.token) return null;
  return handoff;
}

function safeReason(reason) {
  if (typeof reason !== "string") return "";
  return reason.replace(/(?:https?|socks5h?):\/\/[^\s"'<>]+/gi, value => {
    const url = pageMedia("address", value);
    return url ? url.origin + url.pathname : "[address]";
  }).replace(/\s+/g, " ").trim().slice(0, 240);
}

// request sends one JSON request to the window and returns its JSON answer.
async function request(path, body, token) {
  const headers = { "Content-Type": "application/json" };
  if (token) headers["X-MuxShift-Handoff"] = token;
  try {
    return await pageMedia("fetch", `${FALLBACK_BASE}${path}`, {
      method: "POST",
      headers,
      body: JSON.stringify(body || {}),
      redirect: "error",
      credentials: "omit",
    }, DESKTOP_TIMEOUT, async response => {
      const text = await response.text();
      let parsed;
      try {
        parsed = text ? JSON.parse(text) : {};
      } catch {
        parsed = { reason: text };
      }
      if (!response.ok) {
        const error = new Error(safeReason(parsed?.reason || parsed?.error) || `The window answered HTTP ${response.status}.`);
        error.status = response.status;
        throw error;
      }
      return parsed;
    });
  } catch (error) {
    // Preserve the network-failure category for pair and import callers. The
    // wrapper is an ordinary Error, so callers must not inspect instanceof
    // TypeError after this point.
    if (error?.name === "TypeError" || error?.name === "AbortError") {
      const disconnected = new Error("the local MuxShift window could not be reached");
      disconnected.failure = "unreachable";
      throw disconnected;
    }
    throw error;
  }
}

// pair exchanges the code shown in the window for a token. The code is used
// once and then replaced by the window.
async function pair(code) {
  const cleaned = String(code || "").trim();
  if (!/^\d{6}$/.test(cleaned)) {
    return { ok: false, failure: "bad-code" };
  }
  let answer;
  try {
    answer = await request("/pair", { code: cleaned });
  } catch (error) {
    if (error.failure === "unreachable") return { ok: false, failure: "unreachable" };
    if (error.status === 401 || error.status === 403) return { ok: false, failure: "bad-code" };
    return { ok: false, failure: "failed" };
  }
  if (typeof answer?.token !== "string" || !answer.token || answer.address !== FALLBACK_BASE) {
    return { ok: false, failure: "failed" };
  }
  const address = FALLBACK_BASE;
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
    await pageMedia("fetch", `${FALLBACK_BASE}/health`, {
      redirect: "error", credentials: "omit",
    }, DESKTOP_TIMEOUT, response => {
      if (!response.ok) throw new Error(`The window answered HTTP ${response.status}.`);
    });
  } catch (error) {
    return { paired: true, reachable: false, reason: "the window is not answering; is MuxShift running?" };
  }
  return { paired: true, reachable: true, address: handoff.address };
}

// resolveLive asks the page to trade its own session for one address the window
// can fetch by itself. The trade happens inside the page: the site's own API is
// called with the tab's cookies, and only the resulting playlist address leaves
// the browser. No cookie, storage item, or request header is read or exported.
async function resolveLive(target) {
  if (!await sameActivePage(target)) return { failure: "stale-tab" };
  let results;
  try {
    results = await waitForPageResolver(chrome.scripting.executeScript({
      target: scriptTarget(target),
      world: "MAIN",
      func: pageMedia,
      args: ["resolve", target.url],
    }));
  } catch (error) {
    if (error?.name === "TimeoutError") return { error: "The page resolver did not respond in time." };
    if (!await sameActivePage(target)) return { failure: "stale-tab" };
    return { error: safeReason(error?.message) || "The page resolver could not run." };
  }
  const found = results?.[0]?.result;
  if (!await sameActivePage(target)) return { failure: "stale-tab" };
  if (found?.failure === "stale-tab") return { failure: "stale-tab" };
  if (found?.error) return { error: safeReason(found.error) };
  const note = safeReason(found?.note);
  const address = pageMedia("address", found?.url);
  if (!address) return { note: note || "The page did not return a valid HTTP or HTTPS address." };
  return { url: address.href, note };
}

async function waitForPageResolver(result) {
  let timeout;
  try {
    return await Promise.race([
      result,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new DOMException("The page resolver did not respond in time.", "TimeoutError")), 26000);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
}

// importAddress hands one selected address to the window's own add command.
// A pairing the window no longer knows (it restarted, or the code was replaced)
// is dropped here, so the popup can ask for a fresh code instead of repeating
// a request that cannot succeed.
async function importAddress(url, target) {
  const address = pageMedia("address", url);
  if (!address) return { ok: false, failure: "invalid" };
  const handoff = await stored();
  if (!handoff) {
    return { ok: false, failure: "not-paired" };
  }
  if (!await sameActivePage(target)) return { ok: false, failure: "stale-tab" };
  let answer;
  try {
    answer = await request("/import", { url: address.href }, handoff.token);
  } catch (error) {
    if (error.status === 401 || error.status === 403) {
      const current = await stored();
      if (current && current.token === handoff.token && current.address === handoff.address) {
        await chrome.storage.local.remove("handoff");
      }
      return { ok: false, failure: "expired" };
    }
    if (error.failure === "unreachable") {
      return { ok: false, failure: "unreachable" };
    }
    return { ok: false, failure: error.status ? "rejected" : "failed", reason: safeReason(error.message) };
  }
  return { ok: true, message: safeReason(answer?.message) || "the window accepted the address" };
}
