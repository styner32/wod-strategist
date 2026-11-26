jest.mock("react", () => jest.requireActual("../../node_modules/react"));

const mockNavigate = jest.fn();
jest.mock("react-router-dom", () => ({
  useNavigate: () => mockNavigate,
}));

jest.mock("../src/auth/useAuth", () => ({
  useAuth: () => ({
    user: {
      profiles: [{ id: 10, name: "Main Profile" }],
    },
  }),
}));

const mockCreateSession = jest.fn();
const mockGetUploadUrl = jest.fn();
const mockCompleteUpload = jest.fn();
const mockUploadToGCS = jest.fn();
jest.mock("../src/api/upload", () => ({
  validateFileSize: () => null,
  uploadApi: {
    createSession: (...args: unknown[]) => mockCreateSession(...args),
    getUploadUrl: (...args: unknown[]) => mockGetUploadUrl(...args),
    completeUpload: (...args: unknown[]) => mockCompleteUpload(...args),
  },
  uploadToGCS: (...args: unknown[]) => mockUploadToGCS(...args),
}));

import { createElement } from "react";
import { UploadPage } from "../src/upload/UploadPage";

const { act, create } = require("react-test-renderer");

let tree: ReturnType<typeof create> | undefined;
let uuidCounter = 0;

beforeEach(() => {
  jest.clearAllMocks();
  uuidCounter = 0;
  tree = undefined;
  Object.defineProperty(global, "crypto", {
    configurable: true,
    value: {
      randomUUID: () => `uuid-${++uuidCounter}`,
    },
  });
  mockCreateSession.mockResolvedValue({
    session_id: "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
    status: "started",
    workout_type: "wod",
  });
  mockGetUploadUrl.mockResolvedValue({
    upload_url: "https://gcs.example/signed-put",
    gcs_uri: "gs://bucket/videos/10/WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV/wod.mp4",
  });
  mockUploadToGCS.mockResolvedValue(undefined);
  mockCompleteUpload.mockResolvedValue({
    message: "ok",
    task_id: "task-1",
    session_id: "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
  });
});

afterEach(async () => {
  if (tree) {
    await act(async () => {
      tree?.unmount();
    });
  }
});

describe("UploadPage", () => {
  it("calls createSession without session_id and forwards the server-generated ID to upload and navigation", async () => {
    await act(async () => {
      tree = create(createElement(UploadPage));
    });

    const fileInput = tree!.root.findByProps({ type: "file" });
    const fakeFile = { name: "fran.mp4", size: 1024 } as File;
    await act(async () => {
      fileInput.props.onChange({ target: { files: [fakeFile] } });
    });

    const form = tree!.root.findByType("form");
    await act(async () => {
      await form.props.onSubmit({ preventDefault: jest.fn() });
    });

    expect(mockCreateSession).toHaveBeenCalledTimes(1);
    expect(mockCreateSession).toHaveBeenCalledWith({
      profile_id: 10,
      idempotency_key: "uuid-1",
      workout_type: "wod",
      wod_description: undefined,
      movements: [],
    });
    expect(mockCreateSession.mock.calls[0][0]).not.toHaveProperty("session_id");

    expect(mockGetUploadUrl).toHaveBeenCalledWith(
      "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
      "fran.mp4",
      10,
    );
    expect(mockCompleteUpload).toHaveBeenCalledWith(
      expect.objectContaining({
        session_id: "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
        profile_id: 10,
        workout_type: "wod",
      }),
    );
    expect(mockNavigate).toHaveBeenCalledWith(
      "/sessions/WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV?profile_id=10",
    );
  });

  it("reuses idempotency_key on retry after a failed upload and rotates when a different file is selected", async () => {
    mockUploadToGCS.mockRejectedValueOnce(new Error("Upload failed")).mockResolvedValue(undefined);

    await act(async () => {
      tree = create(createElement(UploadPage));
    });

    const fileInput = tree!.root.findByProps({ type: "file" });
    const form = tree!.root.findByType("form");

    await act(async () => {
      fileInput.props.onChange({ target: { files: [{ name: "first.mp4", size: 1024 }] } });
    });
    await act(async () => {
      await form.props.onSubmit({ preventDefault: jest.fn() });
    });
    expect(mockCreateSession).toHaveBeenLastCalledWith(
      expect.objectContaining({ idempotency_key: "uuid-1" }),
    );
    expect(mockNavigate).not.toHaveBeenCalled();

    // Retry same file -> must reuse uuid-1
    await act(async () => {
      await form.props.onSubmit({ preventDefault: jest.fn() });
    });
    expect(mockCreateSession).toHaveBeenLastCalledWith(
      expect.objectContaining({ idempotency_key: "uuid-1" }),
    );
    expect(mockNavigate).toHaveBeenCalledTimes(1);

    // Select a new file -> must mint uuid-2
    await act(async () => {
      fileInput.props.onChange({ target: { files: [{ name: "second.mp4", size: 2048 }] } });
    });
    await act(async () => {
      await form.props.onSubmit({ preventDefault: jest.fn() });
    });
    expect(mockCreateSession).toHaveBeenLastCalledWith(
      expect.objectContaining({ idempotency_key: "uuid-2" }),
    );
  });
});
