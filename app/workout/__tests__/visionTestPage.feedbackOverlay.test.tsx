import React from "react";
import { fireEvent, render, waitFor, within } from "@testing-library/react-native";
import VisionTestPage from "../visionTestPage";
import { StyleSheet } from "react-native";
import { OrientationLock } from "expo-screen-orientation";
import type { ActivitySummary } from "../../../shared/activity";

const mockStartCamera = jest.fn();
const mockCameraMount = jest.fn();
const mockCameraUnmount = jest.fn();
const mockStopCamera = jest.fn();
const mockOrientationLock = jest.fn().mockResolvedValue(undefined);
const mockNative = {
  startRecording: (...args: unknown[]) => mockStartCamera(...args),
  stopRecording: () => mockStopCamera(),
  takeSnapshot: jest.fn(),
};

let mockLiveFeedback: {
  coaching?: string;
  captureAdvice?: string;
  summary?: ActivitySummary;
} = {
  coaching: "팔꿈치를 높게 유지하세요.",
};

let mockRouteParams: Record<string, string> = {};
let mockMonitorData = { confidence: 0, motion: 0, isWorkingOut: false };
let mockModelLoaded = false;

jest.mock("expo-router", () => ({
  router: { replace: jest.fn(), back: jest.fn(), push: jest.fn() },
  useFocusEffect: (effect: () => void | (() => void)) => {
    const ReactActual = jest.requireActual<typeof import("react")>("react");
    ReactActual.useEffect(effect, [effect]);
  },
  useLocalSearchParams: () => ({
    autoRecord: "true",
    continuousRecording: "true",
    showSkeleton: "false",
    skipCompression: "true",
    ...mockRouteParams,
  }),
}));

jest.mock("expo-router/react-navigation", () => ({ useIsFocused: () => true }));

jest.mock("react-native-vision-camera", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  return {
    Camera: ReactActual.forwardRef((_props: any, ref: any) => {
      ReactActual.useImperativeHandle(ref, () => mockNative, []);
      ReactActual.useEffect(() => {
        mockCameraMount();
        _props.onInitialized();
        return () => mockCameraUnmount();
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
  t: (key: string, options?: { value?: string }) => options?.value === undefined ? key : `${key}:${options.value}`,
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
jest.mock("@/features/wod/api", () => ({
  mergeChunks: async () => {},
  processWorkoutChunk: jest.fn(),
  is4xxError: (err: any) => Boolean(err?.status >= 400 && err?.status < 500),
  getErrorStatusCode: (err: any) => err?.status ?? null,
}));
jest.mock("expo-keep-awake", () => ({ activateKeepAwakeAsync: async () => {}, deactivateKeepAwake: () => {} }));
jest.mock("expo-media-library/legacy", () => ({ requestPermissionsAsync: async () => ({ granted: true }) }));
jest.mock("expo-screen-orientation", () => ({
  lockAsync: (...args: unknown[]) => mockOrientationLock(...args),
  OrientationLock: { DEFAULT: 0, LANDSCAPE: 5, PORTRAIT_UP: 3 },
}));
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
    isModelLoaded: mockModelLoaded,
    monitorData: mockMonitorData,
    getWorkoutConfidence: () => 0,
    getLatestMotion: () => null,
    getCaptureConfidence: () => 0,
    resetCaptureObservations: () => {},
  }),
}));
jest.mock("@/features/ai-coach/ui/EnergyMonitor", () => ({ EnergyMonitor: () => null }));
jest.mock("@/features/ai-coach/ui/SkeletonOverlay", () => ({ SkeletonOverlay: () => null }));

function summary(movements: ActivitySummary["movements"], available = true): ActivitySummary {
  return {
    version: 1, available, source_version: "v1", review_version: 1,
    review_state: "provisional", coverage_scope: "recorded_chunks",
    movements, unassessed: [], reviews: [],
  };
}

describe("VisionTestPage Feedback Overlay UI", () => {
  beforeEach(() => {
    mockStartCamera.mockClear();
    mockCameraMount.mockClear();
    mockCameraUnmount.mockClear();
    mockOrientationLock.mockReset().mockResolvedValue(undefined);
    mockRouteParams = {};
    mockLiveFeedback = { coaching: "팔꿈치를 높게 유지하세요." };
    mockMonitorData = { confidence: 0, motion: 0, isWorkingOut: false };
    mockModelLoaded = false;
  });
  afterEach(() => jest.restoreAllMocks());

  it("keeps telemetry hidden by default and expands upon user toggle", async () => {
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());

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
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());

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

  it.each([undefined, summary([]), summary([{ movement: "Plank", unit: "seconds", count: 0, seconds: 30 }], false)])(
    "shows no observations instead of substituting planned movements (%#)", async (value) => {
      mockRouteParams = { movements: "Back Squat, Front Squat" };
      mockLiveFeedback = { summary: value };
      const view = render(<VisionTestPage />);
      await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
      fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
      expect(view.getByText("activity.noEvidence")).toBeTruthy();
      for (const text of ["Back Squat", "Front Squat", "Plank", "15", "4", "overlay.recording.sensorSynced"]) {
        expect(view.queryByText(text)).toBeNull();
      }
      view.unmount();
    },
  );

  it("does not invent default squats when no movements were configured", async () => {
    mockLiveFeedback = {};
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
    expect(view.getByText("activity.noEvidence")).toBeTruthy();
    expect(view.queryByText("Back Squat")).toBeNull();
    expect(view.queryByText("Front Squat")).toBeNull();
    view.unmount();
  });

  it("preserves repetitions, seconds, and provisional status in mixed observations", async () => {
    mockLiveFeedback = { summary: summary([
      { movement: "Squat", unit: "reps", count: 7, seconds: 0 },
      { movement: "Plank", unit: "seconds", count: 0, seconds: 30.25 },
    ]) };
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
    const squat = within(view.getByTestId("observed-movement-Squat-reps"));
    const plank = within(view.getByTestId("observed-movement-Plank-seconds"));
    expect(squat.getByText("7")).toBeTruthy();
    expect(squat.getByText("overlay.recording.repsUnit")).toBeTruthy();
    expect(plank.getByText("30.3")).toBeTruthy();
    expect(plank.getByText("overlay.recording.secondsUnit")).toBeTruthy();
    expect(plank.queryByText("0")).toBeNull();
    expect(view.getByText("activity.state.provisional")).toBeTruthy();
    view.unmount();
  });

  it.each([true, false])("renders only measured pose diagnostics (moving=%s)", async (moving) => {
    mockModelLoaded = true;
    mockMonitorData = { confidence: 0.8, motion: 0.025, isWorkingOut: moving };
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
    expect(view.getByText("overlay.recording.detectionConfidence:80")).toBeTruthy();
    expect(view.getByText("overlay.recording.motionMagnitude:0.025")).toBeTruthy();
    expect(view.getByText(`overlay.recording.${moving ? "motionDetected" : "motionNotDetected"}`)).toBeTruthy();
    expect(view.queryByText(/eccentric_descent|ankle_symmetry|stability_index|motion_velocity|ms/)).toBeNull();
    view.unmount();
  });

  it("shows model loading without fabricated pose measurements", async () => {
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
    expect(view.getByText("overlay.recording.modelLoading")).toBeTruthy();
    expect(view.queryByText(/overlay.recording.detectionConfidence|overlay.recording.motionMagnitude/)).toBeNull();
    view.unmount();
  });

  it.each([
    { width: 844, height: 390, remainingHeight: 307, aspectRatio: "16:9", ratio: 16 / 9 },
    { width: 568, height: 320, remainingHeight: 245, aspectRatio: "4:3", ratio: 4 / 3 },
    { width: 390, height: 844, remainingHeight: 560, aspectRatio: "16:9", ratio: 9 / 16 },
    { width: 390, height: 844, remainingHeight: 560, aspectRatio: "4:3", ratio: 3 / 4 },
  ])("fits the measured camera area at $width x $height ($aspectRatio)", async ({ width, height, remainingHeight, aspectRatio, ratio }) => {
    const rn = require("react-native");
    jest.spyOn(rn, "useWindowDimensions").mockReturnValue({ width, height, scale: 3, fontScale: 1 });
    mockRouteParams = { aspectRatio };
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    const availableWidth = width - 64; // Remaining area after safe insets/margins.
    const layout = (spaceHeight: number) => {
      fireEvent(view.getByTestId("recording-viewfinder-space"), "layout", {
        nativeEvent: { layout: { x: 0, y: 0, width: availableWidth, height: spaceHeight } },
      });
      const frame = StyleSheet.flatten(view.getByTestId("recording-viewfinder").props.style);
      expect(frame.height).toBeGreaterThan(0);
      expect(frame.height).toBeLessThanOrEqual(spaceHeight);
      expect(frame.width).toBeLessThanOrEqual(availableWidth);
      expect(frame.width / frame.height).toBeCloseTo(ratio);
      expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy();
      expect(view.getByLabelText("overlay.recording.pause")).toBeTruthy();
    };
    layout(remainingHeight);
    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
    expect(StyleSheet.flatten(view.getByTestId("recording-viewfinder-space").props.style).maxHeight)
      .toBe(width > height ? undefined : 210);
    layout(width > height ? remainingHeight : remainingHeight / 2);
    fireEvent.press(view.getByLabelText("overlay.recording.collapseTelemetry"));
    layout(remainingHeight);
    // Resizing must not recreate the native recorder or start a new recording.
    expect(mockStartCamera).toHaveBeenCalledTimes(1);
    view.unmount();
  });


  it.each([
    { params: {}, expected: OrientationLock.DEFAULT },
    { params: { landscapeMode: "false" }, expected: OrientationLock.DEFAULT },
    { params: { landscapeMode: "true" }, expected: OrientationLock.LANDSCAPE },
    { params: { previewOnly: "true", autoRecord: "false" }, expected: OrientationLock.DEFAULT },
  ])("applies recording orientation on focus and restores portrait on exit (%#)", async ({ params, expected }) => {
    mockRouteParams = params as Record<string, string>;
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(mockOrientationLock).toHaveBeenCalledWith(expected));
    view.unmount();
    expect(mockOrientationLock).toHaveBeenLastCalledWith(OrientationLock.PORTRAIT_UP);
  });

  it("reports a rejected orientation request without failing the recording screen", async () => {
    const error = new Error("Orientation unavailable");
    mockOrientationLock.mockRejectedValueOnce(error);
    const warn = jest.spyOn(console, "warn").mockImplementation(() => {});
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("Recording screen orientation failed", error));
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    view.unmount();
    expect(mockOrientationLock).toHaveBeenLastCalledWith(OrientationLock.PORTRAIT_UP);
  });

  it("adapts portrait to landscape and back during recording without restarting capture", async () => {
    const rn = require("react-native");
    const dimensions = jest.spyOn(rn, "useWindowDimensions");
    dimensions.mockReturnValue({ width: 390, height: 844, scale: 3, fontScale: 1 });
    const view = render(<VisionTestPage />);
    await waitFor(() => expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy());
    const checkRatio = (width: number, height: number, ratio: number) => {
      fireEvent(view.getByTestId("recording-viewfinder-space"), "layout", {
        nativeEvent: { layout: { x: 0, y: 0, width, height } },
      });
      const frame = StyleSheet.flatten(view.getByTestId("recording-viewfinder").props.style);
      expect(frame.width / frame.height).toBeCloseTo(ratio);
      expect(frame.height).toBeLessThanOrEqual(height);
      expect(view.getByLabelText("overlay.recording.stop")).toBeTruthy();
    };
    checkRatio(358, 560, 9 / 16);
    dimensions.mockReturnValue({ width: 844, height: 390, scale: 3, fontScale: 1 });
    view.rerender(<VisionTestPage />);
    checkRatio(590, 307, 16 / 9);
    dimensions.mockReturnValue({ width: 390, height: 844, scale: 3, fontScale: 1 });
    view.rerender(<VisionTestPage />);
    checkRatio(358, 560, 9 / 16);
    expect(mockStartCamera).toHaveBeenCalledTimes(1);
    expect(mockOrientationLock).toHaveBeenCalledTimes(1);
    expect(mockCameraMount).toHaveBeenCalledTimes(1);
    expect(mockCameraUnmount).not.toHaveBeenCalled();
    view.unmount();
  });


  it.each(["true", "false"])("uses a large landscape viewfinder and a separate telemetry column (autoRecord=%s)", async (autoRecord) => {
    const rn = require("react-native");
    jest.spyOn(rn, "useWindowDimensions").mockReturnValue({ width: 844, height: 390, scale: 3, fontScale: 1 });
    mockRouteParams = { autoRecord };
    const view = render(<VisionTestPage />);
    const control = autoRecord === "true" ? "overlay.recording.stop" : "Start Recording";
    await waitFor(() => expect(view.getByLabelText(control)).toBeTruthy());
    expect(StyleSheet.flatten(view.getByTestId("recording-chrome").props.style).flexDirection).toBe("row");
    const body = view.getByTestId("recording-body");
    expect(StyleSheet.flatten(body.props.style).flexDirection).toBe("row");
    const cameraArea = view.getByTestId("recording-viewfinder-space");
    const sidebar = view.getByTestId("recording-sidebar");
    const parentTestID = (node: typeof cameraArea) => {
      const ownID = node.props.testID;
      let parent = node.parent;
      while (parent && (!parent.props.testID || parent.props.testID === ownID)) parent = parent.parent;
      return parent?.props.testID;
    };
    expect(parentTestID(cameraArea)).toBe("recording-body");
    expect(parentTestID(sidebar)).toBe("recording-body");
    expect(within(sidebar).getByLabelText(control)).toBeTruthy();

    const measureCamera = (width: number) => {
      fireEvent(cameraArea, "layout", { nativeEvent: { layout: { x: 0, y: 0, width, height: 307 } } });
      return StyleSheet.flatten(view.getByTestId("recording-viewfinder").props.style);
    };
    const collapsed = measureCamera(590);
    expect(collapsed.height).toBeCloseTo(307);
    expect(collapsed.width).toBeGreaterThan(540);

    fireEvent.press(view.getByLabelText("overlay.recording.expandTelemetry"));
    expect(within(sidebar).getByTestId("recording-telemetry")).toBeTruthy();
    // A narrower camera column may reduce width, but telemetry never takes its height.
    const expanded = measureCamera(432);
    expect(expanded.width).toBeCloseTo(432);
    expect(expanded.height).toBeGreaterThan(240);
    expect(within(sidebar).getByLabelText(control)).toBeTruthy();
    fireEvent.press(view.getByLabelText("overlay.recording.collapseTelemetry"));
    expect(measureCamera(590).height).toBeCloseTo(collapsed.height);
    expect(mockCameraMount).toHaveBeenCalledTimes(1);
    expect(mockCameraUnmount).not.toHaveBeenCalled();
    expect(mockStartCamera).toHaveBeenCalledTimes(autoRecord === "true" ? 1 : 0);
    view.unmount();
  });

});
