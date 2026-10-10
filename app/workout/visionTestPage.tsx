import { appleEnvironment, environmentNativeAvailable } from "../../modules/apple-on-device-ai";
import { weatherEnabled } from "../../features/environment/recorder";
import { flushEnvironmentUploads } from "../../features/environment/store";
import { useEnvironmentRecorder } from "../../features/environment/useEnvironmentRecorder";
import { EnvironmentLiveCard } from "../../features/environment/EnvironmentCard";
import { useLiveWorkoutFeedback } from "../../features/wod/useLiveWorkoutFeedback";
import { ActivitySummaryContent } from "../../features/wod/ui/ActivitySummaryCard";
import { useIsFocused } from "expo-router/react-navigation";
import { activateKeepAwakeAsync, deactivateKeepAwake } from "expo-keep-awake";
import * as MediaLibrary from "expo-media-library/legacy";
import { router, useFocusEffect, useLocalSearchParams } from "expo-router";
import * as ScreenOrientation from "expo-screen-orientation";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Animated,
  AppState,
  AppStateStatus,
  Linking,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  useWindowDimensions,
  View,
} from "react-native";
import { Video } from "react-native-compressor";
import { SafeAreaView } from "react-native-safe-area-context";
import {
  Camera,
  useCameraDevice,
  useCameraFormat,
  useCameraPermission,
  useMicrophonePermission,
  type VideoSegment,
  type VideoFile,
} from "react-native-vision-camera";

import { IconSymbol } from "@/components/ui/icon-symbol";
import { PolarSensorRecorder } from "@/features/health/polar/polarSensorRecorder";
import {
  H10MemoryRecorderInstance,
  type H10MemoryStatus,
} from "@/features/health/polar/h10MemoryRecorder";
import {
  enqueueSensorUpload,
  flushSensorUploads,
} from "@/features/health/polar/sensorTelemetryUpload";
import { useBleHeartRate } from "@/features/health/useBleHeartRate";
import {
  buildWorkoutSessionId,
  formatWorkoutTypeLabel,
  parseWorkoutType,
} from "@/features/wod/workoutType";
import { usePoseDetection } from "../../features/ai-coach/frame-processors/usePoseDetection";
import { EnergyMonitor } from "../../features/ai-coach/ui/EnergyMonitor";
import { SkeletonOverlay } from "../../features/ai-coach/ui/SkeletonOverlay";
import { AppleAiFeedbackCard } from "../../features/ai-coach/ui/AppleAiFeedbackCard";
import { useAppleAiFeedback } from "../../features/ai-coach/useAppleAiFeedback";
import { saveAppleAiObservation, flushAppleAiUploads } from "../../features/ai-coach/appleAiUpload";
import type { AppleAiObservation } from "../../features/ai-coach/appleAiObservation";
import { TelemetryRecorder } from "../../features/debug/telemetryRecorder";
import {
  enqueueUpload as enqueueDebugUpload,
  flushPendingUploads,
} from "../../features/debug/telemetryUpload";
import {
  mergeChunks,
  processWorkoutChunk,
  is4xxError,
  getErrorStatusCode,
} from "../../features/wod/api";
import {
  prepareOriginalSession, beginOriginalChunk, addOriginalChunk,
  markOriginalRecordingStopped, finalizeAndSaveOriginal, holdOriginalFiles,
  type OriginalSessionRef,
} from "../../features/video/originalVideoStore";
import { CaptureWindow } from "../../features/wod/captureWindow";
import { createChunkRecordingCompletion } from "../../features/wod/chunkRecordingCompletion";
import { recordingErrorMessage } from "../../features/wod/recordingErrorMessage";

import { useAuthStore } from "@/features/auth/useAuthStore";
import { t, useLocale } from "@/features/i18n";
import { useMergeStatus } from "@/store/useMergeStatus";
import { useProfileStore } from "@/store/useProfileStore";

const CHUNK_DURATION_MS = 10000; // 10 seconds
/** Upper bound on waiting for chunk uploads before the server merge fires. */
const MERGE_UPLOAD_WAIT_MS = 90000;
type OriginalCapture = {
  ref: OriginalSessionRef;
  startedAt: number;
  order: number;
  complete: boolean;
  failure?: string;
  failedUploads: number;
  drains: Promise<void>[];
  consumersFinished?: Promise<void>;
  release: () => void;
};
const IS_ANDROID = Platform.OS === "android";

function localPath(uri: string): string {
  return decodeURIComponent(uri.replace(/^file:\/\//, ""));
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatElapsed(ms: number): string {
  const totalSecs = Math.floor(ms / 1000);
  const mins = Math.floor(totalSecs / 60);
  const secs = totalSecs % 60;
  return `${mins.toString().padStart(2, "0")}:${secs.toString().padStart(2, "0")}`;
}

export default function VisionTestPage() {
  const locale = useLocale();
  const {
    resolution = "720p",
    movements = "",
    injuries = "",
    workoutType: workoutTypeParam,
    autoRecord,
    onDeviceAi: onDeviceAiParam,
    environmentObservation: environmentObservationParam,
    environmentAnalysis: environmentAnalysisParam,
    observationIntervalSeconds,
    showSkeleton: showSkeletonParam,
    lowFps: lowFpsParam,

    skipCompression: skipCompressionParam,
    serialUpload: serialUploadParam,
    continuousRecording: continuousRecordingParam,
    h10MemoryRecording: h10MemoryRecordingParam,
    landscapeMode: landscapeModeParam,
    previewOnly: previewOnlyParam,
    zoomMode: zoomModeParam,
    aspectRatio: aspectRatioParam,
    wodDescription: wodDescriptionParam,
    appearanceHints: appearanceHintsParam,
  } = useLocalSearchParams<{
    resolution?: string;
    movements?: string;
    injuries?: string;
    workoutType?: string;
    autoRecord?: string;
    onDeviceAi?: string;
    environmentObservation?: string;
    environmentAnalysis?: string;
    observationIntervalSeconds?: string;
    showSkeleton?: string;
    lowFps?: string;
    skipCompression?: string;
    serialUpload?: string;
    continuousRecording?: string;
    h10MemoryRecording?: string;
    landscapeMode?: string;
    previewOnly?: string;
    zoomMode?: string;
    aspectRatio?: string;
    wodDescription?: string;
    appearanceHints?: string;
  }>();

  const landscapeMode = landscapeModeParam === "true";
  const previewOnly = previewOnlyParam === "true";
  const onDeviceAi = onDeviceAiParam === "true";
  const environmentEnabled = environmentObservationParam === "true" && !previewOnly;
  const environment = useEnvironmentRecorder(environmentEnabled);
  const zoomMode = zoomModeParam === "true";
  const aspectRatio = (aspectRatioParam === "4:3" ? "4:3" : "16:9") as
    | "4:3"
    | "16:9";
  const wodDescription = wodDescriptionParam || "";
  const appearanceHints = appearanceHintsParam || undefined;

  // Performance flags — default to power-saving on Android, full quality on iOS
  const showSkeleton =
    showSkeletonParam !== undefined
      ? showSkeletonParam === "true"
      : !IS_ANDROID;
  const lowFps =
    lowFpsParam !== undefined ? lowFpsParam === "true" : IS_ANDROID;
  const skipCompression =
    skipCompressionParam !== undefined
      ? skipCompressionParam === "true"
      : IS_ANDROID;
  const serialUpload =
    serialUploadParam !== undefined ? serialUploadParam === "true" : IS_ANDROID;

  // Experimental native capability. Remains opt-in until release/device acceptance.
  const continuousRecording = Platform.OS === "ios" && continuousRecordingParam === "true";
  // Experimental H10 internal HR recording beside the live pipeline (iOS first, opt-in).
  const h10MemoryEnabled = Platform.OS === "ios" && h10MemoryRecordingParam === "true" && !previewOnly;

  const workoutType = parseWorkoutType(workoutTypeParam);
  const workoutTypeLabel = formatWorkoutTypeLabel(workoutType).toUpperCase();

  // Resolution: honor selected resolution and aspect ratio
  const is43 = aspectRatio === "4:3";
  const resMap: Record<string, { w16: number; w43: number; h: number }> = {
    "480p": { w16: 854, w43: 640, h: 480 },
    "720p": { w16: 1280, w43: 960, h: 720 },
    "1080p": { w16: 1920, w43: 1440, h: 1080 },
    "2160p": { w16: 3840, w43: 2880, h: 2160 },
  };
  const res = resMap[resolution] || resMap["720p"];
  const targetWidth = is43 ? res.w43 : res.w16;
  const targetHeight = res.h;

  // FPS: honor lowFps toggle
  const targetFps = lowFps ? 24 : 30;

  const isFocused = useIsFocused();
  const [appState, setAppState] = useState<AppStateStatus>(
    AppState.currentState,
  );
  const isCameraActive = isFocused && appState === "active";
  const [cameraPosition, setCameraPosition] = useState<"back" | "front">("back");
  const device = useCameraDevice(cameraPosition);
  const handleFlipCamera = useCallback(() => {
    setCameraPosition((pos) => (pos === "back" ? "front" : "back"));
  }, []);

  const { hasPermission, requestPermission } = useCameraPermission();
  const {
    hasPermission: hasMicPermission,
    requestPermission: requestMicPermission,
  } = useMicrophonePermission();
  const { width, height } = useWindowDimensions();
  const isLandscapeLayout = width > height;
  // On Android, landscape mode keeps portrait but user mounts phone sideways.
  // Apply landscape styles based on the toggle, not screen dimensions.
  const applyLandscapeStyles =
    isLandscapeLayout || (IS_ANDROID && landscapeMode);

  // Telemetry visibility: collapsed by default, expanded via user toggle
  const [isTelemetryExpanded, setIsTelemetryExpanded] = useState(false);

  // Exact recording aspect ratio based on camera orientation and format:
  // Portrait (default): 9:16 (0.5625) or 3:4 (0.75 if is43)
  // Landscape: 16:9 (1.7778) or 4:3 (1.3333 if is43)
  const recordingAspectRatio = applyLandscapeStyles
    ? (is43 ? 4 / 3 : 16 / 9)
    : (is43 ? 3 / 4 : 9 / 16);

  // Flex layout reserves the measured header, controls and safe areas first.
  // Only the remaining camera area participates in aspect-ratio sizing.
  const [viewfinderSpace, setViewfinderSpace] = useState({ width: 0, height: 0 });
  // Landscape uses a side panel, so telemetry must not halve the camera height.
  const maxViewfinderHeight = isLandscapeLayout
    ? viewfinderSpace.height
    : (isTelemetryExpanded ? 210 : 560);
  const viewfinderWidth = Math.max(0, Math.min(
    viewfinderSpace.width,
    isLandscapeLayout ? viewfinderSpace.width : 440,
    Math.min(viewfinderSpace.height, maxViewfinderHeight) * recordingAspectRatio,
  ));
  const viewfinderHeight = viewfinderWidth / recordingAspectRatio;

  const camera = useRef<Camera>(null);

  // Use a ref to track if we should continue recording chunks,
  // preventing stale state in closures/timeouts.
  const isRecordingChunks = useRef(false);
  // Chunks whose compress+upload work has not settled yet, and how many
  // actually entered the upload path this session.
  const outstandingUploads = useRef(0);
  const uploadedChunkCount = useRef(0);
  const isStartingRecording = useRef(false);
  const chunkTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isChunkRecordingActive = useRef(false);

  // Camera originals and consumer ownership are independent from server analysis.
  const originalCapture = useRef<OriginalCapture | null>(null);
  const startingChunk = useRef<Promise<void> | null>(null);
  const preparationQueue = useRef<Promise<void>>(Promise.resolve());
  const stoppingWorkout = useRef(false);
  const pauseTransition = useRef<Promise<unknown> | null>(null);
  const unmountRecorder = useRef<() => void>(() => {});
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      if (bannerTimer.current) clearTimeout(bannerTimer.current);
      mounted.current = false;
      unmountRecorder.current();
    };
  }, []);
  const heartRateWindow = useRef(new CaptureWindow());
  // A timer-stopped chunk remains pending until the native finish callback.
  const pendingChunk = useRef<ReturnType<typeof createChunkRecordingCompletion> | null>(null);

  // Session ID computed once at recording start, reused for all chunks + merge
  const sessionIdRef = useRef<string>("");
  const capturedProfileIdRef = useRef<number | null>(null);

  // Track recording session start time for chunk timing
  const recordingStartTime = useRef<number>(0);

  // Polar sensor live collection stats (polled at 1Hz during recording)
  const [sensorLiveStatus, setSensorLiveStatus] = useState<{
    accSamples: number;
    dropped: number | null;
  }>({ accSamples: 0, dropped: null });

  // --- Upload monitoring ---
  const [pendingUploads, setPendingUploads] = useState(0);
  const [inflightUploads, setInflightUploads] = useState(0);

  // --- Upload banner & error notification ---
  type UploadBannerState = {
    type: "retry" | "error";
    message: string;
  } | null;

  const [uploadBanner, setUploadBanner] = useState<UploadBannerState>(null);
  const bannerTimer = useRef<NodeJS.Timeout | null>(null);

  const showUploadBanner = useCallback(
    (banner: UploadBannerState, autoDismissMs?: number) => {
      if (bannerTimer.current) {
        clearTimeout(bannerTimer.current);
        bannerTimer.current = null;
      }
      setUploadBanner(banner);
      if (banner && autoDismissMs && autoDismissMs > 0) {
        bannerTimer.current = setTimeout(() => {
          setUploadBanner((current) => (current === banner ? null : current));
        }, autoDismissMs);
      }
    },
    [],
  );

  const clearRetryBanner = useCallback(() => {
    setUploadBanner((prev) => (prev?.type === "retry" ? null : prev));
  }, []);

  // --- Serial Upload Queue ---
  // Prevents concurrent uploads from piling up in memory on slow connections.
  // Each chunk upload is queued and processed one at a time.
  const uploadQueue = useRef<Array<() => Promise<void>>>([]);
  const isUploading = useRef(false);

  const drainUploadQueue = async () => {
    if (isUploading.current) return; // already draining
    isUploading.current = true;
    while (uploadQueue.current.length > 0) {
      const task = uploadQueue.current.shift()!;
      setPendingUploads(uploadQueue.current.length);
      setInflightUploads((prev) => prev + 1);
      try {
        await task();
      } catch (err) {
        console.error("Upload queue task failed:", err);
      }
      setInflightUploads((prev) => Math.max(0, prev - 1));
    }
    isUploading.current = false;
  };

  const enqueueUpload = (task: () => Promise<void>) => {
    uploadQueue.current.push(task);
    setPendingUploads(uploadQueue.current.length);
    drainUploadQueue();
  };

  /**
   * Waits until every chunk's compress+upload work has settled, capped so a
   * stuck upload cannot postpone the merge forever.
   */
  const waitForOutstandingUploads = async (timeoutMs: number) => {
    const deadline = Date.now() + timeoutMs;
    while (outstandingUploads.current > 0 && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    if (outstandingUploads.current > 0) {
      console.warn(
        `⚠️ Waiting ended with ${outstandingUploads.current} chunk upload(s) still in flight after ${timeoutMs}ms`,
      );
    }
  };

  // Track fire-and-forget (concurrent) uploads
  const trackUpload = (task: () => Promise<void>) => {
    setInflightUploads((prev) => prev + 1);
    task().finally(() => setInflightUploads((prev) => Math.max(0, prev - 1)));
  };

  // 720p, 1080p, or 2160p (4K) format based on user selection; keep targetFps at 30 (or 24 for lowFps)
  const format = useCameraFormat(device, [
    { videoResolution: { width: targetWidth, height: targetHeight } },
    { fps: targetFps },
  ]);

  const videoStabilizationMode = useMemo(() => {
    if (!format) return undefined;
    if (format.videoStabilizationModes.includes("cinematic-extended")) {
      return "cinematic-extended";
    }
    if (format.videoStabilizationModes.includes("cinematic")) {
      return "cinematic";
    }
    if (format.videoStabilizationModes.includes("auto")) {
      return "auto";
    }
    return undefined;
  }, [format]);


  const [isRecording, setIsRecording] = useState(false);
  const [isPaused, setIsPaused] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isMerging, setIsMerging] = useState(false);
  const [mergeChunkTotal, setMergeChunkTotal] = useState(0);
  const mergeShimmerAnim = useRef(new Animated.Value(0)).current;
  const [isCameraReady, setIsCameraReady] = useState(false);

  const captureAppleAiFrame = useCallback(async () => {
    if (!camera.current) throw new Error("Camera unavailable");
    return camera.current.takeSnapshot({ quality: 80 });
  }, []);
  // Capture identity by value: a cancelled old request may finish during a new recording.
  const archiveProfileId = capturedProfileIdRef.current;
  const archiveStartedAt = recordingStartTime.current;
  const archiveAppleAiObservation = useCallback(async (observation: AppleAiObservation) => {
    if (archiveProfileId === null) throw new Error("Missing recording profile");
    await saveAppleAiObservation(observation, archiveProfileId, archiveStartedAt);
  }, [archiveProfileId, archiveStartedAt]);
  const appleAi = useAppleAiFeedback({
    enabled: onDeviceAi && !previewOnly,
    running: isRecording && !isPaused && !isSaving && isCameraActive && isCameraReady,
    foreground: isCameraActive,
    sessionId: sessionIdRef.current,
    wodDescription,
    movements,
    appearanceHints,
    language: locale,
    capture: captureAppleAiFrame,
    onObservation: archiveAppleAiObservation,
  });
  const stopAppleAi = appleAi.stop;
  useEffect(() => () => {
    void stopAppleAi().then(flushAppleAiUploads).catch(() => {});
  }, [stopAppleAi]);
  const [chunkCount, setChunkCount] = useState(0);

  // Elapsed timer for recording with Pause/Resume support
  const [elapsedMs, setElapsedMs] = useState(0);
  const accumulatedMs = useRef(0);
  const segmentStartTime = useRef(0);

  // AppState change listener — auto-pause recording on background / app switch
  const isRecordingRef = useRef(isRecording);
  const isPausedRef = useRef(isPaused);
  useEffect(() => {
    isRecordingRef.current = isRecording;
    isPausedRef.current = isPaused;
  }, [isRecording, isPaused]);

  useEffect(() => {
    const subscription = AppState.addEventListener("change", (nextAppState) => {
      console.log(`📱 AppState changed: ${nextAppState}`);
      if (nextAppState === "background" || nextAppState === "inactive") {
        if (isRecordingRef.current && !isPausedRef.current) {
          console.log("📱 App backgrounded while recording — auto-pausing");
          handlePauseRecording();
        }
      }
      setAppState(nextAppState);
    });
    return () => {
      subscription.remove();
      useAuthStore.getState().setRecordingActive(false);
      void flushEnvironmentUploads().catch(() => {});
    };
  }, []);

  const profileId = useProfileStore((s) => s.activeProfileId);
  const liveFeedback = useLiveWorkoutFeedback(isRecording, profileId ?? 0, sessionIdRef, recordingStartTime);
  const chunkFeedback = liveFeedback.coaching;
  const [isFeedbackCollapsed, setIsFeedbackCollapsed] = useState(false);
  const prevChunkFeedbackRef = useRef<string | null>(null);

  useEffect(() => {
    if (chunkFeedback && chunkFeedback !== prevChunkFeedbackRef.current) {
      setIsFeedbackCollapsed(false);
      prevChunkFeedbackRef.current = chunkFeedback;
    }
  }, [chunkFeedback]);


  const observedMovements = liveFeedback.summary?.available
    ? liveFeedback.summary.movements
    : [];

  // Keep screen awake while recording (prevents Android/iOS sleep)
  useEffect(() => {
    if (isRecording) {
      void activateKeepAwakeAsync("recording");
    } else {
      deactivateKeepAwake("recording");
    }
    return () => {
      deactivateKeepAwake("recording");
    };
  }, [isRecording]);

  // Elapsed timer — ticks every second during recording (only when active and not paused)
  useEffect(() => {
    if (!isRecording || isPaused) {
      return;
    }
    const tick = setInterval(() => {
      const currentSegment =
        segmentStartTime.current > 0
          ? Date.now() - segmentStartTime.current
          : 0;
      setElapsedMs(accumulatedMs.current + currentSegment);
    }, 1000);
    return () => clearInterval(tick);
  }, [isRecording, isPaused]);

  // Shimmer animation for merging progress bar
  useEffect(() => {
    if (isMerging) {
      mergeShimmerAnim.setValue(0);
      const loop = Animated.loop(
        Animated.timing(mergeShimmerAnim, {
          toValue: 1,
          duration: 1500,
          useNativeDriver: false,
        }),
      );
      loop.start();
      return () => loop.stop();
    }
  }, [isMerging]);

  // Override the app's portrait lock for the lifetime of the recording screen.
  // iOS: follow device rotation unless landscape was explicitly selected.
  // Android: keep portrait — CameraX breaks when Activity rotates via configChanges.
  //   Instead, the user mounts their phone sideways. The camera sensor is physically
  //   landscape, so content is captured wide. UI shows a mounting hint.
  useFocusEffect(
    useCallback(() => {
      if (!IS_ANDROID) {
        void ScreenOrientation.lockAsync(
          landscapeMode
            ? ScreenOrientation.OrientationLock.LANDSCAPE
            : ScreenOrientation.OrientationLock.DEFAULT,
        ).catch((error) => console.warn("Recording screen orientation failed", error));
      }
      return () => {
        if (!IS_ANDROID) {
          void ScreenOrientation.lockAsync(
            ScreenOrientation.OrientationLock.PORTRAIT_UP,
          ).catch((error) => console.warn("Restoring portrait orientation failed", error));
        }
      };
    }, [landscapeMode]),
  );

  // Pass isRecording to the hook — inference always runs, but frame counting only during recording
  const {
    frameProcessor,
    poseResult,
    monitorData,
    isModelLoaded,
    getWorkoutConfidence,
    getLatestMotion,
    getCaptureConfidence,
    resetCaptureObservations,
  } = usePoseDetection(isRecording);

  const safeConfidence = Number.isFinite(monitorData?.confidence)
    ? Math.max(0, Math.min(1, monitorData.confidence))
    : 0;
  const safeMotion = Number.isFinite(monitorData?.motion)
    ? Math.max(0, monitorData.motion)
    : 0;
  const bpmRef = useRef(0);
  const {
    bpm,
    quality: hrQuality,
    getReading,
    resetQuality,
    status: hrStatus,
    batteryLevel,
  } = useBleHeartRate({
    sink: PolarSensorRecorder,
    recording: isRecording,
    paused: isPaused,
    onReading: (reading) => {
      bpmRef.current = reading.bpm ?? 0;
      if (
        reading.bpm !== null &&
        isRecording &&
        !isPaused &&
        isChunkRecordingActive.current
      ) {
        heartRateWindow.current.add(reading.receivedAt, reading.bpm);
      }
    },
  });
  // const { bpm, status: hrStatus } = useHeartRate();

  // Poll Polar sensor live status at 1Hz during recording (avoids 50Hz re-renders)
  useEffect(() => {
    if (!isRecording) return;
    const interval = setInterval(() => {
      const stats = PolarSensorRecorder.getLiveStatus();
      setSensorLiveStatus({
        accSamples: stats.accSamples,
        dropped: stats.dropped,
      });
    }, 1000);
    return () => clearInterval(interval);
  }, [isRecording]);

  // --- Experimental H10 internal recording: status + live-pipeline watchdog ---
  const [h10MemoryStatus, setH10MemoryStatus] = useState<H10MemoryStatus | null>(null);
  const [h10MemoryStall, setH10MemoryStall] = useState<"hr" | "acc" | null>(null);
  const hrStatusRef = useRef(hrStatus);
  useEffect(() => {
    hrStatusRef.current = hrStatus;
  }, [hrStatus]);
  useEffect(() => {
    if (!h10MemoryEnabled) return;
    // A previous run may still be finishing in the background; show only this session's status.
    return H10MemoryRecorderInstance.subscribe((status) => {
      if (status.sessionId === sessionIdRef.current) setH10MemoryStatus(status);
    });
  }, [h10MemoryEnabled]);
  useEffect(() => {
    if (!h10MemoryEnabled || !isRecording || isPaused) return;
    let lastHr = -1;
    let lastAcc = -1;
    let accFlowing = false;
    let hrChangedAt = Date.now();
    let accChangedAt = Date.now();
    let shownAt = 0;
    const interval = setInterval(() => {
      const now = Date.now();
      const stats = PolarSensorRecorder.getLiveStatus();
      if (stats.hrSamples !== lastHr) {
        lastHr = stats.hrSamples;
        hrChangedAt = now;
      }
      if (stats.accSamples !== lastAcc) {
        if (lastAcc >= 0 && stats.accSamples > lastAcc) accFlowing = true;
        lastAcc = stats.accSamples;
        accChangedAt = now;
      }
      // Leaving BLE range changes hrStatus away from "Live"; that is expected, not a malfunction.
      let stall: "hr" | "acc" | null = null;
      if (hrStatusRef.current === "Live" && now - hrChangedAt > 15_000) stall = "hr";
      else if (accFlowing && now - accChangedAt > 5_000 && now - hrChangedAt < 3_000) stall = "acc";
      if (stall) {
        shownAt = now;
        setH10MemoryStall(stall);
      } else if (now - shownAt >= 10_000) {
        setH10MemoryStall(null);
      }
    }, 1000);
    return () => {
      clearInterval(interval);
      setH10MemoryStall(null);
    };
  }, [h10MemoryEnabled, isRecording, isPaused]);
  useEffect(() => () => {
    // No-op unless a run for this session is still active (e.g. the screen closed mid-workout).
    void H10MemoryRecorderInstance.finish(sessionIdRef.current);
  }, []);
  const h10MemoryStatusForSession = h10MemoryStatus;
  const h10MemoryBanner: { type: "error" | "warning"; message: string } | null = !h10MemoryEnabled
    ? null
    : h10MemoryStatusForSession?.phase === "start_failed" && h10MemoryStatusForSession.error
      ? { type: "error", message: t("overlay.recording.h10MemoryStartFailed", { code: h10MemoryStatusForSession.error.code }) }
      : h10MemoryStatusForSession?.phase === "failed" && h10MemoryStatusForSession.error
        ? {
            type: "error",
            message: t("overlay.recording.h10MemoryFailed", {
              stage: h10MemoryStatusForSession.error.stage,
              code: h10MemoryStatusForSession.error.code,
            }),
          }
        : h10MemoryStall === "hr"
          ? { type: "warning", message: t("overlay.recording.h10MemoryHrStalled") }
          : h10MemoryStall === "acc"
            ? { type: "warning", message: t("overlay.recording.h10MemoryAccStalled") }
            : null;

  // Refs that mirror render-state for sampling outside the render cycle.
  // TelemetryRecorder polls these at 1Hz via registered providers.
  const chunkCountRef = useRef(0);
  useEffect(() => {
    chunkCountRef.current = chunkCount;
  }, [chunkCount]);

  useEffect(() => {
    if (!hasPermission) requestPermission();
    if (!hasMicPermission) requestMicPermission();
  }, [
    hasPermission,
    hasMicPermission,
    requestMicPermission,
    requestPermission,
  ]);

  // Auto-start recording when navigated from setup with autoRecord
  const hasAutoStarted = useRef(false);
  useEffect(() => {
    if (
      autoRecord === "true" &&
      !hasAutoStarted.current &&
      hasPermission &&
      device &&
      isCameraReady &&
      camera.current &&
      !isRecording
    ) {
      hasAutoStarted.current = true;
      handleStartRecording();
    }
  }, [autoRecord, hasPermission, device, isCameraReady, isRecording]);

  // --- Chunk Recording Logic (Raw Camera) ---

  const finishOriginalConsumers = (capture: OriginalCapture) => {
    if (!capture.consumersFinished) {
      capture.consumersFinished = Promise.all(capture.drains).then(() => environment.stop())
        .catch(console.warn).finally(capture.release);
    }
    return capture.consumersFinished;
  };

  const queueAnalysis = (capture: OriginalCapture, path: string, start: number, end: number, nativeClockOffsetMs?: number) => {
    const startSecs = Math.max(0, (start - capture.startedAt) / 1000);
    const endSecs = Math.max(startSecs, (end - capture.startedAt) / 1000);
    const heartRateBpm = heartRateWindow.current.peak(start, end);
    const workoutConfidence = getCaptureConfidence(start, end, nativeClockOffsetMs);
    const release = holdOriginalFiles(capture.ref);
    uploadedChunkCount.current += 1;
    outstandingUploads.current += 1;
    setChunkCount((count) => count + 1);
    try {
      environment.event("existing_sensors", { hr: getReading().bpm ?? null, hrSource: "BLE",
        moveNet: getLatestMotion(), sensorFiles: "existing_session_sensor_telemetry", source: "existing_enabled_providers" });
      environment.offerChunk({ path, captureStart: start, captureEnd: end,
        durationMs: end - start, index: uploadedChunkCount.current });
    } catch (error) { console.warn("Environment observation unavailable; original capture continues", error); }

    // Serialize compression as well as native preparation; upload scheduling stays configurable.
    preparationQueue.current = preparationQueue.current.then(async () => {
      let uri = path;
      try {
        if (!skipCompression) uri = await Video.compress(path, { compressionMethod: "auto", maxSize: 720 });
      } catch (error) {
        capture.failedUploads += 1;
        outstandingUploads.current -= 1;
        release();
        console.warn("Analysis compression failed; original retained", error);
        return;
      }
      const upload = async () => {
        try {
          await processWorkoutChunk(uri, capture.ref.sessionId, {
            movements: movements ? movements.split(", ") : [],
            injuries: injuries ? injuries.split(", ") : [],
            workoutType, profileId: capture.ref.profileId, startSecs, endSecs,
            heartRateBpm, workoutConfidence, appearanceHints,
            maxRetries: 5,
            retryDelayMs: 5000,
            onRetry: (attempt, maxRetries) => {
              showUploadBanner({
                type: "retry",
                message: t("overlay.recording.chunkRetrying", {
                  attempt,
                  maxRetries,
                }),
              });
            },
          });
          clearRetryBanner();
        } catch (error) {
          capture.failedUploads += 1;
          const is4xx = is4xxError(error);
          const statusCode = getErrorStatusCode(error);
          console.warn("Analysis upload failed; gallery archive is independent", error);
          if (is4xx) {
            showUploadBanner(
              {
                type: "error",
                message: t("overlay.recording.chunk4xxError", {
                  code: statusCode ? `HTTP ${statusCode}` : "4xx",
                }),
              },
              8000,
            );
          } else {
            showUploadBanner(
              {
                type: "error",
                message: t("overlay.recording.chunkUploadFailed", {
                  maxRetries: 5,
                }),
              },
              8000,
            );
          }
        } finally {
          // Compressor may return its input. Never delete the original/analysis input here.
          try {
            if (localPath(uri) !== localPath(path)) {
              const { File } = require("expo-file-system");
              const file = new File(uri);
              if (file.exists) file.delete();
            }
          } catch (_) { /* A leftover disposable copy is safe. */ }
          outstandingUploads.current = Math.max(0, outstandingUploads.current - 1);
          release();
        }
      };
      if (serialUpload) enqueueUpload(upload);
      else trackUpload(upload);
    }).catch((error) => {
      capture.failedUploads += 1;
      outstandingUploads.current = Math.max(0, outstandingUploads.current - 1);
      release();
      console.warn("Analysis preparation failed; original retained", error);
    });
  };

  async function startChunkLoop() {
    const capture = originalCapture.current;
    const recordingCamera = camera.current;
    if (!recordingCamera || !capture || !isRecordingChunks.current) return;
    const order = ++capture.order;
    let finishPreparation!: () => void;
    capture.drains.push(new Promise<void>((resolve) => { finishPreparation = resolve; }));
    const completion = createChunkRecordingCompletion(
      () => recordingCamera.stopRecording(), continuousRecording ? null : 5000,
    );
    pendingChunk.current = completion;
    const finishChunk = (path: string | null) => {
      completion.finish(path);
      if (pendingChunk.current === completion) pendingChunk.current = null;
    };
    let captureFailed = false;
    const failCapture = (error: unknown) => {
      if (captureFailed) return;
      captureFailed = true;
      capture.complete = false;
      capture.failure = recordingErrorMessage(error);
      isRecordingChunks.current = false;
      isChunkRecordingActive.current = false;
      finishChunk(null);
      finishPreparation();
      // Durable failure evidence also survives navigation or process termination.
      void markOriginalRecordingStopped(capture.ref, { complete: false, reason: capture.failure }).catch(console.warn);
      Alert.alert(t("originalVideo.captureFailedTitle"), `${t("originalVideo.captureFailedBody")}\n\n${capture.failure}`);
      void handleStopRecording();
    };
    try {
      const directory = await beginOriginalChunk(capture.ref, order);
      if (!mounted.current) {
        capture.complete = false;
        capture.failure = "recording_screen_unmounted_before_capture";
        finishChunk(null);
        finishPreparation();
        return;
      }
      const startedAt = Date.now();
      isChunkRecordingActive.current = true;
      let sourceRegistration: Promise<void> | undefined;
      let sourceAccepted = false;
      const registerSource = (video: VideoFile): Promise<void> => {
        if (sourceRegistration) return sourceRegistration;
        sourceRegistration = (async () => {
          if (pendingChunk.current === completion) isChunkRecordingActive.current = false;
          try {
            await addOriginalChunk(capture.ref, { order, path: video.path, durationSecs: video.duration });
            sourceAccepted = true;
            if (video.segmented) {
              if (video.droppedVideoFrames || video.droppedAudioBuffers || video.tailDrainTimedOut) {
                capture.complete = false;
                capture.failure = `capture dropped buffers: video=${video.droppedVideoFrames}, audio=${video.droppedAudioBuffers}, tailTimeout=${video.tailDrainTimedOut}`;
              }
            } else {
              try { queueAnalysis(capture, video.path, startedAt, startedAt + video.duration * 1000); }
              catch (error) { capture.failedUploads += 1; console.warn("Analysis unavailable; source retained", error); }
            }
            finishChunk(video.path);
            if (!continuousRecording && isRecordingChunks.current && originalCapture.current === capture) {
              const next = () => { startingChunk.current = startChunkLoop(); };
              if (IS_ANDROID) setTimeout(next, 500);
              else next();
            }
          } catch (error) { failCapture(error); }
        })();
        return sourceRegistration;
      };
      recordingCamera.startRecording({
        path: localPath(directory),
        ...(IS_ANDROID ? { fileType: "mp4" as const, videoCodec: "h265" as const } : {}),
        ...(continuousRecording ? {
          segmented: true,
          onRecordingSourceFinalized: (video: VideoFile) => { void registerSource(video); },
          onRecordingSegment: (segment: VideoSegment) => {
            if (segment.status === "ready" && segment.path) {
              try { queueAnalysis(capture, segment.path, segment.captureStartTimeMs, segment.captureEndTimeMs, segment.captureClockOffsetMs); }
              catch (error) { capture.failedUploads += 1; console.warn("Analysis unavailable; source retained", error); }
            } else {
              capture.failedUploads += 1;
              console.warn("Analysis fragment preparation failed; original writer continues", segment.error);
            }
          },
        } : {}),
        onRecordingFinished: async (video) => {
          // The original may already be safely archived while analysis preparation drains.
          await registerSource(video);
          finishPreparation();
        },
        onRecordingError: (error) => {
          // A previous run can finish derivative work after resume. Its late
          // preparation error must not stop or clear the current original writer.
          void (async () => {
            await sourceRegistration;
            if (sourceAccepted) {
              capture.failedUploads += 1;
              finishPreparation();
              console.warn("Post-capture preparation failed; original is retained", error);
            } else { failCapture(error); }
          })();
        },
      });
      if (!continuousRecording) {
        chunkTimer.current = setTimeout(() => {
          if (isRecordingChunks.current && pendingChunk.current === completion) {
            void completion.stop().catch(failCapture);
          }
        }, CHUNK_DURATION_MS);
      }
    } catch (error) { failCapture(error); }
  };

  const startChunkRecording = () => {
    isRecordingChunks.current = true;
    startingChunk.current = startChunkLoop();
  };

  const stopChunkRecording = async (): Promise<string | null> => {
    isRecordingChunks.current = false;
    if (chunkTimer.current) { clearTimeout(chunkTimer.current); chunkTimer.current = null; }
    // A folder/manifest write in progress must settle before requesting native stop.
    await startingChunk.current;
    const finishing = pendingChunk.current;
    if (!finishing) return null;
    try {
      const path = await finishing.stop();
      if (!path && originalCapture.current) {
        originalCapture.current.complete = false;
        originalCapture.current.failure = "Native finalization did not confirm a complete source";
      }
      return path;
    } catch (error) {
      if (originalCapture.current) {
        originalCapture.current.complete = false;
        originalCapture.current.failure = recordingErrorMessage(error);
      }
      console.warn("Native stop failed; retaining source", error);
      return null;
    }
  };

  // --- Main Recording Logic ---

  async function handleStartRecording() {
    if (isRecording || isStartingRecording.current) return;
    isStartingRecording.current = true;

    if (!profileId) {
      isStartingRecording.current = false;
      Alert.alert(
        "Profile Required",
        "Please select a profile before recording.",
        [{ text: "OK", onPress: () => router.push("/profiles" as any) }],
      );
      return;
    }

    try {
      if (!camera.current) {
        isStartingRecording.current = false;
        return;
      }

      // Resolve the OS location prompt before recording: permission UI may background the camera.
      if (environmentEnabled && environmentNativeAvailable && weatherEnabled) {
        await appleEnvironment.requestEnvironmentLocationPermission().catch(() => {});
      }
      await MediaLibrary.requestPermissionsAsync(true).catch(() => {});
      if (!mounted.current) return;
      const sessionId = buildWorkoutSessionId(workoutType);
      const ref = await prepareOriginalSession({ profileId, sessionId, mode: continuousRecording ? "continuous" : "chunks" });
      if (!mounted.current) {
        await markOriginalRecordingStopped(ref, { complete: false, reason: "recording_screen_unmounted_before_capture" });
        return;
      }
      const startedAt = Date.now();
      originalCapture.current = { ref, startedAt, order: 0, complete: true, failedUploads: 0, drains: [], release: holdOriginalFiles(ref) };
      heartRateWindow.current.clear();
      resetCaptureObservations();
      stoppingWorkout.current = false;
      resetQuality();
      bpmRef.current = 0;
      isRecordingRef.current = true;
      setIsRecording(true);
      setIsPaused(false);
      useAuthStore.getState().setRecordingActive(true);
      accumulatedMs.current = 0;
      segmentStartTime.current = Date.now();
      setElapsedMs(0);
      console.log("✅ Recording Started (Chunk Streaming)");

      // Compute session ID once for the entire recording session
      sessionIdRef.current = sessionId;
      recordingStartTime.current = startedAt;
      capturedProfileIdRef.current = profileId!;
      setSensorLiveStatus({ accSamples: 0, dropped: null });
      outstandingUploads.current = 0;
      uploadedChunkCount.current = 0;

      environment.start({ sessionId: sessionIdRef.current, profileId: profileId!, startedAt: recordingStartTime.current },
        { wodDescription, movements, appearanceHints: appearanceHints ?? "", language: locale },
        { resolution, lowFps, showSkeleton, onDeviceAi, zoomMode, aspectRatio, landscapeMode,
          observationIntervalSeconds: Number(observationIntervalSeconds) || 300,
          environmentAnalysis: true,
          visualQuestions: environmentAnalysisParam === "true",
          fps: targetFps, videoHdr: false, bufferCompression: false, audio: hasMicPermission, zoom: zoomMode ? 0.1 : 0,
          deviceId: device?.id ?? null, physicalDevices: device?.physicalDevices ?? [],
          format: format ? { width: format.videoWidth, height: format.videoHeight } : null,
          exposure: "unavailable", focus: "unavailable" });

      // Start debug telemetry recording (1Hz sampling)
      TelemetryRecorder.start(sessionIdRef.current, profileId!);
      TelemetryRecorder.registerProvider("hr", () => ({
        hr: getReading().bpm ?? 0,
      }));
      TelemetryRecorder.registerProvider("chunk", () => ({
        chunkIdx: chunkCountRef.current,
      }));
      TelemetryRecorder.registerProvider("workoutConf", () => ({
        workoutConf: Math.round(getWorkoutConfidence() * 1000) / 1000,
      }));
      TelemetryRecorder.registerProvider("motion", () => ({
        motion: getLatestMotion(),
      }));

      // Start Polar H10 time-series sensor recording.
      // Sensor capture is auxiliary: a failure to create/open its file must
      // never keep the camera from recording.
      try {
        PolarSensorRecorder.start({
          sessionId: sessionIdRef.current,
          profileId: profileId!,
          baseEpochMs: recordingStartTime.current,
        });
      } catch (sensorError) {
        console.warn(
          "⚠️ Sensor recording unavailable for this session:",
          sensorError,
        );
      }

      // Experimental: H10 internal HR recording (fire-and-forget, never blocks the camera).
      if (h10MemoryEnabled) {
        try {
          H10MemoryRecorderInstance.begin({
            sessionId: sessionIdRef.current,
            profileId: profileId!,
            baseEpochMs: recordingStartTime.current,
          });
        } catch (h10Error) {
          console.warn("⚠️ H10 internal recording unavailable:", h10Error);
        }
      }

      // Record sequential chunks: each is uploaded for real-time analysis,
      // and raw chunk files are kept locally for gallery-save merge.
      startChunkRecording();
      await startingChunk.current;
    } catch (error) {
      console.error("Recording Start Error:", error);
      // Roll back the optimistic "recording" state. Without this the UI and
      // the auth store stay in a recording state that no camera is backing,
      // and the `if (isRecording) return` guard blocks any retry.
      isRecordingRef.current = false;
      setIsRecording(false);
      setIsPaused(false);
      useAuthStore.getState().setRecordingActive(false);
      void flushEnvironmentUploads().catch(() => {});
      isRecordingChunks.current = false;
      accumulatedMs.current = 0;
      segmentStartTime.current = 0;
      setElapsedMs(0);
      void environment.stop("start_error").catch(() => {});
      void TelemetryRecorder.stop().catch(() => {});
      void PolarSensorRecorder.stop().catch(() => {});
      void H10MemoryRecorderInstance.finish(sessionIdRef.current);
      const capture = originalCapture.current;
      if (capture) {
        await markOriginalRecordingStopped(capture.ref, { complete: false, reason: recordingErrorMessage(error) }).catch(console.warn);
        capture.release();
      }
      Alert.alert(t("originalVideo.captureFailedTitle"), t("originalVideo.captureFailedBody"));
    } finally {
      isStartingRecording.current = false;
    }
  };

  async function handlePauseRecording() {
    if (!isRecordingRef.current || isPausedRef.current || stoppingWorkout.current) return;
    void appleAi.stop().then(flushAppleAiUploads).catch(() => {});
    void environment.pause()?.catch(() => {});
    console.log("⏸️ Pausing recording...");

    if (segmentStartTime.current > 0) {
      accumulatedMs.current += Date.now() - segmentStartTime.current;
      segmentStartTime.current = 0;
    }

    isPausedRef.current = true;
    setIsPaused(true);
    PolarSensorRecorder.pause();
    pauseTransition.current = stopChunkRecording();
    await pauseTransition.current;
    console.log("⏸️ Recording paused safely");
  };

  async function handleResumeRecording() {
    if (!isRecording || !isPaused || stoppingWorkout.current) return;
    await pauseTransition.current;
    if (stoppingWorkout.current || !originalCapture.current || pendingChunk.current) return;
    console.log("▶️ Resuming recording...");

    segmentStartTime.current = Date.now();
    isPausedRef.current = false;
    setIsPaused(false);
    PolarSensorRecorder.resume();
    environment.resume();
    resetQuality();
    startChunkRecording();
    console.log("▶️ Recording resumed");
  };

  async function handleStopRecording() {
    if (!isRecordingRef.current || stoppingWorkout.current) return;
    stoppingWorkout.current = true;
    void appleAi.stop().then(flushAppleAiUploads).catch(() => {});

    try {
      setIsSaving(true);
      const capture = originalCapture.current;
      // Sensor capture ends on its own clock at workout stop, independent of video export latency.
      const sensorStopped = PolarSensorRecorder.stop().catch((error) => {
        console.warn("sensor recorder stop failed", error); return null;
      });
      const telemetryStopped = TelemetryRecorder.stop().catch((error) => {
        console.warn("telemetry stop failed", error); return null;
      });
      // Experimental: no-op unless H10 internal recording runs for this session.
      void H10MemoryRecorderInstance.finish(sessionIdRef.current);

      if (!isPaused && segmentStartTime.current > 0) {
        accumulatedMs.current += Date.now() - segmentStartTime.current;
      }
      segmentStartTime.current = 0;

      await pauseTransition.current;
      await stopChunkRecording();
      if (capture) {
        await markOriginalRecordingStopped(capture.ref, { complete: capture.complete, reason: capture.failure });
        // Gallery export can start as soon as sources close. Pending derivatives/readers
        // keep a hold until both native preparation and environment work have drained.
        void finishOriginalConsumers(capture);
      } else {
        void environment.stop().catch(console.warn);
      }
      isRecordingRef.current = false;
      setIsRecording(false);
      setIsPaused(false);

      // Stop debug telemetry and enqueue upload
      try {
        const telemetryResult = await telemetryStopped;
        if (telemetryResult) {
          await enqueueDebugUpload(
            telemetryResult.sessionId,
            telemetryResult.filePath,
          );
          flushPendingUploads().catch(() => {}); // fire and forget
        }
      } catch (e) {
        console.warn("telemetry stop failed", e);
      }

      // Stop Polar H10 sensor recording and enqueue upload
      try {
        const sensorResult = await sensorStopped;
        if (sensorResult && !sensorResult.complete) {
          // Still worth uploading — the file records its own write failures in
          // the footer, so downstream can tell a lossy session from a full one.
          console.warn(
            `⚠️ Sensor file for ${sensorResult.sessionId} is incomplete; uploading anyway`,
          );
        }
        if (sensorResult && capturedProfileIdRef.current !== null) {
          await enqueueSensorUpload(
            sensorResult.sessionId,
            capturedProfileIdRef.current,
            sensorResult.filePath,
          );
          flushSensorUploads().catch(() => {}); // fire and forget
        }
      } catch (e) {
        console.warn("sensor recorder stop failed", e);
      }

      // Snapshot session ID for both server merge and local merge
      const sessionId = sessionIdRef.current;

      // Fire-and-forget: trigger server-side merge in the background.
      // The merge API just enqueues a task — no reason to block the user
      // on the recording view while it completes.
      // We call it inline (not in setTimeout) so the network request starts
      // before the user can background the app.
      // Use the ref, not `chunkCount` state: the final chunk increments it
      // inside an async callback that this closure may not have observed yet.
      if (capture && capture.order > 0 && (capture.complete || uploadedChunkCount.current > 0)) {
        const movementsArray = movements ? movements.split(", ") : [];
        const injuriesArray = injuries ? injuries.split(", ") : [];

        // Track the merge globally so History page can show a banner
        useMergeStatus.getState().addPending(sessionId);

        (async () => {
          // Merge only once every chunk has actually reached GCS. A fixed
          // delay merged incomplete sessions whenever compression or the
          // network ran long.
          await Promise.all(capture.drains);
          await waitForOutstandingUploads(MERGE_UPLOAD_WAIT_MS);
          try {
            if (uploadedChunkCount.current === 0) {
              console.warn("Server merge skipped because no chunks were uploaded");
              return;
            }
            if (outstandingUploads.current > 0 || capture.failedUploads > 0) {
              console.warn(
                `⚠️ Proceeding with server merge with ${uploadedChunkCount.current} chunk(s) uploaded (${capture.failedUploads} failed, ${outstandingUploads.current} still in flight)`,
              );
            }
            await mergeChunks(sessionId, {
              workoutType,
              movements: movementsArray,
              injuries: injuriesArray,
              profileId: capturedProfileIdRef.current!,
              wodDescription: wodDescription || undefined,
              appearanceHints,
            });
            console.log(`✅ Auto-merge triggered for ${Platform.OS} session`);
          } catch (e) {
            console.error("❌ Auto-merge failed:", e);
          } finally {
            useMergeStatus.getState().removePending(sessionId);
          }
        })(); // IIFE — fires immediately, does not block
      }

      setMergeChunkTotal(capture?.order ?? 0);
      setIsMerging(true);
      // A failed capture has no verified complete original to save. Its failure
      // was already reported; do not turn it into a misleading gallery wait.
      const result = capture?.complete ? await finalizeAndSaveOriginal(capture.ref) : null;
      setIsMerging(false);
      setChunkCount(0);
      const { sessionExpiredDuringRecording, finishDeferredUnauthorized, setRecordingActive } = useAuthStore.getState();
      setRecordingActive(false);
      void flushEnvironmentUploads().catch(() => {});
      if (capture?.complete && result?.status !== "saved") {
        Alert.alert(t("originalVideo.pendingTitle"), t("originalVideo.pendingBody"));
      }
      if (sessionExpiredDuringRecording) {
        Alert.alert(t("auth.sessionExpiredDuringWorkoutTitle"), t("auth.sessionExpiredDuringWorkoutMessage"),
          [{ text: t("common.ok"), onPress: finishDeferredUnauthorized }]);
      } else {
        router.replace("/history" as any);
      }
    } catch (error) {
      console.error("Recording Stop Error:", error);
      Alert.alert(t("originalVideo.pendingTitle"), t("originalVideo.pendingBody"));
      useAuthStore.getState().setRecordingActive(false);
      router.replace("/history" as any);
    } finally {
      const capture = originalCapture.current;
      if (capture) void finishOriginalConsumers(capture);
      setIsMerging(false);
      setIsSaving(false);
    }
  };

  useEffect(() => {
    unmountRecorder.current = () => {
    isRecordingChunks.current = false;
    if (chunkTimer.current) clearTimeout(chunkTimer.current);
    const capture = originalCapture.current;
    if (!capture) return;
    void (async () => {
      try {
        await stopChunkRecording();
        await markOriginalRecordingStopped(capture.ref, { complete: false, reason: "recording_screen_unmounted" });
      } catch (error) { console.warn("Original recovery required after leaving recording", error); }
      finally { void finishOriginalConsumers(capture); }
    })();
    };
  });

  if (!hasPermission || !hasMicPermission) {
    return (
      <View style={styles.center}>
        <Text style={{ color: "#fff", fontSize: 18, marginBottom: 16 }}>
          Camera & Microphone Permission Required
        </Text>
        <TouchableOpacity
          onPress={async () => {
            let cameraRes = hasPermission;
            if (!hasPermission) {
              cameraRes = await requestPermission();
            }
            let micRes = hasMicPermission;
            if (!hasMicPermission) {
              micRes = await requestMicPermission();
            }
            if (!cameraRes || !micRes) {
              // Permission permanently denied — direct to settings
              Alert.alert(
                "Permission Denied",
                "Camera or Microphone permission was permanently denied. Please enable them in Settings.",
                [
                  { text: "Cancel", style: "cancel" },
                  {
                    text: "Open Settings",
                    onPress: () => Linking.openSettings(),
                  },
                ],
              );
            }
          }}
          style={{
            backgroundColor: "#fff",
            paddingVertical: 12,
            paddingHorizontal: 32,
            borderRadius: 10,
          }}
        >
          <Text style={{ color: "#000", fontWeight: "bold", fontSize: 16 }}>
            Grant Camera & Microphone Access
          </Text>
        </TouchableOpacity>
      </View>
    );
  }

  if (!device)
    return (
      <View style={styles.center}>
        <Text style={{ color: "#fff" }}>No Camera</Text>
      </View>
    );

  return (
    <SafeAreaView style={styles.container}>
      {/* Android: hint to mount phone sideways when landscape mode is on */}
      {IS_ANDROID && landscapeMode && !isRecording && (
        <View style={styles.landscapeHint}>
          <Text style={styles.landscapeHintText}>
            📱 Mount phone sideways for landscape view
          </Text>
        </View>
      )}

      <View testID="recording-chrome" style={isLandscapeLayout && styles.chromeLandscape}>
        {/* Top Header: Back navigation & Mode badge */}
        <View style={[styles.topHeader, isLandscapeLayout && styles.topHeaderLandscape]}>
          <TouchableOpacity
            style={styles.backButton}
            onPress={() => {
              if (isRecording) {
                Alert.alert(
                  t("common.confirm"),
                  t("workout.recordingExitConfirm", { defaultValue: "Are you sure you want to stop recording and exit?" }),
                  [
                    { text: t("common.cancel"), style: "cancel" },
                    {
                      text: t("common.ok"),
                      style: "destructive",
                      onPress: () => {
                        void handleStopRecording();
                      },
                    },
                  ]
                );
              } else {
                router.back();
              }
            }}
            hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
            accessibilityRole="button"
            accessibilityLabel={t("common.back")}
          >
            <IconSymbol name="chevron.left" size={24} color="#FFFFFF" />
          </TouchableOpacity>

          <View style={styles.modeBadge}>
            <View style={styles.modeDot} />
            <Text style={styles.modeText}>
              {t("overlay.recording.mode", { type: workoutTypeLabel })}
            </Text>
          </View>
        </View>

        {/* Integrated Slim Status Bar (Outside & Above Viewfinder) */}
        <View style={[styles.slimStatusBar, isLandscapeLayout && styles.slimStatusBarLandscape]}>
          {/* Left: REC Status & Timer */}
          <View style={styles.statusRecSection}>
            <View
              style={[
                styles.statusRecDot,
                isRecording && !isPaused ? styles.statusRecDotActive : styles.statusRecDotIdle,
                isPaused && styles.statusRecDotPaused,
              ]}
            />
            <Text style={[styles.statusRecLabel, isPaused && styles.statusRecLabelPaused]}>
              {isRecording
                ? isPaused
                  ? "PAUSE"
                  : t("overlay.recording.rec")
                : "READY"}
            </Text>
            <Text style={[styles.statusRecTime, isPaused && styles.statusRecTimePaused]}>
              {formatElapsed(elapsedMs)}
            </Text>
          </View>

          {/* Center: Heart Rate (BPM) */}
          <View style={styles.statusHrPill}>
            <Text style={styles.statusHrHeart}>♥</Text>
            <Text style={styles.statusHrValue}>
              {bpm > 0 ? bpm : "--"}
            </Text>
            <Text style={styles.statusHrUnit}>BPM</Text>
          </View>

          {/* Right: Heart Rate Sensor (Polar H10) Battery */}
          <View style={styles.statusBatterySection}>
            <Text style={styles.statusBatteryIcon}>🔋</Text>
            <Text
              style={[
                styles.statusBatteryText,
                batteryLevel === null && styles.statusBatteryTextDisconnected,
                batteryLevel !== null && batteryLevel <= 20 && styles.statusBatteryTextLow,
              ]}
            >
              {batteryLevel !== null ? `${batteryLevel}%` : "--"}
            </Text>
          </View>
        </View>

        {/* Upload Status / Error Banner */}
        {uploadBanner && (
          <View
            testID="upload-status-banner"
            style={[
              styles.uploadBanner,
              uploadBanner.type === "error"
                ? styles.uploadBannerError
                : styles.uploadBannerRetry,
              isLandscapeLayout && styles.uploadBannerLandscape,
            ]}
          >
            <IconSymbol
              name={
                uploadBanner.type === "error"
                  ? "xmark.circle.fill"
                  : "arrow.clockwise"
              }
              size={13}
              color={uploadBanner.type === "error" ? "#FF453A" : "#FFD60A"}
            />
            <Text
              style={[
                styles.uploadBannerText,
                uploadBanner.type === "error"
                  ? styles.uploadBannerTextError
                  : styles.uploadBannerTextRetry,
              ]}
              numberOfLines={2}
            >
              {uploadBanner.message}
            </Text>
          </View>
        )}

        {/* Experimental H10 internal recording warning */}
        {h10MemoryBanner && (
          <View
            testID="h10-memory-banner"
            style={[
              styles.uploadBanner,
              h10MemoryBanner.type === "error" ? styles.uploadBannerError : styles.uploadBannerRetry,
              isLandscapeLayout && styles.uploadBannerLandscape,
            ]}
          >
            <IconSymbol
              name={h10MemoryBanner.type === "error" ? "xmark.circle.fill" : "flag.fill"}
              size={13}
              color={h10MemoryBanner.type === "error" ? "#FF453A" : "#FFD60A"}
            />
            <Text
              style={[
                styles.uploadBannerText,
                h10MemoryBanner.type === "error" ? styles.uploadBannerTextError : styles.uploadBannerTextRetry,
              ]}
              numberOfLines={2}
            >
              {h10MemoryBanner.message}
            </Text>
          </View>
        )}

      </View>

      <View testID="recording-body" style={[styles.recordingBody, isLandscapeLayout && styles.recordingBodyLandscape]}>
        {/* Keep this camera subtree mounted when changing orientation or telemetry. */}
        <View
          testID="recording-viewfinder-space"
          style={[
            styles.viewfinderSpace,
            isLandscapeLayout && styles.viewfinderSpaceLandscape,
            !isLandscapeLayout && isTelemetryExpanded && styles.viewfinderSpaceCompact,
          ]}
          onLayout={({ nativeEvent: { layout } }) => {
            setViewfinderSpace({ width: layout.width, height: layout.height });
          }}
        >
          <View
            testID="recording-viewfinder"
            style={[
              styles.viewfinderWrapper,
              { width: viewfinderWidth, height: viewfinderHeight },
            ]}
          >
            <Camera
              ref={camera}
              style={StyleSheet.absoluteFill}
              device={device}
              isActive={isCameraActive}
              format={format}
              fps={targetFps}
              resizeMode="cover"
              frameProcessor={frameProcessor}
              pixelFormat="yuv"
              videoHdr={false}
              enableBufferCompression={false}
              video={true}
              audio={hasMicPermission}
              videoStabilizationMode={videoStabilizationMode}
              zoom={zoomMode ? 0.1 : 0}
              onInitialized={() => setIsCameraReady(true)}
              onError={(error) => {
                if (error.message?.includes("delete orphan")) {
                  console.log("📷 Ignoring orphan cleanup warning");
                  return;
                }
                console.error("📷 Camera Error:", error.code, error.message);
              }}
            />

            {/* Skeleton overlay inside compact viewfinder */}
            {showSkeleton && (
              <View style={StyleSheet.absoluteFill} pointerEvents="none">
                <SkeletonOverlay
                  pose={poseResult}
                  width={viewfinderWidth}
                  height={viewfinderHeight}
                />
              </View>
            )}

            {/* Bottom edge hint and video format indicators */}
            <TouchableOpacity
              activeOpacity={0.8}
              onPress={() => setIsTelemetryExpanded((prev) => !prev)}
              style={styles.viewfinderBottomBar}
              accessibilityRole="button"
              accessibilityLabel={
                isTelemetryExpanded
                  ? t("overlay.recording.tapToCollapse")
                  : t("overlay.recording.tapToExpand")
              }
            >
              <View style={styles.viewfinderBottomLeft}>
                <IconSymbol
                  name={isTelemetryExpanded ? "chevron.up" : "chevron.down"}
                  size={13}
                  color="#00F0FF"
                />
                <Text style={styles.viewfinderBottomText} numberOfLines={1}>
                  {isLandscapeLayout
                    ? t(isTelemetryExpanded ? "overlay.recording.hideTelemetry" : "overlay.recording.showTelemetry")
                    : t(isTelemetryExpanded ? "overlay.recording.tapToCollapse" : "overlay.recording.tapToExpand")}
                </Text>
              </View>
              <Text style={styles.viewfinderSpecText}>
                {[
                  resolution.toUpperCase(),
                  `${targetFps}FPS`,
                  skipCompression ? "RAW" : "LOG",
                ].join(" · ")}
              </Text>
            </TouchableOpacity>
          </View>
        </View>

        <View
          testID="recording-sidebar"
          style={[
            isLandscapeLayout
              ? styles.recordingSidebarLandscape
              : isTelemetryExpanded && styles.recordingSidebarExpanded,
            isLandscapeLayout && { width: isTelemetryExpanded ? "36%" : 92, minWidth: isTelemetryExpanded ? 176 : 92, maxWidth: 280 },
          ]}
        >
          {/* Controls remain outside the scrolling telemetry panel. */}
          {!previewOnly && !isMerging && (
            <View
              style={[
                styles.controlBarContainer,
                isLandscapeLayout && styles.controlBarContainerLandscape,
              ]}
            >
              {isSaving ? (
                <View style={styles.postRecordingFooter}>
                  <Text style={styles.footerStatus}>
                    {t("originalVideos.status.preparing")}
                  </Text>
                  <View style={styles.footerProgressBg}>
                    <View
                      style={[
                        styles.footerProgressFill,
                        { width: "100%", backgroundColor: "#30D158" },
                      ]}
                    />
                  </View>
                </View>
              ) : !isRecording ? (
                /* Standby / Preview Controls */
                <View style={[styles.controlRow, isLandscapeLayout && !isTelemetryExpanded && styles.controlColumn]}>
                  {!isLandscapeLayout && <View style={styles.controlBtnPlaceholder} />}
                  <TouchableOpacity
                    onPress={handleStartRecording}
                    style={styles.startRecordBtn}
                    accessibilityRole="button"
                    accessibilityLabel="Start Recording"
                  >
                    <View style={styles.startRecordInner} />
                  </TouchableOpacity>
                  <TouchableOpacity
                    onPress={handleFlipCamera}
                    style={styles.controlIconBtn}
                    accessibilityRole="button"
                    accessibilityLabel={t("overlay.recording.flipCamera")}
                  >
                    <IconSymbol name="camera.rotate" size={20} color="#FFFFFF" />
                  </TouchableOpacity>
                </View>
              ) : (
                /* Active Recording Controls: Clean 3-button layout (Flag/Lap removed) */
                <View style={[styles.controlRow, isLandscapeLayout && !isTelemetryExpanded && styles.controlColumn]}>
                  {/* 1. Pause / Resume */}
                  <TouchableOpacity
                    onPress={isPaused ? handleResumeRecording : handlePauseRecording}
                    style={[
                      styles.pauseResumeBtn,
                      isPaused ? styles.resumeBtnActive : styles.pauseBtnActive,
                      isLandscapeLayout && styles.landscapeActionBtn,
                    ]}
                    accessibilityRole="button"
                    accessibilityLabel={
                      isPaused
                        ? t("overlay.recording.resume")
                        : t("overlay.recording.pause")
                    }
                  >
                    <IconSymbol
                      name={isPaused ? "play.fill" : "pause.fill"}
                      size={18}
                      color="#FFFFFF"
                    />
                    {!isLandscapeLayout && <Text style={styles.pauseResumeText}>
                      {isPaused
                        ? t("overlay.recording.resume")
                        : t("overlay.recording.pause")}
                    </Text>}
                  </TouchableOpacity>

                  {/* 2. Stop Recording (Red button with square.fill) */}
                  <TouchableOpacity
                    onPress={handleStopRecording}
                    style={[styles.stopRecordBtn, isLandscapeLayout && styles.landscapeActionBtn]}
                    accessibilityRole="button"
                    accessibilityLabel={t("overlay.recording.stop")}
                  >
                    <IconSymbol name="square.fill" size={16} color="#FFFFFF" />
                    {!isLandscapeLayout && <Text style={styles.stopRecordText}>
                      {t("overlay.recording.stop")}
                    </Text>}
                  </TouchableOpacity>

                  {/* 3. Camera Flip */}
                  <TouchableOpacity
                    onPress={handleFlipCamera}
                    style={styles.controlIconBtn}
                    accessibilityRole="button"
                    accessibilityLabel={t("overlay.recording.flipCamera")}
                  >
                    <IconSymbol name="camera.rotate" size={18} color="#FFFFFF" />
                  </TouchableOpacity>
                </View>
              )}
            </View>
          )}

          {/* Telemetry Toggle Bar (Expands/Collapses Telemetry & Adjusts Video Viewfinder) */}
          {!previewOnly && (
            <TouchableOpacity
              onPress={() => setIsTelemetryExpanded((prev) => !prev)}
              activeOpacity={0.8}
              style={[styles.telemetryToggleBar, isLandscapeLayout && styles.telemetryToggleLandscape]}
              accessibilityRole="button"
              accessibilityLabel={
                isTelemetryExpanded
                  ? t("overlay.recording.collapseTelemetry")
                  : t("overlay.recording.expandTelemetry")
              }
            >
              <IconSymbol
                name={isTelemetryExpanded ? "chevron.up" : "chevron.down"}
                size={13}
                color="#00F0FF"
              />
              <Text style={styles.telemetryToggleText}>
                {isLandscapeLayout
                  ? t(isTelemetryExpanded ? "overlay.recording.hideTelemetry" : "overlay.recording.showTelemetry")
                  : t(isTelemetryExpanded ? "overlay.recording.collapseTelemetry" : "overlay.recording.expandTelemetry")}
              </Text>
            </TouchableOpacity>
          )}

          {/* Telemetry Section (Scrollable Cards - Visible only when expanded) */}
          {isTelemetryExpanded && (
            <ScrollView
              testID="recording-telemetry"
              style={styles.telemetryScrollView}
              contentContainerStyle={[styles.telemetryContent, isLandscapeLayout && styles.telemetryContentLandscape]}
              nestedScrollEnabled={true}
              showsVerticalScrollIndicator={false}
            >
              {/* Card 1: 실시간 피드백 (Live Coaching Feedback) */}
              {(isRecording || chunkFeedback || liveFeedback.captureAdvice || liveFeedback.summary?.available) && (
                <View style={styles.feedbackCardWrapper}>
                  {isFeedbackCollapsed ? (
                    <TouchableOpacity
                      style={styles.collapsedFeedbackPill}
                      onPress={() => setIsFeedbackCollapsed(false)}
                      activeOpacity={0.8}
                      accessibilityRole="button"
                      accessibilityLabel={t("overlay.feedback.expand")}
                    >
                      <View style={styles.collapsedFeedbackContent}>
                        <IconSymbol name="sparkles" size={14} color="#30D158" />
                        <Text style={styles.collapsedFeedbackText} numberOfLines={1}>
                          {chunkFeedback ||
                            liveFeedback.captureAdvice ||
                            t("overlay.feedback.collapsedLabel")}
                        </Text>
                        <View style={styles.expandBadge}>
                          <Text style={styles.expandBadgeText}>
                            {t("overlay.feedback.expand")}
                          </Text>
                          <IconSymbol name="chevron.down" size={12} color="#00F0FF" />
                        </View>
                      </View>
                    </TouchableOpacity>
                  ) : (
                    <View style={styles.feedbackCard}>
                      <View style={styles.feedbackHeader}>
                        <View style={styles.feedbackHeaderLeft}>
                          <IconSymbol name="sparkles" size={15} color="#30D158" />
                          <Text style={styles.feedbackTitle} numberOfLines={1}>
                            {t("overlay.feedback.title")}
                          </Text>
                        </View>
                        <View style={styles.feedbackHeaderRight}>
                          {!isLandscapeLayout && <View style={styles.badgeActivePill}>
                            <View style={styles.activeDot} />
                            <Text style={styles.badgeActiveText}>
                              {t("overlay.recording.feedbackActive")}
                            </Text>
                          </View>}
                          <TouchableOpacity
                            style={styles.collapseBtn}
                            onPress={() => setIsFeedbackCollapsed(true)}
                            hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                            accessibilityRole="button"
                            accessibilityLabel={t("overlay.feedback.collapse")}
                          >
                            <Text style={styles.collapseBtnText}>
                              {t("overlay.feedback.collapse")}
                            </Text>
                            <IconSymbol name="chevron.up" size={11} color="#94A3B8" />
                          </TouchableOpacity>
                        </View>
                      </View>

                      {liveFeedback.captureAdvice && (
                        <View style={styles.adviceRow}>
                          <IconSymbol name="camera.fill" size={14} color="#FFD28A" />
                          <Text style={styles.adviceText}>
                            {t("activity.camera")}: {liveFeedback.captureAdvice}
                          </Text>
                        </View>
                      )}

                      {chunkFeedback && (
                        <Text style={styles.coachingText}>{chunkFeedback}</Text>
                      )}

                      <ActivitySummaryContent summary={liveFeedback.summary} compact />
                    </View>
                  )}
                </View>
              )}

              {/* Card 2: 동작 감지 현황 (Observed Activity - Core Workout Data) */}
              {!previewOnly && (
                <View style={styles.activityCard}>
                  <View style={[styles.activityHeader, isLandscapeLayout && styles.cardHeaderLandscape]}>
                    <View style={[styles.cardHeaderTitleRow, isLandscapeLayout && styles.cardTitleLandscape]}>
                      <IconSymbol name="dumbbell.fill" size={14} color="#00F0FF" />
                      <Text style={styles.activityTitle} numberOfLines={1}>
                        {t("overlay.recording.observedActivityTitle")}
                      </Text>
                    </View>
                    {liveFeedback.summary?.available && (
                      <View style={styles.syncBadge}>
                        <Text style={styles.syncBadgeText}>
                          {t(`activity.state.${liveFeedback.summary.review_state}`)}
                        </Text>
                      </View>
                    )}
                  </View>
                  <View style={styles.movementTilesRow}>
                    {observedMovements.length === 0 ? (
                      <View style={styles.movementEmptyTile}>
                        <Text style={styles.movementEmptyText}>
                          {t("activity.noEvidence")}
                        </Text>
                      </View>
                    ) : (
                      observedMovements.map((item) => (
                        <View key={`${item.movement}/${item.unit}`} testID={`observed-movement-${item.movement}-${item.unit}`} style={styles.movementTile}>
                          <Text style={styles.movementName} numberOfLines={1}>
                            {item.movement}
                          </Text>
                          <View style={styles.movementRepBox}>
                            <Text style={styles.movementRepCount}>
                              {item.unit === "reps" ? item.count : Math.round(item.seconds * 10) / 10}
                            </Text>
                            <Text style={styles.movementRepUnit}>
                              {t(`overlay.recording.${item.unit === "reps" ? "repsUnit" : "secondsUnit"}`)}
                            </Text>
                          </View>
                        </View>
                      ))
                    )}
                  </View>
                </View>
              )}

              {/* --- 실험적인 데이터 (EXPERIMENTAL & DIAGNOSTICS - Moved to Bottom) --- */}
              {!previewOnly && (
                <View style={styles.experimentalSectionHeader}>
                  <View style={styles.experimentalDivider} />
                  <Text style={styles.experimentalSectionTitle}>
                    {t("overlay.recording.experimentalSection")}
                  </Text>
                  <View style={styles.experimentalDivider} />
                </View>
              )}

              {/* Card 3: AI 비전 실험 (AI EXPERIMENT & STATE) */}
              {!previewOnly && (
                <View style={styles.aiVisionCard}>
                  <View style={[styles.aiVisionHeader, isLandscapeLayout && styles.cardHeaderLandscape]}>
                    <View style={[styles.cardHeaderTitleRow, isLandscapeLayout && styles.cardTitleLandscape]}>
                      <IconSymbol name="eye" size={14} color="#00F0FF" />
                      <Text style={styles.aiVisionTitle} numberOfLines={1}>
                        {t("overlay.recording.aiVisionTitle")}
                      </Text>
                    </View>
                    <View style={styles.aiVisionBadge}>
                      <Text style={styles.aiVisionBadgeText}>
                        {t(`overlay.recording.${isModelLoaded ? "modelReady" : "modelLoading"}`)}
                      </Text>
                    </View>
                  </View>
                  {isModelLoaded && (
                    <View style={styles.jsonTerminalBox}>
                      <Text style={styles.jsonTerminalText}>
                        {t("overlay.recording.detectionConfidence", { value: (safeConfidence * 100).toFixed(0) })}
                      </Text>
                      <Text style={styles.jsonTerminalText}>
                        {t("overlay.recording.motionMagnitude", { value: safeMotion.toFixed(3) })}
                      </Text>
                      <Text style={styles.jsonTerminalText}>
                        {t(`overlay.recording.${monitorData.isWorkingOut ? "motionDetected" : "motionNotDetected"}`)}
                      </Text>
                    </View>
                  )}
                </View>
              )}

              {/* On-device Environment Observation Card (Experimental) */}
              {environmentEnabled && (
                <View style={styles.experimentalCardWrapper}>
                  <EnvironmentLiveCard
                    status={environment.status}
                    record={environment.record}
                  />
                </View>
              )}

              {/* Experimental H10 internal HR recording status */}
              {h10MemoryEnabled && h10MemoryStatusForSession && (
                <View testID="h10-memory-status" style={[styles.experimentalCardWrapper, styles.jsonTerminalBox]}>
                  <Text style={styles.jsonTerminalText}>
                    {t("overlay.recording.h10MemoryStatus", {
                      phase:
                        h10MemoryStatusForSession.sampleCount !== null
                          ? `${h10MemoryStatusForSession.phase} (${h10MemoryStatusForSession.sampleCount})`
                          : h10MemoryStatusForSession.phase,
                    })}
                  </Text>
                </View>
              )}

              {/* Apple On-Device AI Feedback Card (Experimental) */}
              {onDeviceAi && (
                <View style={styles.experimentalCardWrapper}>
                  <AppleAiFeedbackCard
                    status={appleAi.status}
                    error={appleAi.error}
                    result={appleAi.result}
                    archiveError={appleAi.archiveError}
                  />
                </View>
              )}

              {/* Energy Monitor (Compact bottom telemetry element) */}
              {!previewOnly && (
                <View style={styles.energyMonitorWrapper}>
                  <EnergyMonitor
                    label={
                      isRecording ? "Default Model (7MB) · 2fps" : "Preview · 1fps"
                    }
                  />
                </View>
              )}
            </ScrollView>
          )}

        </View>
      </View>

      {/* Merging indicator overlay */}
      {isMerging && (
        <View style={styles.mergingOverlay}>
          <View style={styles.mergingCard}>
            <ActivityIndicator size="large" color="#30D158" />
            <Text style={styles.mergingTitle}>{t("originalVideos.title")}</Text>
            <Text style={styles.mergingSubtitle}>
              {t("originalVideo.preparing", { count: mergeChunkTotal })}
            </Text>
            <View style={styles.mergingProgressBg}>
              <Animated.View
                style={[
                  styles.mergingProgressIndeterminate,
                  {
                    transform: [
                      {
                        translateX: mergeShimmerAnim.interpolate({
                          inputRange: [0, 1],
                          outputRange: [-60, 160],
                        }),
                      },
                    ],
                  },
                ]}
              />
            </View>
          </View>
        </View>
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: "#05080C",
    paddingVertical: 8,
  },
  chromeLandscape: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingHorizontal: 16,
    marginBottom: 8,
  },
  recordingBody: {
    flex: 1,
    minHeight: 0,
  },
  recordingBodyLandscape: {
    flexDirection: "row",
    paddingHorizontal: 16,
    gap: 12,
  },
  recordingSidebarExpanded: {
    flex: 1,
    minHeight: 0,
  },
  recordingSidebarLandscape: {
    minHeight: 0,
    justifyContent: "center",
    flexShrink: 0,
  },
  center: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    backgroundColor: "#05080C",
    padding: 24,
  },
  lapToastContainer: {
    position: "absolute",
    top: Platform.OS === "ios" ? 56 : 36,
    alignSelf: "center",
    zIndex: 999,
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "rgba(11, 20, 32, 0.95)",
    borderWidth: 1,
    borderColor: "#00F0FF",
    borderRadius: 20,
    paddingHorizontal: 16,
    paddingVertical: 8,
    gap: 8,
    shadowColor: "#00F0FF",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.35,
    shadowRadius: 8,
    elevation: 8,
  },
  lapToastText: {
    color: "#E2E8F0",
    fontSize: 12,
    fontWeight: "700",
  },
  landscapeHint: {
    position: "absolute",
    top: 48,
    alignSelf: "center",
    zIndex: 50,
    backgroundColor: "rgba(11, 20, 32, 0.85)",
    paddingVertical: 6,
    paddingHorizontal: 14,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.15)",
  },
  landscapeHintText: {
    color: "#94A3B8",
    fontSize: 12,
    fontWeight: "600",
  },
  topHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    marginBottom: 8,
  },
  topHeaderLandscape: {
    paddingHorizontal: 0,
    marginBottom: 0,
    gap: 12,
  },
  backButton: {
    width: 38,
    height: 38,
    borderRadius: 19,
    backgroundColor: "rgba(255, 255, 255, 0.08)",
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.12)",
    justifyContent: "center",
    alignItems: "center",
  },
  modeBadge: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "rgba(48, 209, 88, 0.12)",
    borderColor: "rgba(48, 209, 88, 0.35)",
    borderWidth: 1,
    borderRadius: 16,
    paddingHorizontal: 12,
    paddingVertical: 5,
    gap: 6,
  },
  modeDot: {
    width: 7,
    height: 7,
    borderRadius: 3.5,
    backgroundColor: "#30D158",
  },
  modeText: {
    color: "#E2E8F0",
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 0.6,
  },
  slimStatusBar: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    backgroundColor: "#0B111B",
    borderRadius: 22,
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.08)",
    paddingHorizontal: 14,
    paddingVertical: 7,
    marginHorizontal: 16,
    marginBottom: 10,
  },
  slimStatusBarLandscape: {
    flex: 1,
    marginHorizontal: 0,
    marginBottom: 0,
  },
  uploadBanner: {
    flexDirection: "row",
    alignItems: "center",
    borderRadius: 16,
    paddingHorizontal: 12,
    paddingVertical: 6,
    marginHorizontal: 16,
    marginBottom: 8,
    gap: 8,
    borderWidth: 1,
  },
  uploadBannerLandscape: {
    marginHorizontal: 0,
    marginBottom: 6,
  },
  uploadBannerError: {
    backgroundColor: "rgba(255, 69, 58, 0.12)",
    borderColor: "rgba(255, 69, 58, 0.35)",
  },
  uploadBannerRetry: {
    backgroundColor: "rgba(255, 214, 10, 0.12)",
    borderColor: "rgba(255, 214, 10, 0.35)",
  },
  uploadBannerText: {
    flex: 1,
    fontSize: 11,
    fontWeight: "700",
  },
  uploadBannerTextError: {
    color: "#FF453A",
  },
  uploadBannerTextRetry: {
    color: "#FFD60A",
  },
  statusRecSection: {
    flexDirection: "row",
    alignItems: "center",
  },
  statusRecDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: 6,
  },
  statusRecDotActive: {
    backgroundColor: "#FF453A",
  },
  statusRecDotIdle: {
    backgroundColor: "#64748B",
  },
  statusRecDotPaused: {
    backgroundColor: "#FFD60A",
  },
  statusRecLabel: {
    color: "#FF453A",
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 0.5,
    marginRight: 6,
  },
  statusRecLabelPaused: {
    color: "#FFD60A",
  },
  statusRecTime: {
    color: "#FFFFFF",
    fontSize: 13,
    fontWeight: "700",
    fontFamily: "monospace",
  },
  statusRecTimePaused: {
    color: "#FFD60A",
  },
  statusHrPill: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "rgba(48, 209, 88, 0.15)",
    borderWidth: 1,
    borderColor: "rgba(48, 209, 88, 0.35)",
    borderRadius: 14,
    paddingHorizontal: 10,
    paddingVertical: 3,
    gap: 4,
  },
  statusHrHeart: {
    color: "#30D158",
    fontSize: 11,
    fontWeight: "700",
  },
  statusHrValue: {
    color: "#30D158",
    fontSize: 13,
    fontWeight: "800",
    fontFamily: "monospace",
  },
  statusHrUnit: {
    color: "rgba(48, 209, 88, 0.8)",
    fontSize: 9,
    fontWeight: "700",
  },
  statusBatterySection: {
    flexDirection: "row",
    alignItems: "center",
  },
  statusBatteryIcon: {
    fontSize: 12,
    marginRight: 4,
  },
  statusBatteryText: {
    color: "#30D158",
    fontSize: 12,
    fontWeight: "700",
    fontFamily: "monospace",
  },
  statusBatteryTextLow: {
    color: "#FF453A",
  },
  statusBatteryTextDisconnected: {
    color: "#64748B",
  },
  viewfinderSpace: {
    flex: 1,
    minHeight: 0,
    alignItems: "center",
    justifyContent: "center",
    marginHorizontal: 16,
    overflow: "hidden",
  },
  viewfinderSpaceLandscape: {
    marginHorizontal: 0,
    minWidth: 0,
  },
  viewfinderSpaceCompact: {
    maxHeight: 210,
  },
  viewfinderWrapper: {
    alignSelf: "center",
    borderRadius: 20,
    overflow: "hidden",
    borderWidth: 1,
    borderColor: "rgba(0, 240, 255, 0.25)",
    backgroundColor: "#000000",
    position: "relative",
  },
  viewfinderBottomBar: {
    position: "absolute",
    bottom: 0,
    left: 0,
    right: 0,
    backgroundColor: "rgba(5, 8, 12, 0.72)",
    paddingHorizontal: 12,
    paddingVertical: 6,
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    borderTopWidth: 1,
    borderTopColor: "rgba(255, 255, 255, 0.05)",
  },
  viewfinderBottomLeft: {
    flex: 1,
    minWidth: 0,
    marginRight: 8,
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  viewfinderBottomText: {
    flexShrink: 1,
    color: "#94A3B8",
    fontSize: 10,
    fontWeight: "500",
  },
  viewfinderSpecText: {
    color: "#00F0FF",
    fontSize: 9,
    fontWeight: "700",
    letterSpacing: 0.5,
  },
  controlBarContainer: {
    marginVertical: 10,
    paddingHorizontal: 16,
    alignItems: "center",
  },
  controlBarContainerLandscape: {
    paddingHorizontal: 0,
    marginVertical: 6,
  },
  controlColumn: {
    flexDirection: "column",
  },
  landscapeActionBtn: {
    width: 48,
    minWidth: 48,
    paddingHorizontal: 0,
  },
  controlRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 12,
    width: "100%",
  },
  controlBtnPlaceholder: {
    width: 48,
  },
  controlIconBtn: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: "rgba(255, 255, 255, 0.08)",
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.14)",
    justifyContent: "center",
    alignItems: "center",
  },
  startRecordBtn: {
    width: 68,
    height: 68,
    borderRadius: 34,
    borderWidth: 4,
    borderColor: "#FFFFFF",
    justifyContent: "center",
    alignItems: "center",
  },
  startRecordInner: {
    width: 52,
    height: 52,
    borderRadius: 26,
    backgroundColor: "#FF453A",
  },
  pauseResumeBtn: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    height: 48,
    paddingHorizontal: 16,
    borderRadius: 24,
    gap: 6,
    minWidth: 100,
  },
  pauseBtnActive: {
    backgroundColor: "rgba(255, 255, 255, 0.08)",
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.14)",
  },
  resumeBtnActive: {
    backgroundColor: "#30D158",
  },
  pauseResumeText: {
    color: "#FFFFFF",
    fontSize: 13,
    fontWeight: "700",
  },
  stopRecordBtn: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    height: 48,
    paddingHorizontal: 20,
    borderRadius: 24,
    backgroundColor: "#FF453A",
    shadowColor: "#FF453A",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.45,
    shadowRadius: 10,
    elevation: 6,
    gap: 6,
    minWidth: 110,
  },
  stopRecordText: {
    color: "#FFFFFF",
    fontSize: 13,
    fontWeight: "800",
  },
  telemetryToggleBar: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    alignSelf: "center",
    gap: 6,
    paddingHorizontal: 16,
    paddingVertical: 7,
    borderRadius: 18,
    backgroundColor: "rgba(11, 17, 27, 0.85)",
    borderWidth: 1,
    borderColor: "rgba(0, 240, 255, 0.2)",
    marginBottom: 8,
  },
  telemetryToggleLandscape: {
    paddingHorizontal: 8,
    minHeight: 40,
  },
  telemetryToggleText: {
    color: "#00F0FF",
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.3,
  },
  telemetryScrollView: {
    flex: 1,
    marginTop: 2,
  },
  telemetryContent: {
    paddingHorizontal: 16,
    paddingBottom: 40,
    gap: 10,
  },
  telemetryContentLandscape: {
    paddingHorizontal: 0,
    paddingBottom: 8,
  },
  feedbackCardWrapper: {
    marginBottom: 2,
  },
  collapsedFeedbackPill: {
    backgroundColor: "#0B111B",
    borderRadius: 18,
    paddingVertical: 8,
    paddingHorizontal: 14,
    borderWidth: 1,
    borderColor: "rgba(48, 209, 88, 0.45)",
    alignSelf: "center",
    maxWidth: "100%",
  },
  collapsedFeedbackContent: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  collapsedFeedbackText: {
    color: "#FFFFFF",
    fontSize: 12,
    fontWeight: "600",
    flexShrink: 1,
  },
  expandBadge: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "rgba(0, 240, 255, 0.15)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 10,
    gap: 2,
  },
  expandBadgeText: {
    color: "#00F0FF",
    fontSize: 10,
    fontWeight: "700",
  },
  feedbackCard: {
    backgroundColor: "#0B111B",
    borderRadius: 18,
    borderWidth: 1,
    borderColor: "rgba(255, 69, 58, 0.25)",
    borderLeftWidth: 3,
    borderLeftColor: "#FF453A",
    padding: 14,
    gap: 8,
  },
  feedbackHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingBottom: 6,
    borderBottomWidth: 1,
    borderBottomColor: "rgba(255, 255, 255, 0.06)",
    gap: 8,
  },
  feedbackHeaderLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    flex: 1,
  },
  feedbackTitle: {
    color: "#FFFFFF",
    fontSize: 13,
    fontWeight: "800",
    flexShrink: 1,
  },
  feedbackHeaderRight: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  badgeActivePill: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    backgroundColor: "rgba(48, 209, 88, 0.15)",
    borderWidth: 1,
    borderColor: "rgba(48, 209, 88, 0.35)",
    borderRadius: 10,
    paddingHorizontal: 7,
    paddingVertical: 2,
  },
  activeDot: {
    width: 5,
    height: 5,
    borderRadius: 2.5,
    backgroundColor: "#30D158",
  },
  badgeActiveText: {
    color: "#30D158",
    fontSize: 9,
    fontWeight: "800",
  },
  collapseBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 3,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 8,
    backgroundColor: "rgba(255, 255, 255, 0.08)",
  },
  collapseBtnText: {
    color: "#94A3B8",
    fontSize: 10,
    fontWeight: "600",
  },
  adviceRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: "rgba(255, 210, 138, 0.08)",
    padding: 8,
    borderRadius: 8,
  },
  adviceText: {
    color: "#FFD28A",
    fontSize: 12,
    fontWeight: "600",
    flex: 1,
  },
  coachingText: {
    color: "#E2E8F0",
    fontSize: 13,
    fontWeight: "500",
    lineHeight: 19,
  },
  experimentalSectionHeader: {
    flexDirection: "row",
    alignItems: "center",
    marginVertical: 12,
    paddingHorizontal: 4,
    gap: 10,
  },
  experimentalDivider: {
    flex: 1,
    height: 1,
    backgroundColor: "rgba(255, 255, 255, 0.08)",
  },
  experimentalSectionTitle: {
    color: "#64748B",
    fontSize: 10,
    fontWeight: "700",
    letterSpacing: 0.8,
    textTransform: "uppercase",
  },
  experimentalCardWrapper: {
    marginBottom: 4,
  },
  aiVisionCard: {
    backgroundColor: "#0B111B",
    borderRadius: 18,
    borderWidth: 1,
    borderColor: "rgba(0, 240, 255, 0.2)",
    padding: 14,
    gap: 8,
  },
  aiVisionHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  cardHeaderLandscape: {
    flexDirection: "column",
    alignItems: "flex-start",
  },
  cardTitleLandscape: {
    flex: 0,
    alignSelf: "stretch",
  },
  cardHeaderTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    flex: 1,
  },
  aiVisionTitle: {
    color: "#00F0FF",
    fontSize: 12,
    fontWeight: "800",
    letterSpacing: 0.5,
    flexShrink: 1,
  },
  aiVisionBadge: {
    backgroundColor: "rgba(0, 240, 255, 0.1)",
    borderWidth: 1,
    borderColor: "rgba(0, 240, 255, 0.25)",
    borderRadius: 10,
    paddingHorizontal: 8,
    paddingVertical: 3,
  },
  aiVisionBadgeText: {
    color: "#00F0FF",
    fontSize: 10,
    fontWeight: "700",
    fontFamily: "monospace",
  },
  jsonTerminalBox: {
    backgroundColor: "#060A10",
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "rgba(0, 240, 255, 0.12)",
    padding: 10,
  },
  jsonTerminalText: {
    color: "#7DD3FC",
    fontSize: 11,
    fontFamily: "monospace",
    lineHeight: 16,
  },
  activityCard: {
    backgroundColor: "#0B111B",
    borderRadius: 18,
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.08)",
    padding: 14,
    gap: 10,
  },
  activityHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  activityTitle: {
    color: "#FFFFFF",
    fontSize: 12,
    fontWeight: "800",
    flexShrink: 1,
  },
  syncBadge: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    backgroundColor: "rgba(48, 209, 88, 0.12)",
    borderWidth: 1,
    borderColor: "rgba(48, 209, 88, 0.3)",
    borderRadius: 12,
    paddingHorizontal: 8,
    paddingVertical: 3,
  },
  syncDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: "#30D158",
  },
  syncBadgeText: {
    color: "#30D158",
    fontSize: 10,
    fontWeight: "700",
  },
  movementEmptyTile: {
    flex: 1,
    paddingVertical: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  movementEmptyText: {
    color: "#64748B",
    fontSize: 12,
    fontWeight: "500",
  },
  movementTilesRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  movementTile: {
    flex: 1,
    minWidth: 140,
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    backgroundColor: "#060A10",
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "rgba(255, 255, 255, 0.06)",
    paddingHorizontal: 12,
    paddingVertical: 10,
  },
  movementName: {
    color: "#CBD5E1",
    fontSize: 12,
    fontWeight: "600",
    flexShrink: 1,
    marginRight: 6,
  },
  movementRepBox: {
    flexDirection: "row",
    alignItems: "baseline",
    gap: 3,
  },
  movementRepCount: {
    color: "#00F0FF",
    fontSize: 15,
    fontWeight: "800",
    fontFamily: "monospace",
  },
  movementRepUnit: {
    color: "#94A3B8",
    fontSize: 10,
    fontWeight: "600",
  },
  energyMonitorWrapper: {
    marginTop: 2,
  },
  postRecordingFooter: {
    alignItems: "center",
    backgroundColor: "rgba(11, 17, 27, 0.95)",
    paddingVertical: 14,
    paddingHorizontal: 20,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: "#333",
    width: "100%",
    gap: 8,
  },
  footerStatus: {
    color: "#fff",
    fontSize: 14,
    fontWeight: "700",
  },
  footerProgressBg: {
    width: "100%",
    height: 6,
    backgroundColor: "#333",
    borderRadius: 3,
    overflow: "hidden",
  },
  footerProgressFill: {
    height: "100%",
    backgroundColor: "#FFD60A",
    borderRadius: 3,
  },
  mergingOverlay: {
    ...StyleSheet.absoluteFill,
    backgroundColor: "rgba(0, 0, 0, 0.85)",
    justifyContent: "center",
    alignItems: "center",
    zIndex: 100,
  },
  mergingCard: {
    backgroundColor: "rgba(11, 17, 27, 0.95)",
    borderRadius: 20,
    paddingVertical: 28,
    paddingHorizontal: 36,
    alignItems: "center",
    borderWidth: 1,
    borderColor: "rgba(0, 240, 255, 0.25)",
    gap: 12,
    minWidth: 260,
  },
  mergingTitle: {
    color: "#fff",
    fontSize: 18,
    fontWeight: "700",
    marginTop: 6,
  },
  mergingSubtitle: {
    color: "#94A3B8",
    fontSize: 13,
    fontWeight: "500",
    textAlign: "center",
  },
  mergingProgressBg: {
    width: "100%",
    height: 4,
    backgroundColor: "#1E293B",
    borderRadius: 2,
    overflow: "hidden",
    marginTop: 6,
  },
  mergingProgressIndeterminate: {
    width: "40%",
    height: "100%",
    backgroundColor: "#30D158",
    borderRadius: 2,
  },
});
