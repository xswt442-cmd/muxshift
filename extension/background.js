// The service worker is the only place that may reach the native host, so the
// popup talks to it and never touches the bridge directly.
const HOST = "com.muxshift.bridge";

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type === "scan") {
    scan().then(respond, (error) => respond({ error: String(error?.message || error) }));
    return true;
  }
  if (message?.type === "import") {
    bridge({ type: "import", url: message.url }).then(respond, (error) =>
      respond({ ok: false, reason: String(error?.message || error) }),
    );
    return true;
  }
  if (message?.type === "ping") {
    bridge({ type: "ping" }).then(respond, (error) =>
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

// bridge sends one control message to the native host. Only control messages
// travel this way; media never does.
function bridge(message) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendNativeMessage(HOST, message, (reply) => {
      if (chrome.runtime.lastError) {
        reject(new Error(chrome.runtime.lastError.message));
        return;
      }
      if (!reply) {
        reject(new Error("the native host closed without an answer"));
        return;
      }
      resolve(reply);
    });
  });
}
