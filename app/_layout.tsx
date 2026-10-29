import { flushEnvironmentUploads } from "@/features/environment/store";
// src/app/_layout.tsx
import 'react-native-get-random-values'; // Must be first — polyfills crypto.getRandomValues for ULID
import 'react-native-worklets-core';
import '@/features/i18n'; // Initialize i18n (side-effect: sets locale)

import { VideoQueueOverlay } from "@/components/VideoQueueOverlay";
import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useAuthStore } from "@/features/auth/useAuthStore";
import { useProfileStore } from "@/store/useProfileStore";
import { flushPendingUploads } from "@/features/debug/telemetryUpload";
import { flushAppleAiUploads } from "@/features/ai-coach/appleAiUpload";
import {
  flushSensorUploads,
  startPeriodicSensorUpload,
} from "@/features/health/polar/sensorTelemetryUpload";
import { Redirect, Stack } from "expo-router";
import * as ScreenOrientation from "expo-screen-orientation";
import { StatusBar } from "expo-status-bar";
import { useEffect } from "react";
import { ActivityIndicator, AppState, StyleSheet, View } from "react-native";
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

  // Once logged in, hydrate profiles + flush telemetry + start periodic sensor queue sync
  useEffect(() => {
    if (isLoggedIn) {
      useProfileStore.getState().hydrate();
      flushPendingUploads().catch(() => {});
      flushSensorUploads().catch(() => {});
      flushAppleAiUploads().catch(() => {});
      flushEnvironmentUploads().catch(() => {});
      const appleAiRetry = AppState.addEventListener("change", state => {
        if (state === "active") { flushAppleAiUploads().catch(() => {}); flushEnvironmentUploads().catch(() => {}); }
      });
      const stopPeriodic = startPeriodicSensorUpload(30000);
      return () => {
        stopPeriodic();
        appleAiRetry.remove();
      };
    }
  }, [isLoggedIn]);

  // Show loading spinner while checking SecureStore
  if (!isReady) {
    return (
      <SafeAreaProvider>
        <StatusBar style="dark" />
        <View style={styles.splash}>
          <ActivityIndicator size="large" color={TacticalTheme.light.electricBlue} />
        </View>
      </SafeAreaProvider>
    );
  }

  return (
    <SafeAreaProvider>
      <StatusBar style="dark" />
      <Stack
        key={locale}
        screenOptions={{
          headerStyle: { backgroundColor: TacticalTheme.light.canvas },
          headerTintColor: TacticalTheme.light.ink,
          headerTitleStyle: { fontWeight: "800", color: TacticalTheme.light.ink },
          headerShadowVisible: false,
          contentStyle: { backgroundColor: TacticalTheme.light.canvas },
        }}
      >
        {/* Auth screens */}
        <Stack.Screen name="auth/login" options={{ headerShown: false }} />
        <Stack.Screen name="auth/signup" options={{ headerShown: false }} />

        {/* 1. Tab Navigator (Home, Train, History, Profile) */}
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />

        {/* Sub-pages (consistent right-slide card push) & fullScreenModals */}
        <Stack.Screen
          name="profile-edit"
          options={{
            headerShown: false,
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
        />
        <Stack.Screen
          name="profiles"
          options={{
            headerShown: false,
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
        />
        <Stack.Screen
          name="workout/setup"
          options={{
            headerShown: false,
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
        />
        <Stack.Screen
          name="workout/visionTestPage"
          options={{
            headerShown: false,
            presentation: "fullScreenModal",
            contentStyle: { backgroundColor: "#000" },
          }}
        />
        <Stack.Screen
          name="workout/player"
          options={{
            headerShown: false,
            presentation: "fullScreenModal",
            contentStyle: { backgroundColor: "#000" },
          }}
        />
        <Stack.Screen
          name="upload/index"
          options={{
            headerShown: false,
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
        />
        <Stack.Screen
          name="queue"
          options={{
            headerShown: false,
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
        />
        <Stack.Screen
          name="settings/deleteAccount"
          options={{
            headerShown: false,
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
        />
        <Stack.Screen
          name="stretch/[key]"
          options={{
            title: t("stretches.detailTitle"),
            presentation: "card",
            animation: "slide_from_right",
            contentStyle: { backgroundColor: TacticalTheme.light.canvas },
          }}
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
    backgroundColor: TacticalTheme.light.canvas,
    justifyContent: "center",
    alignItems: "center",
  },
});
