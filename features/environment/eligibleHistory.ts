import { useAuthStore } from "../auth/useAuthStore";
import { fetchArchiveStates, sourceCanInfluenceWorkouts } from "../wod/archiveStates";
import { listSessions, readRecords } from "./store";
import type { EnvironmentRecord } from "./types";

export interface EligibleEnvironmentSnapshot {
  ownerUserId: number;
  profileId: number;
  sourceSessionIds: string[];
  historyRevision: string;
  records: EnvironmentRecord[];
}

/** An aggregate is a snapshot of one revision, including the final evidence reads. */
export async function assertEnvironmentHistoryCurrent(snapshot: EligibleEnvironmentSnapshot, additionalIds: string[] = [], signal?: AbortSignal): Promise<void> {
  const auth = useAuthStore.getState();
  if (!auth.isLoggedIn || auth.userId !== snapshot.ownerUserId) throw new Error("archive_scope_changed");
  const ids = [...new Set([...snapshot.sourceSessionIds, ...additionalIds])];
  if (!ids.length) return;
  const response = await fetchArchiveStates(snapshot.profileId, ids, signal);
  if (snapshot.historyRevision && response.history_revision !== snapshot.historyRevision) throw new Error("history_changed_during_archive_check");
  if (ids.some(id => !sourceCanInfluenceWorkouts({ ownerUserId: snapshot.ownerUserId, profileId: snapshot.profileId, sessionId: id }, response.states.find(state => state.session_id === id)))) throw new Error("archived_source");
}

/** Archived sessions do not consume the thirty-session observation window. */
export async function readEligibleEnvironmentSnapshot(profileId: number, signal?: AbortSignal): Promise<EligibleEnvironmentSnapshot> {
  const owner = useAuthStore.getState().userId;
  if (!owner || !useAuthStore.getState().isLoggedIn) throw new Error("archive_scope_unavailable");
  const sessions = (await listSessions(profileId)).filter(s => !s.deleting && s.ownerUserId === owner && s.profileId === profileId);
  const selected = new Set<string>();
  const records: EnvironmentRecord[] = [];
  const selectedSessions: typeof sessions = [];
  let historyRevision = "";
  for (let offset = 0; offset < sessions.length && selected.size < 30; offset += 100) {
    const batch = sessions.slice(offset, offset + 100);
    const response = await fetchArchiveStates(profileId, batch.map(s => s.sessionId), signal);
    if (historyRevision && historyRevision !== response.history_revision) throw new Error("history_changed_during_archive_check");
    historyRevision = response.history_revision;
    const states = new Map(response.states.map(state => [state.session_id, state]));
    for (const session of batch) {
      if (!sourceCanInfluenceWorkouts(session, states.get(session.sessionId))) continue;
      if (!selected.has(session.sessionId) && selected.size >= 30) break;
      selected.add(session.sessionId);
      selectedSessions.push(session);
    }
  }
  for (const session of selectedSessions) records.push(...await readRecords(session));
  if (signal?.aborted || useAuthStore.getState().userId !== owner || !useAuthStore.getState().isLoggedIn) throw new Error("archive_scope_changed");
  const snapshot = { ownerUserId: owner, profileId, sourceSessionIds: [...selected], historyRevision, records };
  await assertEnvironmentHistoryCurrent(snapshot, [], signal);
  return snapshot;
}

export async function readEligibleEnvironmentHistory(profileId: number, signal?: AbortSignal): Promise<EnvironmentRecord[]> {
  return (await readEligibleEnvironmentSnapshot(profileId, signal)).records;
}
