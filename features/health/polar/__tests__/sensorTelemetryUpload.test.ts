const mockReadAsStringAsync = jest.fn();
const mockWriteAsStringAsync = jest.fn().mockResolvedValue(undefined);
const mockDeleteAsync = jest.fn().mockResolvedValue(undefined);
const mockGetInfoAsync = jest.fn().mockResolvedValue({ exists: true });
const mockMakeDirectoryAsync = jest.fn().mockResolvedValue(undefined);

jest.mock("expo-file-system/legacy", () => ({
  documentDirectory: "/mock/docs/",
  readAsStringAsync: (...args: unknown[]) => mockReadAsStringAsync(...args),
  writeAsStringAsync: (...args: unknown[]) => mockWriteAsStringAsync(...args),
  deleteAsync: (...args: unknown[]) => mockDeleteAsync(...args),
  getInfoAsync: (...args: unknown[]) => mockGetInfoAsync(...args),
  makeDirectoryAsync: (...args: unknown[]) => mockMakeDirectoryAsync(...args),
}));

const mockGetUploadUrl = jest.fn();
const mockUploadToGcs = jest.fn();

jest.mock("../../../wod/api", () => ({
  getUploadUrl: (...args: unknown[]) => mockGetUploadUrl(...args),
  uploadToGcs: (...args: unknown[]) => mockUploadToGcs(...args),
}));

import {
  enqueueSensorUpload,
  flushSensorUploads,
  uploadSensorTelemetry,
} from "../sensorTelemetryUpload";

describe("sensorTelemetryUpload", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetInfoAsync.mockResolvedValue({ exists: true });
  });

  describe("uploadSensorTelemetry", () => {
    it("obtains signed upload url and uploads to GCS with application/x-ndjson", async () => {
      mockGetUploadUrl.mockResolvedValueOnce({
        upload_url: "https://storage.googleapis.com/signed-url",
      });
      mockUploadToGcs.mockResolvedValueOnce(undefined);

      await uploadSensorTelemetry("session-test-01", 42, "/mock/docs/sensor/session-test-01.ndjson");

      expect(mockGetUploadUrl).toHaveBeenCalledWith(
        "session-test-01",
        "sensor_telemetry.ndjson",
        42,
      );
      expect(mockUploadToGcs).toHaveBeenCalledWith(
        "https://storage.googleapis.com/signed-url",
        "/mock/docs/sensor/session-test-01.ndjson",
        "application/x-ndjson",
      );
    });
  });

  describe("sensor upload queue", () => {
    it("enqueues upload entry and saves queue to disk", async () => {
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify([]));

      await enqueueSensorUpload(
        "session-enqueue",
        15,
        "/mock/docs/sensor/session-enqueue.ndjson",
      );

      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/sensor/_pending.json",
        JSON.stringify([
          {
            sessionId: "session-enqueue",
            filePath: "/mock/docs/sensor/session-enqueue.ndjson",
            attempts: 0,
            profileId: 15,
          },
        ]),
      );
    });

    it("flushes queue successfully: uploads and deletes local file", async () => {
      const queue = [
        {
          sessionId: "session-ok",
          filePath: "/mock/docs/sensor/session-ok.ndjson",
          attempts: 0,
          profileId: 10,
        },
      ];
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify(queue));
      mockGetUploadUrl.mockResolvedValueOnce({ upload_url: "https://gcs.com/signed" });
      mockUploadToGcs.mockResolvedValueOnce(undefined);

      await flushSensorUploads();

      expect(mockGetUploadUrl).toHaveBeenCalledWith("session-ok", "sensor_telemetry.ndjson", 10);
      expect(mockUploadToGcs).toHaveBeenCalledWith(
        "https://gcs.com/signed",
        "/mock/docs/sensor/session-ok.ndjson",
        "application/x-ndjson",
      );
      expect(mockDeleteAsync).toHaveBeenCalledWith(
        "/mock/docs/sensor/session-ok.ndjson",
        { idempotent: true },
      );

      // Remaining queue is now empty
      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/sensor/_pending.json",
        JSON.stringify([]),
      );
    });

    it("handles upload failure: increments attempts, retains in queue, does not delete file", async () => {
      const queue = [
        {
          sessionId: "session-fail",
          filePath: "/mock/docs/sensor/session-fail.ndjson",
          attempts: 1,
          profileId: 7,
        },
      ];
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify(queue));
      mockGetUploadUrl.mockRejectedValueOnce(new Error("Network timeout"));

      await flushSensorUploads();

      expect(mockDeleteAsync).not.toHaveBeenCalled();

      // Saved queue should have attempts = 2
      const [savedPath, savedContent] = mockWriteAsStringAsync.mock.calls[0];
      expect(savedPath).toBe("/mock/docs/sensor/_pending.json");
      const savedQueue = JSON.parse(savedContent);
      expect(savedQueue.length).toBe(1);
      expect(savedQueue[0].sessionId).toBe("session-fail");
      expect(savedQueue[0].attempts).toBe(2);
      expect(savedQueue[0].lastAttemptAt).toBeDefined();
    });

    it("drops entries that reach MAX_ATTEMPTS (5)", async () => {
      const queue = [
        {
          sessionId: "session-maxed",
          filePath: "/mock/docs/sensor/session-maxed.ndjson",
          attempts: 5,
          profileId: 99,
        },
      ];
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify(queue));

      await flushSensorUploads();

      // upload should not even be attempted
      expect(mockGetUploadUrl).not.toHaveBeenCalled();
      expect(mockUploadToGcs).not.toHaveBeenCalled();
      // dropped from queue -> saved as empty
      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/sensor/_pending.json",
        JSON.stringify([]),
      );
    });
  });
});
