import { Canvas, Circle, Path, Skia, Text as SkiaText, matchFont } from '@shopify/react-native-skia';
import React, { useMemo } from 'react';
import { Platform, StyleSheet } from 'react-native';

// MoveNet Thunder — 17 keypoints in order
const KEYPOINT_NAMES = [
  'Nose',        // 0
  'L Eye',       // 1
  'R Eye',       // 2
  'L Ear',       // 3
  'R Ear',       // 4
  'L Shoulder',  // 5
  'R Shoulder',  // 6
  'L Elbow',     // 7
  'R Elbow',     // 8
  'L Wrist',     // 9
  'R Wrist',     // 10
  'L Hip',       // 11
  'R Hip',       // 12
  'L Knee',      // 13
  'R Knee',      // 14
  'L Ankle',     // 15
  'R Ankle',     // 16
];

// Colors per body region
const COLORS = {
  head: '#FF6B6B',      // coral red
  shoulder: '#4ECDC4',  // teal
  arm: '#45B7D1',       // sky blue
  torso: '#96CEB4',     // sage green
  leg: '#FFEAA7',       // warm yellow
  foot: '#DDA0DD',      // plum
};

const KEYPOINT_COLORS = [
  COLORS.head,     // 0 nose
  COLORS.head,     // 1 l_eye
  COLORS.head,     // 2 r_eye
  COLORS.head,     // 3 l_ear
  COLORS.head,     // 4 r_ear
  COLORS.shoulder, // 5 l_shoulder
  COLORS.shoulder, // 6 r_shoulder
  COLORS.arm,      // 7 l_elbow
  COLORS.arm,      // 8 r_elbow
  COLORS.arm,      // 9 l_wrist
  COLORS.arm,      // 10 r_wrist
  COLORS.torso,    // 11 l_hip
  COLORS.torso,    // 12 r_hip
  COLORS.leg,      // 13 l_knee
  COLORS.leg,      // 14 r_knee
  COLORS.foot,     // 15 l_ankle
  COLORS.foot,     // 16 r_ankle
];

// Skeleton connections for drawing bones
const CONNECTIONS: [number, number][] = [
  [0, 1], [0, 2], [1, 3], [2, 4],           // face
  [5, 6], [5, 7], [7, 9], [6, 8], [8, 10],  // arms/shoulders
  [5, 11], [6, 12], [11, 12],               // torso
  [11, 13], [13, 15], [12, 14], [14, 16],   // legs
];

const MIN_SCORE = 0.05; // Low threshold for debug — use 0.3 in production

interface Props {
  /** Flat array of [y0, x0, s0, y1, x1, s1, ...] — 17*3 = 51 values, normalized 0-1 */
  poseData: number[];
  width: number;
  height: number;
}

export { KEYPOINT_NAMES, KEYPOINT_COLORS, MIN_SCORE };

/**
 * Draws labeled, color-coded skeleton keypoints on a Skia Canvas.
 * Accepts poseData as a plain number[] (bridged from worklet via useRunOnJS).
 */
export const KeypointLabelOverlay = ({ poseData, width, height }: Props) => {
  const font = useMemo(() => matchFont({
    fontFamily: Platform.select({ ios: 'Menlo', default: 'monospace' }),
    fontSize: 11,
    fontWeight: 'bold',
  }), []);

  // Pre-compute screen-space keypoint positions
  const keypoints = useMemo(() => {
    if (!poseData || poseData.length < 51) return [];
    const result = [];
    for (let i = 0; i < 17; i++) {
      const y = poseData[i * 3];
      const x = poseData[i * 3 + 1];
      const s = poseData[i * 3 + 2];
      result.push({
        screenX: x * width,
        screenY: y * height,
        score: s,
      });
    }
    return result;
  }, [poseData, width, height]);

  // Build skeleton path
  const skeletonPath = useMemo(() => {
    const path = Skia.Path.Make();
    if (keypoints.length === 0) return path;

    for (const [start, end] of CONNECTIONS) {
      const p1 = keypoints[start];
      const p2 = keypoints[end];
      if (p1.score > MIN_SCORE && p2.score > MIN_SCORE) {
        path.moveTo(p1.screenX, p1.screenY);
        path.lineTo(p2.screenX, p2.screenY);
      }
    }
    return path;
  }, [keypoints]);

  if (!font || keypoints.length === 0) return null;

  return (
    <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
      {/* Skeleton bones — bright green */}
      <Path
        path={skeletonPath}
        color="#00FF00"
        style="stroke"
        strokeWidth={4}
        strokeJoin="round"
        strokeCap="round"
        opacity={0.85}
      />
      {/* Keypoint dots + labels */}
      {keypoints.map((kp, idx) => {
        if (kp.score <= MIN_SCORE) return null;
        const color = KEYPOINT_COLORS[idx];
        return (
          <React.Fragment key={idx}>
            {/* Filled dot */}
            <Circle cx={kp.screenX} cy={kp.screenY} r={6} color={color} />
            {/* White outline */}
            <Circle cx={kp.screenX} cy={kp.screenY} r={6} color="white" style="stroke" strokeWidth={1.5} />
            {/* Label */}
            <SkiaText
              x={kp.screenX + 8}
              y={kp.screenY - 8}
              text={KEYPOINT_NAMES[idx]}
              font={font}
              color={color}
            />
          </React.Fragment>
        );
      })}
    </Canvas>
  );
};
