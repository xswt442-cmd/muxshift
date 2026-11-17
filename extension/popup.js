const list = document.getElementById("sources");
const page = document.getElementById("page");
const importButton = document.getElementById("import");
const status = document.getElementById("status");
const code = document.getElementById("code");
const pairButton = document.getElementById("pair");
const pairLine = document.getElementById("pair-line");
const resolveButton = document.getElementById("resolve");

let selected = null;

function setStatus(text, kind) {
  status.textContent = text;
  status.className = kind || "";
}

function setPairLine(text) {
  pairLine.textContent = text;
}

function render(scanned) {
  page.textContent = scanned.tab?.url || scanned.page?.url || "unknown page";
  list.textContent = "";
  selected = null;
  importButton.disabled = true;
  // Only a page with a resolver gets the extra button. Everywhere else it would
  // promise an address the page cannot name.
  const live = liveLabel(scanned.tab?.url || "");
  resolveButton.hidden = !live;
  resolveButton.textContent = live;

  const sources = scanned.sources || [];
  for (const source of sources) {
    const item = document.createElement("li");
    const kind = document.createElement("div");
    kind.className = "kind";
    kind.textContent = source.kind;
    const url = document.createElement("div");
    url.className = "url";
    url.textContent = source.url;
    const from = document.createElement("div");
    from.className = "from";
    from.textContent = source.from;
    item.append(kind, url, from);
    item.addEventListener("click", () => {
      selected = source.url;
      for (const other of list.children) other.classList.toggle("selected", other === item);
      importButton.disabled = false;
      setStatus("");
    });
    list.append(item);
  }

  for (const note of scanned.notes || []) {
    const element = document.createElement("div");
    element.className = "note";
    element.textContent = note.detail;
    list.append(element);
  }

  if (!sources.length) {
    setStatus("Nothing can be imported from this tab.", "bad");
  }
}

// liveLabel names the resolver the active page has, or an empty string. The
// background repeats this test itself, so a page navigated between the scan and
// the click is still refused there.
function liveLabel(url) {
  let host;
  try {
    host = new URL(url).hostname;
  } catch {
    return "";
  }
  if (host === "bilibili.com" || host.endsWith(".bilibili.com")) return "Find this room's live HLS address";
  if (host === "twitch.tv" || host.endsWith(".twitch.tv")) return "Find this channel's live HLS address";
  if (host === "kick.com" || host.endsWith(".kick.com")) return "Find this channel's live HLS address";
  if (host === "youtube.com" || host.endsWith(".youtube.com")) return "Find this video's live HLS address";
  return "";
}

// offer puts a resolved address at the top of the list and selects it: resolving
// is the work, and the person should not have to click the same thing twice.
function offer(reply) {
  const item = document.createElement("li");
  const kind = document.createElement("div");
  kind.className = "kind";
  kind.textContent = "live hls";
  const url = document.createElement("div");
  url.className = "url";
  url.textContent = reply.url;
  const from = document.createElement("div");
  from.className = "from";
  from.textContent = "resolved in the page's own session";
  item.append(kind, url, from);
  item.addEventListener("click", () => {
    selected = reply.url;
    for (const other of list.children) other.classList.toggle("selected", other === item);
    importButton.disabled = false;
  });
  for (const other of list.children) other.classList.remove("selected");
  item.classList.add("selected");
  list.prepend(item);
  selected = reply.url;
  importButton.disabled = false;
  setStatus(reply.note || "the page named a live address", "ok");
}

resolveButton.addEventListener("click", () => {
  resolveButton.disabled = true;
  setStatus("asking the page for its live address…");
  chrome.runtime.sendMessage({ type: "resolve" }, (reply) => {
    resolveButton.disabled = false;
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      return;
    }
    if (reply?.error) {
      setStatus(reply.error, "bad");
      return;
    }
    if (!reply?.url) {
      setStatus(reply?.note || "this page did not name a live address", "bad");
      return;
    }
    offer(reply);
  });
});

function start() {
  chrome.runtime.sendMessage({ type: "status" }, (reply) => {
    if (chrome.runtime.lastError) {
      setPairLine(chrome.runtime.lastError.message);
      return;
    }
    if (reply?.paired && reply.reachable) {
      setPairLine(`paired with ${reply.address}`);
    } else if (reply?.paired) {
      setPairLine("paired, but the window is not answering — is MuxShift running?");
    } else {
      setPairLine("not paired: open MuxShift's Diagnostics page for the code");
    }
  });
  chrome.runtime.sendMessage({ type: "scan" }, (reply) => {
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      return;
    }
    if (reply?.error) {
      setStatus(reply.error, "bad");
      return;
    }
    render(reply);
  });
}

pairButton.addEventListener("click", () => {
  const typed = code.value.trim();
  pairButton.disabled = true;
  setStatus("pairing…");
  chrome.runtime.sendMessage({ type: "pair", code: typed }, (reply) => {
    pairButton.disabled = false;
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      return;
    }
    if (!reply?.ok) {
      setStatus(reply?.reason || "the window refused that code", "bad");
      return;
    }
    code.value = "";
    setPairLine(`paired with ${reply.address}`);
    setStatus("paired; pick an address below", "ok");
  });
});

importButton.addEventListener("click", () => {
  if (!selected) return;
  importButton.disabled = true;
  setStatus("sending the address to the window…");
  chrome.runtime.sendMessage({ type: "import", url: selected }, (reply) => {
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      importButton.disabled = false;
      return;
    }
    if (!reply?.ok) {
      const reason = reply?.reason || "the window refused the address";
      setStatus(reason, "bad");
      // The background drops a pairing the window no longer knows; say so here
      // too, otherwise the popup keeps promising a connection that is gone.
      if (/no longer paired/.test(reason)) {
        setPairLine("not paired: open MuxShift's Diagnostics page for a new code");
      }
      importButton.disabled = false;
      return;
    }
    // The extension only learns that the window's add command was accepted.
    // Ready and the first picture are the window's own status, so this line
    // must not claim them.
    setStatus(`${reply.message || "the window accepted the address"} — watch the window's Sources page for Ready and the first frame.`, "ok");
  });
});

start();
