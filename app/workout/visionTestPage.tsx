import * as MediaLibrary from "expo-media-library";
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
import { processWorkoutChunk, fetchChunkAnalysis } from "../../features/wod/api";
import { IconSymbol } from "@/components/ui/icon-symbol";
import { useVideoQueue } from "@/store/useVideoQueue";
import { useProfileStore } from "@/store/useProfileStore";

const CHUNK_DURATION_MS = 10000; // 10 seconds

export default function VisionTestPage() {
  const {
    resolution = "720p",
    movements = "",
    injuries = "",
    workoutType: workoutTypeParam,
    autoRecord,
  } = useLocalSearchParams<{
    resolution?: string;
    movements?: string;
    injuries?: string;
    workoutType?: string;
    autoRecord?: string;
  }>();
  const workoutType = parseWorkoutType(workoutTypeParam);
  const workoutTypeLabel = formatWorkoutTypeLabel(workoutType).toUpperCase();
  
  const targetWidth = resolution === '1080p' ? 1920 : 1280;
  const targetHeight = resolution === '1080p' ? 1080 : 720;

  const device = useCameraDevice("back");
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  const camera = useRef<Camera>(null);

  // Use a ref to track if we should continue recording chunks,
  // preventing stale state in closures/timeouts.
  const isRecordingChunks = useRef(false);
  const chunkTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isChunkRecordingActive = useRef(false);

  // Android: resolve function for the continuous camera recording promise
  const androidRecordingResolve = useRef<((video: { path: string }) => void) | null>(null);

  // Store chunk paths locally (Android: used as final video source)
  const chunkPaths = useRef<string[]>([]);
  // Promise resolve for waiting on the last chunk's onRecordingFinished
  const lastChunkResolve = useRef<((path: string) => void) | null>(null);

  // 720p or 1080p format based on user selection
  const format = useCameraFormat(device, [
    { videoResolution: { width: targetWidth, height: targetHeight } },
    { fps: 30 },
  ]);

  const [mediaPermission, requestMediaPermission] =
    MediaLibrary.usePermissions();

  const [isRecording, setIsRecording] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [enableChunks, setEnableChunks] = useState(true);
  const [chunkFeedback, setChunkFeedback] = useState<string | null>(null);
  const [currentItemId, setCurrentItemId] = useState<string | null>(null);
  const [isCameraReady, setIsCameraReady] = useState(false);

  const enqueue = useVideoQueue((s) => s.enqueue);
  const startEncoding = useVideoQueue((s) => s.startEncoding);
  const startUpload = useVideoQueue((s) => s.startUpload);
  const profileId = useProfileStore((s) => s.backendId);
  const currentItem = useVideoQueue((s) =>
    currentItemId ? s.items.find((i) => i.id === currentItemId) ?? null : null
  );

  // Poll for chunk feedback while recording
  useEffect(() => {
    let interval: ReturnType<typeof setInterval>;
    if (isRecording) {
      interval = setInterval(async () => {
        try {
          const sessionId = buildWorkoutSessionId(workoutType);
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
      camera.current.startRecording({
        onRecordingFinished: async (video) => {
          console.log("📷 Chunk Finished:", video.path);
          isChunkRecordingActive.current = false;

          // Keep local copy for Android final video assembly
          chunkPaths.current.push(video.path);

          // Resolve the last-chunk promise if we're waiting for it
          if (lastChunkResolve.current) {
            lastChunkResolve.current(video.path);
            lastChunkResolve.current = null;
          }

          // Android: save each chunk to gallery immediately (all footage preserved as segments)
          if (Platform.OS !== 'ios') {
            try {
              await MediaLibrary.saveToLibraryAsync(video.path);
              console.log("📱 Chunk saved to gallery:", video.path);
            } catch (e) {
              console.warn("⚠️ Failed to save chunk to gallery:", e);
            } finally {
              // Always clean up temp chunk file (gallery copy is separate)
              try {
                const { File: FSFile } = require("expo-file-system");
                const f = new FSFile(video.path);
                if (f.exists) f.delete();
              } catch (_) {}
            }
          }

          // Compress and Upload chunk to backend
          try {
            const sessionId = buildWorkoutSessionId(workoutType);
            const movementsArray = movements ? movements.split(', ') : [];
            const injuriesArray = injuries ? injuries.split(', ') : [];
            
            Video.compress(video.path, {
              compressionMethod: "auto",
              maxSize: 720,
            }).then((compressedUri) => {
              processWorkoutChunk(compressedUri, sessionId, {
                movements: movementsArray,
                injuries: injuriesArray,
                workoutType,
              }).then(() => {
                console.log("✅ Chunk asynchronously uploaded to backend");
              }).catch((err) => {
                console.error("Failed to upload chunk:", err);
              }).finally(() => {
                // Clean up compressed temp file
                try {
                  const { File: FSFile } = require("expo-file-system");
                  const f = new FSFile(compressedUri);
                  if (f.exists) f.delete();
                } catch (_) {}
              });
            }).catch((err) => {
              console.error("Failed to compress chunk:", err);
            });
          } catch (e) {
            console.error("Failed to process chunk for upload:", e);
          }

          // If still recording, start the next chunk immediately
          if (isRecordingChunks.current) {
            startChunkLoop();
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

        // Optionally start chunk recording for real-time backend analysis
        if (enableChunks) {
          startChunkRecording();
        }
      } else {
        // Android: single continuous camera recording
        // TODO(android-chunks): Android camera only supports one recording at a time,
        // so chunk-based recording can't run alongside the main recording.
        // Future plan: enable chunk recording on Android (like iOS) and merge
        // chunks server-side in the Go backend. This will unlock real-time
        // AI analysis feedback on Android during workouts.
        if (!camera.current) return;

        camera.current.startRecording({
          onRecordingFinished: (video) => {
            console.log("📼 Camera Recording Finished:", video.path);
            androidRecordingResolve.current?.(video);
            androidRecordingResolve.current = null;
          },
          onRecordingError: (error) => {
            console.error("📷 Camera Recording Error:", error);
            androidRecordingResolve.current = null;
          },
        });

        setIsRecording(true);
        console.log("✅ Recording Started (Android Camera)");
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
        // Android: stop camera recording and wait for the video file
        if (camera.current) {
          const videoPromise = new Promise<{ path: string }>((resolve) => {
            androidRecordingResolve.current = resolve;
          });
          await camera.current.stopRecording();
          file = await videoPromise;
        }
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
        const sessionId = buildWorkoutSessionId(workoutType);
        const movementsArray = movements ? movements.split(", ") : [];
        const injuriesArray = injuries ? injuries.split(", ") : [];

        const itemId = enqueue(rawPath, {
          sessionId,
          workoutType,
          movements: movementsArray,
          injuries: injuriesArray,
          profileId: profileId ?? undefined,
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
        frameProcessor={frameProcessor}
        pixelFormat="yuv"
        video={true}
        audio={false}
        onInitialized={() => setIsCameraReady(true)}
      />

      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        <SkeletonOverlay pose={poseResult} width={width} height={height} />
      </View>

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
          </>
        )}

        {!isRecording && (
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
        {isSaving ? (
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

            {currentItem.status === "READY" && (
              <>
                <Text style={[styles.footerStatus, { color: "#30D158" }]}>
                  ✅ Ready to upload
                </Text>
                <TouchableOpacity
                  style={styles.encodeBtn}
                  onPress={() => startUpload(currentItem.id)}
                >
                  <Text style={styles.encodeBtnText}>Upload now</Text>
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
                  onPress={() => router.back()}
                >
                  <Text style={styles.footerLinkText}>Continue in background</Text>
                </TouchableOpacity>
              </>
            )}

            {currentItem.status === "DONE" && (
              <>
                <Text style={[styles.footerStatus, { color: "#30D158" }]}>
                  ✅ Upload complete!
                </Text>
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.back()}
                >
                  <Text style={styles.footerLinkText}>Done</Text>
                </TouchableOpacity>
              </>
            )}

            {currentItem.status === "ERROR" && (
              <>
                <Text style={[styles.footerStatus, { color: "#FF453A" }]}>
                  ❌ {currentItem.error || "An error occurred"}
                </Text>
                <TouchableOpacity
                  style={styles.footerLinkBtn}
                  onPress={() => router.push("/queue" as any)}
                >
                  <Text style={styles.footerLinkText}>View in Queue</Text>
                </TouchableOpacity>
              </>
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
