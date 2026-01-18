import { Canvas, Circle, Line, vec } from '@shopify/react-native-skia';
import React, { useEffect } from 'react';
import { StyleSheet, Text, useWindowDimensions, View } from 'react-native';
import { Camera, useCameraDevice, useCameraPermission } from 'react-native-vision-camera';
import { usePoseDetection } from '../../features/ai-coach/frame-processors/usePoseDetection';

export default function VisionTestPage() {
  const device = useCameraDevice('back');
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();
  
  const { frameProcessor, monitorData, isModelLoaded } = usePoseDetection();

  // 좌표 계산
  const dotX = monitorData.x * width;
  const dotY = monitorData.y * height;
  
  // 🎯 두 개의 기준선
  const squatLineY = monitorData.squatThresh * height; // 이 선 아래로 가야 함
  const standLineY = monitorData.standThresh * height; // 이 선 위로 가야 함

  const isSquatting = monitorData.state === 'SQUAT';
  const dotColor = isSquatting ? '#00FF00' : '#FF0000'; // 앉으면 초록, 서면 빨강
  
  // 갇힌 상태인지 체크 (데드존)
  const isTrapped = monitorData.y > monitorData.standThresh && monitorData.y < monitorData.squatThresh;

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

      {/* 1. 그래픽 레이어 */}
      <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
         {monitorData.score > 0.2 && (
           <>
             {/* 📉 앉기 기준선 (Yellow) */}
             <Line p1={vec(0, squatLineY)} p2={vec(width, squatLineY)} 
                   color="yellow" style="stroke" strokeWidth={3} />
             
             {/* 📈 서기 기준선 (Cyan) - 여기까지 올라와야 카운트 됨! */}
             <Line p1={vec(0, standLineY)} p2={vec(width, standLineY)} 
                   color="cyan" style="stroke" strokeWidth={3} strokeDash={[10, 10]} />

             {/* 🔴 내 엉덩이 */}
             <Circle cx={dotX} cy={dotY} r={20} color={dotColor} />
           </>
         )}
      </Canvas>

      {/* 2. 중앙 상태 메시지 */}
      <View style={[styles.centerMsg, { top: squatLineY - 20 }]}>
          <Text style={{color:'yellow', fontWeight:'bold'}}>👇 GO DOWN</Text>
      </View>
      <View style={[styles.centerMsg, { top: standLineY - 20 }]}>
          <Text style={{color:'cyan', fontWeight:'bold'}}>👆 GO UP (RESET)</Text>
      </View>

      {/* 3. 초대형 카운터 */}
      <View style={styles.counterBox}>
         <Text style={styles.repCount}>{monitorData.count}</Text>
         <View style={[styles.badge, { backgroundColor: isTrapped ? 'gray' : dotColor }]}>
            <Text style={styles.badgeText}>
               {isTrapped ? "MOVE MORE" : monitorData.state}
            </Text>
         </View>
      </View>

      {/* 4. 📊 상세 데이터 패널 (왼쪽 상단) - 모든 숫자 표시 */}
      <View style={styles.dashboard}>
        <Text style={styles.dashTitle}>📊 FULL TELEMETRY</Text>
        
        {/* 신뢰도 */}
        <View style={styles.row}>
            <Text style={styles.label}>CONF:</Text>
            <Text style={[styles.val, {color: monitorData.score>0.4?'#0f0':'#f55'}]}>
                {(monitorData.score * 100).toFixed(0)}%
            </Text>
        </View>
        <View style={styles.divider} />
        
        {/* 좌표 */}
        <View style={styles.row}><Text style={styles.label}>HIP Y:</Text><Text style={styles.val}>{monitorData.y.toFixed(2)}</Text></View>
        <View style={styles.row}><Text style={styles.label}>KNEE Y:</Text><Text style={styles.val}>{monitorData.kneeY.toFixed(2)}</Text></View>
        <View style={styles.divider} />
        
        {/* 기준값 */}
        <View style={styles.row}><Text style={{color:'cyan', fontSize:10, fontWeight:'bold'}}>RESET:</Text><Text style={styles.val}>{monitorData.standThresh.toFixed(2)}</Text></View>
        <View style={styles.row}><Text style={{color:'yellow', fontSize:10, fontWeight:'bold'}}>GOAL:</Text><Text style={styles.val}>{monitorData.squatThresh.toFixed(2)}</Text></View>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: 'black' },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center' },
  
  centerMsg: { position: 'absolute', width: '100%', alignItems: 'center', zIndex:1 },

  counterBox: {
    position: 'absolute', top: 100, alignSelf: 'center', alignItems: 'center'
  },
  repCount: { 
    color: 'white', fontSize: 100, fontWeight: '900', 
    textShadowColor: 'black', textShadowRadius: 10 
  },
  badge: { paddingHorizontal: 15, paddingVertical: 5, borderRadius: 10, marginTop: 5 },
  badgeText: { color: 'white', fontWeight: 'bold', fontSize: 16 },

  dashboard: {
    position: 'absolute', top: 50, left: 10,
    backgroundColor: 'rgba(0,0,0,0.7)', padding: 10, borderRadius: 8,
    width: 140, borderWidth: 1, borderColor: '#555'
  },
  dashTitle: { color: '#fff', fontWeight:'bold', fontSize: 10, marginBottom: 5, textAlign:'center' },
  row: { flexDirection: 'row', justifyContent: 'space-between', marginVertical: 2 },
  label: { color: '#aaa', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  val: { color: '#fff', fontSize: 11, fontFamily: 'monospace', fontWeight: 'bold' },
  divider: { height: 1, backgroundColor: '#444', marginVertical: 3 }
});