const list = document.getElementById("sources");
const page = document.getElementById("page");
const importButton = document.getElementById("import");
const status = document.getElementById("status");
const code = document.getElementById("code");
const pairButton = document.getElementById("pair");
const pairLine = document.getElementById("pair-line");
const resolveButton = document.getElementById("resolve");

let selected = null;
let pairEpoch = 0;

// Chinese first: follow the browser's UI language, and fall back to English for
// anything that is not Chinese. `navigator.language` is the popup's best hint.
const browserLang = (navigator.language || (navigator.languages && navigator.languages[0]) || "").toLowerCase();
const lang = browserLang ? (browserLang.startsWith("zh") ? "zh" : "en") : "zh";
document.documentElement.lang = lang;

const STRINGS = {
  zh: {
    pairPlaceholder: "配对码",
    pairButton: "配对",
    importButton: "导入所选地址",
    advancedTitle: "高级",
    advancedMeta:
      "「高级」列出当前页面已经加载过的地址；「解析并发送」会让受支持的站点给出一个可播放的地址。扩展只与本机 127.0.0.1 上的桌面窗口配对一次；Cookie、请求头和受保护的媒体都不会被导出。",
    checking: "正在检查本机窗口…",
    readingTab: "正在读取当前标签页…",
    unknownPage: "未知页面",
    pairedWith: (address) => `已与本机窗口配对：${address}`,
    pairedUnreachable: "已配对，但本机窗口无响应 —— MuxShift 是否在运行？",
    notPaired: "尚未配对：请打开 MuxShift 诊断页获取配对码",
    notPairedNew: "尚未配对：请打开 MuxShift 诊断页获取新的配对码",
    parseBilibili: "解析本直播间的 HLS 地址",
    parseTwitch: "解析本频道的 HLS 地址",
    parseKick: "解析本频道的 HLS 地址",
    parseYouTube: "解析本视频的 HLS 地址",
    pairing: "正在配对…",
    pairedPick: "配对成功；可在「高级」中选择要发送的地址",
    refusedCode: "本机窗口拒绝了该配对码",
    nothingImportable: "此标签页没有可导入的地址。",
    resolving: "正在向页面索取直播地址…",
    noLiveAddress: "此页面没有给出直播地址",
    sending: "正在把地址发送到本机窗口…",
    refusedAddress: "本机窗口拒绝了该地址",
    accepted: "本机窗口已接收该地址 —— 请在窗口的「来源」页查看 Ready 和首帧。",
    liveKind: "直播 HLS",
    resolvedFrom: "已在页面自身的会话中解析",
  },
  en: {
    pairPlaceholder: "pairing code",
    pairButton: "Pair",
    importButton: "Import the selected address",
    advancedTitle: "Advanced",
    advancedMeta:
      "Advanced lists addresses this page already loaded; “Parse & send” asks a supported site for a playable address. The extension pairs once with the desktop window over 127.0.0.1; cookies, headers, and protected media are not exported.",
    checking: "checking the local window…",
    readingTab: "reading the active tab…",
    unknownPage: "unknown page",
    pairedWith: (address) => `paired with ${address}`,
    pairedUnreachable: "paired, but the window is not answering — is MuxShift running?",
    notPaired: "not paired: open MuxShift's Diagnostics page for the code",
    notPairedNew: "not paired: open MuxShift's Diagnostics page for a new code",
    parseBilibili: "Find this room's live HLS address",
    parseTwitch: "Find this channel's live HLS address",
    parseKick: "Find this channel's live HLS address",
    parseYouTube: "Find this video's live HLS address",
    pairing: "pairing…",
    pairedPick: "paired; pick an address under Advanced",
    refusedCode: "the window refused that code",
    nothingImportable: "Nothing can be imported from this tab.",
    resolving: "asking the page for its live address…",
    noLiveAddress: "this page did not name a live address",
    sending: "sending the address to the window…",
    refusedAddress: "the window refused the address",
    accepted: "the window accepted the address — watch the window's Sources page for Ready and the first frame.",
    liveKind: "live hls",
    resolvedFrom: "resolved in the page's own session",
  },
};

const S = STRINGS[lang];

function t(key, ...args) {
  const entry = S[key] ?? STRINGS.en[key] ?? key;
  return typeof entry === "function" ? entry(...args) : entry;
}

function setStatus(text, kind) {
  status.textContent = text;
  status.className = kind || "";
}

function setPairLine(text) {
  pairLine.textContent = text;
}

function render(scanned) {
  page.textContent = scanned.tab?.url || scanned.page?.url || t("unknownPage");
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
    setStatus(t("nothingImportable"), "bad");
  }
}

// liveLabel names the resolver the active page has, or an empty string. The host
// check mirrors background.js's own site table (including `youtu.be` short
// links), and the background repeats this test itself, so a page navigated
// between the scan and the click is still refused there.
function liveLabel(url) {
  let host;
  try {
    host = new URL(url).hostname;
  } catch {
    return "";
  }
  const matches = (known) => host === known || host.endsWith(`.${known}`);
  if (matches("bilibili.com")) return t("parseBilibili");
  if (matches("twitch.tv")) return t("parseTwitch");
  if (matches("kick.com")) return t("parseKick");
  if (matches("youtube.com") || matches("youtu.be")) return t("parseYouTube");
  return "";
}

// offer puts a resolved address at the top of the Advanced list and selects it,
// so the person can see exactly what was found and what is being sent.
function offer(reply) {
  const item = document.createElement("li");
  const kind = document.createElement("div");
  kind.className = "kind";
  kind.textContent = t("liveKind");
  const url = document.createElement("div");
  url.className = "url";
  url.textContent = reply.url;
  const from = document.createElement("div");
  from.className = "from";
  from.textContent = t("resolvedFrom");
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
}

// sendAddress hands one address to the window and reports the outcome. `done`
// re-enables whichever button started the send.
function sendAddress(url, done) {
  setStatus(t("sending"));
  chrome.runtime.sendMessage({ type: "import", url }, (reply) => {
    if (done) done();
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      return;
    }
    if (!reply?.ok) {
      const reason = reply?.reason || t("refusedAddress");
      setStatus(reason, "bad");
      // The background drops a pairing the window no longer knows; say so here
      // too, otherwise the popup keeps promising a connection that is gone.
      if (/no longer paired/.test(reason)) {
        setPairLine(t("notPairedNew"));
      }
      return;
    }
    // The extension only learns that the window's add command was accepted.
    // Ready and the first picture are the window's own status, so this line
    // must not claim them.
    setStatus(t("accepted"), "ok");
  });
}

resolveButton.addEventListener("click", () => {
  resolveButton.disabled = true;
  setStatus(t("resolving"));
  chrome.runtime.sendMessage({ type: "resolve" }, (reply) => {
    if (chrome.runtime.lastError) {
      resolveButton.disabled = false;
      setStatus(chrome.runtime.lastError.message, "bad");
      return;
    }
    if (reply?.error) {
      resolveButton.disabled = false;
      setStatus(reply.error, "bad");
      return;
    }
    if (!reply?.url) {
      resolveButton.disabled = false;
      setStatus(reply?.note || t("noLiveAddress"), "bad");
      return;
    }
    // Resolving is the work; send the address in the same click.
    offer(reply);
    sendAddress(reply.url, () => {
      resolveButton.disabled = false;
    });
  });
});

function start() {
  const epoch = pairEpoch;
  chrome.runtime.sendMessage({ type: "status" }, (reply) => {
    if (pairEpoch !== epoch) {
      return;
    }
    if (chrome.runtime.lastError) {
      setPairLine(chrome.runtime.lastError.message);
      return;
    }
    if (reply?.paired && reply.reachable) {
      setPairLine(t("pairedWith", reply.address));
    } else if (reply?.paired) {
      setPairLine(t("pairedUnreachable"));
    } else {
      setPairLine(t("notPaired"));
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
  setStatus(t("pairing"));
  chrome.runtime.sendMessage({ type: "pair", code: typed }, (reply) => {
    pairButton.disabled = false;
    if (chrome.runtime.lastError) {
      setStatus(chrome.runtime.lastError.message, "bad");
      return;
    }
    if (!reply?.ok) {
      setStatus(reply?.reason || t("refusedCode"), "bad");
      return;
    }
    code.value = "";
    pairEpoch++;
    setPairLine(t("pairedWith", reply.address));
    setStatus(t("pairedPick"), "ok");
  });
});

importButton.addEventListener("click", () => {
  if (!selected) return;
  importButton.disabled = true;
  sendAddress(selected, () => {
    importButton.disabled = false;
  });
});

// Fill the static labels for the chosen language before the first refresh.
document.title = "MuxShift";
code.placeholder = t("pairPlaceholder");
pairButton.textContent = t("pairButton");
importButton.textContent = t("importButton");
document.getElementById("advanced-title").textContent = t("advancedTitle");
document.getElementById("advanced-meta").textContent = t("advancedMeta");
page.textContent = t("readingTab");
pairLine.textContent = t("checking");

start();
