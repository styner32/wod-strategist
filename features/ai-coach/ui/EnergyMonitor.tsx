import React, { useEffect, useRef, useState } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import * as Battery from 'expo-battery';

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
  const [currentLevel, setCurrentLevel] = useState<number | null>(null);
  const [drainRate, setDrainRate] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const startSnapshot = useRef<BatterySnapshot | null>(null);
  const startTime = useRef(Date.now());

  // Initial battery read & start tracking
  useEffect(() => {
    let interval: ReturnType<typeof setInterval>;

    const init = async () => {
      // Enable battery monitoring on iOS
      await Battery.getBatteryLevelAsync(); // triggers monitoring

      const level = await Battery.getBatteryLevelAsync();
      setCurrentLevel(level);
      startSnapshot.current = { level, time: Date.now() };
      startTime.current = Date.now();

      // Poll every 10 seconds for updated battery level
      interval = setInterval(async () => {
        try {
          const now = Date.now();
          const lvl = await Battery.getBatteryLevelAsync();
          setCurrentLevel(lvl);

          const elapsedSec = (now - startTime.current) / 1000;
          setElapsed(elapsedSec);

          const snap = startSnapshot.current;
          if (snap && elapsedSec > 30) {
            // Calculate drain rate in %/hour
            const drainPct = (snap.level - lvl) * 100;
            const elapsedHours = elapsedSec / 3600;
            const rate = drainPct / elapsedHours;
            setDrainRate(rate);
          }
        } catch (_) {
          // Battery API might not be available on simulator
        }
      }, 10_000);
    };

    init();
    return () => clearInterval(interval);
  }, []);

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
