import { useAuthStore } from "../auth/useAuthStore";
import * as FS from 'expo-file-system/legacy';
import { ulid } from 'ulid';
import { apiClient, getUploadUrl, uploadSessionAssetToGcs } from '../wod/api';
import type { EnvironmentRecord, EnvironmentSession, Evidence } from './types';

const idPattern = /^[0-9A-HJKMNP-TV-Z]{26}$/;
const root = () => `${FS.documentDirectory}environment/`;
export function bundlePath(id: string) {
  if (!idPattern.test(id)) throw new Error('invalid_bundle');
  return `${root()}${id}/`;
}
const uri = (path: string) => path.startsWith('file://') ? path : `file://${path}`;
export function evidencePath(bundleId: string, filename: string) {
  if (!/^environment_[0-9A-HJKMNP-TV-Z]{26}_\d+\.(jpg|m4a)$/.test(filename)) throw new Error('invalid_evidence');
  return bundlePath(bundleId) + filename;
}
async function atomic(path: string, data: unknown) {
  const tmp = `${path}.${ulid()}.tmp`;
  await FS.writeAsStringAsync(tmp, JSON.stringify(data));
  await FS.moveAsync({ from: tmp, to: path });
}
export async function saveSession(session: EnvironmentSession) {
  await FS.makeDirectoryAsync(bundlePath(session.id), { intermediates: true });
  await atomic(bundlePath(session.id) + 'session.json', session);
  await FS.deleteAsync(bundlePath(session.id) + 'session.uploaded', { idempotent: true });
}
export async function saveRecord(record: EnvironmentRecord) {
  if (!idPattern.test(record.id)) throw new Error('invalid_record');
  await atomic(bundlePath(record.bundleId) + `environment_${record.id}.json`, record);
}
export async function archiveEvidence(bundleId: string, recordId: string, inputs: { path: string; mediaOffsetMs?: number; width?: number; height?: number }[], mime: 'image/jpeg' | 'audio/mp4'): Promise<Evidence[]> {
  const output: Evidence[] = [];
  for (const [index, input] of inputs.entries()) {
    const filename = `environment_${recordId}_${index}.${mime === 'image/jpeg' ? 'jpg' : 'm4a'}`;
    const to = evidencePath(bundleId, filename);
    await FS.copyAsync({ from: uri(input.path), to });
    const info = await FS.getInfoAsync(to);
    output.push({ filename, mime, bytes: info.exists ? info.size : 0,
      mediaOffsetMs: input.mediaOffsetMs, width: input.width, height: input.height });
  }
  return output;
}
export async function listSessions(profileId?: number): Promise<EnvironmentSession[]> {
  if (!FS.documentDirectory || !(await FS.getInfoAsync(root())).exists) return [];
  const results: EnvironmentSession[] = [];
  for (const id of (await FS.readDirectoryAsync(root())).filter(id => idPattern.test(id))) {
    try {
      const s: EnvironmentSession = JSON.parse(await FS.readAsStringAsync(bundlePath(id) + 'session.json'));
      if (s.version === 1 && s.id === id && s.ownerUserId !== null && s.ownerUserId === useAuthStore.getState().userId && (profileId === undefined || s.profileId === profileId)) results.push(s);
    } catch { /* Incomplete metadata is never published as a complete session. */ }
  }
  return results.sort((a,b) => b.startedAt-a.startedAt);
}
export async function readRecords(session: EnvironmentSession): Promise<EnvironmentRecord[]> {
  const records: EnvironmentRecord[] = [];
  for (const filename of await FS.readDirectoryAsync(bundlePath(session.id))) {
    if (!/^environment_[0-9A-HJKMNP-TV-Z]{26}\.json$/.test(filename)) continue;
    const r: EnvironmentRecord = JSON.parse(await FS.readAsStringAsync(bundlePath(session.id) + filename));
    if (r.version === 1 && r.bundleId === session.id && r.sessionId === session.sessionId && r.profileId === session.profileId) records.push(r);
  }
  return records.sort((a,b) => a.startedAt-b.startedAt);
}
let recordingCount = 0;
export function holdEnvironmentUploads() { recordingCount++; let released = false; return () => { if (!released) { recordingCount--; released = true; } }; }
export const environmentRecordingActive = () => recordingCount > 0 || useAuthStore.getState().isRecordingActive;
let flushing: Promise<void> | null = null;
let flushAgain = false;
/** Retain local evidence for review. Uploaded markers make retries idempotent. */
export function flushEnvironmentUploads(): Promise<void> {
  if (environmentRecordingActive() || !useAuthStore.getState().isLoggedIn) return Promise.resolve();
  if (flushing) { flushAgain = true; return flushing; }
  flushing = (async () => {
    for (const session of await listSessions()) {
      if (environmentRecordingActive()) return;
      const dir = bundlePath(session.id);
      if (session.deleting) {
        try {
          await apiClient(`/sessions/${encodeURIComponent(session.sessionId)}/environment?profile_id=${session.profileId}`, { method: 'DELETE' });
          await FS.deleteAsync(dir, { idempotent: true });
        } catch (error) { console.warn('Environment deletion retained for retry', error); }
        continue;
      }
      // No live controller owns this session: a missing footer means interrupted capture.
      if (session.endedAt === null) {
        session.endedAt = Date.now(); session.complete = false;
        await saveSession(session);
      }
      const upload = async (filename: string, mime: string) => {
        if (environmentRecordingActive()) throw new Error('recording_active');
        if (!useAuthStore.getState().isLoggedIn || session.ownerUserId !== useAuthStore.getState().userId) throw new Error('account_changed');
        const { upload_url } = await getUploadUrl(session.sessionId, filename, session.profileId);
        if (!upload_url) throw new Error('missing_upload_url');
        await uploadSessionAssetToGcs(upload_url, dir + filename, mime);
      };
      try {
        for (const r of await readRecords(session)) {
          if ((await FS.getInfoAsync(dir + `${r.id}.uploaded`)).exists) continue;
          for (const e of r.evidence) {
            evidencePath(session.id, e.filename);
            await upload(e.filename, e.mime);
          }
          await upload(`environment_${r.id}.json`, 'application/json');
          await FS.writeAsStringAsync(dir + `${r.id}.uploaded`, '1');
        }
        if (!(await FS.getInfoAsync(dir + 'session.uploaded')).exists) {
          if (session.eventsFile && /^environment_[0-9A-HJKMNP-TV-Z]{26}_events\.ndjson$/.test(session.eventsFile) && (await FS.getInfoAsync(dir + session.eventsFile)).exists)
            await upload(session.eventsFile, 'application/x-ndjson');
          const name = `environment_${session.id}_session.json`;
          await FS.copyAsync({ from: dir + 'session.json', to: dir + name });
          await upload(name, 'application/json');
          await FS.writeAsStringAsync(dir + 'session.uploaded', '1');
        }
      } catch (error) { console.warn('Environment upload retained:', session.id, error); }
    }
  })().finally(() => { flushing = null; if (flushAgain) { flushAgain = false; void flushEnvironmentUploads().catch(() => {}); } });
  return flushing;
}
/** Durable deletion wins over upload; a failed server request stays pending. */
export async function deleteEnvironmentSession(profileId: number, sessionId: string) {
  if (environmentRecordingActive()) throw new Error('recording_active');
  await flushing;
  for (const s of await listSessions(profileId)) if (s.sessionId === sessionId) await saveSession({ ...s, deleting: true });
  await flushEnvironmentUploads();
  if ((await listSessions(profileId)).some(s => s.sessionId === sessionId)) throw new Error('deletion_pending');
}
