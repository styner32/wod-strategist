import { appleEnvironment, environmentNativeAvailable } from "../../modules/apple-on-device-ai";
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
  StyleSheet,
  Text,
  TouchableOpacity,
  useWindowDimensions,
  View,
} from "react-native";
import { Video } from "react-native-compressor";
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

  const device = useCameraDevice("back");
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
    return () => { mounted.current = false; unmountRecorder.current(); };
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

  // Orientation lock: landscape mode from setup page
  // iOS: lock to landscape (works perfectly with AVCaptureSession)
  // Android: keep portrait — CameraX breaks when Activity rotates via configChanges.
  //   Instead, the user mounts their phone sideways. The camera sensor is physically
  //   landscape, so content is captured wide. UI shows a mounting hint.
  useFocusEffect(
    useCallback(() => {
      if (landscapeMode && !IS_ANDROID) {
        ScreenOrientation.lockAsync(
          ScreenOrientation.OrientationLock.LANDSCAPE,
        );
      }
      return () => {
        if (!IS_ANDROID) {
          ScreenOrientation.lockAsync(
            ScreenOrientation.OrientationLock.PORTRAIT_UP,
          );
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
          });
        } catch (error) {
          capture.failedUploads += 1;
          console.warn("Analysis upload failed; gallery archive is independent", error);
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
      if (environmentEnabled && environmentNativeAvailable) {
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
          observationIntervalSeconds: Number(observationIntervalSeconds) || 60,
          environmentAnalysis: environmentAnalysisParam !== "false",
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
    if (stoppingWorkout.current || !originalCapture.current?.complete || pendingChunk.current) return;
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
      if (capture?.complete && capture.order > 0) {
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
            if (uploadedChunkCount.current === 0 || outstandingUploads.current > 0 || capture.failedUploads > 0) {
              console.warn("Server merge skipped because analysis inputs are incomplete");
              return;
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
    <View style={styles.container}>
      {/* Android: hint to mount phone sideways when landscape mode is on */}
      {IS_ANDROID && landscapeMode && !isRecording && (
        <View style={styles.landscapeHint}>
          <Text style={styles.landscapeHintText}>
            📱 Mount phone sideways for landscape view
          </Text>
        </View>
      )}
      <Camera
        ref={camera}
        style={StyleSheet.absoluteFill}
        device={device}
        isActive={isCameraActive}
        format={format}
        fps={targetFps}
        frameProcessor={frameProcessor}
        pixelFormat="yuv"
        // MoveNet's resize plugin requires uncompressed 8-bit frames, including at 4K.
        videoHdr={false}
        enableBufferCompression={false}
        video={true}
        audio={hasMicPermission}
        videoStabilizationMode={videoStabilizationMode}
        zoom={zoomMode ? 0.1 : 0}
        onInitialized={() => setIsCameraReady(true)}
        onError={(error) => {
          // Filter out harmless orphan-deletion warning (VisionCamera bug in v4.x)
          if (error.message?.includes("delete orphan")) {
            console.log("📷 Ignoring orphan cleanup warning");
            return;
          }
          console.error("📷 Camera Error:", error.code, error.message);
        }}
      />

      {/* Skeleton overlay: controlled by user toggle in setup page.
          Default OFF on Android (saves GPU/memory), ON on iOS. */}
      {showSkeleton && (
        <View style={StyleSheet.absoluteFill} pointerEvents="none">
          <SkeletonOverlay pose={poseResult} width={width} height={height} />
        </View>
      )}

      {/* Energy impact monitor — always visible for testing */}
      {!previewOnly && (
        <View
          style={[
            styles.energyMonitorContainer,
            applyLandscapeStyles && styles.energyMonitorLandscape,
          ]}
        >
          <EnergyMonitor
            label={
              isRecording ? "Default Model (7MB) · 2fps" : "Preview · 1fps"
            }
          />
        </View>
      )}

      {/* 닫기 버튼 */}
      {!isRecording && (
        <TouchableOpacity
          style={[
            styles.closeBtn,
            applyLandscapeStyles && styles.closeBtnLandscape,
          ]}
          onPress={() => router.back()}
        >
          <IconSymbol name="chevron.left" size={32} color="#fff" />
        </TouchableOpacity>
      )}

      {/* 심박수 패널 */}
      <View
        style={[
          styles.hrPanel,
          applyLandscapeStyles && styles.hrPanelLandscape,
        ]}
      >
        <Text style={styles.hrLabel}>{t("overlay.sensor.heartRate")}</Text>
        <View style={styles.hrValueContainer}>
          <Text
            style={[
              styles.hrValue,
              {
                color: bpm > 0 ? "#0f0" : "#ffbe72",
                ...(bpm > 0 ? {} : { fontSize: 13 }),
              },
            ]}
          >
            {bpm > 0
              ? bpm
              : t(
                  hrQuality === "missing" && !isRecording && hrStatus !== "Live"
                    ? "heartRate.waiting"
                    : "heartRate.unstable",
                )}
          </Text>
          {bpm > 0 && <Text style={styles.hrUnit}> BPM</Text>}
        </View>
        {hrQuality === "low" && (
          <Text style={styles.hrWarning}>{t("heartRate.low")}</Text>
        )}
        <Text style={styles.hrStatus}>
          {t("overlay.sensor.state", { status: hrStatus })}
        </Text>
        <Text style={styles.hrStatus}>
          {batteryLevel != null
            ? t("overlay.sensor.strapBattery", { level: batteryLevel })
            : t("overlay.sensor.strapBatteryUnknown")}
        </Text>
        {isRecording && (
          <Text style={styles.hrStatus}>
            {t("overlay.sensor.liveStatus", {
              accSamples: sensorLiveStatus.accSamples,
              dropped: sensorLiveStatus.dropped ?? "--",
            })}
          </Text>
        )}
      </View>

      <View
        style={[
          styles.dashboard,
          applyLandscapeStyles && styles.dashboardLandscape,
        ]}
      >
        <Text style={styles.dashTitle}>
          {isRecording
            ? isPaused
              ? `${workoutTypeLabel} PAUSED`
              : `${workoutTypeLabel} LIVE`
            : `${workoutTypeLabel} SETUP`}
        </Text>
        <View style={styles.row}>
          <Text style={styles.label}>TYPE:</Text>
          <Text style={styles.val}>{workoutTypeLabel}</Text>
        </View>
        {!isRecording && injuries.length > 0 && (
          <View style={styles.row}>
            <Text style={styles.label}>INJ:</Text>
            <Text style={styles.val}>{injuries.split(", ").length}</Text>
          </View>
        )}
        {!isRecording && (
          <View style={styles.row}>
            <Text style={styles.label}>RES:</Text>
            <Text style={styles.val}>
              {format?.videoWidth}x{format?.videoHeight}
            </Text>
          </View>
        )}

        {isRecording && (
          <>
            <View style={styles.row}>
              <Text style={styles.label}>CONF:</Text>
              <Text style={styles.val}>
                {(monitorData.confidence * 100).toFixed(0)}%
              </Text>
            </View>
            <View style={styles.row}>
              <Text style={styles.label}>MOTION:</Text>
              <Text style={styles.val}>{monitorData.motion.toFixed(3)}</Text>
            </View>
            <View style={styles.row}>
              <Text style={styles.label}>STATE:</Text>
              <Text style={styles.val}>
                {monitorData.isWorkingOut ? "ACTIVE" : "IDLE"}
              </Text>
            </View>
            <View
              style={{
                marginTop: 6,
                borderTopWidth: 1,
                borderTopColor: "#333",
                paddingTop: 4,
              }}
            >
              <Text style={[styles.label, { fontSize: 8, color: "#666" }]}>
                OPT FLAGS
              </Text>
              <Text
                style={{ color: "#555", fontSize: 9, fontFamily: "monospace" }}
              >
                {[
                  lowFps ? "24fps" : "30fps",
                  resolution,
                  skipCompression ? "raw" : "compress",
                  showSkeleton ? "skel" : "no-skel",
                  onDeviceAi ? `apple-ai:${appleAi.status}` : "apple-ai:off",
                  environmentEnabled ? `environment:${environment.status}:${environment.record?.questionId ?? "-"}:${environment.record?.outcome === "success" && environment.record?.source === "FoundationModels" ? "review_needed" : "-"}` : "environment:off",
                  serialUpload ? "serial" : "parallel",
                  t(continuousRecording ? "originalVideo.continuousMode" : "originalVideo.chunkMode"),
                  landscapeMode ? "land" : "port",
                  zoomMode ? "zoom:0.1" : "zoom:0",
                  aspectRatio,
                ].join(" · ")}
              </Text>
              <Text
                style={{
                  color: inflightUploads > 2 ? "#FF453A" : "#555",
                  fontSize: 9,
                  fontFamily: "monospace",
                  marginTop: 2,
                }}
              >
                UL: {inflightUploads} inflight · {pendingUploads} queued ·{" "}
                {chunkCount} chunks
              </Text>
            </View>
          </>
        )}

        {/* Pose detection metrics — visible during preview and recording */}
        {!previewOnly && (
          <>
            <View
              style={{
                marginTop: 4,
                borderTopWidth: 1,
                borderTopColor: "#333",
                paddingTop: 4,
              }}
            >
              <Text
                style={[
                  styles.label,
                  { fontSize: 8, color: "#666", marginBottom: 2 },
                ]}
              >
                POSE {isModelLoaded ? "✅" : "⏳ LOADING..."}
              </Text>
              <View style={styles.row}>
                <Text style={styles.label}>CONF:</Text>
                <Text style={styles.val}>
                  {(monitorData.confidence * 100).toFixed(0)}%
                </Text>
              </View>
              <View style={styles.row}>
                <Text style={styles.label}>MOTION:</Text>
                <Text style={styles.val}>{monitorData.motion.toFixed(3)}</Text>
              </View>
              <View style={styles.row}>
                <Text style={styles.label}>STATE:</Text>
                <Text
                  style={[
                    styles.val,
                    { color: monitorData.isWorkingOut ? "#30D158" : "#888" },
                  ]}
                >
                  {monitorData.isWorkingOut ? "ACTIVE" : "IDLE"}
                </Text>
              </View>
            </View>
          </>
        )}
      </View>

      {/* Chunk Feedback Overlay */}
      {isRecording && !previewOnly && (environmentEnabled || onDeviceAi || chunkFeedback || liveFeedback.captureAdvice || liveFeedback.summary?.available) && (
        <View
          style={[
            styles.feedbackOverlay,
            applyLandscapeStyles && styles.feedbackOverlayLandscape,
            onDeviceAi && { backgroundColor: "transparent", paddingHorizontal: 0, paddingVertical: 0 },
          ]}
        >
          {(chunkFeedback || liveFeedback.captureAdvice || liveFeedback.summary?.available) && (
            <View style={onDeviceAi ? { backgroundColor: "rgba(255, 0, 0, 0.8)", borderRadius: 8, padding: 12 } : undefined}>
              {liveFeedback.captureAdvice && <Text style={[styles.feedbackText, { color: "#ffd28a" }]}>{t("activity.camera")}: {liveFeedback.captureAdvice}</Text>}
              {chunkFeedback && <Text style={styles.feedbackText}>{chunkFeedback}</Text>}
              <ActivitySummaryContent summary={liveFeedback.summary} compact />
            </View>
          )}
          {environmentEnabled && <EnvironmentLiveCard status={environment.status} record={environment.record} />}
          {onDeviceAi && <AppleAiFeedbackCard status={appleAi.status} error={appleAi.error} result={appleAi.result} archiveError={appleAi.archiveError} />}
        </View>
      )}

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

      {/* Recording controls — compact pill bar */}
      {!previewOnly && !isMerging && (
        <View
          style={[
            styles.recordControl,
            applyLandscapeStyles && styles.recordControlLandscape,
          ]}
        >
          {isSaving ? (
            <View style={styles.postRecordingFooter}>
              <Text style={styles.footerStatus}>{t("originalVideos.status.preparing")}</Text>
              <View style={styles.footerProgressBg}>
                <View
                  style={[
                    styles.footerProgressFill,
                    { width: "100%", backgroundColor: "#30D158" },
                  ]}
                />
              </View>
            </View>
          ) : (
            /* Compact pill recording bar with Pause / Resume / Stop */
            <View style={styles.pillBar}>
              {!isRecording ? (
                <TouchableOpacity
                  onPress={handleStartRecording}
                  style={styles.pillRecordBtn}
                >
                  <View style={styles.pillRecordInner} />
                </TouchableOpacity>
              ) : (
                <>
                  {/* Pause / Resume Button */}
                  <TouchableOpacity
                    onPress={
                      isPaused ? handleResumeRecording : handlePauseRecording
                    }
                    style={[
                      styles.pillActionBtn,
                      isPaused ? styles.pillResumeBtn : styles.pillPauseBtn,
                    ]}
                  >
                    <IconSymbol
                      name={isPaused ? "play.fill" : "pause.fill"}
                      size={20}
                      color="#fff"
                    />
                  </TouchableOpacity>

                  <View style={styles.pillDivider} />

                  {/* Timer Display */}
                  <View style={styles.pillTimerContainer}>
                    <Text
                      style={[
                        styles.pillTimer,
                        isPaused && styles.pillTimerPaused,
                      ]}
                    >
                      {formatElapsed(elapsedMs)}
                    </Text>
                    {isPaused && (
                      <Text style={styles.pillPausedBadge}>PAUSED</Text>
                    )}
                  </View>

                  {!applyLandscapeStyles && (
                    <>
                      <View style={styles.pillDivider} />
                      <Text style={styles.pillChunks}>▌▌ {chunkCount}</Text>
                    </>
                  )}

                  <View style={styles.pillDivider} />

                  {/* Stop Button */}
                  <TouchableOpacity
                    onPress={handleStopRecording}
                    style={styles.pillStopBtn}
                  >
                    <IconSymbol name="square.fill" size={18} color="#FF453A" />
                  </TouchableOpacity>
                </>
              )}
            </View>
          )}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "black" },
  center: { flex: 1, justifyContent: "center", alignItems: "center" },
  closeBtn: {
    position: "absolute",
    top: 50,
    left: 10,
    zIndex: 30,
    padding: 10,
    backgroundColor: "rgba(0,0,0,0.5)",
    borderRadius: 25,
  },
  closeBtnLandscape: {
    top: 20,
    left: 20,
  },
  landscapeHint: {
    position: "absolute",
    bottom: "auto" as any,
    top: 50,
    alignSelf: "center",
    zIndex: 50,
    backgroundColor: "rgba(0,0,0,0.7)",
    paddingVertical: 8,
    paddingHorizontal: 16,
    borderRadius: 20,
    transform: [{ rotate: "-90deg" }],
  },
  landscapeHintText: {
    color: "#fff",
    fontSize: 13,
    fontWeight: "600",
  },
  dashboard: {
    position: "absolute",
    top: 50,
    left: 10,
    backgroundColor: "rgba(0,0,0,0.7)",
    padding: 8,
    borderRadius: 8,
    width: 140,
    borderWidth: 1,
    borderColor: "#555",
    zIndex: 10,
  },
  dashboardLandscape: {
    top: 10,
    left: "auto" as any,
    right: -30,
    transform: [{ rotate: "-90deg" }],
  },
  dashTitle: {
    color: "#fff",
    fontWeight: "bold",
    fontSize: 10,
    marginBottom: 4,
    textAlign: "center",
  },
  row: {
    flexDirection: "row",
    justifyContent: "space-between",
    marginVertical: 1,
  },
  label: {
    color: "#aaa",
    fontSize: 10,
    fontFamily: "monospace",
    fontWeight: "bold",
  },
  val: {
    color: "#fff",
    fontSize: 10,
    fontFamily: "monospace",
    fontWeight: "bold",
  },
  energyMonitorContainer: {
    position: "absolute",
    bottom: 120,
    left: 0,
    right: 0,
    zIndex: 10,
  },
  energyMonitorLandscape: {
    bottom: "auto" as any,
    top: "50%" as any,
    left: -40,
    right: "auto" as any,
    width: 280,
    transform: [{ rotate: "-90deg" }],
  },
  hrPanel: {
    position: "absolute",
    top: 50,
    right: 10,
    maxWidth: 280,
    backgroundColor: "rgba(0,0,0,0.7)",
    padding: 10,
    borderRadius: 8,
    alignItems: "flex-end",
    borderRightWidth: 3,
    borderColor: "#FF0000",
    zIndex: 10,
  },
  hrPanelLandscape: {
    top: 200,
    right: -10,
    transform: [{ rotate: "-90deg" }],
  },
  hrLabel: { color: "#FF0000", fontSize: 10, fontWeight: "900" },
  hrWarning: { color: "#ffbe72", fontSize: 11, marginTop: 2 },
  hrValue: {
    flexShrink: 1,
    fontSize: 32,
    fontWeight: "bold",
    fontFamily: "monospace",
  },
  hrUnit: { color: "#888", fontSize: 12, marginBottom: 5, fontWeight: "bold" },
  hrValueContainer: {
    flexDirection: "row",
    alignItems: "flex-end",
  },
  hrStatus: {
    color: "#aaa",
    fontSize: 9,
    marginTop: 2,
  },
  recordControl: {
    position: "absolute",
    bottom: 40,
    alignSelf: "center",
    alignItems: "center",
    zIndex: 20,
    width: "80%",
  },
  recordControlLandscape: {
    bottom: "auto" as any,
    right: "auto" as any,
    left: "auto" as any,
    top: "40%" as any,
    width: "auto" as any,
    alignSelf: "center" as any,
    transform: [{ rotate: "-90deg" }],
  },

  // --- Compact pill recording bar ---
  pillBar: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "rgba(0,0,0,0.75)",
    borderRadius: 28,
    paddingVertical: 6,
    paddingHorizontal: 10,
    borderWidth: 1,
    borderColor: "#333",
    gap: 0,
  },
  pillRecordBtn: {
    width: 48,
    height: 48,
    borderRadius: 24,
    borderWidth: 4,
    borderColor: "#fff",
    justifyContent: "center",
    alignItems: "center",
  },
  pillRecordInner: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: "#FF453A",
  },
  pillActionBtn: {
    width: 42,
    height: 42,
    borderRadius: 21,
    justifyContent: "center",
    alignItems: "center",
  },
  pillPauseBtn: {
    backgroundColor: "rgba(255, 255, 255, 0.2)",
  },
  pillResumeBtn: {
    backgroundColor: "#30D158",
  },
  pillStopBtn: {
    width: 42,
    height: 42,
    borderRadius: 21,
    backgroundColor: "rgba(255, 69, 58, 0.2)",
    justifyContent: "center",
    alignItems: "center",
    borderWidth: 1.5,
    borderColor: "#FF453A",
  },
  pillDivider: {
    width: 1,
    height: 24,
    backgroundColor: "#444",
    marginHorizontal: 8,
  },
  pillTimerContainer: {
    alignItems: "center",
    justifyContent: "center",
    minWidth: 60,
  },
  pillTimer: {
    color: "#fff",
    fontSize: 18,
    fontWeight: "700",
    fontFamily: "monospace",
    textAlign: "center",
  },
  pillTimerPaused: {
    color: "#FFD60A",
  },
  pillPausedBadge: {
    color: "#FFD60A",
    fontSize: 8,
    fontWeight: "800",
    letterSpacing: 1,
    marginTop: -2,
  },
  pillChunks: {
    color: "#888",
    fontSize: 13,
    fontFamily: "monospace",
    fontWeight: "600",
  },

  postRecordingFooter: {
    alignItems: "center",
    backgroundColor: "rgba(0, 0, 0, 0.85)",
    paddingVertical: 16,
    paddingHorizontal: 24,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: "#333",
    width: "100%",
    gap: 10,
  },
  footerStatus: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "700",
  },
  encodeBtn: {
    backgroundColor: "#fff",
    paddingVertical: 12,
    paddingHorizontal: 32,
    borderRadius: 10,
    width: "100%",
    alignItems: "center",
  },
  encodeBtnText: {
    color: "#000",
    fontSize: 16,
    fontWeight: "bold",
  },
  footerLinkBtn: {
    paddingVertical: 6,
  },
  footerLinkText: {
    color: "#888",
    fontSize: 14,
    textDecorationLine: "underline",
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
  footerPercent: {
    color: "#aaa",
    fontSize: 13,
    fontFamily: "monospace",
  },
  feedbackOverlay: {
    position: "absolute",
    top: "35%" as any,
    alignSelf: "center",
    backgroundColor: "rgba(255, 0, 0, 0.8)",
    paddingHorizontal: 20,
    paddingVertical: 12,
    borderRadius: 8,
    maxWidth: "80%",
    zIndex: 50,
  },
  feedbackOverlayLandscape: {
    top: "auto" as any,
    bottom: 80,
    maxWidth: "60%",
    transform: [{ rotate: "-90deg" }],
  },
  feedbackText: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "bold",
    textAlign: "center",
  },
  // --- Merging indicator overlay ---
  mergingOverlay: {
    ...StyleSheet.absoluteFill,
    backgroundColor: "rgba(0, 0, 0, 0.85)",
    justifyContent: "center",
    alignItems: "center",
    zIndex: 100,
  },
  mergingCard: {
    backgroundColor: "rgba(30, 30, 30, 0.95)",
    borderRadius: 20,
    paddingVertical: 32,
    paddingHorizontal: 40,
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#333",
    gap: 12,
    minWidth: 260,
  },
  mergingTitle: {
    color: "#fff",
    fontSize: 20,
    fontWeight: "700",
    marginTop: 8,
  },
  mergingSubtitle: {
    color: "#999",
    fontSize: 14,
    fontWeight: "500",
    textAlign: "center",
  },
  mergingProgressBg: {
    width: "100%",
    height: 4,
    backgroundColor: "#333",
    borderRadius: 2,
    overflow: "hidden",
    marginTop: 8,
  },
  mergingProgressIndeterminate: {
    width: "40%",
    height: "100%",
    backgroundColor: "#30D158",
    borderRadius: 2,
  },
});
