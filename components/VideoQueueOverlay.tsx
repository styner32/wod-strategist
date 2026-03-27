import { useIsProcessing, useQueueCount, useVideoQueue } from "@/store/useVideoQueue";
import { router } from "expo-router";
import React from "react";
import {
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";

/**
 * A floating pill overlay rendered at the root layout level.
 * Shows active queue count and current processing status.
 * Tap to navigate to the queue list screen.
 * Only visible when there are items in the queue.
 */
export function VideoQueueOverlay() {
  const count = useQueueCount();
  const isProcessing = useIsProcessing();
  const items = useVideoQueue((s) => s.items);

  if (count === 0) return null;

  // Get the first active item for status text
  const activeItem = items.find(
    (i) => i.status === "ENCODING" || i.status === "UPLOADING"
  );
  const readyCount = items.filter((i) => i.status === "ENCODED").length;
  const uploadedCount = items.filter((i) => i.status === "UPLOADED").length;
  const recordedCount = items.filter((i) => i.status === "RECORDED").length;
  // Check for items with errors (any state can have an error message)
  const errorCount = items.filter((i) => !!i.error).length;

  let statusText = "";
  let statusColor = "#30D158";
  if (activeItem) {
    const pct = Math.round(activeItem.progress * 100);
    if (activeItem.status === "ENCODING") {
      statusText = `Encoding... ${pct}%`;
      statusColor = "#FFD60A";
    } else {
      statusText = `Uploading... ${pct}%`;
      statusColor = "#64D2FF";
    }
  } else if (errorCount > 0) {
    statusText = `${errorCount} need attention`;
    statusColor = "#FF453A";
  } else if (readyCount > 0) {
    statusText = `${readyCount} ready to upload`;
    statusColor = "#30D158";
  } else if (recordedCount > 0) {
    statusText = `${recordedCount} awaiting encode`;
    statusColor = "#A0A0A0";
  } else if (uploadedCount > 0) {
    statusText = `${uploadedCount} uploaded`;
    statusColor = "#30D158";
  }

  return (
    <TouchableOpacity
      style={[styles.pill, { borderColor: statusColor }]}
      onPress={() => router.push("/queue" as any)}
      activeOpacity={0.8}
    >
      <View style={[styles.dot, { backgroundColor: statusColor }]} />
      <Text style={styles.pillText} numberOfLines={1}>
        {statusText}
      </Text>
      <View style={styles.countBadge}>
        <Text style={styles.countText}>{count}</Text>
      </View>
    </TouchableOpacity>
  );
}

const styles = StyleSheet.create({
  pill: {
    position: "absolute",
    bottom: 100,
    alignSelf: "center",
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "rgba(26, 26, 26, 0.95)",
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: 24,
    borderWidth: 1,
    gap: 8,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.5,
    shadowRadius: 8,
    elevation: 10,
    zIndex: 999,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  pillText: {
    color: "#fff",
    fontSize: 13,
    fontWeight: "600",
    maxWidth: 200,
  },
  countBadge: {
    backgroundColor: "#333",
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 10,
  },
  countText: {
    color: "#fff",
    fontSize: 12,
    fontWeight: "bold",
  },
});
