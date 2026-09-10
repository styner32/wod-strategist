import type { SensorTimelinePoint, VideoMappingSegment } from "../api/history";

/**
 * Maps a workout capture-clock timestamp (in milliseconds) to merged video media time (in seconds).
 * Returns null if the capture time does not fall into any verified video segment.
 */
export function captureToMedia(
  captureMs: number,
  segments: VideoMappingSegment[],
): number | null {
  if (!segments || segments.length === 0 || !Number.isFinite(captureMs)) {
    return null;
  }

  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i];
    const isFinal = i === segments.length - 1;
    const inRange = isFinal
      ? captureMs >= seg.capture_start_ms && captureMs <= seg.capture_end_ms
      : captureMs >= seg.capture_start_ms && captureMs < seg.capture_end_ms;

    if (inRange) {
      const captureDur = seg.capture_end_ms - seg.capture_start_ms;
      const mediaDur = seg.media_end_ms - seg.media_start_ms;
      if (captureDur <= 0 || mediaDur <= 0) {
        return null;
      }
      const offsetMs = (captureMs - seg.capture_start_ms) * (mediaDur / captureDur);
      const targetMediaMs = seg.media_start_ms + offsetMs;
      return targetMediaMs / 1000.0;
    }
  }

  return null;
}

/**
 * Maps a merged video media-clock timestamp (in seconds) to workout capture time (in milliseconds).
 * Returns null if the media time does not fall into any verified video segment.
 */
export function mediaToCapture(
  mediaSec: number,
  segments: VideoMappingSegment[],
): number | null {
  if (!segments || segments.length === 0 || !Number.isFinite(mediaSec)) {
    return null;
  }

  const mediaMs = mediaSec * 1000.0;

  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i];
    const isFinal = i === segments.length - 1;
    const inRange = isFinal
      ? mediaMs >= seg.media_start_ms && mediaMs <= seg.media_end_ms
      : mediaMs >= seg.media_start_ms && mediaMs < seg.media_end_ms;

    if (inRange) {
      const captureDur = seg.capture_end_ms - seg.capture_start_ms;
      const mediaDur = seg.media_end_ms - seg.media_start_ms;
      if (captureDur <= 0 || mediaDur <= 0) {
        return null;
      }
      const offsetMs = (mediaMs - seg.media_start_ms) * (captureDur / mediaDur);
      return seg.capture_start_ms + offsetMs;
    }
  }

  return null;
}

/**
 * Format milliseconds into mm:ss (or hh:mm:ss if >= 1 hour).
 */
export function formatTimelineTime(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) {
    return "00:00";
  }
  const totalSeconds = Math.floor(ms / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  const mm = String(minutes).padStart(2, "0");
  const ss = String(seconds).padStart(2, "0");

  if (hours > 0) {
    return `${hours}:${mm}:${ss}`;
  }
  return `${mm}:${ss}`;
}

export interface RenderableSample {
  xMs: number;
  value: number | null;
  status: string;
}

/**
 * Decimates high-density 1s points for display over pixel width, preserving peaks,
 * valleys, and gap/null boundaries.
 */
export function decimatePoints(
  points: SensorTimelinePoint[],
  channel: "heart_rate_bpm" | "acc_magnitude_std_g",
  visibleStartMs: number,
  visibleEndMs: number,
  targetWidthPx = 600,
): RenderableSample[] {
  const visible = points.filter(
    (p) => p.end_ms >= visibleStartMs && p.start_ms <= visibleEndMs,
  );

  if (visible.length <= targetWidthPx * 2) {
    return visible.map((p) => {
      const valObj = p[channel];
      return {
        xMs: (p.start_ms + p.end_ms) / 2,
        value: valObj.status === "valid" ? valObj.value : null,
        status: valObj.status,
      };
    });
  }

  // Binning to preserve extrema and gap edges
  const binDurationMs = (visibleEndMs - visibleStartMs) / targetWidthPx;
  const result: RenderableSample[] = [];

  let currentBin = 0;
  let binMin: { sample: RenderableSample; val: number } | null = null;
  let binMax: { sample: RenderableSample; val: number } | null = null;
  let binFirst: RenderableSample | null = null;
  let binLast: RenderableSample | null = null;

  const flushBin = () => {
    if (!binFirst) return;
    const candidates = [binFirst];
    if (binMin && binMin.sample !== binFirst && binMin.sample !== binLast) {
      candidates.push(binMin.sample);
    }
    if (binMax && binMax.sample !== binFirst && binMax.sample !== binLast && binMax.sample !== binMin?.sample) {
      candidates.push(binMax.sample);
    }
    if (binLast && binLast !== binFirst) {
      candidates.push(binLast);
    }
    candidates.sort((a, b) => a.xMs - b.xMs);
    result.push(...candidates);

    binMin = null;
    binMax = null;
    binFirst = null;
    binLast = null;
  };

  for (const p of visible) {
    const xMs = (p.start_ms + p.end_ms) / 2;
    const bin = Math.floor((xMs - visibleStartMs) / binDurationMs);
    if (bin !== currentBin) {
      flushBin();
      currentBin = bin;
    }

    const valObj = p[channel];
    const val = valObj.status === "valid" ? valObj.value : null;
    const sample: RenderableSample = {
      xMs,
      value: val,
      status: valObj.status,
    };

    if (!binFirst) binFirst = sample;
    binLast = sample;

    if (val !== null) {
      if (!binMin || val < binMin.val) binMin = { sample, val };
      if (!binMax || val > binMax.val) binMax = { sample, val };
    }
  }
  flushBin();

  return result;
}

/**
 * Builds SVG path data (d attribute) from samples, breaking lines wherever values are null.
 */
export function buildSvgLinePath(
  samples: RenderableSample[],
  xScale: (ms: number) => number,
  yScale: (val: number) => number,
): string {
  let path = "";
  let inSegment = false;

  for (let i = 0; i < samples.length; i++) {
    const s = samples[i];
    if (s.value === null || !Number.isFinite(s.value)) {
      inSegment = false;
      continue;
    }

    const x = xScale(s.xMs);
    const y = yScale(s.value);

    if (!inSegment) {
      path += `M ${x.toFixed(1)} ${y.toFixed(1)} `;
      inSegment = true;
    } else {
      path += `L ${x.toFixed(1)} ${y.toFixed(1)} `;
    }
  }

  return path.trim();
}
