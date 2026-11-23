const list = document.getElementById("sources");
const detailsList = document.getElementById("source-details");
const notesList = document.getElementById("scan-notes");
const page = document.getElementById("page");
const importButton = document.getElementById("import");
const status = document.getElementById("status");
const code = document.getElementById("code");
const pairButton = document.getElementById("pair");
const pairLine = document.getElementById("pair-line");
const resolveButton = document.getElementById("resolve");
const candidateHeading = document.getElementById("candidate-heading");

let selected = null;
let pairEpoch = 0;
let handoffBusy = false;
let candidateControls = [];

const browserLang = (navigator.language || (navigator.languages && navigator.languages[0]) || "").toLowerCase();
const lang = browserLang ? (browserLang.startsWith("zh") ? "zh" : "en") : "zh";
document.documentElement.lang = lang;

const STRINGS = {
  zh: {
    pairPlaceholder: "配对码",
    pairButton: "配对",
    importButton: "发送所选地址",
    candidateHeading: (count) => `可选地址（${count}）`,
    detailsTitle: "地址与扫描详情",
    advancedMeta: "原始地址、页面来源和扫描说明。地址仅按用户选择发送；Cookie、请求头和受保护的媒体不会导出。",
    checking: "正在检查本机窗口…",
    readingTab: "正在读取当前标签页…",
    unknownPage: "未知页面",
    pairedWith: "已配对 · 本机窗口可连接",
    pairedUnreachable: "配对已保存 · 本机窗口无响应，请启动 MuxShift",
    notPaired: "尚未配对 · 请从 MuxShift 诊断页获取配对码",
    notPairedNew: "配对已失效 · 请从 MuxShift 诊断页获取新配对码",
    parseBilibili: "解析并发送本直播间地址",
    parseTwitch: "解析并发送本频道地址",
    parseKick: "解析并发送本频道地址",
    parseYouTube: "解析并发送本视频地址",
    pairing: "正在配对…",
    pairedPick: "配对成功 · 选择一个地址后发送",
    refusedCode: "配对码无效或已过期 · 请从诊断页复制当前配对码",
    pairFailed: "配对失败 · 请重试",
    scanEmpty: "未发现普通媒体地址。",
    scanEmptyWithResolver: "未发现普通媒体地址；可使用上方的站点解析操作。",
    noteBlob: "此播放器使用页面内部的 Media Source 数据，没有可独立导入的地址。",
    noteProtocol: "此类型的地址不能导入；仅支持 http 和 https。",
    noteCredentials: "地址包含用户名或密码，已拒绝。",
    noteSegments: (count) => `页面已读取 ${count} 个媒体分片；分片不能单独播放，请选择播放列表。`,
    noteNone: "未找到可导入地址；页面可能使用内部媒体数据或受保护媒体。",
    scanFailed: "读取当前页面失败 · 请确认页面已打开后重试",
    resolving: "正在请求页面提供直播地址…",
    resolveFailed: "页面解析失败或未提供可用地址 · 请检查页面状态后重试",
    noLiveAddress: "页面没有提供可用的直播地址",
    sending: "正在发送地址到本机窗口…",
    accepted: "桌面端已接收导入请求 · 请在「来源」页查看打开状态；失败原因会显示在那里。",
    sendFailed: "桌面端未能接收该地址 · 请查看桌面端提示并重试",
    selectFirst: "先选择一个地址，再发送。",
    expired: "配对已失效 · 请从 MuxShift 诊断页获取新配对码",
    windowUnreachable: "无法连接本机 MuxShift · 请确认桌面窗口正在运行",
    liveKind: "直播 HLS",
    resolvedFrom: "由当前网页解析",
    ordinaryKind: (kind) => ({ hls: "HLS 播放列表", dash: "DASH 播放列表", audio: "音频", file: "媒体文件" }[kind] || "媒体地址"),
    candidate: (index) => `地址 ${index}`,
  },
  en: {
    pairPlaceholder: "pairing code",
    pairButton: "Pair",
    importButton: "Send selected address",
    candidateHeading: (count) => `Available addresses (${count})`,
    detailsTitle: "Address and scan details",
    advancedMeta: "Raw addresses, page sources, and scan notes. An address is sent only after you choose it; cookies, headers, and protected media are not exported.",
    checking: "checking the local window…",
    readingTab: "reading the active tab…",
    unknownPage: "unknown page",
    pairedWith: "paired · local window is reachable",
    pairedUnreachable: "pairing saved · local window is not answering; start MuxShift",
    notPaired: "not paired · get a code from MuxShift Diagnostics",
    notPairedNew: "pairing expired · get a new code from MuxShift Diagnostics",
    parseBilibili: "Resolve and send this room's address",
    parseTwitch: "Resolve and send this channel's address",
    parseKick: "Resolve and send this channel's address",
    parseYouTube: "Resolve and send this video's address",
    pairing: "pairing…",
    pairedPick: "paired · choose an address and send it",
    refusedCode: "pairing code is invalid or expired · copy the current code from Diagnostics",
    pairFailed: "Pairing failed · try again",
    scanEmpty: "No ordinary media address was found.",
    scanEmptyWithResolver: "No ordinary media address was found; use the site resolver above if available.",
    noteBlob: "This player uses Media Source data held by the page; there is no standalone address to import.",
    noteProtocol: "This address type cannot be imported; only http and https are supported.",
    noteCredentials: "This address contains a user name or password and was refused.",
    noteSegments: (count) => `The page read ${count} media segment(s); a segment cannot play alone, so choose a playlist.`,
    noteNone: "No importable address was found; the page may use internal or protected media.",
    scanFailed: "Could not read this page · make sure it is open, then retry",
    resolving: "asking the page for its live address…",
    resolveFailed: "page resolution failed or returned no usable address · check the page and retry",
    noLiveAddress: "the page did not provide a usable live address",
    sending: "sending the address to the local window…",
    accepted: "The desktop accepted the import request · check Sources for its open status and any failure reason.",
    sendFailed: "The desktop could not accept this address · check its message and try again",
    selectFirst: "Choose an address before sending.",
    expired: "pairing expired · get a new code from MuxShift Diagnostics",
    windowUnreachable: "Cannot reach local MuxShift · make sure the desktop window is running",
    liveKind: "live HLS",
    resolvedFrom: "resolved by the active page",
    ordinaryKind: (kind) => ({ hls: "HLS playlist", dash: "DASH playlist", audio: "audio", file: "media file" }[kind] || "media address"),
    candidate: (index) => `Address ${index}`,
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
function safePageAddress(value) {
  try {
    const parsed = new URL(value);
    parsed.username = "";
    parsed.password = "";
    parsed.search = "";
    parsed.hash = "";
    return parsed.href;
  } catch {
    return t("unknownPage");
  }
}
function candidateParts(source) {
  const kind = t("ordinaryKind", source.kind);
  let host = "";
  try { host = new URL(source.url).hostname; } catch {}
  return { kind, host, from: source.from || "" };
}
function shortPathLabel(value) {
  try {
    const parts = new URL(value).pathname.split("/").filter(Boolean).slice(-2).map((part) => {
      let decoded = part;
      try { decoded = decodeURIComponent(part); } catch {}
      return decoded.replace(/[^\p{L}\p{N}._ -]/gu, "").slice(0, 24);
    }).filter(Boolean);
    return parts.join("/").slice(0, 48);
  } catch {
    return "";
  }
}
function candidateLabel(source, index, includePath) {
  const { kind, host, from } = candidateParts(source);
  const path = includePath ? shortPathLabel(source.url) : "";
  return [t("candidate", index + 1), kind, host, from, path].filter(Boolean).join(" · ");
}
function updateHandoffControls() {
  resolveButton.disabled = handoffBusy;
  importButton.disabled = handoffBusy || !selected;
  for (const control of candidateControls) control.disabled = handoffBusy;
}
function finishHandoff() {
  handoffBusy = false;
  updateHandoffControls();
}

function render(scanned) {
  page.textContent = safePageAddress(scanned.tab?.url || scanned.page?.url || "");
  list.textContent = "";
  detailsList.textContent = "";
  notesList.textContent = "";
  selected = null;
  candidateControls = [];
  updateHandoffControls();

  const live = liveLabel(scanned.tab?.url || "");
  resolveButton.hidden = !live;
  resolveButton.textContent = live;

  const sources = Array.isArray(scanned.sources) ? scanned.sources : [];
  candidateHeading.textContent = t("candidateHeading", sources.length);
  const signatures = sources.map((source) => JSON.stringify(candidateParts(source)));
  const signatureCounts = new Map();
  for (const signature of signatures) signatureCounts.set(signature, (signatureCounts.get(signature) || 0) + 1);
  sources.forEach((source, index) => {
    const item = document.createElement("li");
    const label = document.createElement("label");
    label.className = "candidate";
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "candidate";
    radio.value = String(index);
    const text = document.createElement("span");
    text.textContent = candidateLabel(source, index, signatureCounts.get(signatures[index]) > 1);
    candidateControls.push(radio);
    label.append(radio, text);
    radio.addEventListener("change", () => {
      selected = source.url;
      for (const other of list.children) other.classList.toggle("selected", other === item);
      updateHandoffControls();
      setStatus("");
    });
    item.append(label);
    list.append(item);

    const detail = document.createElement("li");
    const kind = document.createElement("div");
    kind.className = "kind";
    kind.textContent = source.kind;
    const url = document.createElement("div");
    url.className = "url";
    url.textContent = source.url;
    const from = document.createElement("div");
    from.className = "from";
    from.textContent = source.from;
    detail.append(kind, url, from);
    detailsList.append(detail);
  });

  for (const note of scanned.notes || []) {
    const element = document.createElement("div");
    element.className = "note";
    if (note.kind === "segments") {
      const count = Number((note.detail.match(/\d+/) || [])[0]) || 0;
      element.textContent = t("noteSegments", count);
    } else {
      const noteKey = ({ blob: "noteBlob", credentials: "noteCredentials", none: "noteNone" })[note.kind]
        || "noteProtocol";
      element.textContent = t(noteKey);
    }
    notesList.append(element);
  }

  if (sources.length) {
    setStatus("");
  } else if (live) {
    setStatus(t("scanEmptyWithResolver"));
  } else {
    setStatus(t("scanEmpty"));
  }
}

function liveLabel(url) {
  let host;
  try { host = new URL(url).hostname; } catch { return ""; }
  const matches = (known) => host === known || host.endsWith(`.${known}`);
  if (matches("bilibili.com")) return t("parseBilibili");
  if (matches("twitch.tv")) return t("parseTwitch");
  if (matches("kick.com")) return t("parseKick");
  if (matches("youtube.com") || matches("youtu.be")) return t("parseYouTube");
  return "";
}

function offer(reply) {
  const source = { url: reply.url, kind: "hls", from: t("resolvedFrom") };
  const item = document.createElement("li");
  const label = document.createElement("label");
  label.className = "candidate";
  const radio = document.createElement("input");
  radio.type = "radio";
  radio.name = "candidate";
  radio.value = "resolved";
  radio.checked = true;
  radio.disabled = handoffBusy;
  candidateControls.push(radio);
  const text = document.createElement("span");
  text.textContent = t("liveKind");
  label.append(radio, text);
  item.append(label);
  radio.addEventListener("change", () => { selected = source.url; });
  for (const other of list.children) other.classList.remove("selected");
  item.classList.add("selected");
  list.prepend(item);
  selected = source.url;
  updateHandoffControls();

  const detail = document.createElement("li");
  const kind = document.createElement("div");
  kind.className = "kind";
  kind.textContent = t("liveKind");
  const url = document.createElement("div");
  url.className = "url";
  url.textContent = source.url;
  const from = document.createElement("div");
  from.className = "from";
  from.textContent = source.from;
  detail.append(kind, url, from);
  detailsList.prepend(detail);
  candidateHeading.textContent = t("candidateHeading", list.children.length);
}

function sendAddress(url, done) {
  setStatus(t("sending"));
  chrome.runtime.sendMessage({ type: "import", url }, (reply) => {
    if (chrome.runtime.lastError) {
      setStatus(t("sendFailed"), "bad");
      if (done) done();
      return;
    }
    if (!reply?.ok) {
      if (reply?.failure === "expired") {
        setPairLine(t("notPairedNew"));
        setStatus(t("expired"), "bad");
      } else if (reply?.failure === "not-paired") {
        setPairLine(t("notPaired"));
        setStatus(t("notPaired"), "bad");
      } else if (reply?.failure === "unreachable") {
        setPairLine(t("pairedUnreachable"));
        setStatus(t("windowUnreachable"), "bad");
      } else {
        setStatus(t("sendFailed"), "bad");
      }
      if (done) done();
      return;
    }
    setStatus(t("accepted"), "ok");
    if (done) done();
  });
}

resolveButton.addEventListener("click", () => {
  if (handoffBusy) return;
  handoffBusy = true;
  updateHandoffControls();
  setStatus(t("resolving"));
  chrome.runtime.sendMessage({ type: "resolve" }, (reply) => {
    if (chrome.runtime.lastError) {
      setStatus(t("scanFailed"), "bad");
      finishHandoff();
      return;
    }
    if (reply?.error || !reply?.url) {
      setStatus(reply?.note ? t("resolveFailed") : t("noLiveAddress"), "bad");
      finishHandoff();
      return;
    }
    offer(reply);
    sendAddress(reply.url, finishHandoff);
  });
});

function start() {
  const epoch = pairEpoch;
  chrome.runtime.sendMessage({ type: "status" }, (reply) => {
    if (pairEpoch !== epoch) return;
    if (chrome.runtime.lastError) {
      setPairLine(t("pairedUnreachable"));
    } else if (reply?.paired && reply.reachable) {
      setPairLine(t("pairedWith"));
    } else if (reply?.paired) {
      setPairLine(t("pairedUnreachable"));
    } else {
      setPairLine(t("notPaired"));
    }
  });
  chrome.runtime.sendMessage({ type: "scan" }, (reply) => {
    if (chrome.runtime.lastError || reply?.error) {
      setStatus(t("scanFailed"), "bad");
      return;
    }
    render(reply || {});
  });
}

pairButton.addEventListener("click", () => {
  const typed = code.value.trim();
  pairButton.disabled = true;
  setStatus(t("pairing"));
  chrome.runtime.sendMessage({ type: "pair", code: typed }, (reply) => {
    pairButton.disabled = false;
    if (reply?.failure === "unreachable") {
      setStatus(t("windowUnreachable"), "bad");
      return;
    }
    if (reply?.failure === "bad-code") {
      setStatus(t("refusedCode"), "bad");
      return;
    }
    if (chrome.runtime.lastError || !reply?.ok) {
      setStatus(t("pairFailed"), "bad");
      return;
    }
    code.value = "";
    pairEpoch++;
    setPairLine(t("pairedWith"));
    setStatus(t("pairedPick"), "ok");
  });
});

importButton.addEventListener("click", () => {
  if (handoffBusy) return;
  if (!selected) {
    setStatus(t("selectFirst"), "bad");
    return;
  }
  handoffBusy = true;
  updateHandoffControls();
  sendAddress(selected, finishHandoff);
});

document.title = "MuxShift";
code.placeholder = t("pairPlaceholder");
pairButton.textContent = t("pairButton");
importButton.textContent = t("importButton");
candidateHeading.textContent = t("candidateHeading", 0);
document.getElementById("details-title").textContent = t("detailsTitle");
document.getElementById("advanced-meta").textContent = t("advancedMeta");
page.textContent = t("readingTab");
pairLine.textContent = t("checking");
start();
