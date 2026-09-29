import React from "react";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import VisionTestPage from "../visionTestPage";

const mockStartCamera = jest.fn();
const mockStopCamera = jest.fn();
const mockNative = {
  startRecording: (...args: unknown[]) => mockStartCamera(...args),
  stopRecording: () => mockStopCamera(),
  takeSnapshot: jest.fn(),
};

let mockLiveFeedback: {
  coaching?: string;
  captureAdvice?: string;
  summary?: { available: boolean; review_state: string; movements: []; unassessed: [] };
} = {
  coaching: "팔꿈치를 높게 유지하세요.",
};

jest.mock("expo-router", () => ({
  router: { replace: jest.fn(), back: jest.fn(), push: jest.fn() },
  useFocusEffect: () => {},
  useLocalSearchParams: () => ({
    autoRecord: "true",
    continuousRecording: "true",
    showSkeleton: "false",
    skipCompression: "true",
  }),
}));

jest.mock("expo-router/react-navigation", () => ({ useIsFocused: () => true }));

jest.mock("react-native-vision-camera", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  return {
    Camera: ReactActual.forwardRef((_props: any, ref: any) => {
      ReactActual.useImperativeHandle(ref, () => mockNative, []);
      ReactActual.useEffect(() => {
        _props.onInitialized();
      }, []);
      return null;
    }),
    useCameraDevice: () => ({ id: "rear", physicalDevices: [] }),
    useCameraFormat: () => ({ videoWidth: 1280, videoHeight: 720, videoStabilizationModes: [] }),
    useCameraPermission: () => ({ hasPermission: true, requestPermission: jest.fn() }),
    useMicrophonePermission: () => ({ hasPermission: true, requestPermission: jest.fn() }),
  };
});

jest.mock("@/components/ui/icon-symbol", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { IconSymbol: ({ name }: { name: string }) => ReactActual.createElement(Text, {}, name) };
});

jest.mock("@/features/i18n", () => ({
  t: (key: string) => key,
  useLocale: () => "ko",
}));

jest.mock("@/features/auth/useAuthStore", () => ({
  useAuthStore: {
    getState: () => ({
      userId: 7,
      isLoggedIn: true,
      sessionExpiredDuringRecording: false,
      setRecordingActive: jest.fn(),
      finishDeferredUnauthorized: jest.fn(),
    }),
  },
}));

jest.mock("@/store/useProfileStore", () => ({
  useProfileStore: (selector: (value: unknown) => unknown) => selector({ activeProfileId: 3 }),
}));

jest.mock("@/store/useMergeStatus", () => ({
  useMergeStatus: { getState: () => ({ addPending: jest.fn(), removePending: jest.fn() }) },
}));

jest.mock("@/features/wod/workoutType", () => ({
  buildWorkoutSessionId: () => "WOD-20260929-TEST",
  parseWorkoutType: () => "WOD",
  formatWorkoutTypeLabel: () => "WOD",
}));

const mockRef = { ownerUserId: 7, profileId: 3, sessionId: "WOD-20260929-TEST" };

jest.mock("@/features/video/originalVideoStore", () => ({
  prepareOriginalSession: jest.fn().mockResolvedValue(mockRef),
  beginOriginalChunk: jest.fn().mockImplementation(async (_ref: unknown, order: number) => `file:///documents/run-${order}/`),
  addOriginalChunk: jest.fn().mockResolvedValue({}),
  markOriginalRecordingStopped: jest.fn().mockResolvedValue({}),
  finalizeAndSaveOriginal: jest.fn().mockResolvedValue({ status: "saved" }),
  holdOriginalFiles: () => () => {},
}));

jest.mock("@/modules/apple-on-device-ai", () => ({ environmentNativeAvailable: false, appleEnvironment: {} }));
jest.mock("@/features/environment/store", () => ({ flushEnvironmentUploads: async () => {} }));
jest.mock("@/features/environment/useEnvironmentRecorder", () => ({
  useEnvironmentRecorder: () => ({
    status: "off",
    start: jest.fn(),
    stop: jest.fn().mockResolvedValue(undefined),
    pause: jest.fn(),
    resume: jest.fn(),
    event: jest.fn(),
    offerChunk: jest.fn(),
  }),
}));
jest.mock("@/features/environment/EnvironmentCard", () => ({ EnvironmentLiveCard: () => null }));
jest.mock("@/features/wod/useLiveWorkoutFeedback", () => ({
  useLiveWorkoutFeedback: () => mockLiveFeedback,
}));
jest.mock("@/features/wod/ui/ActivitySummaryCard", () => ({ ActivitySummaryContent: () => null }));
jest.mock("@/features/ai-coach/ui/AppleAiFeedbackCard", () => ({ AppleAiFeedbackCard: () => null }));
jest.mock("@/features/ai-coach/useAppleAiFeedback", () => ({
  useAppleAiFeedback: () => ({ status: "off", stop: jest.fn().mockResolvedValue(undefined) }),
}));
jest.mock("@/features/ai-coach/appleAiUpload", () => ({ saveAppleAiObservation: async () => {}, flushAppleAiUploads: async () => {} }));
jest.mock("@/features/debug/telemetryRecorder", () => ({ TelemetryRecorder: { start: () => {}, registerProvider: () => {}, stop: async () => null } }));
jest.mock("@/features/debug/telemetryUpload", () => ({ enqueueUpload: async () => {}, flushPendingUploads: async () => {} }));
jest.mock("@/features/wod/api", () => ({ mergeChunks: async () => {}, processWorkoutChunk: jest.fn() }));
jest.mock("expo-keep-awake", () => ({ activateKeepAwakeAsync: async () => {}, deactivateKeepAwake: () => {} }));
jest.mock("expo-media-library/legacy", () => ({ requestPermissionsAsync: async () => ({ granted: true }) }));
jest.mock("expo-screen-orientation", () => ({ lockAsync: async () => {}, OrientationLock: { LANDSCAPE: 3, PORTRAIT_UP: 1 } }));
jest.mock("react-native-compressor", () => ({ Video: { compress: async (path: string) => path } }));
jest.mock("@/features/health/polar/polarSensorRecorder", () => ({
  PolarSensorRecorder: {
    start: () => {},
    pause: () => {},
    resume: () => {},
    stop: async () => null,
    getLiveStatus: () => ({ accSamples: 0, dropped: null }),
  },
}));
jest.mock("@/features/health/polar/sensorTelemetryUpload", () => ({ enqueueSensorUpload: async () => {}, flushSensorUploads: async () => {} }));
let mockBatteryLevel: number | null = 85;
jest.mock("@/features/health/useBleHeartRate", () => ({
  useBleHeartRate: () => ({
    bpm: 0,
    quality: "missing",
    status: "off",
    batteryLevel: mockBatteryLevel,
    getReading: () => ({ bpm: null }),
    resetQuality: () => {},
  }),
}));
jest.mock("@/features/ai-coach/frame-processors/usePoseDetection", () => ({
  usePoseDetection: () => ({
    frameProcessor: undefined,
    poseResult: undefined,
    isModelLoaded: false,
    monitorData: { confidence: 0, motion: 0, isWorkingOut: false },
    getWorkoutConfidence: () => 0,
    getLatestMotion: () => null,
    getCaptureConfidence: () => 0,
    resetCaptureObservations: () => {},
  }),
}));
jest.mock("@/features/ai-coach/ui/EnergyMonitor", () => ({ EnergyMonitor: () => null }));
jest.mock("@/features/ai-coach/ui/SkeletonOverlay", () => ({ SkeletonOverlay: () => null }));

describe("VisionTestPage Feedback Overlay UI", () => {
  it("keeps telemetry hidden by default and expands upon user toggle", async () => {
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));

    // Stop button is always present
    expect(view.getByText("square.fill")).toBeTruthy();

    // By default, telemetry section is collapsed/hidden
    expect(view.queryByText("overlay.feedback.title")).toBeNull();
    expect(view.queryByText("overlay.recording.aiVisionTitle")).toBeNull();
    expect(view.queryByText("overlay.recording.observedActivityTitle")).toBeNull();

    // Toggle button to expand telemetry is present
    expect(view.getByLabelText("overlay.recording.expandTelemetry")).toBeTruthy();

    // Expand telemetry via toggle
    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));

    // Now telemetry is visible
    await waitFor(() => {
      expect(view.getByText("overlay.feedback.title")).toBeTruthy();
    });
    expect(view.getByText("팔꿈치를 높게 유지하세요.")).toBeTruthy();
    expect(view.getByText("overlay.feedback.collapse")).toBeTruthy();
    expect(view.getByText("overlay.recording.aiVisionTitle")).toBeTruthy();
    expect(view.getByText("overlay.recording.observedActivityTitle")).toBeTruthy();

    // Inside feedback card, collapsing feedback text works
    fireEvent.press(view.getByLabelText("overlay.feedback.collapse"));
    await waitFor(() => {
      expect(view.getByText("overlay.feedback.expand")).toBeTruthy();
    });
    expect(view.queryByText("overlay.feedback.title")).toBeNull();

    // Collapsing entire telemetry via main toggle button
    fireEvent.press(view.getByLabelText("overlay.recording.collapseTelemetry"));

    // Telemetry is collapsed again
    await waitFor(() => {
      expect(view.queryByText("overlay.recording.aiVisionTitle")).toBeNull();
    });
    expect(view.queryByText("overlay.feedback.title")).toBeNull();

    // Press stop recording
    fireEvent.press(view.getByText("square.fill"));
    view.unmount();
  });

  it("renders slim status bar, controls without lap/flag icon, and handles flip camera", async () => {
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(mockStartCamera).toHaveBeenCalledTimes(1));

    // Slim status bar elements: REC and Polar H10 battery
    expect(view.getByText("overlay.recording.rec")).toBeTruthy();
    expect(view.getByText("85%")).toBeTruthy();

    // Control bar: Lap (flag/report) icon MUST NOT exist
    expect(view.queryByLabelText("overlay.recording.lap")).toBeNull();

    // Core controls are present
    expect(view.getByLabelText("overlay.recording.pause")).toBeTruthy();
    expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy();
    expect(view.getByLabelText("overlay.recording.flipCamera")).toBeTruthy();

    // Pressing Flip Camera works without error
    fireEvent.press(view.getByLabelText("overlay.recording.flipCamera"));

    // Stop button works
    fireEvent.press(view.getByText("square.fill"));
    view.unmount();
  });
});
