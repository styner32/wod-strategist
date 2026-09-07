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

  // The queue file is a whole-file read-modify-write. Uploads take seconds, so
  // an enqueue from a finishing recording can land in the middle of a flush;
  // without serialization the flush's stale snapshot overwrites the new entry
  // and that upload is lost for good. Every mutation runs under this lock,
  // and the lock is never held across a network call.
  let mutationLock: Promise<unknown> = Promise.resolve();
  let isFlushing = false;

  function withLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = mutationLock.then(fn, fn);
    mutationLock = run.catch(() => undefined);
    return run;
  }

  function isSameEntry(a: PendingUpload, b: PendingUpload): boolean {
    return a.sessionId === b.sessionId && a.filePath === b.filePath;
  }

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
    await withLock(async () => {
      const queue = await loadQueue();
      queue.push({ sessionId, filePath, attempts: 0, profileId });
      await saveQueue(queue);
    });
  }

  /** Removes one entry from the queue as it stands right now. */
  async function removeEntry(entry: PendingUpload): Promise<void> {
    await withLock(async () => {
      const queue = await loadQueue();
      await saveQueue(queue.filter((e) => !isSameEntry(e, entry)));
    });
  }

  /** Records a failed attempt against the queue as it stands right now. */
  async function recordFailure(entry: PendingUpload): Promise<void> {
    await withLock(async () => {
      const queue = await loadQueue();
      const match = queue.find((e) => isSameEntry(e, entry));
      if (match) {
        match.attempts += 1;
        match.lastAttemptAt = Date.now();
      }
      await saveQueue(queue);
    });
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
      console.warn(
        `${logTag} upload failed for ${pending.sessionId} (attempt ${pending.attempts + 1}):`,
        e,
      );
      return false;
    }
  }

  async function flushPendingUploads(): Promise<void> {
    // A second concurrent flush would upload the same entries twice.
    if (isFlushing) return;
    isFlushing = true;

    try {
      const snapshot = await withLock(() => loadQueue());
      if (snapshot.length === 0) return;

      for (const entry of snapshot) {
        if (entry.attempts >= MAX_ATTEMPTS) {
          console.warn(
            `${logTag} Giving up on upload for ${entry.sessionId} after ${entry.attempts} attempts`,
          );
          await removeEntry(entry);
          continue;
        }

        const ok = await uploadOne(entry);
        // Re-read the queue for each outcome: entries enqueued while this
        // upload was in flight must survive.
        if (ok) {
          await removeEntry(entry);
        } else {
          await recordFailure(entry);
        }
      }
    } finally {
      isFlushing = false;
    }
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
