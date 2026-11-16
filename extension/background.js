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

  const extensions = [".m3u8", ".mpd", ".mp4", ".webm", ".m4s", ".ts"];
  for (const entry of performance.getEntriesByType("resource")) {
    const name = entry.name || "";
    const clean = name.split("?")[0].toLowerCase();
    if (!extensions.some((ending) => clean.endsWith(ending))) continue;
    if (new URL(name, location.href).protocol === "blob:") continue;
    const kind = clean.endsWith(".m3u8")
      ? "hls"
      : clean.endsWith(".mpd")
        ? "dash"
        : clean.endsWith(".ts") || clean.endsWith(".m4s")
          ? "segment"
          : "file";
    add(name, kind, "resource timing");
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
    throw new Error(parsed.reason || `the window answered ${response.status}`);
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

// importAddress hands one selected address to the window's own add command.
async function importAddress(url) {
  if (!url) throw new Error("no address was selected");
  const handoff = await stored();
  if (!handoff) {
    throw new Error("pair with the window first: open its Diagnostics page and type its code");
  }
  const answer = await request(base(handoff.address), "/import", { url }, handoff.token);
  return { ok: true, message: answer.message || "the window accepted the address" };
}
