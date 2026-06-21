/**
 * Shared types for debug telemetry recording and upload.
 *
 * Telemetry is captured at 1Hz during workout recordings and uploaded
 * to GCS as JSON for offline debugging and SRT generation.
 */

export interface TelemetrySample {
  /** Seconds since startedAt. */
  ts: number;
  /** BLE heart rate, undefined if no device connected. */
  hr?: number;
  /** Battery level 0..1, undefined if unavailable. */
  batt?: number;
  /** Index of the chunk being recorded at this moment. */
  chunkIdx?: number;
  /** Pose/motion detection snapshot. */
  motion?: { detected: boolean; confidence: number };
  /** Workout confidence 0..1, ratio of "working out" frames to total inference frames. */
  workoutConf?: number;
}

export interface TelemetrySession {
  sessionId: string;
  profileId: number;
  /** Date.now() ms at recording start. */
  startedAt: number;
  /** Date.now() ms at recording stop. */
  endedAt: number;
  samples: TelemetrySample[];
  appVersion: string;
  platform: 'ios' | 'android';
  deviceModel: string;
}

export interface PendingUpload {
  sessionId: string;
  filePath: string;
  attempts: number;
  lastAttemptAt?: number;
}
