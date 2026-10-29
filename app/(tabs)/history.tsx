import React, { useCallback, useRef } from "react";
import {
  NativeScrollEvent,
  NativeSyntheticEvent,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { StatusBar } from "expo-status-bar";
import { useLocalSearchParams } from "expo-router";

import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { HistoryList, useHistoryData } from "@/features/wod/ui/HistoryList";

const T = TacticalTheme.light;

export default function HistoryScreen() {
  useLocale();
  const { focusSessionId } = useLocalSearchParams<{ focusSessionId?: string }>();
  const { data, loading, refreshing, onRefresh, onArchive } = useHistoryData({
    limit: focusSessionId ? 100 : undefined,
  });

  const scrollViewRef = useRef<ScrollView>(null);
  const currentOffsetRef = useRef<number>(0);

  const handleScroll = useCallback((event: NativeSyntheticEvent<NativeScrollEvent>) => {
    currentOffsetRef.current = event.nativeEvent.contentOffset.y;
  }, []);

  return (
    <SafeAreaView style={styles.container} edges={["top", "left", "right"]}>
      <StatusBar style="dark" />
      <ScrollView
        ref={scrollViewRef}
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
        onScroll={handleScroll}
        scrollEventThrottle={16}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={onRefresh}
            tintColor={T.electricBlue}
          />
        }
      >
        <View style={styles.topBar}>
          <View style={styles.eyebrowRow}>
            <View style={styles.eyebrowDot} />
            <Text style={styles.eyebrowText}>{t("history.eyebrow")}</Text>
          </View>
          <View style={styles.titleRow}>
            <Text style={styles.title} numberOfLines={1}>
              {t("history.title")}
            </Text>
            <View style={styles.voltDot} />
          </View>
          <Text style={styles.subtitle}>{t("history.subtitle")}</Text>
        </View>

        <HistoryList
          data={data}
          loading={loading}
          onRefresh={onRefresh}
          onArchive={onArchive}
          focusSessionId={focusSessionId}
          scrollViewRef={scrollViewRef}
          currentOffsetRef={currentOffsetRef}
        />
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: T.canvas,
  },
  scrollContent: {
    paddingHorizontal: 18,
    paddingTop: 10,
    paddingBottom: 112,
  },
  topBar: {
    marginBottom: 16,
    gap: 2,
  },
  eyebrowRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    marginBottom: 2,
  },
  eyebrowDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: T.electricBlue,
  },
  eyebrowText: {
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 1.1,
    color: T.electricBlue,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  titleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  title: {
    fontSize: 24,
    fontWeight: "900",
    letterSpacing: -0.6,
    color: T.ink,
    flexShrink: 1,
  },
  voltDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
  },
  subtitle: {
    fontSize: 13,
    fontWeight: "600",
    color: T.inkMuted,
    marginTop: 2,
  },
});
