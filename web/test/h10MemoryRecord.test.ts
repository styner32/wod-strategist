import { summarizeH10Memory } from '../src/history/h10MemoryRecord';

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
