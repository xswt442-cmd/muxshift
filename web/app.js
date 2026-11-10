// The web client: it asks the Go session what to show, keeps one media element
// per source, floats a second picture for comparing by eye, and reports what the
// browser really does with each stream.
"use strict";

const params = new URLSearchParams(window.location.search);
const bootstrapToken = params.get("token") || "";
// The bootstrap address carries the token once. Everything after that rides on
// the HttpOnly cookie the server set while serving this page, so the token does
// not stay in the address bar, the history, or every request URL.
if (bootstrapToken) {
  params.delete("token");
  const cleaned = window.location.pathname + (params.toString() ? `?${params}` : "");
  window.history.replaceState(null, "", cleaned);
}

const el = {
  chipSession: document.getElementById("chip-session"),
  chipConnection: document.getElementById("chip-connection"),
  stage: document.getElementById("stage"),
  stageVideo: document.getElementById("stage-video"),
  stageSource: document.getElementById("stage-source"),
  liveChip: document.getElementById("live-chip"),
  tutorial: document.getElementById("tutorial"),
  inset: document.getElementById("inset"),
  insetBar: document.getElementById("inset-bar"),
  insetLabel: document.getElementById("inset-label"),
  insetClose: document.getElementById("inset-close"),
  insetVideo: document.getElementById("inset-video"),
  toggle: document.getElementById("toggle"),
  back: document.getElementById("back"),
  fwd: document.getElementById("fwd"),
  position: document.getElementById("position"),
  duration: document.getElementById("duration"),
  scrub: document.getElementById("scrub"),
  scrubBuffer: document.getElementById("scrub-buffer"),
  scrubPlayed: document.getElementById("scrub-played"),
  seek: document.getElementById("seek"),
  peekToggle: document.getElementById("peek-toggle"),
  mute: document.getElementById("mute"),
  volume: document.getElementById("volume"),
  volumeOut: document.getElementById("volume-out"),
  fullscreen: document.getElementById("fullscreen"),
  status: document.getElementById("status"),
  peekRow: document.getElementById("peek-row"),
  peekOpacity: document.getElementById("peek-opacity"),
  peekOpacityOut: document.getElementById("peek-opacity-out"),
  sources: document.getElementById("sources"),
  slotPicture: document.getElementById("slot-picture"),
  slotSound: document.getElementById("slot-sound"),
  refresh: document.getElementById("refresh"),
  environment: document.getElementById("environment"),
  clearLog: document.getElementById("clear-log"),
  log: document.getElementById("log"),
};

const players = new Map();
const state = {
  session: null,
  heard: "",
  muted: false,
  volume: 1,
  interacted: false,
  dragging: false,
  peek: "",
  peekOpacity: 0.6,
  offline: false,
  lastShown: "",
};

document.addEventListener(
  "pointerdown",
  () => {
    state.interacted = true;
    applyScene();
  },
  { once: true },
);

function log(message, kind = "info") {
  const item = document.createElement("li");
  item.className = kind;
  const stamp = document.createElement("span");
  stamp.className = "stamp";
  stamp.textContent = new Date().toLocaleTimeString([], { hour12: false });
  const text = document.createElement("span");
  text.className = "text";
  text.textContent = message;
  item.append(stamp, text);
  el.log.prepend(item);
  while (el.log.children.length > 80) {
    el.log.lastChild.remove();
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

function format(value) {
  if (!Number.isFinite(value) || value < 0) {
    return "—";
  }
  const total = Math.floor(value);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

function shortURI(uri) {
  try {
    const parsed = new URL(uri, window.location.origin);
    return parsed.pathname;
  } catch {
    return uri;
  }
}

function readyLabel(video) {
  if (video.readyState >= 4) return "ready";
  if (video.readyState >= 3) return "buffered";
  if (video.readyState >= 2) return "loading";
  if (video.readyState >= 1) return "metadata";
  return "idle";
}

function describeEnvironment() {
  const canPlay = (type) => {
    const probe = document.createElement("video");
    return probe.canPlayType(type) || "no";
  };
  const rows = [
    `MSE: ${window.MediaSource ? "available" : "missing"}`,
    `hls.js: ${window.Hls ? window.Hls.version : "not loaded"}`,
    `native HLS: ${canPlay("application/vnd.apple.mpegurl")}`,
    `MP4 H.264/AAC: ${canPlay('video/mp4; codecs="avc1.42E01E,mp4a.40.2"')}`,
    `user agent: ${navigator.userAgent}`,
  ];
  for (const row of rows) {
    const item = document.createElement("li");
    item.textContent = row;
    el.environment.append(item);
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
  el.stageVideo.append(video);

  const player = { source, video, hls: null, lastError: "" };
  video.addEventListener("error", () => {
    const code = video.error ? `${video.error.code} ${video.error.message}` : "unknown";
    player.lastError = `media element error ${code}`;
    log(`${source.id}: ${player.lastError}`, "error");
    updateCards();
  });
  video.addEventListener("waiting", () => {
    if (player.source && player.source.shown) {
      log(`${source.id}: waiting for data`, "warn");
    }
  });
  video.addEventListener("playing", () => log(`${source.id}: playing`));
  video.addEventListener("ended", () => {
    if (player.source && player.source.shown) {
      log(`${source.id}: reached its end`);
    }
  });

  attachStream(player);
  players.set(source.id, player);
  makeCard(player);
  return player;
}

function attachStream(player) {
  const source = player.source;
  player.lastError = "";
  if (player.hls) {
    player.hls.destroy();
    player.hls = null;
  }
  const isHls = /\.m3u8(\?|$)/i.test(source.url);
  if (isHls) {
    if (!window.Hls) {
      player.lastError = "hls.js did not load, HLS cannot play in this browser";
      log(`${source.id}: ${player.lastError}`, "error");
      return;
    }
    if (!window.Hls.isSupported()) {
      if (player.video.canPlayType("application/vnd.apple.mpegurl")) {
        player.video.src = source.url;
        log(`${source.id}: using the browser's own HLS support`);
      } else {
        player.lastError = "this browser can play neither MSE HLS nor native HLS";
        log(`${source.id}: ${player.lastError}`, "error");
      }
      return;
    }
    const hls = new window.Hls({ enableWorker: true, lowLatencyMode: false });
    hls.loadSource(source.url);
    hls.attachMedia(player.video);
    hls.on(window.Hls.Events.ERROR, (_event, data) => {
      if (!data.fatal) {
        return;
      }
      player.lastError = `hls.js ${data.type}/${data.details} (${data.reason || "no reason"})`;
      log(`${source.id}: ${player.lastError}`, "error");
      updateCards();
      if (data.type === window.Hls.ErrorTypes.NETWORK_ERROR) {
        hls.startLoad();
      } else if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR) {
        hls.recoverMediaError();
      }
    });
    player.hls = hls;
    log(`${source.id}: hls.js attached to ${shortURI(source.url)}`);
    return;
  }
  player.video.src = source.url;
  log(`${source.id}: media element opens ${shortURI(source.url)}`);
}

function retry(player) {
  player.video.removeAttribute("src");
  player.video.load();
  attachStream(player);
  updateCards();
  player.video.play().catch((error) => log(`${player.source.id}: play() refused: ${error.message}`, "error"));
}

function makeCard(player) {
  const source = player.source;
  const root = document.createElement("li");
  root.className = "card";

  const thumb = document.createElement("canvas");
  thumb.className = "thumb";
  thumb.width = 192;
  thumb.height = 108;

  const body = document.createElement("div");
  body.className = "card-body";

  const head = document.createElement("div");
  head.className = "card-head";
  const dot = document.createElement("span");
  dot.className = "dot";
  const id = document.createElement("span");
  id.className = "card-id";
  id.textContent = source.id;
  const badges = document.createElement("span");
  badges.className = "badges";
  const picture = document.createElement("span");
  picture.className = "badge picture";
  picture.textContent = "picture";
  const sound = document.createElement("span");
  sound.className = "badge sound";
  sound.textContent = "sound";
  badges.append(picture, sound);
  head.append(dot, id, badges);

  const uri = document.createElement("p");
  uri.className = "card-uri";
  uri.textContent = shortURI(source.url);
  uri.title = source.url;

  const stats = document.createElement("p");
  stats.className = "card-stats";
  stats.textContent = "—";

  const actions = document.createElement("div");
  actions.className = "card-actions";
  const show = document.createElement("button");
  show.type = "button";
  show.textContent = "Show";
  show.addEventListener("click", () => {
    requestScene(source.id, state.heard || source.id).catch((error) => log(error.message, "error"));
  });
  const hear = document.createElement("button");
  hear.type = "button";
  hear.textContent = "Hear";
  hear.addEventListener("click", () => {
    requestScene(shownID() || source.id, source.id).catch((error) => log(error.message, "error"));
  });
  const peek = document.createElement("button");
  peek.type = "button";
  peek.textContent = "Peek";
  peek.addEventListener("click", () => openPeek(source.id));
  const again = document.createElement("button");
  again.type = "button";
  again.textContent = "Retry";
  again.hidden = true;
  again.addEventListener("click", () => retry(player));
  actions.append(show, hear, peek, again);

  const problem = document.createElement("p");
  problem.className = "card-error";
  problem.hidden = true;

  body.append(head, uri, stats, actions, problem);
  root.append(thumb, body);
  el.sources.append(root);
  player.card = { root, thumb, dot, picture, sound, stats, show, hear, peek, again, problem };
}

function captureThumb(player) {
  const video = player.video;
  if (!player.card || video.readyState < 2 || video.videoWidth === 0) {
    return;
  }
  const context = player.card.thumb.getContext("2d");
  if (!context) {
    return;
  }
  try {
    context.drawImage(video, 0, 0, player.card.thumb.width, player.card.thumb.height);
  } catch (error) {
    log(`${player.source.id}: the browser refused to draw the thumbnail (${error.message})`, "warn");
  }
}

function updateCards() {
  if (!state.session) {
    return;
  }
  for (const source of state.session.sources) {
    const player = players.get(source.id);
    if (!player || !player.card) {
      continue;
    }
    const c = player.card;
    c.root.classList.toggle("shown", Boolean(source.shown));
    c.root.classList.toggle("peeking", state.peek === source.id);
    c.picture.classList.toggle("on", Boolean(source.shown));
    c.sound.classList.toggle("on", source.id === state.heard);
    c.show.disabled = Boolean(source.shown);
    c.hear.disabled = source.id === state.heard;
    c.peek.disabled = Boolean(source.shown) || state.peek === source.id;
    c.again.hidden = !player.lastError;
    c.problem.hidden = !player.lastError;
    c.problem.textContent = player.lastError;
    const ready = readyLabel(player.video);
    c.dot.className = `dot ${player.lastError ? "bad" : ready === "ready" || ready === "buffered" ? "ready" : ready === "idle" ? "" : "busy"}`;
    c.stats.textContent = `${ready} · ${format(player.video.currentTime)} / ${format(player.video.duration)}`;
  }
}

function shownID() {
  if (!state.session) {
    return "";
  }
  const shown = state.session.sources.find((source) => source.shown);
  return shown ? shown.id : "";
}

function shownPlayer() {
  return players.get(shownID());
}

function openPeek(id) {
  if (!id || id === shownID()) {
    return;
  }
  state.peek = id;
  const player = players.get(id);
  if (!player) {
    return;
  }
  el.insetVideo.append(player.video);
  player.video.classList.remove("hidden");
  el.inset.hidden = false;
  el.peekRow.hidden = false;
  el.insetLabel.textContent = `peek · ${id}`;
  el.inset.style.opacity = String(state.peekOpacity);
  el.peekOpacity.value = String(state.peekOpacity);
  el.peekOpacityOut.textContent = state.peekOpacity.toFixed(2);
  el.peekToggle.classList.add("on");
  log(`peek: ${id} floats over the picture; it changes nothing in the session`);
  clipInset();
  applyScene();
}

function closePeek() {
  if (!state.peek) {
    return;
  }
  const player = players.get(state.peek);
  const id = state.peek;
  state.peek = "";
  if (player) {
    el.stageVideo.append(player.video);
    player.video.classList.add("hidden");
  }
  el.inset.hidden = true;
  el.peekRow.hidden = true;
  el.peekToggle.classList.remove("on");
  log(`peek: closed ${id}`);
  applyScene();
}

// clipInset keeps the floating window inside the stage after a resize.
function clipInset() {
  if (el.inset.hidden) {
    return;
  }
  const stage = el.stage.getBoundingClientRect();
  const rect = el.inset.getBoundingClientRect();
  const maxLeft = Math.max(0, stage.width - rect.width - 8);
  const maxTop = Math.max(0, stage.height - rect.height - 8);
  el.inset.style.left = `${Math.min(Math.max(0, rect.left - stage.left), maxLeft)}px`;
  el.inset.style.top = `${Math.min(Math.max(0, rect.top - stage.top), maxTop)}px`;
}

function applyScene() {
  if (!state.session) {
    return;
  }
  const shown = shownID();
  for (const source of state.session.sources) {
    const player = players.get(source.id);
    if (!player) {
      continue;
    }
    player.source = source;
    const isShown = source.id === shown;
    const isPeek = source.id === state.peek;
    if (!isPeek) {
      player.video.className = isShown ? "player" : "player hidden";
    }
    const audible = state.interacted && !state.muted && source.id === state.heard;
    player.video.muted = !audible;
    player.video.volume = Math.max(0, Math.min(1, source.id === state.heard ? state.volume : source.volume));
  }

  if (state.peek && state.peek === shown) {
    closePeek();
  }

  const player = shownPlayer();
  el.tutorial.hidden = Boolean(shown) || Boolean(player && player.lastError);
  el.stageSource.hidden = !shown;
  if (shown) {
    el.stageSource.textContent = `${shown} · ${shortURI(player.source.url)}`;
  }
  el.liveChip.hidden = true;

  el.slotPicture.textContent = shown || "none";
  el.slotSound.textContent = state.heard || "none";
  el.chipSession.textContent = `${state.session.sources.length} source${state.session.sources.length === 1 ? "" : "s"} · scene ${state.session.scene}`;
  el.mute.textContent = state.muted ? "unmute" : "mute";
  el.mute.classList.toggle("on", state.muted);
  el.volume.value = String(state.volume);
  el.volumeOut.textContent = state.volume.toFixed(2);
  el.seek.disabled = !shown;

  updateTransport();
  updateCards();
  if (state.lastShown !== shown) {
    state.lastShown = shown;
    if (shown) {
      log(`picture is now ${shown}`);
    }
  }
}

function updateTransport() {
  const player = shownPlayer();
  if (!player) {
    el.position.textContent = "—";
    el.duration.textContent = "—";
    el.toggle.disabled = true;
    el.back.disabled = true;
    el.fwd.disabled = true;
    el.scrubPlayed.style.width = "0%";
    el.scrubBuffer.style.width = "0%";
    el.status.textContent = "no source is on the picture; pick one from the list";
    el.status.classList.remove("bad");
    el.liveChip.hidden = true;
    return;
  }
  const video = player.video;
  const duration = Number.isFinite(video.duration) ? video.duration : NaN;
  el.position.textContent = format(video.currentTime);
  el.duration.textContent = Number.isFinite(duration) ? format(duration) : "live";
  el.liveChip.hidden = Number.isFinite(duration);
  el.toggle.disabled = false;
  el.toggle.textContent = video.paused ? "▶" : "⏸";
  el.toggle.setAttribute("aria-label", video.paused ? "Play" : "Pause");
  el.back.disabled = !Number.isFinite(duration);
  el.fwd.disabled = !Number.isFinite(duration);

  if (Number.isFinite(duration) && duration > 0) {
    el.seek.max = String(duration);
    if (!state.dragging) {
      el.seek.value = String(video.currentTime);
    }
    const start = video.buffered.length ? video.buffered.start(0) : 0;
    const end = video.buffered.length ? video.buffered.end(video.buffered.length - 1) : 0;
    el.scrubBuffer.style.left = `${(start / duration) * 100}%`;
    el.scrubBuffer.style.width = `${Math.max(0, ((end - start) / duration) * 100)}%`;
    el.scrubPlayed.style.width = `${Math.max(0, (video.currentTime / duration) * 100)}%`;
  } else {
    el.seek.max = "1";
    el.seek.value = "0";
    el.scrubPlayed.style.width = "0%";
    el.scrubBuffer.style.width = "0%";
  }

  const soundSource = state.session.sources.find((source) => source.id === state.heard);
  el.status.classList.toggle("bad", Boolean(player.lastError));
  if (player.lastError) {
    el.status.textContent = `${player.source.id} cannot play this stream: ${player.lastError}`;
  } else {
    const parts = [`${player.source.id} shows ${format(video.currentTime)} of ${Number.isFinite(duration) ? format(duration) : "a live stream"}`];
    if (soundSource) {
      parts.push(`sound comes from ${soundSource.id}${state.muted ? " (muted)" : ""}`);
    }
    if (!state.interacted) {
      parts.push("click the page once to let the browser play sound");
    }
    if (state.peek) {
      parts.push(`peeking at ${state.peek}`);
    }
    el.status.textContent = parts.join(" · ");
  }
}

function nudge(seconds) {
  const player = shownPlayer();
  if (!player) {
    return;
  }
  const duration = Number.isFinite(player.video.duration) ? player.video.duration : 0;
  if (!duration) {
    log("this source has no known duration, it cannot be moved by hand", "error");
    return;
  }
  const target = Math.min(duration - 0.1, Math.max(0, player.video.currentTime + seconds));
  player.video.currentTime = target;
  log(`seek asked for ${format(target)} of ${format(duration)}`);
}

async function requestScene(videoID, audioID) {
  const payload = { video: videoID, audio: audioID, volume: state.volume, muted: state.muted };
  const updated = await api("/api/scene", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  state.session = updated;
  state.heard = audioID;
  log(`scene: video ${videoID}, audio ${audioID}`);
  applyScene();
}

function adoptedScene(fresh) {
  const shown = fresh.sources.find((source) => source.shown);
  const heard = fresh.sources.find((source) => !source.muted) || fresh.sources[0];
  return {
    video: shown ? shown.id : "",
    audio: heard ? heard.id : "",
    volume: heard ? heard.volume : state.volume,
    muted: heard ? heard.muted : state.muted,
  };
}

async function refreshSession(force = false) {
  let fresh;
  try {
    fresh = await api("/api/session");
  } catch (error) {
    if (!state.offline) {
      state.offline = true;
      el.chipConnection.hidden = false;
      log(`the session could not be read: ${error.message}`, "error");
    }
    return;
  }
  if (state.offline) {
    state.offline = false;
    el.chipConnection.hidden = true;
  }
  const known = new Set(players.keys());
  const added = fresh.sources.filter((source) => !known.has(source.id));
  const scene = adoptedScene(fresh);
  const changed =
    force || added.length > 0 || scene.video !== shownID() || scene.audio !== state.heard || scene.volume !== state.volume || scene.muted !== state.muted;
  state.session = fresh;
  if (!changed) {
    return;
  }
  for (const source of added) {
    const player = buildPlayer(source);
    player.video.play().catch((error) => log(`${source.id}: play() refused: ${error.message}`, "error"));
  }
  state.heard = scene.audio;
  state.volume = scene.volume;
  state.muted = scene.muted;
  if (added.length) {
    log(`session now lists ${added.map((source) => source.id).join(", ")}`);
  }
  applyScene();
}

function startPlayback() {
  for (const player of players.values()) {
    const result = player.video.play();
    if (result && typeof result.catch === "function") {
      result.catch((error) => log(`${player.source.id}: play() refused: ${error.message}`, "error"));
    }
  }
}

async function start() {
  describeEnvironment();
  try {
    state.session = await api("/api/session");
  } catch (error) {
    log(`the session could not be read: ${error.message}`, "error");
    log("open the address printed at startup, which carries the token once", "error");
    el.status.textContent = "the session is not reachable; open the address printed at startup";
    el.status.classList.add("bad");
    return;
  }
  const scene = adoptedScene(state.session);
  state.heard = scene.audio;
  state.volume = scene.volume;
  state.muted = scene.muted;
  for (const source of state.session.sources) {
    buildPlayer(source);
  }
  applyScene();
  startPlayback();
  setInterval(updateTransport, 250);
  setInterval(updateCards, 1000);
  setInterval(() => {
    for (const player of players.values()) {
      captureThumb(player);
    }
  }, 4000);
  setInterval(() => refreshSession(false), 2000);
}

/* ---------- controls ---------- */

el.toggle.addEventListener("click", () => {
  const player = shownPlayer();
  if (!player) {
    return;
  }
  if (player.video.ended) {
    player.video.currentTime = 0;
  }
  if (player.video.paused) {
    player.video.play().catch((error) => log(`play() refused: ${error.message}`, "error"));
    log("played the picture");
  } else {
    player.video.pause();
    log("paused the picture");
  }
});

el.back.addEventListener("click", () => nudge(-10));
el.fwd.addEventListener("click", () => nudge(10));

el.mute.addEventListener("click", () => {
  state.muted = !state.muted;
  applyScene();
  const video = shownID() || state.session.sources[0]?.id || "";
  requestScene(video, state.heard || video).catch((error) => log(error.message, "error"));
});

el.volume.addEventListener("input", () => {
  state.volume = Number(el.volume.value);
  el.volumeOut.textContent = state.volume.toFixed(2);
  applyScene();
});

el.volume.addEventListener("change", () => {
  const video = shownID() || state.session.sources[0]?.id || "";
  requestScene(video, state.heard || video).catch((error) => log(error.message, "error"));
});

el.seek.addEventListener("pointerdown", () => {
  state.dragging = true;
});
el.seek.addEventListener("pointerup", () => {
  state.dragging = false;
});
el.seek.addEventListener("input", () => {
  const player = shownPlayer();
  if (!player) {
    return;
  }
  const duration = Number.isFinite(player.video.duration) ? player.video.duration : 0;
  if (!duration) {
    return;
  }
  player.video.currentTime = Number(el.seek.value);
  el.position.textContent = format(player.video.currentTime);
});
el.seek.addEventListener("change", () => {
  state.dragging = false;
  const player = shownPlayer();
  if (player) {
    log(`seek asked for ${format(player.video.currentTime)} of ${format(player.video.duration)}`);
  }
});

el.peekToggle.addEventListener("click", () => {
  if (state.peek) {
    closePeek();
    return;
  }
  const candidate = state.session.sources.find((source) => !source.shown);
  if (!candidate) {
    log("there is no other picture to peek at", "warn");
    return;
  }
  openPeek(candidate.id);
});

el.insetClose.addEventListener("click", closePeek);

el.peekOpacity.addEventListener("input", () => {
  state.peekOpacity = Number(el.peekOpacity.value);
  el.peekOpacityOut.textContent = state.peekOpacity.toFixed(2);
  el.inset.style.opacity = String(state.peekOpacity);
});

el.fullscreen.addEventListener("click", () => {
  if (document.fullscreenElement) {
    document.exitFullscreen().catch((error) => log(error.message, "warn"));
    return;
  }
  el.stage.requestFullscreen().catch((error) => log(`full screen was refused: ${error.message}`, "warn"));
});

el.refresh.addEventListener("click", () => {
  refreshSession(true).catch((error) => log(error.message, "error"));
});

el.clearLog.addEventListener("click", () => {
  el.log.textContent = "";
});

/* ---------- peek dragging ---------- */

let drag = null;
el.insetBar.addEventListener("pointerdown", (event) => {
  if (event.target === el.insetClose) {
    return;
  }
  const stage = el.stage.getBoundingClientRect();
  const rect = el.inset.getBoundingClientRect();
  drag = { x: event.clientX, y: event.clientY, left: rect.left - stage.left, top: rect.top - stage.top };
  el.insetBar.setPointerCapture(event.pointerId);
});
el.insetBar.addEventListener("pointermove", (event) => {
  if (!drag) {
    return;
  }
  const stage = el.stage.getBoundingClientRect();
  const rect = el.inset.getBoundingClientRect();
  const left = Math.min(Math.max(0, drag.left + event.clientX - drag.x), Math.max(0, stage.width - rect.width));
  const top = Math.min(Math.max(0, drag.top + event.clientY - drag.y), Math.max(0, stage.height - rect.height));
  el.inset.style.left = `${left}px`;
  el.inset.style.top = `${top}px`;
});
el.insetBar.addEventListener("pointerup", () => {
  drag = null;
});

window.addEventListener("resize", clipInset);

/* ---------- keyboard ---------- */

document.addEventListener("keydown", (event) => {
  const tag = (event.target && event.target.tagName) || "";
  if (tag === "INPUT" || tag === "TEXTAREA") {
    return;
  }
  if (event.key === " ") {
    event.preventDefault();
    el.toggle.click();
    return;
  }
  if (event.key === "ArrowLeft") {
    event.preventDefault();
    nudge(-10);
    return;
  }
  if (event.key === "ArrowRight") {
    event.preventDefault();
    nudge(10);
    return;
  }
  if (event.key === "m" || event.key === "M") {
    el.mute.click();
    return;
  }
  if (event.key === "p" || event.key === "P") {
    el.peekToggle.click();
    return;
  }
  if (/^[1-9]$/.test(event.key) && state.session) {
    const source = state.session.sources[Number(event.key) - 1];
    if (source) {
      requestScene(source.id, state.heard || source.id).catch((error) => log(error.message, "error"));
    }
  }
});

start().catch((error) => log(error.message, "error"));
