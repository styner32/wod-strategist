// src/app/_layout.tsx
import 'react-native-worklets-core';

import { VideoQueueOverlay } from "@/components/VideoQueueOverlay";
import { useProfileStore } from "@/store/useProfileStore";
import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { SafeAreaProvider } from "react-native-safe-area-context";

export default function RootLayout() {
  useEffect(() => {
    useProfileStore.getState().hydrate();
  }, []);

  return (
    <SafeAreaProvider>
      <StatusBar style="light" />
      <Stack
        screenOptions={{
          headerStyle: { backgroundColor: "#000" },
          headerTintColor: "#fff",
          headerTitleStyle: { fontWeight: "bold" },
          contentStyle: { backgroundColor: "#000" },
        }}
      >
        {/* 1. 메인 대시보드 */}
        <Stack.Screen name="index" options={{ title: "WOD Strategist" }} />

        {/* 1.1 프로필 설정 */}
        <Stack.Screen name="profile" options={{ title: "Profile", presentation: "modal" }} />

        {/* 1.2 프로필 목록 */}
        <Stack.Screen name="profiles" options={{ title: "Profiles", presentation: "modal" }} />

        {/* 1.5. 워크아웃 설정 */}
        <Stack.Screen name="workout/setup" options={{ title: "Setup", presentation: "modal" }} />

        {/* 2. 비전 테스트 페이지 (카메라 화면이므로 헤더 숨김) */}
        <Stack.Screen
          name="workout/visionTestPage"
          options={{
            headerShown: false,
            presentation: "fullScreenModal",
          }}
        />

        {/* 3. 분석 이력 페이지 */}
        <Stack.Screen name="history" options={{ title: "History" }} />

        {/* 4. 비디오 업로드 페이지 */}
        <Stack.Screen name="upload/index" options={{ headerShown: false }} />

        {/* 5. 비디오 큐 페이지 */}
        <Stack.Screen name="queue" options={{ headerShown: false }} />
      </Stack>

      {/* Global overlay — visible on all screens */}
      <VideoQueueOverlay />
    </SafeAreaProvider>
  );
}
