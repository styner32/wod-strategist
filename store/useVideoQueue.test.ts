import { useVideoQueue, type VideoItem } from "./useVideoQueue";

// ==========================================
// Mocks
// ==========================================

const mockCompress = jest.fn();
jest.mock("react-native-compressor", () => ({
  Video: {
    compress: (...args: any[]) => mockCompress(...args),
  },
}));

const mockDeleteAsync = jest.fn().mockResolvedValue(undefined);
jest.mock("expo-file-system", () => ({
  deleteAsync: (...args: any[]) => mockDeleteAsync(...args),
}));

const mockProcessWorkoutVideo = jest.fn();
jest.mock("@/features/wod/api", () => ({
  processWorkoutVideo: (...args: any[]) => mockProcessWorkoutVideo(...args),
}));

// ==========================================
// Helpers
// ==========================================

const defaultMetadata = {
  sessionId: "test_session_001",
  workoutType: "wod" as const,
  movements: ["Squat"],
  injuries: [],
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

    it("should transition RECORDED → ENCODING → READY after startEncoding succeeds", async () => {
      mockCompress.mockResolvedValue("file:///compressed/video.mp4");

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      expect(getItems()[0].status).toBe("RECORDED");

      getStore().startEncoding(id);
      expect(getItems()[0].status).toBe("ENCODING");

      await new Promise((r) => setTimeout(r, 50));

      expect(getItems()[0].status).toBe("READY");
      expect(getItems()[0].compressedUri).toBe("file:///compressed/video.mp4");
    });

    it("should call Video.compress when startEncoding is called", () => {
      mockCompress.mockReturnValue(new Promise(() => {}));

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);

      expect(mockCompress).toHaveBeenCalledWith(
        "file:///raw/video.mp4",
        expect.objectContaining({ compressionMethod: "auto", maxSize: 720 })
      );
    });

    it("should delete raw file after encoding succeeds", async () => {
      mockCompress.mockResolvedValue("file:///compressed/video.mp4");

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);
      await new Promise((r) => setTimeout(r, 50));

      expect(mockDeleteAsync).toHaveBeenCalledWith(
        "file:///raw/video.mp4",
        expect.objectContaining({ idempotent: true })
      );
    });

    it("should transition to ERROR if encoding fails", async () => {
      mockCompress.mockRejectedValue(new Error("Compress failed"));

      const id = getStore().enqueue("file:///raw/video.mp4", defaultMetadata);
      getStore().startEncoding(id);
      await new Promise((r) => setTimeout(r, 50));

      const items = getItems();
      expect(items[0].status).toBe("ERROR");
      expect(items[0].errorStep).toBe("encode");
      expect(items[0].error).toContain("Compress failed");
    });
  });

  describe("startUpload", () => {
    it("should transition READY → UPLOADING → DONE", async () => {
      // Manually set up a READY item
      const item: VideoItem = {
        id: "test_upload_1",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: ["Squat"],
        injuries: [],
        status: "READY",
        progress: 1,
        createdAt: Date.now(),
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

      expect(getItems()[0].status).toBe("DONE");
    });

    it("should delete compressed file after upload succeeds", async () => {
      const item: VideoItem = {
        id: "test_upload_2",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "READY",
        progress: 1,
        createdAt: Date.now(),
      };
      useVideoQueue.setState({ items: [item] });

      mockProcessWorkoutVideo.mockResolvedValue({
        taskId: "task_456",
        sessionId: "session_001",
      });

      getStore().startUpload("test_upload_2");
      await new Promise((r) => setTimeout(r, 50));

      expect(mockDeleteAsync).toHaveBeenCalledWith(
        "file:///compressed/video.mp4",
        expect.objectContaining({ idempotent: true })
      );
    });

    it("should transition to ERROR if upload fails", async () => {
      const item: VideoItem = {
        id: "test_upload_3",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "READY",
        progress: 1,
        createdAt: Date.now(),
      };
      useVideoQueue.setState({ items: [item] });

      mockProcessWorkoutVideo.mockRejectedValue(new Error("Network error"));

      getStore().startUpload("test_upload_3");
      await new Promise((r) => setTimeout(r, 50));

      expect(getItems()[0].status).toBe("ERROR");
      expect(getItems()[0].errorStep).toBe("upload");
    });

    it("should not start upload for non-READY items", () => {
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
      };
      useVideoQueue.setState({ items: [item] });

      getStore().startUpload("test_upload_4");

      // Should remain ENCODING, not change
      expect(getItems()[0].status).toBe("ENCODING");
      expect(mockProcessWorkoutVideo).not.toHaveBeenCalled();
    });
  });

  describe("retry", () => {
    it("should retry encoding for encode errors", async () => {
      const item: VideoItem = {
        id: "test_retry_1",
        rawUri: "file:///raw/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ERROR",
        errorStep: "encode",
        error: "Previous failure",
        progress: 0,
        createdAt: Date.now(),
      };
      useVideoQueue.setState({ items: [item] });

      mockCompress.mockResolvedValue("file:///compressed/video.mp4");

      getStore().retry("test_retry_1");

      expect(getItems()[0].status).toBe("ENCODING");
      expect(mockCompress).toHaveBeenCalled();

      await new Promise((r) => setTimeout(r, 50));
      expect(getItems()[0].status).toBe("READY");
    });

    it("should retry upload for upload errors", async () => {
      const item: VideoItem = {
        id: "test_retry_2",
        rawUri: "file:///raw/video.mp4",
        compressedUri: "file:///compressed/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "ERROR",
        errorStep: "upload",
        error: "Network error",
        progress: 0,
        createdAt: Date.now(),
      };
      useVideoQueue.setState({ items: [item] });

      mockProcessWorkoutVideo.mockResolvedValue({
        taskId: "task_789",
        sessionId: "session_001",
      });

      getStore().retry("test_retry_2");

      expect(getItems()[0].status).toBe("UPLOADING");

      await new Promise((r) => setTimeout(r, 50));
      expect(getItems()[0].status).toBe("DONE");
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
        status: "READY",
        progress: 1,
        createdAt: Date.now(),
      };
      useVideoQueue.setState({ items: [item] });

      getStore().remove("test_remove_1");

      expect(getItems()).toHaveLength(0);
      expect(mockDeleteAsync).toHaveBeenCalledWith(
        "file:///raw/video.mp4",
        expect.anything()
      );
      expect(mockDeleteAsync).toHaveBeenCalledWith(
        "file:///compressed/video.mp4",
        expect.anything()
      );
    });
  });

  describe("dismiss", () => {
    it("should remove item without deleting files", () => {
      const item: VideoItem = {
        id: "test_dismiss_1",
        rawUri: "file:///raw/video.mp4",
        sessionId: "session_001",
        workoutType: "wod",
        movements: [],
        injuries: [],
        status: "DONE",
        progress: 1,
        createdAt: Date.now(),
      };
      useVideoQueue.setState({ items: [item] });

      getStore().dismiss("test_dismiss_1");

      expect(getItems()).toHaveLength(0);
      expect(mockDeleteAsync).not.toHaveBeenCalled();
    });
  });
});
