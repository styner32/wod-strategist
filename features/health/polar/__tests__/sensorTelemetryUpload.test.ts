const mockFiles = new Map<string, string>();

const mockReadAsStringAsync = jest.fn(async (...args: unknown[]) => {
  const path = args[0] as string;
  const content = mockFiles.get(path);
  if (content === undefined) throw new Error(`File not found: ${path}`);
  return content;
});

const mockWriteAsStringAsync = jest.fn(async (...args: unknown[]) => {
  mockFiles.set(args[0] as string, args[1] as string);
});

const mockDeleteAsync = jest.fn(async (...args: unknown[]) => {
  mockFiles.delete(args[0] as string);
});

const mockGetInfoAsync = jest.fn(async (path: string) => {
  const exists = mockFiles.has(path);
  return { exists, size: exists ? (mockFiles.get(path)?.length ?? 0) : 0 };
});

const mockMakeDirectoryAsync = jest.fn().mockResolvedValue(undefined);

jest.mock("expo-file-system/legacy", () => ({
  documentDirectory: "/mock/docs/",
  readAsStringAsync: (...args: any[]) => (mockReadAsStringAsync as any)(...args),
  writeAsStringAsync: (...args: any[]) => (mockWriteAsStringAsync as any)(...args),
  deleteAsync: (...args: any[]) => (mockDeleteAsync as any)(...args),
  getInfoAsync: (...args: any[]) => (mockGetInfoAsync as any)(...args),
  makeDirectoryAsync: (...args: any[]) => (mockMakeDirectoryAsync as any)(...args),
}));

const mockPrepareSensorUpload = jest.fn();
const mockCompleteSensorUpload = jest.fn();
const mockUploadSensorToGcs = jest.fn();
const mockGetUploadUrl = jest.fn();
const mockUploadToGcs = jest.fn();

jest.mock("../../../wod/api", () => ({
  prepareSensorUpload: (...args: any[]) => (mockPrepareSensorUpload as any)(...args),
  completeSensorUpload: (...args: any[]) => (mockCompleteSensorUpload as any)(...args),
  uploadSensorToGcs: (...args: any[]) => (mockUploadSensorToGcs as any)(...args),
  getUploadUrl: (...args: any[]) => (mockGetUploadUrl as any)(...args),
  uploadToGcs: (...args: any[]) => (mockUploadToGcs as any)(...args),
}));

import {
  computeSha256,
  enqueueSensorUpload,
  flushSensorUploads,
  loadQueue,
  saveQueue,
  uploadSensorTelemetry,
  type SensorQueueEntry,
} from "../sensorTelemetryUpload";

describe("sensorTelemetryUpload", () => {
  const QUEUE_PATH = "/mock/docs/sensor/_pending.json";
  const BAK_PATH = "/mock/docs/sensor/_pending.json.bak";
  const FILE_PATH = "/mock/docs/sensor/session-01.ndjson";
  const SAMPLE_NDJSON = '{"type":"HR","ts":1000,"bpm":150}\n';

  beforeEach(() => {
    jest.clearAllMocks();
    mockFiles.clear();
    mockFiles.set(FILE_PATH, SAMPLE_NDJSON);
    mockGetInfoAsync.mockImplementation(async (path: string) => {
      const exists = mockFiles.has(path);
      return { exists, size: exists ? (mockFiles.get(path)?.length ?? 0) : 0 };
    });
  });

  describe("uploadSensorTelemetry", () => {
    it("performs full prepare -> PUT -> complete cycle", async () => {
      mockPrepareSensorUpload.mockResolvedValueOnce({
        request_id: "req-123",
        version: "1",
        state: "UPLOADING",
        object_name: "videos/42/session-test-01/sensor_telemetry_v1_req-123.ndjson",
        upload_url: "https://storage.googleapis.com/signed-url",
        required_headers: { "x-goog-if-generation-match": "0" },
      });
      mockUploadSensorToGcs.mockResolvedValueOnce(undefined);
      mockCompleteSensorUpload.mockResolvedValueOnce({
        accepted: true,
        request_id: "req-123",
        version: "1",
        state: "PENDING",
        retryable: false,
      });

      await uploadSensorTelemetry("session-test-01", 42, FILE_PATH);

      expect(mockPrepareSensorUpload).toHaveBeenCalledWith(
        "session-test-01",
        expect.objectContaining({
          profile_id: 42,
          expected_version: "0",
          size_bytes: SAMPLE_NDJSON.length,
        }),
      );
      expect(mockUploadSensorToGcs).toHaveBeenCalledWith(
        "https://storage.googleapis.com/signed-url",
        FILE_PATH,
        { "x-goog-if-generation-match": "0" },
      );
      expect(mockCompleteSensorUpload).toHaveBeenCalledWith(
        "session-test-01",
        expect.objectContaining({
          profile_id: 42,
          version: "1",
        }),
      );
    });
  });

  describe("sensor upload queue lifecycle", () => {
    it("enqueues upload entry with UUID, PREPARE_PENDING stage, and sha256", async () => {
      await enqueueSensorUpload("session-enqueue", 15, FILE_PATH);

      const savedJson = mockFiles.get(QUEUE_PATH);
      expect(savedJson).toBeDefined();
      const entries: SensorQueueEntry[] = JSON.parse(savedJson!);
      expect(entries.length).toBe(1);
      expect(entries[0].sessionId).toBe("session-enqueue");
      expect(entries[0].profileId).toBe(15);
      expect(entries[0].stage).toBe("PREPARE_PENDING");
      expect(entries[0].requestId).toMatch(
        /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/,
      );
      expect(entries[0].expectedVersion).toBe("0");
      expect(entries[0].sha256).toBe(await computeSha256(SAMPLE_NDJSON));
    });

    it("flushes queue successfully: PREPARE -> PUT -> COMPLETE -> ACCEPTED and deletes local file", async () => {
      await enqueueSensorUpload("session-flush", 10, FILE_PATH);

      mockPrepareSensorUpload.mockResolvedValueOnce({
        request_id: "req-abc",
        version: "1",
        state: "UPLOADING",
        object_name: "videos/10/session-flush/sensor_telemetry_v1_req-abc.ndjson",
        upload_url: "https://storage.googleapis.com/upload-target",
        required_headers: { "x-goog-if-generation-match": "0" },
      });
      mockUploadSensorToGcs.mockResolvedValueOnce(undefined);
      mockCompleteSensorUpload.mockResolvedValueOnce({
        accepted: true,
        request_id: "req-abc",
        version: "1",
        state: "PENDING",
        retryable: false,
      });

      await flushSensorUploads();

      expect(mockPrepareSensorUpload).toHaveBeenCalled();
      expect(mockUploadSensorToGcs).toHaveBeenCalledWith(
        "https://storage.googleapis.com/upload-target",
        FILE_PATH,
        { "x-goog-if-generation-match": "0" },
      );
      expect(mockCompleteSensorUpload).toHaveBeenCalled();
      expect(mockDeleteAsync).toHaveBeenCalledWith(FILE_PATH, { idempotent: true });

      const finalQueue = JSON.parse(mockFiles.get(QUEUE_PATH)!);
      expect(finalQueue.length).toBe(0);
    });

    it("U1: PUT failure / 412 proceeds to COMPLETE_PENDING to check with server", async () => {
      const entry: SensorQueueEntry = {
        sessionId: "session-u1",
        profileId: 10,
        filePath: FILE_PATH,
        requestId: "req-u1",
        expectedVersion: "0",
        sizeBytes: SAMPLE_NDJSON.length,
        sha256: "hash123",
        stage: "PUT_PENDING",
        uploadUrl: "https://storage.googleapis.com/u1",
        attempts: 0,
        createdAt: Date.now(),
      };
      mockFiles.set(QUEUE_PATH, JSON.stringify([entry]));

      const err: any = new Error("Precondition Failed");
      err.status = 412;
      mockUploadSensorToGcs.mockRejectedValueOnce(err);

      await flushSensorUploads();

      const savedQueue: SensorQueueEntry[] = JSON.parse(mockFiles.get(QUEUE_PATH)!);
      expect(savedQueue.length).toBe(1);
      expect(savedQueue[0].stage).toBe("COMPLETE_PENDING");
    });

    it("U2: complete confirms UPLOAD_NOT_FOUND -> transitions back to PUT_PENDING", async () => {
      const entry: SensorQueueEntry = {
        sessionId: "session-u2",
        profileId: 10,
        filePath: FILE_PATH,
        requestId: "req-u2",
        expectedVersion: "0",
        serverVersion: "1",
        sizeBytes: SAMPLE_NDJSON.length,
        sha256: "hash123",
        stage: "COMPLETE_PENDING",
        uploadUrl: "https://storage.googleapis.com/u2",
        attempts: 0,
        createdAt: Date.now(),
      };
      mockFiles.set(QUEUE_PATH, JSON.stringify([entry]));

      mockCompleteSensorUpload.mockResolvedValueOnce({
        accepted: false,
        error_code: "UPLOAD_NOT_FOUND",
        retryable: true,
        state: "UPLOADING",
      });

      await flushSensorUploads();

      const savedQueue: SensorQueueEntry[] = JSON.parse(mockFiles.get(QUEUE_PATH)!);
      expect(savedQueue.length).toBe(1);
      expect(savedQueue[0].stage).toBe("PUT_PENDING");
    });

    it("U3: 24 hours exceeded -> transitions to NEEDS_ATTENTION, does NOT delete file", async () => {
      const oldTime = Date.now() - 25 * 60 * 60 * 1000;
      const entry: SensorQueueEntry = {
        sessionId: "session-old",
        profileId: 10,
        filePath: FILE_PATH,
        requestId: "req-old",
        expectedVersion: "0",
        sizeBytes: SAMPLE_NDJSON.length,
        sha256: "hash123",
        stage: "PREPARE_PENDING",
        attempts: 3,
        createdAt: oldTime,
      };
      mockFiles.set(QUEUE_PATH, JSON.stringify([entry]));

      await flushSensorUploads();

      expect(mockDeleteAsync).not.toHaveBeenCalledWith(FILE_PATH, expect.anything());
      const savedQueue: SensorQueueEntry[] = JSON.parse(mockFiles.get(QUEUE_PATH)!);
      expect(savedQueue.length).toBe(1);
      expect(savedQueue[0].stage).toBe("NEEDS_ATTENTION");
    });

    it("U3: 409 conflict -> marks SUPERSEDED, does not auto re-request", async () => {
      const entry: SensorQueueEntry = {
        sessionId: "session-conflict",
        profileId: 10,
        filePath: FILE_PATH,
        requestId: "req-conflict",
        expectedVersion: "0",
        sizeBytes: SAMPLE_NDJSON.length,
        sha256: "hash123",
        stage: "PREPARE_PENDING",
        attempts: 0,
        createdAt: Date.now(),
      };
      mockFiles.set(QUEUE_PATH, JSON.stringify([entry]));

      const err: any = new Error("SENSOR_VERSION_CONFLICT");
      err.status = 409;
      mockPrepareSensorUpload.mockRejectedValueOnce(err);

      await flushSensorUploads();

      const savedQueue: SensorQueueEntry[] = JSON.parse(mockFiles.get(QUEUE_PATH)!);
      expect(savedQueue.length).toBe(1);
      expect(savedQueue[0].stage).toBe("SUPERSEDED");
    });

    it("U4: recovers from backup when main queue file is corrupted", async () => {
      const validEntry: SensorQueueEntry = {
        sessionId: "session-bak",
        profileId: 10,
        filePath: FILE_PATH,
        requestId: "req-bak",
        expectedVersion: "0",
        sizeBytes: SAMPLE_NDJSON.length,
        sha256: "hash123",
        stage: "PREPARE_PENDING",
        attempts: 0,
        createdAt: Date.now(),
      };
      mockFiles.set(QUEUE_PATH, "{ corrupted json !!!");
      mockFiles.set(BAK_PATH, JSON.stringify([validEntry]));

      const loaded = await loadQueue();
      expect(loaded.length).toBe(1);
      expect(loaded[0].sessionId).toBe("session-bak");
      expect(mockFiles.get(QUEUE_PATH)).toBe(JSON.stringify([validEntry]));
    });

    it("U4: throws error when both primary and backup are corrupted", async () => {
      mockFiles.set(QUEUE_PATH, "{ corrupt 1");
      mockFiles.set(BAK_PATH, "{ corrupt 2");

      await expect(loadQueue()).rejects.toThrow("Corrupted sensor upload queue file");
    });

    it("migrates legacy queue entries lacking stage or requestId", async () => {
      const legacyEntry = {
        sessionId: "session-legacy",
        profileId: 77,
        filePath: FILE_PATH,
        attempts: 1,
      };
      mockFiles.set(QUEUE_PATH, JSON.stringify([legacyEntry]));

      const loaded = await loadQueue();
      expect(loaded.length).toBe(1);
      expect(loaded[0].sessionId).toBe("session-legacy");
      expect(loaded[0].profileId).toBe(77);
      expect(loaded[0].stage).toBe("PREPARE_PENDING");
      expect(loaded[0].requestId).toBeDefined();
    });
  });
});
