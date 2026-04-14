import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";

import { t } from "@/features/i18n";
import { fetchVideoDownloadURL, fetchChunkAnalysis } from "@/features/wod/api";
import type { ChunkAnalysisResult } from "@/features/wod/api";
import {
  WorkoutVideoPlayer,
  type HighlightSegment,
} from "@/features/wod/ui/WorkoutVideoPlayer";
import { fetchAnalysisHistory } from "@/features/wod/history";

/**
 * Full-screen workout video player page.
 *
 * Route: /workout/player?sessionId=X&profileId=Y
 *
 * Loads the merged video, chunk analysis (guidance cues), and highlight
 * segments for a completed workout session.
 */
export default function WorkoutPlayerPage() {
  const { sessionId, profileId, videoKind } = useLocalSearchParams<{
    sessionId: string;
    profileId: string;
    videoKind?: string;
  }>();

  const [videoUrl, setVideoUrl] = useState<string | null>(null);
  const [chunks, setChunks] = useState<ChunkAnalysisResult[]>([]);
  const [highlights, setHighlights] = useState<HighlightSegment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const pid = parseInt(profileId ?? "0", 10);
  const kind = (videoKind as "merged" | "hardsubbed" | "encoded") || "merged";

  const loadData = useCallback(async () => {
    if (!sessionId || !pid) {
      setError(t("player.missingIds"));
      setLoading(false);
      return;
    }
    try {
      setLoading(true);
      setError(null);

      // Fetch video URL, chunks, and analysis (for highlights) in parallel
      const [videoRes, chunksRes, historyRes] = await Promise.allSettled([
        fetchVideoDownloadURL(sessionId, pid, kind),
        fetchChunkAnalysis(sessionId),
        fetchAnalysisHistory(pid),
      ]);

      // Video URL (required)
      if (videoRes.status === "fulfilled") {
        setVideoUrl(videoRes.value.download_url);
      } else {
        setError(t("player.failedLoadVideo"));
        setLoading(false);
        return;
      }

      // Chunk analysis (guidance cues — optional, may be empty)
      if (chunksRes.status === "fulfilled") {
        setChunks(chunksRes.value);
      }

      // Extract highlight_segments from the matching analysis result
      if (historyRes.status === "fulfilled") {
        const match = historyRes.value.find(
          (r) => r.session_id === sessionId && r.highlight_segments
        );
        if (match?.highlight_segments) {
          try {
            const parsed = JSON.parse(match.highlight_segments) as HighlightSegment[];
            setHighlights(parsed);
          } catch {
            // Ignore invalid JSON
          }
        }
      }
    } catch (e: any) {
      setError(e?.message || t("player.failedLoadVideo"));
    } finally {
      setLoading(false);
    }
  }, [sessionId, pid]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleClose = useCallback(() => {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace("/(tabs)/history");
    }
  }, []);

  // Build a human-readable label
  const sessionLabel = sessionId
    ? sessionId.split("-")[0]?.toUpperCase() || t("common.workout")
    : t("common.workout");

  if (loading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator size="large" color="#64D2FF" />
        <Text style={styles.loadingText}>{t("player.loadingVideo")}</Text>
      </View>
    );
  }

  if (error || !videoUrl) {
    return (
      <View style={styles.center}>
        <Text style={styles.errorIcon}>📹</Text>
        <Text style={styles.errorTitle}>{t("player.videoUnavailable")}</Text>
        <Text style={styles.errorMessage}>
          {error || t("player.videoLoadError")}
        </Text>
        <Text style={styles.backLink} onPress={handleClose}>
          {t("player.goBack")}
        </Text>
      </View>
    );
  }

  return (
    <WorkoutVideoPlayer
      videoUrl={videoUrl}
      sessionLabel={sessionLabel}
      chunks={chunks}
      highlightSegments={highlights}
      onClose={handleClose}
    />
  );
}

const styles = StyleSheet.create({
  center: {
    flex: 1,
    backgroundColor: "#000",
    justifyContent: "center",
    alignItems: "center",
    padding: 32,
    gap: 12,
  },
  loadingText: {
    fontSize: 14,
    color: "#888",
  },
  errorIcon: {
    fontSize: 48,
    marginBottom: 8,
  },
  errorTitle: {
    fontSize: 20,
    fontWeight: "700",
    color: "#FFF",
  },
  errorMessage: {
    fontSize: 14,
    color: "#888",
    textAlign: "center",
    lineHeight: 20,
  },
  backLink: {
    fontSize: 16,
    fontWeight: "600",
    color: "#64D2FF",
    marginTop: 16,
  },
});
