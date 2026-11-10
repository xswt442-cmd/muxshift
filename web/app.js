// The web client: it asks the Go session what to show, keeps one media element
// per source, and reports what the browser really does with the stream.
"use strict";

const params = new URLSearchParams(window.location.search);
const token = params.get("token") || "";
// The bootstrap address carries the token once. Everything after that rides on
// the HttpOnly cookie the server set while serving this page, so the token does
// not stay in the address bar, the history, or every request URL.
if (token) {
  params.delete("token");
  const cleaned = window.location.pathname + (params.toString() ? `?${params}` : "");
  window.history.replaceState(null, "", cleaned);
}

const elements = {
  sessionLine: document.getElementById("session-line"),
  stage: document.getElementById("stage"),
  placeholder: document.getElementById("stage-placeholder"),
  sources: document.getElementById("sources"),
  sceneLine: document.getElementById("scene-line"),
  environment: document.getElementById("environment"),
  log: document.getElementById("log"),
  seek: document.getElementById("seek"),
  timeLine: document.getElementById("time-line"),
  volume: document.getElementById("volume"),
  mute: document.getElementById("mute"),
};

/** players holds one media element and one hls.js instance per source id. */
const players = new Map();
let session = null;
let heard = "";
let muted = false;
let volume = 1;
let interacted = false;

// Browsers only allow sound after the page has been clicked or tapped; until
// then every element stays muted so playback can still start.
document.addEventListener("pointerdown", () => {
  interacted = true;
  applyScene();
}, { once: true });

function log(message, kind = "info") {
  const item = document.createElement("li");
  item.className = kind;
  const stamp = new Date().toLocaleTimeString();
  item.textContent = `${stamp} ${message}`;
  elements.log.prepend(item);
  while (elements.log.children.length > 60) {
    elements.log.lastChild.remove();
  }
}

async function api(path, options = {}) {
  // No token in the URL: the session cookie set on load authorizes these calls,
  // and a cross-site page cannot read or write this session because the server
  // checks the Origin and Host of every mutation.
  const response = await fetch(new URL(path, window.location.origin), {
    credentials: "same-origin",
    ...options,
  });
  if (!response.ok) {
    throw new Error(`${options.method || "GET"} ${path} failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

function describeEnvironment() {
  const rows = [];
  const canPlay = (type) => {
    const probe = document.createElement("video");
    return probe.canPlayType(type) || "no";
  };
  rows.push(`MSE: ${window.MediaSource ? "available" : "missing"}`);
  rows.push(`hls.js: ${window.Hls ? window.Hls.version : "not loaded"}`);
  rows.push(`native HLS: ${canPlay("application/vnd.apple.mpegurl")}`);
  rows.push(`MP4 H.264/AAC: ${canPlay('video/mp4; codecs="avc1.42E01E,mp4a.40.2"')}`);
  rows.push(`user agent: ${navigator.userAgent}`);
  for (const row of rows) {
    const item = document.createElement("li");
    item.textContent = row;
    elements.environment.append(item);
  }
}

/** buildPlayer creates the media element of one source and wires hls.js to it. */
function buildPlayer(source) {
  const video = document.createElement("video");
  video.playsInline = true;
  video.muted = true;
  video.preload = "auto";
  video.dataset.source = source.id;
  video.className = "player hidden";
  elements.stage.append(video);

  const state = { source, video, hls: null, lastError: "" };
  video.addEventListener("error", () => {
    const code = video.error ? `${video.error.code} ${video.error.message}` : "unknown";
    state.lastError = `media element error ${code}`;
    log(`${source.id}: ${state.lastError}`, "error");
  });
  video.addEventListener("waiting", () => {
    if (source.shown) {
      log(`${source.id}: waiting for data`);
    }
  });
  video.addEventListener("playing", () => {
    log(`${source.id}: playing`);
  });

  const isHls = /\.m3u8(\?|$)/i.test(source.url);
  if (isHls) {
    if (!window.Hls) {
      state.lastError = "hls.js did not load, HLS cannot play in this browser";
      log(`${source.id}: ${state.lastError}`, "error");
    } else if (window.Hls.isSupported()) {
      const hls = new window.Hls({ enableWorker: true, lowLatencyMode: false });
      hls.loadSource(source.url);
      hls.attachMedia(video);
      hls.on(window.Hls.Events.ERROR, (_event, data) => {
        if (!data.fatal) {
          return;
        }
        state.lastError = `hls.js ${data.type}/${data.details} (${data.reason || "no reason"})`;
        log(`${source.id}: ${state.lastError}`, "error");
        if (data.type === window.Hls.ErrorTypes.NETWORK_ERROR) {
          hls.startLoad();
        } else if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR) {
          hls.recoverMediaError();
        }
      });
      state.hls = hls;
      log(`${source.id}: hls.js attached to ${source.url}`);
    } else if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = source.url;
      log(`${source.id}: using the browser's own HLS support`);
    } else {
      state.lastError = "this browser can play neither MSE HLS nor native HLS";
      log(`${source.id}: ${state.lastError}`, "error");
    }
  } else {
    video.src = source.url;
    log(`${source.id}: media element opens ${source.url}`);
  }
  players.set(source.id, state);
  return state;
}

function renderSources() {
  elements.sources.textContent = "";
  for (const source of session.sources) {
    const player = players.get(source.id);
    const item = document.createElement("li");
    item.className = source.shown ? "source shown" : "source";

    const title = document.createElement("p");
    title.className = "source-title";
    title.textContent = `${source.id} — ${source.uri}`;
    item.append(title);

    const picture = document.createElement("button");
    picture.type = "button";
    picture.textContent = source.shown ? "shown" : "show this picture";
    picture.disabled = source.shown;
    picture.addEventListener("click", () => {
      requestScene(source.id, heardSource() || source.id).catch((error) => log(error.message, "error"));
    });
    item.append(picture);

    const sound = document.createElement("button");
    sound.type = "button";
    sound.textContent = heard === source.id ? "heard" : "hear this source";
    sound.disabled = heard === source.id;
    sound.addEventListener("click", () => {
      requestScene(shownSource() || source.id, source.id).catch((error) => log(error.message, "error"));
    });
    item.append(sound);

    const state = document.createElement("p");
    state.className = "note";
    if (player) {
      const video = player.video;
      state.textContent = `ready ${video.readyState}/4 · ${format(video.currentTime)} of ${format(video.duration)} · leveled ${format(bufferedFor(video))}`;
    }
    item.append(state);
    elements.sources.append(item);
  }
}

function shownSource() {
  const shown = session.sources.find((source) => source.shown);
  return shown ? shown.id : "";
}

function heardSource() {
  return heard;
}

function format(value) {
  if (!Number.isFinite(value)) {
    return "—";
  }
  const minutes = Math.floor(value / 60);
  const seconds = Math.floor(value % 60);
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function bufferedFor(video) {
  if (!video.buffered || video.buffered.length === 0) {
    return 0;
  }
  return video.buffered.end(video.buffered.length - 1) - video.buffered.start(0);
}

function applyScene() {
  for (const source of session.sources) {
    const player = players.get(source.id);
    if (!player) {
      continue;
    }
    player.source = source;
    player.video.classList.toggle("hidden", !source.shown);
    const audible = !muted && interacted && source.id === heard;
    player.video.muted = source.muted || !audible;
    player.video.volume = Math.max(0, Math.min(1, source.id === heard ? volume : source.volume));
  }
  const shownPlayer = players.get(shownSource());
  const failed = shownPlayer && shownPlayer.lastError && shownPlayer.video.readyState === 0;
  elements.placeholder.textContent = failed
    ? `this picture cannot play: ${shownPlayer.lastError}`
    : "no source is shown yet";
  elements.placeholder.classList.toggle("hidden", Boolean(shownSource()) && !failed);
  elements.sceneLine.textContent = `picture ${shownSource() || "none"} · sound ${heard || "none"} at ${volume.toFixed(2)}${muted ? " (muted)" : ""}`;
  const shown = players.get(shownSource());
  elements.seek.disabled = !shown;
  if (shown) {
    const duration = Number.isFinite(shown.video.duration) ? shown.video.duration : 0;
    elements.seek.max = String(duration || 1);
    elements.seek.value = String(shown.video.currentTime || 0);
    elements.timeLine.textContent = `${format(shown.video.currentTime)} of ${format(shown.video.duration)} · buffered ${format(bufferedFor(shown.video))}`;
  } else {
    elements.timeLine.textContent = "—";
  }
  renderSources();
}

async function requestScene(videoID, audioID) {
  const payload = { video: videoID, audio: audioID, volume, muted };
  const updated = await api("/api/scene", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  session = updated;
  heard = audioID;
  log(`scene: video ${videoID}, audio ${audioID}`);
  applyScene();
}

async function start() {
  describeEnvironment();
  try {
    session = await api("/api/session");
  } catch (error) {
    log(`the session could not be read: ${error.message}`, "error");
    log("open the address printed at startup, which carries the token once", "error");
    return;
  }
  elements.sessionLine.textContent = `session ${session.session} · scene ${session.scene} · ${session.sources.length} sources`;
  heard = (session.sources.find((source) => !source.muted) || session.sources[0] || {}).id || "";
  for (const source of session.sources) {
    buildPlayer(source);
  }
  renderSources();
  applyScene();
  for (const state of players.values()) {
    const result = state.video.play();
    if (result && typeof result.catch === "function") {
      result.catch((error) => log(`${state.source.id}: play() refused: ${error.message}`, "error"));
    }
  }
  setInterval(applyScene, 500);
  setInterval(refreshSession, 2000);
}

// refreshSession picks up changes another client made, which is how a source
// imported through the browser extension appears without a reload. It adopts a
// new source only when the session really lists one the page has no player
// for, and takes the scene only when it differs from what is shown here.
async function refreshSession() {
  let fresh;
  try {
    fresh = await api("/api/session");
  } catch (error) {
    return;
  }
  const known = new Set(players.keys());
  const added = fresh.sources.filter((source) => !known.has(source.id));
  const shownNow = fresh.sources.find((source) => source.shown)?.id || "";
  const heardNow = fresh.sources.find((source) => !source.muted)?.id || "";
  const changed =
    added.length > 0 ||
    shownNow !== (session.sources.find((source) => source.shown)?.id || "") ||
    heardNow !== heard;
  session = fresh;
  if (!changed) return;
  for (const source of added) {
    const state = buildPlayer(source);
    state.video.play().catch((error) => log(`${source.id}: play() refused: ${error.message}`, "error"));
  }
  heard = heardNow || heard;
  elements.sessionLine.textContent = `session ${session.session} · scene ${session.scene} · ${session.sources.length} sources`;
  renderSources();
  applyScene();
  if (added.length) {
    log(`session now lists ${added.map((source) => source.id).join(", ")}`, "info");
  }
}

document.getElementById("play-all").addEventListener("click", () => {
  for (const state of players.values()) {
    const result = state.video.play();
    if (result && typeof result.catch === "function") {
      result.catch((error) => log(`${state.source.id}: play() refused: ${error.message}`, "error"));
    }
  }
  log("asked every source to play");
});

document.getElementById("pause-all").addEventListener("click", () => {
  for (const state of players.values()) {
    state.video.pause();
  }
  log("paused every source");
});

document.getElementById("mute").addEventListener("click", () => {
  muted = !muted;
  elements.mute.textContent = muted ? "Unmute heard source" : "Mute heard source";
  applyScene();
});

document.getElementById("fit").addEventListener("click", () => {
  const shown = players.get(shownSource());
  if (shown) {
    shown.video.scrollIntoView({ block: "nearest" });
  }
});

elements.volume.addEventListener("input", () => {
  volume = Number(elements.volume.value);
  applyScene();
});

elements.seek.addEventListener("input", () => {
  const shown = players.get(shownSource());
  if (!shown) {
    return;
  }
  const duration = Number.isFinite(shown.video.duration) ? shown.video.duration : 0;
  if (duration === 0) {
    log("this source has no known duration, it cannot be moved by hand", "error");
    return;
  }
  shown.video.currentTime = Number(elements.seek.value);
  log(`seek asked for ${format(shown.video.currentTime)} of ${format(duration)}`);
});

start().catch((error) => log(error.message, "error"));
