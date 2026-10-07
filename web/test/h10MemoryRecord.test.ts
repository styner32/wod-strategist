import type { SensorTimelineData } from '../src/api/history';
import {
  compareH10MemoryWithTimeline,
  h10MemoryToRenderableSamples,
  h10MemoryValueAtTime,
  summarizeH10Memory,
} from '../src/history/h10MemoryRecord';

describe('summarizeH10Memory', () => {
  it('summarizes samples, timing, offline ranges and errors from the uploaded record', () => {
    const s = summarizeH10Memory({
      status: 'complete', base_epoch_ms: 1000, recording_interval_ms: 1000,
      start: { sent_epoch_ms: 1400, ack_epoch_ms: 1500 },
      stop: { recording_on_before_stop: true },
      fetch: { attempts: 2, started_epoch_ms: 5000, completed_epoch_ms: 5600, entries: ['/WOD/SAMPLES.BPB'], entry_path: '/WOD/SAMPLES.BPB', bytes: 12 },
      hr_samples: [90, 0, 110, 100], hr_offline: [{ start_index: 1, stop_index: 1 }],
      transport: { packet_size: 182, write_mode: 'without_response' }, device_name: 'Polar H10 1',
      leftovers: [{ path: '/OLD/SAMPLES.BPB', action: 'recovered' }], sensor_removed: true,
      errors: [{ stage: 'fetch_attempt', code: 'BLE_201', message: 'disconnected', at_epoch_ms: 5200 }],
    });
    expect(s).toMatchObject({
      status: 'complete', sampleCount: 4, intervalMs: 1000, durationMs: 4000,
      startAckOffsetMs: 500, startRoundTripMs: 100, min: 90, max: 110, avg: 100, zeroCount: 1,
      offlineRanges: [{ start: 1, stop: 1 }], attempts: 2, fetchMs: 600, bytes: 12,
      transport: 'packet 182B · without_response', leftovers: ['/OLD/SAMPLES.BPB → recovered'],
      sensorRemoved: true, recordingOnBeforeStop: true,
      errors: [{ stage: 'fetch_attempt', code: 'BLE_201', message: 'disconnected', atEpochMs: 5200 }],
    });
  });

  it('tolerates an error-only or malformed record', () => {
    const s = summarizeH10Memory({ status: 'error', hr_samples: 'bad', errors: [{ stage: 'start', code: 'NO_DEVICE' }] });
    expect(s).toMatchObject({ status: 'error', sampleCount: 0, min: null, durationMs: null, startAckOffsetMs: null, sensorRemoved: false });
    expect(s.errors[0]).toMatchObject({ stage: 'start', code: 'NO_DEVICE', atEpochMs: null });
    expect(summarizeH10Memory(null).status).toBe('unknown');
  });
});

describe('h10Memory timeline integration', () => {
  const sampleRecord = {
    schema_version: 1,
    base_epoch_ms: 10000,
    start: { sent_epoch_ms: 11500, ack_epoch_ms: 12000 },
    recording_interval_ms: 1000,
    hr_samples: [100, 105, 0, 110, 115],
  };

  it('converts H10 memory HR samples aligned to capture clock and filters zeros', () => {
    // startOffsetMs = 12000 - 10000 = 2000 ms.
    // sample 0: [2000, 3000] -> center 2500, bpm 100
    // sample 1: [3000, 4000] -> center 3500, bpm 105
    // sample 2: [4000, 5000] -> center 4500, bpm 0 -> null
    // sample 3: [5000, 6000] -> center 5500, bpm 110
    // sample 4: [6000, 7000] -> center 6500, bpm 115
    const samples = h10MemoryToRenderableSamples(sampleRecord, 0, 10000, 100);
    expect(samples.map(s => ({ xMs: s.xMs, value: s.value }))).toEqual([
      { xMs: 2500, value: 100 },
      { xMs: 3500, value: 105 },
      { xMs: 4500, value: null },
      { xMs: 5500, value: 110 },
      { xMs: 6500, value: 115 },
    ]);
  });

  it('looks up H10 memory HR value at targetMs in O(1)', () => {
    // startOffsetMs = 2000 ms
    expect(h10MemoryValueAtTime(sampleRecord, 1500)).toEqual({ value: null, status: 'missing' });
    expect(h10MemoryValueAtTime(sampleRecord, 2500)).toEqual({ value: 100, status: 'valid' });
    expect(h10MemoryValueAtTime(sampleRecord, 3500)).toEqual({ value: 105, status: 'valid' });
    expect(h10MemoryValueAtTime(sampleRecord, 4500)).toEqual({ value: null, status: 'missing' });
    expect(h10MemoryValueAtTime(sampleRecord, 6500)).toEqual({ value: 115, status: 'valid' });
    expect(h10MemoryValueAtTime(sampleRecord, 8000)).toEqual({ value: null, status: 'missing' });
  });

  it('compares H10 memory recording with timeline to calculate error and gap-fill', () => {
    const mockTimeline: SensorTimelineData = {
      schema_version: 1,
      clock: 'capture_clock',
      bucket_ms: 1000,
      duration_ms: 10000,
      source: { sensor_version: '1', request_id: 'r', source_generation: '1', hr_calculation_version: 2 },
      points: [
        // at 2500ms: timeline is 98, h10 is 100 -> diff 2
        { start_ms: 2000, end_ms: 3000, heart_rate_bpm: { value: 98, status: 'valid' }, acc_magnitude_std_g: { value: 0.1, status: 'valid' } },
        // at 3500ms: timeline is 105, h10 is 105 -> diff 0
        { start_ms: 3000, end_ms: 4000, heart_rate_bpm: { value: 105, status: 'valid' }, acc_magnitude_std_g: { value: 0.1, status: 'valid' } },
        // at 4500ms: h10 is 0 (missing)
        { start_ms: 4000, end_ms: 5000, heart_rate_bpm: { value: 108, status: 'valid' }, acc_magnitude_std_g: { value: 0.1, status: 'valid' } },
        // at 5500ms: timeline is missing/gap, h10 is 110 -> gap filled!
        { start_ms: 5000, end_ms: 6000, heart_rate_bpm: { value: null, status: 'missing' }, acc_magnitude_std_g: { value: 0.1, status: 'valid' } },
        // at 6500ms: timeline is 114, h10 is 115 -> diff 1
        { start_ms: 6000, end_ms: 7000, heart_rate_bpm: { value: 114, status: 'valid' }, acc_magnitude_std_g: { value: 0.1, status: 'valid' } },
      ],
      gaps: [{ start_ms: 5000, end_ms: 6000, channel: 'heart_rate', reason: 'dropout' }],
      pauses: [],
    };

    const stats = compareH10MemoryWithTimeline(sampleRecord, mockTimeline);
    expect(stats).toEqual({
      hasData: true,
      totalMemorySamples: 5,
      matchedCount: 3, // samples at index 0, 1, 4 (diffs: 2, 0, 1)
      meanAbsDiff: 1.0, // (2 + 0 + 1) / 3 = 1.0
      maxAbsDiff: 2,
      gapFilledSeconds: 1, // sample at index 3 (5500ms) filled the gap
    });
  });
});

