// The queue is a real read-modify-write target: back the mocks with an
// in-memory file map so re-reading the queue behaves like the device does.
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
  const queueFile = (subDir: string) => `/mock/docs/${subDir}/_pending.json`;
  const seedQueue = (subDir: string, entries: unknown[]) =>
    mockFiles.set(queueFile(subDir), JSON.stringify(entries));

  beforeEach(() => {
    jest.clearAllMocks();
    mockFiles.clear();
    mockGetInfoAsync.mockResolvedValue({ exists: true });
  });

  describe("createUploadQueue factory", () => {
    it("creates an isolated queue under its configured subDir", async () => {
      seedQueue("custom", []);

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
      seedQueue("custom_dir", []);

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
      // no queue file on disk at all

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
      seedQueue("test", queue);

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
      seedQueue("test", queue);

      const uploadFn = jest.fn().mockRejectedValue(new Error("Network disconnect"));
      const customQueue = createUploadQueue({
        subDir: "test",
        uploadFn,
      });

      await customQueue.flushPendingUploads();

      expect(uploadFn).toHaveBeenCalled();
      expect(mockDeleteAsync).not.toHaveBeenCalled();

      const savedQueue = JSON.parse(mockFiles.get(queueFile("test"))!);
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
      seedQueue("test", queue);

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

  describe("concurrent enqueue and flush", () => {
    // Lets the flush run until it is parked on the upload promise.
    const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

    it("keeps an entry enqueued while an earlier upload was in flight", async () => {
      seedQueue("race", [
        { sessionId: "sess-A", filePath: "/mock/docs/race/sess-A.json", attempts: 0 },
      ]);

      let releaseUpload: (() => void) | null = null;
      const uploadFn = jest.fn(
        () => new Promise<void>((resolve) => {
          releaseUpload = resolve;
        }),
      );
      const raceQueue = createUploadQueue({ subDir: "race", uploadFn });

      const flushing = raceQueue.flushPendingUploads();
      await settle();
      expect(uploadFn).toHaveBeenCalledTimes(1);

      // A new session finishes and enqueues while A is still uploading
      await raceQueue.enqueueUpload("sess-B", "/mock/docs/race/sess-B.json", 7);

      releaseUpload!();
      await flushing;

      const remaining = await raceQueue.loadQueue();
      expect(remaining.map((e) => e.sessionId)).toEqual(["sess-B"]);
      expect(remaining[0].profileId).toBe(7);
    });

    it("ignores a second flush while one is already running", async () => {
      seedQueue("race2", [
        { sessionId: "sess-A", filePath: "/mock/docs/race2/sess-A.json", attempts: 0 },
      ]);

      let releaseUpload: (() => void) | null = null;
      const uploadFn = jest.fn(
        () => new Promise<void>((resolve) => {
          releaseUpload = resolve;
        }),
      );
      const raceQueue = createUploadQueue({ subDir: "race2", uploadFn });

      const first = raceQueue.flushPendingUploads();
      await settle();
      await raceQueue.flushPendingUploads(); // must not upload sess-A twice

      releaseUpload!();
      await first;

      expect(uploadFn).toHaveBeenCalledTimes(1);
      expect(await raceQueue.loadQueue()).toEqual([]);
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
      seedQueue("debug", []);
      await enqueueUpload("debug-sess-1", "/mock/docs/debug/debug-sess-1.json");

      expect(mockWriteAsStringAsync).toHaveBeenCalledWith(
        "/mock/docs/debug/_pending.json",
        expect.stringContaining("debug-sess-1"),
      );

      // 2. Flush
      mockFiles.set("/mock/docs/debug/debug-sess-1.json", JSON.stringify(mockSession));
      mockUploadDebugTelemetry.mockResolvedValueOnce(undefined);

      await flushPendingUploads();

      expect(mockUploadDebugTelemetry).toHaveBeenCalledWith(mockSession);
      expect(mockDeleteAsync).toHaveBeenCalledWith("/mock/docs/debug/debug-sess-1.json", {
        idempotent: true,
      });
    });
  });
});
