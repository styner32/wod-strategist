import * as FS from "expo-file-system/legacy";
import * as MediaLibrary from "expo-media-library/legacy";

import { useAuthStore } from "../auth/useAuthStore";
import { mergeChunksLocal } from "../wod/mergeChunksLocal";

export interface OriginalSessionRef {
  ownerUserId: number;
  profileId: number;
  sessionId: string;
}

export async function discardOriginalSession(ref: OriginalSessionRef): Promise<void> {
  assertOwner(ref);
  const id = key(ref);
  if (recording.has(id)) throw new Error("cannot_discard_recording_original");
  if (saving.has(id)) throw new Error("cannot_discard_saving_original");
  if ((holds.get(id) ?? 0) > 0) throw new Error("cannot_discard_held_original");
  await exclusive(ref, async () => {
    const directory = getOriginalDirectory(ref);
    await FS.deleteAsync(directory, { idempotent: true });
    queues.delete(id);
    saving.delete(id);
    recording.delete(id);
    holds.delete(id);
  });
  notify();
}

export type OriginalVideoStatus =
  | "recording" | "pending" | "preparing" | "saving" | "saved"
  | "permission_denied" | "failed" | "uncertain" | "needs_attention";

interface OriginalChunk {
  order: number;
  directory: string;
  path?: string;
  finalized: boolean;
  sizeBytes?: number;
  durationSecs?: number;
}

export interface OriginalVideoSession extends OriginalSessionRef {
  version: 1;
  revision: number;
  mode: "chunks" | "continuous";
  createdAt: number;
  stoppedAt: number | null;
  complete: boolean;
  status: OriginalVideoStatus;
  chunks: OriginalChunk[];
  outputPath?: string;
  /** Reserved at capture start; absent on sessions created before named exports. */
  galleryFileStem?: string;
  savedAt?: number;
  cleanedAt?: number;
  captureIssue?: string;
  lastError?: string;
  lastErrorStage?: "validation" | "permission" | "preparing" | "gallery";
}

const sessionPattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,180}$/;
const revisionPattern = /^manifest-(\d+)\.json$/;
const compareRevisions = (a: string, b: string) => Number(a.match(revisionPattern)?.[1] ?? 0) - Number(b.match(revisionPattern)?.[1] ?? 0);
const queues = new Map<string, Promise<unknown>>();
const saving = new Map<string, Promise<OriginalVideoSession>>();
const recording = new Set<string>();
const holds = new Map<string, number>();
const listeners = new Set<() => void>();
let filenameQueue: Promise<unknown> = Promise.resolve();
const galleryFileStemPattern = /^(WARMUP|WOD|ACCESSORY|COOLDOWN)-\d{8}-[1-9]\d*$/;

function key(ref: OriginalSessionRef) {
  return `${ref.ownerUserId}:${ref.profileId}:${ref.sessionId}`;
}

function assertOwner(ref: OriginalSessionRef) {
  const auth = useAuthStore.getState();
  if (!auth.isLoggedIn || auth.userId !== ref.ownerUserId) throw new Error("original_account_changed");
  if (!Number.isSafeInteger(ref.profileId) || ref.profileId <= 0 ||
      !Number.isSafeInteger(ref.ownerUserId) || ref.ownerUserId <= 0 ||
      !sessionPattern.test(ref.sessionId)) throw new Error("invalid_original_session");
}

function root() {
  if (!FS.documentDirectory) throw new Error("original_documents_unavailable");
  return `${FS.documentDirectory}originals/`;
}

/** A file URI; Camera's native path option expects a decoded absolute path. */
export function getOriginalDirectory(ref: OriginalSessionRef): string {
  assertOwner(ref);
  return `${root()}${ref.profileId}/${ref.sessionId}/`;
}

function fileUri(path: string) {
  if (path.startsWith("file://")) return path;
  if (!path.startsWith("/")) throw new Error("original_local_file_required");
  return `file://${path}`;
}

function relativePath(ref: OriginalSessionRef, path: string) {
  const uri = fileUri(path);
  const directory = getOriginalDirectory(ref);
  // Decode for comparisons; persist relative paths so iOS container UUID changes are harmless.
  const decodedUri = decodeURIComponent(uri);
  const decodedDirectory = decodeURIComponent(directory);
  if (!decodedUri.startsWith(decodedDirectory)) throw new Error("original_path_outside_session");
  const relative = decodedUri.slice(decodedDirectory.length);
  if (!safeRelative(relative)) throw new Error("invalid_original_path");
  return relative;
}

function safeRelative(path: unknown): path is string {
  return typeof path === "string" && path.length > 0 && !path.startsWith("/") &&
    !path.includes("\\") && !path.includes("\0") && !path.split("/").some(p => p === "." || p === ".." || !p);
}

function resolvePath(ref: OriginalSessionRef, relative: string) {
  if (!safeRelative(relative)) throw new Error("invalid_original_path");
  return getOriginalDirectory(ref) + relative.split("/").map(encodeURIComponent).join("/");
}

function notify() {
  listeners.forEach(listener => {
    try { listener(); } catch (error) { console.warn("Original archive subscriber failed", error); }
  });
}

export function subscribeOriginalVideos(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

function exclusive<T>(ref: OriginalSessionRef, action: () => Promise<T>): Promise<T> {
  const id = key(ref);
  const result = (queues.get(id) ?? Promise.resolve()).catch(() => {}).then(action);
  queues.set(id, result);
  void result.finally(() => { if (queues.get(id) === result) queues.delete(id); }).catch(() => {});
  return result;
}

async function atomicNewFile(path: string, value: unknown) {
  const temporary = `${path}.tmp`;
  await FS.writeAsStringAsync(temporary, JSON.stringify(value));
  await FS.moveAsync({ from: temporary, to: path });
}

/** Device-wide reservations survive cleanup, account changes and interrupted starts. */
function reserveGalleryFileStem(sessionId: string, createdAt: number): Promise<string> {
  const workoutType = sessionId.match(/^(?:P\d+-)?(WARMUP|WOD|ACCESSORY|COOLDOWN)-/)?.[1];
  if (!workoutType) return Promise.reject(new Error("original_workout_type_invalid"));
  const started = new Date(createdAt);
  const date = `${started.getFullYear()}${String(started.getMonth() + 1).padStart(2, "0")}${String(started.getDate()).padStart(2, "0")}`;
  const task = filenameQueue.catch(() => {}).then(async () => {
    const directory = `${root()}filename-sequences/${date}/`;
    await FS.makeDirectoryAsync(directory, { intermediates: true });
    const names = await FS.readDirectoryAsync(directory);
    let last = 0;
    for (const name of names) {
      // A temp marker may have been written just before a crash. Never reuse it.
      const match = /^([1-9]\d*)\.json(?:\.tmp)?$/.exec(name);
      if (!match) continue;
      const sequence = Number(match[1]);
      if (!Number.isSafeInteger(sequence)) throw new Error("original_filename_sequence_invalid");
      last = Math.max(last, sequence);
    }
    const next = last + 1;
    if (!Number.isSafeInteger(next)) throw new Error("original_filename_sequence_exhausted");
    // Immutable, non-personal markers are not deleted when a saved video is cleaned.
    await atomicNewFile(`${directory}${next}.json`, { version: 1 });
    return `${workoutType}-${date}-${next}`;
  });
  filenameQueue = task;
  return task;
}

/** Commit to a new filename: no delete/replace window can erase the last valid manifest. */
async function commit(session: OriginalVideoSession) {
  assertOwner(session);
  const directory = getOriginalDirectory(session);
  const files = await FS.readDirectoryAsync(directory);
  const lastRevision = Math.max(0, ...files.map(f => Number(f.match(revisionPattern)?.[1] ?? 0)));
  session.revision = Math.max(session.revision, lastRevision) + 1;
  await atomicNewFile(`${directory}manifest-${String(session.revision).padStart(8, "0")}.json`, session);
  // Retain a previous committed revision for diagnostics. Never prune source files here.
  for (const name of files.filter(f => revisionPattern.test(f)).sort(compareRevisions).slice(0, -1)) {
    await FS.deleteAsync(directory + name, { idempotent: true }).catch(() => {});
  }
  notify();
}

function validManifest(value: any, ref: OriginalSessionRef): value is OriginalVideoSession {
  return value?.version === 1 && value.ownerUserId === ref.ownerUserId && value.profileId === ref.profileId &&
    value.sessionId === ref.sessionId && Number.isSafeInteger(value.revision) && Array.isArray(value.chunks) &&
    (value.mode === "chunks" || value.mode === "continuous") && typeof value.createdAt === "number" &&
    (value.stoppedAt === null || typeof value.stoppedAt === "number") && typeof value.complete === "boolean" &&
    ["recording", "pending", "preparing", "saving", "saved", "permission_denied", "failed", "uncertain", "needs_attention"].includes(value.status) &&
    value.chunks.every((part: OriginalChunk) => Number.isSafeInteger(part.order) && part.order >= 0 &&
      part.directory === `sources/${String(part.order).padStart(6, "0")}` && typeof part.finalized === "boolean" &&
      (part.path === undefined || (safeRelative(part.path) && part.path.startsWith(`${part.directory}/`)))) &&
    (value.outputPath === undefined || safeRelative(value.outputPath)) &&
    (value.galleryFileStem === undefined || (typeof value.galleryFileStem === "string" && galleryFileStemPattern.test(value.galleryFileStem))) &&
    (value.status !== "saved" || (value.complete && typeof value.savedAt === "number" && value.savedAt > 0));
}

async function read(ref: OriginalSessionRef): Promise<OriginalVideoSession> {
  assertOwner(ref);
  const directory = getOriginalDirectory(ref);
  const owner = JSON.parse(await FS.readAsStringAsync(directory + "owner.json"));
  if (owner.ownerUserId !== ref.ownerUserId || owner.profileId !== ref.profileId || owner.sessionId !== ref.sessionId) {
    throw new Error("original_owner_mismatch");
  }
  const names = (await FS.readDirectoryAsync(directory)).filter(f => revisionPattern.test(f)).sort(compareRevisions).reverse();
  for (const [index, name] of names.entries()) {
    try {
      const session = JSON.parse(await FS.readAsStringAsync(directory + name));
      if (!validManifest(session, ref)) continue;
      if (index > 0) {
        // A corrupt newer state could have been saving. Never replay it automatically.
        return { ...session, status: "needs_attention", complete: false, captureIssue: "manifest_invalid", lastError: "manifest_invalid" };
      }
      return session;
    } catch { /* Preserve corrupt files and use older metadata only for recovery display. */ }
  }
  return { ...ref, version: 1, revision: 0, mode: "chunks", createdAt: owner.createdAt ?? Date.now(),
    stoppedAt: Date.now(), complete: false, chunks: [], status: "needs_attention", captureIssue: "manifest_missing", lastError: "manifest_missing" };
}

export async function prepareOriginalSession(input: {
  profileId: number; sessionId: string; mode?: "chunks" | "continuous";
}): Promise<OriginalSessionRef> {
  const ref: OriginalSessionRef = { profileId: input.profileId, sessionId: input.sessionId, ownerUserId: useAuthStore.getState().userId ?? 0 };
  assertOwner(ref);
  const createdAt = Date.now();
  await exclusive(ref, async () => {
    const directory = getOriginalDirectory(ref);
    if ((await FS.getInfoAsync(directory)).exists) throw new Error("original_session_already_exists");
    await FS.makeDirectoryAsync(directory, { intermediates: true });
    const galleryFileStem = await reserveGalleryFileStem(ref.sessionId, createdAt);
    assertOwner(ref);
    await atomicNewFile(directory + "owner.json", { ...ref, createdAt });
    await commit({ ...ref, version: 1, revision: 0, mode: input.mode ?? "chunks", createdAt, galleryFileStem,
      stoppedAt: null, complete: false, chunks: [], status: "recording" });
    recording.add(key(ref));
  });
  return ref;
}

/** Persist the expected source before capture, including a unique directory for crash reconciliation. */
export async function beginOriginalChunk(ref: OriginalSessionRef, order: number): Promise<string> {
  return exclusive(ref, async () => {
    const session = await read(ref);
    if (session.status !== "recording" || session.stoppedAt !== null || !Number.isSafeInteger(order) || order < 0 ||
        (session.chunks.length > 0 && order !== session.chunks[session.chunks.length - 1].order + 1)) {
      throw new Error("invalid_original_chunk_order");
    }
    const directory = `sources/${String(order).padStart(6, "0")}`;
    await FS.makeDirectoryAsync(resolvePath(ref, directory), { intermediates: true });
    session.chunks.push({ order, directory, finalized: false });
    await commit(session);
    return resolvePath(ref, directory) + "/";
  });
}

export async function addOriginalChunk(ref: OriginalSessionRef, input: {
  order: number; path: string; durationSecs: number;
}): Promise<void> {
  return exclusive(ref, async () => {
    const session = await read(ref);
    const chunk = session.chunks.find(c => c.order === input.order);
    const lateIncomplete = session.stoppedAt !== null && !session.complete && session.status === "needs_attention" && chunk && !chunk.finalized;
    if (session.status !== "recording" && !lateIncomplete) throw new Error("unexpected_original_chunk");
    try {
      if (!chunk) throw new Error("unexpected_original_chunk");
      const path = relativePath(ref, input.path);
      if (!path.startsWith(`${chunk.directory}/`) || path.slice(chunk.directory.length + 1).includes("/") ||
          !/\.(mp4|mov)$/i.test(path) || !Number.isFinite(input.durationSecs) || input.durationSecs <= 0) {
        throw new Error("invalid_original_chunk");
      }
      const info = await FS.getInfoAsync(resolvePath(ref, path));
      if (!info.exists || info.isDirectory || info.size <= 0) throw new Error("empty_original_chunk");
      if (chunk.finalized && (chunk.path !== path || chunk.sizeBytes !== info.size)) throw new Error("original_chunk_changed");
      Object.assign(chunk, { path, finalized: true, sizeBytes: info.size, durationSecs: input.durationSecs });
      await commit(session);
    } catch (error) {
      session.captureIssue = error instanceof Error ? error.message : "original_chunk_registration_failed";
      session.complete = false;
      await commit(session);
      throw error;
    }
  });
}

export async function markOriginalRecordingStopped(ref: OriginalSessionRef, result: {
  complete: boolean; reason?: string;
}): Promise<void> {
  try {
    await exclusive(ref, async () => {
      const session = await read(ref);
      if (session.stoppedAt !== null) return;
      session.stoppedAt = Date.now();
      if (!result.complete) session.captureIssue = result.reason ?? "recording_incomplete";
      session.complete = result.complete && !session.captureIssue;
      session.status = session.complete ? "pending" : "needs_attention";
      session.lastError = session.captureIssue;
      await commit(session);
    });
  } finally {
    // Even a failed footer write must not make recovery think a departed screen is still recording.
    recording.delete(key(ref));
  }
}

async function validateSources(session: OriginalVideoSession): Promise<string[]> {
  if (!session.complete || session.stoppedAt === null || session.captureIssue || session.chunks.length === 0) {
    throw new Error(session.captureIssue ?? "recording_incomplete");
  }
  const sourceDirectories = await FS.readDirectoryAsync(resolvePath(session, "sources"));
  const expectedDirectories = session.chunks.map(c => c.directory.split("/")[1]);
  if (sourceDirectories.some(name => !expectedDirectories.includes(name))) throw new Error("unknown_original_source");
  const paths: string[] = [];
  for (const [index, chunk] of session.chunks.entries()) {
    if (!chunk.finalized || !chunk.path || chunk.sizeBytes === undefined ||
        (index > 0 && chunk.order !== session.chunks[index - 1].order + 1)) throw new Error("unfinalized_original_source");
    const path = resolvePath(session, chunk.path);
    const info = await FS.getInfoAsync(path);
    if (!info.exists || info.isDirectory || info.size <= 0 || info.size !== chunk.sizeBytes) throw new Error("missing_or_changed_original_source");
    const entries = await FS.readDirectoryAsync(resolvePath(session, chunk.directory));
    const filename = chunk.path.slice(chunk.directory.length + 1);
    if (entries.some(name => name !== filename && !(session.mode === "continuous" &&
        (name === "analysis" || name === `${filename}.segments.json`)))) {
      throw new Error("unknown_original_source");
    }
    paths.push(path);
  }
  return paths;
}

async function cleanSaved(session: OriginalVideoSession) {
  if (session.status !== "saved" || !session.savedAt || session.cleanedAt || (holds.get(key(session)) ?? 0) > 0) return;
  assertOwner(session);
  // Delete only registered sources and the validated result, never unknown recovery evidence.
  const paths = new Set([...session.chunks.flatMap(c => c.path ? [c.path] : []), ...(session.outputPath ? [session.outputPath] : [])]);
  for (const path of paths) await FS.deleteAsync(resolvePath(session, path), { idempotent: true });
  if (session.mode === "continuous") {
    for (const chunk of session.chunks) {
      await FS.deleteAsync(resolvePath(session, `${chunk.directory}/analysis`), { idempotent: true });
      if (chunk.path) await FS.deleteAsync(resolvePath(session, `${chunk.path}.segments.json`), { idempotent: true });
    }
  }
  session.cleanedAt = Date.now();
  await commit(session);
}

/** Hold while upload, compression or analysis still reads camera originals. Release is idempotent. */
export function holdOriginalFiles(ref: OriginalSessionRef): () => void {
  assertOwner(ref);
  const id = key(ref);
  holds.set(id, (holds.get(id) ?? 0) + 1);
  let released = false;
  return () => {
    if (released) return;
    released = true;
    const remaining = Math.max(0, (holds.get(id) ?? 1) - 1);
    if (remaining) holds.set(id, remaining); else holds.delete(id);
    if (!remaining) void exclusive(ref, async () => cleanSaved(await read(ref))).catch(() => {});
  };
}

/** Expected failures return a retained session; authentication/storage-contract failures may reject. */
export function finalizeAndSaveOriginal(ref: OriginalSessionRef, options: {
  retryUncertain?: boolean;
} = {}): Promise<OriginalVideoSession> {
  assertOwner(ref);
  const id = key(ref);
  const existing = saving.get(id);
  if (existing) return existing;
  const task = exclusive(ref, async () => {
    const session = await read(ref);
    if (session.status === "saved") { await cleanSaved(session); return session; }
    if ((session.status === "saving" || session.status === "uncertain") && !options.retryUncertain) {
      session.status = "uncertain";
      session.lastError = "gallery_result_unknown";
      await commit(session);
      return session;
    }
    let sources: string[];
    try { sources = await validateSources(session); }
    catch (error) {
      session.status = "needs_attention";
      session.lastErrorStage = "validation";
      session.lastError = error instanceof Error ? error.message : "original_sources_invalid";
      await commit(session);
      return session;
    }
    let failureStage: OriginalVideoSession["lastErrorStage"] = "permission";
    try {
      const permission = await MediaLibrary.requestPermissionsAsync(true);
      assertOwner(ref);
      if (!permission.granted) {
        session.status = "permission_denied";
        session.lastErrorStage = "permission";
        session.lastError = "gallery_permission_denied";
        await commit(session);
        return session;
      }
      session.status = "preparing";
      session.lastError = undefined;
      session.lastErrorStage = undefined;
      failureStage = "preparing";
      await commit(session);
      let output = session.outputPath ? resolvePath(ref, session.outputPath) : undefined;
      if (output) {
        const info = await FS.getInfoAsync(output);
        if (!info.exists || info.isDirectory || info.size <= 0) output = undefined;
      }
      if (!output) {
        // The native passthrough validator also verifies a single finalized recording.
        await FS.makeDirectoryAsync(resolvePath(ref, "output"), { intermediates: true });
        output = await mergeChunksLocal(sources, resolvePath(ref, `output/${session.galleryFileStem ?? "original"}.mp4`));
      }
      const outputPath = relativePath(ref, output);
      if (!outputPath.startsWith("output/") || session.chunks.some(chunk => chunk.path === outputPath)) {
        throw new Error("original_output_overlaps_source");
      }
      session.outputPath = outputPath;
      const outputInfo = await FS.getInfoAsync(fileUri(output));
      if (!outputInfo.exists || outputInfo.isDirectory || outputInfo.size <= 0 || !/\.(mp4|mov)$/i.test(output)) {
        throw new Error("invalid_original_output");
      }
      assertOwner(ref);
      failureStage = "gallery";
      session.status = "saving";
      // Must be durable before invoking a non-idempotent platform Photos operation.
      await commit(session);
      await MediaLibrary.saveToLibraryAsync(fileUri(output));
    } catch (error) {
      assertOwner(ref);
      session.status = "failed";
      session.lastErrorStage = failureStage;
      session.lastError = error instanceof Error ? error.message : "gallery_save_failed";
      await commit(session);
      return session;
    }
    session.status = "saved";
    session.savedAt = Date.now();
    session.lastError = undefined;
    session.lastErrorStage = undefined;
    // If this commit fails, the durable state stays "saving": recovery asks the user.
    await commit(session);
    await cleanSaved(session).catch(error => { console.warn("Original cleanup retained for retry", error); });
    return session;
  });
  saving.set(id, task);
  void task.finally(() => { if (saving.get(id) === task) saving.delete(id); }).catch(() => {});
  return task;
}

export async function confirmOriginalAlreadySaved(ref: OriginalSessionRef): Promise<void> {
  await exclusive(ref, async () => {
    const session = await read(ref);
    if (session.status !== "uncertain" && session.status !== "saving") throw new Error("original_save_not_uncertain");
    if (saving.has(key(ref))) throw new Error("original_save_in_progress");
    session.status = "saved";
    session.savedAt = Date.now();
    session.lastError = undefined;
    await commit(session);
    await cleanSaved(session);
  });
}

/** Recovery never restarts a Photos save. Pending originals also exist without any server row. */
export async function listOriginalSessions(profileId?: number | null): Promise<OriginalVideoSession[]> {
  const auth = useAuthStore.getState();
  if (!auth.isLoggedIn || !auth.userId) return [];
  if (profileId == null) {
    // Profile hydration can be unavailable offline. Ownership is still checked in every manifest.
    if (!(await FS.getInfoAsync(root())).exists) return [];
    const all: OriginalVideoSession[] = [];
    for (const name of await FS.readDirectoryAsync(root())) {
      if (/^[1-9]\d*$/.test(name) && Number.isSafeInteger(Number(name))) {
        all.push(...await listOriginalSessions(Number(name)));
      }
    }
    if (useAuthStore.getState().userId !== auth.userId) return [];
    return all.sort((a, b) => b.createdAt - a.createdAt);
  }
  if (!Number.isSafeInteger(profileId) || profileId <= 0) return [];
  const directory = `${root()}${profileId}/`;
  if (!(await FS.getInfoAsync(directory)).exists) return [];
  const results: OriginalVideoSession[] = [];
  for (const sessionId of await FS.readDirectoryAsync(directory)) {
    if (!sessionPattern.test(sessionId)) continue;
    const ref = { ownerUserId: auth.userId, profileId, sessionId };
    try {
      let session = await read(ref);
      if (!recording.has(key(ref)) && !saving.has(key(ref))) {
        session = await exclusive(ref, async () => {
          const current = await read(ref);
          if (current.status === "saving") {
            current.status = "uncertain";
            current.lastError = "gallery_result_unknown";
            await commit(current);
          } else if (current.stoppedAt === null || current.status === "recording") {
            current.stoppedAt = Date.now();
            current.complete = false;
            current.status = "needs_attention";
            current.captureIssue = "recording_interrupted";
            current.lastError = "recording_interrupted";
            await commit(current);
          } else if (current.status === "preparing") {
            current.status = "pending";
            await commit(current);
          }
          if (current.status === "saved") await cleanSaved(current).catch(() => {});
          return current;
        });
      }
      assertOwner(ref);
      results.push(session);
    } catch { /* Unowned or unidentifiable records remain on disk and are never exported or deleted. */ }
  }
  if (useAuthStore.getState().userId !== auth.userId) return [];
  return results.sort((a, b) => b.createdAt - a.createdAt);
}
