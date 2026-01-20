import React, { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Alert, StyleSheet, Text, TouchableOpacity, useWindowDimensions, View } from 'react-native';
import { Camera, useCameraDevice, useCameraFormat, useCameraPermission } from 'react-native-vision-camera';
import { Canvas, Line, vec } from '@shopify/react-native-skia';
import * as MediaLibrary from 'expo-media-library';
import { Video } from 'react-native-compressor';
import { startInAppRecording, stopInAppRecording } from 'react-native-nitro-screen-recorder';

import { usePoseDetection } from '../../features/ai-coach/frame-processors/usePoseDetection';
import { SkeletonOverlay } from '../../features/ai-coach/ui/SkeletonOverlay';
import { useHeartRate } from '../../features/health/useHeartRate';

const cameraFormat = [
  { videoResolution: { width: 1280, height: 720 } },
  { fps: 30 }
];

const CHUNK_DURATION_MS = 10000; // 10 seconds

export default function VisionTestPage() {
  const device = useCameraDevice('back');
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  const camera = useRef<Camera>(null);

  // Use a ref to track if we should continue recording chunks,
  // preventing stale state in closures/timeouts.
  const isRecordingChunks = useRef(false);
  const chunkTimer = useRef<NodeJS.Timeout | null>(null);

  // 720p 포맷 고정
  const format = useCameraFormat(device, cameraFormat);

  const [mediaPermission, requestMediaPermission] = MediaLibrary.usePermissions();
  const { frameProcessor, poseResult, monitorData } = usePoseDetection();
  const { bpm, status: hrStatus } = useHeartRate();

  const [isRecording, setIsRecording] = useState(false);
  const [isProcessing, setIsProcessing] = useState(false);

  // UI 좌표 계산 (Updated for Body Parts)
  const headY = monitorData.headY * height;
  const shoulderY = monitorData.shoulderY * height;
  const hipY = monitorData.hipY * height;

  useEffect(() => { 
    if (!hasPermission) requestPermission();
    if (!mediaPermission?.granted) requestMediaPermission();
  }, [hasPermission, mediaPermission]);

  // --- Chunk Recording Logic (Raw Camera) ---

  const startChunkLoop = async () => {
    if (!camera.current || !isRecordingChunks.current) return;

    try {
      console.log("📷 Starting new chunk recording...");
      camera.current.startRecording({
        onRecordingFinished: async (video) => {
          console.log("📷 Chunk Finished:", video.path);

          // Save chunk to gallery
          try {
             await MediaLibrary.saveToLibraryAsync(video.path);
             console.log("✅ Chunk saved to gallery");
          } catch (e) {
             console.error("Failed to save chunk:", e);
          }

          // If still recording, start the next chunk immediately
          if (isRecordingChunks.current) {
            startChunkLoop();
          }
        },
        onRecordingError: (error) => {
          console.error("📷 Chunk Recording Error:", error);
        }
      });

      // Schedule stop
      chunkTimer.current = setTimeout(async () => {
        if (isRecordingChunks.current && camera.current) {
          try {
            await camera.current.stopRecording();
          } catch (e) {
            console.error("Failed to stop chunk recording:", e);
          }
        }
      }, CHUNK_DURATION_MS);

    } catch (e) {
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
    if (camera.current) {
      try {
        await camera.current.stopRecording();
      } catch (e) {
        // Ignore error if not recording
      }
    }
  };


  // --- Main Screen Recording Logic (Full Video with Overlays) ---

  const handleStartRecording = async () => {
    try {
      // 1. Start Screen Recorder (Full Video)
      // 마이크 권한 충돌 방지를 위해 mic: false 설정 (필요 시 true)
      // 앱에 이미 카메라 프리뷰가 있으므로 recorder 카메라 오버레이는 끔.
      await startInAppRecording({
        options: {
          enableMic: false,
          enableCamera: false
        },
        onRecordingFinished: (file) => {
          console.log("📼 Screen Recording Finished:", file.path);
        }
      });
      
      setIsRecording(true);
      console.log("✅ Screen Recording Started");

      // 2. Start Chunk Recording (Raw Camera)
      startChunkRecording();

    } catch (error) {
      console.error("Recording Start Error:", error);
      Alert.alert("녹화 시작 실패", "녹화를 시작할 수 없습니다.");
    }
  };

  const handleStopRecording = async () => {
    if (!isRecording) return;

    try {
      setIsProcessing(true); // Show spinner

      // 1. Stop Chunk Recording
      await stopChunkRecording();

      // 2. Stop Screen Recorder
      const file = await stopInAppRecording();
      setIsRecording(false);
      console.log("📼 Original Video Path:", file?.path);

      if (file?.path) {
        // 압축 (기존 로직 유지)
        const compressedUri = await Video.compress(file.path, {
          compressionMethod: 'auto',
          maxSize: 1280,
        });

        // 갤러리 저장 (기존 로직 유지)
        await MediaLibrary.saveToLibraryAsync(compressedUri);
        Alert.alert("저장 완료", "운동 영상이 갤러리에 저장되었습니다.");
      }
    } catch (error) {
      console.error("Recording Stop Error:", error);
      Alert.alert("저장 오류", "영상 처리 중 문제가 발생했습니다.");
    } finally {
      setIsProcessing(false);
    }
  };

  if (!device) return <View style={styles.center}><Text>No Camera</Text></View>;

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
      />

      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        <SkeletonOverlay pose={poseResult} width={width} height={height} />
      </View>

      {/* Body Part Annotations */}
      <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
         {monitorData.score > 0.2 && (
           <>
             {/* Head Line (Red) */}
             <Line 
               p1={vec(0, headY)}
               p2={vec(width, headY)}
               color="red"
               style="stroke" 
               strokeWidth={2}
             />
             
             {/* Shoulder Line (Green) */}
             <Line 
               p1={vec(0, shoulderY)}
               p2={vec(width, shoulderY)}
               color="green"
               style="stroke" 
               strokeWidth={2}
             />

             {/* Hip Line (Blue) */}
             <Line
               p1={vec(0, hipY)}
               p2={vec(width, hipY)}
               color="blue"
               style="stroke"
               strokeWidth={2}
             />
           </>
         )}
      </Canvas>

      {/* 심박수 패널 */}
      <View style={styles.hrPanel}>
          <Text style={styles.hrLabel}>HEART RATE</Text>
          <View style={styles.hrValueContainer}>
             <Text style={[styles.hrValue, { color: bpm > 0 ? '#0f0' : '#888' }]}>{bpm > 0 ? bpm : '--'}</Text>
             <Text style={styles.hrUnit}> BPM</Text>
          </View>
          <Text style={styles.hrStatus}>State: {hrStatus}</Text>
      </View>

      {/* 녹화 중엔 디버그 숨김 */}
      {!isRecording && (
        <View style={styles.dashboard}>
          <Text style={styles.dashTitle}>📊 SYSTEM</Text>
          <View style={styles.row}><Text style={styles.label}>RES:</Text><Text style={styles.val}>{format?.videoWidth}x{format?.videoHeight}</Text></View>
          <View style={styles.row}><Text style={styles.label}>CONF:</Text><Text style={styles.val}>{(monitorData.score * 100).toFixed(0)}%</Text></View>
        </View>
      )}

      {/* 녹화 버튼 */}
      <View style={styles.recordControl}>
        {isProcessing ? (
          <View style={styles.processingBadge}>
             <ActivityIndicator color="#000" />
             <Text style={styles.processingText}> Saving...</Text>
          </View>
        ) : (
          <TouchableOpacity onPress={isRecording ? handleStopRecording : handleStartRecording} style={[styles.recordBtn, isRecording && styles.recordingBtn]}>
            <View style={[styles.innerBtn, isRecording && styles.innerRecordingBtn]} />
          </TouchableOpacity>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: 'black' },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center' },
  dashboard: { position: 'absolute', top: 50, left: 10, backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8, width: 140, borderWidth: 1, borderColor: '#555', zIndex: 10 },
  dashTitle: { color: '#fff', fontWeight:'bold', fontSize: 10, marginBottom: 5, textAlign:'center' },
  row: { flexDirection: 'row', justifyContent: 'space-between', marginVertical: 2 },
  label: { color: '#aaa', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  val: { color: '#fff', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  hrPanel: { position: 'absolute', top: 50, right: 10, backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8, alignItems: 'flex-end', borderRightWidth: 3, borderColor: '#FF0000', zIndex: 10 },
  hrLabel: { color: '#FF0000', fontSize: 10, fontWeight: '900' },
  hrValue: { fontSize: 32, fontWeight: 'bold', fontFamily: 'monospace' },
  hrUnit: { color: '#888', fontSize: 12, marginBottom: 5, fontWeight: 'bold' },
  hrValueContainer: {
    flexDirection: 'row',
    alignItems: 'flex-end',
  },
  hrStatus: {
    color: '#aaa',
    fontSize: 9,
    marginTop: 2,
  },
  recordControl: { position: 'absolute', bottom: 50, alignSelf: 'center', alignItems: 'center', zIndex: 20 },
  recordBtn: { width: 80, height: 80, borderRadius: 40, borderWidth: 6, borderColor: 'white', justifyContent: 'center', alignItems: 'center' },
  recordingBtn: { borderColor: 'red' },
  innerBtn: { width: 60, height: 60, borderRadius: 30, backgroundColor: 'red' },
  innerRecordingBtn: { width: 30, height: 30, borderRadius: 6 },
  processingBadge: { flexDirection:'row', backgroundColor:'#00FF00', padding:15, borderRadius:30, alignItems:'center' },
  processingText: {
    fontWeight: 'bold',
  },
});
