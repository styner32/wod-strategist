import React, { useEffect, useState } from 'react';
import { StyleSheet, Text, useWindowDimensions, View } from 'react-native';
import { useDerivedValue } from 'react-native-reanimated';
import { Camera, useCameraDevice, useCameraPermission } from 'react-native-vision-camera';
import { scheduleOnRN } from 'react-native-worklets';
import { usePoseDetection } from '../../features/ai-coach/frame-processors/usePoseDetection';
import { SkeletonOverlay } from '../../features/ai-coach/ui/SkeletonOverlay';

export default function VisionTestPage() {
  const device = useCameraDevice('back');
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  
  const { frameProcessor, poseResult, debugInfo, modelState, errorMsg } = usePoseDetection();
  
  // 화면 표시용 State
  const [stats, setStats] = useState({ count: 0, x: 0, y: 0 });

  // 빠른 Worklet 데이터를 UI로 안전하게 전달 (0.5초마다 갱신)
  useDerivedValue(() => {
    if (Math.random() > 0.95) { // 부하 조절
      scheduleOnRN(setStats, {
        count: debugInfo.value[0],
        x: debugInfo.value[1],
        y: debugInfo.value[2]
      });
    }
  });

  useEffect(() => {
    if (!hasPermission) requestPermission();
  }, [hasPermission]);

  if (!device) return <View style={styles.center}><Text>No Camera</Text></View>;

  return (
    <View style={styles.container}>
      <Camera
        style={StyleSheet.absoluteFill}
        device={device}
        isActive={true}
        frameProcessor={frameProcessor}
        pixelFormat="yuv"
      />
      
      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        <SkeletonOverlay 
          pose={poseResult} 
          width={width} 
          height={height}
          cameraPosition="back"
        />
      </View>

      {/* 🛑 디버그 패널 (화면 중앙) */}
      <View style={styles.debugPanel}>
        <Text style={styles.debugTitle}>🔍 AI DIAGNOSTICS</Text>
        <Text style={styles.debugText}>Model: {modelState}</Text>
        
        {/* 에러가 있으면 빨간색으로 표시 */}
        {errorMsg ? (
          <Text style={styles.errorText}>⚠️ ERROR: {errorMsg}</Text>
        ) : (
          <>
            <Text style={styles.debugText}>Frames: {stats.count} (Running)</Text>
            <Text style={styles.debugText}>Nose X: {stats.x.toFixed(1)}</Text>
            <Text style={styles.debugText}>Nose Y: {stats.y.toFixed(1)}</Text>
          </>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: 'black' },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center' },
  debugPanel: {
    position: 'absolute', top: 100, left: 20, right: 20,
    backgroundColor: 'rgba(0,0,0,0.7)', padding: 15, borderRadius: 10,
    borderWidth: 2, borderColor: '#00FF00'
  },
  debugTitle: { color: '#00FF00', fontWeight: 'bold', fontSize: 16, marginBottom: 5 },
  debugText: { color: 'white', fontSize: 14, fontFamily: 'monospace' },
  errorText: { color: '#FF4444', fontWeight: 'bold', fontSize: 14 }
});