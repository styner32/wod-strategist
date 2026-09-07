import { Buffer } from "buffer";
import Constants from "expo-constants";
import { Platform } from "react-native";
import type { Device, Subscription } from "react-native-ble-plx";

import type { BleSensorSink } from "../bleSensorSink";
import { NdjsonWriter } from "./ndjsonWriter";
import {
  PMD_CONTROL_POINT_UUID,
  PMD_DATA_UUID,
  PMD_SERVICE_UUID,
  PmdStreamContext,
  createGetMeasurementSettingsCommand,
  createStartMeasurementCommand,
  createStopMeasurementCommand,
  parseAccPacket,
  parseMeasurementSettingsResponse,
  parseStartMeasurementResponse,
} from "./polarPmdProtocol";

export interface StopResult {
  filePath: string;
  sessionId: string;
  /** False when some lines never reached disk — the file is not a full record. */
  complete: boolean;
}

interface PauseInterval {
  start_offset_ms: number;
  end_offset_ms: number;
}

interface RecorderState {
  isActive: boolean;
  isPaused: boolean;
  sessionId: string;
  profileId: number;
  baseEpochMs: number;
  writer: NdjsonWriter | null;
  streamContext: PmdStreamContext;
  streamId: number;
  pauseIntervals: PauseInterval[];
  pauseStartTime: number | null;
  accSamples: number;
  hrSamples: number;
  droppedPackets: number | null;
  gaps: number;
  openGapId: number | null;
  accRangeG: number;
  accResolutionBits: number;
  device: Device | null;
  deviceName?: string;
  hasPmd: boolean;
  /** Battery at the start of THIS session. */
  batteryStart?: number;
  /** Most recent reading, independent of session boundaries. */
  batteryLatest?: number;
  pmdCpSubscription: Subscription | null;
  pmdDataSubscription: Subscription | null;
}

let state: RecorderState = {
  isActive: false,
  isPaused: false,
  sessionId: "",
  profileId: 0,
  baseEpochMs: 0,
  writer: null,
  streamContext: {
    baseEpochMs: 0,
    accHz: 50,
  },
  streamId: 1,
  pauseIntervals: [],
  pauseStartTime: null,
  accSamples: 0,
  hrSamples: 0,
  droppedPackets: null,
  gaps: 0,
  openGapId: null,
  accRangeG: 8,
  accResolutionBits: 16,
  device: null,
  hasPmd: false,
  pmdCpSubscription: null,
  pmdDataSubscription: null,
};

let stopPromise: Promise<StopResult | null> | null = null;

/**
 * Monotonic token identifying the current PMD setup attempt.
 *
 * setupPmdStreaming awaits BLE calls; a session stop, a disconnect, or a newer
 * device can land while it waits. Every continuation checks its captured
 * generation before touching state, so a stale attempt cannot resurrect
 * subscriptions or start a measurement after the session it belonged to ended.
 */
let pmdGeneration = 0;

function invalidatePmdGeneration(): void {
  pmdGeneration += 1;
}

function cleanupSubscriptions(): void {
  if (state.pmdCpSubscription) {
    state.pmdCpSubscription.remove();
    state.pmdCpSubscription = null;
  }
  if (state.pmdDataSubscription) {
    state.pmdDataSubscription.remove();
    state.pmdDataSubscription = null;
  }
}

export const PolarSensorRecorder: BleSensorSink & {
  start(opts: { sessionId: string; profileId: number; baseEpochMs: number }): void;
  pause(): void;
  resume(): void;
  stop(): Promise<StopResult | null>;
  getLiveStatus(): { accSamples: number; hrSamples: number; dropped: number | null };
  isActive(): boolean;
  setBattery(percent: number): void;
  onBattery(percent: number): void;
} = {
  isActive(): boolean {
    return state.isActive;
  },

  setBattery(percent: number): void {
    state.batteryLatest = percent;
    if (state.isActive && state.batteryStart === undefined) {
      state.batteryStart = percent;
    }
  },

  onBattery(percent: number): void {
    this.setBattery(percent);
  },

  start(opts: { sessionId: string; profileId: number; baseEpochMs: number }): void {
    if (state.isActive) {
      console.warn("⚠️ PolarSensorRecorder is already active");
      return;
    }

    // Any PMD setup still in flight belongs to the previous session.
    invalidatePmdGeneration();

    const writer = new NdjsonWriter(opts.sessionId);

    state = {
      isActive: true,
      isPaused: false,
      sessionId: opts.sessionId,
      profileId: opts.profileId,
      baseEpochMs: opts.baseEpochMs,
      writer,
      streamContext: {
        baseEpochMs: opts.baseEpochMs,
        accHz: 50,
      },
      streamId: 1,
      pauseIntervals: [],
      pauseStartTime: null,
      accSamples: 0,
      hrSamples: 0,
      droppedPackets: null,
      gaps: 0,
      openGapId: null,
      accRangeG: 8,
      accResolutionBits: 16,
      device: state.device ?? null,
      deviceName: state.device?.name ?? state.deviceName ?? undefined,
      hasPmd: state.hasPmd,
      // Snapshot the current reading — a value carried over from an earlier
      // session in the same app run is not this session's starting battery.
      batteryStart: state.batteryLatest,
      batteryLatest: state.batteryLatest,
      pmdCpSubscription: null,
      pmdDataSubscription: null,
    };

    // Header (1st line) matches Section 3 specification
    const header = {
      k: "meta",
      schema_version: "2.0.0",
      workout_session_id: opts.sessionId,
      profile_id: opts.profileId,
      clock_source: "capture_clock",
      base_epoch_ms: opts.baseEpochMs,
      requested_sampling: {
        acc_hz: 50,
        acc_range_g: 8,
        acc_resolution_bits: 16,
      },
      device: null,
      app: {
        version: Constants.expoConfig?.version ?? "1.0.0",
        platform: Platform.OS,
      },
    };

    writer.writeImmediate(header);
    writer.startAutoFlush(1000);

    // If device is already ready and connected, write device_ready event and initiate PMD
    if (state.device) {
      writer.writeImmediate({
        k: "device_ready",
        t: 0,
        stream_id: state.streamId,
        device: {
          name: state.deviceName ?? "Unknown",
          firmware: null,
          battery_percent_start: state.batteryStart ?? null,
        },
      });

      if (state.hasPmd) {
        void setupPmdStreaming(state.device);
      }
    }
  },

  pause(): void {
    if (!state.isActive || state.isPaused) return;
    state.isPaused = true;
    const offset = Math.max(0, Date.now() - state.baseEpochMs);
    state.pauseStartTime = offset;
    state.writer?.writeImmediate({ k: "pause", t: offset });
  },

  resume(): void {
    if (!state.isActive || !state.isPaused) return;
    state.isPaused = false;
    const offset = Math.max(0, Date.now() - state.baseEpochMs);
    if (state.pauseStartTime !== null) {
      state.pauseIntervals.push({
        start_offset_ms: state.pauseStartTime,
        end_offset_ms: offset,
      });
      state.pauseStartTime = null;
    }
    state.writer?.writeImmediate({ k: "resume", t: offset });
  },

  stop(): Promise<StopResult | null> {
    if (stopPromise) {
      return stopPromise;
    }
    const activeWriter = state.writer;
    if (!state.isActive || !activeWriter) {
      return Promise.resolve(null);
    }

    // Stop any PMD setup that is still waiting on a BLE round-trip.
    invalidatePmdGeneration();

    stopPromise = (async () => {
      const { sessionId, baseEpochMs, pauseIntervals, pauseStartTime } = state;
      const endOffset = Math.max(0, Date.now() - baseEpochMs);

      // Close any unclosed pause interval
      if (pauseStartTime !== null) {
        pauseIntervals.push({
          start_offset_ms: pauseStartTime,
          end_offset_ms: endOffset,
        });
        state.pauseStartTime = null;
      }

      // Stop PMD measurement on device with timeout
      if (state.device) {
        try {
          const stopCmd = createStopMeasurementCommand();
          const base64Cmd = Buffer.from(stopCmd).toString("base64");
          await Promise.race([
            state.device.writeCharacteristicWithResponseForService(
              PMD_SERVICE_UUID,
              PMD_CONTROL_POINT_UUID,
              base64Cmd,
            ),
            new Promise((_, reject) =>
              setTimeout(() => reject(new Error("PMD stop timeout")), 1000),
            ),
          ]);
        } catch (err) {
          console.warn("⚠️ Failed to send PMD stop command:", err);
        }
      }

      cleanupSubscriptions();

      // Footer (last line). Write failures are recorded so a consumer can tell
      // a lossy file from a complete one even when the file itself parses.
      const writeStats = activeWriter.stats();
      const footer = {
        k: "end",
        t: endOffset,
        pause_intervals: pauseIntervals,
        device: {
          battery_percent_end: state.batteryLatest ?? state.batteryStart ?? null,
        },
        summary: {
          hr_samples: state.hrSamples,
          acc_samples: state.accSamples,
          dropped_packets: state.droppedPackets,
          gaps: state.gaps,
          write_failures: writeStats.failedWrites,
          dropped_lines: writeStats.droppedLines,
        },
      };

      activeWriter.writeImmediate(footer);
      const closed = activeWriter.close();

      state.isActive = false;
      state.writer = null;
      delete state.streamContext.anchor;

      if (!closed.complete) {
        console.warn(
          `⚠️ Sensor file for ${sessionId} is incomplete: ${closed.failedWrites} failed writes, ` +
            `${closed.droppedLines} dropped lines, ${closed.pendingLines} lines never written`,
        );
      }

      return { filePath: closed.filePath, sessionId, complete: closed.complete };
    })().finally(() => {
      stopPromise = null;
    });

    return stopPromise;
  },

  getLiveStatus(): { accSamples: number; hrSamples: number; dropped: number | null } {
    return {
      accSamples: state.accSamples,
      hrSamples: state.hrSamples,
      dropped: state.droppedPackets,
    };
  },

  // --- BleSensorSink Implementation ---

  onDeviceReady(device: Device, hasPmd: boolean): void {
    state.device = device;
    state.deviceName = device.name ?? undefined;
    state.hasPmd = hasPmd;

    if (!state.isActive || !state.writer) {
      return;
    }

    const offset = Math.max(0, Date.now() - state.baseEpochMs);

    // If there was an open gap, close it now
    if (state.openGapId !== null) {
      state.writer.writeImmediate({
        k: "gap_end",
        t: offset,
        gap_id: state.openGapId,
      });
      state.openGapId = null;
      state.streamId += 1;
    }

    state.writer.writeImmediate({
      k: "device_ready",
      t: offset,
      stream_id: state.streamId,
      device: {
        name: device.name ?? "Unknown",
        firmware: null,
        battery_percent_start: state.batteryStart ?? null,
      },
    });

    if (hasPmd) {
      void setupPmdStreaming(device);
    }
  },

  onDeviceLost(reason: string): void {
    invalidatePmdGeneration();
    cleanupSubscriptions();
    state.device = null;
    delete state.streamContext.anchor;

    // The hook can report the same outage twice (onDisconnected, then the
    // reconnect path tearing the connection down). One gap per outage.
    if (state.isActive && state.writer && state.openGapId === null) {
      state.gaps += 1;
      state.openGapId = state.gaps;
      const offset = Math.max(0, Date.now() - state.baseEpochMs);
      state.writer.writeImmediate({
        k: "gap_start",
        t: offset,
        gap_id: state.gaps,
        reason,
      });
    }
  },

  onHeartRate(bpm: number, rrIntervalsMs: number[], receivedAtMs: number): void {
    if (!state.isActive || !state.writer) {
      return;
    }

    state.hrSamples += 1;
    const offset = Math.max(0, receivedAtMs - state.baseEpochMs);
    const event: { k: string; t: number; bpm: number; rr?: number[] } = {
      k: "hr",
      t: offset,
      bpm,
    };
    if (rrIntervalsMs && rrIntervalsMs.length > 0) {
      event.rr = rrIntervalsMs;
    }
    state.writer.write(event);
  },
};

/** Prefers `preferred` when the device offers it, else the device's first option. */
function pickSetting(available: number[], preferred: number, fallback: number): number {
  if (available.includes(preferred)) return preferred;
  return available[0] ?? fallback;
}

async function setupPmdStreaming(device: Device): Promise<void> {
  cleanupSubscriptions();

  // Claim a generation; every continuation below bails out if a stop, a
  // disconnect, or a newer setup has since superseded this attempt.
  invalidatePmdGeneration();
  const generation = pmdGeneration;
  const isCurrent = () => generation === pmdGeneration && state.device === device;

  try {
    if (Platform.OS === "android") {
      try {
        await device.requestMTU(232);
      } catch (err) {
        console.warn("⚠️ Failed to request MTU 232 on Android:", err);
      }
      if (!isCurrent()) return;
    }

    // Monitor PMD Control Point for settings response and start response
    const cpSubscription = device.monitorCharacteristicForService(
      PMD_SERVICE_UUID,
      PMD_CONTROL_POINT_UUID,
      (error, characteristic) => {
        if (error || !characteristic?.value) {
          if (error) console.warn("⚠️ PMD CP error:", error);
          return;
        }
        // Never negotiate or start a measurement for a session that ended.
        if (!isCurrent() || !state.isActive) return;

        try {
          const raw = Buffer.from(characteristic.value, "base64");
          if (raw[0] === 0xf0 && raw[1] === 0x01) {
            // Settings query response -> send start command
            const settings = parseMeasurementSettingsResponse(raw);
            if (settings.sampleRates.length === 0) {
              console.warn(
                "⚠️ PMD settings response carried no sample rates; using defaults",
              );
            }
            const sampleRate = pickSetting(settings.sampleRates, 50, 50);
            const resolution = pickSetting(settings.resolutions, 16, 16);
            const range = pickSetting(settings.ranges, 8, 8);

            // The negotiated rate defines dt for every sample offset. Leaving
            // it at the requested 50Hz would silently mis-timestamp a stream
            // the device opened at another rate.
            state.streamContext.accHz = sampleRate;
            state.accResolutionBits = resolution;
            state.accRangeG = range;

            const startCmd = createStartMeasurementCommand({
              sampleRate,
              resolution,
              range,
            });
            const base64Start = Buffer.from(startCmd).toString("base64");
            void device.writeCharacteristicWithResponseForService(
              PMD_SERVICE_UUID,
              PMD_CONTROL_POINT_UUID,
              base64Start,
            );
          } else if (raw[0] === 0xf0 && raw[1] === 0x02) {
            // Start response
            const res = parseStartMeasurementResponse(raw);
            if (!res.success) {
              console.warn("⚠️ PMD start measurement failed with code:", res.errorCode);
            }
          }
        } catch (e) {
          console.warn("⚠️ Error parsing PMD CP notification:", e);
        }
      },
    );

    // Monitor PMD Data notifications (50Hz ACC)
    const dataSubscription = device.monitorCharacteristicForService(
      PMD_SERVICE_UUID,
      PMD_DATA_UUID,
      (error, characteristic) => {
        if (error || !characteristic?.value) {
          if (error) {
            state.droppedPackets = (state.droppedPackets ?? 0) + 1;
          }
          return;
        }

        if (!isCurrent() || !state.isActive || !state.writer) return;

        try {
          const parsed = parseAccPacket(
            characteristic.value,
            state.streamContext,
            Date.now(),
          );

          if (parsed.isNewAnchor) {
            state.writer.writeImmediate({
              k: "stream_start",
              t: parsed.packetOffsetMs,
              stream_id: state.streamId,
              sampling: {
                acc_hz: state.streamContext.accHz,
                acc_range_g: state.accRangeG,
                acc_resolution_bits: state.accResolutionBits,
                frame_type: parsed.frameType,
                delta_compressed: parsed.isDelta,
              },
              clock_anchor: {
                device_timestamp_ns: parsed.deviceTimestampNs,
                capture_offset_ms: parsed.packetOffsetMs,
                method: "first_packet_received",
              },
            });
          }

          if (parsed.samples.length > 0) {
            state.accSamples += parsed.samples.length;
            state.writer.write({
              k: "acc",
              t: parsed.firstSampleOffsetMs,
              stream_id: state.streamId,
              dt: parsed.dt,
              v: parsed.samples,
            });
          }
        } catch (e) {
          state.droppedPackets = (state.droppedPackets ?? 0) + 1;
          console.warn("⚠️ Failed to parse PMD ACC packet:", e);
        }
      },
    );

    // Both subscriptions exist now — publish them only if still relevant,
    // otherwise remove them so no notification outlives its session.
    if (!isCurrent()) {
      cpSubscription.remove();
      dataSubscription.remove();
      return;
    }
    state.pmdCpSubscription = cpSubscription;
    state.pmdDataSubscription = dataSubscription;

    // Query settings to initiate negotiation
    const queryCmd = createGetMeasurementSettingsCommand();
    const base64Query = Buffer.from(queryCmd).toString("base64");
    await device.writeCharacteristicWithResponseForService(
      PMD_SERVICE_UUID,
      PMD_CONTROL_POINT_UUID,
      base64Query,
    );
  } catch (err) {
    console.warn("⚠️ Failed to setup PMD streaming:", err);
  }
}
