import type {
  ChunkAnalysisResult,
  SensorTimelineData,
  SensorTimelinePoint,
  SensorTimelineResponse,
  TimelineGap,
  TimelineValue,
  VideoMappingSegment,
} from "../api/history";

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

/** Find only the containing bucket; missing time must never snap to another sample. */
export function findTimelinePoint(
  points: SensorTimelinePoint[],
  targetMs: number,
  durationMs: number,
): SensorTimelinePoint | null {
  if (!Number.isFinite(targetMs)) return null;
  let low = 0;
  let high = points.length - 1;
  while (low <= high) {
    const mid = Math.floor((low + high) / 2);
    const point = points[mid];
    const atSessionEnd = targetMs === durationMs && point.end_ms === durationMs;
    if (targetMs >= point.start_ms && (targetMs < point.end_ms || atSessionEnd)) {
      return point;
    }
    if (targetMs < point.start_ms) high = mid - 1;
    else low = mid + 1;
  }
  return null;
}

/** Resolve channel quality independently, including compressed pauses and gaps. */
export function timelineValueAtTime(
  timeline: SensorTimelineData,
  channel: "heart_rate_bpm" | "acc_magnitude_std_g",
  targetMs: number,
): TimelineValue {
  const contains = (interval: { start_ms: number; end_ms: number }) =>
    targetMs >= interval.start_ms &&
    (targetMs < interval.end_ms ||
      (targetMs === timeline.duration_ms && interval.end_ms === timeline.duration_ms));
  if (timeline.pauses.some(contains)) return { value: null, status: "paused" };
  const gap = timeline.gaps.find((gap) =>
    (gap.channel === "both" || gap.channel === channelName(channel)) && contains(gap),
  );
  if (gap) return { value: null, status: "missing", reason: gap.reason };
  return findTimelinePoint(timeline.points, targetMs, timeline.duration_ms)?.[channel] ?? {
    value: null,
    status: "missing",
  };
}

function channelName(channel: "heart_rate_bpm" | "acc_magnitude_std_g") {
  return channel === "heart_rate_bpm" ? "heart_rate" : "acc";
}

export const SENSOR_TIMELINE_POLL_LIMIT_MS = 5 * 60 * 1000;

/** Bound automatic waits; a missing video can be intentional or never become ready. */
export function sensorTimelinePollInterval(
  response: SensorTimelineResponse | undefined,
  videoReady: boolean,
  elapsedMs: number,
  hasError: boolean,
): number | false {
  if (hasError || elapsedMs >= SENSOR_TIMELINE_POLL_LIMIT_MS) return false;
  if (response?.status === "pending") return 5000;
  if (response?.timeline && (response.status === "completed" || response.status === "limited") &&
      (!videoReady || response.video_mapping.segments.length === 0)) return 5000;
  return false;
}

/**
 * Video/chunk changes refresh the mapping even after sensor processing completes.
 * Only mapping inputs enter the key, so sensor responses cannot trigger a fetch loop.
 */
export function sensorTimelineQueryKey(
  sessionId: string | undefined,
  profileId: number | undefined,
  chunks: Pick<ChunkAnalysisResult,
    "id" | "start_secs" | "end_secs" | "media_start_secs" | "media_end_secs">[],
  mergedVideoReady: boolean,
) {
  return ["sensor-timeline", sessionId, profileId, {
    mergedVideoReady,
    boundaries: [...chunks].sort((a, b) => a.id - b.id).map((chunk) => [
      chunk.id,
      chunk.start_secs ?? null,
      chunk.end_secs ?? null,
      chunk.media_start_secs ?? null,
      chunk.media_end_secs ?? null,
    ]),
  }] as const;
}

/**
 * Decimates high-density 1s points for display over pixel width, preserving peaks,
 * valleys, and every channel gap/null boundary before grouping valid runs into bins.
 */
export function decimatePoints(
  points: SensorTimelinePoint[],
  channel: "heart_rate_bpm" | "acc_magnitude_std_g",
  visibleStartMs: number,
  visibleEndMs: number,
  targetWidthPx = 600,
  gaps: TimelineGap[] = [],
): RenderableSample[] {
  const visible = points.filter(
    (p) => p.end_ms > visibleStartMs && p.start_ms < visibleEndMs,
  );
  const channelGaps = gaps
    .filter((gap) => gap.channel === "both" || gap.channel === channelName(channel))
    .sort((a, b) => a.start_ms - b.start_ms);
  const samples: RenderableSample[] = [];
  let gapIndex = 0;
  let previousEnd: number | undefined;
  for (const point of visible) {
    // Sparse points omit long missing intervals entirely. Insert a separator before
    // decimation so no line can cross the omitted time, even inside a single pixel.
    if (previousEnd !== undefined && point.start_ms > previousEnd) {
      samples.push({ xMs: (previousEnd + point.start_ms) / 2, value: null, status: "missing" });
    }
    while (gapIndex < channelGaps.length && channelGaps[gapIndex].end_ms <= point.start_ms) {
      gapIndex++;
    }
    const gap = channelGaps[gapIndex];
    const overlapsGap = gap && gap.start_ms < point.end_ms && gap.end_ms > point.start_ms;
    const value = point[channel];
    samples.push({
      xMs: (point.start_ms + point.end_ms) / 2,
      value: !overlapsGap && value.status === "valid" ? value.value : null,
      status: overlapsGap ? "missing" : value.status,
    });
    previousEnd = point.end_ms;
  }

  const width = Math.max(1, Math.floor(targetWidthPx));
  if (samples.length <= width * 2) return samples;

  const binDurationMs = Math.max(1, visibleEndMs - visibleStartMs) / width;
  const result: RenderableSample[] = [];
  let currentBin = -1;
  let binMin: RenderableSample | null = null;
  let binMax: RenderableSample | null = null;
  let binFirst: RenderableSample | null = null;
  let binLast: RenderableSample | null = null;

  const flushBin = () => {
    const candidates = [...new Set([binFirst, binMin, binMax, binLast])]
      .filter((sample): sample is RenderableSample => sample !== null)
      .sort((a, b) => a.xMs - b.xMs);
    result.push(...candidates);
    binMin = null;
    binMax = null;
    binFirst = null;
    binLast = null;
  };

  for (const sample of samples) {
    if (sample.value === null || !Number.isFinite(sample.value)) {
      flushBin();
      result.push(sample);
      currentBin = -1;
      continue;
    }
    const bin = Math.floor((sample.xMs - visibleStartMs) / binDurationMs);
    if (bin !== currentBin) {
      flushBin();
      currentBin = bin;
    }
    if (!binFirst) binFirst = sample;
    binLast = sample;
    if (!binMin || sample.value < binMin.value!) binMin = sample;
    if (!binMax || sample.value > binMax.value!) binMax = sample;
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
