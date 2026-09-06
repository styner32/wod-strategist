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

export const MAX_ATTEMPTS = 5;

export interface UploadQueueOptions {
  subDir: string;
  logTag?: string;
  uploadFn: (pending: PendingUpload) => Promise<void>;
}

export function createUploadQueue(options: UploadQueueOptions) {
  const { subDir, uploadFn, logTag = '📊' } = options;

  function queuePath(): string {
    return `${documentDirectory}${subDir}/_pending.json`;
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
      const dir = `${documentDirectory}${subDir}/`;
      const info = await getInfoAsync(dir);
      if (!info.exists) {
        await makeDirectoryAsync(dir, { intermediates: true });
      }
      await writeAsStringAsync(queuePath(), JSON.stringify(queue));
    } catch (e) {
      console.warn(`${logTag} Failed to persist queue:`, e);
    }
  }

  async function enqueueUpload(
    sessionId: string,
    filePath: string,
    profileId?: number,
  ): Promise<void> {
    const queue = await loadQueue();
    queue.push({ sessionId, filePath, attempts: 0, profileId });
    await saveQueue(queue);
  }

  async function uploadOne(pending: PendingUpload): Promise<boolean> {
    try {
      await uploadFn(pending);

      // Success — delete the local file
      try {
        await deleteAsync(pending.filePath, { idempotent: true });
      } catch {
        // Non-fatal — file already gone or inaccessible
      }

      console.log(`${logTag} uploaded for ${pending.sessionId}`);
      return true;
    } catch (e) {
      pending.attempts += 1;
      pending.lastAttemptAt = Date.now();
      console.warn(
        `${logTag} upload failed for ${pending.sessionId} (attempt ${pending.attempts}):`,
        e,
      );
      return false;
    }
  }

  async function flushPendingUploads(): Promise<void> {
    const queue = await loadQueue();
    if (queue.length === 0) return;

    const remaining: PendingUpload[] = [];

    for (const entry of queue) {
      if (entry.attempts >= MAX_ATTEMPTS) {
        console.warn(
          `${logTag} Giving up on upload for ${entry.sessionId} after ${entry.attempts} attempts`,
        );
        continue;
      }

      const ok = await uploadOne(entry);
      if (!ok) {
        remaining.push(entry);
      }
    }

    await saveQueue(remaining);
  }

  return {
    enqueueUpload,
    flushPendingUploads,
    loadQueue,
    saveQueue,
    uploadOne,
  };
}

// ---------------------------------------------------------------------------
// Default Debug Telemetry Queue (maintains backward compatibility)
// ---------------------------------------------------------------------------

const debugQueue = createUploadQueue({
  subDir: 'debug',
  logTag: '📊 Telemetry',
  uploadFn: async (pending) => {
    const raw = await readAsStringAsync(pending.filePath);
    const session: TelemetrySession = JSON.parse(raw);
    await uploadDebugTelemetry(session);
  },
});

export const enqueueUpload = debugQueue.enqueueUpload;
export const flushPendingUploads = debugQueue.flushPendingUploads;
