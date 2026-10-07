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

/** Converts H10 internal memory HR samples into renderable chart samples aligned to the capture clock. */
export function h10MemoryToRenderableSamples(
  value: unknown,
  visibleStartMs: number,
  visibleEndMs: number,
  targetWidthPx = 600,
): RenderableSample[] {
  const r = object(value);
  const samples = arrayValue(r.hr_samples).map(numberValue);
  if (samples.length === 0) return [];

  const intervalMs = numberValue(r.recording_interval_ms) ?? 1000;
  const base = numberValue(r.base_epoch_ms);
  const start = object(r.start);
  const ack = numberValue(start.ack_epoch_ms) ?? numberValue(start.sent_epoch_ms);
  const startOffsetMs = base !== null && ack !== null ? Math.max(0, ack - base) : 0;

  const raw: RenderableSample[] = [];
  let previousEnd: number | undefined;

  for (let i = 0; i < samples.length; i++) {
    const sStart = startOffsetMs + i * intervalMs;
    const sEnd = sStart + intervalMs;
    if (sEnd <= visibleStartMs || sStart >= visibleEndMs) {
      continue;
    }
    if (previousEnd !== undefined && sStart > previousEnd) {
      raw.push({ xMs: (previousEnd + sStart) / 2, value: null, status: 'missing' });
    }
    const bpm = samples[i];
    const isValid = bpm !== null && bpm > 0 && Number.isFinite(bpm);
    raw.push({
      xMs: (sStart + sEnd) / 2,
      value: isValid ? bpm : null,
      status: isValid ? 'valid' : 'missing',
    });
    previousEnd = sEnd;
  }

  return decimateRenderableSamples(raw, visibleStartMs, visibleEndMs, targetWidthPx);
}

export type H10MemoryTimelineValue =
  | { value: number; status: 'valid' }
  | { value: null; status: 'missing' };

/** Looks up the H10 memory HR reading at a given capture-clock timestamp in O(1). */
export function h10MemoryValueAtTime(
  value: unknown,
  targetMs: number,
): H10MemoryTimelineValue {
  if (!Number.isFinite(targetMs)) return { value: null, status: 'missing' };
  const r = object(value);
  const samples = arrayValue(r.hr_samples).map(numberValue);
  if (samples.length === 0) return { value: null, status: 'missing' };

  const intervalMs = numberValue(r.recording_interval_ms) ?? 1000;
  const base = numberValue(r.base_epoch_ms);
  const start = object(r.start);
  const ack = numberValue(start.ack_epoch_ms) ?? numberValue(start.sent_epoch_ms);
  const startOffsetMs = base !== null && ack !== null ? Math.max(0, ack - base) : 0;

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
  value: unknown,
  timeline: SensorTimelineData | null | undefined,
): H10ComparisonStats | null {
  if (!timeline || !value) return null;
  const r = object(value);
  const samples = arrayValue(r.hr_samples).map(numberValue);
  if (samples.length === 0) return null;

  const intervalMs = numberValue(r.recording_interval_ms) ?? 1000;
  const base = numberValue(r.base_epoch_ms);
  const start = object(r.start);
  const ack = numberValue(start.ack_epoch_ms) ?? numberValue(start.sent_epoch_ms);
  const startOffsetMs = base !== null && ack !== null ? Math.max(0, ack - base) : 0;

  let matchedCount = 0;
  let sumDiff = 0;
  let maxAbsDiff: number | null = null;
  let gapFilledCount = 0;

  for (let i = 0; i < samples.length; i++) {
    const bpm = samples[i];
    if (bpm === null || bpm <= 0 || !Number.isFinite(bpm)) continue;
    const centerMs = startOffsetMs + i * intervalMs + intervalMs / 2;
    const tlVal = timelineValueAtTime(timeline, 'heart_rate_bpm', centerMs);
    if (tlVal.status === 'valid' && tlVal.value !== null) {
      matchedCount++;
      const diff = Math.abs(bpm - tlVal.value);
      sumDiff += diff;
      if (maxAbsDiff === null || diff > maxAbsDiff) {
        maxAbsDiff = diff;
      }
    } else {
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
