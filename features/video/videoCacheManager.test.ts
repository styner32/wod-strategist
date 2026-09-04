import * as FileSystem from "expo-file-system/legacy";
import {
  clearAllVideoCache,
  deleteCachedVideo,
  formatBytes,
  getCachedVideoUri,
  getVideoCacheFilename,
  getVideoCacheStats,
  getVideoCacheUri,
  isSessionVideoCached,
  listCachedVideos,
  pruneVideoCacheIfNeeded,
  startProgressiveVideoDownload,
} from "./videoCacheManager";

// --- In-Memory FileSystem Mock ---
const mockFiles: Record<
  string,
  {
    exists: boolean;
    isDirectory: boolean;
    size: number;
    modificationTime: number;
  }
> = {};
let mockReadDirectoryFiles: string[] = [];

jest.mock("expo-file-system/legacy", () => ({
  cacheDirectory: "file:///mock_cache/",
  getInfoAsync: jest.fn(async (uri: string) => {
    return mockFiles[uri] ?? { exists: false, isDirectory: false };
  }),
  makeDirectoryAsync: jest.fn(async () => {}),
  readDirectoryAsync: jest.fn(async () => [...mockReadDirectoryFiles]),
  deleteAsync: jest.fn(async (uri: string) => {
    delete mockFiles[uri];
    const filename = uri.split("/").pop();
    if (filename) {
      mockReadDirectoryFiles = mockReadDirectoryFiles.filter(
        (f) => f !== filename,
      );
    }
  }),
  moveAsync: jest.fn(async ({ from, to }: { from: string; to: string }) => {
    if (mockFiles[from]) {
      mockFiles[to] = { ...mockFiles[from] };
      delete mockFiles[from];
      const fromName = from.split("/").pop();
      const toName = to.split("/").pop();
      if (fromName && toName) {
        mockReadDirectoryFiles = mockReadDirectoryFiles.filter(
          (f) => f !== fromName,
        );
        mockReadDirectoryFiles.push(toName);
      }
    }
  }),
  createDownloadResumable: jest.fn((url, fileUri, options, callback) => {
    return {
      downloadAsync: jest.fn(async () => {
        // simulate writing temp file
        mockFiles[fileUri] = {
          exists: true,
          isDirectory: false,
          size: 50 * 1024 * 1024,
          modificationTime: Date.now() / 1000,
        };
        const filename = fileUri.split("/").pop();
        if (filename && !mockReadDirectoryFiles.includes(filename)) {
          mockReadDirectoryFiles.push(filename);
        }
        callback?.({
          totalBytesWritten: 50 * 1024 * 1024,
          totalBytesExpectedToWrite: 50 * 1024 * 1024,
        });
        return { status: 200, uri: fileUri };
      }),
      cancelAsync: jest.fn(async () => {}),
    };
  }),
}));

describe("videoCacheManager", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    Object.keys(mockFiles).forEach((k) => delete mockFiles[k]);
    mockReadDirectoryFiles = [];
  });

  describe("formatBytes", () => {
    it("formats bytes accurately", () => {
      expect(formatBytes(0)).toBe("0 B");
      expect(formatBytes(512)).toBe("512 B");
      expect(formatBytes(1024)).toBe("1.0 KB");
      expect(formatBytes(1024 * 1024 * 2.5)).toBe("2.5 MB");
      expect(formatBytes(1024 * 1024 * 1024 * 1.75)).toBe("1.75 GB");
    });
  });

  describe("file naming and URIs", () => {
    it("generates correct filename and URI", () => {
      const sessionId = "WOD-20260407-01JQXYZ";
      const filename = getVideoCacheFilename(sessionId, "hardsubbed");
      expect(filename).toBe("WOD-20260407-01JQXYZ_hardsubbed.mp4");

      const uri = getVideoCacheUri(sessionId, "hardsubbed");
      expect(uri).toBe(
        "file:///mock_cache/video_cache/WOD-20260407-01JQXYZ_hardsubbed.mp4",
      );
    });
  });

  describe("cache check & retrieval", () => {
    const sessionId = "WOD-123";
    const uri = getVideoCacheUri(sessionId, "merged");

    it("returns false/null when file does not exist", async () => {
      expect(await isSessionVideoCached(sessionId, "merged")).toBe(false);
      expect(await getCachedVideoUri(sessionId, "merged")).toBeNull();
    });

    it("returns true and URI when file exists with size > 0", async () => {
      mockFiles[uri] = {
        exists: true,
        isDirectory: false,
        size: 1048576,
        modificationTime: 12345,
      };

      expect(await isSessionVideoCached(sessionId, "merged")).toBe(true);
      expect(await getCachedVideoUri(sessionId, "merged")).toBe(uri);
    });

    it("returns false when file exists but size is 0", async () => {
      mockFiles[uri] = {
        exists: true,
        isDirectory: false,
        size: 0,
        modificationTime: 12345,
      };

      expect(await isSessionVideoCached(sessionId, "merged")).toBe(false);
      expect(await getCachedVideoUri(sessionId, "merged")).toBeNull();
    });
  });

  describe("download and cache creation", () => {
    it("downloads and moves to target .mp4 path", async () => {
      const sessionId = "WOD-DOWNLOAD-1";
      const targetUri = getVideoCacheUri(sessionId, "hardsubbed");
      let progressVal = 0;

      const handle = startProgressiveVideoDownload(
        sessionId,
        "hardsubbed",
        "https://example.com/video.mp4",
        (p) => {
          progressVal = p;
        },
      );

      const result = await handle.promise;
      expect(result).toBe(targetUri);
      expect(progressVal).toBe(1);
      expect(mockFiles[targetUri]?.exists).toBe(true);
    });

    it("skips download if already cached", async () => {
      const sessionId = "WOD-EXISTING";
      const targetUri = getVideoCacheUri(sessionId, "merged");
      mockFiles[targetUri] = {
        exists: true,
        isDirectory: false,
        size: 2048,
        modificationTime: 12345,
      };

      const handle = startProgressiveVideoDownload(
        sessionId,
        "merged",
        "https://example.com/video.mp4",
      );

      const result = await handle.promise;
      expect(result).toBe(targetUri);
      expect(FileSystem.createDownloadResumable).not.toHaveBeenCalled();
    });
  });

  describe("cache statistics & deletion", () => {
    beforeEach(() => {
      // Setup mock cache dir with 2 files
      const file1 = "WOD-A_merged.mp4";
      const file2 = "WOD-B_hardsubbed.mp4";
      const uri1 = `file:///mock_cache/video_cache/${file1}`;
      const uri2 = `file:///mock_cache/video_cache/${file2}`;

      mockReadDirectoryFiles = [file1, file2, "ignored.txt"];
      mockFiles["file:///mock_cache/video_cache/"] = {
        exists: true,
        isDirectory: true,
        size: 0,
        modificationTime: 0,
      };
      mockFiles[uri1] = {
        exists: true,
        isDirectory: false,
        size: 100 * 1024 * 1024,
        modificationTime: 2000,
      };
      mockFiles[uri2] = {
        exists: true,
        isDirectory: false,
        size: 200 * 1024 * 1024,
        modificationTime: 3000,
      };
    });

    it("lists cached videos and computes stats", async () => {
      const items = await listCachedVideos();
      expect(items).toHaveLength(2);
      expect(items[0].sessionId).toBe("WOD-B"); // Newer first
      expect(items[1].sessionId).toBe("WOD-A");

      const stats = await getVideoCacheStats();
      expect(stats.count).toBe(2);
      expect(stats.totalSizeBytes).toBe(300 * 1024 * 1024);
      expect(stats.formattedSize).toBe("300.0 MB");
    });

    it("deletes a single cached video", async () => {
      const deleted = await deleteCachedVideo("WOD-A", "merged");
      expect(deleted).toBe(true);
      expect(
        mockFiles["file:///mock_cache/video_cache/WOD-A_merged.mp4"],
      ).toBeUndefined();
    });

    it("clears all video cache", async () => {
      const freed = await clearAllVideoCache();
      expect(freed).toBe(300 * 1024 * 1024);
      expect(FileSystem.deleteAsync).toHaveBeenCalledWith(
        "file:///mock_cache/video_cache/",
        { idempotent: true },
      );
    });
  });

  describe("LRU cache pruning", () => {
    it("prunes oldest files when total size exceeds maxBytes limit", async () => {
      const fileOld = "WOD-OLD_merged.mp4";
      const fileNew = "WOD-NEW_merged.mp4";
      const uriOld = `file:///mock_cache/video_cache/${fileOld}`;
      const uriNew = `file:///mock_cache/video_cache/${fileNew}`;

      mockReadDirectoryFiles = [fileOld, fileNew];
      mockFiles["file:///mock_cache/video_cache/"] = {
        exists: true,
        isDirectory: true,
        size: 0,
        modificationTime: 0,
      };
      const nowSec = Date.now() / 1000;
      // Old file: 60MB, 2 days ago
      mockFiles[uriOld] = {
        exists: true,
        isDirectory: false,
        size: 60 * 1024 * 1024,
        modificationTime: nowSec - 2 * 86400,
      };
      // New file: 50MB, 1 hour ago
      mockFiles[uriNew] = {
        exists: true,
        isDirectory: false,
        size: 50 * 1024 * 1024,
        modificationTime: nowSec - 3600,
      };

      // Limit is 100MB (total is 110MB). Deleting old file (60MB) leaves 50MB <= 80MB target.
      const pruned = await pruneVideoCacheIfNeeded(100 * 1024 * 1024, 30);
      expect(pruned).toBe(1);
      // The oldest file should have been deleted
      expect(mockFiles[uriOld]).toBeUndefined();
      // The newer file should remain
      expect(mockFiles[uriNew]?.exists).toBe(true);
    });
  });
});
