import React, { useEffect, useState } from 'react';
import {
  AppState,
  AppStateStatus,
  Platform,
  StyleSheet,
  Text,
  TouchableOpacity,
  useWindowDimensions,
  View,
  Alert,
  Linking,
  ScrollView,
} from 'react-native';
import {
  Camera,
  useCameraDevice,
  useCameraFormat,
  useCameraPermission,
} from 'react-native-vision-camera';
import { router } from 'expo-router';
import { useIsFocused } from '@react-navigation/native';

import { usePoseDetection, HEAVY_MODEL } from '../../features/ai-coach/frame-processors/usePoseDetection';
import { KeypointLabelOverlay, KEYPOINT_NAMES, KEYPOINT_COLORS, MIN_SCORE } from '../../features/ai-coach/ui/KeypointLabelOverlay';
import { IconSymbol } from '@/components/ui/icon-symbol';
import { EnergyMonitor } from '../../features/ai-coach/ui/EnergyMonitor';

/**
 * Pose Detection Test Page
 *
 * A lightweight camera view that runs MoveNet pose detection continuously
 * (no recording, no uploads) to visualize all 17 body keypoints with labels.
 * Uses the heavy float16 v4 model (12MB) at 15fps for maximum accuracy.
 * This is for experimentation — battery efficiency is not a concern here.
 */
export default function PoseTestPage() {
  const isFocused = useIsFocused();
  const [appState, setAppState] = useState<AppStateStatus>(AppState.currentState);

  useEffect(() => {
    const subscription = AppState.addEventListener('change', (nextAppState) => {
      setAppState(nextAppState);
    });
    return () => subscription.remove();
  }, []);

  const isCameraActive = isFocused && appState === 'active';

  const device = useCameraDevice('back');
  const { hasPermission, requestPermission } = useCameraPermission();
  const { width, height } = useWindowDimensions();

  const [showDetails, setShowDetails] = useState(false);
  const [cameraPosition, setCameraPosition] = useState<'back' | 'front'>('back');

  const frontDevice = useCameraDevice('front');
  const backDevice = useCameraDevice('back');
  const activeDevice = cameraPosition === 'front' ? frontDevice : backDevice;

  // 720p, 30fps — good enough for testing
  const format = useCameraFormat(activeDevice, [
    { videoResolution: { width: 1280, height: 720 } },
    { fps: 30 },
  ]);

  useEffect(() => {
    if (!hasPermission) requestPermission();
  }, [hasPermission, requestPermission]);

  // Use the heavy model (12MB float16 v4) at 15fps for best accuracy on the test page.
  // Production recording uses the default model at 2fps for battery efficiency.
  const { frameProcessor, monitorData, isModelLoaded } = usePoseDetection(true, {
    modelSource: HEAVY_MODEL,
    fps: 15,
  });

  if (!hasPermission) {
    return (
      <View style={styles.center}>
        <Text style={styles.permText}>Camera Permission Required</Text>
        <TouchableOpacity
          onPress={async () => {
            const result = await requestPermission();
            if (!result) {
              Alert.alert(
                'Permission Denied',
                'Camera permission was permanently denied. Please enable it in Settings.',
                [
                  { text: 'Cancel', style: 'cancel' },
                  { text: 'Open Settings', onPress: () => Linking.openSettings() },
                ]
              );
            }
          }}
          style={styles.permBtn}
        >
          <Text style={styles.permBtnText}>Grant Camera Access</Text>
        </TouchableOpacity>
      </View>
    );
  }

  if (!activeDevice) {
    return (
      <View style={styles.center}>
        <Text style={{ color: '#fff' }}>No Camera Available</Text>
      </View>
    );
  }

  return (
    <View style={styles.container}>
      {/* Camera */}
      <Camera
        style={StyleSheet.absoluteFill}
        device={activeDevice}
        isActive={isCameraActive}
        format={format}
        fps={30}
        frameProcessor={frameProcessor}
        pixelFormat="yuv"
        video={true}
        audio={false}
      />

      {/* Keypoint overlay with labels */}
      <View style={StyleSheet.absoluteFill} pointerEvents="none">
        <KeypointLabelOverlay poseData={monitorData.poseData} width={width} height={height} />
      </View>

      {/* Top bar */}
      <View style={styles.topBar}>
        <TouchableOpacity style={styles.backBtn} onPress={() => router.back()}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.topTitle}>POSE TEST</Text>
        <TouchableOpacity
          style={styles.flipBtn}
          onPress={() => setCameraPosition(prev => prev === 'back' ? 'front' : 'back')}
        >
          <IconSymbol name="camera.rotate" size={24} color="#fff" />
        </TouchableOpacity>
      </View>

      {/* Status badge */}
      <View style={styles.statusBadge}>
        <View style={[styles.statusDot, { backgroundColor: isModelLoaded ? '#4ADE80' : '#F87171' }]} />
        <Text style={styles.statusText}>
          {isModelLoaded ? 'Model Loaded' : 'Loading Model...'}
        </Text>
      </View>

      {/* Energy impact monitor */}
      <View style={styles.energyMonitorContainer}>
        <EnergyMonitor label="Heavy Model (12MB) · 15fps" />
      </View>

      {/* Bottom dashboard */}
      <View style={styles.bottomPanel}>
        {/* Summary row */}
        <View style={styles.summaryRow}>
          <View style={styles.summaryItem}>
            <Text style={styles.summaryValue}>
              {(monitorData.confidence * 100).toFixed(0)}%
            </Text>
            <Text style={styles.summaryLabel}>Confidence</Text>
          </View>
          <View style={styles.summaryDivider} />
          <View style={styles.summaryItem}>
            <Text style={styles.summaryValue}>
              {monitorData.motion.toFixed(3)}
            </Text>
            <Text style={styles.summaryLabel}>Motion</Text>
          </View>
          <View style={styles.summaryDivider} />
          <View style={styles.summaryItem}>
            <Text style={[styles.summaryValue, {
              color: monitorData.isWorkingOut ? '#4ADE80' : '#F87171',
            }]}>
              {monitorData.isWorkingOut ? 'ACTIVE' : 'IDLE'}
            </Text>
            <Text style={styles.summaryLabel}>State</Text>
          </View>
        </View>

        {/* Raw model output debug */}
        {monitorData.rawFirstValues && monitorData.rawFirstValues.length > 0 && (
          <View style={styles.debugSection}>
            <Text style={styles.debugTitle}>RAW MODEL OUTPUT (first 6)</Text>
            <Text style={styles.debugValues}>
              {monitorData.rawFirstValues.map((v: number) => v.toFixed(4)).join('  ')}
            </Text>
          </View>
        )}

        {/* Toggle details button */}
        <TouchableOpacity
          style={styles.detailsToggle}
          onPress={() => setShowDetails(!showDetails)}
        >
          <Text style={styles.detailsToggleText}>
            {showDetails ? 'Hide Keypoints ▼' : 'Show Keypoints ▶'}
          </Text>
        </TouchableOpacity>

        {/* Keypoint scores from monitorData (bridged via useRunOnJS) */}
        {showDetails && monitorData.rawScores && monitorData.rawScores.length > 0 && (
          <ScrollView style={styles.keypointGrid} nestedScrollEnabled>
            {KEYPOINT_NAMES.map((name, idx) => {
              const score = monitorData.rawScores[idx] || 0;
              const detected = score > MIN_SCORE;
              return (
                <View key={idx} style={styles.keypointRow}>
                  <View style={[styles.keypointDot, { backgroundColor: detected ? KEYPOINT_COLORS[idx] : '#333' }]} />
                  <Text style={[styles.keypointName, { color: detected ? '#fff' : '#555' }]}>
                    {name}
                  </Text>
                  <View style={styles.confidenceBarBg}>
                    <View
                      style={[
                        styles.confidenceBarFill,
                        {
                          width: `${Math.min(score * 100, 100)}%`,
                          backgroundColor: detected ? KEYPOINT_COLORS[idx] : '#555',
                        },
                      ]}
                    />
                  </View>
                  <Text style={[styles.keypointScore, { color: detected ? '#fff' : '#888' }]}>
                    {(score * 100).toFixed(1)}%
                  </Text>
                </View>
              );
            })}
          </ScrollView>
        )}
      </View>

      {/* Legend */}
      <View style={styles.legend}>
        {[
          { label: 'Head', color: '#FF6B6B' },
          { label: 'Shoulder', color: '#4ECDC4' },
          { label: 'Arm', color: '#45B7D1' },
          { label: 'Torso', color: '#96CEB4' },
          { label: 'Leg', color: '#FFEAA7' },
          { label: 'Foot', color: '#DDA0DD' },
        ].map(({ label, color }) => (
          <View key={label} style={styles.legendItem}>
            <View style={[styles.legendDot, { backgroundColor: color }]} />
            <Text style={styles.legendText}>{label}</Text>
          </View>
        ))}
      </View>
    </View>
  );
}


const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#000',
  },
  center: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: '#000',
  },
  permText: {
    color: '#fff',
    fontSize: 18,
    marginBottom: 16,
  },
  permBtn: {
    backgroundColor: '#fff',
    paddingVertical: 12,
    paddingHorizontal: 32,
    borderRadius: 10,
  },
  permBtnText: {
    color: '#000',
    fontWeight: 'bold',
    fontSize: 16,
  },

  // Top bar
  topBar: {
    position: 'absolute',
    top: 0,
    left: 0,
    right: 0,
    paddingTop: Platform.OS === 'ios' ? 60 : 40,
    paddingHorizontal: 16,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  backBtn: {
    width: 44,
    height: 44,
    borderRadius: 22,
    backgroundColor: 'rgba(0,0,0,0.5)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  topTitle: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '800',
    letterSpacing: 2,
  },
  flipBtn: {
    width: 44,
    height: 44,
    borderRadius: 22,
    backgroundColor: 'rgba(0,0,0,0.5)',
    justifyContent: 'center',
    alignItems: 'center',
  },

  // Status badge
  statusBadge: {
    position: 'absolute',
    top: Platform.OS === 'ios' ? 110 : 90,
    alignSelf: 'center',
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: 'rgba(0,0,0,0.6)',
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 20,
    gap: 6,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  statusText: {
    color: '#ccc',
    fontSize: 12,
    fontWeight: '600',
  },

  // Legend
  legend: {
    position: 'absolute',
    top: Platform.OS === 'ios' ? 140 : 120,
    right: 12,
    backgroundColor: 'rgba(0,0,0,0.6)',
    borderRadius: 10,
    padding: 8,
    gap: 4,
  },
  legendItem: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
  },
  legendDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  legendText: {
    color: '#aaa',
    fontSize: 10,
    fontWeight: '600',
  },

  // Bottom panel
  energyMonitorContainer: {
    position: 'absolute',
    bottom: 260,
    left: 0,
    right: 0,
    zIndex: 10,
  },
  bottomPanel: {
    position: 'absolute',
    bottom: 0,
    left: 0,
    right: 0,
    backgroundColor: 'rgba(0,0,0,0.85)',
    borderTopLeftRadius: 20,
    borderTopRightRadius: 20,
    paddingTop: 16,
    paddingBottom: Platform.OS === 'ios' ? 34 : 20,
    paddingHorizontal: 20,
  },
  summaryRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-around',
  },
  summaryItem: {
    alignItems: 'center',
    flex: 1,
  },
  summaryValue: {
    color: '#fff',
    fontSize: 22,
    fontWeight: '800',
    fontVariant: ['tabular-nums'],
  },
  summaryLabel: {
    color: '#888',
    fontSize: 11,
    fontWeight: '600',
    marginTop: 2,
    textTransform: 'uppercase',
    letterSpacing: 1,
  },
  summaryDivider: {
    width: 1,
    height: 30,
    backgroundColor: '#333',
  },

  // Details toggle
  detailsToggle: {
    alignSelf: 'center',
    marginTop: 12,
    paddingVertical: 6,
    paddingHorizontal: 16,
  },
  detailsToggleText: {
    color: '#888',
    fontSize: 12,
    fontWeight: '600',
  },

  // Keypoint grid
  keypointGrid: {
    marginTop: 8,
    maxHeight: 280,
  },
  keypointRow: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingVertical: 4,
    gap: 8,
  },
  keypointDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
  },
  keypointName: {
    width: 80,
    fontSize: 12,
    fontWeight: '600',
  },
  confidenceBarBg: {
    flex: 1,
    height: 6,
    backgroundColor: '#222',
    borderRadius: 3,
    overflow: 'hidden',
  },
  confidenceBarFill: {
    height: '100%',
    borderRadius: 3,
  },
  keypointScore: {
    width: 36,
    fontSize: 11,
    fontWeight: '700',
    textAlign: 'right',
    fontVariant: ['tabular-nums'],
  },

  // Debug section
  debugSection: {
    marginTop: 10,
    padding: 8,
    backgroundColor: 'rgba(255,100,0,0.15)',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255,100,0,0.3)',
  },
  debugTitle: {
    color: '#FF6400',
    fontSize: 10,
    fontWeight: '800',
    letterSpacing: 1,
    marginBottom: 4,
  },
  debugValues: {
    color: '#fff',
    fontSize: 11,
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
  },
});
