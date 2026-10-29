import AsyncStorage from "@react-native-async-storage/async-storage";
import { ulid } from "ulid";
import { useAuthStore } from "../auth/useAuthStore";
import { fetchArchiveStates, sourceCanInfluenceWorkouts } from "./archiveStates";
import { parseRecentWorkoutSetup, type RecentWorkoutSetup } from "./workoutType";

export interface SetupScope { ownerUserId: number; profileId: number }
export type SetupFields = Omit<RecentWorkoutSetup, "savedAt" | "profileId">;
export interface RecentSetupSnapshot extends RecentWorkoutSetup {
  version: 2;
  snapshotId: string;
  ownerUserId: number;
  origin: "draft" | "recording";
  sourceSessionId?: string;
}
interface SetupSlots { version: 2; draft?: RecentSetupSnapshot; recentRecording?: RecentSetupSnapshot }
const writes = new Map<string, Promise<unknown>>();
export const recentSetupStorageKey = (scope: SetupScope) => `wod_recent_workout_setup_v2:${scope.ownerUserId}:${scope.profileId}`;
const owns = (scope: SetupScope) => {
  const auth = useAuthStore.getState();
  return auth.isLoggedIn && auth.userId === scope.ownerUserId && scope.ownerUserId > 0 && scope.profileId > 0;
};

export function createSetupSnapshot(fields: SetupFields, scope: SetupScope, now = Date.now(), inherited?: RecentSetupSnapshot): RecentSetupSnapshot {
  return { ...fields, ...scope, savedAt: now, version: 2, snapshotId: ulid(),
    origin: inherited?.origin === "recording" ? "recording" : "draft",
    ...(inherited?.origin === "recording" ? { sourceSessionId: inherited.sourceSessionId } : {}) };
}
export function parseSetupSnapshot(raw: string | null | undefined, scope: SetupScope, now = Date.now()): RecentSetupSnapshot | undefined {
  if (!raw) return undefined;
  try {
    const value = JSON.parse(raw);
    const setup = parseRecentWorkoutSetup(raw, now);
    if (!setup || value.version !== 2 || value.ownerUserId !== scope.ownerUserId || setup.profileId !== scope.profileId
      || typeof value.snapshotId !== "string" || !value.snapshotId || !["draft", "recording"].includes(value.origin)
      || (value.origin === "recording" && (typeof value.sourceSessionId !== "string" || !value.sourceSessionId))) return undefined;
    return { ...setup, version: 2, ownerUserId: value.ownerUserId, snapshotId: value.snapshotId,
      origin: value.origin, ...(value.origin === "recording" ? { sourceSessionId: value.sourceSessionId } : {}) };
  } catch { return undefined; }
}
async function readSlots(scope: SetupScope): Promise<SetupSlots> {
  if (!owns(scope)) throw new Error("setup_scope_changed");
  const raw = await AsyncStorage.getItem(recentSetupStorageKey(scope));
  if (!owns(scope)) throw new Error("setup_scope_changed");
  try {
    const slots = raw ? JSON.parse(raw) : {};
    if (slots.version !== 2) return { version: 2 };
    return { version: 2,
      draft: parseSetupSnapshot(JSON.stringify(slots.draft), scope),
      recentRecording: parseSetupSnapshot(JSON.stringify(slots.recentRecording), scope) };
  } catch { return { version: 2 }; }
}
async function updateSlots(scope: SetupScope, update: (slots: SetupSlots) => SetupSlots, isCurrent: () => boolean = () => true): Promise<void> {
  const key = recentSetupStorageKey(scope);
  const task = (writes.get(key) ?? Promise.resolve()).catch(() => {}).then(async () => {
    const slots = await readSlots(scope);
    if (!owns(scope) || !isCurrent()) throw new Error("setup_scope_changed");
    await AsyncStorage.setItem(key, JSON.stringify(update(slots)));
  });
  writes.set(key, task);
  try { await task; } finally { if (writes.get(key) === task) writes.delete(key); }
}
export async function saveDraftSetup(fields: SetupFields, scope: SetupScope): Promise<RecentSetupSnapshot> {
  const snapshot = createSetupSnapshot(fields, scope);
  await updateSlots(scope, slots => ({ ...slots, draft: snapshot }));
  return snapshot;
}
/** Edits and whiteboard merges retain their recording source until an explicit fresh-input reset. */
export async function saveSetupForLater(fields: SetupFields, scope: SetupScope, inherited?: RecentSetupSnapshot, isCurrent: () => boolean = () => true): Promise<RecentSetupSnapshot> {
  if (!isCurrent()) throw new Error("setup_scope_changed");
  if (inherited && !await verifyRecentSetup(inherited, scope)) throw new Error("archived_source");
  if (!isCurrent()) throw new Error("setup_scope_changed");
  const snapshot = createSetupSnapshot(fields, scope, Date.now(), inherited);
  await updateSlots(scope, slots => snapshot.origin === "recording"
    ? { ...slots, recentRecording: snapshot } : { ...slots, draft: snapshot }, isCurrent);
  return snapshot;
}
/** Rechecked by the camera at actual start, after permission dialogs and navigation delays. */
export async function validateRecordingSetup(raw: string | undefined, scope: SetupScope): Promise<RecentSetupSnapshot | undefined> {
  if (!owns(scope)) throw new Error("setup_scope_changed");
  if (raw === undefined) return undefined;
  const snapshot = parseSetupSnapshot(raw, scope);
  if (!snapshot || !await verifyRecentSetup(snapshot, scope)) throw new Error("archived_source");
  return snapshot;
}
/** Called only after the first original source is accepted; never overwrites a saved draft. */
export async function saveRecordingSetup(raw: string | undefined, scope: SetupScope, sessionId: string): Promise<void> {
  const candidate = parseSetupSnapshot(raw, scope);
  if (!candidate || !owns(scope)) return;
  const snapshot: RecentSetupSnapshot = { ...candidate, origin: "recording", sourceSessionId: sessionId, savedAt: Date.now() };
  await updateSlots(scope, slots => ({ ...slots, recentRecording: snapshot }));
}
export async function verifyRecentSetup(snapshot: RecentSetupSnapshot, scope: SetupScope, signal?: AbortSignal): Promise<boolean> {
  if (!owns(scope) || !parseSetupSnapshot(JSON.stringify(snapshot), scope)) return false;
  if (snapshot.origin === "draft") return true;
  const response = await fetchArchiveStates(scope.profileId, [snapshot.sourceSessionId!], signal);
  return owns(scope) && sourceCanInfluenceWorkouts({ ...scope, sessionId: snapshot.sourceSessionId! }, response.states[0]);
}
export async function loadRecentSetup(scope: SetupScope, signal?: AbortSignal): Promise<RecentSetupSnapshot | undefined> {
  const slots = await readSlots(scope);
  const candidates = [slots.draft, slots.recentRecording].filter((value): value is RecentSetupSnapshot => !!value)
    .sort((a, b) => b.savedAt - a.savedAt);
  for (const candidate of candidates) {
    try { if (await verifyRecentSetup(candidate, scope, signal)) return candidate; }
    catch { if (signal?.aborted || !owns(scope)) return undefined; /* Independent drafts remain usable offline. */ }
  }
  return undefined;
}
export async function clearRecentSetup(scope: SetupScope, snapshotId: string): Promise<void> {
  await updateSlots(scope, slots => ({ version: 2,
    draft: slots.draft?.snapshotId === snapshotId ? undefined : slots.draft,
    recentRecording: slots.recentRecording?.snapshotId === snapshotId ? undefined : slots.recentRecording }));
}
