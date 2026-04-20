import * as MediaLibrary from "expo-media-library";
import { activateKeepAwakeAsync, deactivateKeepAwake } from "expo-keep-awake";
import React, { useEffect, useRef, useState } from "react";
import {
  Alert,
  Linking,
  Platform,
  StyleSheet,
  Switch,
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
import { router, useLocalSearchParams } from "expo-router";

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
import { useVideoQueue } from "@/store/useVideoQueue";
import { useProfileStore } from "@/store/useProfileStore";

const CHUNK_DURATION_MS = 10000; // 10 seconds
const IS_ANDROID = Platform.OS === 'android';

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
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
    force720p: force720pParam,
    skipCompression: skipCompressionParam,
    serialUpload: serialUploadParam,
  } = useLocalSearchParams<{
    resolution?: string;
    movements?: string;
    injuries?: string;
    workoutType?: string;
    autoRecord?: string;
    showSkeleton?: string;
    lowFps?: string;
    force720p?: string;
    skipCompression?: string;
    serialUpload?: string;
  }>();

  // Performance flags — default to power-saving on Android, full quality on iOS
  const showSkeleton = showSkeletonParam !== undefined
    ? showSkeletonParam === 'true'
    : !IS_ANDROID;
  const lowFps = lowFpsParam !== undefined
    ? lowFpsParam === 'true'
    : IS_ANDROID;
  const force720p = force720pParam !== undefined
    ? force720pParam === 'true'
    : IS_ANDROID;
  const skipCompression = skipCompressionParam !== undefined
    ? skipCompressionParam === 'true'
    : IS_ANDROID;
  const serialUpload = serialUploadParam !== undefined
    ? serialUploadParam === 'true'
    : IS_ANDROID;

  const workoutType = parseWorkoutType(workoutTypeParam);
  const workoutTypeLabel = formatWorkoutTypeLabel(workoutType).toUpperCase();
  
  // Resolution: honor force720p toggle
  const effectiveResolution = force720p ? '720p' : resolution;
  const targetWidth = effectiveResolution === '1080p' ? 1920 : 1280;
  const targetHeight = effectiveResolution === '1080p' ? 1080 : 720;

  // FPS: honor lowFps toggle
  const targetFps = lowFps ? 24 : 30;

  const device = useCameraDevice("back");
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
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
  const [enableChunks, setEnableChunks] = useState(true);
  const [chunkFeedback, setChunkFeedback] = useState<string | null>(null);
  const [currentItemId, setCurrentItemId] = useState<string | null>(null);
  const [isCameraReady, setIsCameraReady] = useState(false);
  const [chunkCount, setChunkCount] = useState(0);
  const [isMerging, setIsMerging] = useState(false);
  const [mergeComplete, setMergeComplete] = useState(false);
  // Android: track auto-merge-and-navigate state after stopping
  const [androidAutoMerging, setAndroidAutoMerging] = useState(false);

  const enqueue = useVideoQueue((s) => s.enqueue);
  const startEncoding = useVideoQueue((s) => s.startEncoding);
  const startUpload = useVideoQueue((s) => s.startUpload);
  const cancelUpload = useVideoQueue((s) => s.cancelUpload);
  const profileId = useProfileStore((s) => s.activeProfileId);
  const currentItem = useVideoQueue((s) =>
    currentItemId ? s.items.find((i) => i.id === currentItemId) ?? null : null
  );

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

  // Pass isRecording to the hook to toggle inference on/off
  const { frameProcessor, poseResult, monitorData } = usePoseDetection(isRecording);
  const { bpm, status: hrStatus } = useBleHeartRate();
  // const { bpm, status: hrStatus } = useHeartRate();

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
            console.log("⏹️ Recording stopped — skipping chunk upload");
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
                const uploadTask = () => doUpload(video.path, false);
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
                  const uploadTask = () => doUpload(compressedUri, true);
                  if (serialUpload) {
                    enqueueUpload(uploadTask);
                  } else {
                    trackUpload(uploadTask);
                  }
                }).catch((err) => {
                  console.error("Failed to compress chunk:", err);
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

        // Optionally start chunk recording for real-time backend analysis
        if (enableChunks) {
          startChunkRecording();
        }
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

      let file: { path: string } | undefined;

      if (Platform.OS === 'ios') {
        // 1. Stop Chunk Recording (safe to call even if not running)
        await stopChunkRecording();

        // 2. Stop Screen Recorder
        file = await stopInAppRecording();
      } else {
        // Android: stop chunk streaming and auto-merge on server
        await stopChunkRecording();
        setIsRecording(false);

        if (chunkCount > 0) {
          // Auto-trigger server-side merge + analysis
          setAndroidAutoMerging(true);
          try {
            const sessionId = sessionIdRef.current;
            const movementsArray = movements ? movements.split(", ") : [];
            const injuriesArray = injuries ? injuries.split(", ") : [];

            // Small delay to let the last chunk upload reach the server
            await new Promise(resolve => setTimeout(resolve, 2000));

            await mergeChunks(sessionId, {
              workoutType,
              movements: movementsArray,
              injuries: injuriesArray,
              profileId: profileId!,
            });

            console.log("✅ Auto-merge triggered for Android session");
          } catch (e) {
            console.error("❌ Auto-merge failed:", e);
            Alert.alert(
              "Merge Failed",
              "Could not start merge. You can try again from history.",
              [{ text: "OK" }]
            );
          } finally {
            setAndroidAutoMerging(false);
          }
        }

        // Clean up and navigate to history
        chunkPaths.current = [];
        setChunkCount(0);
        setIsSaving(false);
        router.replace("/history" as any);
        return;
      }

      setIsRecording(false);
      console.log("📼 Video Path:", file?.path);

      if (file?.path) {
        // Save to gallery (best-effort — retry available from queue)
        let gallerySaved = false;
        try {
          await MediaLibrary.saveToLibraryAsync(file.path);
          gallerySaved = true;
        } catch (gallerySaveErr) {
          console.warn("⚠️ Gallery save failed (low storage?):", gallerySaveErr);
          Alert.alert(
            "Gallery Save Failed",
            "Could not save to gallery (device storage may be full). " +
            "Your video is still queued — you can save to gallery later from the queue.",
            [{ text: "OK" }]
          );
        }

        // Rename raw file with _raw suffix for debugging
        let rawPath = file.path;
        try {
          const { File: FSFile } = require("expo-file-system");
          const dir = rawPath.substring(0, rawPath.lastIndexOf("/"));
          const ext = rawPath.substring(rawPath.lastIndexOf("."));
          const rawName = `recording_raw_${Date.now()}${ext}`;
          const destPath = `${dir}/${rawName}`;
          new FSFile(rawPath).move(new FSFile(destPath));
          rawPath = destPath;
          console.log("📝 Renamed raw file to:", rawPath);
        } catch (renameErr) {
          console.warn("⚠️ Could not rename raw file:", renameErr);
        }

        // Enqueue as RECORDED — user decides when to encode
        let sessionId = sessionIdRef.current;
        if (!sessionId) {
          console.warn("⚠️ sessionIdRef was empty at enqueue time — generating fallback");
          sessionId = buildWorkoutSessionId(workoutType);
          sessionIdRef.current = sessionId;
        }
        const movementsArray = movements ? movements.split(", ") : [];
        const injuriesArray = injuries ? injuries.split(", ") : [];

        const itemId = enqueue(rawPath, {
          sessionId,
          workoutType,
          movements: movementsArray,
          injuries: injuriesArray,
          profileId: profileId!,
        });

        // Update gallerySaved status on the enqueued item
        if (gallerySaved) {
          useVideoQueue.getState()._updateItem(itemId, { gallerySaved: true });
        }

        // Navigate to queue — user can encode/upload from there
        router.replace("/queue" as any);
      }

      // Clean up chunk paths for next session
      chunkPaths.current = [];
    } catch (error) {
      console.error("Recording Stop Error:", error);
      Alert.alert("Error", "Failed to save recording.");
    } finally {
      setIsSaving(false);
    }
  };

  const handleMergeChunks = async () => {
    if (chunkCount === 0) return;

    try {
      setIsMerging(true);
      const sessionId = sessionIdRef.current;
      const movementsArray = movements ? movements.split(", ") : [];
      const injuriesArray = injuries ? injuries.split(", ") : [];

      await mergeChunks(sessionId, {
        workoutType,
        movements: movementsArray,
        injuries: injuriesArray,
        profileId: profileId!,
      });

      setMergeComplete(true);
      Alert.alert(
        "Merge Started",
        "Your chunks are being merged and analyzed on the server. Check history for results.",
        [{ text: "OK" }]
      );
    } catch (e) {
      console.error("❌ Merge failed:", e);
      Alert.alert("Merge Failed", "Could not start chunk merge. Please try again.");
    } finally {
      setIsMerging(false);
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
      {isRecording && (
        <View style={styles.energyMonitorContainer}>
          <EnergyMonitor label="Default Model (7MB) · 2fps" />
        </View>
      )}

      {/* 닫기 버튼 */}
      {!isRecording && (
        <TouchableOpacity 
          style={styles.closeBtn} 
          onPress={() => router.back()}
        >
          <IconSymbol name="chevron.left" size={32} color="#fff" />
        </TouchableOpacity>
      )}

      {/* 심박수 패널 */}
      <View style={styles.hrPanel}>
        <Text style={styles.hrLabel}>HEART RATE</Text>
        <View style={styles.hrValueContainer}>
          <Text style={[styles.hrValue, { color: bpm > 0 ? "#0f0" : "#888" }]}>
            {bpm > 0 ? bpm : "--"}
          </Text>
          <Text style={styles.hrUnit}> BPM</Text>
        </View>
        <Text style={styles.hrStatus}>State: {hrStatus}</Text>
      </View>

      <View style={styles.dashboard}>
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
                  force720p ? '720p' : (effectiveResolution === '1080p' ? '1080p' : '720p'),
                  skipCompression ? 'raw' : 'compress',
                  showSkeleton ? 'skel' : 'no-skel',
                  serialUpload ? 'serial' : 'parallel',
                ].join(' · ')}
              </Text>
              <Text style={{ color: inflightUploads > 2 ? '#FF453A' : '#555', fontSize: 9, fontFamily: 'monospace', marginTop: 2 }}>
                UL: {inflightUploads} inflight · {pendingUploads} queued · {chunkCount} chunks
              </Text>
            </View>
          </>
        )}

        {!isRecording && !IS_ANDROID && (
          <View style={[styles.row, { marginTop: 10, alignItems: "center" }]}>
            <Text style={styles.label}>RAW VIDEO:</Text>
            <Switch
              value={enableChunks}
              onValueChange={setEnableChunks}
              trackColor={{ false: "#767577", true: "#81b0ff" }}
              thumbColor={enableChunks ? "#f5dd4b" : "#f4f3f4"}
              style={{ transform: [{ scaleX: 0.8 }, { scaleY: 0.8 }] }}
            />
          </View>
        )}

      </View>

      {/* Chunk Feedback Overlay */}
      {isRecording && chunkFeedback && (
        <View style={styles.feedbackOverlay}>
          <Text style={styles.feedbackText}>{chunkFeedback}</Text>
        </View>
      )}

      {/* Post-recording footer */}
      <View style={styles.recordControl}>
        {androidAutoMerging ? (
          <View style={styles.postRecordingFooter}>
            <Text style={styles.footerStatus}>🔗 Merging & Analyzing...</Text>
            <Text style={[styles.footerStatus, { color: '#888', fontSize: 13 }]}>
              {chunkCount} chunks uploaded. Redirecting to history...
            </Text>
            <View style={styles.footerProgressBg}>
              <View
                style={[styles.footerProgressFill, { width: "100%", backgroundColor: "#64D2FF" }]}
              />
            </View>
          </View>
        ) : isSaving ? (
          <View style={styles.postRecordingFooter}>
            <Text style={styles.footerStatus}>Saving...</Text>
            <View style={styles.footerProgressBg}>
              <View
                style={[styles.footerProgressFill, { width: "100%", backgroundColor: "#30D158" }]}
              />
            </View>
          </View>
        ) : currentItem ? (
          <View style={styles.postRecordingFooter}>
            {currentItem.status === "RECORDED" && (
              <>
                <Text style={styles.footerStatus}>Recording saved</Text>
                <TouchableOpacity
                  style={styles.encodeBtn}
                  onPress={() => startEncoding(currentItem.id)}
                >
                  <Text style={styles.encodeBtnText}>Encode Video</Text>
                </TouchableOpacity>
                {chunkCount > 0 && !mergeComplete && (
                  <TouchableOpacity
                    style={[styles.encodeBtn, { backgroundColor: "#64D2FF" }]}
                    onPress={handleMergeChunks}
                    disabled={isMerging}
                  >
                    <Text style={[styles.encodeBtnText, { color: "#000" }]}>
                      {isMerging ? "Merging..." : `🔗 Merge & Analyze ${chunkCount} Chunks`}
                    </Text>
                  </TouchableOpacity>
                )}
                {mergeComplete && (
                  <Text style={[styles.footerStatus, { color: "#64D2FF", fontSize: 13 }]}>
                    ✅ Merge queued on server
                  </Text>
                )}
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.back()}
                >
                  <Text style={styles.footerLinkText}>Skip for now</Text>
                </TouchableOpacity>
              </>
            )}

            {currentItem.status === "ENCODING" && (
              <>
                <Text style={styles.footerStatus}>Encoding...</Text>
                <View style={styles.footerProgressBg}>
                  <View
                    style={[
                      styles.footerProgressFill,
                      { width: `${Math.round(currentItem.progress * 100)}%` },
                    ]}
                  />
                </View>
                <Text style={styles.footerPercent}>
                  {Math.round(currentItem.progress * 100)}%
                </Text>
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.back()}
                >
                  <Text style={styles.footerLinkText}>Continue in background</Text>
                </TouchableOpacity>
              </>
            )}

            {currentItem.status === "ENCODED" && (
              <>
                <Text style={[styles.footerStatus, { color: "#30D158" }]}>
                  ✅ Ready to upload{currentItem.compressedSize ? ` (${formatBytes(currentItem.compressedSize)})` : ""}
                </Text>
                {currentItem.error && (
                  <Text style={[styles.footerStatus, { color: "#FF453A", fontSize: 12 }]}>
                    ⚠️ {currentItem.error}
                  </Text>
                )}
                <TouchableOpacity
                  style={styles.encodeBtn}
                  onPress={() => startUpload(currentItem.id)}
                >
                  <Text style={styles.encodeBtnText}>
                    Upload now{currentItem.compressedSize ? ` (${formatBytes(currentItem.compressedSize)})` : ""}
                  </Text>
                </TouchableOpacity>
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.push("/queue" as any)}
                >
                  <Text style={styles.footerLinkText}>Go to Queue</Text>
                </TouchableOpacity>
              </>
            )}

            {currentItem.status === "UPLOADING" && (
              <>
                <Text style={styles.footerStatus}>Uploading...</Text>
                <View style={styles.footerProgressBg}>
                  <View
                    style={[
                      styles.footerProgressFill,
                      {
                        width: `${Math.round(currentItem.progress * 100)}%`,
                        backgroundColor: "#64D2FF",
                      },
                    ]}
                  />
                </View>
                <Text style={styles.footerPercent}>
                  {Math.round(currentItem.progress * 100)}%
                </Text>
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => cancelUpload(currentItem.id)}
                >
                  <Text style={[styles.footerLinkText, { color: "#FF453A" }]}>Cancel Upload</Text>
                </TouchableOpacity>
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.back()}
                >
                  <Text style={styles.footerLinkText}>Continue in background</Text>
                </TouchableOpacity>
              </>
            )}

            {currentItem.status === "UPLOADED" && (
              <>
                <Text style={[styles.footerStatus, { color: "#30D158" }]}>
                  ✅ Upload complete!
                </Text>
                {chunkCount > 0 && !mergeComplete && (
                  <TouchableOpacity
                    style={styles.encodeBtn}
                    onPress={handleMergeChunks}
                    disabled={isMerging}
                  >
                    <Text style={styles.encodeBtnText}>
                      {isMerging ? "Merging..." : `🔗 Merge & Analyze ${chunkCount} Chunks`}
                    </Text>
                  </TouchableOpacity>
                )}
                {mergeComplete && (
                  <Text style={[styles.footerStatus, { color: "#64D2FF", fontSize: 13 }]}>
                    ✅ Merge queued on server
                  </Text>
                )}
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.back()}
                >
                  <Text style={styles.footerLinkText}>Done</Text>
                </TouchableOpacity>
              </>
            )}

            {/* Inline error display for RECORDED state (e.g. encoding failed) */}
            {currentItem.status === "RECORDED" && currentItem.error && (
              <Text style={[styles.footerStatus, { color: "#FF453A", fontSize: 12 }]}>
                ⚠️ {currentItem.error}
              </Text>
            )}
          </View>
        ) : (
          <TouchableOpacity
            onPress={isRecording ? handleStopRecording : handleStartRecording}
            style={[styles.recordBtn, isRecording && styles.recordingBtn]}
          >
            <View
              style={[styles.innerBtn, isRecording && styles.innerRecordingBtn]}
            />
          </TouchableOpacity>
        )}
      </View>
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
  dashboard: {
    position: "absolute",
    top: 110,
    left: 10,
    backgroundColor: "rgba(0,0,0,0.7)",
    padding: 10,
    borderRadius: 8,
    width: 150,
    borderWidth: 1,
    borderColor: "#555",
    zIndex: 10,
  },
  dashTitle: {
    color: "#fff",
    fontWeight: "bold",
    fontSize: 10,
    marginBottom: 5,
    textAlign: "center",
  },
  row: {
    flexDirection: "row",
    justifyContent: "space-between",
    marginVertical: 2,
  },
  label: {
    color: "#aaa",
    fontSize: 11,
    fontFamily: "monospace",
    fontWeight: "bold",
  },
  val: {
    color: "#fff",
    fontSize: 11,
    fontFamily: "monospace",
    fontWeight: "bold",
  },
  energyMonitorContainer: {
    position: 'absolute',
    bottom: 160,
    left: 0,
    right: 0,
    zIndex: 10,
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
    bottom: 50,
    alignSelf: "center",
    alignItems: "center",
    zIndex: 20,
    width: '80%', // Ensure width for progress bar
  },
  recordBtn: {
    width: 80,
    height: 80,
    borderRadius: 40,
    borderWidth: 6,
    borderColor: "white",
    justifyContent: "center",
    alignItems: "center",
  },
  recordingBtn: { borderColor: "red" },
  innerBtn: { width: 60, height: 60, borderRadius: 30, backgroundColor: "red" },
  innerRecordingBtn: { width: 30, height: 30, borderRadius: 6 },

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
    top: 150,
    alignSelf: "center",
    backgroundColor: "rgba(255, 0, 0, 0.8)",
    paddingHorizontal: 20,
    paddingVertical: 12,
    borderRadius: 8,
    maxWidth: "80%",
    zIndex: 50,
  },
  feedbackText: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "bold",
    textAlign: "center",
  },
});
