const list = document.getElementById("sources");
const page = document.getElementById("page");
const importButton = document.getElementById("import");
const status = document.getElementById("status");

let selected = null;

function setStatus(text, kind) {
  status.textContent = text;
  status.className = kind || "";
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

importButton.addEventListener("click", () => {
  if (!selected) return;
  importButton.disabled = true;
  setStatus("asking the local session…");
  chrome.runtime.sendMessage({ type: "import", url: selected }, (reply) => {
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      importButton.disabled = false;
      return;
    }
    if (!reply?.ok) {
      setStatus(reply?.reason || "the import was refused", "bad");
      importButton.disabled = false;
      return;
    }
    setStatus(`imported as ${reply.source.id}`, "ok");
  });
});

start();
