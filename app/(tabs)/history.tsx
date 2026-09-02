import React, { useCallback, useRef } from "react";
import {
  NativeScrollEvent,
  NativeSyntheticEvent,
  RefreshControl,
  ScrollView,
  StyleSheet,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useLocalSearchParams } from "expo-router";

import { HistoryList, useHistoryData } from "@/features/wod/ui/HistoryList";

export default function HistoryScreen() {
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
    <SafeAreaView style={styles.container}>
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
            tintColor="#64D2FF"
          />
        }
      >
        <HistoryList
          data={data}
          loading={loading}
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
    backgroundColor: "#3A3A3C",
  },
  scrollContent: {
    padding: 20,
    paddingTop: 12,
    paddingBottom: 40,
  },
});
