/**
 * Experimental Polar H10 internal HR recording (opt-in, phase 1).
 *
 * Runs beside the live BLE pipeline and never blocks it:
 * - begin(): after the strap is connected, clears any leftover sensor recording and
 *   starts H10 internal HR recording (1 s) named with the workout session ID.
 * - finish(): stops it, fetches SAMPLES.BPB, saves a local JSON record, then removes
 *   the sensor recording. If the local save fails or takes longer than 10 minutes,
 *   only the error (stage, code) is kept and the sensor recording is removed anyway.
 * - Records are uploaded to videos/{profileId}/{sessionId}/h10_memory_hr.json.
 *
 * The H10 stores no timestamps: sample i belongs to start ack + i * interval. Both the
 * send and ack phone times are kept so the alignment can be validated on real devices.
 */
import Constants from "expo-constants";
import {
  deleteAsync,
  documentDirectory,
  makeDirectoryAsync,
  moveAsync,
  readAsStringAsync,
  readDirectoryAsync,
  writeAsStringAsync,
} from "expo-file-system/legacy";
import { Platform } from "react-native";
import type { Device } from "react-native-ble-plx";

import { getUploadUrl, uploadSessionAssetToGcs } from "../../wod/api";
import { PolarSensorRecorder } from "./polarSensorRecorder";
import { PsftpClient, PsftpTimeoutError } from "./psftpClient";
import {
  PFTP_COMMAND_GET,
  PFTP_COMMAND_REMOVE,
  PFTP_QUERY_REQUEST_RECORDING_STATUS,
  PFTP_QUERY_REQUEST_START_RECORDING,
  PFTP_QUERY_REQUEST_STOP_RECORDING,
  PftpError,
  decodeDirectory,
  decodeExerciseSamples,
  decodeRecordingStatus,
  encodeOperation,
  encodeStartRecordingParams,
  type SensorOfflineRange,
} from "./psftpProtocol";

export const H10_MEMORY_FILENAME = "h10_memory_hr.json";
export const H10_MEMORY_DEADLINE_MS = 10 * 60 * 1000;
const SENSOR_CLEANUP_TIMEOUT_MS = 30_000;
const FETCH_TIMEOUT_MS = 60_000;
const SELF_CONNECT_AFTER_MS = 5_000;
const FETCH_RETRY_DELAY_MS = 3_000;
const SAMPLES_FILE = "SAMPLES.BPB";

export type H10MemoryPhase =
  | "idle"
  | "waiting_device"
  | "starting"
  | "recording"
  | "start_failed"
  | "finishing"
  | "saved"
  | "failed";

export interface H10MemoryError {
  stage: string;
  code: string;
  message: string;
  at_epoch_ms: number;
}

export interface H10MemoryRecord {
  schema_version: 1;
  kind: "polar_h10_memory_hr";
  workout_session_id: string;
  /** Null only for an orphan sensor recording found without a local session record. */
  profile_id: number | null;
  base_epoch_ms: number | null;
  status: "recording" | "complete" | "error";
  recording_identifier: string;
  device_name: string | null;
  app: { platform: string; version: string };
  transport: { packet_size: number | null; write_mode: string | null };
  leftovers: { path: string; identifier: string; action: string }[];
  start: { sent_epoch_ms: number | null; ack_epoch_ms: number | null };
  stop: {
    requested_epoch_ms: number | null;
    sent_epoch_ms: number | null;
    ack_epoch_ms: number | null;
    recording_on_before_stop: boolean | null;
    sensor_identifier: string | null;
  };
  fetch: {
    attempts: number;
    started_epoch_ms: number | null;
    completed_epoch_ms: number | null;
    entries: string[];
    entry_path: string | null;
    bytes: number | null;
  };
  recording_interval_ms: number | null;
  hr_samples: number[];
  hr_offline: SensorOfflineRange[];
  other_fields: number[];
  sensor_removed: boolean;
  errors: H10MemoryError[];
  recovered_later: boolean;
  saved_epoch_ms: number | null;
}

export interface H10MemoryStatus {
  phase: H10MemoryPhase;
  sessionId: string | null;
  error: H10MemoryError | null;
  sampleCount: number | null;
}

export interface H10RecordStore {
  read(sessionId: string): Promise<H10MemoryRecord | null>;
  /** Persists the record and returns its local file URI. */
  write(record: H10MemoryRecord): Promise<string>;
  listPendingUploads(): Promise<{ record: H10MemoryRecord; uri: string }[]>;
  markUploaded(record: H10MemoryRecord): Promise<void>;
}

export interface H10PsftpLike {
  readonly packetSize: number;
  readonly writeMode: string;
  open(): void;
  close(): void;
  query(id: number, params?: Uint8Array, timeoutMs?: number): Promise<Uint8Array>;
  request(header: Uint8Array, timeoutMs?: number, label?: string): Promise<Uint8Array>;
}

export interface H10MemoryDeps {
  now(): number;
  sleep(ms: number): Promise<void>;
  getDevice(): Device | null;
  createClient(device: Device): H10PsftpLike;
  store: H10RecordStore;
  upload(sessionId: string, profileId: number, fileUri: string): Promise<void>;
}

interface Run {
  sessionId: string;
  profileId: number;
  record: H10MemoryRecord;
  finishRequested: boolean;
  startSent: boolean;
  lastDevice: Device | null;
  selfConnected: Device | null;
}

class StageError extends Error {
  constructor(readonly code: string, message: string) {
    super(message);
    this.name = "StageError";
  }
}

function errorCode(error: unknown): string {
  if (error instanceof StageError) return error.code;
  if (error instanceof PftpError) return `PFTP_${error.code}_${error.codeName}`;
  if (error instanceof PsftpTimeoutError) return "TIMEOUT";
  const bleCode = (error as { errorCode?: unknown } | null)?.errorCode;
  if (typeof bleCode === "number") return `BLE_${bleCode}`;
  return "UNKNOWN";
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message.slice(0, 500);
  return String(error).slice(0, 500);
}

function safeName(value: string): string {
  return value.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 96) || "unknown";
}

function identifierFromPath(path: string): string {
  return path.split("/").filter(Boolean)[0] ?? "";
}

function appInfo(): { platform: string; version: string } {
  return { platform: Platform.OS, version: Constants.expoConfig?.version ?? "1.0.0" };
}

export function createEmptyRecord(
  sessionId: string,
  profileId: number | null,
  baseEpochMs: number | null,
): H10MemoryRecord {
  return {
    schema_version: 1,
    kind: "polar_h10_memory_hr",
    workout_session_id: sessionId,
    profile_id: profileId,
    base_epoch_ms: baseEpochMs,
    status: "recording",
    recording_identifier: sessionId,
    device_name: null,
    app: appInfo(),
    transport: { packet_size: null, write_mode: null },
    leftovers: [],
    start: { sent_epoch_ms: null, ack_epoch_ms: null },
    stop: {
      requested_epoch_ms: null,
      sent_epoch_ms: null,
      ack_epoch_ms: null,
      recording_on_before_stop: null,
      sensor_identifier: null,
    },
    fetch: { attempts: 0, started_epoch_ms: null, completed_epoch_ms: null, entries: [], entry_path: null, bytes: null },
    recording_interval_ms: null,
    hr_samples: [],
    hr_offline: [],
    other_fields: [],
    sensor_removed: false,
    errors: [],
    recovered_later: false,
    saved_epoch_ms: null,
  };
}

function withDeadline<T>(work: Promise<T>, ms: number, code: string, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new StageError(code, message)), Math.max(0, ms));
  });
  return Promise.race([work, timeout]).finally(() => clearTimeout(timer));
}

export class H10MemoryRecorder {
  private status: H10MemoryStatus = { phase: "idle", sessionId: null, error: null, sampleCount: null };
  private listeners = new Set<(status: H10MemoryStatus) => void>();
  /** Serializes every sensor-side job: the H10 keeps only one recording. */
  private sensorQueue: Promise<void> = Promise.resolve();
  private active: Run | null = null;
  private flushing: Promise<void> | null = null;

  constructor(private readonly deps: H10MemoryDeps) {}

  getStatus(): H10MemoryStatus {
    return this.status;
  }

  subscribe(listener: (status: H10MemoryStatus) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Fire-and-forget. Never throws; failures are reported through status and the record. */
  begin(ctx: { sessionId: string; profileId: number; baseEpochMs: number }): void {
    if (this.active) {
      if (this.active.sessionId === ctx.sessionId) return;
      // A previous run never received finish(): close it first (jobs are serialized).
      console.warn("[H10 memory] finishing previous run before a new begin", this.active.sessionId);
      void this.finish(this.active.sessionId);
    }
    const run: Run = {
      sessionId: ctx.sessionId,
      profileId: ctx.profileId,
      record: createEmptyRecord(ctx.sessionId, ctx.profileId, ctx.baseEpochMs),
      finishRequested: false,
      startSent: false,
      lastDevice: null,
      selfConnected: null,
    };
    this.active = run;
    this.setStatus({ phase: "waiting_device", sessionId: ctx.sessionId, error: null, sampleCount: null });
    this.enqueue(() => this.runStart(run));
    void this.flushUploads();
  }

  /** Resolves when the record has been saved (or the error recorded) and the upload attempted. */
  finish(sessionId: string): Promise<void> {
    const run = this.active;
    if (!run || run.sessionId !== sessionId) return Promise.resolve();
    run.finishRequested = true;
    run.record.stop.requested_epoch_ms = this.deps.now();
    this.active = null;
    return this.enqueue(() => this.runFinish(run));
  }

  flushUploads(): Promise<void> {
    if (this.flushing) return this.flushing;
    this.flushing = (async () => {
      try {
        const pending = await this.deps.store.listPendingUploads();
        for (const { record, uri } of pending) {
          if (record.status === "recording" || record.profile_id === null) continue;
          try {
            await this.deps.upload(record.workout_session_id, record.profile_id, uri);
            await this.deps.store.markUploaded(record);
          } catch (error) {
            console.warn("[H10 memory] upload failed; will retry", record.workout_session_id, error);
          }
        }
      } catch (error) {
        console.warn("[H10 memory] could not list pending uploads", error);
      } finally {
        this.flushing = null;
      }
    })();
    return this.flushing;
  }

  // -------------------------------------------------------------------------

  private enqueue(job: () => Promise<void>): Promise<void> {
    const next = this.sensorQueue.then(job, job).catch((error) => {
      console.warn("[H10 memory] job failed", error);
    });
    this.sensorQueue = next;
    return next;
  }

  private setStatus(status: H10MemoryStatus): void {
    this.status = status;
    for (const listener of this.listeners) {
      try {
        listener(status);
      } catch {
        // UI listeners must not break the recorder.
      }
    }
  }

  private addError(record: H10MemoryRecord, stage: string, error: unknown): H10MemoryError {
    const entry: H10MemoryError = {
      stage,
      code: errorCode(error),
      message: errorMessage(error),
      at_epoch_ms: this.deps.now(),
    };
    record.errors.push(entry);
    return entry;
  }

  private async save(record: H10MemoryRecord): Promise<string | null> {
    try {
      return await this.deps.store.write(record);
    } catch (error) {
      console.warn("[H10 memory] local save failed", record.workout_session_id, error);
      return null;
    }
  }

  private async waitForDevice(run: Run): Promise<Device | null> {
    for (;;) {
      if (run.finishRequested) return null;
      const device = this.deps.getDevice();
      if (device) {
        run.lastDevice = device;
        if (await device.isConnected().catch(() => false)) return device;
      }
      await this.deps.sleep(1000);
    }
  }

  /** Uses the live connection when possible; reconnects itself after the recording screen closed. */
  private async ensureConnected(run: Run, deadline: number): Promise<Device> {
    const waitingSince = this.deps.now();
    for (;;) {
      if (this.deps.now() >= deadline) {
        throw new StageError("DEVICE_UNREACHABLE", "H10 was not reachable before the deadline");
      }
      const device = this.deps.getDevice() ?? run.selfConnected ?? run.lastDevice;
      if (device) {
        run.lastDevice = device;
        if (await device.isConnected().catch(() => false)) return device;
        if (this.deps.now() - waitingSince >= SELF_CONNECT_AFTER_MS) {
          try {
            const connected = await device.connect({ timeout: 10_000 });
            await connected.discoverAllServicesAndCharacteristics();
            run.selfConnected = connected;
            return connected;
          } catch {
            // Out of range or the strap is off; keep trying until the deadline.
          }
        }
      }
      await this.deps.sleep(1000);
    }
  }

  private openClient(run: Run, device: Device): H10PsftpLike {
    const client = this.deps.createClient(device);
    client.open();
    run.record.device_name = device.name ?? run.record.device_name;
    run.record.transport = { packet_size: client.packetSize, write_mode: client.writeMode };
    return client;
  }

  private async listSampleFiles(client: H10PsftpLike, path = "/", depth = 0): Promise<string[]> {
    if (depth > 3) return [];
    const entries = decodeDirectory(await client.request(encodeOperation(PFTP_COMMAND_GET, path), 15_000, `list ${path}`));
    const files: string[] = [];
    for (const entry of entries) {
      if (entry.name === SAMPLES_FILE) files.push(path + entry.name);
      else if (entry.name.endsWith("/")) files.push(...(await this.listSampleFiles(client, path + entry.name, depth + 1)));
    }
    return files;
  }

  private async fetchInto(client: H10PsftpLike, record: H10MemoryRecord, path: string): Promise<void> {
    record.fetch.entry_path = path;
    record.fetch.started_epoch_ms = this.deps.now();
    const bytes = await client.request(encodeOperation(PFTP_COMMAND_GET, path), FETCH_TIMEOUT_MS, `get ${path}`);
    const samples = decodeExerciseSamples(bytes);
    record.fetch.completed_epoch_ms = this.deps.now();
    record.fetch.bytes = bytes.length;
    record.recording_interval_ms = samples.recordingIntervalMs;
    record.hr_samples = samples.heartRateSamples;
    record.hr_offline = samples.heartRateOffline;
    record.other_fields = samples.otherFields;
  }

  /** A previous session's recording blocks the single slot; recover or discard it first. */
  private async clearLeftovers(client: H10PsftpLike, run: Run): Promise<void> {
    const status = decodeRecordingStatus(await client.query(PFTP_QUERY_REQUEST_RECORDING_STATUS));
    if (status.recordingOn) await client.query(PFTP_QUERY_REQUEST_STOP_RECORDING);
    for (const path of await this.listSampleFiles(client)) {
      const identifier = identifierFromPath(path);
      const existing = identifier ? await this.deps.store.read(identifier).catch(() => null) : null;
      let action = "removed";
      let recovered: H10MemoryRecord | null = null;
      if (!existing || existing.status === "recording") {
        // App closed before finish(): keep the data with its original session if known.
        recovered = existing ?? createEmptyRecord(`orphan_${safeName(identifier)}_${this.deps.now()}`, null, null);
        recovered.recording_identifier = identifier;
        recovered.recovered_later = true;
        try {
          await this.fetchInto(client, recovered, path);
          recovered.status = "complete";
          recovered.saved_epoch_ms = this.deps.now();
          action = "recovered";
        } catch (error) {
          this.addError(recovered, "leftover_fetch", error);
          recovered.status = "error";
          action = "fetch_failed_removed";
        }
        await this.save(recovered);
      }
      try {
        await client.request(encodeOperation(PFTP_COMMAND_REMOVE, path), 15_000, `remove ${path}`);
        // An already handled record (complete/error, possibly uploaded) is not rewritten.
        if (recovered) recovered.sensor_removed = true;
      } catch (error) {
        action += "_remove_failed";
        if (recovered) this.addError(recovered, "leftover_remove", error);
      }
      if (recovered) await this.save(recovered);
      run.record.leftovers.push({ path, identifier, action });
    }
  }

  private async runStart(run: Run): Promise<void> {
    await this.save(run.record);
    const device = await this.waitForDevice(run);
    if (!device) return; // finish() records NO_DEVICE
    this.setStatus({ phase: "starting", sessionId: run.sessionId, error: null, sampleCount: null });
    let client: H10PsftpLike | null = null;
    try {
      client = this.openClient(run, device);
      const opened = client;
      try {
        await withDeadline(this.clearLeftovers(opened, run), H10_MEMORY_DEADLINE_MS, "DEADLINE_10MIN", "Leftover cleanup exceeded 10 minutes");
      } catch (error) {
        // Not fatal: if the slot is really occupied, the start query reports it.
        this.addError(run.record, "leftover_check", error);
      }
      if (run.record.leftovers.some((l) => l.action.startsWith("recovered"))) void this.flushUploads();
      if (run.finishRequested) return;
      run.record.start.sent_epoch_ms = this.deps.now();
      run.startSent = true;
      await this.save(run.record);
      await opened.query(PFTP_QUERY_REQUEST_START_RECORDING, encodeStartRecordingParams(run.sessionId, 1));
      run.record.start.ack_epoch_ms = this.deps.now();
      run.record.transport.write_mode = opened.writeMode;
      await this.save(run.record);
      if (!run.finishRequested) {
        this.setStatus({ phase: "recording", sessionId: run.sessionId, error: null, sampleCount: null });
      }
    } catch (error) {
      const entry = this.addError(run.record, "start", error);
      await this.save(run.record);
      if (!run.finishRequested) {
        this.setStatus({ phase: "start_failed", sessionId: run.sessionId, error: entry, sampleCount: null });
      }
    } finally {
      client?.close();
    }
  }

  private async runFinish(run: Run): Promise<void> {
    const record = run.record;
    this.setStatus({ phase: "finishing", sessionId: run.sessionId, error: null, sampleCount: null });
    if (!run.startSent) {
      if (record.errors.length === 0) {
        const code = run.lastDevice ? "NOT_STARTED" : "NO_DEVICE";
        this.addError(record, "start", new StageError(code, "H10 recording was not started before the workout ended"));
      }
      record.status = "error";
      await this.save(record);
      this.setStatus({ phase: "failed", sessionId: run.sessionId, error: record.errors[0], sampleCount: null });
      await this.flushUploads();
      return;
    }

    const deadline = (record.stop.requested_epoch_ms ?? this.deps.now()) + H10_MEMORY_DEADLINE_MS;
    let client: H10PsftpLike | null = null;
    let abandoned = false;
    // Work on a draft: an attempt abandoned at the deadline must not overwrite the error record.
    const draft: H10MemoryRecord = JSON.parse(JSON.stringify(record));
    const attempt = async () => {
      const device = await this.ensureConnected(run, deadline);
      if (abandoned) throw new StageError("ABANDONED", "Attempt abandoned at deadline");
      const opened = this.openClient(run, device);
      client = opened;
      const status = decodeRecordingStatus(await opened.query(PFTP_QUERY_REQUEST_RECORDING_STATUS));
      // A retry after a successful stop sees recording off; keep the first observation.
      if (draft.stop.recording_on_before_stop === null) {
        draft.stop.recording_on_before_stop = status.recordingOn;
        draft.stop.sensor_identifier = status.identifier || null;
      }
      if (status.recordingOn) {
        draft.stop.sent_epoch_ms = this.deps.now();
        await opened.query(PFTP_QUERY_REQUEST_STOP_RECORDING);
        draft.stop.ack_epoch_ms = this.deps.now();
      }
      const files = await this.listSampleFiles(opened);
      draft.fetch.entries = files;
      const path = files.find((file) => identifierFromPath(file) === run.sessionId) ?? (files.length === 1 ? files[0] : null);
      if (!path) throw new StageError("NO_RECORDING_FOUND", `No ${SAMPLES_FILE} for this session on the sensor`);
      await this.fetchInto(opened, draft, path);
      if (abandoned) throw new StageError("ABANDONED", "Attempt abandoned at deadline");
      draft.status = "complete";
      draft.saved_epoch_ms = this.deps.now();
      draft.device_name = run.record.device_name;
      draft.transport = run.record.transport;
      try {
        await this.deps.store.write(draft);
      } catch (error) {
        throw new StageError("LOCAL_SAVE_FAILED", `Local record was not written: ${errorMessage(error)}`);
      }
    };
    const work = (async () => {
      for (let n = 1; ; n++) {
        draft.fetch.attempts = n;
        try {
          await attempt();
          return;
        } catch (error) {
          // The sensor answered (PFTP error) or the outcome is final: do not retry.
          if (error instanceof StageError || error instanceof PftpError || abandoned) throw error;
          if (draft.errors.length < 20) this.addError(draft, "fetch_attempt", error);
          (client as H10PsftpLike | null)?.close();
          client = null;
          if (this.deps.now() + FETCH_RETRY_DELAY_MS >= deadline) throw error;
          await this.deps.sleep(FETCH_RETRY_DELAY_MS);
        }
      }
    })();

    try {
      await withDeadline(work, deadline - this.deps.now(), "DEADLINE_10MIN", "Local save did not finish within 10 minutes");
      Object.assign(record, draft);
    } catch (error) {
      abandoned = true;
      (client as H10PsftpLike | null)?.close();
      client = null;
      work.catch(() => {}); // the abandoned attempt may still reject later
      record.status = "error";
      record.stop = draft.stop;
      record.fetch = { ...draft.fetch };
      record.errors = [...draft.errors];
      this.addError(record, "fetch", error);
    }

    // Saved locally or given up: the sensor slot is cleared either way.
    try {
      await withDeadline(this.removeFromSensor(run, client), SENSOR_CLEANUP_TIMEOUT_MS, "TIMEOUT", "Sensor cleanup timed out");
      record.sensor_removed = true;
    } catch (error) {
      this.addError(record, "sensor_remove", error);
    }
    (client as H10PsftpLike | null)?.close();
    await this.save(record);
    if (run.selfConnected) {
      await run.selfConnected.cancelConnection().catch(() => {});
    }

    const firstError = record.status === "error" ? record.errors[0] ?? null : null;
    this.setStatus({
      phase: record.status === "complete" ? "saved" : "failed",
      sessionId: run.sessionId,
      error: firstError,
      sampleCount: record.status === "complete" ? record.hr_samples.length : null,
    });
    await this.flushUploads();
  }

  private async removeFromSensor(run: Run, existing: H10PsftpLike | null): Promise<void> {
    let client = existing;
    let opened = false;
    if (!client) {
      const device = await this.ensureConnected(run, this.deps.now() + SENSOR_CLEANUP_TIMEOUT_MS);
      client = this.openClient(run, device);
      opened = true;
    }
    try {
      const status = decodeRecordingStatus(await client.query(PFTP_QUERY_REQUEST_RECORDING_STATUS));
      if (status.recordingOn) await client.query(PFTP_QUERY_REQUEST_STOP_RECORDING);
      const files = await this.listSampleFiles(client);
      const targets = files.filter((file) => identifierFromPath(file) === run.sessionId);
      const toRemove = targets.length > 0 ? targets : files.length === 1 ? files : [];
      for (const path of toRemove) {
        await client.request(encodeOperation(PFTP_COMMAND_REMOVE, path), 15_000, `remove ${path}`);
      }
    } finally {
      if (opened) client.close();
    }
  }
}

// ---------------------------------------------------------------------------
// Default wiring: expo-file-system (legacy API) store and existing session upload URL.
// ---------------------------------------------------------------------------

function storeDir(): string {
  return `${documentDirectory ?? ""}h10mem/`;
}

function recordUri(name: string, uploaded = false): string {
  return `${storeDir()}${safeName(name)}${uploaded ? ".uploaded" : ""}.json`;
}

async function readRecord(uri: string): Promise<H10MemoryRecord | null> {
  try {
    return JSON.parse(await readAsStringAsync(uri)) as H10MemoryRecord;
  } catch {
    return null;
  }
}

export const fileRecordStore: H10RecordStore = {
  async read(sessionId) {
    return (await readRecord(recordUri(sessionId))) ?? (await readRecord(recordUri(sessionId, true)));
  },
  async write(record) {
    await makeDirectoryAsync(storeDir(), { intermediates: true }).catch(() => {});
    const uri = recordUri(record.workout_session_id);
    await writeAsStringAsync(uri, JSON.stringify(record));
    return uri;
  },
  async listPendingUploads() {
    const names = await readDirectoryAsync(storeDir()).catch(() => [] as string[]);
    const pending: { record: H10MemoryRecord; uri: string }[] = [];
    for (const name of names) {
      if (!name.endsWith(".json") || name.endsWith(".uploaded.json")) continue;
      const uri = `${storeDir()}${name}`;
      const record = await readRecord(uri);
      if (record) pending.push({ record, uri });
    }
    return pending;
  },
  async markUploaded(record) {
    const to = recordUri(record.workout_session_id, true);
    await deleteAsync(to, { idempotent: true });
    await moveAsync({ from: recordUri(record.workout_session_id), to });
  },
};

export const H10MemoryRecorderInstance = new H10MemoryRecorder({
  now: () => Date.now(),
  sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  getDevice: () => PolarSensorRecorder.getDevice(),
  createClient: (device) => new PsftpClient(device),
  store: fileRecordStore,
  async upload(sessionId, profileId, fileUri) {
    const { upload_url } = await getUploadUrl(sessionId, H10_MEMORY_FILENAME, profileId);
    if (!upload_url) throw new Error("Missing H10 memory upload URL");
    await uploadSessionAssetToGcs(upload_url, fileUri, "application/json");
  },
});
