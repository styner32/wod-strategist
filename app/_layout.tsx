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
        {/* 1. Tab Navigator (Home, Train, History, Profile) */}
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />

        {/* Modals & sub-pages */}
        <Stack.Screen name="profile" options={{ title: "Profile", presentation: "modal" }} />
        <Stack.Screen name="profiles" options={{ title: "Profiles", presentation: "modal" }} />
        <Stack.Screen name="workout/setup" options={{ title: "Setup", presentation: "modal" }} />
        <Stack.Screen
          name="workout/visionTestPage"
          options={{
            headerShown: false,
            presentation: "fullScreenModal",
          }}
        />
        <Stack.Screen name="upload/index" options={{ headerShown: false }} />
        <Stack.Screen name="queue" options={{ headerShown: false }} />
      </Stack>

      {/* Global overlay — visible on all screens */}
      <VideoQueueOverlay />
    </SafeAreaProvider>
  );
}
