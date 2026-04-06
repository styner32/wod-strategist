import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import {
  fetchMovements,
  fetchInjuries,
  getUploadUrl,
  uploadToGcs,
  notifyUploadComplete,
  processWorkoutVideo,
} from "./api";
import { FileSystemUploadType } from "expo-file-system/legacy";

// The createUploadTask mock needs to be defined here since jest-expo
// internally overrides __mocks__/expo-file-system/legacy.ts
export const mockUploadAsync = jest.fn().mockResolvedValue({
  status: 200,
  body: "",
  headers: {},
});
export const mockCreateUploadTask = jest.fn((url: string, fileUri: string, options?: any, callback?: any) => {
  return { uploadAsync: mockUploadAsync };
});

jest.mock("expo-file-system/legacy", () => ({
  FileSystemUploadType: {
    BINARY_CONTENT: 0,
    MULTIPART: 1,
  },
  createUploadTask: jest.fn().mockImplementation((url: string, fileUri: string, options?: any, callback?: any) => mockCreateUploadTask(url, fileUri, options, callback)),
}));

// We must require the module AFTER the mock is defined so that the tests
// can access the mocked exported function correctly.
const { createUploadTask } = require("expo-file-system/legacy");

const API_BASE_URL = "http://localhost:8088/api/v1";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  jest.clearAllMocks();
});
afterAll(() => server.close());

describe("API Client Methods", () => {
  
  describe("fetchMovements", () => {
    it("should fetch movements successfully", async () => {
      server.use(
        http.get(`${API_BASE_URL}/movements`, ({ request }) => {
          expect(request.headers.get("X-API-Key")).toBe("");
          return HttpResponse.json(["Squat", "Deadlift"]);
        })
      );
      
      const res = await fetchMovements();
      expect(res).toEqual(["Squat", "Deadlift"]);
    });

    it("should throw on server error", async () => {
      server.use(
        http.get(`${API_BASE_URL}/movements`, () => new HttpResponse(null, { status: 500 }))
      );
      await expect(fetchMovements()).rejects.toThrow(/API Error \[500\]/);
    });
  });

  describe("fetchInjuries", () => {
    it("should fetch injuries successfully", async () => {
      server.use(
        http.get(`${API_BASE_URL}/injuries`, () => HttpResponse.json(["Knee", "Shoulder"]))
      );
      const res = await fetchInjuries();
      expect(res).toEqual(["Knee", "Shoulder"]);
    });
  });

  describe("getUploadUrl", () => {
    it("should POST to /upload-url with correct payload", async () => {
      let capturedBody: any;
      server.use(
        http.post(`${API_BASE_URL}/upload-url`, async ({ request }) => {
          capturedBody = await request.json();
          return HttpResponse.json({
            upload_url: "https://gcs.fake/upload",
            gcs_uri: "gs://bucket/vid.mp4",
          });
        })
      );

      const res = await getUploadUrl("session_123", "test.mp4");
      
      expect(capturedBody).toEqual({
        session_id: "session_123",
        filename: "test.mp4",
      });
      expect(res.upload_url).toBe("https://gcs.fake/upload");
      expect(res.gcs_uri).toBe("gs://bucket/vid.mp4");
    });
  });

  describe("uploadToGcs", () => {
    it("should create an upload task with correct arguments and call uploadAsync", async () => {
      // The `expo-file-system/legacy` module is mocked via `__mocks__` folder
      await uploadToGcs("https://gcs.fake/upload", "file:///fake/path.mp4", "video/mp4");

      expect(createUploadTask).toHaveBeenCalledTimes(1);
      expect(createUploadTask).toHaveBeenCalledWith(
        "https://gcs.fake/upload",
        "file:///fake/path.mp4",
        {
          httpMethod: "PUT",
          headers: { "Content-Type": "video/mp4" },
          uploadType: FileSystemUploadType.BINARY_CONTENT,
        },
        expect.any(Function)
      );
    });
  });

  describe("notifyUploadComplete", () => {
    it("should POST to /upload-complete with correct payload", async () => {
      let capturedBody: any;
      server.use(
        http.post(`${API_BASE_URL}/upload-complete`, async ({ request }) => {
          capturedBody = await request.json();
          return HttpResponse.json({
            task_id: "task_123",
            session_id: "session_123",
          });
        })
      );

      const res = await notifyUploadComplete(
        "session_123",
        "gs://bucket/vid.mp4",
        ["Squat"],
        ["Knee"],
        "wod",
        1
      );

      expect(capturedBody).toEqual({
        session_id: "session_123",
        gcs_uri: "gs://bucket/vid.mp4",
        movements: ["Squat"],
        injuries: ["Knee"],
        workout_type: "wod",
        profile_id: 1,
      });
      expect(res.task_id).toBe("task_123");
    });
  });

  describe("processWorkoutVideo (Orchestrator)", () => {
    it("should orchestrate the full 3-step upload flow", async () => {
      // 1. Mock Upload URL endpoint
      server.use(
        http.post(`${API_BASE_URL}/upload-url`, () => {
          return HttpResponse.json({
            upload_url: "https://gcs.fake/upload",
            gcs_uri: "gs://bucket/full-video.mp4",
          });
        })
      );

      // 2. Mock Complete endpoint
      server.use(
        http.post(`${API_BASE_URL}/upload-complete`, () => {
          return HttpResponse.json({
            task_id: "backend_task_999",
            session_id: "session_dev_001",
          });
        })
      );

      const onProgress = jest.fn();
      
      const result = await processWorkoutVideo("file:///test/video.mp4", "session_dev_001", {
        movements: ["Deadlift"],
        injuries: [],
        mimeType: "video/mp4",
        workoutType: "wod",
        profileId: 1,
        onProgress,
      });

      // Verify final return value
      expect(result.taskId).toBe("backend_task_999");
      expect(result.sessionId).toBe("session_dev_001");

      // Verify the Expo System upload task was dispatched
      expect(createUploadTask).toHaveBeenCalledTimes(1);
    });
  });
});
