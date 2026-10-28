import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { AppState, type AppStateStatus, StyleSheet, Text, View } from 'react-native';
import * as Battery from 'expo-battery';
import { useIsFocused } from 'expo-router/react-navigation';

interface EnergyMonitorProps {
  /** Label shown at the top, e.g. "Heavy Model · 15fps" */
  label: string;
}

interface BatterySnapshot {
  level: number;   // 0–1
  time: number;     // Date.now()
}

/**
 * Compact overlay badge that tracks battery drain in real-time.
 *
 * Shows:
 * - Current battery %
 * - Drain rate (%/hour), calculated from the level when monitoring started
 * - Elapsed time since monitoring started
 *
 * Usage:
 *   <EnergyMonitor label="Heavy · 15fps" />
 */
export function EnergyMonitor({ label }: EnergyMonitorProps) {
  // Both callers are navigation screens. Hidden or inactive screens need no battery polling.
  const isFocused = useIsFocused();
  const [appState, setAppState] = useState<AppStateStatus>(AppState.currentState);
  const monitoringActive = isFocused && appState === 'active';
  const activeRef = useRef(monitoringActive);
  const focusedRef = useRef(isFocused);
  useLayoutEffect(() => {
    focusedRef.current = isFocused;
    activeRef.current = monitoringActive;
  }, [isFocused, monitoringActive]);
  const [currentLevel, setCurrentLevel] = useState<number | null>(null);
  const [drainRate, setDrainRate] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const startSnapshot = useRef<BatterySnapshot | null>(null);
  const inFlightRead = useRef<Promise<number> | null>(null);

  useEffect(() => {
    const subscription = AppState.addEventListener('change', next => {
      activeRef.current = focusedRef.current && next === 'active';
      setAppState(next);
    });
    return () => subscription.remove();
  }, []);

  // Wait for each native read before scheduling another, including across focus changes.
  useEffect(() => {
    if (!monitoringActive) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const poll = async () => {
      try {
        // A native read cannot be cancelled. A returning screen waits for the old one.
        if (inFlightRead.current) await inFlightRead.current.catch(() => {});
        if (cancelled || !activeRef.current) return;
        const request = Battery.getBatteryLevelAsync();
        inFlightRead.current = request;
        let level: number;
        try {
          level = await request;
        } finally {
          if (inFlightRead.current === request) inFlightRead.current = null;
        }
        if (cancelled || !activeRef.current) return;
        const now = Date.now();
        setCurrentLevel(level);
        if (startSnapshot.current === null) startSnapshot.current = { level, time: now };
        const snap = startSnapshot.current;
        const elapsedSec = (now - snap.time) / 1000;
        setElapsed(elapsedSec);
        if (elapsedSec > 30) {
          const drainPct = (snap.level - level) * 100;
          setDrainRate(drainPct / (elapsedSec / 3600));
        }
      } catch {
        // Battery API might not be available on simulator, including on the first read.
      } finally {
        if (!cancelled && activeRef.current) {
          timer = setTimeout(() => { timer = null; void poll(); }, 10_000);
        }
      }
    };

    void poll();
    return () => {
      cancelled = true;
      if (timer !== null) clearTimeout(timer);
    };
  }, [monitoringActive]);

  const formatTime = (secs: number) => {
    const m = Math.floor(secs / 60);
    const s = Math.floor(secs % 60);
    return `${m}:${s.toString().padStart(2, '0')}`;
  };

  const batteryPct = currentLevel !== null ? (currentLevel * 100).toFixed(1) : '--';
  const drainStr = drainRate !== null ? drainRate.toFixed(1) : '--';
  const drainColor =
    drainRate === null ? '#888' :
    drainRate < 5 ? '#4ECDC4' :   // green — light usage
    drainRate < 15 ? '#FFEAA7' :  // yellow — moderate
    '#FF6B6B';                     // red — heavy

  return (
    <View style={styles.container}>
      <Text style={styles.label}>⚡ {label}</Text>
      <View style={styles.row}>
        <View style={styles.metric}>
          <Text style={styles.value}>{batteryPct}%</Text>
          <Text style={styles.unit}>BATTERY</Text>
        </View>
        <View style={styles.divider} />
        <View style={styles.metric}>
          <Text style={[styles.value, { color: drainColor }]}>{drainStr}</Text>
          <Text style={styles.unit}>%/HOUR</Text>
        </View>
        <View style={styles.divider} />
        <View style={styles.metric}>
          <Text style={styles.value}>{formatTime(elapsed)}</Text>
          <Text style={styles.unit}>ELAPSED</Text>
        </View>
      </View>
      {elapsed < 60 && (
        <Text style={styles.hint}>measuring drain rate...</Text>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    backgroundColor: 'rgba(0, 0, 0, 0.75)',
    borderRadius: 12,
    padding: 10,
    marginHorizontal: 12,
    borderWidth: 1,
    borderColor: 'rgba(255, 255, 255, 0.15)',
  },
  label: {
    color: '#FFA500',
    fontSize: 11,
    fontWeight: '700',
    letterSpacing: 0.5,
    textAlign: 'center',
    marginBottom: 6,
  },
  row: {
    flexDirection: 'row',
    justifyContent: 'space-around',
    alignItems: 'center',
  },
  metric: {
    alignItems: 'center',
    flex: 1,
  },
  value: {
    color: '#FFF',
    fontSize: 18,
    fontWeight: '700',
    fontVariant: ['tabular-nums'],
  },
  unit: {
    color: '#888',
    fontSize: 9,
    fontWeight: '600',
    letterSpacing: 1,
    marginTop: 2,
  },
  divider: {
    width: 1,
    height: 28,
    backgroundColor: 'rgba(255, 255, 255, 0.15)',
  },
  hint: {
    color: '#666',
    fontSize: 10,
    textAlign: 'center',
    marginTop: 4,
    fontStyle: 'italic',
  },
});
