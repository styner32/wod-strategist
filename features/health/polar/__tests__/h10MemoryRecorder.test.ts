import { Buffer } from "buffer";
import type { Device } from "react-native-ble-plx";

import {
  H10MemoryRecorder,
  H10_MEMORY_DEADLINE_MS,
  type H10MemoryRecord,
  type H10PsftpLike,
  type H10RecordStore,
} from "../h10MemoryRecorder";
import { PftpError, decodeFields } from "../psftpProtocol";

jest.mock("expo-constants", () => ({
  __esModule: true,
  default: { expoConfig: { version: "2.0.0-test" } },
}));

jest.mock("react-native", () => ({
  Platform: { OS: "ios" },
  TurboModuleRegistry: { get: jest.fn(() => null) },
}));

jest.mock("../../../wod/api", () => ({
  getUploadUrl: jest.fn(),
  uploadSessionAssetToGcs: jest.fn(),
}));

const SID = "WOD-20261005-01M3TG96JJQBNPE00V0CW4S44H";
const PROFILE = 7;

function varint(value: number): number[] {
  const out: number[] = [];
  let v = value;
  while (v > 0x7f) {
    out.push((v & 0x7f) | 0x80);
    v >>>= 7;
  }
  out.push(v);
  return out;
}

function lengthDelimited(field: number, body: number[]): number[] {
  return [(field << 3) | 2, ...varint(body.length), ...body];
}

function samplesFile(samples: number[]): Uint8Array {
  return Uint8Array.from([
    ...lengthDelimited(1, [0x18, 0x01]),
    ...lengthDelimited(2, samples.flatMap(varint)),
  ]);
}

function directory(names: string[]): Uint8Array {
  return Uint8Array.from(names.flatMap((name) => lengthDelimited(1, lengthDelimited(1, Array.from(Buffer.from(name))))));
}

/** In-memory H10 exercise recording slot with PS-FTP semantics used by the recorder. */
class FakeSensor {
  recordingOn = false;
  identifier = "";
  files = new Map<string, Uint8Array>();
  samples = [80, 81, 0, 83];
  hangGet = false;
  failGetOnce: unknown = null;
  starts = 0;
}

class FakeClient implements H10PsftpLike {
  readonly packetSize = 20;
  readonly writeMode = "without_response";
  constructor(private readonly sensor: FakeSensor) {}
  open(): void {}
  close(): void {}

  async query(id: number, params?: Uint8Array): Promise<Uint8Array> {
    const s = this.sensor;
    if (id === 16) {
      return Uint8Array.from([0x08, s.recordingOn ? 1 : 0, ...lengthDelimited(2, Array.from(Buffer.from(s.identifier)))]);
    }
    if (id === 14) {
      if (s.recordingOn || s.files.size > 0) throw new PftpError(206);
      const identifier = decodeFields(params!).find((f) => f.field === 3)!.bytes!;
      s.identifier = Buffer.from(identifier).toString("utf8");
      s.recordingOn = true;
      s.starts += 1;
      return new Uint8Array();
    }
    if (id === 15) {
      s.recordingOn = false;
      s.files.set(`/${s.identifier}/SAMPLES.BPB`, samplesFile(s.samples));
      return new Uint8Array();
    }
    throw new PftpError(101);
  }

  async request(header: Uint8Array): Promise<Uint8Array> {
    const s = this.sensor;
    const fields = decodeFields(header);
    const command = fields.find((f) => f.field === 1)?.varint;
    const path = Buffer.from(fields.find((f) => f.field === 2)!.bytes!).toString("utf8");
    if (command === 0 && path.endsWith("/")) {
      const names = new Set<string>();
      for (const file of s.files.keys()) {
        if (!file.startsWith(path)) continue;
        const rest = file.slice(path.length);
        names.add(rest.includes("/") ? `${rest.split("/")[0]}/` : rest);
      }
      return directory([...names]);
    }
    if (command === 0) {
      if (s.hangGet) return new Promise<Uint8Array>(() => {});
      if (s.failGetOnce) {
        const error = s.failGetOnce;
        s.failGetOnce = null;
        throw error;
      }
      const file = s.files.get(path);
      if (!file) throw new PftpError(103);
      return file;
    }
    if (command === 3) {
      if (!s.files.delete(path)) throw new PftpError(103);
      return new Uint8Array();
    }
    throw new PftpError(101);
  }
}

function memoryStore() {
  const clone = (r: H10MemoryRecord): H10MemoryRecord => JSON.parse(JSON.stringify(r));
  const records = new Map<string, H10MemoryRecord>();
  const uploaded = new Set<string>();
  const state = { failComplete: false };
  const store: H10RecordStore = {
    async read(id) {
      const r = records.get(id);
      return r ? clone(r) : null;
    },
    async write(record) {
      if (state.failComplete && record.status === "complete") throw new Error("disk full");
      records.set(record.workout_session_id, clone(record));
      return `mem://${record.workout_session_id}`;
    },
    async listPendingUploads() {
      return [...records.values()]
        .filter((r) => !uploaded.has(r.workout_session_id))
        .map((record) => ({ record: clone(record), uri: `mem://${record.workout_session_id}` }));
    },
    async markUploaded(record) {
      uploaded.add(record.workout_session_id);
    },
  };
  return { store, records, uploaded, state };
}

function fakeDevice(): Device {
  return {
    name: "Polar H10 TEST",
    isConnected: jest.fn(async () => true),
    connect: jest.fn(),
    discoverAllServicesAndCharacteristics: jest.fn(),
    cancelConnection: jest.fn(async () => undefined),
  } as unknown as Device;
}

function setup(options: { device?: Device | null } = {}) {
  const sensor = new FakeSensor();
  const mem = memoryStore();
  const upload = jest.fn(async () => undefined);
  const device = options.device === undefined ? fakeDevice() : options.device;
  const recorder = new H10MemoryRecorder({
    now: () => Date.now(),
    sleep: (ms) => new Promise((resolve) => setTimeout(resolve, Math.min(ms, 5))),
    getDevice: () => device,
    createClient: () => new FakeClient(sensor),
    store: mem.store,
    upload,
  });
  return { sensor, mem, upload, recorder };
}

async function waitForPhase(recorder: H10MemoryRecorder, phase: string): Promise<void> {
  for (let i = 0; i < 200; i++) {
    if (recorder.getStatus().phase === phase) return;
    await new Promise((resolve) => setTimeout(resolve, 1));
  }
  throw new Error(`phase ${phase} not reached (now ${recorder.getStatus().phase})`);
}

describe("H10MemoryRecorder", () => {
  afterEach(() => {
    jest.useRealTimers();
  });

  it("records, retrieves, saves locally, removes from the sensor and uploads", async () => {
    const { sensor, mem, upload, recorder } = setup();
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(recorder, "recording");
    expect(sensor.recordingOn).toBe(true);
    expect(sensor.identifier).toBe(SID);

    await recorder.finish(SID);

    const record = mem.records.get(SID)!;
    expect(record.status).toBe("complete");
    expect(record.hr_samples).toEqual([80, 81, 0, 83]);
    expect(record.recording_interval_ms).toBe(1000);
    expect(record.fetch.entry_path).toBe(`/${SID}/SAMPLES.BPB`);
    expect(record.fetch.attempts).toBe(1);
    expect(record.stop.recording_on_before_stop).toBe(true);
    expect(record.start.ack_epoch_ms).not.toBeNull();
    expect(record.sensor_removed).toBe(true);
    expect(record.errors).toEqual([]);
    expect(sensor.files.size).toBe(0);
    expect(upload).toHaveBeenCalledWith(SID, PROFILE, `mem://${SID}`);
    expect(mem.uploaded.has(SID)).toBe(true);
    expect(recorder.getStatus()).toMatchObject({ phase: "saved", sessionId: SID, sampleCount: 4 });
  });

  it("recovers a leftover recording before starting; orphans are kept locally but never uploaded", async () => {
    const { sensor, mem, upload, recorder } = setup();
    sensor.files.set("/OLD-SESSION/SAMPLES.BPB", samplesFile([70, 71]));
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(recorder, "recording");

    const orphan = [...mem.records.values()].find((r) => r.recording_identifier === "OLD-SESSION")!;
    expect(orphan.profile_id).toBeNull();
    expect(orphan.hr_samples).toEqual([70, 71]);
    expect(orphan.recovered_later).toBe(true);
    expect(orphan.sensor_removed).toBe(true);
    expect(sensor.files.has("/OLD-SESSION/SAMPLES.BPB")).toBe(false);

    await recorder.finish(SID);
    expect(mem.records.get(SID)!.leftovers).toEqual([
      { path: "/OLD-SESSION/SAMPLES.BPB", identifier: "OLD-SESSION", action: "recovered" },
    ]);
    expect(upload).toHaveBeenCalledTimes(1);
    expect(upload).toHaveBeenCalledWith(SID, PROFILE, `mem://${SID}`);
  });

  it("recovers an unfinished previous session with its own profile and uploads it", async () => {
    const { sensor, mem, upload, recorder } = setup();
    const previous = "WOD-20261004-01M3TG96JJQBNPE00V0CW4S44A";
    recorder.begin({ sessionId: previous, profileId: PROFILE, baseEpochMs: 500 });
    await waitForPhase(recorder, "recording");
    // The app is killed: finish() never runs. The next begin finishes the stale run first,
    // so simulate a fresh process instead by creating a second recorder over the same sensor.
    const next = new H10MemoryRecorder({
      now: () => Date.now(),
      sleep: (ms) => new Promise((resolve) => setTimeout(resolve, Math.min(ms, 5))),
      getDevice: () => fakeDevice(),
      createClient: () => new FakeClient(sensor),
      store: mem.store,
      upload,
    });
    next.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(next, "recording");

    const recovered = mem.records.get(previous)!;
    expect(recovered).toMatchObject({ status: "complete", profile_id: PROFILE, recovered_later: true, sensor_removed: true });
    await next.finish(SID);
    expect(upload).toHaveBeenCalledWith(previous, PROFILE, `mem://${previous}`);
    expect(mem.records.get(SID)!.status).toBe("complete");
  });

  it("keeps only the error and still removes the sensor recording when the local save fails", async () => {
    const { sensor, mem, recorder } = setup();
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(recorder, "recording");
    mem.state.failComplete = true;

    await recorder.finish(SID);

    const record = mem.records.get(SID)!;
    expect(record.status).toBe("error");
    expect(record.hr_samples).toEqual([]);
    expect(record.errors).toEqual([expect.objectContaining({ stage: "fetch", code: "LOCAL_SAVE_FAILED" })]);
    expect(record.sensor_removed).toBe(true);
    expect(sensor.files.size).toBe(0);
    expect(recorder.getStatus()).toMatchObject({ phase: "failed", error: expect.objectContaining({ code: "LOCAL_SAVE_FAILED" }) });
  });

  it("retries a transient BLE failure during retrieval", async () => {
    const { sensor, mem, recorder } = setup();
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(recorder, "recording");
    sensor.failGetOnce = Object.assign(new Error("Device disconnected"), { errorCode: 201 });

    await recorder.finish(SID);

    const record = mem.records.get(SID)!;
    expect(record.status).toBe("complete");
    expect(record.fetch.attempts).toBe(2);
    expect(record.errors).toEqual([expect.objectContaining({ stage: "fetch_attempt", code: "BLE_201" })]);
    expect(sensor.files.size).toBe(0);
  });

  it("gives up after 10 minutes, records the timeout and removes the sensor recording", async () => {
    jest.useFakeTimers();
    const { sensor, mem, recorder } = setup();
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await jest.advanceTimersByTimeAsync(10);
    expect(recorder.getStatus().phase).toBe("recording");
    sensor.hangGet = true;

    const done = recorder.finish(SID);
    await jest.advanceTimersByTimeAsync(H10_MEMORY_DEADLINE_MS + 1000);
    await done;

    const record = mem.records.get(SID)!;
    expect(record.status).toBe("error");
    expect(record.errors).toEqual([expect.objectContaining({ stage: "fetch", code: "DEADLINE_10MIN" })]);
    expect(record.sensor_removed).toBe(true);
    expect(sensor.files.size).toBe(0);
  });

  it("records NO_DEVICE when the strap never connected", async () => {
    const { mem, upload, recorder } = setup({ device: null });
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(recorder, "waiting_device");

    await recorder.finish(SID);

    const record = mem.records.get(SID)!;
    expect(record.status).toBe("error");
    expect(record.errors).toEqual([expect.objectContaining({ stage: "start", code: "NO_DEVICE" })]);
    expect(upload).toHaveBeenCalledWith(SID, PROFILE, `mem://${SID}`);
    expect(recorder.getStatus().phase).toBe("failed");
  });

  it("reports a start failure without throwing", async () => {
    const { mem, recorder } = setup();
    jest.spyOn(FakeClient.prototype, "query").mockImplementation(async (id: number) => {
      if (id === 16) return Uint8Array.from([0x08, 0x00]);
      throw new PftpError(209);
    });
    recorder.begin({ sessionId: SID, profileId: PROFILE, baseEpochMs: 1000 });
    await waitForPhase(recorder, "start_failed");
    expect(recorder.getStatus().error).toMatchObject({ stage: "start", code: "PFTP_209_BATTERY_TOO_LOW" });
    expect(mem.records.get(SID)!.errors).toEqual([expect.objectContaining({ code: "PFTP_209_BATTERY_TOO_LOW" })]);
    jest.restoreAllMocks();
  });
});
