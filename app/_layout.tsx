// src/app/_layout.tsx
import 'react-native-get-random-values'; // Must be first — polyfills crypto.getRandomValues for ULID
import 'react-native-worklets-core';
import '@/features/i18n'; // Initialize i18n (side-effect: sets locale)

import { VideoQueueOverlay } from "@/components/VideoQueueOverlay";
import { t, useLocale } from "@/features/i18n";
import { useProfileStore } from "@/store/useProfileStore";
import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { SafeAreaProvider } from "react-native-safe-area-context";

export default function RootLayout() {
  const locale = useLocale(); // triggers re-render on language switch

  useEffect(() => {
    useProfileStore.getState().hydrate();
  }, []);

  return (
    <SafeAreaProvider>
      <StatusBar style="light" />
      <Stack
        key={locale}
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
        <Stack.Screen name="profile" options={{ title: t("tabs.profile"), presentation: "modal" }} />
        <Stack.Screen name="profiles" options={{ title: t("profiles.title"), presentation: "modal" }} />
        <Stack.Screen name="workout/setup" options={{ title: t("setup.title"), presentation: "modal" }} />
        <Stack.Screen
          name="workout/visionTestPage"
          options={{
            headerShown: false,
            presentation: "fullScreenModal",
          }}
        />
        <Stack.Screen
          name="workout/player"
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
