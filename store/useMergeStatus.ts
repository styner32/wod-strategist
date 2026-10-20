import { create } from "zustand";
import type { WorkoutType } from "@/features/wod/workoutType";

/**
 * Lightweight global store to track pending server-side merges and
 * sequential re-merge queue state across refreshes.
 */

export const REQUESTED_REMERGES_STORAGE_KEY = "wod_requested_remerges_v1";
export const REMERGE_REQUEST_TTL_MS = 60 * 60 * 1000; // 1 hour deduplication window
export const ACTIVE_REMERGE_MAX_WAIT_MS = 5 * 60 * 1000; // Advance queue if active session takes >5m

export interface PendingMerge {
  sessionId: string;
  startedAt: number;
  /**
   * Set once the server has listed this session as pending. A fresh recording
   * has no server row until its chunks upload and the merge is requested, so
   * syncPendingSessions prunes only entries the server has already reported.
   */
  serverSeen?: boolean;
}

export interface RemergeQueueItem {
  sessionId: string;
  profileId: number;
  workoutType: WorkoutType;
  enqueuedAt: number;
}

export type RemergeExecutor = (item: RemergeQueueItem) => Promise<void>;

interface PersistedRemergeState {
  requestedRemerges: Record<string, number>;
  remergeQueue: RemergeQueueItem[];
  activeRemergeSessionId: string | null;
  activeStartedAt: number | null;
}

function getStorage(): {
  getItem: (key: string) => Promise<string | null>;
  setItem: (key: string, value: string) => Promise<void>;
  removeItem: (key: string) => Promise<void>;
} | null {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const mod = require("@react-native-async-storage/async-storage");
    return mod?.default ?? mod ?? null;
  } catch {
    return null;
  }
}

let inFlightDispatch = false;

interface MergeStatusState {
  pending: PendingMerge[];
  requestedRemerges: Record<string, number>;
  remergeQueue: RemergeQueueItem[];
  activeRemergeSessionId: string | null;
  activeStartedAt: number | null;
  hydrated: boolean;
  addPending: (sessionId: string, serverSeen?: boolean) => void;
  removePending: (sessionId: string) => void;
  hydrateRemergeState: () => Promise<void>;
  isRemergeRequested: (sessionId: string, now?: number) => boolean;
  getRemergeQueuePosition: (sessionId: string) => number;
  enqueueRemerge: (
    item: Omit<RemergeQueueItem, "enqueuedAt">,
    executor: RemergeExecutor,
  ) => Promise<"started" | "queued" | "duplicate">;
  syncPendingSessions: (
    pendingSessionIds: string[],
    executor?: RemergeExecutor,
    now?: number,
  ) => Promise<void>;
  resetRemergeState: () => void;
}

async function persistRemergeState(state: PersistedRemergeState): Promise<void> {
  try {
    const storage = getStorage();
    if (!storage) return;
    await storage.setItem(REQUESTED_REMERGES_STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Ignore storage errors in non-native/test environments
  }
}

function pruneExpiredRequests(
  requested: Record<string, number>,
  now: number,
): Record<string, number> {
  const next: Record<string, number> = {};
  for (const [sid, ts] of Object.entries(requested)) {
    if (now - ts < REMERGE_REQUEST_TTL_MS) {
      next[sid] = ts;
    }
  }
  return next;
}

export const useMergeStatus = create<MergeStatusState>((set, get) => ({
  pending: [],
  requestedRemerges: {},
  remergeQueue: [],
  activeRemergeSessionId: null,
  activeStartedAt: null,
  hydrated: false,

  addPending: (sessionId, serverSeen = false) =>
    set((state) => {
      if (state.pending.some((p) => p.sessionId === sessionId)) {
        return state;
      }
      return {
        pending: [...state.pending, { sessionId, startedAt: Date.now(), serverSeen }],
      };
    }),

  removePending: (sessionId) =>
    set((state) => ({
      pending: state.pending.filter((p) => p.sessionId !== sessionId),
    })),

  hydrateRemergeState: async () => {
    if (get().hydrated) return;
    try {
      const storage = getStorage();
      if (!storage) {
        set({ hydrated: true });
        return;
      }
      const raw = await storage.getItem(REQUESTED_REMERGES_STORAGE_KEY);
      if (!raw) {
        set({ hydrated: true });
        return;
      }
      const parsed = JSON.parse(raw) as Partial<PersistedRemergeState>;
      const now = Date.now();
      const requestedRemerges = pruneExpiredRequests(
        parsed.requestedRemerges ?? {},
        now,
      );
      const remergeQueue = Array.isArray(parsed.remergeQueue)
        ? parsed.remergeQueue.filter(
            (q) => q && typeof q.sessionId === "string" && requestedRemerges[q.sessionId],
          )
        : [];
      const activeRemergeSessionId =
        typeof parsed.activeRemergeSessionId === "string" &&
        requestedRemerges[parsed.activeRemergeSessionId]
          ? parsed.activeRemergeSessionId
          : null;
      const activeStartedAt =
        activeRemergeSessionId && typeof parsed.activeStartedAt === "number"
          ? parsed.activeStartedAt
          : null;

      set((state) => ({
        requestedRemerges: { ...requestedRemerges, ...state.requestedRemerges },
        remergeQueue:
          state.remergeQueue.length > 0 ? state.remergeQueue : remergeQueue,
        activeRemergeSessionId:
          state.activeRemergeSessionId ?? activeRemergeSessionId,
        activeStartedAt: state.activeStartedAt ?? activeStartedAt,
        hydrated: true,
      }));
    } catch {
      set({ hydrated: true });
    }
  },

  isRemergeRequested: (sessionId, now = Date.now()) => {
    const state = get();
    const ts = state.requestedRemerges[sessionId];
    if (typeof ts === "number" && now - ts < REMERGE_REQUEST_TTL_MS) {
      return true;
    }
    if (state.activeRemergeSessionId === sessionId) return true;
    return state.remergeQueue.some((q) => q.sessionId === sessionId);
  },

  getRemergeQueuePosition: (sessionId) => {
    const state = get();
    if (state.activeRemergeSessionId === sessionId) return 0;
    const idx = state.remergeQueue.findIndex((q) => q.sessionId === sessionId);
    if (idx >= 0) return idx + 1;
    if (state.isRemergeRequested(sessionId)) return 0;
    return -1;
  },

  enqueueRemerge: async (item, executor) => {
    const now = Date.now();
    const state = get();
    if (state.isRemergeRequested(item.sessionId, now)) {
      return "duplicate";
    }

    const queueItem: RemergeQueueItem = {
      ...item,
      enqueuedAt: now,
    };

    // If another session is currently active (or being dispatched), queue this session
    if (state.activeRemergeSessionId !== null || inFlightDispatch) {
      const nextRequested = {
        ...pruneExpiredRequests(state.requestedRemerges, now),
        [item.sessionId]: now,
      };
      const nextQueue = [...state.remergeQueue, queueItem];
      set({
        requestedRemerges: nextRequested,
        remergeQueue: nextQueue,
      });
      await persistRemergeState({
        requestedRemerges: nextRequested,
        remergeQueue: nextQueue,
        activeRemergeSessionId: get().activeRemergeSessionId,
        activeStartedAt: get().activeStartedAt,
      });
      return "queued";
    }

    // Otherwise start immediately as the active re-merge session
    inFlightDispatch = true;
    const nextRequested = {
      ...pruneExpiredRequests(state.requestedRemerges, now),
      [item.sessionId]: now,
    };
    set({
      requestedRemerges: nextRequested,
      activeRemergeSessionId: item.sessionId,
      activeStartedAt: now,
    });
    await persistRemergeState({
      requestedRemerges: nextRequested,
      remergeQueue: get().remergeQueue,
      activeRemergeSessionId: item.sessionId,
      activeStartedAt: now,
    });

    try {
      await executor(queueItem);
      get().addPending(item.sessionId, true);
      return "started";
    } catch (err) {
      // Roll back so user can retry on network/server failure
      const current = get();
      const rolledBack = { ...current.requestedRemerges };
      delete rolledBack[item.sessionId];
      const nextActive =
        current.activeRemergeSessionId === item.sessionId
          ? null
          : current.activeRemergeSessionId;
      set({
        requestedRemerges: rolledBack,
        activeRemergeSessionId: nextActive,
        activeStartedAt: nextActive ? current.activeStartedAt : null,
      });
      await persistRemergeState({
        requestedRemerges: rolledBack,
        remergeQueue: current.remergeQueue,
        activeRemergeSessionId: nextActive,
        activeStartedAt: nextActive ? current.activeStartedAt : null,
      });
      throw err;
    } finally {
      inFlightDispatch = false;
    }
  },

  syncPendingSessions: async (pendingSessionIds, executor, now = Date.now()) => {
    const pendingSet = new Set(pendingSessionIds);
    const state = get();
    const cleanedRequested = pruneExpiredRequests(state.requestedRemerges, now);
    let changed = false;

    for (const sid of Object.keys(cleanedRequested)) {
      if (!pendingSet.has(sid)) {
        delete cleanedRequested[sid];
        changed = true;
      }
    }

    // An entry the server has not listed yet (a recording still uploading) is
    // kept; its owner removes it.
    const nextPending: PendingMerge[] = [];
    for (const p of state.pending) {
      if (pendingSet.has(p.sessionId)) {
        if (!p.serverSeen) changed = true;
        nextPending.push(p.serverSeen ? p : { ...p, serverSeen: true });
      } else if (!p.serverSeen) {
        nextPending.push(p);
      } else {
        changed = true;
      }
    }

    const nextQueue = state.remergeQueue.filter((q) => pendingSet.has(q.sessionId));
    if (nextQueue.length !== state.remergeQueue.length) {
      changed = true;
    }

    let nextActive = state.activeRemergeSessionId;
    let nextActiveStartedAt = state.activeStartedAt;

    const activeFinished =
      nextActive !== null && !pendingSet.has(nextActive);
    const activeTimedOut =
      nextActive !== null &&
      nextActiveStartedAt !== null &&
      now - nextActiveStartedAt >= ACTIVE_REMERGE_MAX_WAIT_MS;

    if (activeFinished || activeTimedOut) {
      nextActive = null;
      nextActiveStartedAt = null;
      changed = true;
    }

    if (changed) {
      set({
        pending: nextPending,
        requestedRemerges: cleanedRequested,
        remergeQueue: nextQueue,
        activeRemergeSessionId: nextActive,
        activeStartedAt: nextActiveStartedAt,
      });
      await persistRemergeState({
        requestedRemerges: cleanedRequested,
        remergeQueue: nextQueue,
        activeRemergeSessionId: nextActive,
        activeStartedAt: nextActiveStartedAt,
      });
    }

    // If no active re-merge is running and there are queued items, dispatch the next one
    if (
      !inFlightDispatch &&
      get().activeRemergeSessionId === null &&
      get().remergeQueue.length > 0 &&
      executor
    ) {
      const [nextItem, ...restQueue] = get().remergeQueue;
      inFlightDispatch = true;
      const startedAt = Date.now();
      set({
        remergeQueue: restQueue,
        activeRemergeSessionId: nextItem.sessionId,
        activeStartedAt: startedAt,
      });
      await persistRemergeState({
        requestedRemerges: get().requestedRemerges,
        remergeQueue: restQueue,
        activeRemergeSessionId: nextItem.sessionId,
        activeStartedAt: startedAt,
      });
      try {
        await executor(nextItem);
        get().addPending(nextItem.sessionId, true);
      } catch {
        const current = get();
        const rolledBack = { ...current.requestedRemerges };
        delete rolledBack[nextItem.sessionId];
        set({
          requestedRemerges: rolledBack,
          activeRemergeSessionId: null,
          activeStartedAt: null,
        });
        await persistRemergeState({
          requestedRemerges: rolledBack,
          remergeQueue: current.remergeQueue,
          activeRemergeSessionId: null,
          activeStartedAt: null,
        });
      } finally {
        inFlightDispatch = false;
      }
    }
  },

  resetRemergeState: () => {
    inFlightDispatch = false;
    set({
      pending: [],
      requestedRemerges: {},
      remergeQueue: [],
      activeRemergeSessionId: null,
      activeStartedAt: null,
      hydrated: false,
    });
  },
}));

/** Returns true if any merge is in-flight */
export const useHasPendingMerge = () =>
  useMergeStatus((s) => s.pending.length > 0);
