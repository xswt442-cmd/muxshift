const list = document.getElementById("sources");
const page = document.getElementById("page");
const importButton = document.getElementById("import");
const status = document.getElementById("status");
const code = document.getElementById("code");
const pairButton = document.getElementById("pair");
const pairLine = document.getElementById("pair-line");

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
