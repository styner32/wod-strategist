import { arrayValue, numberValue, object, strings, textValue } from './onDeviceRecord';
import type { SensorTimelineData } from '../api/history';
import { decimateRenderableSamples, type RenderableSample, timelineValueAtTime } from './timelineUtils';

/** Uploaded by the mobile app to videos/{profileId}/{sessionId}/ (experimental, phase 1). */
export const H10_MEMORY_FILENAME = 'h10_memory_hr.json';

export interface H10MemoryError { stage: string; code: string; message: string; atEpochMs: number | null }
export interface H10MemorySummary {
  status: string;
  sampleCount: number;
  intervalMs: number | null;
  /** Sample 0 is assumed to start at the start ack; offset from the workout base time. */
  startAckOffsetMs: number | null;
  startRoundTripMs: number | null;
  durationMs: number | null;
  min: number | null;
  max: number | null;
  avg: number | null;
  zeroCount: number;
  offlineRanges: { start: number; stop: number }[];
  entries: string[];
  entryPath: string;
  attempts: number | null;
  fetchMs: number | null;
  bytes: number | null;
  deviceName: string;
  transport: string;
  leftovers: string[];
  sensorRemoved: boolean;
  recoveredLater: boolean;
  recordingOnBeforeStop: boolean | null;
  errors: H10MemoryError[];
}

/** Defensive view over the raw record; unknown or missing fields become null/empty. */
export function summarizeH10Memory(value: unknown): H10MemorySummary {
  const r = object(value);
  const start = object(r.start);
  const stop = object(r.stop);
  const fetch = object(r.fetch);
  const transport = object(r.transport);
  const samples = arrayValue(r.hr_samples).map(numberValue).filter((n): n is number => n !== null);
  const valid = samples.filter(n => n > 0);
  const intervalMs = numberValue(r.recording_interval_ms);
  const base = numberValue(r.base_epoch_ms);
  const ack = numberValue(start.ack_epoch_ms);
  const sent = numberValue(start.sent_epoch_ms);
  const fetchStart = numberValue(fetch.started_epoch_ms);
  const fetchEnd = numberValue(fetch.completed_epoch_ms);
  return {
    status: textValue(r.status) || 'unknown',
    sampleCount: samples.length,
    intervalMs,
    startAckOffsetMs: base !== null && ack !== null ? ack - base : null,
    startRoundTripMs: sent !== null && ack !== null ? ack - sent : null,
    durationMs: intervalMs !== null ? samples.length * intervalMs : null,
    min: valid.length ? Math.min(...valid) : null,
    max: valid.length ? Math.max(...valid) : null,
    avg: valid.length ? Math.round(valid.reduce((sum, n) => sum + n, 0) / valid.length) : null,
    zeroCount: samples.length - valid.length,
    offlineRanges: arrayValue(r.hr_offline).map(item => {
      const range = object(item);
      return { start: numberValue(range.start_index) ?? numberValue(range.startIndex) ?? 0, stop: numberValue(range.stop_index) ?? numberValue(range.stopIndex) ?? 0 };
    }),
    entries: strings(fetch.entries),
    entryPath: textValue(fetch.entry_path),
    attempts: numberValue(fetch.attempts),
    fetchMs: fetchStart !== null && fetchEnd !== null ? fetchEnd - fetchStart : null,
    bytes: numberValue(fetch.bytes),
    deviceName: textValue(r.device_name),
    transport: [numberValue(transport.packet_size) !== null ? `packet ${numberValue(transport.packet_size)}B` : '', textValue(transport.write_mode)].filter(Boolean).join(' · '),
    leftovers: arrayValue(r.leftovers).map(item => { const l = object(item); return `${textValue(l.path)} → ${textValue(l.action)}`; }),
    sensorRemoved: r.sensor_removed === true,
    recoveredLater: r.recovered_later === true,
    recordingOnBeforeStop: typeof stop.recording_on_before_stop === 'boolean' ? stop.recording_on_before_stop : null,
    errors: arrayValue(r.errors).map(item => {
      const e = object(item);
      return { stage: textValue(e.stage), code: textValue(e.code), message: textValue(e.message), atEpochMs: numberValue(e.at_epoch_ms) };
    }),
  };
}

export interface ParsedH10Memory {
  valid: boolean;
  intervalMs: number;
  startOffsetMs: number;
  samples: (number | null)[];
  errorReason?: 'invalid_interval' | 'invalid_offset' | 'no_samples';
}

function isParsedH10Memory(v: unknown): v is ParsedH10Memory {
  return typeof v === 'object' && v !== null && 'valid' in v && 'samples' in v && 'intervalMs' in v;
}

/** Parses and validates raw H10 internal memory data into aligned samples and timing offsets. */
export function parseH10MemoryData(value: unknown): ParsedH10Memory {
  const r = object(value);
  const rawSamples = arrayValue(r.hr_samples).map(numberValue);
  if (rawSamples.length === 0) {
    return { valid: false, intervalMs: 1000, startOffsetMs: 0, samples: [], errorReason: 'no_samples' };
  }

  const intervalMs = numberValue(r.recording_interval_ms);
  if (intervalMs === null || intervalMs <= 0 || !Number.isFinite(intervalMs)) {
    return { valid: false, intervalMs: 0, startOffsetMs: 0, samples: rawSamples, errorReason: 'invalid_interval' };
  }

  const base = numberValue(r.base_epoch_ms);
  const start = object(r.start);
  const ack = numberValue(start.ack_epoch_ms) ?? numberValue(start.sent_epoch_ms);
  if (base === null || ack === null || ack < base) {
    return { valid: false, intervalMs, startOffsetMs: 0, samples: rawSamples, errorReason: 'invalid_offset' };
  }

  return {
    valid: true,
    intervalMs,
    startOffsetMs: ack - base,
    samples: rawSamples,
  };
}

/** Converts H10 internal memory HR samples into renderable chart samples aligned to the capture clock. */
export function h10MemoryToRenderableSamples(
  valueOrParsed: ParsedH10Memory | unknown,
  visibleStartMs: number,
  visibleEndMs: number,
  targetWidthPx = 600,
): RenderableSample[] {
  const parsed = isParsedH10Memory(valueOrParsed) ? valueOrParsed : parseH10MemoryData(valueOrParsed);
  if (!parsed.valid || parsed.samples.length === 0) return [];

  const { samples, intervalMs, startOffsetMs } = parsed;
  const raw: RenderableSample[] = [];

  for (let i = 0; i < samples.length; i++) {
    const sStart = startOffsetMs + i * intervalMs;
    const sEnd = sStart + intervalMs;
    if (sEnd <= visibleStartMs || sStart >= visibleEndMs) {
      continue;
    }
    const bpm = samples[i];
    const isValid = bpm !== null && bpm > 0 && Number.isFinite(bpm);
    raw.push({
      xMs: (sStart + sEnd) / 2,
      value: isValid ? bpm : null,
      status: isValid ? 'valid' : 'missing',
    });
  }

  return decimateRenderableSamples(raw, visibleStartMs, visibleEndMs, targetWidthPx);
}

export type H10MemoryTimelineValue =
  | { value: number; status: 'valid' }
  | { value: null; status: 'missing' };

/** Looks up the H10 memory HR reading at a given capture-clock timestamp in O(1). */
export function h10MemoryValueAtTime(
  valueOrParsed: ParsedH10Memory | unknown,
  targetMs: number,
): H10MemoryTimelineValue {
  if (!Number.isFinite(targetMs)) return { value: null, status: 'missing' };
  const parsed = isParsedH10Memory(valueOrParsed) ? valueOrParsed : parseH10MemoryData(valueOrParsed);
  if (!parsed.valid || parsed.samples.length === 0) return { value: null, status: 'missing' };

  const { samples, intervalMs, startOffsetMs } = parsed;
  if (targetMs < startOffsetMs) return { value: null, status: 'missing' };
  const index = Math.floor((targetMs - startOffsetMs) / intervalMs);
  if (index < 0 || index >= samples.length) {
    return { value: null, status: 'missing' };
  }
  const bpm = samples[index];
  if (bpm !== null && bpm > 0 && Number.isFinite(bpm)) {
    return { value: bpm, status: 'valid' };
  }
  return { value: null, status: 'missing' };
}

export interface H10ComparisonStats {
  hasData: boolean;
  totalMemorySamples: number;
  matchedCount: number;
  meanAbsDiff: number | null;
  maxAbsDiff: number | null;
  gapFilledSeconds: number;
}

/** Compares H10 memory HR with the processed live BLE sensor timeline points. */
export function compareH10MemoryWithTimeline(
  valueOrParsed: ParsedH10Memory | unknown,
  timeline: SensorTimelineData | null | undefined,
): H10ComparisonStats | null {
  if (!timeline || !valueOrParsed) return null;
  const parsed = isParsedH10Memory(valueOrParsed) ? valueOrParsed : parseH10MemoryData(valueOrParsed);
  if (!parsed.valid || parsed.samples.length === 0) return null;

  const { samples, intervalMs, startOffsetMs } = parsed;
  let matchedCount = 0;
  let sumDiff = 0;
  let maxAbsDiff: number | null = null;
  let gapFilledCount = 0;

  for (let i = 0; i < samples.length; i++) {
    const bpm = samples[i];
    if (bpm === null || bpm <= 0 || !Number.isFinite(bpm)) continue;
    const centerMs = startOffsetMs + i * intervalMs + intervalMs / 2;
    // Only evaluate within active timeline span [0, timeline.duration_ms]
    if (centerMs < 0 || centerMs > timeline.duration_ms) continue;

    const tlVal = timelineValueAtTime(timeline, 'heart_rate_bpm', centerMs);
    if (tlVal.status === 'valid' && tlVal.value !== null) {
      matchedCount++;
      const diff = Math.abs(bpm - tlVal.value);
      sumDiff += diff;
      if (maxAbsDiff === null || diff > maxAbsDiff) {
        maxAbsDiff = diff;
      }
    } else if (tlVal.status === 'missing') {
      // Exclude paused intervals; only count actual missing/dropout samples
      gapFilledCount++;
    }
  }

  return {
    hasData: true,
    totalMemorySamples: samples.length,
    matchedCount,
    meanAbsDiff: matchedCount > 0 ? Number((sumDiff / matchedCount).toFixed(2)) : null,
    maxAbsDiff: maxAbsDiff !== null ? Number(maxAbsDiff.toFixed(2)) : null,
    gapFilledSeconds: Math.round((gapFilledCount * intervalMs) / 1000),
  };
}
