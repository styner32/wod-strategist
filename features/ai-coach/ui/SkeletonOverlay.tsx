import { Canvas, Path, Skia } from '@shopify/react-native-skia';
import React from 'react';
import { StyleSheet } from 'react-native';
import { SharedValue, useDerivedValue } from 'react-native-reanimated';

// 17개 관절 연결도 (MoveNet 기준)
const CONNECTIONS = [
  [0, 1], [0, 2], [1, 3], [2, 4],       // 얼굴
  [5, 6], [5, 7], [7, 9], [6, 8], [8, 10], // 팔/어깨
  [5, 11], [6, 12], [11, 12],           // 몸통
  [11, 13], [13, 15], [12, 14], [14, 16] // 다리
];

interface Props {
  pose: SharedValue<number[]>;
  width: number;
  height: number;
}

export const SkeletonOverlay = ({ pose, width, height }: Props) => {
  
  const skeletonPath = useDerivedValue(() => {
    const path = Skia.Path.Make();
    const data = pose.value;
    if (!data || data.length === 0) return path;

    const getPoint = (idx: number) => {
      // y, x, score 순서
      const y = data[idx * 3];
      const x = data[idx * 3 + 1];
      const s = data[idx * 3 + 2];
      // 후방 카메라(back) 기준: x 그대로 사용
      return { x: x * width, y: y * height, s };
    };

    // 선 그리기 (신뢰도 0.3 이상만)
    for (const [start, end] of CONNECTIONS) {
      const p1 = getPoint(start);
      const p2 = getPoint(end);
      if (p1.s > 0.3 && p2.s > 0.3) {
        path.moveTo(p1.x, p1.y);
        path.lineTo(p2.x, p2.y);
      }
    }
    return path;
  }, [width, height]);

  return (
    <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
      <Path
        path={skeletonPath}
        color="#00FF00" // 형광 초록
        style="stroke"
        strokeWidth={2}
        strokeJoin="round"
        strokeCap="round"
        opacity={0.6}
      />
    </Canvas>
  );
};