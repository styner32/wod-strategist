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

const mockUploadDebugTelemetry = jest.fn();

jest.mock("../../wod/api", () => ({
  uploadDebugTelemetry: (...args: unknown[]) => mockUploadDebugTelemetry(...args),
}));

import {
  createUploadQueue,
  enqueueUpload,
  flushPendingUploads,
  MAX_ATTEMPTS,
} from "../telemetryUpload";

describe("telemetryUpload", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetInfoAsync.mockResolvedValue({ exists: true });
  });

  describe("createUploadQueue factory", () => {
    it("creates an isolated queue under its configured subDir", async () => {
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify([]));

      const uploadFn = jest.fn().mockResolvedValue(undefined);
      const customQueue = createUploadQueue({
        subDir: "custom",
        uploadFn,
      });

      await customQueue.enqueueUpload("session-abc", "/mock/docs/custom/file.json", 3);

      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/custom/_pending.json",
        JSON.stringify([
          {
            sessionId: "session-abc",
            filePath: "/mock/docs/custom/file.json",
            attempts: 0,
            profileId: 3,
          },
        ]),
      );
    });

    it("makes directory if queue directory does not exist", async () => {
      mockGetInfoAsync.mockResolvedValueOnce({ exists: false });
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify([]));

      const customQueue = createUploadQueue({
        subDir: "custom_dir",
        uploadFn: jest.fn(),
      });

      await customQueue.enqueueUpload("session-dir", "/mock/file.json");

      expect(mockMakeDirectoryAsync).toHaveBeenCalledWith("/mock/docs/custom_dir/", {
        intermediates: true,
      });
    });

    it("handles missing or corrupt queue file gracefully by starting empty", async () => {
      mockReadAsStringAsync.mockRejectedValueOnce(new Error("File not found"));

      const customQueue = createUploadQueue({
        subDir: "corrupt",
        uploadFn: jest.fn(),
      });

      const loaded = await customQueue.loadQueue();
      expect(loaded).toEqual([]);
    });

    it("flushes pending uploads: on success, deletes file and empties queue", async () => {
      const queue = [
        {
          sessionId: "sess-ok",
          filePath: "/mock/docs/test/sess-ok.json",
          attempts: 0,
        },
      ];
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify(queue));

      const uploadFn = jest.fn().mockResolvedValue(undefined);
      const customQueue = createUploadQueue({
        subDir: "test",
        uploadFn,
      });

      await customQueue.flushPendingUploads();

      expect(uploadFn).toHaveBeenCalledWith(queue[0]);
      expect(mockDeleteAsync).toHaveBeenCalledWith("/mock/docs/test/sess-ok.json", {
        idempotent: true,
      });
      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/test/_pending.json",
        JSON.stringify([]),
      );
    });

    it("flushes pending uploads: on failure, increments attempts and preserves queue and file", async () => {
      const queue = [
        {
          sessionId: "sess-err",
          filePath: "/mock/docs/test/sess-err.json",
          attempts: 1,
        },
      ];
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify(queue));

      const uploadFn = jest.fn().mockRejectedValue(new Error("Network disconnect"));
      const customQueue = createUploadQueue({
        subDir: "test",
        uploadFn,
      });

      await customQueue.flushPendingUploads();

      expect(uploadFn).toHaveBeenCalled();
      expect(mockDeleteAsync).not.toHaveBeenCalled();

      const [savedPath, savedContent] = mockWriteAsStringAsync.mock.calls[0];
      expect(savedPath).toBe("/mock/docs/test/_pending.json");
      const savedQueue = JSON.parse(savedContent);
      expect(savedQueue.length).toBe(1);
      expect(savedQueue[0].sessionId).toBe("sess-err");
      expect(savedQueue[0].attempts).toBe(2);
      expect(savedQueue[0].lastAttemptAt).toBeDefined();
    });

    it("drops entries exceeding MAX_ATTEMPTS without retrying", async () => {
      const queue = [
        {
          sessionId: "sess-max",
          filePath: "/mock/docs/test/sess-max.json",
          attempts: MAX_ATTEMPTS,
        },
      ];
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify(queue));

      const uploadFn = jest.fn();
      const customQueue = createUploadQueue({
        subDir: "test",
        uploadFn,
      });

      await customQueue.flushPendingUploads();

      expect(uploadFn).not.toHaveBeenCalled();
      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/test/_pending.json",
        JSON.stringify([]),
      );
    });
  });

  describe("default debug queue exports", () => {
    it("enqueues to debug queue and flushes debug telemetry session", async () => {
      const mockSession = {
        sessionId: "debug-sess-1",
        profileId: 100,
        startedAt: 1000,
        endedAt: 2000,
        samples: [],
      };

      // 1. Enqueue
      mockReadAsStringAsync.mockResolvedValueOnce(JSON.stringify([]));
      await enqueueUpload("debug-sess-1", "/mock/docs/debug/debug-sess-1.json");

      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/debug/_pending.json",
        expect.stringContaining("debug-sess-1"),
      );

      // 2. Flush
      mockReadAsStringAsync
        .mockResolvedValueOnce(
          JSON.stringify([
            {
              sessionId: "debug-sess-1",
              filePath: "/mock/docs/debug/debug-sess-1.json",
              attempts: 0,
            },
          ]),
        )
        .mockResolvedValueOnce(JSON.stringify(mockSession)); // read session file
      mockUploadDebugTelemetry.mockResolvedValueOnce(undefined);

      await flushPendingUploads();

      expect(mockUploadDebugTelemetry).toHaveBeenCalledWith(mockSession);
      expect(mockDeleteAsync).toHaveBeenCalledWith("/mock/docs/debug/debug-sess-1.json", {
        idempotent: true,
      });
    });
  });
});
