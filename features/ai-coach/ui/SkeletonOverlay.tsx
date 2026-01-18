import { Canvas, Path, Skia } from '@shopify/react-native-skia';
import React from 'react';
import { StyleSheet } from 'react-native';
import { SharedValue, useDerivedValue } from 'react-native-reanimated';

const CONNECTIONS = [
  [0, 1], [0, 2], [1, 3], [2, 4],
  [5, 6], [5, 7], [7, 9], [6, 8], [8, 10],
  [5, 11], [6, 12], [11, 12],
  [11, 13], [13, 15], [12, 14], [14, 16]
];

interface Props {
  pose: SharedValue<number[]>;
  width: number;
  height: number;
  cameraPosition: 'front' | 'back';
}

export const SkeletonOverlay = ({ pose, width, height, cameraPosition }: Props) => {
  
  const skeletonPath = useDerivedValue(() => {
    const path = Skia.Path.Make();
    const data = pose.value;
    
    // 🚨 [수정] 기준을 0으로 변경 (무조건 그리기)
    const threshold = 0.0; 

    if (!data || data.length === 0) return path;

    const getPoint = (idx: number) => {
      const y = data[idx * 3];
      const x = data[idx * 3 + 1];
      // const s = data[idx * 3 + 2]; // 점수는 무시
      
      const finalX = cameraPosition === 'front' ? (1 - x) : x;
      return { x: finalX * width, y: y * height };
    };

    // 1. 뼈대 연결
    for (const [start, end] of CONNECTIONS) {
      const p1 = getPoint(start);
      const p2 = getPoint(end);
      path.moveTo(p1.x, p1.y);
      path.lineTo(p2.x, p2.y);
    }
    
    // 2. 관절 점
    for (let i = 0; i < 17; i++) {
        const p = getPoint(i);
        path.addCircle(p.x, p.y, 5);
    }

    return path;
  }, [pose, width, height, cameraPosition]);

  return (
    <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
      <Path path={skeletonPath} color="#00FF00" style="stroke" strokeWidth={3} strokeJoin="round" strokeCap="round"/>
      <Path path={skeletonPath} color="#00FF00" style="fill" />
    </Canvas>
  );
};