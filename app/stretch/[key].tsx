import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Image } from "expo-image";
import { useLocalSearchParams, useRouter, Stack } from "expo-router";
import { useVideoPlayer, VideoView } from "expo-video";

import { t } from "@/features/i18n";
import { useProfileId } from "@/store/useProfileStore";
import { fetchRecommendedStretch, RecommendedStretch } from "@/features/stretch/api";
import { formatSessionLabel } from "@/features/wod/sessionLabel";

function StretchVideoSection({ videoUrl }: { videoUrl: string }) {
  const player = useVideoPlayer(videoUrl, (p) => {
    p.loop = true;
    // Do NOT autoplay
  });

  return (
    <View style={styles.videoCard}>
      <Text style={styles.sectionTitle}>{t("stretches.video")}</Text>
      <VideoView
        style={styles.videoPlayer}
        player={player}
        contentFit="contain"
        nativeControls
      />
    </View>
  );
}

export default function StretchDetailScreen() {
  const router = useRouter();
  const { key, name } = useLocalSearchParams<{ key: string; name?: string }>();
  const profileId = useProfileId();

  const [stretch, setStretch] = useState<RecommendedStretch | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!profileId || !key) {
      setLoading(false);
      return;
    }

    let isMounted = true;
    setLoading(true);
    setError(null);

    fetchRecommendedStretch(profileId, key)
      .then((data) => {
        if (isMounted) {
          setStretch(data);
          if (!data) {
            setError(t("stretches.notFound"));
          }
        }
      })
      .catch((e: any) => {
        if (isMounted) {
          setError(e?.message || t("stretches.loadFailed"));
        }
      })
      .finally(() => {
        if (isMounted) {
          setLoading(false);
        }
      });

    return () => {
      isMounted = false;
    };
  }, [profileId, key]);

  const displayName = stretch?.name || name || t("stretches.detailTitle");

  return (
    <SafeAreaView style={styles.container} edges={["left", "right", "bottom"]}>
      <Stack.Screen options={{ title: displayName }} />

      {loading ? (
        <View style={styles.centerContainer}>
          <ActivityIndicator size="large" color="#64D2FF" />
        </View>
      ) : error || !stretch ? (
        <View style={styles.centerContainer}>
          <Text style={styles.errorText}>{error || t("stretches.notFound")}</Text>
          <TouchableOpacity style={styles.retryButton} onPress={() => router.back()}>
            <Text style={styles.retryButtonText}>{t("common.back")}</Text>
          </TouchableOpacity>
        </View>
      ) : (
        <ScrollView contentContainerStyle={styles.scrollContent} showsVerticalScrollIndicator={false}>
          {/* Hero image */}
          {stretch.image_url ? (
            <View style={styles.heroImageContainer}>
              <Image
                source={{
                  uri: stretch.image_url,
                  cacheKey: `stretch-${stretch.id}-image-${stretch.updated_at}`,
                }}
                cachePolicy="memory-disk"
                style={styles.heroImage}
                contentFit="cover"
              />
            </View>
          ) : null}

          {/* Title & Target Area */}
          <View style={styles.headerCard}>
            <Text style={styles.stretchTitle}>{stretch.name}</Text>
            <View style={styles.badgeRow}>
              {stretch.target_area ? (
                <View style={styles.targetBadge}>
                  <Text style={styles.targetBadgeText}>{stretch.target_area}</Text>
                </View>
              ) : null}
              <View style={styles.sessionBadge}>
                <Text style={styles.sessionBadgeText}>
                  {t("stretches.sessionCount", { count: stretch.session_count })}
                </Text>
              </View>
            </View>

            {/* Description */}
            <Text style={styles.descriptionText}>
              {stretch.description || t("stretches.noDescription")}
            </Text>

            {/* Duration hint & caution */}
            {stretch.duration_hint ? (
              <View style={styles.hintRow}>
                <Text style={styles.hintText}>⏱️ {stretch.duration_hint}</Text>
              </View>
            ) : null}

            {stretch.caution ? (
              <View style={styles.cautionRow}>
                <Text style={styles.cautionText}>⚠️ {stretch.caution}</Text>
              </View>
            ) : null}
          </View>

          {/* Video Section */}
          {stretch.video_url ? (
            <StretchVideoSection videoUrl={stretch.video_url} />
          ) : null}

          {/* Sessions List */}
          <View style={styles.sessionsCard}>
            <Text style={styles.sectionTitle}>
              {t("stretches.sessionsTitle", { count: stretch.sessions.length })}
            </Text>

            <View style={styles.sessionList}>
              {stretch.sessions.map((sess, idx) => {
                const sessionDate = sess.created_at
                  ? new Date(sess.created_at).toLocaleDateString()
                  : "";
                const label = formatSessionLabel(sess.session_id);

                return (
                  <TouchableOpacity
                    key={`${sess.session_id}-${idx}`}
                    style={styles.sessionItem}
                    activeOpacity={0.7}
                    onPress={() =>
                      router.navigate({
                        pathname: "/(tabs)/history" as any,
                        params: { focusSessionId: sess.session_id },
                      })
                    }
                  >
                    <View style={styles.sessionHeader}>
                      <View style={styles.sessionLabelContainer}>
                        <View style={styles.sessionLabelBadge}>
                          <Text style={styles.sessionLabelText}>{label}</Text>
                        </View>
                        <Text style={styles.sessionDateText}>{sessionDate}</Text>
                      </View>

                      {sess.provisional ? (
                        <View style={styles.provisionalBadge}>
                          <Text style={styles.provisionalText}>
                            {t("stretchRecs.provisional")}
                          </Text>
                        </View>
                      ) : null}
                    </View>

                    {sess.reason ? (
                      <Text style={styles.sessionReason}>{sess.reason}</Text>
                    ) : null}

                    {sess.duration_hint ? (
                      <Text style={styles.sessionHint}>⏱️ {sess.duration_hint}</Text>
                    ) : null}

                    <View style={styles.sessionFooter}>
                      <Text style={styles.viewSessionLink}>
                        {t("stretches.viewSession")} →
                      </Text>
                    </View>
                  </TouchableOpacity>
                );
              })}
            </View>
          </View>
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: "#3A3A3C",
  },
  centerContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 24,
  },
  scrollContent: {
    padding: 16,
    paddingBottom: 40,
    gap: 14,
  },
  heroImageContainer: {
    width: "100%",
    height: 220,
    borderRadius: 14,
    overflow: "hidden",
    backgroundColor: "#1C1C1E",
  },
  heroImage: {
    width: "100%",
    height: "100%",
  },
  headerCard: {
    backgroundColor: "#1C1C1E",
    borderRadius: 14,
    padding: 16,
    borderWidth: 1,
    borderColor: "#2C2C2E",
  },
  stretchTitle: {
    fontSize: 22,
    fontWeight: "700",
    color: "#FFFFFF",
    marginBottom: 8,
  },
  badgeRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
    marginBottom: 12,
  },
  targetBadge: {
    backgroundColor: "rgba(255, 214, 10, 0.15)",
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  targetBadgeText: {
    color: "#FFD60A",
    fontSize: 12,
    fontWeight: "600",
  },
  sessionBadge: {
    backgroundColor: "rgba(100, 210, 255, 0.15)",
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  sessionBadgeText: {
    color: "#64D2FF",
    fontSize: 12,
    fontWeight: "600",
  },
  descriptionText: {
    color: "#E5E5EA",
    fontSize: 14,
    lineHeight: 20,
    marginBottom: 8,
  },
  hintRow: {
    marginTop: 6,
  },
  hintText: {
    color: "#8E8E93",
    fontSize: 12,
  },
  cautionRow: {
    marginTop: 4,
  },
  cautionText: {
    color: "#FF453A",
    fontSize: 12,
  },
  videoCard: {
    backgroundColor: "#1C1C1E",
    borderRadius: 14,
    padding: 16,
    borderWidth: 1,
    borderColor: "#2C2C2E",
    gap: 10,
  },
  videoPlayer: {
    width: "100%",
    aspectRatio: 16 / 9,
    borderRadius: 10,
    backgroundColor: "#000",
  },
  sessionsCard: {
    backgroundColor: "#1C1C1E",
    borderRadius: 14,
    padding: 16,
    borderWidth: 1,
    borderColor: "#2C2C2E",
  },
  sectionTitle: {
    fontSize: 16,
    fontWeight: "700",
    color: "#FFFFFF",
    marginBottom: 12,
  },
  sessionList: {
    gap: 10,
  },
  sessionItem: {
    backgroundColor: "#2C2C2E",
    borderRadius: 10,
    padding: 12,
    borderWidth: 1,
    borderColor: "#3A3A3C",
  },
  sessionHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 6,
  },
  sessionLabelContainer: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  sessionLabelBadge: {
    backgroundColor: "rgba(100, 210, 255, 0.15)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  sessionLabelText: {
    color: "#64D2FF",
    fontSize: 11,
    fontWeight: "700",
  },
  sessionDateText: {
    color: "#8E8E93",
    fontSize: 12,
  },
  provisionalBadge: {
    backgroundColor: "rgba(255, 159, 10, 0.2)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  provisionalText: {
    color: "#FF9F0A",
    fontSize: 11,
    fontWeight: "600",
  },
  sessionReason: {
    color: "#E5E5EA",
    fontSize: 13,
    lineHeight: 18,
    marginBottom: 6,
  },
  sessionHint: {
    color: "#8E8E93",
    fontSize: 11,
    marginBottom: 6,
  },
  sessionFooter: {
    flexDirection: "row",
    justifyContent: "flex-end",
    alignItems: "center",
    marginTop: 4,
  },
  viewSessionLink: {
    color: "#64D2FF",
    fontSize: 12,
    fontWeight: "600",
  },
  errorText: {
    color: "#FF453A",
    fontSize: 14,
    textAlign: "center",
    marginBottom: 16,
  },
  retryButton: {
    backgroundColor: "#64D2FF",
    paddingHorizontal: 20,
    paddingVertical: 10,
    borderRadius: 8,
  },
  retryButtonText: {
    color: "#000",
    fontWeight: "700",
    fontSize: 14,
  },
});
