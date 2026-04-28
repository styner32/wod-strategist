import * as MediaLibrary from "expo-media-library";
import { activateKeepAwakeAsync, deactivateKeepAwake } from "expo-keep-awake";
import * as ScreenOrientation from "expo-screen-orientation";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Linking,
  Platform,
  StyleSheet,
  Text,
  TouchableOpacity,
  useWindowDimensions,
  View,
} from "react-native";
import {
  startInAppRecording,
  stopInAppRecording,
} from "react-native-nitro-screen-recorder";
import {
  Camera,
  useCameraDevice,
  useCameraFormat,
  useCameraPermission,
} from "react-native-vision-camera";
import { Video } from "react-native-compressor";
import { router, useLocalSearchParams, useFocusEffect } from "expo-router";

import { useBleHeartRate } from "@/features/health/useBleHeartRate";
import {
  buildWorkoutSessionId,
  formatWorkoutTypeLabel,
  parseWorkoutType,
} from "@/features/wod/workoutType";
import { usePoseDetection } from "../../features/ai-coach/frame-processors/usePoseDetection";
import { SkeletonOverlay } from "../../features/ai-coach/ui/SkeletonOverlay";
import { processWorkoutChunk, fetchChunkAnalysis, mergeChunks } from "../../features/wod/api";
import { IconSymbol } from "@/components/ui/icon-symbol";
import { EnergyMonitor } from '../../features/ai-coach/ui/EnergyMonitor';
import { TelemetryRecorder } from '../../features/debug/telemetryRecorder';
import { enqueueUpload as enqueueDebugUpload, flushPendingUploads } from '../../features/debug/telemetryUpload';

import { useProfileStore } from "@/store/useProfileStore";
import { useMergeStatus } from "@/store/useMergeStatus";

const CHUNK_DURATION_MS = 10000; // 10 seconds
const IS_ANDROID = Platform.OS === 'android';

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatElapsed(ms: number): string {
  const totalSecs = Math.floor(ms / 1000);
  const mins = Math.floor(totalSecs / 60);
  const secs = totalSecs % 60;
  return `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
}

export default function VisionTestPage() {
  const {
    resolution = "720p",
    movements = "",
    injuries = "",
    workoutType: workoutTypeParam,
    autoRecord,
    showSkeleton: showSkeletonParam,
    lowFps: lowFpsParam,

    skipCompression: skipCompressionParam,
    serialUpload: serialUploadParam,
    landscapeMode: landscapeModeParam,
    previewOnly: previewOnlyParam,
    zoomMode: zoomModeParam,
    aspectRatio: aspectRatioParam,
  } = useLocalSearchParams<{
    resolution?: string;
    movements?: string;
    injuries?: string;
    workoutType?: string;
    autoRecord?: string;
    showSkeleton?: string;
    lowFps?: string;
    skipCompression?: string;
    serialUpload?: string;
    landscapeMode?: string;
    previewOnly?: string;
    zoomMode?: string;
    aspectRatio?: string;
  }>();

  const landscapeMode = landscapeModeParam === 'true';
  const previewOnly = previewOnlyParam === 'true';
  const zoomMode = zoomModeParam === 'true';
  const aspectRatio = (aspectRatioParam === '4:3' ? '4:3' : '16:9') as '4:3' | '16:9';

  // Performance flags — default to power-saving on Android, full quality on iOS
  const showSkeleton = showSkeletonParam !== undefined
    ? showSkeletonParam === 'true'
    : !IS_ANDROID;
  const lowFps = lowFpsParam !== undefined
    ? lowFpsParam === 'true'
    : IS_ANDROID;
  const skipCompression = skipCompressionParam !== undefined
    ? skipCompressionParam === 'true'
    : IS_ANDROID;
  const serialUpload = serialUploadParam !== undefined
    ? serialUploadParam === 'true'
    : IS_ANDROID;

  const workoutType = parseWorkoutType(workoutTypeParam);
  const workoutTypeLabel = formatWorkoutTypeLabel(workoutType).toUpperCase();
  
  // Resolution: honor selected resolution and aspect ratio
  const is43 = aspectRatio === '4:3';
  const resMap: Record<string, { w16: number; w43: number; h: number }> = {
    '480p':  { w16: 854,  w43: 640,  h: 480 },
    '720p':  { w16: 1280, w43: 960,  h: 720 },
    '1080p': { w16: 1920, w43: 1440, h: 1080 },
    '2160p': { w16: 3840, w43: 2880, h: 2160 },
  };
  const res = resMap[resolution] || resMap['720p'];
  const targetWidth = is43 ? res.w43 : res.w16;
  const targetHeight = res.h;

  // FPS: honor lowFps toggle
  const targetFps = lowFps ? 24 : 30;

  const device = useCameraDevice("back");
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  const isLandscapeLayout = width > height;
  // On Android, landscape mode keeps portrait but user mounts phone sideways.
  // Apply landscape styles based on the toggle, not screen dimensions.
  const applyLandscapeStyles = isLandscapeLayout || (IS_ANDROID && landscapeMode);
  const camera = useRef<Camera>(null);

  // Use a ref to track if we should continue recording chunks,
  // preventing stale state in closures/timeouts.
  const isRecordingChunks = useRef(false);
  const chunkTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isChunkRecordingActive = useRef(false);



  // Store chunk paths locally (Android: used as final video source)
  const chunkPaths = useRef<string[]>([]);
  // Promise resolve for waiting on the last chunk's onRecordingFinished
  const lastChunkResolve = useRef<((path: string) => void) | null>(null);

  // Session ID computed once at recording start, reused for all chunks + merge
  const sessionIdRef = useRef<string>("");

  // Track recording session start time for chunk timing
  const recordingStartTime = useRef<number>(0);
  // Track individual chunk start time
  const chunkStartTime = useRef<number>(0);

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
      setInflightUploads(prev => prev + 1);
      try {
        await task();
      } catch (err) {
        console.error("Upload queue task failed:", err);
      }
      setInflightUploads(prev => Math.max(0, prev - 1));
    }
    isUploading.current = false;
  };

  const enqueueUpload = (task: () => Promise<void>) => {
    uploadQueue.current.push(task);
    setPendingUploads(uploadQueue.current.length);
    drainUploadQueue();
  };

  // Track fire-and-forget (concurrent) uploads
  const trackUpload = (task: () => Promise<void>) => {
    setInflightUploads(prev => prev + 1);
    task().finally(() => setInflightUploads(prev => Math.max(0, prev - 1)));
  };

  // 720p or 1080p format based on user/platform selection
  const format = useCameraFormat(device, [
    { videoResolution: { width: targetWidth, height: targetHeight } },
    { fps: targetFps },
  ]);

  const [mediaPermission, requestMediaPermission] =
    MediaLibrary.usePermissions();

  const [isRecording, setIsRecording] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [chunkFeedback, setChunkFeedback] = useState<string | null>(null);
  const [isCameraReady, setIsCameraReady] = useState(false);
  const [chunkCount, setChunkCount] = useState(0);

  // Elapsed timer for recording
  const [elapsedMs, setElapsedMs] = useState(0);

  const profileId = useProfileStore((s) => s.activeProfileId);

  // Poll for chunk feedback while recording
  useEffect(() => {
    let interval: ReturnType<typeof setInterval>;
    if (isRecording) {
      interval = setInterval(async () => {
        try {
          const sessionId = sessionIdRef.current;
          const results = await fetchChunkAnalysis(sessionId);
          if (results.length > 0) {
            const latest = results.find(r => r.status === 'COMPLETED');
            if (latest && latest.output) {
               setChunkFeedback(latest.output);
            }
          }
        } catch (e) {
            // Error fetching feedback, ignore to not clutter logs
        }
      }, 3000);
    }
    return () => clearInterval(interval);
  }, [isRecording, workoutType]);

  // Keep screen awake while recording (prevents Android/iOS sleep)
  useEffect(() => {
    if (isRecording) {
      void activateKeepAwakeAsync('recording');
    } else {
      deactivateKeepAwake('recording');
    }
    return () => {
      deactivateKeepAwake('recording');
    };
  }, [isRecording]);

  // Elapsed timer — ticks every second during recording
  useEffect(() => {
    if (!isRecording) {
      setElapsedMs(0);
      return;
    }
    const tick = setInterval(() => {
      setElapsedMs(Date.now() - recordingStartTime.current);
    }, 1000);
    return () => clearInterval(tick);
  }, [isRecording]);

  // Orientation lock: landscape mode from setup page
  // iOS: lock to landscape (works perfectly with AVCaptureSession)
  // Android: keep portrait — CameraX breaks when Activity rotates via configChanges.
  //   Instead, the user mounts their phone sideways. The camera sensor is physically
  //   landscape, so content is captured wide. UI shows a mounting hint.
  useFocusEffect(
    useCallback(() => {
      if (landscapeMode && !IS_ANDROID) {
        ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.LANDSCAPE);
      }
      return () => {
        if (!IS_ANDROID) {
          ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP);
        }
      };
    }, [landscapeMode])
  );

  // Pass isRecording to the hook to toggle inference on/off
  const { frameProcessor, poseResult, monitorData } = usePoseDetection(isRecording);
  const { bpm, status: hrStatus } = useBleHeartRate();
  // const { bpm, status: hrStatus } = useHeartRate();

  // Refs that mirror render-state for sampling outside the render cycle.
  // TelemetryRecorder polls these at 1Hz via registered providers.
  const bpmRef = useRef(0);
  const chunkCountRef = useRef(0);
  useEffect(() => { bpmRef.current = bpm; }, [bpm]);
  useEffect(() => { chunkCountRef.current = chunkCount; }, [chunkCount]);

  useEffect(() => {
    if (!hasPermission) requestPermission();
    if (!mediaPermission?.granted) requestMediaPermission();
  }, [hasPermission, mediaPermission, requestMediaPermission, requestPermission]);

  // Auto-start recording when navigated from setup with autoRecord
  const hasAutoStarted = useRef(false);
  useEffect(() => {
    if (
      autoRecord === 'true' &&
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

  const startChunkLoop = async () => {
    if (!camera.current || !isRecordingChunks.current) return;

    try {
      console.log("📷 Starting new chunk recording...");
      isChunkRecordingActive.current = true;
      chunkStartTime.current = Date.now();
      camera.current.startRecording({
        // Android: force mp4 + HEVC to reduce chunk size (12MB → ~2-4MB).
        // iOS: use VisionCamera defaults (chunks are already small via screen recorder).
        ...(IS_ANDROID ? { fileType: 'mp4' as const, videoCodec: 'h265' as const } : {}),
        onRecordingFinished: async (video) => {
          console.log("📷 Chunk Finished:", video.path);
          isChunkRecordingActive.current = false;

          // Compute chunk timing relative to recording start
          const chunkEndTime = Date.now();
          const startSecs = (chunkStartTime.current - recordingStartTime.current) / 1000;
          const endSecs = (chunkEndTime - recordingStartTime.current) / 1000;

          // Keep local copy for Android final video assembly
          chunkPaths.current.push(video.path);
          setChunkCount(prev => prev + 1);

          // Resolve the last-chunk promise if we're waiting for it
          if (lastChunkResolve.current) {
            lastChunkResolve.current(video.path);
            lastChunkResolve.current = null;
          }



          // Skip upload if recording has already been stopped
          if (!isRecordingChunks.current) {
            console.log("⏹️ Recording stopped — cleaning up orphan chunk");
            // Clean up the raw chunk file since we won't upload it
            try {
              const { File: FSFile } = require("expo-file-system");
              const f = new FSFile(video.path);
              if (f.exists) f.delete();
              console.log("🗑️ Deleted orphan chunk:", video.path);
            } catch (_) {}
          } else {
            // Compress (iOS only) and Upload chunk to backend
            try {
              const sessionId = sessionIdRef.current;
              const movementsArray = movements ? movements.split(', ') : [];
              const injuriesArray = injuries ? injuries.split(', ') : [];

              const doUpload = async (uri: string, shouldCleanup: boolean) => {
                try {
                  await processWorkoutChunk(uri, sessionId, {
                    movements: movementsArray,
                    injuries: injuriesArray,
                    workoutType,
                    profileId: profileId!,
                    startSecs,
                    endSecs,
                    heartRateBpm: bpm > 0 ? bpm : undefined,
                  });
                  console.log("✅ Chunk uploaded to backend");
                } catch (err) {
                  console.error("Failed to upload chunk:", err);
                } finally {
                  if (shouldCleanup) {
                    try {
                      const { File: FSFile } = require("expo-file-system");
                      const f = new FSFile(uri);
                      if (f.exists) f.delete();
                    } catch (_) {}
                  }
                }
              };

              if (skipCompression) {
                // Skip re-compression — upload raw chunk directly.
                // shouldCleanup=true: delete the raw file after upload completes
                const uploadTask = () => doUpload(video.path, true);
                if (serialUpload) {
                  enqueueUpload(uploadTask);
                } else {
                  trackUpload(uploadTask);
                }
              } else {
                // Compress before upload, then dispatch
                Video.compress(video.path, {
                  compressionMethod: "auto",
                  maxSize: 720,
                }).then((compressedUri) => {
                  // Delete the raw chunk now that compression produced a new file
                  const isSamePath =
                    compressedUri === video.path ||
                    compressedUri.replace("file://", "") === video.path.replace("file://", "");
                  if (!isSamePath) {
                    try {
                      const { File: FSFile } = require("expo-file-system");
                      const f = new FSFile(video.path);
                      if (f.exists) f.delete();
                      console.log("🗑️ Deleted raw chunk after compression:", video.path);
                    } catch (_) {}
                  }

                  const uploadTask = () => doUpload(compressedUri, true);
                  if (serialUpload) {
                    enqueueUpload(uploadTask);
                  } else {
                    trackUpload(uploadTask);
                  }
                }).catch((err) => {
                  console.error("Failed to compress chunk:", err);
                  // Compression failed — clean up raw chunk to prevent leak
                  try {
                    const { File: FSFile } = require("expo-file-system");
                    const f = new FSFile(video.path);
                    if (f.exists) f.delete();
                  } catch (_) {}
                });
              }
            } catch (e) {
              console.error("Failed to process chunk for upload:", e);
            }
          }

          // If still recording, start the next chunk after a short delay.
          // The camera HAL (especially Samsung) needs time to finalize the
          // previous recording before accepting a new startRecording() call.
          // In release builds (no debug overhead), calling immediately causes
          // a native crash (CameraDeviceClient BUFFER_ERROR / DEVICE_ERROR).
          if (isRecordingChunks.current) {
            if (IS_ANDROID) {
              // Android: 500ms cooldown for camera HAL to finalize.
              setTimeout(() => startChunkLoop(), 500);
            } else {
              startChunkLoop();
            }
          }
        },
        onRecordingError: (error) => {
          isChunkRecordingActive.current = false;
          console.error("📷 Chunk Recording Error:", error);
        },
      });

      // Schedule stop
      chunkTimer.current = setTimeout(async () => {
        if (
          isRecordingChunks.current &&
          isChunkRecordingActive.current &&
          camera.current
        ) {
          try {
            isChunkRecordingActive.current = false;
            await camera.current.stopRecording();
          } catch (e) {
            console.error("Failed to stop chunk recording:", e);
          }
        }
      }, CHUNK_DURATION_MS);
    } catch (e) {
      isChunkRecordingActive.current = false;
      console.error("Failed to start chunk recording:", e);
    }
  };

  const startChunkRecording = () => {
    isRecordingChunks.current = true;
    startChunkLoop();
  };

  const stopChunkRecording = async () => {
    isRecordingChunks.current = false;
    if (chunkTimer.current) {
      clearTimeout(chunkTimer.current);
      chunkTimer.current = null;
    }

    // Stop the current recording if active.
    // This will trigger onRecordingFinished, which checks isRecordingChunks.current (false), so loop stops.
    if (camera.current && isChunkRecordingActive.current) {
      try {
        isChunkRecordingActive.current = false;
        await camera.current.stopRecording();
      } catch (e) {
        console.error("Failed to stop chunk recording:", e);
      }
    }
  };

  /**
   * Delete all local chunk files and clear the paths array.
   * Called on recording stop to prevent tmp/ from growing unbounded.
   */
  const cleanupChunkFiles = () => {
    const paths = chunkPaths.current;
    if (paths.length === 0) {
      chunkPaths.current = [];
      return;
    }

    console.log(`🗑️ Cleaning up ${paths.length} chunk files from tmp/`);
    try {
      const { File: FSFile } = require("expo-file-system");
      for (const p of paths) {
        try {
          const f = new FSFile(p);
          if (f.exists) {
            f.delete();
            console.log("🗑️ Deleted chunk:", p);
          }
        } catch (_) {}
      }
    } catch (e) {
      console.warn("⚠️ Chunk cleanup error:", e);
    }
    chunkPaths.current = [];
  };

  // --- Main Recording Logic ---

  const handleStartRecording = async () => {
    if (!profileId) {
      Alert.alert(
        "Profile Required",
        "Please select a profile before recording.",
        [{ text: "OK", onPress: () => router.push("/profiles" as any) }]
      );
      return;
    }

    try {
      if (Platform.OS === 'ios') {
        // iOS: use screen recorder for full video with overlays
        await startInAppRecording({
          options: {
            enableMic: false,
            enableCamera: false,
          },
          onRecordingFinished: (file) => {
            console.log("📼 Screen Recording Finished:", file.path);
          },
        });

        setIsRecording(true);
        console.log("✅ Recording Started (iOS Screen Recorder)");

        // Compute session ID once for the entire recording session
        sessionIdRef.current = buildWorkoutSessionId(workoutType);
        recordingStartTime.current = Date.now();

        // Start debug telemetry recording (1Hz sampling)
        TelemetryRecorder.start(sessionIdRef.current, profileId!);
        TelemetryRecorder.registerProvider('hr', () => ({ hr: bpmRef.current }));
        TelemetryRecorder.registerProvider('chunk', () => ({ chunkIdx: chunkCountRef.current }));

        // Start chunk recording for real-time backend analysis
        startChunkRecording();
      } else {
        // Android: chunk-only streaming mode.
        // Android camera only supports one recording at a time, so we can't
        // run a continuous recording alongside chunk recording.
        // Instead, we record sequential 10s chunks, upload each for real-time
        // analysis, and auto-merge on the server when the user stops.
        // Trade-off: no local gallery save of the final video.
        if (!camera.current) return;

        setIsRecording(true);
        console.log("✅ Recording Started (Android Chunk Streaming)");

        // Compute session ID once for the entire recording session
        sessionIdRef.current = buildWorkoutSessionId(workoutType);
        recordingStartTime.current = Date.now();

        // Start debug telemetry recording (1Hz sampling)
        TelemetryRecorder.start(sessionIdRef.current, profileId!);
        TelemetryRecorder.registerProvider('hr', () => ({ hr: bpmRef.current }));
        TelemetryRecorder.registerProvider('chunk', () => ({ chunkIdx: chunkCountRef.current }));

        // Start chunk recording loop (same mechanism as iOS chunks)
        startChunkRecording();
      }
    } catch (error) {
      console.error("Recording Start Error:", error);
      Alert.alert("녹화 시작 실패", "녹화를 시작할 수 없습니다.");
    }
  };

  const handleStopRecording = async () => {
    if (!isRecording) return;

    try {
      setIsSaving(true);

      let screenRecorderFile: { path: string } | undefined;

      if (Platform.OS === 'ios') {
        // 1. Stop Chunk Recording (safe to call even if not running)
        await stopChunkRecording();

        // 2. Stop Screen Recorder
        screenRecorderFile = await stopInAppRecording();
      } else {
        // Android: stop chunk streaming
        await stopChunkRecording();
      }

      setIsRecording(false);

      // Stop debug telemetry and enqueue upload
      try {
        const telemetryResult = await TelemetryRecorder.stop();
        if (telemetryResult) {
          await enqueueDebugUpload(telemetryResult.sessionId, telemetryResult.filePath);
          flushPendingUploads().catch(() => {}); // fire and forget
        }
      } catch (e) {
        console.warn('telemetry stop failed', e);
      }

      // Fire-and-forget: trigger server-side merge in the background.
      // The merge API just enqueues a task — no reason to block the user
      // on the recording view while it completes.
      // We call it inline (not in setTimeout) so the network request starts
      // before the user can background the app.
      if (chunkCount > 0) {
        const sessionId = sessionIdRef.current;
        const movementsArray = movements ? movements.split(", ") : [];
        const injuriesArray = injuries ? injuries.split(", ") : [];

        // Track the merge globally so History page can show a banner
        useMergeStatus.getState().addPending(sessionId);

        // Fire the merge — the 2s delay lets the last chunk upload reach GCS
        (async () => {
          // Small delay to let the last chunk upload reach the server
          await new Promise(resolve => setTimeout(resolve, 2000));
          try {
            await mergeChunks(sessionId, {
              workoutType,
              movements: movementsArray,
              injuries: injuriesArray,
              profileId: profileId!,
            });
            console.log(`✅ Auto-merge triggered for ${Platform.OS} session`);
          } catch (e) {
            console.error("❌ Auto-merge failed:", e);
          } finally {
            useMergeStatus.getState().removePending(sessionId);
          }
        })();  // IIFE — fires immediately, does not block
      }

      // Clean up local chunk files and navigate away immediately
      cleanupChunkFiles();
      setChunkCount(0);
      setIsSaving(false);

      // iOS only: prompt to save screen recording to gallery BEFORE navigating.
      // Alert must fire while this component is still mounted — navigating first
      // unmounts the component, which can silently swallow the Alert on long sessions.
      if (Platform.OS === 'ios' && screenRecorderFile?.path) {
        const filePath = screenRecorderFile.path;
        Alert.alert(
          "갤러리에 저장하시겠습니까?",
          "운동 영상이 이미 업로드되었습니다. 기기 갤러리에도 저장하시겠습니까?",
          [
            {
              text: "아니오",
              style: "cancel",
              onPress: () => {
                try {
                  const { File: FSFile } = require("expo-file-system");
                  const f = new FSFile(filePath);
                  if (f.exists) f.delete();
                  console.log("🗑️ Deleted screen recorder temp file");
                } catch (_) {}
                router.replace("/history" as any);
              },
            },
            {
              text: "저장",
              onPress: () => {
                MediaLibrary.saveToLibraryAsync(filePath)
                  .then(() => console.log("📱 Saved screen recording to gallery"))
                  .catch((e) => console.warn("⚠️ Gallery save failed:", e))
                  .finally(() => {
                    try {
                      const { File: FSFile } = require("expo-file-system");
                      const f = new FSFile(filePath);
                      if (f.exists) f.delete();
                      console.log("🗑️ Cleaned up screen recorder temp file");
                    } catch (_) {}
                    router.replace("/history" as any);
                  });
              },
            },
          ]
        );
      } else {
        // Android or no screen recorder file — navigate immediately
        router.replace("/history" as any);
      }
    } catch (error) {
      console.error("Recording Stop Error:", error);
      Alert.alert("Error", "Failed to save recording.");
    } finally {
      setIsSaving(false);
    }
  };

  if (!hasPermission) {
    return (
      <View style={styles.center}>
        <Text style={{ color: "#fff", fontSize: 18, marginBottom: 16 }}>
          Camera Permission Required
        </Text>
        <TouchableOpacity
          onPress={async () => {
            const result = await requestPermission();
            if (!result) {
              // Permission permanently denied — direct to settings
              Alert.alert(
                "Permission Denied",
                "Camera permission was permanently denied. Please enable it in Settings.",
                [
                  { text: "Cancel", style: "cancel" },
                  { text: "Open Settings", onPress: () => Linking.openSettings() },
                ]
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
            Grant Camera Access
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
          <Text style={styles.landscapeHintText}>📱 Mount phone sideways for landscape view</Text>
        </View>
      )}
      <Camera
        ref={camera}
        style={StyleSheet.absoluteFill}
        device={device}
        isActive={true}
        format={format}
        fps={targetFps}
        frameProcessor={frameProcessor}
        pixelFormat="yuv"
        video={true}
        audio={false}
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

      {/* Energy impact monitor — compare with poseTestPage (heavy model at 15fps) */}
      {isRecording && !previewOnly && (
        <View style={[styles.energyMonitorContainer, applyLandscapeStyles && styles.energyMonitorLandscape]}>
          <EnergyMonitor label="Default Model (7MB) · 2fps" />
        </View>
      )}

      {/* 닫기 버튼 */}
      {!isRecording && (
        <TouchableOpacity 
          style={[styles.closeBtn, applyLandscapeStyles && styles.closeBtnLandscape]} 
          onPress={() => router.back()}
        >
          <IconSymbol name="chevron.left" size={32} color="#fff" />
        </TouchableOpacity>
      )}

      {/* 심박수 패널 */}
      <View style={[styles.hrPanel, applyLandscapeStyles && styles.hrPanelLandscape]}>
        <Text style={styles.hrLabel}>HEART RATE</Text>
        <View style={styles.hrValueContainer}>
          <Text style={[styles.hrValue, { color: bpm > 0 ? "#0f0" : "#888" }]}>
            {bpm > 0 ? bpm : "--"}
          </Text>
          <Text style={styles.hrUnit}> BPM</Text>
        </View>
        <Text style={styles.hrStatus}>State: {hrStatus}</Text>
      </View>

      <View style={[styles.dashboard, applyLandscapeStyles && styles.dashboardLandscape]}>
          <Text style={styles.dashTitle}>
            {isRecording ? `${workoutTypeLabel} LIVE` : `${workoutTypeLabel} SETUP`}
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
              <View style={{ marginTop: 6, borderTopWidth: 1, borderTopColor: '#333', paddingTop: 4 }}>
                <Text style={[styles.label, { fontSize: 8, color: '#666' }]}>OPT FLAGS</Text>
                <Text style={{ color: '#555', fontSize: 9, fontFamily: 'monospace' }}>
                  {[
                    lowFps ? '24fps' : '30fps',
                    resolution,
                    skipCompression ? 'raw' : 'compress',
                    showSkeleton ? 'skel' : 'no-skel',
                    serialUpload ? 'serial' : 'parallel',
                    landscapeMode ? 'land' : 'port',
                    zoomMode ? 'zoom:0.1' : 'zoom:0',
                    aspectRatio,
                  ].join(' · ')}
                </Text>
                <Text style={{ color: inflightUploads > 2 ? '#FF453A' : '#555', fontSize: 9, fontFamily: 'monospace', marginTop: 2 }}>
                  UL: {inflightUploads} inflight · {pendingUploads} queued · {chunkCount} chunks
                </Text>
              </View>
            </>
          )}



        </View>

      {/* Chunk Feedback Overlay */}
      {isRecording && !previewOnly && chunkFeedback && (
        <View style={[styles.feedbackOverlay, applyLandscapeStyles && styles.feedbackOverlayLandscape]}>
          <Text style={styles.feedbackText}>{chunkFeedback}</Text>
        </View>
      )}

      {/* Recording controls — compact pill bar */}
      {!previewOnly && (
        <View style={[styles.recordControl, applyLandscapeStyles && styles.recordControlLandscape]}>
        {isSaving ? (
          <View style={styles.postRecordingFooter}>
            <Text style={styles.footerStatus}>Saving...</Text>
            <View style={styles.footerProgressBg}>
              <View
                style={[styles.footerProgressFill, { width: "100%", backgroundColor: "#30D158" }]}
              />
            </View>
          </View>
        ) : (
          /* Compact pill recording bar */
          <View style={styles.pillBar}>
            <TouchableOpacity
              onPress={isRecording ? handleStopRecording : handleStartRecording}
              style={[styles.pillRecordBtn, isRecording && styles.pillRecordBtnActive]}
            >
              <View
                style={[styles.pillRecordInner, isRecording && styles.pillRecordInnerActive]}
              />
            </TouchableOpacity>
            {isRecording && !applyLandscapeStyles && (
              <>
                <View style={styles.pillDivider} />
                <Text style={styles.pillTimer}>{formatElapsed(elapsedMs)}</Text>
                <View style={styles.pillDivider} />
                <Text style={styles.pillChunks}>▌▌ {chunkCount}</Text>
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
    position: 'absolute',
    bottom: 'auto' as any,
    top: 50,
    alignSelf: 'center',
    zIndex: 50,
    backgroundColor: 'rgba(0,0,0,0.7)',
    paddingVertical: 8,
    paddingHorizontal: 16,
    borderRadius: 20,
    transform: [{ rotate: '-90deg' }],
  },
  landscapeHintText: {
    color: '#fff',
    fontSize: 13,
    fontWeight: '600',
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
    left: 'auto' as any,
    right: -30,
    transform: [{ rotate: '-90deg' }],
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
    position: 'absolute',
    bottom: 120,
    left: 0,
    right: 0,
    zIndex: 10,
  },
  energyMonitorLandscape: {
    bottom: 'auto' as any,
    top: '50%' as any,
    left: -40,
    right: 'auto' as any,
    width: 280,
    transform: [{ rotate: '-90deg' }],
  },
  hrPanel: {
    position: "absolute",
    top: 50,
    right: 10,
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
    transform: [{ rotate: '-90deg' }],
  },
  hrLabel: { color: "#FF0000", fontSize: 10, fontWeight: "900" },
  hrValue: { fontSize: 32, fontWeight: "bold", fontFamily: "monospace" },
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
    width: '80%',
  },
  recordControlLandscape: {
    bottom: 'auto' as any,
    right: 'auto' as any,
    left: 'auto' as any,
    top: '40%' as any,
    width: 'auto' as any,
    alignSelf: 'center' as any,
    transform: [{ rotate: '-90deg' }],
  },

  // --- Compact pill recording bar ---
  pillBar: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: 'rgba(0,0,0,0.75)',
    borderRadius: 28,
    paddingVertical: 6,
    paddingHorizontal: 10,
    borderWidth: 1,
    borderColor: '#333',
    gap: 0,
  },
  pillRecordBtn: {
    width: 48,
    height: 48,
    borderRadius: 24,
    borderWidth: 4,
    borderColor: '#fff',
    justifyContent: 'center',
    alignItems: 'center',
  },
  pillRecordBtnActive: {
    borderColor: '#FF453A',
  },
  pillRecordInner: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: '#FF453A',
  },
  pillRecordInnerActive: {
    width: 20,
    height: 20,
    borderRadius: 4,
  },
  pillDivider: {
    width: 1,
    height: 24,
    backgroundColor: '#444',
    marginHorizontal: 10,
  },
  pillTimer: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '700',
    fontFamily: 'monospace',
    minWidth: 52,
    textAlign: 'center',
  },
  pillChunks: {
    color: '#888',
    fontSize: 13,
    fontFamily: 'monospace',
    fontWeight: '600',
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
    top: '35%' as any,
    alignSelf: "center",
    backgroundColor: "rgba(255, 0, 0, 0.8)",
    paddingHorizontal: 20,
    paddingVertical: 12,
    borderRadius: 8,
    maxWidth: "80%",
    zIndex: 50,
  },
  feedbackOverlayLandscape: {
    top: 'auto' as any,
    bottom: 80,
    maxWidth: '60%',
    transform: [{ rotate: '-90deg' }],
  },
  feedbackText: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "bold",
    textAlign: "center",
  },
});
