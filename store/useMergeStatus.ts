import { create } from "zustand";

/**
 * Lightweight global store to track pending server-side merges.
 * Used to show a "Merge in progress" banner on the History page
 * after the user stops recording and navigates away.
 *
 * The merge API call itself is fire-and-forget (just enqueues a task),
 * but we want the user to know it's happening.
 */

interface PendingMerge {
  sessionId: string;
  startedAt: number;
}

interface MergeStatusState {
  pending: PendingMerge[];
  addPending: (sessionId: string) => void;
  removePending: (sessionId: string) => void;
}

export const useMergeStatus = create<MergeStatusState>((set) => ({
  pending: [],

  addPending: (sessionId) =>
    set((state) => ({
      pending: [...state.pending, { sessionId, startedAt: Date.now() }],
    })),

  removePending: (sessionId) =>
    set((state) => ({
      pending: state.pending.filter((p) => p.sessionId !== sessionId),
    })),
}));

/** Returns true if any merge is in-flight */
export const useHasPendingMerge = () =>
  useMergeStatus((s) => s.pending.length > 0);
