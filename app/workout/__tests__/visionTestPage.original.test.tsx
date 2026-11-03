import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";

import VisionTestPage from "../visionTestPage";
import type { RecentSetupSnapshot } from "@/features/wod/recentWorkoutSetup";

const mockRef = { ownerUserId: 7, profileId: 3, sessionId: "WOD-20260929-LIFECYCLE" };
const mockPrepare = jest.fn();
const mockValidateRecordingSetup = jest.fn(async (..._args: unknown[]): Promise<unknown> => undefined);
const mockSaveRecordingSetup = jest.fn(async (..._args: unknown[]) => {});
const mockFetchArchiveStates = jest.fn();
const mockBegin = jest.fn();
const mockAdd = jest.fn();
const mockStopped = jest.fn();
const mockSave = jest.fn();
const mockReleases: jest.Mock[] = [];
const mockStartCamera = jest.fn();
const mockStopCamera = jest.fn();
const mockReplace = jest.fn();
const mockProcessChunk = jest.fn();
const mockCompress = jest.fn();
const mockCancelCompression = jest.fn();
const mockMergeChunks = jest.fn();
let mockContinuousRecording = true;
let mockSkipCompression = true;
let mockRecordingSetupParam: string | string[] | undefined = "launch-snapshot";
const mockEnvironment = {
  status: "off", start: jest.fn(), stop: jest.fn(), pause: jest.fn(), resume: jest.fn(),
  event: jest.fn(), offerChunk: jest.fn(),
};
const mockAppleStop = jest.fn();
const mockSetRecording = jest.fn();
const mockAddPending = jest.fn();
const mockNative = { startRecording: (...args: unknown[]) => mockStartCamera(...args), stopRecording: () => mockStopCamera(), takeSnapshot: jest.fn() };
const mockDevice = { id: "rear", physicalDevices: [] };
const mockFormat = { videoWidth: 1280, videoHeight: 720, videoStabilizationModes: [] };

jest.mock("@react-native-async-storage/async-storage", () => require("@react-native-async-storage/async-storage/jest/async-storage-mock"));
jest.mock("expo-router", () => ({
  router: { replace: (...args: unknown[]) => mockReplace(...args), back: jest.fn(), push: jest.fn() },
  useFocusEffect: () => {},
  useLocalSearchParams: () => ({ recordingSetup: mockRecordingSetupParam, autoRecord: "true", continuousRecording: String(mockContinuousRecording), showSkeleton: "false", skipCompression: String(mockSkipCompression) }),
}));
jest.mock("expo-router/react-navigation", () => ({ useIsFocused: () => true }));
jest.mock("react-native-vision-camera", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  return {
    Camera: ReactActual.forwardRef((_props: any, ref: any) => {
      ReactActual.useImperativeHandle(ref, () => mockNative, []);
      ReactActual.useEffect(() => { _props.onInitialized(); }, []);
      return null;
    }),
    useCameraDevice: () => mockDevice,
    useCameraFormat: () => mockFormat,
    useCameraPermission: () => ({ hasPermission: true, requestPermission: jest.fn() }),
    useMicrophonePermission: () => ({ hasPermission: true, requestPermission: jest.fn() }),
  };
});
jest.mock("@/components/ui/icon-symbol", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { IconSymbol: ({ name }: { name: string }) => ReactActual.createElement(Text, {}, name) };
});
jest.mock("@/features/i18n", () => ({ t: (key: string) => key, useLocale: () => "en" }));
jest.mock("@/features/auth/useAuthStore", () => ({ useAuthStore: { getState: () => ({
  userId: 7, isLoggedIn: true, sessionExpiredDuringRecording: false,
  setRecordingActive: mockSetRecording, finishDeferredUnauthorized: jest.fn(),
}) } }));
jest.mock("@/store/useProfileStore", () => ({ useProfileStore: (selector: (value: unknown) => unknown) => selector({ activeProfileId: 3 }) }));
jest.mock("@/store/useMergeStatus", () => ({ useMergeStatus: { getState: () => ({ addPending: mockAddPending, removePending: jest.fn() }) } }));
jest.mock("@/features/wod/recentWorkoutSetup", () => ({ saveRecordingSetup: (...args: unknown[]) => mockSaveRecordingSetup(...args), validateRecordingSetup: (...args: unknown[]) => mockValidateRecordingSetup(...args) }));
jest.mock("@/features/wod/archiveStates", () => ({
  ...jest.requireActual("@/features/wod/archiveStates"),
  fetchArchiveStates: (...args: unknown[]) => mockFetchArchiveStates(...args),
}));
jest.mock("@/features/wod/workoutType", () => ({
  ...jest.requireActual("@/features/wod/workoutType"),
  buildWorkoutSessionId: () => mockRef.sessionId, parseWorkoutType: () => "WOD", formatWorkoutTypeLabel: () => "WOD",
}));
jest.mock("@/features/video/originalVideoStore", () => ({
  prepareOriginalSession: (...args: unknown[]) => mockPrepare(...args),
  beginOriginalChunk: (...args: unknown[]) => mockBegin(...args),
  addOriginalChunk: (...args: unknown[]) => mockAdd(...args),
  markOriginalRecordingStopped: (...args: unknown[]) => mockStopped(...args),
  finalizeAndSaveOriginal: (...args: unknown[]) => mockSave(...args),
  holdOriginalFiles: () => { const release = jest.fn(); mockReleases.push(release); return release; },
}));
jest.mock("@/modules/apple-on-device-ai", () => ({ environmentNativeAvailable: false, appleEnvironment: {} }));
jest.mock("@/features/environment/store", () => ({ flushEnvironmentUploads: async () => {} }));
jest.mock("@/features/environment/useEnvironmentRecorder", () => ({ useEnvironmentRecorder: () => mockEnvironment }));
jest.mock("@/features/environment/EnvironmentCard", () => ({ EnvironmentLiveCard: () => null }));
jest.mock("@/features/wod/useLiveWorkoutFeedback", () => ({ useLiveWorkoutFeedback: () => ({}) }));
jest.mock("@/features/wod/ui/ActivitySummaryCard", () => ({ ActivitySummaryContent: () => null }));
jest.mock("expo-keep-awake", () => ({ activateKeepAwakeAsync: async () => {}, deactivateKeepAwake: () => {} }));
jest.mock("expo-media-library/legacy", () => ({ requestPermissionsAsync: async () => ({ granted: true }) }));
jest.mock("expo-screen-orientation", () => ({ lockAsync: async () => {}, OrientationLock: { LANDSCAPE: 3, PORTRAIT_UP: 1 } }));
jest.mock("react-native-compressor", () => ({
  Video: {
    compress: (...args: unknown[]) => mockCompress(...args),
    cancelCompression: (...args: unknown[]) => mockCancelCompression(...args),
  },
}));
jest.mock("@/features/health/polar/polarSensorRecorder", () => ({ PolarSensorRecorder: {
  start: () => {}, pause: () => {}, resume: () => {}, stop: async () => null,
  getLiveStatus: () => ({ accSamples: 0, dropped: null }),
} }));
jest.mock("@/features/health/polar/sensorTelemetryUpload", () => ({ enqueueSensorUpload: async () => {}, flushSensorUploads: async () => {} }));
jest.mock("@/features/health/useBleHeartRate", () => ({ useBleHeartRate: () => ({
  bpm: 0, quality: "missing", status: "off", batteryLevel: null,
  getReading: () => ({ bpm: null }), resetQuality: () => {},
}) }));
jest.mock("@/features/ai-coach/frame-processors/usePoseDetection", () => ({ usePoseDetection: () => ({
  frameProcessor: undefined, poseResult: undefined, isModelLoaded: false,
  status: "disabled", hasInference: false, error: null,
  monitorData: { confidence: 0, motion: 0, isWorkingOut: false },
}) }));
jest.mock("@/features/ai-coach/ui/EnergyMonitor", () => ({ EnergyMonitor: () => null }));
jest.mock("@/features/ai-coach/ui/SkeletonOverlay", () => ({ SkeletonOverlay: () => null }));
jest.mock("@/features/ai-coach/ui/AppleAiFeedbackCard", () => ({ AppleAiFeedbackCard: () => null }));
jest.mock("@/features/ai-coach/useAppleAiFeedback", () => ({ useAppleAiFeedback: () => ({ status: "off", stop: mockAppleStop }) }));
jest.mock("@/features/ai-coach/appleAiUpload", () => ({ saveAppleAiObservation: async () => {}, flushAppleAiUploads: async () => {} }));
jest.mock("@/features/debug/telemetryRecorder", () => ({ TelemetryRecorder: { start: () => {}, registerProvider: () => {}, stop: async () => null } }));
jest.mock("@/features/debug/telemetryUpload", () => ({ enqueueUpload: async () => {}, flushPendingUploads: async () => {} }));
jest.mock("@/features/wod/api", () => ({
  mergeChunks: (...args: unknown[]) => mockMergeChunks(...args),
  processWorkoutChunk: (...args: unknown[]) => mockProcessChunk(...args),
  is4xxError: (err: any) => Boolean(err?.status >= 400 && err?.status < 500),
  getErrorStatusCode: (err: any) => err?.status ?? null,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

function source(order = 1) {
  return { path: `file:///documents/run-${order}/full.mp4`, duration: 10, segmented: true,
    droppedVideoFrames: 0, droppedAudioBuffers: 0, tailDrainTimedOut: false };
}

let logs: jest.SpyInstance;
let warnings: jest.SpyInstance;
const originalMediaPipelineVersion = process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION;
beforeEach(() => {
  delete process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION;
  jest.clearAllMocks();
  mockStartCamera.mockReset();
  mockValidateRecordingSetup.mockReset().mockResolvedValue(undefined);
  mockFetchArchiveStates.mockReset().mockRejectedValue(new Error("unexpected_archive_check"));
  mockContinuousRecording = true;
  mockSkipCompression = true;
  mockRecordingSetupParam = "launch-snapshot";
  mockReleases.length = 0;
  mockPrepare.mockResolvedValue(mockRef);
  mockBegin.mockImplementation(async (_ref: unknown, order: number) => `file:///documents/run-${order}/`);
  mockAdd.mockResolvedValue(undefined);
  mockStopped.mockResolvedValue(undefined);
  mockSave.mockResolvedValue({ status: "saved" });
  mockProcessChunk.mockResolvedValue(undefined);
  mockCompress.mockImplementation(async (path: string) => path);
  mockMergeChunks.mockResolvedValue(undefined);
  mockStopCamera.mockResolvedValue(undefined);
  mockAppleStop.mockResolvedValue(undefined);
  mockEnvironment.stop.mockResolvedValue(undefined);
  mockEnvironment.pause.mockResolvedValue(undefined);
  logs = jest.spyOn(console, "log").mockImplementation(() => {});
  warnings = jest.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => {
  logs.mockRestore(); warnings.mockRestore();
  if (originalMediaPipelineVersion === undefined) delete process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION;
  else process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION = originalMediaPipelineVersion;
});

it("retains an incomplete session without acquiring a hold when unmounted before session preparation resolves", async () => {
  const session = deferred<typeof mockRef>();
  mockPrepare.mockReturnValueOnce(session.promise);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockPrepare).toHaveBeenCalledTimes(1));
  view.unmount();
  await act(async () => { session.resolve(mockRef); });
  await waitFor(() => expect(mockStopped).toHaveBeenCalledWith(mockRef, {
    complete: false, reason: "recording_screen_unmounted_before_capture",
  }));
  expect(mockBegin).not.toHaveBeenCalled();
  expect(mockStartCamera).not.toHaveBeenCalled();
  expect(mockStopCamera).not.toHaveBeenCalled();
  expect(mockSave).not.toHaveBeenCalled();
  expect(mockReleases).toHaveLength(0);
  expect(mockSetRecording).not.toHaveBeenCalledWith(true);
});

it("does not start native capture and releases its hold when unmounted before source directory preparation resolves", async () => {
  const folder = deferred<string>();
  mockBegin.mockReturnValueOnce(folder.promise);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockBegin).toHaveBeenCalledTimes(1));
  expect(mockReleases).toHaveLength(1);
  view.unmount();
  expect(mockReleases[0]).not.toHaveBeenCalled();
  await act(async () => { folder.resolve("file:///documents/run-1/"); });
  await waitFor(() => expect(mockStopped).toHaveBeenCalledWith(mockRef, {
    complete: false, reason: "recording_screen_unmounted",
  }));
  await waitFor(() => expect(mockReleases[0]).toHaveBeenCalledTimes(1));
  expect(mockEnvironment.stop).toHaveBeenCalledTimes(1);
  expect(mockStartCamera).not.toHaveBeenCalled();
  expect(mockStopCamera).not.toHaveBeenCalled();
  expect(mockSave).not.toHaveBeenCalled();
});

it("waits for initial durable source preparation before requesting native stop", async () => {
  const folder = deferred<string>();
  mockBegin.mockReturnValueOnce(folder.promise);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockBegin).toHaveBeenCalledTimes(1));
  fireEvent.press(view.getByText("square.fill"));
  expect(mockStartCamera).not.toHaveBeenCalled();
  expect(mockStopCamera).not.toHaveBeenCalled();
  await act(async () => { folder.resolve("file:///documents/run-1/"); });
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledWith(mockRef));
  await act(async () => { await callbacks.onRecordingFinished(source()); });
  view.unmount();
});

it("starts gallery saving after source finalization but retains the session hold through derivative and environment drains", async () => {
  const environment = deferred<void>();
  mockEnvironment.stop.mockReturnValue(environment.promise);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  expect(mockReleases[0]).not.toHaveBeenCalled();
  expect(mockEnvironment.stop).not.toHaveBeenCalled();
  await act(async () => { await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockEnvironment.stop).toHaveBeenCalledTimes(1));
  expect(mockReleases[0]).not.toHaveBeenCalled();
  await act(async () => { environment.resolve(); });
  await waitFor(() => expect(mockReleases[0]).toHaveBeenCalledTimes(1));
  view.unmount();
});

it("does not resume another camera run until the paused source has finalized", async () => {
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const first = mockStartCamera.mock.calls[0][0];
  fireEvent.press(view.getByText("pause.fill"));
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(1));
  fireEvent.press(view.getByText("play.fill"));
  expect(mockStartCamera).toHaveBeenCalledTimes(1);
  await act(async () => { first.onRecordingSourceFinalized(source()); });
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(2));
  expect(mockBegin.mock.calls.map(call => call[1])).toEqual([1, 2]);
  await act(async () => { await first.onRecordingFinished(source()); });
  const second = mockStartCamera.mock.calls[1][0];
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { second.onRecordingSourceFinalized(source(2)); await second.onRecordingFinished(source(2)); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  expect(mockSaveRecordingSetup).toHaveBeenCalledTimes(1);
  view.unmount();
});

it("marks unexpected unmount incomplete and releases only after native and environment readers finish", async () => {
  const environment = deferred<void>();
  mockEnvironment.stop.mockReturnValue(environment.promise);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  view.unmount();
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(1));
  expect(mockReleases[0]).not.toHaveBeenCalled();
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); });
  await waitFor(() => expect(mockStopped).toHaveBeenCalledWith(mockRef, { complete: false, reason: "recording_screen_unmounted" }));
  expect(mockSave).not.toHaveBeenCalled();
  expect(mockReleases[0]).not.toHaveBeenCalled();
  await act(async () => { await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockEnvironment.stop).toHaveBeenCalledTimes(1));
  await act(async () => { environment.resolve(); });
  await waitFor(() => expect(mockReleases[0]).toHaveBeenCalledTimes(1));
});

it("retains the default-mode original and drains readers when native detach finalization follows a missing-view stop error", async () => {
  mockContinuousRecording = false;
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  expect(callbacks.segmented).toBeUndefined();
  mockStopCamera.mockRejectedValueOnce(new Error("system/view-not-found"));
  view.unmount();
  await waitFor(() => expect(mockStopped).toHaveBeenCalledWith(mockRef, {
    complete: false, reason: "recording_screen_unmounted",
  }));
  expect(mockReleases[0]).not.toHaveBeenCalled();
  // The native view's detach hook finalizes the writer even though JS can no longer find it.
  await act(async () => { await callbacks.onRecordingFinished({ path: "file:///documents/run-1/full.mov", duration: 10 }); });
  await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  expect(mockAdd).toHaveBeenCalledWith(mockRef, { order: 1, path: "file:///documents/run-1/full.mov", durationSecs: 10 });
  expect(mockStartCamera).toHaveBeenCalledTimes(1);
  expect(mockEnvironment.stop).toHaveBeenCalledTimes(1);
  expect(mockSave).not.toHaveBeenCalled();
  expect(mockSaveRecordingSetup).toHaveBeenCalledTimes(1);
});

it("keeps the resumed writer active when a source-accepted older run reports a late preparation error", async () => {
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const first = mockStartCamera.mock.calls[0][0];
  fireEvent.press(view.getByText("pause.fill"));
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(1));
  await act(async () => { first.onRecordingSourceFinalized(source()); });
  fireEvent.press(view.getByText("play.fill"));
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(2));

  await act(async () => { first.onRecordingError(new Error("Old derivative preparation failed")); });
  expect(mockStopCamera).toHaveBeenCalledTimes(1);
  expect(mockStopped).not.toHaveBeenCalled();
  expect(mockSave).not.toHaveBeenCalled();
  expect(mockReleases[0]).not.toHaveBeenCalled();

  const second = mockStartCamera.mock.calls[1][0];
  fireEvent.press(view.getByText("square.fill"));
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(2));
  await act(async () => {
    second.onRecordingSourceFinalized(source(2));
    await second.onRecordingFinished(source(2));
  });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  expect(mockStopped).toHaveBeenCalledWith(mockRef, { complete: true, reason: undefined });
  expect(mockAdd.mock.calls.map(call => call[1].order)).toEqual([1, 2]);
  await waitFor(() => expect(mockReleases[0]).toHaveBeenCalledTimes(1));
  view.unmount();
});

it("resumes recording after pause-time dropped buffers, saves the finalized original to gallery, and triggers server merge", async () => {
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const first = mockStartCamera.mock.calls[0][0];

  // Upload an analysis segment during run 1
  await act(async () => {
    first.onRecordingSegment({
      status: "ready",
      path: "file:///documents/run-1/analysis/part.mp4",
      captureStartTimeMs: Date.now() - 1000,
      captureEndTimeMs: Date.now(),
    });
  });

  // Pause recording (e.g. backgrounded by system alert)
  fireEvent.press(view.getByText("pause.fill"));
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(1));

  // The interrupted run finalizes its source file while reporting dropped buffers and tail timeout
  await act(async () => {
    first.onRecordingSourceFinalized({
      ...source(1),
      droppedVideoFrames: 24,
      droppedAudioBuffers: 0,
      tailDrainTimedOut: true,
    });
    await first.onRecordingFinished({
      ...source(1),
      droppedVideoFrames: 24,
      droppedAudioBuffers: 0,
      tailDrainTimedOut: true,
    });
  });

  // Resume recording after dismissing the alert
  fireEvent.press(view.getByText("play.fill"));
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(2));

  // Stop the second run and complete the workout
  const second = mockStartCamera.mock.calls[1][0];
  fireEvent.press(view.getByText("square.fill"));
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(2));
  await act(async () => {
    second.onRecordingSourceFinalized(source(2));
    await second.onRecordingFinished(source(2));
  });

  // Finalized sources remain complete so automatic gallery save and server merge both run
  expect(mockStopped).toHaveBeenCalledWith(mockRef, {
    complete: true,
    reason: undefined,
  });
  expect(mockAdd.mock.calls.map(call => call[1].order)).toEqual([1, 2]);
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  expect(mockAddPending).toHaveBeenCalledWith(mockRef.sessionId);
  await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
  expect(mockMergeChunks.mock.calls[0][0]).toBe(mockRef.sessionId);
  view.unmount();
});

it("triggers gallery save and server merge when the only segment is emitted during drain after tailDrainTimedOut", async () => {
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];

  fireEvent.press(view.getByText("square.fill"));
  await waitFor(() => expect(mockStopCamera).toHaveBeenCalledTimes(1));

  // Native writer finalizes before the final analysis segment finishes packaging
  await act(async () => {
    callbacks.onRecordingSourceFinalized({
      ...source(1),
      droppedVideoFrames: 0,
      droppedAudioBuffers: 0,
      tailDrainTimedOut: true,
    });
    callbacks.onRecordingSegment({
      status: "ready",
      path: "file:///documents/run-1/analysis/part-0.mp4",
      captureStartTimeMs: Date.now() - 1000,
      captureEndTimeMs: Date.now(),
    });
    await callbacks.onRecordingFinished({
      ...source(1),
      droppedVideoFrames: 0,
      droppedAudioBuffers: 0,
      tailDrainTimedOut: true,
    });
  });

  expect(mockStopped).toHaveBeenCalledWith(mockRef, {
    complete: true,
    reason: undefined,
  });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
  expect(mockAddPending).toHaveBeenCalledWith(mockRef.sessionId);
  expect(mockMergeChunks.mock.calls[0][0]).toBe(mockRef.sessionId);
  view.unmount();
});

it("keeps complete originals saveable and releases readers after an analysis network failure", async () => {
  mockProcessChunk.mockRejectedValue(new Error("offline"));
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  await act(async () => { callbacks.onRecordingSegment({ status: "ready", path: "file:///documents/run-1/analysis/part.mp4",
    captureStartTimeMs: Date.now() - 1000, captureEndTimeMs: Date.now() }); });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalled());
  expect(mockProcessChunk.mock.calls[0][2]).not.toHaveProperty("workoutConfidence");
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  expect(mockStopped).toHaveBeenCalledWith(mockRef, { complete: true, reason: undefined });
  await act(async () => { await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  expect(mockMergeChunks).not.toHaveBeenCalled();
  view.unmount();
});

it.each(["exception", "missing_output", "empty_output"])("falls back to native analysis chunks after compression %s and publishes only after both uploads finish", async (failure) => {
  mockSkipCompression = false;
  if (failure === "exception") mockCompress.mockRejectedValueOnce(new Error("encoder failed"));
  else {
    mockCompress.mockResolvedValueOnce("file:///missing-compressed.mp4");
    require("expo-file-system/legacy").getInfoAsync.mockResolvedValueOnce(
      failure === "missing_output" ? { exists: false } : { exists: true, size: 0 },
    );
  }
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  const start = Date.now();
  const paths = ["file:///documents/run-1/analysis/part-43.mp4", "file:///documents/run-1/analysis/part-44.mp4"];
  await act(async () => {
    paths.forEach((path, index) => callbacks.onRecordingSegment({ status: "ready", path,
      captureStartTimeMs: start + index * 10000, captureEndTimeMs: start + (index + 1) * 10000 }));
  });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(2));
  expect(mockProcessChunk.mock.calls.map(call => call[0])).toEqual(paths);
  expect(mockCompress).toHaveBeenCalledTimes(1);
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
  expect(mockMergeChunks.mock.calls[0][0]).toBe(mockRef.sessionId);
  await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  view.unmount();
});

it("cancels hung compression after timeout, falls back to native analysis chunks, and completes server merge", async () => {
  jest.useFakeTimers();
  mockSkipCompression = false;
  mockCompress.mockImplementationOnce((_path: string, options?: { getCancellationId?: (id: string) => void }) => {
    options?.getCancellationId?.("cancel-hung-11");
    return new Promise<string>(() => {});
  });
  const view = render(<VisionTestPage />);
  try {
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
    const callbacks = mockStartCamera.mock.calls[0][0];
    const start = Date.now();
    const paths = ["file:///documents/run-1/analysis/part-11.mp4", "file:///documents/run-1/analysis/part-12.mp4"];
    await act(async () => {
      paths.forEach((path, index) => callbacks.onRecordingSegment({ status: "ready", path,
        captureStartTimeMs: start + index * 10000, captureEndTimeMs: start + (index + 1) * 10000 }));
    });
    expect(mockCompress).toHaveBeenCalledTimes(1);
    expect(mockProcessChunk).not.toHaveBeenCalled();

    await act(async () => {
      await jest.advanceTimersByTimeAsync(8500);
    });
    await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(2));
    expect(mockCancelCompression).toHaveBeenCalledWith("cancel-hung-11");
    expect(mockProcessChunk.mock.calls.map(call => call[0])).toEqual(paths);
    expect(mockCompress).toHaveBeenCalledTimes(1);

    fireEvent.press(view.getByText("square.fill"));
    await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
    await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
    expect(mockMergeChunks.mock.calls[0][0]).toBe(mockRef.sessionId);
    await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  } finally {
    view.unmount();
    jest.useRealTimers();
  }
});

it("does not publish a partial server video when one upload succeeds and the next fails", async () => {
  mockProcessChunk.mockResolvedValueOnce(undefined).mockRejectedValue(new Error("offline"));
  const notice = jest.spyOn(Alert, "alert");
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  const start = Date.now();
  await act(async () => {
    [1, 2].forEach(index => callbacks.onRecordingSegment({ status: "ready", path: `file:///documents/run-1/analysis/part-${index}.mp4`,
      captureStartTimeMs: start + (index - 1) * 10000, captureEndTimeMs: start + index * 10000 }));
  });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(2));
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(notice).toHaveBeenCalledWith("overlay.recording.analysisIncompleteTitle", "overlay.recording.analysisIncompleteBody"));
  expect(mockSave).toHaveBeenCalledTimes(1);
  expect(mockMergeChunks).not.toHaveBeenCalled();
  await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  view.unmount();
  notice.mockRestore();
});

it("recovers a failed chunk via deferred recovery queue and completes server merge", async () => {
  mockProcessChunk
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValue(undefined);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  const start = Date.now();
  await act(async () => {
    [1, 2].forEach(index => callbacks.onRecordingSegment({ status: "ready", path: `file:///documents/run-1/analysis/part-${index}.mp4`,
      captureStartTimeMs: start + (index - 1) * 10000, captureEndTimeMs: start + index * 10000 }));
  });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(3));
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
  expect(mockMergeChunks.mock.calls[0][0]).toBe(mockRef.sessionId);
  await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  view.unmount();
});

it("freezes v1 at capture start and seals receipts in capture order after out-of-order upload completion", async () => {
  process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION = "1";
  const first = deferred<{ taskId: string; sessionId: string; chunkId: string; sourceGeneration: string }>();
  mockProcessChunk.mockReturnValueOnce(first.promise).mockResolvedValueOnce({
    taskId: "second", sessionId: mockRef.sessionId, chunkId: "22", sourceGeneration: "9007199254740995",
  });
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  delete process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION;
  const callbacks = mockStartCamera.mock.calls[0][0];
  const start = Date.now();
  await act(async () => {
    [1, 2].forEach(index => callbacks.onRecordingSegment({ status: "ready",
      path: `file:///documents/run-1/analysis/part-${index}.mp4`,
      captureStartTimeMs: start + (index - 1) * 10000, captureEndTimeMs: start + index * 10000 }));
  });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(2));
  const identities = mockProcessChunk.mock.calls.map(call => call[2]);
  expect(identities.map(options => options.mediaPipelineVersion)).toEqual([1, 1]);
  expect(identities.map(options => options.captureOrder)).toEqual([1, 2]);
  expect(new Set(identities.map(options => options.clientChunkId)).size).toBe(2);

  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
  expect(mockMergeChunks).not.toHaveBeenCalled();
  await act(async () => { first.resolve({ taskId: "first", sessionId: mockRef.sessionId,
    chunkId: "21", sourceGeneration: "9007199254740993" }); });
  await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
  expect(mockMergeChunks.mock.calls[0][1]).toMatchObject({
    mediaPipelineVersion: 1, expectedChunkCount: 2,
    chunks: [{ chunk_id: "21", source_generation: "9007199254740993" }, { chunk_id: "22", source_generation: "9007199254740995" }],
  });
  view.unmount();
});

it("retains the v1 logical chunk ID and order when a failed upload is recovered from its original file", async () => {
  process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION = "1";
  mockProcessChunk.mockRejectedValueOnce(new Error("offline")).mockImplementation(async (_path, _sessionId, options) => ({
    taskId: "chunk", sessionId: mockRef.sessionId, chunkId: String(options.captureOrder + 40), sourceGeneration: "1234",
  }));
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  const start = Date.now();
  await act(async () => {
    [1, 2].forEach(index => callbacks.onRecordingSegment({ status: "ready",
      path: `file:///documents/run-1/analysis/part-${index}.mp4`,
      captureStartTimeMs: start + (index - 1) * 10000, captureEndTimeMs: start + index * 10000 }));
  });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(3));
  const firstAttempt = mockProcessChunk.mock.calls[0][2];
  const recovered = mockProcessChunk.mock.calls[2][2];
  expect(recovered).toMatchObject({ mediaPipelineVersion: 1,
    clientChunkId: firstAttempt.clientChunkId, captureOrder: firstAttempt.captureOrder,
    startSecs: firstAttempt.startSecs, endSecs: firstAttempt.endSecs,
  });
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockMergeChunks).toHaveBeenCalledTimes(1));
  expect(mockMergeChunks.mock.calls[0][1].chunks).toEqual([
    { chunk_id: "41", source_generation: "1234" }, { chunk_id: "42", source_generation: "1234" },
  ]);
  view.unmount();
});

it("withholds a v1 merge if upload completion lacks an approved source receipt", async () => {
  process.env.EXPO_PUBLIC_MEDIA_PIPELINE_VERSION = "1";
  const notice = jest.spyOn(Alert, "alert");
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  await act(async () => { callbacks.onRecordingSegment({ status: "ready",
    path: "file:///documents/run-1/analysis/part-1.mp4", captureStartTimeMs: Date.now(), captureEndTimeMs: Date.now() + 10000 }); });
  await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(1));
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(notice).toHaveBeenCalledWith("overlay.recording.analysisIncompleteTitle", "overlay.recording.analysisIncompleteBody"));
  expect(mockMergeChunks).not.toHaveBeenCalled();
  expect(mockSave).toHaveBeenCalledTimes(1);
  view.unmount();
  notice.mockRestore();
});

it("withholds server merge after the upload wait expires while preserving readers until the late upload settles", async () => {
  jest.useFakeTimers();
  const upload = deferred<void>();
  mockProcessChunk.mockReturnValueOnce(upload.promise);
  const notice = jest.spyOn(Alert, "alert");
  const view = render(<VisionTestPage />);
  try {
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
    const callbacks = mockStartCamera.mock.calls[0][0];
    const start = Date.now();
    await act(async () => { callbacks.onRecordingSegment({ status: "ready", path: "file:///documents/run-1/analysis/pending.mp4",
      captureStartTimeMs: start, captureEndTimeMs: start + 10000 }); });
    await waitFor(() => expect(mockProcessChunk).toHaveBeenCalledTimes(1));
    fireEvent.press(view.getByText("square.fill"));
    await act(async () => { callbacks.onRecordingSourceFinalized(source()); await callbacks.onRecordingFinished(source()); });
    await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
    await act(async () => { await jest.advanceTimersByTimeAsync(90500); });
    expect(notice).toHaveBeenCalledWith("overlay.recording.analysisIncompleteTitle", "overlay.recording.analysisIncompleteBody");
    expect(mockMergeChunks).not.toHaveBeenCalled();
    expect(mockReleases.some(release => release.mock.calls.length === 0)).toBe(true);
    await act(async () => { upload.resolve(); });
    await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
    expect(mockMergeChunks).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    notice.mockRestore();
    jest.useRealTimers();
  }
});

it.each(["callback", "throw"])("allows a new session after native startup %s failure without waiting for gallery or server", async (delivery) => {
  const error = { code: "capture/create-recorder-error", message: "Audio settings unavailable" };
  if (delivery === "throw") mockStartCamera.mockImplementationOnce(() => { throw error; });
  const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
  try {
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
    if (delivery === "callback") {
      await act(async () => { mockStartCamera.mock.calls[0][0].onRecordingError(error); });
    }
    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith("/history"));
    expect(mockStopped).toHaveBeenCalledWith(mockRef, {
      complete: false, reason: "[capture/create-recorder-error] Audio settings unavailable",
    });
    expect(mockSave).not.toHaveBeenCalled();
    expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
    expect(mockAddPending).not.toHaveBeenCalled();
    expect(mockSetRecording).toHaveBeenLastCalledWith(false);
    expect(alert.mock.calls.map(call => call[0])).toEqual(["originalVideo.captureFailedTitle"]);
    expect(alert.mock.calls[0][1]).toContain(error.message);
    await waitFor(() => expect(mockReleases[0]).toHaveBeenCalledTimes(1));
    view.unmount();

    const next = render(<VisionTestPage />);
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(2));
    expect(mockSetRecording).toHaveBeenLastCalledWith(true);
    const callbacks = mockStartCamera.mock.calls[1][0];
    fireEvent.press(next.getByText("square.fill"));
    await act(async () => { await callbacks.onRecordingFinished(source()); });
    await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
    next.unmount();
  } finally { alert.mockRestore(); }
});

it("displays 4xx upload error notice on the screen when chunk upload fails with 4xx", async () => {
  const err400: any = new Error("API Error [400]: Bad Request");
  err400.status = 400;
  mockProcessChunk.mockRejectedValueOnce(err400);

  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];

  await act(async () => {
    callbacks.onRecordingSegment({
      status: "ready",
      path: "file:///documents/run-1/analysis/part.mp4",
      captureStartTimeMs: Date.now() - 1000,
      captureEndTimeMs: Date.now(),
    });
  });

  await waitFor(() => {
    expect(view.getByTestId("upload-status-banner")).toBeTruthy();
    expect(view.getByText("overlay.recording.chunk4xxError")).toBeTruthy();
  });
  view.unmount();
});

it("displays retry notice when chunk upload triggers onRetry callback", async () => {
  let finishUpload!: () => void;
  mockProcessChunk.mockImplementationOnce((_uri, _sessionId, options) => {
    options.onRetry?.(2, 5, new Error("Temporary network timeout"));
    return new Promise<void>((resolve) => {
      finishUpload = resolve;
    });
  });

  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];

  await act(async () => {
    callbacks.onRecordingSegment({
      status: "ready",
      path: "file:///documents/run-1/analysis/part.mp4",
      captureStartTimeMs: Date.now() - 1000,
      captureEndTimeMs: Date.now(),
    });
  });

  await waitFor(() => {
    expect(view.getByTestId("upload-status-banner")).toBeTruthy();
    expect(view.getByText("overlay.recording.chunkRetrying")).toBeTruthy();
  });

  await act(async () => {
    finishUpload();
  });
  await waitFor(() => {
    expect(view.queryByTestId("upload-status-banner")).toBeNull();
  });
  view.unmount();
});

it("binds recent setup once after the first original is accepted without delaying finalization", async () => {
  const prepared = deferred<typeof mockRef>();
  const registered = deferred<void>();
  const setupSaved = deferred<void>();
  mockPrepare.mockReturnValueOnce(prepared.promise);
  mockAdd.mockReturnValueOnce(registered.promise);
  mockSaveRecordingSetup.mockReturnValueOnce(setupSaved.promise);
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockPrepare).toHaveBeenCalled());
  expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
  await act(async () => { prepared.resolve(mockRef); });
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalled());
  expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
  const callbacks = mockStartCamera.mock.calls[0][0];
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); });
  expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => {
    registered.resolve();
    await callbacks.onRecordingFinished(source());
    callbacks.onRecordingSourceFinalized(source());
  });
  expect(mockSaveRecordingSetup).toHaveBeenCalledWith("launch-snapshot", { ownerUserId: 7, profileId: 3 }, mockRef.sessionId);
  expect(mockSaveRecordingSetup).toHaveBeenCalledTimes(1);
  expect(mockAdd).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  await act(async () => { setupSaved.resolve(); });
  view.unmount();
});

it("preserves inherited setup provenance when registering the first original fails", async () => {
  mockAdd.mockRejectedValueOnce(new Error("Original manifest write failed"));
  const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
  try {
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
    const callbacks = mockStartCamera.mock.calls[0][0];
    await act(async () => { await callbacks.onRecordingFinished(source()); });
    await waitFor(() => expect(mockStopped).toHaveBeenCalledWith(mockRef, expect.objectContaining({ complete: false })));
    expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
    view.unmount();
  } finally { alert.mockRestore(); }
});

it("rechecks inherited setup at actual start and never creates a new capture after its source was archived", async () => {
  mockValidateRecordingSetup.mockRejectedValueOnce(new Error("archived_source"));
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockValidateRecordingSetup).toHaveBeenCalledWith("launch-snapshot", { ownerUserId: 7, profileId: 3 }));
  expect(mockPrepare).toHaveBeenCalledTimes(1);
  expect(mockStopped).toHaveBeenCalledWith(mockRef, {complete:false,reason:"recording_source_unavailable"});
  expect(mockStartCamera).not.toHaveBeenCalled();
  expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
  view.unmount();
});

it("retires prepared originals when their inherited source is archived during asynchronous preparation", async () => {
  const prepared=deferred<typeof mockRef>();
  mockPrepare.mockReturnValueOnce(prepared.promise);
  const view=render(<VisionTestPage />);
  await waitFor(()=>expect(mockPrepare).toHaveBeenCalledTimes(1));
  mockValidateRecordingSetup.mockRejectedValueOnce(new Error("archived_source"));
  await act(async()=>{prepared.resolve(mockRef);});
  await waitFor(()=>expect(mockStopped).toHaveBeenCalledWith(mockRef,{complete:false,reason:"recording_source_unavailable"}));
  expect(mockStartCamera).not.toHaveBeenCalled(); expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
  view.unmount();
});
it("checks again after first source-directory preparation before starting the native camera", async () => {
  const directory=deferred<string>();
  mockBegin.mockReturnValueOnce(directory.promise);
  const view=render(<VisionTestPage />);
  await waitFor(()=>expect(mockBegin).toHaveBeenCalledTimes(1));
  mockValidateRecordingSetup.mockRejectedValueOnce(new Error("archived_source"));
  await act(async()=>{directory.resolve("file:///documents/run-1/");});
  expect(mockStartCamera).not.toHaveBeenCalled(); expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
  view.unmount();
});

describe("recording setup validation at camera start", () => {
  const inheritedSessionId = "WOD-20261029-SOURCE";
  const launchParam = (overrides: Partial<RecentSetupSnapshot> = {}) => JSON.stringify({
    version: 2,
    snapshotId: "recording-start-regression",
    ownerUserId: 7,
    profileId: 3,
    savedAt: Date.now(),
    origin: "draft",
    workoutType: "wod",
    wodDescription: "",
    rawText: "",
    selectedMovements: [],
    customMovements: [],
    sessionAppearance: "",
    ...overrides,
  });

  beforeEach(() => {
    const { validateRecordingSetup } = jest.requireActual<typeof import("@/features/wod/recentWorkoutSetup")>("@/features/wod/recentWorkoutSetup");
    mockValidateRecordingSetup.mockImplementation((raw, scope) => validateRecordingSetup(raw as string | undefined, scope as { ownerUserId: number; profileId: number }));
  });

  it.each(["string", "array"])("starts native recording from an empty draft passed as a %s without an archive request", async paramKind => {
    const raw = launchParam();
    mockRecordingSetupParam = paramKind === "array" ? [raw] : raw;
    const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    const view = render(<VisionTestPage />);
    try {
      await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
      expect(mockValidateRecordingSetup).toHaveBeenCalledTimes(2);
      expect(mockValidateRecordingSetup).toHaveBeenNthCalledWith(2, raw, { ownerUserId: 7, profileId: 3 });
      expect(mockFetchArchiveStates).not.toHaveBeenCalled();
      expect(alert).not.toHaveBeenCalled();
      fireEvent.press(view.getByText("square.fill"));
      await act(async () => { await mockStartCamera.mock.calls[0][0].onRecordingFinished(source()); });
      await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
    } finally { view.unmount(); alert.mockRestore(); }
  });

  it("starts native recording with selected movements and no WOD description", async () => {
    mockRecordingSetupParam = launchParam({ selectedMovements: ["Air Squat"] });
    const view = render(<VisionTestPage />);
    try {
      await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
      expect(mockValidateRecordingSetup).toHaveBeenCalledTimes(2);
      expect(mockFetchArchiveStates).not.toHaveBeenCalled();
      fireEvent.press(view.getByText("square.fill"));
      await act(async () => { await mockStartCamera.mock.calls[0][0].onRecordingFinished(source()); });
      await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
    } finally { view.unmount(); }
  });

  it.each(["malformed", "foreign account", "foreign profile", "archived inherited"])("blocks a %s snapshot before native recording", async scenario => {
    if (scenario === "malformed") mockRecordingSetupParam = "{invalid-json";
    else if (scenario === "foreign account") mockRecordingSetupParam = launchParam({ ownerUserId: 99 });
    else if (scenario === "foreign profile") mockRecordingSetupParam = launchParam({ profileId: 99 });
    else {
      mockRecordingSetupParam = launchParam({ origin: "recording", sourceSessionId: inheritedSessionId });
      mockFetchArchiveStates.mockResolvedValue({
        checked_at: "now", history_revision: "2",
        states: [{ session_id: inheritedSessionId, state: "archived" }],
      });
    }
    const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    const view = render(<VisionTestPage />);
    try {
      await waitFor(() => expect(mockStopped).toHaveBeenCalledWith(mockRef, { complete: false, reason: "recording_source_unavailable" }));
      expect(mockStartCamera).not.toHaveBeenCalled();
      expect(mockBegin).not.toHaveBeenCalled();
      expect(mockSaveRecordingSetup).not.toHaveBeenCalled();
      expect(alert).toHaveBeenCalledWith("common.error", "setup.archiveSourceUnavailable");
      if (scenario === "archived inherited") expect(mockFetchArchiveStates).toHaveBeenCalledWith(3, [inheritedSessionId], undefined);
      else expect(mockFetchArchiveStates).not.toHaveBeenCalled();
    } finally { view.unmount(); alert.mockRestore(); }
  });
});
