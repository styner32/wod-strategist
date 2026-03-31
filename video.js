const CONFIG_STORAGE_KEY = "video-qa:config";
const SESSION_STORAGE_KEY = "video-qa:session";
const VIDEO_CACHE_NAME = "video-qa-cache-v1";

const DEFAULT_CONFIG = {
  apiBaseUrl: "http://localhost:8088/api/v1",
  apiKey: "",
  profileId: "",
  workoutType: "wod",
  movements: "",
  injuries: "",
};

const DEFAULT_SESSION = {
  sessionId: "",
  maxDuration: "60",
};

const state = {
  chunkFiles: [],
  sessionCatalog: [],
  assetResponse: null,
  assetMap: new Map(),
  playerSlots: {
    left: {
      objectUrl: null,
      subtitleUrl: null,
      asset: null,
    },
    right: {
      objectUrl: null,
      subtitleUrl: null,
      asset: null,
    },
  },
  subtitleBlobs: new Map(),
};

const refs = {};

document.addEventListener("DOMContentLoaded", () => {
  cacheDom();
  restorePersistedState();
  bindEvents();
  logLine("Workbench ready.");
  renderSessionCatalog([]);
  maybeRefreshSessionCatalog();

  if (refs.sessionId.value.trim()) {
    refreshSessionAssets().catch((error) => {
      logLine(`Initial refresh failed: ${error.message}`, "error");
    });
  }
});

function cacheDom() {
  refs.configForm = document.getElementById("configForm");
  refs.apiBaseUrl = document.getElementById("apiBaseUrl");
  refs.apiKey = document.getElementById("apiKey");
  refs.profileId = document.getElementById("profileId");
  refs.workoutType = document.getElementById("workoutType");
  refs.movements = document.getElementById("movements");
  refs.injuries = document.getElementById("injuries");
  refs.resetConfigBtn = document.getElementById("resetConfigBtn");
  refs.configStatus = document.getElementById("configStatus");

  refs.sessionId = document.getElementById("sessionId");
  refs.maxDuration = document.getElementById("maxDuration");
  refs.chunkFiles = document.getElementById("chunkFiles");
  refs.selectedFiles = document.getElementById("selectedFiles");
  refs.refreshSessionBtn = document.getElementById("refreshSessionBtn");
  refs.uploadChunksBtn = document.getElementById("uploadChunksBtn");
  refs.mergeChunksBtn = document.getElementById("mergeChunksBtn");
  refs.generateHighlightBtn = document.getElementById("generateHighlightBtn");
  refs.refreshAssetsBtn = document.getElementById("refreshAssetsBtn");
  refs.sessionStatus = document.getElementById("sessionStatus");
  refs.sessionCatalog = document.getElementById("sessionCatalog");
  refs.sessionCatalogStatus = document.getElementById("sessionCatalogStatus");
  refs.refreshCatalogBtn = document.getElementById("refreshCatalogBtn");

  refs.chunkSummaryValue = document.getElementById("chunkSummaryValue");
  refs.analysisStatusValue = document.getElementById("analysisStatusValue");
  refs.subtitleStatusValue = document.getElementById("subtitleStatusValue");
  refs.assetCountValue = document.getElementById("assetCountValue");

  refs.assetGroups = document.getElementById("assetGroups");
  refs.activityLog = document.getElementById("activityLog");
  refs.clearLogBtn = document.getElementById("clearLogBtn");

  refs.leftVideo = document.getElementById("leftVideo");
  refs.leftTrack = document.getElementById("leftTrack");
  refs.leftPlayerLabel = document.getElementById("leftPlayerLabel");
  refs.leftPlayerMeta = document.getElementById("leftPlayerMeta");
  refs.leftPlayerCard = document.querySelector('.player-card[data-slot="left"]');
  refs.rightVideo = document.getElementById("rightVideo");
  refs.rightTrack = document.getElementById("rightTrack");
  refs.rightPlayerLabel = document.getElementById("rightPlayerLabel");
  refs.rightPlayerMeta = document.getElementById("rightPlayerMeta");
  refs.rightPlayerCard = document.querySelector('.player-card[data-slot="right"]');
}

function bindEvents() {
  refs.configForm.addEventListener("submit", (event) => {
    event.preventDefault();
    saveConfig();
    maybeRefreshSessionCatalog();
  });

  refs.resetConfigBtn.addEventListener("click", () => {
    applyConfig(DEFAULT_CONFIG);
    saveConfig();
    maybeRefreshSessionCatalog();
  });

  refs.sessionId.addEventListener("change", handleSessionInputChange);
  refs.maxDuration.addEventListener("change", persistSessionInputs);
  refs.chunkFiles.addEventListener("change", handleChunkSelection);

  refs.refreshSessionBtn.addEventListener("click", () => {
    runButtonAction(refs.refreshSessionBtn, "Refreshing...", async () => {
      await refreshSessionAssets();
    });
  });

  refs.refreshAssetsBtn.addEventListener("click", () => {
    runButtonAction(refs.refreshAssetsBtn, "Reloading...", async () => {
      await refreshSessionAssets();
    });
  });

  refs.refreshCatalogBtn.addEventListener("click", () => {
    runCatalogButton(refs.refreshCatalogBtn, "Loading...", async () => {
      await refreshSessionCatalog();
    });
  });

  refs.uploadChunksBtn.addEventListener("click", () => {
    runButtonAction(refs.uploadChunksBtn, "Uploading...", async () => {
      await uploadSelectedChunks();
    });
  });

  refs.mergeChunksBtn.addEventListener("click", () => {
    runButtonAction(refs.mergeChunksBtn, "Queueing...", async () => {
      await triggerMerge();
    });
  });

  refs.generateHighlightBtn.addEventListener("click", () => {
    runButtonAction(refs.generateHighlightBtn, "Queueing...", async () => {
      await triggerHighlightGeneration();
    });
  });

  refs.assetGroups.addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }

    const asset = state.assetMap.get(button.dataset.assetKey || "");
    if (!asset) {
      return;
    }

    const action = button.dataset.action;
    if (action === "load-left") {
      await runAssetButton(button, "Loading...", () => loadAssetIntoSlot("left", asset, false));
    } else if (action === "load-right") {
      await runAssetButton(button, "Loading...", () => loadAssetIntoSlot("right", asset, false));
    } else if (action === "refresh-left") {
      await runAssetButton(button, "Refreshing...", () => loadAssetIntoSlot("left", asset, true));
    } else if (action === "refresh-right") {
      await runAssetButton(button, "Refreshing...", () => loadAssetIntoSlot("right", asset, true));
    } else if (action === "open") {
      await runAssetButton(button, "Opening...", () => openAssetSignedUrl(asset));
    }
  });

  refs.clearLogBtn.addEventListener("click", () => {
    refs.activityLog.textContent = "";
    logLine("Activity log cleared.");
  });

  refs.sessionCatalog.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action='use-session']");
    if (!button) {
      return;
    }

    runCatalogButton(button, "Loading...", async () => {
      await selectExistingSession(button.dataset.sessionId || "");
    });
  });
}

function restorePersistedState() {
  const savedConfig = readJsonStorage(CONFIG_STORAGE_KEY, DEFAULT_CONFIG);
  const savedSession = readJsonStorage(SESSION_STORAGE_KEY, DEFAULT_SESSION);

  applyConfig(savedConfig);
  refs.sessionId.value = savedSession.sessionId || "";
  refs.maxDuration.value = savedSession.maxDuration || DEFAULT_SESSION.maxDuration;
}

function applyConfig(config) {
  refs.apiBaseUrl.value = config.apiBaseUrl || DEFAULT_CONFIG.apiBaseUrl;
  refs.apiKey.value = config.apiKey || "";
  refs.profileId.value = config.profileId || "";
  refs.workoutType.value = config.workoutType || DEFAULT_CONFIG.workoutType;
  refs.movements.value = config.movements || "";
  refs.injuries.value = config.injuries || "";
}

function saveConfig() {
  const config = getRuntimeConfig();
  localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
  setStatus(refs.configStatus, "Settings saved.");
  logLine("Runtime settings saved.");
}

function handleSessionInputChange() {
  persistSessionInputs();
  renderSessionCatalog(state.sessionCatalog);
}

function persistSessionInputs() {
  const payload = {
    sessionId: refs.sessionId.value.trim(),
    maxDuration: refs.maxDuration.value.trim() || DEFAULT_SESSION.maxDuration,
  };
  localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(payload));
}

function maybeRefreshSessionCatalog() {
  if (!hasCatalogConfig()) {
    state.sessionCatalog = [];
    renderSessionCatalog([]);
    setStatus(refs.sessionCatalogStatus, "Set API base URL and API key to browse uploaded sessions.");
    return;
  }

  refreshSessionCatalog().catch((error) => {
    reportCatalogError(error);
  });
}

function getRuntimeConfig() {
  return {
    apiBaseUrl: normalizeApiBase(refs.apiBaseUrl.value),
    apiKey: refs.apiKey.value.trim(),
    profileId: refs.profileId.value.trim(),
    workoutType: refs.workoutType.value.trim() || DEFAULT_CONFIG.workoutType,
    movements: refs.movements.value.trim(),
    injuries: refs.injuries.value.trim(),
  };
}

function getActionContext() {
  const config = getRuntimeConfig();
  const sessionId = refs.sessionId.value.trim();
  const profileId = Number.parseInt(config.profileId, 10);
  const maxDuration = Number.parseInt(refs.maxDuration.value, 10);

  if (!sessionId) {
    throw new Error("Session ID is required.");
  }
  if (!config.apiKey) {
    throw new Error("API key is required.");
  }
  if (!config.apiBaseUrl) {
    throw new Error("API base URL is required.");
  }
  if (!Number.isInteger(profileId) || profileId <= 0) {
    throw new Error("Profile ID must be a positive integer.");
  }
  if (!Number.isInteger(maxDuration) || maxDuration < 1 || maxDuration > 120) {
    throw new Error("Highlight max duration must be between 1 and 120.");
  }

  persistSessionInputs();

  return {
    sessionId,
    profileId,
    workoutType: config.workoutType,
    movements: splitCsv(config.movements),
    injuries: splitCsv(config.injuries),
    maxDuration,
    apiBaseUrl: config.apiBaseUrl,
    apiKey: config.apiKey,
  };
}

function hasCatalogConfig() {
  const config = getRuntimeConfig();
  return Boolean(normalizeApiBase(config.apiBaseUrl) && config.apiKey);
}

async function refreshSessionCatalog() {
  if (!hasCatalogConfig()) {
    state.sessionCatalog = [];
    renderSessionCatalog([]);
    setStatus(refs.sessionCatalogStatus, "Set API base URL and API key to browse uploaded sessions.");
    return;
  }

  setStatus(refs.sessionCatalogStatus, "Loading existing sessions...");

  const response = await apiRequest("/dev/sessions");
  state.sessionCatalog = Array.isArray(response.sessions) ? response.sessions : [];
  renderSessionCatalog(state.sessionCatalog);

  const count = state.sessionCatalog.length;
  setStatus(
    refs.sessionCatalogStatus,
    count > 0 ? `Loaded ${count} existing session${count === 1 ? "" : "s"}.` : "No existing sessions found."
  );
  logLine(`Session catalog refreshed with ${count} session${count === 1 ? "" : "s"}.`);
}

function renderSessionCatalog(sessions) {
  if (!sessions.length) {
    refs.sessionCatalog.classList.add("empty-state");
    refs.sessionCatalog.innerHTML = `<div class="empty-state">No uploaded sessions available yet.</div>`;
    return;
  }

  const selectedSessionId = refs.sessionId.value.trim();
  refs.sessionCatalog.classList.remove("empty-state");
  refs.sessionCatalog.innerHTML = sessions.map((session) => renderSessionCard(session, selectedSessionId)).join("");
}

function renderSessionCard(session, selectedSessionId) {
  const sessionId = String(session.session_id || "");
  const isSelected = sessionId === selectedSessionId;
  const tags = [
    `${session.chunk_count || 0} chunk${session.chunk_count === 1 ? "" : "s"}`,
    session.has_merged ? "merged" : "",
    session.has_hardsubbed ? "hardsubbed" : "",
    (session.highlight_count || 0) > 0 ? `${session.highlight_count} highlight${session.highlight_count === 1 ? "" : "s"}` : "",
  ].filter(Boolean);

  return `
    <article class="session-card ${isSelected ? "selected" : ""}">
      <div class="session-card-header">
        <div>
          <span class="panel-kicker">session</span>
          <strong>${escapeHtml(sessionId)}</strong>
        </div>
        <time datetime="${escapeHtml(session.latest_created_at || "")}">${escapeHtml(formatTimestamp(session.latest_created_at))}</time>
      </div>
      <div class="session-tags">
        ${tags.map((tag) => `<span class="session-tag">${escapeHtml(tag)}</span>`).join("")}
      </div>
      <div class="asset-actions">
        <button type="button" class="secondary" data-action="use-session" data-session-id="${escapeHtml(sessionId)}">Load Session</button>
      </div>
    </article>
  `;
}

async function selectExistingSession(sessionId) {
  if (!sessionId) {
    throw new Error("Selected session is invalid.");
  }

  refs.sessionId.value = sessionId;
  handleSessionInputChange();
  setStatus(refs.sessionStatus, `Selected ${sessionId}.`);
  await refreshSessionAssets();
}

async function handleChunkSelection(event) {
  const files = Array.from(event.target.files || []).sort((left, right) =>
    left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: "base" })
  );

  state.chunkFiles = files.map((file) => ({
    file,
    duration: null,
    error: null,
  }));

  renderSelectedFiles();
  logLine(`Selected ${state.chunkFiles.length} chunk file(s).`);

  await Promise.all(
    state.chunkFiles.map(async (entry) => {
      try {
        entry.duration = await readVideoDuration(entry.file);
      } catch (error) {
        entry.error = error instanceof Error ? error.message : String(error);
      }
    })
  );

  renderSelectedFiles();
}

function renderSelectedFiles() {
  if (state.chunkFiles.length === 0) {
    refs.selectedFiles.innerHTML = `<div class="empty-state">No chunk files selected.</div>`;
    return;
  }

  refs.selectedFiles.innerHTML = state.chunkFiles
    .map((entry, index) => {
      const duration = entry.duration == null ? "Analyzing..." : formatSeconds(entry.duration);
      const meta = entry.error ? escapeHtml(entry.error) : `${duration} · ${formatBytes(entry.file.size)}`;
      return `
        <div class="file-item">
          <div>
            <strong>${String(index + 1).padStart(2, "0")} · ${escapeHtml(entry.file.name)}</strong>
          </div>
          <span>${meta}</span>
        </div>
      `;
    })
    .join("");
}

async function uploadSelectedChunks() {
  const context = getActionContext();
  if (state.chunkFiles.length === 0) {
    throw new Error("Select chunk files before uploading.");
  }

  let cursor = 0;
  setStatus(refs.sessionStatus, "Uploading chunks...");
  logLine(`Starting chunk upload for session ${context.sessionId}.`);

  for (let index = 0; index < state.chunkFiles.length; index += 1) {
    const entry = state.chunkFiles[index];
    if (entry.duration == null) {
      entry.duration = await readVideoDuration(entry.file);
      renderSelectedFiles();
    }

    const duration = roundSeconds(entry.duration || 0);
    const filename = `chunk_${String(index + 1).padStart(4, "0")}_${sanitizeFilename(entry.file.name)}`;
    const startSeconds = roundSeconds(cursor);
    const endSeconds = roundSeconds(cursor + duration);

    setStatus(refs.sessionStatus, `Uploading chunk ${index + 1}/${state.chunkFiles.length}: ${filename}`);

    const uploadDescriptor = await apiRequest("/upload-url", {
      method: "POST",
      body: {
        session_id: context.sessionId,
        filename,
      },
    });

    await uploadFileToSignedUrl(uploadDescriptor.upload_url, entry.file);

    await apiRequest("/chunk-complete", {
      method: "POST",
      body: {
        session_id: context.sessionId,
        gcs_uri: uploadDescriptor.gcs_uri,
        movements: context.movements,
        injuries: context.injuries,
        workout_type: context.workoutType,
        profile_id: context.profileId,
        start_secs: startSeconds,
        end_secs: endSeconds,
      },
    });

    logLine(
      `Chunk ${index + 1}/${state.chunkFiles.length} uploaded (${formatSeconds(startSeconds)} -> ${formatSeconds(endSeconds)}).`
    );

    cursor = endSeconds;
  }

  setStatus(refs.sessionStatus, "Chunk upload flow completed.");
  await refreshSessionAssets();
  await refreshSessionCatalog().catch((error) => {
    reportCatalogError(error);
  });
}

async function triggerMerge() {
  const context = getActionContext();
  setStatus(refs.sessionStatus, "Queueing merge job...");

  const response = await apiRequest("/merge-chunks", {
    method: "POST",
    body: {
      session_id: context.sessionId,
      workout_type: context.workoutType,
      movements: context.movements,
      injuries: context.injuries,
      profile_id: context.profileId,
    },
  });

  setStatus(refs.sessionStatus, `Merge queued: ${response.task_id}`);
  logLine(`Merge queued for ${context.sessionId} (${response.task_id}).`);
  await refreshSessionAssets();
  await refreshSessionCatalog().catch((error) => {
    reportCatalogError(error);
  });
}

async function triggerHighlightGeneration() {
  const context = getActionContext();
  setStatus(refs.sessionStatus, "Queueing highlight generation...");

  const response = await apiRequest("/generate-highlight", {
    method: "POST",
    body: {
      session_id: context.sessionId,
      profile_id: context.profileId,
      max_duration: context.maxDuration,
    },
  });

  setStatus(refs.sessionStatus, `Highlight generation queued: ${response.task_id}`);
  logLine(`Highlight generation queued for ${context.sessionId} (${response.task_id}).`);
  await refreshSessionAssets();
  await refreshSessionCatalog().catch((error) => {
    reportCatalogError(error);
  });
}

async function refreshSessionAssets() {
  const sessionId = refs.sessionId.value.trim();
  if (!sessionId) {
    throw new Error("Session ID is required.");
  }

  setStatus(refs.sessionStatus, "Refreshing session assets...");

  const response = await apiRequest(`/dev/sessions/${encodeURIComponent(sessionId)}/assets`);
  state.assetResponse = response;
  state.assetMap.clear();

  renderSummary(response);
  renderAssets(response.assets || []);
  renderSessionCatalog(state.sessionCatalog);

  setStatus(refs.sessionStatus, `Loaded ${response.assets.length} asset(s) for ${sessionId}.`);
  logLine(`Session ${sessionId} refreshed with ${response.assets.length} asset(s).`);
}

function renderSummary(response) {
  const summary = response.chunk_summary || { total: 0, completed: 0, failed: 0, pending: 0 };
  refs.chunkSummaryValue.textContent = `${summary.completed}/${summary.total} completed · ${summary.failed} failed · ${summary.pending} pending`;
  refs.analysisStatusValue.textContent = response.full_analysis
    ? `${response.full_analysis.status} · ${response.full_analysis.analysis_type || "wod"}`
    : "No full analysis";
  refs.subtitleStatusValue.textContent = response.subtitle_available ? "Available" : "Not available";
  refs.assetCountValue.textContent = String((response.assets || []).length);
}

function renderAssets(assets) {
  if (!assets.length) {
    refs.assetGroups.classList.add("empty-state");
    refs.assetGroups.innerHTML = `<div class="empty-state">No playable assets found for this session yet.</div>`;
    return;
  }

  const groups = new Map();
  const titles = {
    chunk: "Chunks",
    merged: "Merged",
    hardsubbed: "Hardsubbed",
    highlight: "Highlights",
  };
  const order = ["chunk", "merged", "hardsubbed", "highlight"];

  for (const asset of assets) {
    const key = buildAssetKey(asset);
    state.assetMap.set(key, asset);
    if (!groups.has(asset.kind)) {
      groups.set(asset.kind, []);
    }
    groups.get(asset.kind).push({ asset, key });
  }

  refs.assetGroups.classList.remove("empty-state");
  refs.assetGroups.innerHTML = order
    .filter((kind) => groups.has(kind))
    .map((kind) => {
      const entries = groups.get(kind) || [];
      return `
        <section class="asset-group">
          <h3>${titles[kind] || escapeHtml(kind)}</h3>
          <div class="asset-grid">
            ${entries
              .map(({ asset, key }) => renderAssetCard(asset, key))
              .join("")}
          </div>
        </section>
      `;
    })
    .join("");

  updateCacheBadges().catch((error) => {
    logLine(`Failed to update cache badges: ${error.message}`, "error");
  });
}

function renderAssetCard(asset, assetKey) {
  const variant = asset.variant ? ` · ${escapeHtml(asset.variant)}` : "";
  const locationLine = asset.public_url || asset.object_name;
  return `
    <article class="asset-card">
      <div>
        <span class="panel-kicker">${escapeHtml(asset.kind)}${variant}</span>
        <strong>${escapeHtml(asset.label)}</strong>
      </div>
      <div class="asset-meta">
        <span>${escapeHtml(locationLine)}</span>
        <span>${escapeHtml(asset.created_at || "unknown time")}</span>
        <span id="cache-${escapeHtml(assetKey)}" class="cache-pill">Checking cache...</span>
      </div>
      <div class="asset-actions">
        <button type="button" class="secondary" data-action="load-left" data-asset-key="${escapeHtml(assetKey)}">Left</button>
        <button type="button" class="secondary" data-action="load-right" data-asset-key="${escapeHtml(assetKey)}">Right</button>
        <button type="button" class="ghost" data-action="refresh-left" data-asset-key="${escapeHtml(assetKey)}">Left Refresh</button>
        <button type="button" class="ghost" data-action="refresh-right" data-asset-key="${escapeHtml(assetKey)}">Right Refresh</button>
        <button type="button" class="ghost" data-action="open" data-asset-key="${escapeHtml(assetKey)}">Open Signed URL</button>
      </div>
    </article>
  `;
}

async function updateCacheBadges() {
  const cache = await caches.open(VIDEO_CACHE_NAME);
  for (const [key, asset] of state.assetMap.entries()) {
    const badge = document.getElementById(`cache-${key}`);
    if (!badge) {
      continue;
    }
    const hit = Boolean(await cache.match(makeCacheRequest(asset.cache_key)));
    badge.textContent = hit ? "Cache hit" : "Cache miss";
    badge.className = `cache-pill ${hit ? "hit" : "miss"}`;
  }
}

async function loadAssetIntoSlot(slotName, asset, forceRefresh) {
  const videoRef = slotName === "left" ? refs.leftVideo : refs.rightVideo;
  const labelRef = slotName === "left" ? refs.leftPlayerLabel : refs.rightPlayerLabel;
  const metaRef = slotName === "left" ? refs.leftPlayerMeta : refs.rightPlayerMeta;
  const trackRef = slotName === "left" ? refs.leftTrack : refs.rightTrack;

  releaseSlot(slotName);
  setPlayerOrientation(slotName, "portrait");

  labelRef.textContent = `${asset.label} (${asset.kind}${asset.variant ? `/${asset.variant}` : ""})`;
  metaRef.textContent = "Loading asset...";

  const playable = await ensurePlayableAsset(asset, forceRefresh);
  state.playerSlots[slotName].objectUrl = playable.objectUrl;
  state.playerSlots[slotName].asset = asset;

  videoRef.src = playable.objectUrl;
  videoRef.load();
  await waitForVideoMetadata(videoRef);
  const orientation = detectVideoOrientation(videoRef);
  setPlayerOrientation(slotName, orientation);

  if (asset.kind === "merged" && state.assetResponse?.subtitle_available) {
    const subtitleUrl = await ensureSubtitleBlobUrl(state.assetResponse.session_id);
    if (subtitleUrl) {
      state.playerSlots[slotName].subtitleUrl = subtitleUrl;
      trackRef.src = subtitleUrl;
      if (videoRef.textTracks && videoRef.textTracks[0]) {
        videoRef.textTracks[0].mode = "showing";
      }
    }
  } else {
    trackRef.removeAttribute("src");
    if (videoRef.textTracks && videoRef.textTracks[0]) {
      videoRef.textTracks[0].mode = "disabled";
    }
  }

  metaRef.textContent = `${playable.source === "cache" ? "Cache hit" : "Fetched"} · ${formatVideoMeta(videoRef)} · ${asset.created_at}`;
  setStatus(refs.sessionStatus, `Loaded ${asset.label} into the ${slotName.toUpperCase()} player.`);
  logLine(`Loaded ${asset.label} into ${slotName.toUpperCase()} (${playable.source}).`);
  await updateCacheBadges();
}

async function openAssetSignedUrl(asset) {
  const playUrl = await fetchPlayDescriptor(asset);
  window.open(playUrl.signed_url || playUrl.public_url, "_blank", "noopener");
  logLine(`Opened signed URL for ${asset.label}.`);
}

async function ensurePlayableAsset(asset, forceRefresh) {
  const cache = await caches.open(VIDEO_CACHE_NAME);
  const cacheRequest = makeCacheRequest(asset.cache_key);

  if (!forceRefresh) {
    const cached = await cache.match(cacheRequest);
    if (cached) {
      const blob = await cached.blob();
      return {
        source: "cache",
        objectUrl: URL.createObjectURL(blob),
      };
    }
  }

  const playDescriptor = await fetchPlayDescriptor(asset);
  const assetUrl = playDescriptor.signed_url || playDescriptor.public_url;
  if (!assetUrl) {
    throw new Error(`No playable URL returned for ${asset.label}.`);
  }

  const response = await fetch(assetUrl, { method: "GET" });
  if (!response.ok) {
    const text = await safeReadText(response);
    throw new Error(`Signed GET failed: ${response.status} ${text || response.statusText}`);
  }

  const clone = response.clone();
  const blob = await response.blob();
  try {
    await cache.put(cacheRequest, clone);
  } catch (error) {
    logLine(`Cache write skipped for ${asset.label}: ${toMessage(error)}`, "error");
  }

  return {
    source: "network",
    objectUrl: URL.createObjectURL(blob),
  };
}

async function ensureSubtitleBlobUrl(sessionId) {
  if (state.subtitleBlobs.has(sessionId)) {
    return state.subtitleBlobs.get(sessionId);
  }

  const subtitleText = await apiRequest(`/subtitles/${encodeURIComponent(sessionId)}`, {
    expect: "text",
  });

  if (!subtitleText.trim()) {
    return null;
  }

  const blobUrl = URL.createObjectURL(
    new Blob([subtitleText], { type: "application/x-subrip;charset=utf-8" })
  );
  state.subtitleBlobs.set(sessionId, blobUrl);
  return blobUrl;
}

async function fetchPlayDescriptor(asset) {
  const params = new URLSearchParams({ kind: asset.kind });
  if (asset.kind === "chunk" && asset.object_name) {
    params.set("variant", basename(asset.object_name));
  } else if (asset.variant) {
    params.set("variant", asset.variant);
  }
  return apiRequest(
    `/dev/sessions/${encodeURIComponent(refs.sessionId.value.trim())}/play-url?${params.toString()}`
  );
}

async function uploadFileToSignedUrl(uploadUrl, file) {
  const response = await fetch(uploadUrl, {
    method: "PUT",
    headers: {
      "Content-Type": file.type || "video/mp4",
    },
    body: file,
  });

  if (!response.ok) {
    const text = await safeReadText(response);
    throw new Error(`Signed PUT failed: ${response.status} ${text || response.statusText}`);
  }
}

function releaseSlot(slotName) {
  const slot = state.playerSlots[slotName];
  if (slot.objectUrl) {
    URL.revokeObjectURL(slot.objectUrl);
    slot.objectUrl = null;
  }
  slot.asset = null;
  setPlayerOrientation(slotName, "portrait");
}

async function apiRequest(path, options = {}) {
  const { method = "GET", body, headers = {}, expect = "json" } = options;
  const config = getRuntimeConfig();
  const apiBase = normalizeApiBase(config.apiBaseUrl);
  const url = `${apiBase}${path.startsWith("/") ? path : `/${path}`}`;

  const requestHeaders = new Headers(headers);
  if (config.apiKey) {
    requestHeaders.set("X-API-Key", config.apiKey);
  }
  if (body !== undefined && !requestHeaders.has("Content-Type")) {
    requestHeaders.set("Content-Type", "application/json");
  }

  const response = await fetch(url, {
    method,
    headers: requestHeaders,
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (!response.ok) {
    const text = await safeReadText(response);
    throw new Error(`${method} ${path} failed: ${response.status} ${text || response.statusText}`);
  }

  if (expect === "text") {
    return response.text();
  }
  if (response.status === 204) {
    return null;
  }

  const contentType = response.headers.get("content-type") || "";
  if (expect === "json" && contentType.includes("application/json")) {
    return response.json();
  }
  return response.text();
}

async function readVideoDuration(file) {
  const objectUrl = URL.createObjectURL(file);
  const video = document.createElement("video");
  video.preload = "metadata";
  video.src = objectUrl;

  return new Promise((resolve, reject) => {
    video.onloadedmetadata = () => {
      const duration = video.duration;
      URL.revokeObjectURL(objectUrl);
      if (!Number.isFinite(duration) || duration <= 0) {
        reject(new Error(`Could not read duration for ${file.name}.`));
        return;
      }
      resolve(duration);
    };

    video.onerror = () => {
      URL.revokeObjectURL(objectUrl);
      reject(new Error(`Video metadata read failed for ${file.name}.`));
    };
  });
}

function setPlayerOrientation(slotName, orientation) {
  const cardRef = slotName === "left" ? refs.leftPlayerCard : refs.rightPlayerCard;
  if (cardRef) {
    cardRef.dataset.orientation = orientation || "portrait";
  }
}

function detectVideoOrientation(video) {
  if (!video.videoWidth || !video.videoHeight) {
    return "portrait";
  }
  if (video.videoHeight > video.videoWidth * 1.05) {
    return "portrait";
  }
  if (video.videoWidth > video.videoHeight * 1.05) {
    return "landscape";
  }
  return "square";
}

function formatVideoMeta(video) {
  if (!video.videoWidth || !video.videoHeight) {
    return "portrait";
  }
  const orientation = detectVideoOrientation(video);
  return `${orientation} · ${video.videoWidth}x${video.videoHeight}`;
}

async function waitForVideoMetadata(video) {
  if (video.readyState >= 1 && video.videoWidth > 0 && video.videoHeight > 0) {
    return;
  }

  await new Promise((resolve, reject) => {
    const handleLoaded = () => {
      cleanup();
      resolve();
    };
    const handleError = () => {
      cleanup();
      reject(new Error("Video metadata load failed."));
    };
    const cleanup = () => {
      video.removeEventListener("loadedmetadata", handleLoaded);
      video.removeEventListener("error", handleError);
    };

    video.addEventListener("loadedmetadata", handleLoaded, { once: true });
    video.addEventListener("error", handleError, { once: true });
  });
}

function buildAssetKey(asset) {
  return `${asset.kind}:${asset.variant || "default"}:${asset.object_name}`;
}

function makeCacheRequest(cacheKey) {
  return new Request(`${window.location.origin}/__video_cache__/${encodeURIComponent(cacheKey)}`);
}

function splitCsv(raw) {
  return raw
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
}

function normalizeApiBase(raw) {
  return String(raw || DEFAULT_CONFIG.apiBaseUrl).trim().replace(/\/+$/, "");
}

function sanitizeFilename(name) {
  return String(name || "chunk.mp4").replace(/[^a-zA-Z0-9._-]+/g, "_");
}

function basename(value) {
  return String(value || "")
    .split("/")
    .filter(Boolean)
    .pop() || "";
}

function roundSeconds(value) {
  return Math.round(Number(value || 0) * 1000) / 1000;
}

function formatSeconds(value) {
  if (!Number.isFinite(value) || value < 0) {
    return "00:00.000";
  }

  const whole = Math.floor(value);
  const millis = Math.round((value - whole) * 1000);
  const hours = Math.floor(whole / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const seconds = whole % 60;
  const prefix = hours > 0 ? `${String(hours).padStart(2, "0")}:` : "";
  return `${prefix}${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}.${String(millis).padStart(3, "0")}`;
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes)) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  if (bytes < 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function formatTimestamp(value) {
  if (!value) {
    return "Unknown";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }

  return date.toLocaleString();
}

function setStatus(element, message) {
  element.textContent = message;
}

function logLine(message, level = "info") {
  const stamp = new Date().toLocaleTimeString();
  const line = `[${stamp}] ${level.toUpperCase()} ${message}`;
  refs.activityLog.textContent = refs.activityLog.textContent
    ? `${refs.activityLog.textContent}\n${line}`
    : line;
  refs.activityLog.scrollTop = refs.activityLog.scrollHeight;
}

function readJsonStorage(key, fallback) {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) {
      return fallback;
    }
    return { ...fallback, ...JSON.parse(raw) };
  } catch {
    return fallback;
  }
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

async function safeReadText(response) {
  try {
    return await response.text();
  } catch {
    return "";
  }
}

function toMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

function reportCatalogError(error) {
  const message = toMessage(error);
  setStatus(refs.sessionCatalogStatus, message);
  logLine(message, "error");
}

async function runButtonAction(button, busyLabel, fn) {
  const originalText = button.textContent;
  button.disabled = true;
  button.textContent = busyLabel;
  try {
    await fn();
  } catch (error) {
    const message = toMessage(error);
    setStatus(refs.sessionStatus, message);
    logLine(message, "error");
  } finally {
    button.disabled = false;
    button.textContent = originalText;
  }
}

async function runCatalogButton(button, busyLabel, fn) {
  const originalText = button.textContent;
  button.disabled = true;
  button.textContent = busyLabel;
  try {
    await fn();
  } catch (error) {
    reportCatalogError(error);
  } finally {
    button.disabled = false;
    button.textContent = originalText;
  }
}

async function runAssetButton(button, busyLabel, fn) {
  const originalText = button.textContent;
  button.disabled = true;
  button.textContent = busyLabel;
  try {
    await fn();
  } catch (error) {
    const message = toMessage(error);
    setStatus(refs.sessionStatus, message);
    logLine(message, "error");
  } finally {
    button.disabled = false;
    button.textContent = originalText;
  }
}
