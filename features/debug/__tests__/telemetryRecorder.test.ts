/**
 * Unit tests for TelemetryRecorder.
 *
 * Mocks:
 * - expo-battery: getBatteryLevelAsync → 0.85
 * - expo-device: modelName → 'TestDevice'
 * - expo-constants: expoConfig.version → '1.0.0-test'
 * - expo-file-system: in-memory stub
 */

// --- Mocks (must be declared before imports) ---

const mockWriteAsStringAsync = jest.fn().mockResolvedValue(undefined);
const mockGetInfoAsync = jest.fn().mockResolvedValue({ exists: true });
const mockMakeDirectoryAsync = jest.fn().mockResolvedValue(undefined);

jest.mock('expo-file-system', () => ({
  documentDirectory: '/mock/docs/',
  writeAsStringAsync: (...args: unknown[]) => mockWriteAsStringAsync(...args),
  getInfoAsync: (...args: unknown[]) => mockGetInfoAsync(...args),
  makeDirectoryAsync: (...args: unknown[]) => mockMakeDirectoryAsync(...args),
}));

jest.mock('expo-battery', () => ({
  getBatteryLevelAsync: jest.fn().mockResolvedValue(0.85),
}));

jest.mock('expo-constants', () => ({
  __esModule: true,
  default: {
    expoConfig: { version: '1.0.0-test' },
  },
}));

jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
}));

// --- Imports ---

import { TelemetryRecorder } from '../telemetryRecorder';

// --- Helpers ---

function advanceTimersBySeconds(s: number) {
  jest.advanceTimersByTime(s * 1000);
}

// --- Tests ---

beforeEach(() => {
  jest.useFakeTimers();
  jest.clearAllMocks();
  // Ensure recorder is stopped between tests
  if (TelemetryRecorder.isActive()) {
    // Force-clear internal state by stopping
    TelemetryRecorder.stop();
  }
});

afterEach(() => {
  jest.useRealTimers();
});

describe('TelemetryRecorder', () => {
  it('starts and reports isActive correctly', () => {
    expect(TelemetryRecorder.isActive()).toBe(false);
    TelemetryRecorder.start('session-1', 42);
    expect(TelemetryRecorder.isActive()).toBe(true);
  });

  it('double-start is a no-op', () => {
    TelemetryRecorder.start('session-1', 42);
    TelemetryRecorder.start('session-2', 99); // should be ignored
    expect(TelemetryRecorder.isActive()).toBe(true);
  });

  it('stop when not active returns null', async () => {
    const result = await TelemetryRecorder.stop();
    expect(result).toBeNull();
  });

  it('collects samples at 1Hz with provider data', async () => {
    TelemetryRecorder.start('session-collect', 10);
    TelemetryRecorder.registerProvider('hr', () => ({ hr: 140 }));
    TelemetryRecorder.registerProvider('chunk', () => ({ chunkIdx: 3 }));

    // Advance 3 seconds to collect ~3 samples
    advanceTimersBySeconds(3);

    const result = await TelemetryRecorder.stop();
    expect(result).not.toBeNull();
    expect(result!.sessionId).toBe('session-collect');

    // Verify file was written
    expect(mockWriteAsStringAsync).toHaveBeenCalledTimes(1);
    const [filePath, jsonStr] = mockWriteAsStringAsync.mock.calls[0];
    expect(filePath).toContain('session-collect.json');

    const session = JSON.parse(jsonStr);
    expect(session.sessionId).toBe('session-collect');
    expect(session.profileId).toBe(10);
    expect(session.platform).toBe('ios');
    expect(session.deviceModel).toBe('ios-device');
    expect(session.appVersion).toBe('1.0.0-test');
    expect(session.samples.length).toBeGreaterThanOrEqual(3);

    // Check sample shape
    const sample = session.samples[0];
    expect(sample.ts).toBeGreaterThanOrEqual(0);
    expect(sample.hr).toBe(140);
    expect(sample.chunkIdx).toBe(3);
  });

  it('ts field increments across samples', async () => {
    TelemetryRecorder.start('session-ts', 1);

    advanceTimersBySeconds(5);

    const result = await TelemetryRecorder.stop();
    expect(result).not.toBeNull();

    const session = JSON.parse(mockWriteAsStringAsync.mock.calls[0][1]);
    const timestamps = session.samples.map((s: { ts: number }) => s.ts);

    // Timestamps should be monotonically increasing
    for (let i = 1; i < timestamps.length; i++) {
      expect(timestamps[i]).toBeGreaterThan(timestamps[i - 1]);
    }
  });

  it('providers are called on each tick', async () => {
    const mockProvider = jest.fn().mockReturnValue({ hr: 99 });

    TelemetryRecorder.start('session-provider', 1);
    TelemetryRecorder.registerProvider('test', mockProvider);

    advanceTimersBySeconds(3);

    expect(mockProvider).toHaveBeenCalledTimes(3);

    await TelemetryRecorder.stop();
  });

  it('unregisterProvider stops calling the provider', async () => {
    const mockProvider = jest.fn().mockReturnValue({ hr: 88 });

    TelemetryRecorder.start('session-unreg', 1);
    TelemetryRecorder.registerProvider('temp', mockProvider);

    advanceTimersBySeconds(2);
    TelemetryRecorder.unregisterProvider('temp');
    advanceTimersBySeconds(2);

    // Called only during the first 2 ticks
    expect(mockProvider).toHaveBeenCalledTimes(2);

    await TelemetryRecorder.stop();
  });

  it('battery is included from cache', async () => {
    TelemetryRecorder.start('session-batt', 1);

    // Battery mock returns 0.85 — the recorder primes cache on start
    // Let battery cache be primed (async), then tick
    await Promise.resolve(); // flush microtasks
    advanceTimersBySeconds(1);

    const result = await TelemetryRecorder.stop();
    expect(result).not.toBeNull();

    const session = JSON.parse(mockWriteAsStringAsync.mock.calls[0][1]);
    // At least one sample should have batt
    const withBatt = session.samples.filter((s: { batt?: number }) => s.batt !== undefined);
    expect(withBatt.length).toBeGreaterThanOrEqual(1);
    expect(withBatt[0].batt).toBe(0.85);
  });

  it('handles provider errors gracefully', async () => {
    TelemetryRecorder.start('session-err', 1);
    TelemetryRecorder.registerProvider('broken', () => {
      throw new Error('provider exploded');
    });
    TelemetryRecorder.registerProvider('ok', () => ({ hr: 100 }));

    advanceTimersBySeconds(1);

    const result = await TelemetryRecorder.stop();
    expect(result).not.toBeNull();

    const session = JSON.parse(mockWriteAsStringAsync.mock.calls[0][1]);
    // The good provider's data should still be present
    expect(session.samples[0].hr).toBe(100);
  });

  it('creates debug directory if it does not exist', async () => {
    mockGetInfoAsync.mockResolvedValueOnce({ exists: false });

    TelemetryRecorder.start('session-mkdir', 1);
    advanceTimersBySeconds(1);
    await TelemetryRecorder.stop();

    expect(mockMakeDirectoryAsync).toHaveBeenCalledWith(
      '/mock/docs/debug/',
      { intermediates: true },
    );
  });

  it('clears providers on stop', async () => {
    const mockProvider = jest.fn().mockReturnValue({ hr: 77 });

    TelemetryRecorder.start('session-clear', 1);
    TelemetryRecorder.registerProvider('test', mockProvider);
    advanceTimersBySeconds(1);
    await TelemetryRecorder.stop();

    // Start a new session — provider should NOT be called
    TelemetryRecorder.start('session-clear-2', 1);
    advanceTimersBySeconds(1);

    // Provider was called once in first session, zero in second
    expect(mockProvider).toHaveBeenCalledTimes(1);

    await TelemetryRecorder.stop();
  });
});
