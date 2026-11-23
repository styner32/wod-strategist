import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, waitFor } from "@testing-library/react-native";

import UploadScreen from "../index";

const mockCreateSession = jest.fn();
const mockFetchMovementGroups = jest.fn();
const mockGetUploadUrl = jest.fn();
const mockUploadToGcs = jest.fn();
const mockNotifyUploadComplete = jest.fn();
const mockLaunchImageLibraryAsync = jest.fn();
const mockPush = jest.fn();

jest.mock("@/components/ui/icon-symbol", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { IconSymbol: ({ name }: { name: string }) => ReactActual.createElement(Text, {}, name) };
});

jest.mock("@/features/i18n", () => ({
  t: (key: string) => key,
  useLocale: () => "en",
}));

jest.mock("@/store/useProfileStore", () => ({
  useProfileId: () => 3,
}));

jest.mock("expo-image-picker", () => ({
  launchImageLibraryAsync: (...args: unknown[]) => mockLaunchImageLibraryAsync(...args),
}));

jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockPush(...args), back: jest.fn() },
}));

jest.mock("expo-status-bar", () => ({
  StatusBar: () => null,
}));

jest.mock("react-native-safe-area-context", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { SafeAreaView: ({ children, ...props }: any) => ReactActual.createElement(View, props, children) };
});

jest.mock("@/features/wod/api", () => ({
  createSession: (...args: unknown[]) => mockCreateSession(...args),
  fetchMovementGroups: (...args: unknown[]) => mockFetchMovementGroups(...args),
  getUploadUrl: (...args: unknown[]) => mockGetUploadUrl(...args),
  uploadToGcs: (...args: unknown[]) => mockUploadToGcs(...args),
  notifyUploadComplete: (...args: unknown[]) => mockNotifyUploadComplete(...args),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockFetchMovementGroups.mockResolvedValue([]);
  mockLaunchImageLibraryAsync.mockResolvedValue({
    canceled: false,
    assets: [{ uri: "file:///tmp/workout.mp4", fileName: "workout.mp4" }],
  });
  mockCreateSession.mockResolvedValue({
    session_id: "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
    status: "started",
    workout_type: "wod",
  });
  mockGetUploadUrl.mockResolvedValue({
    upload_url: "https://storage.googleapis.com/upload-signed",
    gcs_uri: "gs://bucket/videos/3/WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV/workout.mp4",
  });
  mockUploadToGcs.mockResolvedValue(undefined);
  mockNotifyUploadComplete.mockResolvedValue({ task_id: "task-1", session_id: "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV" });
});

describe("UploadScreen session creation", () => {
  it("creates a backend session without client sessionId and passes the returned ID through upload", async () => {
    const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    try {
      const view = render(<UploadScreen />);
      await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

      fireEvent.press(view.getByText("upload.tapToSelect"));
      await waitFor(() => expect(view.getByText("workout.mp4")).toBeTruthy());

      fireEvent.press(view.getByText("upload.analyzeWod"));

      await waitFor(() => expect(mockNotifyUploadComplete).toHaveBeenCalledTimes(1));
      expect(mockCreateSession).toHaveBeenCalledWith({
        profileId: 3,
        idempotencyKey: expect.any(String),
        workoutType: "wod",
        movements: [],
      });
      expect(mockCreateSession.mock.calls[0][0]).not.toHaveProperty("sessionId");
      expect(mockGetUploadUrl).toHaveBeenCalledWith(
        "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
        "workout.mp4",
        3,
      );
      expect(mockUploadToGcs).toHaveBeenCalledWith(
        "https://storage.googleapis.com/upload-signed",
        "file:///tmp/workout.mp4",
        "video/mp4",
        expect.any(Function),
      );
      expect(mockNotifyUploadComplete).toHaveBeenCalledWith(
        "WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV",
        "gs://bucket/videos/3/WOD-20261123-01ARZ3NDEKTSV4RRFFQ69G5FAV/workout.mp4",
        [],
        [],
        "wod",
        3,
      );
      expect(alert).toHaveBeenCalledWith("upload.success", "upload.analysisStarted", expect.any(Array));
      view.unmount();
    } finally {
      alert.mockRestore();
    }
  });

  it("reuses the same idempotencyKey when retrying the same selected video after a failure", async () => {
    const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    const consoleError = jest.spyOn(console, "error").mockImplementation(() => {});
    mockUploadToGcs.mockRejectedValueOnce(new Error("Network timeout")).mockResolvedValueOnce(undefined);
    try {
      const view = render(<UploadScreen />);
      await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

      fireEvent.press(view.getByText("upload.tapToSelect"));
      await waitFor(() => expect(view.getByText("workout.mp4")).toBeTruthy());

      fireEvent.press(view.getByText("upload.analyzeWod"));
      await waitFor(() => expect(alert).toHaveBeenCalledWith("common.error", "upload.networkError"));

      fireEvent.press(view.getByText("upload.analyzeWod"));
      await waitFor(() => expect(mockNotifyUploadComplete).toHaveBeenCalledTimes(1));

      expect(mockCreateSession).toHaveBeenCalledTimes(2);
      expect(mockCreateSession.mock.calls[0][0].idempotencyKey).toBeTruthy();
      expect(mockCreateSession.mock.calls[1][0].idempotencyKey).toBe(
        mockCreateSession.mock.calls[0][0].idempotencyKey,
      );
      view.unmount();
    } finally {
      consoleError.mockRestore();
      alert.mockRestore();
    }
  });
});
