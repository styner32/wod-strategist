/**
 * Telemetry upload queue — persists pending uploads to disk so they survive
 * app restarts. Retries are triggered on next recording stop and on app start.
 *
 * Max 5 attempts per entry. No exponential backoff in v1 — retry cadence is
 * naturally spread out (once per recording session or app launch).
 */
import {
  documentDirectory,
  readAsStringAsync,
  writeAsStringAsync,
  deleteAsync,
  getInfoAsync,
  makeDirectoryAsync,
} from 'expo-file-system/legacy';

import { uploadDebugTelemetry } from '../wod/api';
import type { PendingUpload, TelemetrySession } from './types';

// ---------------------------------------------------------------------------
// Queue persistence
// ---------------------------------------------------------------------------

const MAX_ATTEMPTS = 5;

function queuePath(): string {
  return `${documentDirectory}debug/_pending.json`;
}

async function loadQueue(): Promise<PendingUpload[]> {
  try {
    const raw = await readAsStringAsync(queuePath());
    return JSON.parse(raw) as PendingUpload[];
  } catch {
    // File missing, corrupt, or unparseable — start fresh
    return [];
  }
}

async function saveQueue(queue: PendingUpload[]): Promise<void> {
  try {
    // Ensure debug dir exists
    const dir = `${documentDirectory}debug/`;
    const info = await getInfoAsync(dir);
    if (!info.exists) {
      await makeDirectoryAsync(dir, { intermediates: true });
    }
    await writeAsStringAsync(queuePath(), JSON.stringify(queue));
  } catch (e) {
    console.warn('📊 Failed to persist telemetry queue:', e);
  }
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Add a telemetry file to the retry queue and persist.
 */
export async function enqueueUpload(
  sessionId: string,
  filePath: string,
): Promise<void> {
  const queue = await loadQueue();
  queue.push({ sessionId, filePath, attempts: 0 });
  await saveQueue(queue);
}

/**
 * Attempt to upload a single pending entry.
 * On success: deletes the local JSON file, returns true.
 * On failure: increments attempts, returns false.
 */
async function uploadOne(pending: PendingUpload): Promise<boolean> {
  try {
    const raw = await readAsStringAsync(pending.filePath);
    const session: TelemetrySession = JSON.parse(raw);

    await uploadDebugTelemetry(session);

    // Success — delete the local file
    try {
      await deleteAsync(pending.filePath, { idempotent: true });
    } catch {
      // Non-fatal — file already gone or inaccessible
    }

    console.log(`📊 Telemetry uploaded for ${pending.sessionId}`);
    return true;
  } catch (e) {
    pending.attempts += 1;
    pending.lastAttemptAt = Date.now();
    console.warn(
      `📊 Telemetry upload failed for ${pending.sessionId} (attempt ${pending.attempts}):`,
      e,
    );
    return false;
  }
}

/**
 * Best-effort flush of all pending uploads. Caller should fire-and-forget:
 *
 *   flushPendingUploads().catch(() => {});
 *
 * - Skips entries with attempts >= MAX_ATTEMPTS
 * - Processes sequentially (network-friendly)
 * - Persists updated queue after each attempt
 */
export async function flushPendingUploads(): Promise<void> {
  const queue = await loadQueue();
  if (queue.length === 0) return;

  const remaining: PendingUpload[] = [];

  for (const entry of queue) {
    if (entry.attempts >= MAX_ATTEMPTS) {
      // Give up on this one — keep file for manual inspection, drop from queue
      console.warn(
        `📊 Giving up on telemetry upload for ${entry.sessionId} after ${entry.attempts} attempts`,
      );
      continue;
    }

    const ok = await uploadOne(entry);
    if (!ok) {
      remaining.push(entry);
    }
    // On success: entry is NOT pushed to remaining → removed from queue
  }

  await saveQueue(remaining);
}
