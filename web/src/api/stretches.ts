import { api } from './client';

export interface Stretch {
  id: number;
  name: string;
  target_area: string;
  description: string;
  duration_hint: string;
  caution: string;
  image_url?: string;
  video_url?: string;
  aliases: string[];
  created_at: string;
  updated_at: string;
}

export interface StretchInput {
  name: string;
  target_area: string;
  description: string;
  duration_hint: string;
  caution: string;
  aliases: string[];
}

export interface StretchMediaUploadUrlResponse {
  upload_url: string;
  object_name: string;
}

export function normalizeStretchKey(raw: string): string {
  if (!raw) return '';
  return raw.trim().toLowerCase().replace(/[\s-]+/g, ' ');
}

export function buildStretchLookup(stretches: Stretch[]): Map<string, Stretch> {
  const lookup = new Map<string, Stretch>();
  if (!stretches) return lookup;

  for (const s of stretches) {
    const mainKey = normalizeStretchKey(s.name);
    if (mainKey) {
      lookup.set(mainKey, s);
    }
    if (Array.isArray(s.aliases)) {
      for (const alias of s.aliases) {
        const aliasKey = normalizeStretchKey(alias);
        if (aliasKey) {
          lookup.set(aliasKey, s);
        }
      }
    }
  }

  return lookup;
}

export const stretchesApi = {
  list: () => api.get<Stretch[]>('/stretches'),
  create: (data: StretchInput) => api.post<Stretch>('/stretches', data),
  update: (id: number, data: StretchInput) => api.put<Stretch>(`/stretches/${id}`, data),
  remove: (id: number) => api.delete<void>(`/stretches/${id}`),
  getMediaUploadUrl: (id: number, filename: string, mediaType: 'image' | 'video') =>
    api.post<StretchMediaUploadUrlResponse>(`/stretches/${id}/media-upload-url`, {
      filename,
      media_type: mediaType,
    }),
  setMedia: (id: number, objectName: string, mediaType: 'image' | 'video') =>
    api.post<Stretch>(`/stretches/${id}/media`, {
      object_name: objectName,
      media_type: mediaType,
    }),
  clearMedia: (id: number, kind?: 'image' | 'video') =>
    api.delete<Stretch>(`/stretches/${id}/media${kind ? `?kind=${kind}` : ''}`),
};
