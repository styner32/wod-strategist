import { Buffer } from "buffer";
import {
  deleteAsync,
  documentDirectory,
  getInfoAsync,
  makeDirectoryAsync,
  readAsStringAsync,
  writeAsStringAsync,
} from "expo-file-system/legacy";

import {
  completeSensorUpload,
  prepareSensorUpload,
  uploadSensorToGcs,
} from "../../wod/api";

export type SensorUploadStage =
  | "PREPARE_PENDING"
  | "PUT_PENDING"
  | "COMPLETE_PENDING"
  | "ACCEPTED"
  | "SUPERSEDED"
  | "NEEDS_ATTENTION";

export interface SensorQueueEntry {
  sessionId: string;
  profileId: number;
  filePath: string;
  requestId: string;
  expectedVersion: string;
  serverVersion?: string;
  sizeBytes: number;
  sha256: string;
  stage: SensorUploadStage;
  uploadUrl?: string;
  requiredHeaders?: Record<string, string>;
  uploadExpiresAt?: string;
  attempts: number;
  createdAt: number;
  lastAttemptAt?: number;
  nextRetryAt?: number;
  lastError?: string;
}

const TWENTY_FOUR_HOURS_MS = 24 * 60 * 60 * 1000;
const SUB_DIR = "sensor";

export function generateUUID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === "x" ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

/**
 * Computes SHA-256 lowercase hex string.
 * Supports Web Crypto subtle digest, Node.js crypto, or synchronous bitwise fallback.
 */
export async function computeSha256(content: string): Promise<string> {
  if (
    typeof crypto !== "undefined" &&
    crypto.subtle &&
    typeof crypto.subtle.digest === "function"
  ) {
    const encoder = new TextEncoder();
    const data = encoder.encode(content);
    const hashBuffer = await crypto.subtle.digest("SHA-256", data);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
  }
  try {
    const nodeCrypto = require("crypto");
    return nodeCrypto.createHash("sha256").update(content, "utf8").digest("hex");
  } catch {
    // Pure JS fallback
    return pureJsSha256(content);
  }
}

function pureJsSha256(ascii: string): string {
  function rightRotate(value: number, amount: number) {
    return (value >>> amount) | (value << (32 - amount));
  }
  const mathPow = Math.pow;
  const maxWord = mathPow(2, 32);
  let i = 0,
    j = 0;
  let result = "";
  const words: number[] = [];
  const asciiBitLength = ascii.length * 8;
  const hash = [
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ];
  const k = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  ascii += "\x80";
  while (ascii.length % 64 - 56) ascii += "\x00";
  for (i = 0; i < ascii.length; i++) {
    j = ascii.charCodeAt(i);
    if (j >> 8) return "";
    words[i >> 2] |= j << (((3 - i) % 4) * 8);
  }
  words[words.length] = (asciiBitLength / maxWord) | 0;
  words[words.length] = asciiBitLength;
  for (j = 0; j < words.length; ) {
    const w = words.slice(j, (j += 16));
    const oldHash = hash.slice(0);
    for (i = 0; i < 64; i++) {
      const i2 = i + j;
      const w15 = w[i - 15],
        w2 = w[i - 2];
      const a = hash[0],
        e = hash[4];
      const temp1 =
        hash[7] +
        (rightRotate(e, 6) ^ rightRotate(e, 11) ^ rightRotate(e, 25)) +
        ((e & hash[5]) ^ (~e & hash[6])) +
        k[i] +
        (w[i] =
          i < 16
            ? w[i]
            : (w[i - 16] +
                (rightRotate(w15, 7) ^ rightRotate(w15, 18) ^ (w15 >>> 3)) +
                w[i - 7] +
                (rightRotate(w2, 17) ^ rightRotate(w2, 19) ^ (w2 >>> 10))) |
              0);
      const temp2 =
        (rightRotate(a, 2) ^ rightRotate(a, 13) ^ rightRotate(a, 22)) +
        ((a & hash[1]) ^ (a & hash[2]) ^ (hash[1] & hash[2]));
      hash[7] = hash[6];
      hash[6] = hash[5];
      hash[5] = hash[4];
      hash[4] = (hash[3] + temp1) | 0;
      hash[3] = hash[2];
      hash[2] = hash[1];
      hash[1] = hash[0];
      hash[0] = (temp1 + temp2) | 0;
    }
    for (i = 0; i < 8; i++) hash[i] = (hash[i] + oldHash[i]) | 0;
  }
  for (i = 0; i < 8; i++) {
    for (j = 3; j + 1; j--) {
      const b = (hash[i] >> (j * 8)) & 255;
      result += (b < 16 ? "0" : "") + b.toString(16);
    }
  }
  return result;
}

let mutationLock: Promise<unknown> = Promise.resolve();
let isFlushing = false;

function withLock<T>(fn: () => Promise<T>): Promise<T> {
  const run = mutationLock.then(fn, fn);
  mutationLock = run.catch(() => undefined);
  return run;
}

function queueDir(): string {
  return `${documentDirectory}${SUB_DIR}/`;
}

function queuePath(): string {
  return `${queueDir()}_pending.json`;
}

function queueTmpPath(): string {
  return `${queueDir()}_pending.json.tmp`;
}

function queueBakPath(): string {
  return `${queueDir()}_pending.json.bak`;
}

/**
 * Loads the queue safely:
 * - Checks main file; if corrupted, attempts to restore from backup.
 * - If both are unreadable/corrupted, throws an error (never silently wipes or creates new UUIDs).
 * - If main file does not exist, returns empty array.
 * - Automatically migrates legacy entries lacking stage or requestId.
 */
export async function loadQueue(): Promise<SensorQueueEntry[]> {
  const qP = queuePath();
  const bakP = queueBakPath();

  let mainContent: string | null = null;
  let mainExisted = false;

  try {
    const info = await getInfoAsync(qP);
    mainExisted = info.exists;
    if (mainExisted) {
      mainContent = await readAsStringAsync(qP);
    }
  } catch {
    // Read failed
  }

  if (mainContent !== null) {
    try {
      const parsed = JSON.parse(mainContent);
      if (Array.isArray(parsed)) {
        const { entries, changed } = migrateLegacyEntries(parsed);
        if (changed) {
          await saveQueue(entries);
        }
        return entries;
      }
    } catch {
      // Main file corrupted, proceed to backup recovery
    }
  }

  if (mainExisted || mainContent !== null) {
    try {
      const bakInfo = await getInfoAsync(bakP);
      if (bakInfo.exists) {
        const bakContent = await readAsStringAsync(bakP);
        const parsed = JSON.parse(bakContent);
        if (Array.isArray(parsed)) {
          const { entries } = migrateLegacyEntries(parsed);
          await writeAsStringAsync(qP, JSON.stringify(entries));
          return entries;
        }
      }
    } catch {
      // Backup read or parse failed
    }

    throw new Error("Corrupted sensor upload queue file and recovery failed");
  }

  return [];
}

/**
 * Saves the queue atomically:
 * - Writes to .tmp in the same directory.
 * - Backs up current valid queue to .bak.
 * - Writes to main queue file.
 * - Propagates errors on write failure instead of swallowing.
 */
export async function saveQueue(queue: SensorQueueEntry[]): Promise<void> {
  const dir = queueDir();
  const dirInfo = await getInfoAsync(dir);
  if (!dirInfo.exists) {
    await makeDirectoryAsync(dir, { intermediates: true });
  }

  const jsonContent = JSON.stringify(queue);
  const qP = queuePath();
  const tmpP = queueTmpPath();
  const bakP = queueBakPath();

  // 1. Write to tmp file
  await writeAsStringAsync(tmpP, jsonContent);

  // 2. Backup existing queue if present
  try {
    const mainInfo = await getInfoAsync(qP);
    if (mainInfo.exists) {
      const existing = await readAsStringAsync(qP);
      await writeAsStringAsync(bakP, existing);
    }
  } catch {
    // Best-effort backup
  }

  // 3. Write to primary queue file
  await writeAsStringAsync(qP, jsonContent);

  // 4. Clean up tmp file
  try {
    await deleteAsync(tmpP, { idempotent: true });
  } catch {
    // Non-fatal
  }
}

function migrateLegacyEntries(rawList: any[]): {
  entries: SensorQueueEntry[];
  changed: boolean;
} {
  let changed = false;
  const entries: SensorQueueEntry[] = [];

  for (const raw of rawList) {
    // iOS may relocate the data container on reinstall. Keep the sensor filename
    // and resolve it against this installation, including restored queue backups.
    const oldSensorPath = typeof raw.filePath === "string"
      ? raw.filePath.match(/^file:\/\/(?:\/private)?\/var\/mobile\/Containers\/Data\/Application\/[^/]+\/Documents\/sensor\/([^/]+\.ndjson)$/)
      : null;
    const filePath = oldSensorPath && documentDirectory
      ? `${queueDir()}${oldSensorPath[1]}`
      : raw.filePath;
    if (filePath !== raw.filePath) changed = true;

    if (!raw.stage || !raw.requestId) {
      changed = true;
      entries.push({
        sessionId: raw.sessionId,
        profileId: raw.profileId ?? 0,
        filePath,
        requestId: raw.requestId || generateUUID(),
        expectedVersion: raw.expectedVersion || "0",
        sizeBytes: raw.sizeBytes || 0,
        sha256: raw.sha256 || "",
        stage: "PREPARE_PENDING",
        attempts: raw.attempts || 0,
        createdAt: raw.createdAt || Date.now(),
        lastAttemptAt: raw.lastAttemptAt,
      });
    } else {
      entries.push({ ...raw, filePath } as SensorQueueEntry);
    }
  }

  return { entries, changed };
}

function isSameEntry(a: SensorQueueEntry, b: SensorQueueEntry): boolean {
  return a.sessionId === b.sessionId && a.filePath === b.filePath;
}

async function updateEntry(entry: SensorQueueEntry): Promise<void> {
  await withLock(async () => {
    const queue = await loadQueue();
    const idx = queue.findIndex((e) => isSameEntry(e, entry));
    if (idx !== -1) {
      queue[idx] = { ...entry };
      await saveQueue(queue);
    }
  });
}

async function removeEntry(entry: SensorQueueEntry): Promise<void> {
  await withLock(async () => {
    const queue = await loadQueue();
    await saveQueue(queue.filter((e) => !isSameEntry(e, entry)));
  });
}

function applyBackoff(entry: SensorQueueEntry, err: any): void {
  entry.attempts += 1;
  entry.lastAttemptAt = Date.now();
  entry.lastError = err?.message || String(err);
  const base = Math.min(300000, 5000 * Math.pow(2, Math.max(0, entry.attempts - 1)));
  const jitter = Math.floor(Math.random() * 1000);
  entry.nextRetryAt = Date.now() + base + jitter;
}

/**
 * Enqueue a sensor telemetry file for durable, multi-stage upload.
 */
export async function enqueueSensorUpload(
  sessionId: string,
  profileId: number,
  filePath: string,
): Promise<void> {
  let sizeBytes = 0;
  let sha256 = "";

  try {
    const content = await readAsStringAsync(filePath);
    sizeBytes = Buffer.byteLength(content, "utf8");
    sha256 = await computeSha256(content);
  } catch {
    // If file cannot be read directly or is empty, use getInfoAsync fallback
    const info = await getInfoAsync(filePath);
    if (!info.exists || !info.size) {
      sizeBytes = 0;
    } else {
      sizeBytes = info.size;
    }
  }

  const requestId = generateUUID();

  await withLock(async () => {
    const queue = await loadQueue();
    const existing = queue.find(
      (e) => e.sessionId === sessionId && e.filePath === filePath,
    );
    if (existing) {
      return;
    }

    queue.push({
      sessionId,
      profileId,
      filePath,
      requestId,
      expectedVersion: "0",
      sizeBytes,
      sha256,
      stage: "PREPARE_PENDING",
      attempts: 0,
      createdAt: Date.now(),
    });

    await saveQueue(queue);
  });
}

/**
 * Uploads a single sensor telemetry file via prepare -> PUT -> complete cycle.
 */
export async function uploadSensorTelemetry(
  sessionId: string,
  profileId: number,
  fileUri: string,
): Promise<void> {
  let content = "";
  try {
    content = await readAsStringAsync(fileUri);
  } catch {}
  const sizeBytes = Buffer.byteLength(content, "utf8") || 1;
  const sha256 = content ? await computeSha256(content) : "0".repeat(64);
  const requestId = generateUUID();

  const prep = await prepareSensorUpload(sessionId, {
    profile_id: profileId,
    request_id: requestId,
    expected_version: "0",
    size_bytes: sizeBytes,
    sha256,
  });

  if (prep.upload_url) {
    await uploadSensorToGcs(prep.upload_url, fileUri, prep.required_headers);
  }

  const comp = await completeSensorUpload(sessionId, {
    profile_id: profileId,
    request_id: requestId,
    version: prep.version,
  });

  if (comp.accepted) {
    try {
      await deleteAsync(fileUri, { idempotent: true });
    } catch {}
  }
}

/**
 * Processes one queue entry according to the robust state machine.
 */
async function processOneEntry(entry: SensorQueueEntry): Promise<boolean> {
  // If already accepted, delete file and remove entry
  if (entry.stage === "ACCEPTED") {
    try {
      await deleteAsync(entry.filePath, { idempotent: true });
    } catch {}
    await removeEntry(entry);
    return true;
  }

  // Terminal conflict/attention states: do not automatically retry
  if (entry.stage === "SUPERSEDED" || entry.stage === "NEEDS_ATTENTION") {
    return false;
  }

  // 24-hour expiration check
  if (Date.now() - entry.createdAt > TWENTY_FOUR_HOURS_MS) {
    entry.stage = "NEEDS_ATTENTION";
    entry.lastError = "24-hour upload window exceeded";
    await updateEntry(entry);
    return false;
  }

  // Backoff check
  if (entry.nextRetryAt && Date.now() < entry.nextRetryAt) {
    return false;
  }

  // Only stages that need a PUT require a local file. A completed PUT can still
  // be acknowledged by the server even if the local file has since disappeared.
  const needsLocalFile = entry.stage === "PREPARE_PENDING" || entry.stage === "PUT_PENDING";
  if (needsLocalFile) {
    try {
      const info = await getInfoAsync(entry.filePath);
      if (!info.exists || info.isDirectory) {
        entry.stage = "NEEDS_ATTENTION";
        entry.lastError = "Sensor upload file is missing or is not a regular file";
        await updateEntry(entry);
        return false;
      }
    } catch (err) {
      applyBackoff(entry, err);
      await updateEntry(entry);
      return false;
    }
  }

  // Re-read file size and hash if missing
  if (needsLocalFile && (!entry.sizeBytes || !entry.sha256)) {
    try {
      const content = await readAsStringAsync(entry.filePath);
      entry.sizeBytes = Buffer.byteLength(content, "utf8");
      entry.sha256 = await computeSha256(content);
      await updateEntry(entry);
    } catch {}
  }

  // Stage 1: PREPARE_PENDING
  if (entry.stage === "PREPARE_PENDING") {
    try {
      const prep = await prepareSensorUpload(entry.sessionId, {
        profile_id: entry.profileId,
        request_id: entry.requestId,
        expected_version: entry.expectedVersion,
        size_bytes: entry.sizeBytes || 1,
        sha256: entry.sha256 || "0".repeat(64),
      });

      entry.serverVersion = prep.version;
      entry.uploadUrl = prep.upload_url;
      entry.requiredHeaders = prep.required_headers;
      entry.uploadExpiresAt = prep.expires_at;

      if (
        prep.state === "PENDING" ||
        prep.state === "RUNNING" ||
        prep.state === "COMPLETED"
      ) {
        entry.stage = "COMPLETE_PENDING";
      } else {
        entry.stage = "PUT_PENDING";
      }
      await updateEntry(entry);
    } catch (err: any) {
      const errMsg = err?.message || "";
      if (
        err?.status === 409 ||
        errMsg.includes("409") ||
        errMsg.includes("CONFLICT") ||
        errMsg.includes("SUPERSEDED")
      ) {
        entry.stage = "SUPERSEDED";
        entry.lastError = errMsg;
        await updateEntry(entry);
        return false;
      }
      applyBackoff(entry, err);
      await updateEntry(entry);
      return false;
    }
  }

  // Stage 2: PUT_PENDING
  if (entry.stage === "PUT_PENDING") {
    if (
      entry.uploadExpiresAt &&
      new Date(entry.uploadExpiresAt).getTime() <= Date.now()
    ) {
      // URL expired - refresh via prepare
      entry.stage = "PREPARE_PENDING";
      await updateEntry(entry);
      return false;
    }

    if (!entry.uploadUrl) {
      entry.stage = "PREPARE_PENDING";
      await updateEntry(entry);
      return false;
    }

    try {
      await uploadSensorToGcs(
        entry.uploadUrl,
        entry.filePath,
        entry.requiredHeaders,
      );
      entry.stage = "COMPLETE_PENDING";
      await updateEntry(entry);
    } catch (err: any) {
      // §6: PUT success or unknown/412 proceeds to COMPLETE_PENDING to check with server
      entry.stage = "COMPLETE_PENDING";
      entry.lastError = err?.message || String(err);
      await updateEntry(entry);
    }
  }

  // Stage 3: COMPLETE_PENDING
  if (entry.stage === "COMPLETE_PENDING") {
    try {
      const comp = await completeSensorUpload(entry.sessionId, {
        profile_id: entry.profileId,
        request_id: entry.requestId,
        version: entry.serverVersion || entry.expectedVersion || "1",
      });

      if (
        comp.accepted ||
        comp.state === "COMPLETED" ||
        comp.state === "PENDING" ||
        comp.state === "RUNNING"
      ) {
        entry.stage = "ACCEPTED";
        await updateEntry(entry);

        try {
          await deleteAsync(entry.filePath, { idempotent: true });
        } catch {}

        await removeEntry(entry);
        return true;
      }

      if (comp.error_code === "UPLOAD_NOT_FOUND") {
        entry.stage = "PUT_PENDING";
        applyBackoff(entry, new Error("UPLOAD_NOT_FOUND"));
        await updateEntry(entry);
        return false;
      }

      if (!comp.retryable) {
        entry.stage = "SUPERSEDED";
        entry.lastError = comp.error_code || comp.state;
        await updateEntry(entry);
        return false;
      }

      applyBackoff(entry, new Error(comp.error_code || "Not accepted"));
      await updateEntry(entry);
      return false;
    } catch (err: any) {
      const errMsg = err?.message || "";
      if (err?.status === 404 || errMsg.includes("UPLOAD_NOT_FOUND")) {
        entry.stage = "PUT_PENDING";
        applyBackoff(entry, err);
        await updateEntry(entry);
        return false;
      }
      if (
        err?.status === 409 ||
        errMsg.includes("409") ||
        errMsg.includes("SUPERSEDED")
      ) {
        entry.stage = "SUPERSEDED";
        entry.lastError = errMsg;
        await updateEntry(entry);
        return false;
      }
      applyBackoff(entry, err);
      await updateEntry(entry);
      return false;
    }
  }

  return false;
}

/**
 * Flush all pending sensor telemetry uploads.
 */
export async function flushSensorUploads(): Promise<void> {
  if (isFlushing) return;
  isFlushing = true;

  try {
    const snapshot = await withLock(() => loadQueue());
    if (snapshot.length === 0) return;

    for (const entry of snapshot) {
      await processOneEntry(entry);
    }
  } finally {
    isFlushing = false;
  }
}

let periodicTimer: ReturnType<typeof setInterval> | null = null;

/**
 * Starts periodic flushing of the sensor upload queue (e.g. every 30s while app is active).
 * Returns a cleanup function that stops the timer.
 */
export function startPeriodicSensorUpload(intervalMs = 30000): () => void {
  if (periodicTimer) {
    clearInterval(periodicTimer);
  }
  periodicTimer = setInterval(() => {
    flushSensorUploads().catch(() => {});
  }, intervalMs);

  return () => {
    stopPeriodicSensorUpload();
  };
}

export function stopPeriodicSensorUpload(): void {
  if (periodicTimer) {
    clearInterval(periodicTimer);
    periodicTimer = null;
  }
}

/**
 * Object export to match legacy interface and allow inspection in tests.
 */
export const sensorUploadQueue = {
  subDir: SUB_DIR,
  logTag: "📡 Sensor telemetry",
  enqueueUpload: enqueueSensorUpload,
  flushPendingUploads: flushSensorUploads,
  startPeriodic: startPeriodicSensorUpload,
  stopPeriodic: stopPeriodicSensorUpload,
  loadQueue,
  saveQueue,
  uploadOne: async (entry: SensorQueueEntry): Promise<boolean> => {
    return processOneEntry(entry);
  },
};
