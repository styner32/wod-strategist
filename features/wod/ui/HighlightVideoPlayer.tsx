import React, { useCallback, useState } from "react";
import { TacticalTheme } from "@/constants/theme";
import { t } from "@/features/i18n";
import {
  ActivityIndicator,
  Alert,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { useVideoPlayer, VideoView } from "expo-video";
import * as FileSystem from "expo-file-system/legacy";
import * as MediaLibrary from "expo-media-library/legacy";

import { fetchHighlightDownloadURL } from "../api";
import type { HighlightResult } from "../history";

interface Props {
  highlight: HighlightResult;
  videoUrl: string; // signed URL
  onClose: () => void;
}

/**
 * Inline video player for highlight previews.
 * Shows the video with native controls, plus a save-to-gallery button.
 */
export function HighlightVideoPlayer({ highlight, videoUrl, onClose }: Props) {
  const [saving, setSaving] = useState(false);

  const player = useVideoPlayer(videoUrl, (p) => {
    p.loop = false;
    p.play();
  });

  const handleSaveToGallery = useCallback(async () => {
    try {
      setSaving(true);

      const { status } = await MediaLibrary.requestPermissionsAsync();
      if (status !== "granted") {
        Alert.alert(t("common.permissionRequired"), t("common.galleryPermission"));
        return;
      }

      const { download_url, filename } = await fetchHighlightDownloadURL(highlight.id);
      const localUri = FileSystem.cacheDirectory + filename;
      const { uri } = await FileSystem.downloadAsync(download_url, localUri);

      try {
        await MediaLibrary.saveToLibraryAsync(uri);
        Alert.alert(t("common.saved"), t("historyList.highlightSaved", { title: highlight.title }));
      } finally {
        try { await FileSystem.deleteAsync(uri, { idempotent: true }); } catch {}
      }
    } catch {
      Alert.alert(t("common.error"), t("common.failedSaveGallery"));
    } finally {
      setSaving(false);
    }
  }, [highlight]);

  return (
    <View style={styles.container}>
      {/* Header */}
      <View style={styles.header}>
        <Text style={styles.title}>{highlight.title}</Text>
        <Text style={styles.duration}>{Math.round(highlight.duration_sec)}s</Text>
        <TouchableOpacity onPress={onClose} style={styles.closeBtn} hitSlop={8}>
          <Text style={styles.closeBtnText}>✕</Text>
        </TouchableOpacity>
      </View>

      {/* Video */}
      <VideoView
        style={styles.video}
        player={player}
        contentFit="contain"
      />

      {/* Actions */}
      <View style={styles.actions}>
        <TouchableOpacity
          style={styles.saveBtn}
          onPress={handleSaveToGallery}
          disabled={saving}
        >
          {saving ? (
            <ActivityIndicator size="small" color={TacticalTheme.light.voltInk} />
          ) : (
            <Text style={styles.saveBtnText}>{t("player.saveToGallery")}</Text>
          )}
        </TouchableOpacity>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    marginTop: 10,
    borderRadius: 14,
    overflow: "hidden",
    backgroundColor: TacticalTheme.light.surfaceInverse,
    borderWidth: 1,
    borderColor: TacticalTheme.light.borderStrong,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 10,
    gap: 8,
  },
  title: {
    fontSize: 14,
    fontWeight: "800",
    color: TacticalTheme.light.inkInverse,
    flex: 1,
    flexShrink: 1,
  },
  duration: {
    fontSize: 12,
    fontWeight: "700",
    color: "#94A3B8",
  },
  closeBtn: {
    width: 28,
    height: 28,
    borderRadius: 14,
    backgroundColor: "rgba(255,255,255,0.14)",
    alignItems: "center",
    justifyContent: "center",
  },
  closeBtnText: {
    fontSize: 14,
    color: "#FFF",
    fontWeight: "700",
  },
  video: {
    width: "100%",
    aspectRatio: 16 / 9,
    backgroundColor: "#000",
  },
  actions: {
    flexDirection: "row",
    padding: 10,
    gap: 8,
  },
  saveBtn: {
    flex: 1,
    backgroundColor: TacticalTheme.light.volt,
    borderWidth: 1,
    borderColor: TacticalTheme.light.voltBorder,
    borderRadius: 10,
    paddingVertical: 11,
    alignItems: "center",
  },
  saveBtnText: {
    fontSize: 13,
    fontWeight: "900",
    letterSpacing: 0.4,
    textTransform: "uppercase",
    color: TacticalTheme.light.voltInk,
  },
});
