import React, { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Alert, StyleSheet, Text, TouchableOpacity, useWindowDimensions, View } from 'react-native';
import { Camera, useCameraDevice, useCameraFormat, useCameraPermission } from 'react-native-vision-camera';
// 📦 [수정] DashPathEffect 추가
import { Canvas, Circle, DashPathEffect, Line, vec } from '@shopify/react-native-skia';
import * as MediaLibrary from 'expo-media-library';
import { Video } from 'react-native-compressor';
import { startInAppRecording, stopInAppRecording } from 'react-native-nitro-screen-recorder';

import { usePoseDetection } from '../../features/ai-coach/frame-processors/usePoseDetection';
import { SkeletonOverlay } from '../../features/ai-coach/ui/SkeletonOverlay';
import { useHeartRate } from '../../features/health/useHeartRate';

export default function VisionTestPage() {
  const device = useCameraDevice('back');
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  const camera = useRef<Camera>(null);

  // 720p 포맷 고정
  const format = useCameraFormat(device, [
    { videoResolution: { width: 1280, height: 720 } },
    { fps: 30 }
  ]);

  const [mediaPermission, requestMediaPermission] = MediaLibrary.usePermissions();
  const { frameProcessor, poseResult, monitorData } = usePoseDetection();
  const { bpm, status: hrStatus } = useHeartRate();

  const [isRecording, setIsRecording] = useState(false);
  const [isProcessing, setIsProcessing] = useState(false);

  // UI 좌표 계산
  const dotX = monitorData.x * width;
  const dotY = monitorData.y * height;
  const squatLineY = monitorData.squatThresh * height;
  const standLineY = monitorData.standThresh * height;
  const dotColor = monitorData.state === 'SQUAT' ? '#00FF00' : '#FF0000';
  const isTrapped = monitorData.y > monitorData.standThresh && monitorData.y < monitorData.squatThresh;

  useEffect(() => { 
    if (!hasPermission) requestPermission();
    if (!mediaPermission?.granted) requestMediaPermission();
  }, [hasPermission, mediaPermission]);

  const handleStartRecording = async () => {
    try {
      // 마이크 권한 충돌 방지를 위해 mic: false 설정 (필요 시 true)
      // 앱에 이미 카메라 프리뷰가 있으므로 recorder 카메라 오버레이는 끔.
      await startInAppRecording({
        options: {
          enableMic: false,
          enableCamera: false
        },
        onRecordingFinished: (file) => {
          console.log("📼 Recording Finished:", file.path);
        }
      });
      
      setIsRecording(true);
      console.log("✅ Recording Started");
    } catch (error) {
      console.error("Recording Start Error:", error);
      Alert.alert("녹화 시작 실패", "녹화를 시작할 수 없습니다.");
    }
  };

  const handleStopRecording = async () => {
    if (!isRecording) return;

    try {
      // 녹화 중단 및 파일 경로 획득
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
        video={false} 
        audio={false}
      />

      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        <SkeletonOverlay pose={poseResult} width={width} height={height} />
      </View>

      {/* 게임 UI */}
      <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
         {monitorData.score > 0.2 && (
           <>
             {/* 📉 앉기 목표선 (실선) */}
             <Line 
               p1={vec(0, squatLineY)} 
               p2={vec(width, squatLineY)} 
               color="yellow" 
               style="stroke" 
               strokeWidth={3} 
             />
             
             {/* 📈 [수정] 서기 목표선 (점선) - DashPathEffect 사용 */}
             <Line 
               p1={vec(0, standLineY)} 
               p2={vec(width, standLineY)} 
               color="cyan" 
               style="stroke" 
               strokeWidth={3}
             >
                {/* 🚨 strokeDash prop 대신 자식 컴포넌트로 효과 적용 */}
                <DashPathEffect intervals={[10, 10]} />
             </Line>

             <Circle cx={dotX} cy={dotY} r={20} color={dotColor} />
           </>
         )}
      </Canvas>

      {/* 중앙 카운터 */}
      <View style={styles.counterBox}>
         <Text style={styles.repCount}>{monitorData.count}</Text>
         <View style={[styles.badge, { backgroundColor: isTrapped ? 'gray' : dotColor }]}>
            <Text style={styles.badgeText}>{isTrapped ? "MOVE MORE" : monitorData.state}</Text>
         </View>
      </View>

      {/* 심박수 패널 */}
      <View style={styles.hrPanel}>
          <Text style={styles.hrLabel}>HEART RATE</Text>
          <View style={{flexDirection:'row', alignItems:'flex-end'}}>
             <Text style={[styles.hrValue, { color: bpm > 0 ? '#0f0' : '#888' }]}>{bpm > 0 ? bpm : '--'}</Text>
             <Text style={styles.hrUnit}> BPM</Text>
          </View>
          <Text style={{color:'#aaa', fontSize:9, marginTop:2}}>State: {hrStatus}</Text>
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
             <Text style={{fontWeight:'bold'}}> Saving...</Text>
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
  counterBox: { position: 'absolute', top: 120, alignSelf: 'center', alignItems: 'center' },
  repCount: { color: 'white', fontSize: 100, fontWeight: '900', textShadowColor: 'black', textShadowRadius: 10 },
  badge: { paddingHorizontal: 15, paddingVertical: 5, borderRadius: 10, marginTop: 5 },
  badgeText: { color: 'white', fontWeight: 'bold', fontSize: 16 },
  dashboard: { position: 'absolute', top: 50, left: 10, backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8, width: 140, borderWidth: 1, borderColor: '#555', zIndex: 10 },
  dashTitle: { color: '#fff', fontWeight:'bold', fontSize: 10, marginBottom: 5, textAlign:'center' },
  row: { flexDirection: 'row', justifyContent: 'space-between', marginVertical: 2 },
  label: { color: '#aaa', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  val: { color: '#fff', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  hrPanel: { position: 'absolute', top: 50, right: 10, backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8, alignItems: 'flex-end', borderRightWidth: 3, borderColor: '#FF0000', zIndex: 10 },
  hrLabel: { color: '#FF0000', fontSize: 10, fontWeight: '900' },
  hrValue: { fontSize: 32, fontWeight: 'bold', fontFamily: 'monospace' },
  hrUnit: { color: '#888', fontSize: 12, marginBottom: 5, fontWeight: 'bold' },
  recordControl: { position: 'absolute', bottom: 50, alignSelf: 'center', alignItems: 'center', zIndex: 20 },
  recordBtn: { width: 80, height: 80, borderRadius: 40, borderWidth: 6, borderColor: 'white', justifyContent: 'center', alignItems: 'center' },
  recordingBtn: { borderColor: 'red' },
  innerBtn: { width: 60, height: 60, borderRadius: 30, backgroundColor: 'red' },
  innerRecordingBtn: { width: 30, height: 30, borderRadius: 6 },
  processingBadge: { flexDirection:'row', backgroundColor:'#00FF00', padding:15, borderRadius:30, alignItems:'center' }
});
