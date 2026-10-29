import { IconSymbol } from "@/components/ui/icon-symbol";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useOrphanedVideos } from "@/store/useOrphanedVideos";
import {
  useVideoQueue,
  type VideoItem,
  type VideoStatus,
} from "@/store/useVideoQueue";
import { router } from "expo-router";
import { StatusBar } from "expo-status-bar";
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

const T = TacticalTheme.light;
const monoFont = Fonts?.mono ?? "monospace";

const STATUS_CONFIG: Record<
  VideoStatus,
  { labelKey: string; color: string; bgColor: string; borderColor: string }
> = {
  RECORDED: {
    labelKey: "queue.statusRecorded",
    color: T.inkSecondary,
    bgColor: T.surfaceContainer,
    borderColor: T.borderStrong,
  },
  ENCODING: {
    labelKey: "queue.statusEncoding",
    color: T.statusWarning,
    bgColor: T.statusWarningBg,
    borderColor: T.statusWarningBorder,
  },
  ENCODED: {
    labelKey: "queue.statusReady",
    color: T.statusFresh,
    bgColor: T.statusFreshBg,
    borderColor: T.statusFreshBorder,
  },
  UPLOADING: {
    labelKey: "queue.statusUploading",
    color: T.electricBlue,
    bgColor: T.electricBlueBg,
    borderColor: T.electricBlueBorder,
  },
  UPLOADED: {
    labelKey: "queue.statusUploaded",
    color: T.statusFresh,
    bgColor: T.statusFreshBg,
    borderColor: T.statusFreshBorder,
  },
};

function formatTime(ts: number): string {
  const d = new Date(ts);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function VideoItemRow({ item }: { item: VideoItem }) {
  const {
    startEncoding,
    startUpload,
    cancelUpload,
    remove,
    dismiss,
    saveToGallery,
  } = useVideoQueue();
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
        <View
          style={[
            styles.statusBadge,
            { backgroundColor: config.bgColor, borderColor: config.borderColor },
          ]}
        >
          <Text style={[styles.statusText, { color: config.color }]}>
            {t(config.labelKey)}
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
              <Text style={styles.actionBtnPrimaryText}>{t("queue.encode")}</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnDestructive}
              onPress={() => remove(item.id)}
            >
              <IconSymbol name="trash" size={16} color={T.statusExhausted} />
            </TouchableOpacity>
          </>
        )}

        {item.status === "ENCODED" && (
          <>
            <TouchableOpacity
              style={styles.actionBtnPrimary}
              onPress={() => startUpload(item.id)}
            >
              <IconSymbol
                name="arrow.up.circle.fill"
                size={16}
                color={T.voltInk}
              />
              <Text style={styles.actionBtnPrimaryText}>
                {t("queue.upload")}
                {item.compressedSize
                  ? ` (${formatBytes(item.compressedSize)})`
                  : ""}
              </Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnSecondary}
              onPress={() => startEncoding(item.id)}
            >
              <Text style={styles.actionBtnSecondaryText}>
                {t("queue.reEncode")}
              </Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnDestructive}
              onPress={() => remove(item.id)}
            >
              <IconSymbol name="trash" size={16} color={T.statusExhausted} />
            </TouchableOpacity>
          </>
        )}

        {item.status === "UPLOADING" && (
          <TouchableOpacity
            style={styles.actionBtnDestructive}
            onPress={() => cancelUpload(item.id)}
          >
            <IconSymbol
              name="xmark.circle.fill"
              size={16}
              color={T.statusExhausted}
            />
            <Text style={styles.actionBtnDestructiveText}>
              {t("queue.cancelUpload")}
            </Text>
          </TouchableOpacity>
        )}

        {item.status === "UPLOADED" && (
          <>
            <TouchableOpacity
              style={styles.actionBtnSecondary}
              onPress={() => startUpload(item.id)}
            >
              <Text style={styles.actionBtnSecondaryText}>
                {t("queue.reUpload")}
              </Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnSecondary}
              onPress={() => startEncoding(item.id)}
            >
              <Text style={styles.actionBtnSecondaryText}>
                {t("queue.reEncode")}
              </Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={styles.actionBtnPrimary}
              onPress={() => dismiss(item.id)}
            >
              <Text style={styles.actionBtnPrimaryText}>
                {t("queue.dismiss")}
              </Text>
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
                Alert.alert(t("common.saved"), t("queue.savedToGallery"));
              } else {
                Alert.alert(t("queue.failed"), t("queue.failedGallery"));
              }
            }}
          >
            <Text style={styles.actionBtnSecondaryText}>
              {t("queue.saveToGallery")}
            </Text>
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
  useLocale();
  const items = useVideoQueue((s) => s.items);
  const { orphans, scanning, scan, deleteFile, saveToGallery, deleteAll } =
    useOrphanedVideos();
  const [showOrphans, setShowOrphans] = useState(true);

  useEffect(() => {
    scan();
  }, [scan]);

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar style="dark" />
      <View style={styles.header}>
        <TouchableOpacity
          onPress={() => router.back()}
          style={styles.backBtn}
          accessibilityRole="button"
        >
          <IconSymbol name="chevron.left" size={22} color={T.ink} />
        </TouchableOpacity>
        <Text style={styles.title} numberOfLines={1}>
          {t("queue.title")}
        </Text>
        <View style={styles.badge}>
          <Text style={styles.badgeText}>{items.length}</Text>
        </View>
      </View>

      <ScrollView
        contentContainerStyle={styles.list}
        showsVerticalScrollIndicator={false}
      >
        {items.length === 0 ? (
          <View style={styles.emptyState}>
            <Text style={styles.emptyIcon}>📭</Text>
            <Text style={styles.emptyText}>{t("queue.emptyTitle")}</Text>
            <Text style={styles.emptySubtext}>{t("queue.emptyDesc")}</Text>
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
            {showOrphans ? "▼" : "▶"} {t("queue.orphanedFiles")}
          </Text>
          <Text style={styles.orphanCount}>
            {scanning
              ? t("queue.scanning")
              : t(
                  orphans.length === 1
                    ? "queue.fileCount"
                    : "queue.filesCount",
                  { count: orphans.length },
                )}
          </Text>
        </TouchableOpacity>

        {showOrphans && (
          <>
            {orphans.length === 0 && !scanning ? (
              <View style={styles.orphanEmpty}>
                <Text style={styles.orphanEmptyText}>
                  {t("queue.noOrphans")}
                </Text>
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
                          Alert.alert(
                            t("common.saved"),
                            t("queue.savedGalleryDeleted"),
                          );
                        } else {
                          Alert.alert(t("queue.failed"), t("queue.failedGallery"));
                        }
                      }}
                    >
                      <Text style={styles.actionBtnSecondaryText}>
                        {t("queue.gallery")}
                      </Text>
                    </TouchableOpacity>
                    <TouchableOpacity
                      style={styles.actionBtnDestructive}
                      onPress={() => {
                        Alert.alert(
                          t("queue.deleteFile"),
                          t("queue.deleteFileConfirm", { name: orphan.name }),
                          [
                            { text: t("common.cancel"), style: "cancel" },
                            {
                              text: t("common.delete"),
                              style: "destructive",
                              onPress: () => deleteFile(orphan.path),
                            },
                          ],
                        );
                      }}
                    >
                      <IconSymbol
                        name="trash"
                        size={14}
                        color={T.statusExhausted}
                      />
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
                    t("queue.deleteAllOrphans"),
                    t("queue.deleteAllConfirm", { count: orphans.length }),
                    [
                      { text: t("common.cancel"), style: "cancel" },
                      {
                        text: t("queue.deleteAll"),
                        style: "destructive",
                        onPress: deleteAll,
                      },
                    ],
                  );
                }}
              >
                <Text style={styles.deleteAllText}>
                  {t("queue.deleteAllOrphans")}
                </Text>
              </TouchableOpacity>
            )}

            <TouchableOpacity
              style={styles.rescanBtn}
              onPress={scan}
              disabled={scanning}
            >
              <Text style={styles.rescanText}>
                {scanning ? t("queue.scanning") : t("queue.rescan")}
              </Text>
            </TouchableOpacity>
          </>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: T.canvas },
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 18,
    paddingVertical: 14,
    gap: 12,
  },
  backBtn: {
    width: 38,
    height: 38,
    borderRadius: 12,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    alignItems: "center",
    justifyContent: "center",
  },
  title: {
    fontSize: 22,
    fontWeight: "900",
    letterSpacing: -0.5,
    color: T.ink,
    flex: 1,
    flexShrink: 1,
  },
  badge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 999,
  },
  badgeText: {
    color: T.electricBlue,
    fontSize: 13,
    fontWeight: "800",
    fontFamily: monoFont,
  },
  list: { paddingHorizontal: 18, paddingTop: 4, paddingBottom: 40, gap: 12 },
  itemCard: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: T.border,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  itemHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 10,
    gap: 8,
  },
  itemMeta: { flex: 1 },
  itemType: {
    color: T.ink,
    fontSize: 16,
    fontWeight: "900",
    flexShrink: 1,
  },
  itemTime: {
    color: T.inkMuted,
    fontSize: 12,
    fontWeight: "600",
    marginTop: 2,
  },
  statusBadge: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
    borderWidth: 1,
  },
  statusText: {
    fontSize: 11,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.4,
  },
  progressBarBg: {
    height: 6,
    backgroundColor: T.surfaceContainer,
    borderRadius: 3,
    overflow: "hidden",
    marginBottom: 8,
  },
  progressBarFill: {
    height: "100%",
    borderRadius: 3,
  },
  errorText: {
    color: T.statusExhausted,
    fontSize: 12,
    fontWeight: "600",
    marginBottom: 8,
  },
  sessionId: {
    color: T.inkMuted,
    fontSize: 11,
    fontFamily: monoFont,
    marginBottom: 12,
  },
  actions: {
    flexDirection: "row",
    gap: 8,
    flexWrap: "wrap",
  },
  actionBtnPrimary: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 14,
    paddingVertical: 9,
    borderRadius: 10,
  },
  actionBtnPrimaryText: {
    color: T.voltInk,
    fontSize: 13,
    fontWeight: "800",
  },
  actionBtnSecondary: {
    paddingHorizontal: 14,
    paddingVertical: 9,
    borderRadius: 10,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  actionBtnSecondaryText: {
    color: T.inkSecondary,
    fontSize: 13,
    fontWeight: "700",
  },
  actionBtnDestructive: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingHorizontal: 12,
    paddingVertical: 9,
    borderRadius: 10,
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
  },
  actionBtnDestructiveText: {
    color: T.statusExhausted,
    fontSize: 13,
    fontWeight: "700",
  },
  emptyState: {
    justifyContent: "center",
    alignItems: "center",
    gap: 8,
    paddingVertical: 48,
    paddingHorizontal: 24,
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
  },
  emptyIcon: { fontSize: 44 },
  emptyText: { color: T.ink, fontSize: 18, fontWeight: "800" },
  emptySubtext: { color: T.inkMuted, fontSize: 14 },

  // Orphaned files section
  orphanHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingVertical: 14,
    paddingHorizontal: 4,
    marginTop: 12,
    borderTopWidth: 1,
    borderTopColor: T.borderStrong,
    gap: 8,
  },
  orphanTitle: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "800",
    flexShrink: 1,
  },
  orphanCount: {
    color: T.inkMuted,
    fontSize: 12,
    fontWeight: "700",
  },
  orphanEmpty: {
    alignItems: "center",
    paddingVertical: 16,
  },
  orphanEmptyText: {
    color: T.inkMuted,
    fontSize: 13,
    fontWeight: "600",
  },
  orphanCard: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    backgroundColor: T.surface,
    borderRadius: 14,
    padding: 12,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: T.border,
  },
  orphanInfo: {
    flex: 1,
    marginRight: 10,
  },
  orphanName: {
    color: T.ink,
    fontSize: 12,
    fontWeight: "600",
    fontFamily: monoFont,
  },
  orphanSize: {
    color: T.inkMuted,
    fontSize: 11,
    marginTop: 2,
  },
  orphanActions: {
    flexDirection: "row",
    gap: 8,
  },
  deleteAllBtn: {
    alignItems: "center",
    paddingVertical: 12,
    marginTop: 4,
    borderRadius: 12,
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
  },
  deleteAllText: {
    color: T.statusExhausted,
    fontSize: 13,
    fontWeight: "800",
  },
  rescanBtn: {
    alignItems: "center",
    paddingVertical: 12,
    marginTop: 4,
  },
  rescanText: {
    color: T.electricBlue,
    fontSize: 13,
    fontWeight: "700",
  },
});
