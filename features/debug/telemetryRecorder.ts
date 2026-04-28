/**
 * TelemetryRecorder — module-level singleton that samples device state at 1Hz
 * during a workout recording and writes the collected data to a local JSON file.
 *
 * Lifecycle is event-driven (start/stop), not render-driven.
 * External code registers "providers" that return partial sample data each tick.
 */
import * as Battery from 'expo-battery';
import * as FileSystem from 'expo-file-system';
import Constants from 'expo-constants';
import { Platform } from 'react-native';

import type { TelemetrySample, TelemetrySession } from './types';

// ---------------------------------------------------------------------------
// Internal state
// ---------------------------------------------------------------------------

let samples: TelemetrySample[] = [];
let sessionId = '';
let profileId = 0;
let startedAt = 0;
let sampleInterval: ReturnType<typeof setInterval> | null = null;
let batteryInterval: ReturnType<typeof setInterval> | null = null;
let cachedBattery: number | undefined;
let providers = new Map<string, () => Partial<TelemetrySample>>();

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SAMPLE_RATE_MS = 1000;
const BATTERY_REFRESH_MS = 10_000;

function debugDir(): string {
  return `${FileSystem.documentDirectory}debug/`;
}

async function ensureDebugDir(): Promise<void> {
  const info = await FileSystem.getInfoAsync(debugDir());
  if (!info.exists) {
    await FileSystem.makeDirectoryAsync(debugDir(), { intermediates: true });
  }
}

async function refreshBattery(): Promise<void> {
  try {
    const level = await Battery.getBatteryLevelAsync();
    // getBatteryLevelAsync returns -1 on simulators / unsupported
    cachedBattery = level >= 0 ? Math.round(level * 100) / 100 : undefined;
  } catch {
    cachedBattery = undefined;
  }
}

function collectSample(): TelemetrySample {
  const ts = (Date.now() - startedAt) / 1000;

  // Merge all provider contributions into a single sample
  let merged: Partial<TelemetrySample> = {};
  for (const fn of providers.values()) {
    try {
      Object.assign(merged, fn());
    } catch {
      // Provider threw — skip silently to avoid breaking the sampler
    }
  }

  return {
    ts: Math.round(ts * 100) / 100, // 2 decimal places
    ...(merged.hr !== undefined && merged.hr > 0 ? { hr: merged.hr } : {}),
    ...(cachedBattery !== undefined ? { batt: cachedBattery } : {}),
    ...(merged.chunkIdx !== undefined ? { chunkIdx: merged.chunkIdx } : {}),
    ...(merged.motion ? { motion: merged.motion } : {}),
  };
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export const TelemetryRecorder = {
  /**
   * Start collecting telemetry for a recording session.
   * No-op if already active.
   */
  start(sid: string, pid: number): void {
    if (sampleInterval) return; // already active

    sessionId = sid;
    profileId = pid;
    startedAt = Date.now();
    samples = [];
    cachedBattery = undefined;

    // Prime battery cache immediately
    void refreshBattery();

    // Refresh battery on a slower cadence (every 10s)
    batteryInterval = setInterval(() => {
      void refreshBattery();
    }, BATTERY_REFRESH_MS);

    // Sample at 1Hz
    sampleInterval = setInterval(() => {
      samples.push(collectSample());
    }, SAMPLE_RATE_MS);

    console.log(`📊 Telemetry started for session ${sid}`);
  },

  /**
   * Register a data provider that will be polled every tick.
   * If a provider with the same key exists, it is replaced.
   */
  registerProvider(key: string, fn: () => Partial<TelemetrySample>): void {
    providers.set(key, fn);
  },

  /**
   * Remove a previously registered provider.
   */
  unregisterProvider(key: string): void {
    providers.delete(key);
  },

  /**
   * Stop collecting, write JSON to disk, reset internal state.
   * Returns the file path + session ID on success, or null if not active.
   */
  async stop(): Promise<{ filePath: string; sessionId: string } | null> {
    if (!sampleInterval) return null;

    // Clear timers
    clearInterval(sampleInterval);
    sampleInterval = null;
    if (batteryInterval) {
      clearInterval(batteryInterval);
      batteryInterval = null;
    }

    const endedAt = Date.now();
    const sid = sessionId;

    const session: TelemetrySession = {
      sessionId: sid,
      profileId,
      startedAt,
      endedAt,
      samples,
      appVersion: Constants.expoConfig?.version ?? 'unknown',
      platform: Platform.OS as 'ios' | 'android',
      deviceModel: Constants.expoConfig?.extra?.deviceModel ?? `${Platform.OS}-device`,
    };

    // Reset state
    sessionId = '';
    profileId = 0;
    startedAt = 0;
    samples = [];
    providers.clear();
    cachedBattery = undefined;

    // Write to disk
    try {
      await ensureDebugDir();
      const filePath = `${debugDir()}${sid}.json`;
      await FileSystem.writeAsStringAsync(filePath, JSON.stringify(session));
      console.log(`📊 Telemetry written to ${filePath} (${session.samples.length} samples)`);
      return { filePath, sessionId: sid };
    } catch (e) {
      console.warn('📊 Telemetry write failed:', e);
      return null;
    }
  },

  /**
   * Whether telemetry is currently being recorded.
   */
  isActive(): boolean {
    return sampleInterval !== null;
  },
};
