import type { ChunkAnalysisResult } from "../api/history";

/**
 * Tracking boxes come from chunk analysis (`target_cues.boxes`): up to three
 * boxes per chunk in Gemini's normalized [ymin, xmin, ymax, xmax] 0..1000
 * format, timed in chunk-local seconds. They are resolution independent and
 * are placed on the merged-media timeline only through verified
 * `media_start_secs`/`media_end_secs` (never capture-clock offsets).
 */
export const TRACKING_MIN_CONFIDENCE = 0.7;
export const TRACKING_OVERLAY_STORAGE_KEY = "wod.trackingBoxOverlay";

export type NormalizedBox = [number, number, number, number];

export interface TrackingSample {
  media_secs: number;
  box: NormalizedBox;
}

export interface TrackingSpan {
  chunk_id: number;
  media_start_secs: number;
  media_end_secs: number;
  samples: TrackingSample[];
}

interface StoredTargetPerson {
  ambiguous?: boolean;
  confidence?: number;
  boxes?: { t?: number; box_2d?: number[] }[];
}

function validBox(raw: number[] | undefined): NormalizedBox | null {
  if (!Array.isArray(raw) || raw.length !== 4 || raw.some(v => typeof v !== "number" || !Number.isFinite(v) || v < 0 || v > 1000)) return null;
  const [ymin, xmin, ymax, xmax] = raw;
  return ymin < ymax && xmin < xmax ? [ymin, xmin, ymax, xmax] : null;
}

function readTargetPerson(chunk: ChunkAnalysisResult): StoredTargetPerson | null {
  const raw = (chunk as { target_cues?: unknown }).target_cues;
  if (raw == null) return null;
  if (typeof raw === "object") return raw as StoredTargetPerson;
  if (typeof raw !== "string" || !raw.trim()) return null;
  try {
    return JSON.parse(raw) as StoredTargetPerson;
  } catch {
    return null;
  }
}

export function buildTrackingTimeline(chunks: ChunkAnalysisResult[] = []): TrackingSpan[] {
  const spans: TrackingSpan[] = [];
  for (const chunk of chunks) {
    const mediaStart = chunk.media_start_secs;
    const mediaEnd = chunk.media_end_secs;
    if (chunk.status !== "COMPLETED" || mediaStart == null || mediaEnd == null || !Number.isFinite(mediaStart) || !Number.isFinite(mediaEnd) || mediaEnd <= mediaStart) continue;
    const target = readTargetPerson(chunk);
    if (!target || target.ambiguous || (target.confidence ?? 0) < TRACKING_MIN_CONFIDENCE || !Array.isArray(target.boxes)) continue;
    // Chunk-local seconds follow the chunk file; scale them onto the media span.
    const captureDuration =
      chunk.start_secs != null && chunk.end_secs != null && chunk.end_secs > chunk.start_secs ? chunk.end_secs - chunk.start_secs : mediaEnd - mediaStart;
    const scale = (mediaEnd - mediaStart) / captureDuration;
    const samples = target.boxes
      .map(entry => {
        const box = validBox(entry.box_2d);
        const t = entry.t;
        if (!box || typeof t !== "number" || !Number.isFinite(t) || t < 0) return null;
        return { media_secs: Math.min(mediaEnd, mediaStart + t * scale), box };
      })
      .filter((s): s is TrackingSample => s != null)
      .sort((a, b) => a.media_secs - b.media_secs);
    if (samples.length > 0) spans.push({ chunk_id: chunk.id, media_start_secs: mediaStart, media_end_secs: mediaEnd, samples });
  }
  return spans.sort((a, b) => a.media_start_secs - b.media_start_secs);
}

/** Box at a playback time: linear interpolation inside one chunk, hidden outside chunks with boxes. */
export function trackingBoxAt(spans: TrackingSpan[], seconds: number): NormalizedBox | null {
  if (!Number.isFinite(seconds)) return null;
  const span = spans.find(s => seconds >= s.media_start_secs && seconds < s.media_end_secs);
  if (!span) return null;
  const { samples } = span;
  if (seconds <= samples[0].media_secs) return samples[0].box;
  const last = samples[samples.length - 1];
  if (seconds >= last.media_secs) return last.box;
  for (let i = 1; i < samples.length; i++) {
    const a = samples[i - 1];
    const b = samples[i];
    if (seconds <= b.media_secs) {
      const f = (seconds - a.media_secs) / (b.media_secs - a.media_secs || 1);
      return a.box.map((v, k) => v + (b.box[k] - v) * f) as NormalizedBox;
    }
  }
  return last.box;
}

/** Rectangle actually covered by an object-fit: contain video inside its element. */
export function containedVideoRect(elementWidth: number, elementHeight: number, videoWidth: number, videoHeight: number) {
  if (elementWidth <= 0 || elementHeight <= 0 || videoWidth <= 0 || videoHeight <= 0) return null;
  const scale = Math.min(elementWidth / videoWidth, elementHeight / videoHeight);
  const width = videoWidth * scale;
  const height = videoHeight * scale;
  return { left: (elementWidth - width) / 2, top: (elementHeight - height) / 2, width, height };
}

export function overlayRect(box: NormalizedBox, rect: { left: number; top: number; width: number; height: number }) {
  const [ymin, xmin, ymax, xmax] = box;
  return {
    left: rect.left + (xmin / 1000) * rect.width,
    top: rect.top + (ymin / 1000) * rect.height,
    width: ((xmax - xmin) / 1000) * rect.width,
    height: ((ymax - ymin) / 1000) * rect.height,
  };
}

export function readTrackingOverlayPreference(storage: Pick<Storage, "getItem"> | undefined = globalThis.localStorage): boolean {
  try {
    return storage?.getItem(TRACKING_OVERLAY_STORAGE_KEY) === "on";
  } catch {
    return false;
  }
}

export function writeTrackingOverlayPreference(enabled: boolean, storage: Pick<Storage, "setItem"> | undefined = globalThis.localStorage) {
  try {
    storage?.setItem(TRACKING_OVERLAY_STORAGE_KEY, enabled ? "on" : "off");
  } catch {
    // Preference persistence is best-effort.
  }
}
