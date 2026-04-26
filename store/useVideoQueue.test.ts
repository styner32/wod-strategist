import { useVideoQueue, type VideoItem } from "./useVideoQueue";

// ==========================================
// Mocks
// ==========================================

jest.mock("@react-native-async-storage/async-storage", () => {
  const store = new Map<string, string>();
  return {
    __esModule: true,
    default: {
      getItem: jest.fn((key: string) => Promise.resolve(store.get(key) ?? null)),
      setItem: jest.fn((key: string, value: string) => {
        store.set(key, value);
        return Promise.resolve();
      }),
      removeItem: jest.fn((key: string) => {
        store.delete(key);
        return Promise.resolve();
      }),
    },
  };
});


const mockCompress = jest.fn();
jest.mock("react-native-compressor", () => ({
  Video: {
    compress: (...args: any[]) => mockCompress(...args),
  },
}));

const mockFileDelete = jest.fn();
const mockFileExists = true;
const mockFileMove = jest.fn();
const mockFileCopy = jest.fn();
const mockFileSize = 1024;
jest.mock("expo-file-system", () => ({
  File: jest.fn().mockImplementation(() => ({
    exists: mockFileExists,
    delete: mockFileDelete,
    move: mockFileMove,
    copy: mockFileCopy,
    size: mockFileSize,
  })),
}));

const mockProcessWorkoutVideo = jest.fn();
jest.mock("@/features/wod/api", () => ({
  processWorkoutVideo: (...args: any[]) => mockProcessWorkoutVideo(...args),
}));

const mockSaveToLibraryAsync = jest.fn();
jest.mock("expo-media-library", () => ({
  saveToLibraryAsync: (...args: any[]) => mockSaveToLibraryAsync(...args),
}));

jest.mock("react-native", () => ({
  Alert: { alert: jest.fn() },
}));

// ==========================================
// Helpers
// ==========================================

const defaultMetadata = {
  sessionId: "test_session_001",
  workoutType: "wod" as const,
  movements: ["Squat"],
  injuries: [],
  profileId: 1,
};

function getStore() {
  return useVideoQueue.getState();
}

function getItems(): VideoItem[] {
  return useVideoQueue.getState().items;
}

// ==========================================
// Tests
// ==========================================

describe("useVideoQueue", () => {
  beforeEach(() => {
    // Reset store between tests
    useVideoQueue.setState({ items: [] });
    jest.clearAllMocks();
    mockFileDelete.mockClear();
    mockFileMove.mockClear();
    mockSaveToLibraryAsync.mockClear();
  });

  describe("enqueue", () => {
    it("should add an item with RECORDED status", () => {
      getStore().enqueue("file:///raw/video.mp4", defaultMetadata);

      const items = getItems();
      expect(items).toHaveLength(1);
      expect(items[0].status).toBe("RECORDED");
      expect(items[0].rawUri).toBe("file:///raw/video.mp4");
      expect(items[0].sessionId).toBe("test_session_001");
      expect(items[0].workoutType).toBe("wod");
      expect(items[0].gallerySaved).toBe(false);
    });

    it("should return the item id", () => {
      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      expect(id).toBeDefined();
      expect(typeof id).toBe("string");
      expect(getItems()[0].id).toBe(id);
    });

    it("should NOT auto-start encoding", () => {
      getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      expect(mockCompress).not.toHaveBeenCalled();
    });

    it("should transition RECORDED → ENCODING → ENCODED after startEncoding succeeds", async () => {
      mockCompress.mockResolvedValue("file:///compressed/video.mp4");

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      expect(getItems()[0].status).toBe("RECORDED");

      getStore().startEncoding(id);
      expect(getItems()[0].status).toBe("ENCODING");

      await new Promise((r) => setTimeout(r, 50));

      expect(getItems()[0].status).toBe("ENCODED");
      expect(getItems()[0].compressedUri).toMatch(/_encoded\.mp4$/);
    });

    it("should call Video.compress when startEncoding is called", () => {
      mockCompress.mockReturnValue(new Promise(() => {}));

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);

      expect(mockCompress).toHaveBeenCalledWith(
        "file:///raw/video.mp4",
        expect.objectContaining({ compressionMethod: "auto", maxSize: 720, minimumFileSizeForCompress: 0 }),
        expect.any(Function)
      );
    });

    it("should NOT delete raw file after encoding succeeds", async () => {
      mockCompress.mockResolvedValue("file:///compressed/video.mp4");

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);
      await new Promise((r) => setTimeout(r, 50));

      // Raw file should be preserved for re-encoding
      expect(mockFileDelete).not.toHaveBeenCalled();
    });

    it("should revert to RECORDED with error if encoding fails", async () => {
      mockCompress.mockRejectedValue(new Error("Compress failed"));

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);
      await new Promise((r) => setTimeout(r, 50));

      const items = getItems();
      expect(items[0].status).toBe("RECORDED");
      expect(items[0].error).toContain("Compress failed");
    });
  });

  describe("startUpload", () => {
    it("should transition ENCODED → UPLOADING → UPLOADED", async () => {
      const item: VideoItem = {
        id: "test_upload_1",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: ["Squat"],
        injuries: [],
        status: "ENCODED",
        progress: 1,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      mockProcessWorkoutVideo.mockResolvedValue({
        taskId: "task_123",
        sessionId: "session_001",
      });

      getStore().startUpload("test_upload_1");

      // Should immediately be UPLOADING
      expect(getItems()[0].status).toBe("UPLOADING");

      await new Promise((r) => setTimeout(r, 50));

      expect(getItems()[0].status).toBe("UPLOADED");
    });

    it("should NOT delete compressed file after upload succeeds", async () => {
      const item: VideoItem = {
        id: "test_upload_2",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ENCODED",
        progress: 1,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      mockProcessWorkoutVideo.mockResolvedValue({
        taskId: "task_456",
        sessionId: "session_001",
      });

      getStore().startUpload("test_upload_2");
      await new Promise((r) => setTimeout(r, 50));

      // Files are only deleted on dismiss/remove
      expect(mockFileDelete).not.toHaveBeenCalled();
    });

    it("should revert to ENCODED with error if upload fails", async () => {
      const item: VideoItem = {
        id: "test_upload_3",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ENCODED",
        progress: 1,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      mockProcessWorkoutVideo.mockRejectedValue(new Error("Network error"));

      getStore().startUpload("test_upload_3");
      await new Promise((r) => setTimeout(r, 50));

      expect(getItems()[0].status).toBe("ENCODED");
      expect(getItems()[0].error).toContain("Network error");
    });

    it("should not start upload for non-ENCODED items", () => {
      const item: VideoItem = {
        id: "test_upload_4",
        rawUri: "file:///raw/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ENCODING",
        progress: 0.5,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      getStore().startUpload("test_upload_4");

      // Should remain ENCODING, not change
      expect(getItems()[0].status).toBe("ENCODING");
      expect(mockProcessWorkoutVideo).not.toHaveBeenCalled();
    });
  });

  describe("re-encode from ENCODED state", () => {
    it("should allow startEncoding from ENCODED state", async () => {
      const item: VideoItem = {
        id: "test_reencode_1",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ENCODED",
        progress: 1,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      mockCompress.mockResolvedValue("file:///compressed/video2.mp4");

      getStore().startEncoding("test_reencode_1");

      expect(getItems()[0].status).toBe("ENCODING");
      expect(mockCompress).toHaveBeenCalled();

      await new Promise((r) => setTimeout(r, 50));
      expect(getItems()[0].status).toBe("ENCODED");
    });
  });

  describe("same-path compression bug", () => {
    it("should COPY instead of MOVE when compress returns same path as input", async () => {
      // Simulate Video.compress returning the same URI (no-op compression)
      mockCompress.mockResolvedValue("file:///raw/video.mp4");

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);

      await new Promise((r) => setTimeout(r, 50));

      // Should use copy (not move) to preserve the raw file
      expect(mockFileCopy).toHaveBeenCalled();
      expect(mockFileMove).not.toHaveBeenCalled();

      // Status should still be ENCODED
      expect(getItems()[0].status).toBe("ENCODED");
      expect(getItems()[0].compressedUri).toMatch(/_encoded\.mp4$/);
    });

    it("should MOVE when compress returns a different path", async () => {
      mockCompress.mockResolvedValue("file:///compressed/output.mp4");

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);

      await new Promise((r) => setTimeout(r, 50));

      // Should use move (not copy) since the paths differ
      expect(mockFileMove).toHaveBeenCalled();
      expect(mockFileCopy).not.toHaveBeenCalled();

      expect(getItems()[0].status).toBe("ENCODED");
    });
  });

  describe("remove", () => {
    it("should remove item and delete temp files", () => {
      const item: VideoItem = {
        id: "test_remove_1",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ENCODED",
        progress: 1,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      getStore().remove("test_remove_1");

      expect(getItems()).toHaveLength(0);
      // safeDelete is called for both rawUri and compressedUri
      expect(mockFileDelete).toHaveBeenCalled();
    });
  });

  describe("dismiss", () => {
    it("should remove item and delete files", () => {
      const item: VideoItem = {
        id: "test_dismiss_1",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "UPLOADED",
        progress: 1,
        createdAt: Date.now(),
        gallerySaved: false,
        profileId: 1,
      };
      useVideoQueue.setState({ items: [item] });

      getStore().dismiss("test_dismiss_1");

      expect(getItems()).toHaveLength(0);
      // dismiss now also cleans up files
      expect(mockFileDelete).toHaveBeenCalled();
    });
  });

  describe("saveToGallery", () => {
    it("should set gallerySaved to true on success", async () => {
      mockSaveToLibraryAsync.mockResolvedValue(undefined);

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      expect(getItems()[0].gallerySaved).toBe(false);

      const result = await getStore().saveToGallery(id);

      expect(result).toBe(true);
      expect(getItems()[0].gallerySaved).toBe(true);
      expect(mockSaveToLibraryAsync).toHaveBeenCalledWith("file:///raw/video.mp4");
    });

    it("should return false and keep gallerySaved false on failure", async () => {
      mockSaveToLibraryAsync.mockRejectedValue(new Error("No space"));

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);

      const result = await getStore().saveToGallery(id);

      expect(result).toBe(false);
      expect(getItems()[0].gallerySaved).toBe(false);
    });

    it("should return false for non-existent item", async () => {
      const result = await getStore().saveToGallery("nonexistent");
      expect(result).toBe(false);
    });
  });
});
