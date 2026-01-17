import { Canvas, Path, Skia } from "@shopify/react-native-skia";
import React from "react";
import { StyleSheet } from "react-native";
import { SharedValue, useDerivedValue } from "react-native-reanimated";

// MoveNet 관절 연결 정보
const CONNECTIONS = [
  [0, 1],
  [0, 2],
  [1, 3],
  [2, 4],
  [5, 6],
  [5, 7],
  [7, 9],
  [6, 8],
  [8, 10],
  [5, 11],
  [6, 12],
  [11, 12],
  [11, 13],
  [13, 15],
  [12, 14],
  [14, 16],
];

interface Props {
  pose: SharedValue<number[]>;
  width: number;
  height: number;
  cameraPosition: "front" | "back"; // Prop 추가
}

export const SkeletonOverlay = ({
  pose,
  width,
  height,
  cameraPosition,
}: Props) => {
  const skeletonPath = useDerivedValue(() => {
    const path = Skia.Path.Make();
    const data = pose.value;
    const threshold = 0.15;

    const getPoint = (idx: number) => {
      const y = data[idx * 3];
      const x = data[idx * 3 + 1];
      const s = data[idx * 3 + 2];

      // 🔄 [핵심 변경] 카메라 위치에 따른 X좌표 처리
      // Front(셀카): 거울처럼 보여야 함 -> (1 - x)
      // Back(후면): 있는 그대로 보여야 함 -> x
      const finalX = cameraPosition === "front" ? 1 - x : x;

      return { x: finalX * width, y: y * height, s };
    };

    // 1. 뼈대 그리기
    for (const [start, end] of CONNECTIONS) {
      const p1 = getPoint(start);
      const p2 = getPoint(end);
      if (p1.s > threshold && p2.s > threshold) {
        path.moveTo(p1.x, p1.y);
        path.lineTo(p2.x, p2.y);
      }
    }

    // 2. 관절 점 그리기
    for (let i = 0; i < 17; i++) {
      const p = getPoint(i);
      if (p.s > threshold) {
        path.addCircle(p.x, p.y, 5);
      }
    }

    return path;
  }, [width, height, cameraPosition]);

  return (
    <Canvas style={StyleSheet.absoluteFill} pointerEvents="none">
      <Path
        path={skeletonPath}
        color="#00FF00"
        style="stroke"
        strokeWidth={4}
        strokeJoin="round"
        strokeCap="round"
      />
    </Canvas>
  );
};
