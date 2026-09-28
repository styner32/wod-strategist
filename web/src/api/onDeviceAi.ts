import { api } from './client';

export interface OnDeviceFileList { files: string[]; next_cursor: string }
export function onDeviceAssetPath(sessionId: string, profileId: number, filename: string) {
  return `/sessions/${encodeURIComponent(sessionId)}/on-device-ai/asset?${new URLSearchParams({ profile_id: String(profileId), filename })}`;
}
export const onDeviceAiApi = {
  list: (sessionId: string, profileId: number, after = '') => api.get<OnDeviceFileList>(
    `/sessions/${encodeURIComponent(sessionId)}/on-device-ai?${new URLSearchParams({ profile_id: String(profileId), after })}`),
  read: (sessionId: string, profileId: number, filename: string) =>
    api.get<unknown>(onDeviceAssetPath(sessionId, profileId, filename)),
};

/** Explicit review action only; no eager downloads of media or historical sessions. */
export async function readEnvironmentRecords(sessionId: string, profileId: number): Promise<unknown[]> {
  const files = new Set<string>();
  let after = '';
  const cursors = new Set<string>();
  do {
    if (cursors.has(after)) throw new Error('Repeated archive cursor');
    cursors.add(after);
    const page = await onDeviceAiApi.list(sessionId, profileId, after);
    page.files.filter(name => /^environment_[0-9A-HJKMNP-TV-Z]{26}\.json$/.test(name)).forEach(name => files.add(name));
    after = page.next_cursor;
  } while (after);
  const records: unknown[] = [];
  const names = [...files];
  for (let i = 0; i < names.length; i += 4) {
    records.push(...await Promise.all(names.slice(i, i + 4).map(name => onDeviceAiApi.read(sessionId, profileId, name))));
  }
  return records;
}
