import { useAuthStore } from "../auth/useAuthStore";
import { apiClient } from "./api";

export type SessionArchiveState = "active" | "archived" | "not_registered" | "unavailable";
export interface ArchiveState {
  session_id: string;
  state: SessionArchiveState;
  archived_at?: string | null;
}
export interface ArchiveStatesResponse {
  checked_at: string;
  history_revision: string;
  states: ArchiveState[];
}
export interface LocalSessionSource { ownerUserId: number | null; profileId: number; sessionId: string }
const listeners = new Set<(profileId: number) => void>();
const revisions = new Map<string, string>();
export const ARCHIVE_STATE_TIMEOUT_MS = 5000;

export function subscribeHistoryChanges(listener: (profileId: number) => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
export function notifyHistoryChanged(profileId: number) {
  for (const listener of listeners) listener(profileId);
}
export function ownsLocalSource(source: LocalSessionSource): boolean {
  const auth = useAuthStore.getState();
  return auth.isLoggedIn && !!auth.userId && source.ownerUserId === auth.userId && source.profileId > 0;
}
export function sourceCanInfluenceWorkouts(source: LocalSessionSource, state?: ArchiveState): boolean {
  return ownsLocalSource(source) && state?.session_id === source.sessionId
    && (state.state === "active" || state.state === "not_registered");
}

/** Always a fresh server read. An earlier active response never authorizes offline reuse. */
export async function fetchArchiveStates(profileId: number, sessionIds: string[], signal?: AbortSignal): Promise<ArchiveStatesResponse> {
  const auth = useAuthStore.getState();
  const owner = auth.userId;
  if (!auth.isLoggedIn || !owner || !Number.isSafeInteger(profileId) || profileId <= 0) throw new Error("archive_scope_unavailable");
  const ids = [...new Set(sessionIds)];
  if (ids.length > 100 || ids.some(id => !id || !/^[A-Za-z0-9][A-Za-z0-9_-]{0,180}$/.test(id))) throw new Error("invalid_archive_sources");
  if (!ids.length) return { checked_at: new Date().toISOString(), history_revision: "", states: [] };
  if (signal?.aborted) throw new Error("archive_check_cancelled");
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let abort: (() => void) | undefined;
  let response: ArchiveStatesResponse;
  try {
    const deadline = new Promise<never>((_resolve, reject) => {
      timer = setTimeout(() => { controller.abort(); reject(new Error("archive_state_timeout")); }, ARCHIVE_STATE_TIMEOUT_MS);
      abort = () => { controller.abort(); reject(new Error("archive_check_cancelled")); };
      signal?.addEventListener("abort", abort, { once: true });
    });
    // Race the entire request, including token reads: a stuck dependency must release native AI work.
    response = await Promise.race([apiClient<ArchiveStatesResponse>("/sessions/archive-states", {
      method: "POST", bodyPayload: { profile_id: profileId, session_ids: ids }, signal: controller.signal,
    }), deadline]);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
    if (abort) signal?.removeEventListener("abort", abort);
  }
  if (signal?.aborted || !useAuthStore.getState().isLoggedIn || useAuthStore.getState().userId !== owner) throw new Error("archive_scope_changed");
  const returned = new Set<string>();
  if (!response || typeof response.checked_at !== "string" || typeof response.history_revision !== "string" || !/^\d+$/.test(response.history_revision) || !Array.isArray(response.states)
    || response.states.length !== ids.length || response.states.some(item => {
      if (!ids.includes(item.session_id) || returned.has(item.session_id)
        || !["active", "archived", "not_registered", "unavailable"].includes(item.state)) return true;
      returned.add(item.session_id); return false;
    })) throw new Error("invalid_archive_state_response");
  const key = `${owner}:${profileId}`;
  const previous = revisions.get(key);
  if (previous !== undefined && BigInt(response.history_revision) < BigInt(previous)) throw new Error("stale_archive_state_response");
  revisions.set(key, response.history_revision);
  if (previous !== undefined && previous !== response.history_revision) notifyHistoryChanged(profileId);
  return response;
}
