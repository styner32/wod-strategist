import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";

import VisionTestPage from "../visionTestPage";

const mockRef = { ownerUserId: 7, profileId: 3, sessionId: "WOD-20260929-LIFECYCLE" };
const mockPrepare = jest.fn();
const mockBegin = jest.fn();
const mockAdd = jest.fn();
const mockStopped = jest.fn();
const mockSave = jest.fn();
const mockReleases: jest.Mock[] = [];
const mockStartCamera = jest.fn();
const mockStopCamera = jest.fn();
const mockReplace = jest.fn();
const mockConfidence = jest.fn();
const mockProcessChunk = jest.fn();
let mockContinuousRecording = true;
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

jest.mock("expo-router", () => ({
  router: { replace: (...args: unknown[]) => mockReplace(...args), back: jest.fn(), push: jest.fn() },
  useFocusEffect: () => {},
  useLocalSearchParams: () => ({ autoRecord: "true", continuousRecording: String(mockContinuousRecording), showSkeleton: "false", skipCompression: "true" }),
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
jest.mock("@/features/wod/workoutType", () => ({
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
jest.mock("react-native-compressor", () => ({ Video: { compress: async (path: string) => path } }));
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
  monitorData: { confidence: 0, motion: 0, isWorkingOut: false },
  getWorkoutConfidence: () => 0, getLatestMotion: () => null,
  getCaptureConfidence: (...args: unknown[]) => mockConfidence(...args), resetCaptureObservations: () => {},
}) }));
jest.mock("@/features/ai-coach/ui/EnergyMonitor", () => ({ EnergyMonitor: () => null }));
jest.mock("@/features/ai-coach/ui/SkeletonOverlay", () => ({ SkeletonOverlay: () => null }));
jest.mock("@/features/ai-coach/ui/AppleAiFeedbackCard", () => ({ AppleAiFeedbackCard: () => null }));
jest.mock("@/features/ai-coach/useAppleAiFeedback", () => ({ useAppleAiFeedback: () => ({ status: "off", stop: mockAppleStop }) }));
jest.mock("@/features/ai-coach/appleAiUpload", () => ({ saveAppleAiObservation: async () => {}, flushAppleAiUploads: async () => {} }));
jest.mock("@/features/debug/telemetryRecorder", () => ({ TelemetryRecorder: { start: () => {}, registerProvider: () => {}, stop: async () => null } }));
jest.mock("@/features/debug/telemetryUpload", () => ({ enqueueUpload: async () => {}, flushPendingUploads: async () => {} }));
jest.mock("@/features/wod/api", () => ({ mergeChunks: async () => {}, processWorkoutChunk: (...args: unknown[]) => mockProcessChunk(...args) }));

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
beforeEach(() => {
  jest.clearAllMocks();
  mockStartCamera.mockReset();
  mockContinuousRecording = true;
  mockReleases.length = 0;
  mockPrepare.mockResolvedValue(mockRef);
  mockBegin.mockImplementation(async (_ref: unknown, order: number) => `file:///documents/run-${order}/`);
  mockAdd.mockResolvedValue(undefined);
  mockStopped.mockResolvedValue(undefined);
  mockSave.mockResolvedValue({ status: "saved" });
  mockConfidence.mockReturnValue(0);
  mockProcessChunk.mockResolvedValue(undefined);
  mockStopCamera.mockResolvedValue(undefined);
  mockAppleStop.mockResolvedValue(undefined);
  mockEnvironment.stop.mockResolvedValue(undefined);
  mockEnvironment.pause.mockResolvedValue(undefined);
  logs = jest.spyOn(console, "log").mockImplementation(() => {});
  warnings = jest.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => { logs.mockRestore(); warnings.mockRestore(); });

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

it.each(["network", "confidence"])("keeps complete originals saveable and releases readers after an analysis %s failure", async (failure) => {
  if (failure === "network") mockProcessChunk.mockRejectedValueOnce(new Error("offline"));
  else mockConfidence.mockImplementationOnce(() => { throw new Error("confidence unavailable"); });
  const view = render(<VisionTestPage />);
  await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));
  const callbacks = mockStartCamera.mock.calls[0][0];
  await act(async () => { callbacks.onRecordingSegment({ status: "ready", path: "file:///documents/run-1/analysis/part.mp4",
    captureStartTimeMs: Date.now() - 1000, captureEndTimeMs: Date.now() }); });
  fireEvent.press(view.getByText("square.fill"));
  await act(async () => { callbacks.onRecordingSourceFinalized(source()); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1));
  expect(mockStopped).toHaveBeenCalledWith(mockRef, { complete: true, reason: undefined });
  await act(async () => { await callbacks.onRecordingFinished(source()); });
  await waitFor(() => expect(mockReleases.every(release => release.mock.calls.length === 1)).toBe(true));
  view.unmount();
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
