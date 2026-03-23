import { IconSymbol } from "@/components/ui/icon-symbol";
import {
  useVideoQueue,
  type VideoItem,
  type VideoStatus,
} from "@/store/useVideoQueue";
import { router } from "expo-router";
import React from "react";
import {
  FlatList,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const STATUS_CONFIG: Record<
  VideoStatus,
  { label: string; color: string; bgColor: string }
> = {
  RECORDED: { label: "Recorded", color: "#A0A0A0", bgColor: "#2A2A2A" },
  ENCODING: { label: "Encoding", color: "#FFD60A", bgColor: "#3D3200" },
  READY: { label: "Ready", color: "#30D158", bgColor: "#0A3D1A" },
  UPLOADING: { label: "Uploading", color: "#64D2FF", bgColor: "#002B3D" },
  DONE: { label: "Done", color: "#30D158", bgColor: "#0A3D1A" },
  ERROR: { label: "Error", color: "#FF453A", bgColor: "#3D0A08" },
};

function formatTime(ts: number): string {
  const d = new Date(ts);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function VideoItemRow({ item }: { item: VideoItem }) {
  const { startEncoding, startUpload, retry, remove, dismiss } = useVideoQueue();
  const config = STATUS_CONFIG[item.status];

  return (
    <View style={styles.itemCard}>
      <View style={styles.itemHeader}>
        <View style={styles.itemMeta}>
          <Text style={styles.itemType}>
            {item.workoutType.toUpperCase()}
          </Text>
          <Text style={styles.itemTime}>{formatTime(item.createdAt)}</Text>
        </View>
        <View style={[styles.statusBadge, { backgroundColor: config.bgColor }]}>
          <Text style={[styles.statusText, { color: config.color }]}>
            {config.label}
          </Text>
        </View>
      </View>

      {/* Progress bar for active items */}
      {(item.status === "ENCODING" || item.status === "UPLOADING") && (
        <View style={styles.progressBarBg}>
          <View
            style={[
              styles.progressBarFill,
              {
                width: `${Math.round(item.progress * 100)}%`,
                backgroundColor: config.color,
              },
            ]}
          />
        </View>
      )}

      {/* Error message */}
      {item.status === "ERROR" && item.error && (
        <Text style={styles.errorText} numberOfLines={2}>
          {item.error}
        </Text>
      )}

      {/* Session ID */}
      <Text style={styles.sessionId} numberOfLines={1}>
        {item.sessionId}
      </Text>

      {/* Actions */}
      <View style={styles.actions}>
        {item.status === "RECORDED" && (
          <>
            <TouchableOpacity
              style={styles.actionBtnPrimary}
              onPress={() => startEncoding(item.id)}
            >
              <Text style={styles.actionBtnPrimaryText}>Encode</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnDestructive}
              onPress={() => remove(item.id)}
            >
              <IconSymbol name="trash" size={16} color="#FF453A" />
            </TouchableOpacity>
          </>
        )}

        {item.status === "READY" && (
          <TouchableOpacity
            style={styles.actionBtnPrimary}
            onPress={() => startUpload(item.id)}
          >
            <IconSymbol name="arrow.up.circle.fill" size={16} color="#000" />
            <Text style={styles.actionBtnPrimaryText}>Upload</Text>
          </TouchableOpacity>
        )}

        {item.status === "ERROR" && (
          <>
            <TouchableOpacity
              style={styles.actionBtnPrimary}
              onPress={() => retry(item.id)}
            >
              <IconSymbol name="arrow.clockwise" size={16} color="#000" />
              <Text style={styles.actionBtnPrimaryText}>Retry</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnDestructive}
              onPress={() => remove(item.id)}
            >
              <IconSymbol name="trash" size={16} color="#FF453A" />
              <Text style={styles.actionBtnDestructiveText}>Delete</Text>
            </TouchableOpacity>
          </>
        )}

        {item.status === "DONE" && (
          <TouchableOpacity
            style={styles.actionBtnSecondary}
            onPress={() => dismiss(item.id)}
          >
            <Text style={styles.actionBtnSecondaryText}>Dismiss</Text>
          </TouchableOpacity>
        )}

        {/* Always allow delete for non-active items */}
        {item.status === "READY" && (
          <TouchableOpacity
            style={styles.actionBtnDestructive}
            onPress={() => remove(item.id)}
          >
            <IconSymbol name="trash" size={16} color="#FF453A" />
          </TouchableOpacity>
        )}
      </View>
    </View>
  );
}

export default function QueueScreen() {
  const items = useVideoQueue((s) => s.items);

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>Video Queue</Text>
        <Text style={styles.badge}>{items.length}</Text>
      </View>

      {items.length === 0 ? (
        <View style={styles.emptyState}>
          <Text style={styles.emptyIcon}>📭</Text>
          <Text style={styles.emptyText}>No videos in queue</Text>
          <Text style={styles.emptySubtext}>
            Record a workout to get started
          </Text>
        </View>
      ) : (
        <FlatList
          data={items}
          keyExtractor={(item) => item.id}
          renderItem={({ item }) => <VideoItemRow item={item} />}
          contentContainerStyle={styles.list}
          showsVerticalScrollIndicator={false}
        />
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  header: {
    flexDirection: "row",
    alignItems: "center",
    padding: 20,
    borderBottomWidth: 1,
    borderBottomColor: "#222",
  },
  backBtn: { marginRight: 15 },
  title: { fontSize: 20, fontWeight: "bold", color: "#fff", flex: 1 },
  badge: {
    backgroundColor: "#333",
    color: "#fff",
    fontSize: 14,
    fontWeight: "bold",
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 12,
    overflow: "hidden",
  },
  list: { padding: 16, gap: 12 },
  itemCard: {
    backgroundColor: "#1A1A1A",
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: "#333",
  },
  itemHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 10,
  },
  itemMeta: { flex: 1 },
  itemType: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "700",
  },
  itemTime: {
    color: "#666",
    fontSize: 12,
    marginTop: 2,
  },
  statusBadge: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
  },
  statusText: {
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
  },
  progressBarBg: {
    height: 4,
    backgroundColor: "#333",
    borderRadius: 2,
    overflow: "hidden",
    marginBottom: 8,
  },
  progressBarFill: {
    height: "100%",
    borderRadius: 2,
  },
  errorText: {
    color: "#FF453A",
    fontSize: 12,
    marginBottom: 8,
  },
  sessionId: {
    color: "#555",
    fontSize: 11,
    fontFamily: "monospace",
    marginBottom: 12,
  },
  actions: {
    flexDirection: "row",
    gap: 10,
  },
  actionBtnPrimary: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: "#fff",
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
  },
  actionBtnPrimaryText: {
    color: "#000",
    fontSize: 14,
    fontWeight: "bold",
  },
  actionBtnSecondary: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: "#444",
  },
  actionBtnSecondaryText: {
    color: "#888",
    fontSize: 14,
  },
  actionBtnDestructive: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: "#3D0A08",
  },
  actionBtnDestructiveText: {
    color: "#FF453A",
    fontSize: 14,
  },
  emptyState: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    gap: 8,
  },
  emptyIcon: { fontSize: 48 },
  emptyText: { color: "#fff", fontSize: 18, fontWeight: "bold" },
  emptySubtext: { color: "#666", fontSize: 14 },
});
