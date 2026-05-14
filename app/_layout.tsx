// src/app/_layout.tsx
import 'react-native-get-random-values'; // Must be first — polyfills crypto.getRandomValues for ULID
import 'react-native-worklets-core';
import '@/features/i18n'; // Initialize i18n (side-effect: sets locale)

import { VideoQueueOverlay } from "@/components/VideoQueueOverlay";
import { t, useLocale } from "@/features/i18n";
import { useAuthStore } from "@/features/auth/useAuthStore";
import { useProfileStore } from "@/store/useProfileStore";
import { flushPendingUploads } from "@/features/debug/telemetryUpload";
import { Redirect, Stack } from "expo-router";
import * as ScreenOrientation from "expo-screen-orientation";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";

export default function RootLayout() {
  const locale = useLocale(); // triggers re-render on language switch
  const isReady = useAuthStore((s) => s.isReady);
  const isLoggedIn = useAuthStore((s) => s.isLoggedIn);

  useEffect(() => {
    // Hydrate auth first — profile hydration happens after auth is confirmed
    useAuthStore.getState().hydrate();
    // Lock the entire app to portrait by default.
    // Individual screens (e.g. visionTestPage) can override this.
    ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP);
  }, []);

  // Once logged in, hydrate profiles + flush telemetry
  useEffect(() => {
    if (isLoggedIn) {
      useProfileStore.getState().hydrate();
      flushPendingUploads().catch(() => {});
    }
  }, [isLoggedIn]);

  // Show loading spinner while checking SecureStore
  if (!isReady) {
    return (
      <SafeAreaProvider>
        <StatusBar style="light" />
        <View style={styles.splash}>
          <ActivityIndicator size="large" color="#007AFF" />
        </View>
      </SafeAreaProvider>
    );
  }

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
        {/* Auth screens */}
        <Stack.Screen name="auth/login" options={{ headerShown: false }} />
        <Stack.Screen name="auth/signup" options={{ headerShown: false }} />

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
        <Stack.Screen
          name="settings/deleteAccount"
          options={{ title: t("auth.deleteAccount"), presentation: "modal" }}
        />
      </Stack>

      {/* Redirect to login when not authenticated */}
      {!isLoggedIn && <Redirect href="/auth/login" />}

      {/* Global overlay — visible on all screens */}
      {isLoggedIn && <VideoQueueOverlay />}
    </SafeAreaProvider>
  );
}

const styles = StyleSheet.create({
  splash: {
    flex: 1,
    backgroundColor: "#000",
    justifyContent: "center",
    alignItems: "center",
  },
});
