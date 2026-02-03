// src/app/_layout.tsx
import 'react-native-worklets-core';

import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { SafeAreaProvider } from "react-native-safe-area-context";

export default function RootLayout() {
  return (
    <SafeAreaProvider>
      <StatusBar style="light" />
      <Stack
        screenOptions={{
          headerStyle: { backgroundColor: "#000" }, // 다크 모드 헤더
          headerTintColor: "#fff",
          headerTitleStyle: { fontWeight: "bold" },
          contentStyle: { backgroundColor: "#000" }, // 배경 검정
        }}
      >
        {/* 1. 메인 대시보드 */}
        <Stack.Screen name="index" options={{ title: "WOD Strategist" }} />

        {/* 1.5. 워크아웃 설정 */}
        <Stack.Screen name="workout/setup" options={{ title: "Setup", presentation: "modal" }} />

        {/* 2. 비전 테스트 페이지 (카메라 화면이므로 헤더 숨김) */}
        <Stack.Screen
          name="workout/visionTestPage"
          options={{
            headerShown: false, // 전체화면 모드
            presentation: "fullScreenModal", // 모달 형태로 뜨도록 설정 (선택사항)
          }}
        />

        {/* 3. 분석 이력 페이지 */}
        <Stack.Screen name="history" options={{ title: "History" }} />

        {/* 4. 비디오 업로드 페이지 */}
        <Stack.Screen name="upload/index" options={{ headerShown: false }} />
      </Stack>
    </SafeAreaProvider>
  );
}
