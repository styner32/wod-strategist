import { Canvas, Circle, Line, vec } from '@shopify/react-native-skia';
import React, { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Alert, StyleSheet, Text, TouchableOpacity, useWindowDimensions, View } from 'react-native';
import { Video } from 'react-native-compressor';
import { Camera, useCameraDevice, useCameraPermission, VideoFile } from 'react-native-vision-camera';

import { useHeartRate } from '@/features/health/useHeartRate';
import { usePoseDetection } from '../../features/ai-coach/frame-processors/usePoseDetection';
import { SkeletonOverlay } from '../../features/ai-coach/ui/SkeletonOverlay';

export default function VisionTestPage() {
  const device = useCameraDevice('back');
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  const camera = useRef<Camera>(null);

  const { frameProcessor, poseResult, monitorData } = usePoseDetection();
  
  const [isRecording, setIsRecording] = useState(false);
  const [isProcessing, setIsProcessing] = useState(false);

  // 심박수 색상 (Zone)
  const getHrColor = (bpm: number) => bpm < 140 ? '#0f0' : (bpm < 160 ? '#ff0' : '#f00');

  // 좌표 계산
  const dotX = monitorData.x * width;
  const dotY = monitorData.y * height;
  const squatLineY = monitorData.squatThresh * height;
  const standLineY = monitorData.standThresh * height;
  
  const isSquatting = monitorData.state === 'SQUAT';
  const dotColor = isSquatting ? '#00FF00' : '#FF0000';
  const isTrapped = monitorData.y > monitorData.standThresh && monitorData.y < monitorData.squatThresh;

  const { bpm, isAuthorized } = useHeartRate();

  useEffect(() => { 
    if (!hasPermission) {
      requestPermission();
    }
  }, [hasPermission]);

  // 🎬 녹화 함수
  const handleRecording = async () => {
    if (!camera.current) return;
    if (isRecording) {
      await camera.current.stopRecording();
      setIsRecording(false);
    } else {
      setIsRecording(true);
      camera.current.startRecording({
        fileType: 'mp4',
        onRecordingFinished: async (video) => processVideo(video),
        onRecordingError: (e) => console.error(e)
      });
    }
  };

  const processVideo = async (video: VideoFile) => {
    setIsProcessing(true);
    try {
      const compressedUri = await Video.compress(video.path, {
        compressionMethod: 'auto'
      });
      Alert.alert("분석 준비 완료", `영상 압축됨 (720p):\n${compressedUri}`);
    } catch (e) {
      Alert.alert("Error", "영상 처리 실패");
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
        frameProcessor={frameProcessor}
        pixelFormat="yuv"
        video={true} 
        audio={false} // 🚨 [핵심] 오디오를 꺼서 권한 크래시 방지
      />

      {/* 1. 스켈레톤 */}
      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        <SkeletonOverlay pose={poseResult} width={width} height={height} />
      </View>

      {/* 2. 게임 UI (점, 선) */}
      <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
         {monitorData.score > 0.2 && (
           <>
             {/* 📉 앉기 목표선 (Yellow) */}
             <Line p1={vec(0, squatLineY)} p2={vec(width, squatLineY)} 
                   color="yellow" style="stroke" strokeWidth={3} />
             {/* 📈 서기 목표선 (Cyan) */}
             <Line p1={vec(0, standLineY)} p2={vec(width, standLineY)} 
                   color="cyan" style="stroke" strokeWidth={3} />
             {/* 🔴 내 엉덩이 */}
             <Circle cx={dotX} cy={dotY} r={20} color={dotColor} />
           </>
         )}
      </Canvas>

      {/* 3. 중앙 카운터 */}
      <View style={styles.counterBox}>
         <Text style={styles.repCount}>{monitorData.count}</Text>
         <View style={[styles.badge, { backgroundColor: isTrapped ? 'gray' : dotColor }]}>
            <Text style={styles.badgeText}>
               {isTrapped ? "MOVE MORE" : monitorData.state}
            </Text>
         </View>
      </View>

      {/* 4. 📊 [복구] 좌측 상세 데이터 패널 */}
      <View style={styles.dashboard}>
        <Text style={styles.dashTitle}>📊 TELEMETRY</Text>
        <View style={styles.row}>
            <Text style={styles.label}>CONF:</Text>
            <Text style={[styles.val, {color: monitorData.score>0.4?'#0f0':'#f55'}]}>
                {(monitorData.score * 100).toFixed(0)}%
            </Text>
        </View>
        <View style={styles.divider} />
        <View style={styles.row}><Text style={styles.label}>HIP Y:</Text><Text style={styles.val}>{monitorData.y.toFixed(2)}</Text></View>
        <View style={styles.row}><Text style={styles.label}>KNEE Y:</Text><Text style={styles.val}>{monitorData.kneeY.toFixed(2)}</Text></View>
        <View style={styles.divider} />
        <View style={styles.row}><Text style={{color:'cyan', fontSize:10, fontWeight:'bold'}}>RESET:</Text><Text style={styles.val}>{monitorData.standThresh.toFixed(2)}</Text></View>
        <View style={styles.row}><Text style={{color:'yellow', fontSize:10, fontWeight:'bold'}}>GOAL:</Text><Text style={styles.val}>{monitorData.squatThresh.toFixed(2)}</Text></View>
      </View>

      {/* 5. 💓 [신규] 우측 심박수 패널 (Mock) */}
      <View style={styles.hrPanel}>
          <Text style={styles.hrLabel}>HEART RATE</Text>
          <View style={{flexDirection:'row', alignItems:'flex-end'}}>
             {/* 데이터가 없거나 0이면 대기 표시 */}
             <Text style={[styles.hrValue, { color: getHrColor(bpm) }]}>
               {bpm > 0 ? bpm : '--'}
             </Text>
             <Text style={styles.hrUnit}> BPM</Text>
          </View>
          
          <Text style={{color:'#666', fontSize:9}}>
             {isAuthorized ? "Synced via HealthKit" : "Check Permissions"}
          </Text>
      </View>

      {/* 6. 녹화 버튼 */}
      <View style={styles.recordControl}>
        {isProcessing ? (
          <View style={styles.processingBadge}>
             <ActivityIndicator color="#000" />
             <Text style={{fontWeight:'bold'}}> Saving...</Text>
          </View>
        ) : (
          <TouchableOpacity 
            onPress={handleRecording}
            style={[styles.recordBtn, isRecording && styles.recordingBtn]}
          >
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

  // 좌측 디버그 패널
  dashboard: {
    position: 'absolute', top: 50, left: 10,
    backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8,
    width: 140, borderWidth: 1, borderColor: '#555', zIndex: 10
  },
  dashTitle: { color: '#fff', fontWeight:'bold', fontSize: 10, marginBottom: 5, textAlign:'center' },
  row: { flexDirection: 'row', justifyContent: 'space-between', marginVertical: 2 },
  label: { color: '#aaa', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  val: { color: '#fff', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  divider: { height: 1, backgroundColor: '#444', marginVertical: 3 },

  // 우측 심박수 패널
  hrPanel: {
    position: 'absolute', top: 50, right: 10,
    backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8,
    alignItems: 'flex-end', borderRightWidth: 3, borderColor: '#FF0000', zIndex: 10
  },
  hrLabel: { color: '#FF0000', fontSize: 10, fontWeight: '900' },
  hrValue: { fontSize: 32, fontWeight: 'bold', fontFamily: 'monospace' },
  hrUnit: { color: '#888', fontSize: 12, marginBottom: 5, fontWeight: 'bold' },

  // 녹화 버튼
  recordControl: { position: 'absolute', bottom: 50, alignSelf: 'center', alignItems: 'center' },
  recordBtn: {
    width: 80, height: 80, borderRadius: 40,
    borderWidth: 6, borderColor: 'white',
    justifyContent: 'center', alignItems: 'center'
  },
  recordingBtn: { borderColor: 'red' },
  innerBtn: { width: 60, height: 60, borderRadius: 30, backgroundColor: 'red' },
  innerRecordingBtn: { width: 30, height: 30, borderRadius: 6 },
  processingBadge: { flexDirection:'row', backgroundColor:'#00FF00', padding:15, borderRadius:30, alignItems:'center' }
});