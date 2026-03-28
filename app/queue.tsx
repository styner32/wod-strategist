import { IconSymbol } from "@/components/ui/icon-symbol";
import { useOrphanedVideos } from "@/store/useOrphanedVideos";
import {
  useVideoQueue,
  type VideoItem,
  type VideoStatus,
} from "@/store/useVideoQueue";
import { router } from "expo-router";
import React, { useEffect, useState } from "react";
import {
  Alert,
  ScrollView,
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
  ENCODED: { label: "Ready", color: "#30D158", bgColor: "#0A3D1A" },
  UPLOADING: { label: "Uploading", color: "#64D2FF", bgColor: "#002B3D" },
  UPLOADED: { label: "Uploaded", color: "#30D158", bgColor: "#0A3D1A" },
};

function formatTime(ts: number): string {
  const d = new Date(ts);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function VideoItemRow({ item }: { item: VideoItem }) {
  const { startEncoding, startUpload, cancelUpload, remove, dismiss, saveToGallery } = useVideoQueue();
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

      {/* Error message (shown inline, clears on next action) */}
      {item.error && (
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

        {item.status === "ENCODED" && (
          <>
            <TouchableOpacity
              style={styles.actionBtnPrimary}
              onPress={() => startUpload(item.id)}
            >
              <IconSymbol name="arrow.up.circle.fill" size={16} color="#000" />
              <Text style={styles.actionBtnPrimaryText}>
                Upload{item.compressedSize ? ` (${formatBytes(item.compressedSize)})` : ""}
              </Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnSecondary}
              onPress={() => startEncoding(item.id)}
            >
              <Text style={styles.actionBtnSecondaryText}>Re-encode</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnDestructive}
              onPress={() => remove(item.id)}
            >
              <IconSymbol name="trash" size={16} color="#FF453A" />
            </TouchableOpacity>
          </>
        )}

        {item.status === "UPLOADING" && (
          <TouchableOpacity
            style={styles.actionBtnDestructive}
            onPress={() => cancelUpload(item.id)}
          >
            <IconSymbol name="xmark.circle.fill" size={16} color="#FF453A" />
            <Text style={styles.actionBtnDestructiveText}>Cancel Upload</Text>
          </TouchableOpacity>
        )}

        {item.status === "UPLOADED" && (
          <>
            <TouchableOpacity
              style={styles.actionBtnSecondary}
              onPress={() => startUpload(item.id)}
            >
              <Text style={styles.actionBtnSecondaryText}>Re-upload</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnSecondary}
              onPress={() => startEncoding(item.id)}
            >
              <Text style={styles.actionBtnSecondaryText}>Re-encode</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnPrimary}
              onPress={() => dismiss(item.id)}
            >
              <Text style={styles.actionBtnPrimaryText}>Dismiss</Text>
            </TouchableOpacity>
          </>
        )}

        {/* Save to Gallery */}
        {!item.gallerySaved && (
          <TouchableOpacity
            style={styles.actionBtnSecondary}
            onPress={async () => {
              const ok = await saveToGallery(item.id);
              if (ok) {
                Alert.alert("Saved", "Video saved to gallery.");
              } else {
                Alert.alert("Failed", "Could not save to gallery. Check device storage.");
              }
            }}
          >
            <Text style={styles.actionBtnSecondaryText}>📱 Save to Gallery</Text>
          </TouchableOpacity>
        )}
      </View>
    </View>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export default function QueueScreen() {
  const items = useVideoQueue((s) => s.items);
  const { orphans, scanning, scan, deleteFile, saveToGallery, deleteAll } =
    useOrphanedVideos();
  const [showOrphans, setShowOrphans] = useState(true);

  useEffect(() => {
    scan();
  }, [scan]);

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>Video Queue</Text>
        <Text style={styles.badge}>{items.length}</Text>
      </View>

      <ScrollView contentContainerStyle={styles.list} showsVerticalScrollIndicator={false}>
        {/* Queue Items */}
        {items.length === 0 ? (
          <View style={styles.emptyState}>
            <Text style={styles.emptyIcon}>📭</Text>
            <Text style={styles.emptyText}>No videos in queue</Text>
            <Text style={styles.emptySubtext}>
              Record a workout to get started
            </Text>
          </View>
        ) : (
          items.map((item) => <VideoItemRow key={item.id} item={item} />)
        )}

        {/* Orphaned Files Section */}
        <TouchableOpacity
          style={styles.orphanHeader}
          onPress={() => setShowOrphans(!showOrphans)}
        >
          <Text style={styles.orphanTitle}>
            {showOrphans ? "▼" : "▶"} Orphaned Files
          </Text>
          <Text style={styles.orphanCount}>
            {scanning ? "Scanning..." : `${orphans.length} file${orphans.length !== 1 ? "s" : ""}`}
          </Text>
        </TouchableOpacity>

        {showOrphans && (
          <>
            {orphans.length === 0 && !scanning ? (
              <View style={styles.orphanEmpty}>
                <Text style={styles.orphanEmptyText}>No orphaned files found ✓</Text>
              </View>
            ) : (
              orphans.map((orphan) => (
                <View key={orphan.path} style={styles.orphanCard}>
                  <View style={styles.orphanInfo}>
                    <Text style={styles.orphanName} numberOfLines={1}>
                      {orphan.name}
                    </Text>
                    <Text style={styles.orphanSize}>
                      {formatBytes(orphan.size)}
                    </Text>
                  </View>
                  <View style={styles.orphanActions}>
                    <TouchableOpacity
                      style={styles.actionBtnSecondary}
                      onPress={async () => {
                        const ok = await saveToGallery(orphan.path);
                        if (ok) {
                          Alert.alert("Saved", "Saved to gallery and deleted temp file.");
                        } else {
                          Alert.alert("Failed", "Could not save to gallery. Check device storage.");
                        }
                      }}
                    >
                      <Text style={styles.actionBtnSecondaryText}>📱 Gallery</Text>
                    </TouchableOpacity>
                    <TouchableOpacity
                      style={styles.actionBtnDestructive}
                      onPress={() => {
                        Alert.alert(
                          "Delete File",
                          `Delete ${orphan.name}?`,
                          [
                            { text: "Cancel", style: "cancel" },
                            {
                              text: "Delete",
                              style: "destructive",
                              onPress: () => deleteFile(orphan.path),
                            },
                          ]
                        );
                      }}
                    >
                      <IconSymbol name="trash" size={14} color="#FF453A" />
                    </TouchableOpacity>
                  </View>
                </View>
              ))
            )}

            {orphans.length > 1 && (
              <TouchableOpacity
                style={styles.deleteAllBtn}
                onPress={() => {
                  Alert.alert(
                    "Delete All Orphans",
                    `Delete ${orphans.length} orphaned video files?`,
                    [
                      { text: "Cancel", style: "cancel" },
                      {
                        text: "Delete All",
                        style: "destructive",
                        onPress: deleteAll,
                      },
                    ]
                  );
                }}
              >
                <Text style={styles.deleteAllText}>🗑️ Delete All Orphans</Text>
              </TouchableOpacity>
            )}

            <TouchableOpacity
              style={styles.rescanBtn}
              onPress={scan}
              disabled={scanning}
            >
              <Text style={styles.rescanText}>
                {scanning ? "Scanning..." : "🔄 Rescan"}
              </Text>
            </TouchableOpacity>
          </>
        )}
      </ScrollView>
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
    flexWrap: "wrap",
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
    justifyContent: "center",
    alignItems: "center",
    gap: 8,
    paddingVertical: 40,
  },
  emptyIcon: { fontSize: 48 },
  emptyText: { color: "#fff", fontSize: 18, fontWeight: "bold" },
  emptySubtext: { color: "#666", fontSize: 14 },

  // Orphaned files section
  orphanHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingVertical: 14,
    paddingHorizontal: 4,
    marginTop: 20,
    borderTopWidth: 1,
    borderTopColor: "#222",
  },
  orphanTitle: {
    color: "#aaa",
    fontSize: 14,
    fontWeight: "700",
  },
  orphanCount: {
    color: "#666",
    fontSize: 12,
  },
  orphanEmpty: {
    alignItems: "center",
    paddingVertical: 16,
  },
  orphanEmptyText: {
    color: "#555",
    fontSize: 13,
  },
  orphanCard: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    backgroundColor: "#1A1A1A",
    borderRadius: 12,
    padding: 12,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: "#2A2A2A",
  },
  orphanInfo: {
    flex: 1,
    marginRight: 10,
  },
  orphanName: {
    color: "#ccc",
    fontSize: 12,
    fontFamily: "monospace",
  },
  orphanSize: {
    color: "#666",
    fontSize: 11,
    marginTop: 2,
  },
  orphanActions: {
    flexDirection: "row",
    gap: 8,
  },
  deleteAllBtn: {
    alignItems: "center",
    paddingVertical: 10,
    marginTop: 4,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: "#3D0A08",
  },
  deleteAllText: {
    color: "#FF453A",
    fontSize: 13,
    fontWeight: "600",
  },
  rescanBtn: {
    alignItems: "center",
    paddingVertical: 10,
    marginTop: 4,
  },
  rescanText: {
    color: "#666",
    fontSize: 13,
  },
});
