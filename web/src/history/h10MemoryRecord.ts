import { arrayValue, numberValue, object, strings, textValue } from './onDeviceRecord';

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
