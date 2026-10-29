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
import { StatusBar } from "expo-status-bar";
import { useVideoPlayer, VideoView } from "expo-video";

import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useProfileId } from "@/store/useProfileStore";
import {
  fetchRecommendedStretch,
  RecommendedStretch,
} from "@/features/stretch/api";
import { formatSessionLabel } from "@/features/wod/sessionLabel";

const T = TacticalTheme.light;

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
  useLocale();
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
      <StatusBar style="dark" />
      <Stack.Screen options={{ title: displayName }} />

      {loading ? (
        <View style={styles.centerContainer}>
          <ActivityIndicator size="large" color={T.electricBlue} />
        </View>
      ) : error || !stretch ? (
        <View style={styles.centerContainer}>
          <Text style={styles.errorText}>{error || t("stretches.notFound")}</Text>
          <TouchableOpacity
            style={styles.retryButton}
            onPress={() => router.back()}
          >
            <Text style={styles.retryButtonText}>{t("common.back")}</Text>
          </TouchableOpacity>
        </View>
      ) : (
        <ScrollView
          contentContainerStyle={styles.scrollContent}
          showsVerticalScrollIndicator={false}
        >
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
                    activeOpacity={0.75}
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
                      <Text style={styles.sessionHint}>
                        ⏱️ {sess.duration_hint}
                      </Text>
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
    backgroundColor: T.canvas,
  },
  centerContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 24,
  },
  scrollContent: {
    paddingHorizontal: 18,
    paddingTop: 12,
    paddingBottom: 40,
    gap: 14,
  },
  heroImageContainer: {
    width: "100%",
    height: 220,
    borderRadius: 16,
    overflow: "hidden",
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
  },
  heroImage: {
    width: "100%",
    height: "100%",
  },
  headerCard: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: T.border,
  },
  stretchTitle: {
    fontSize: 22,
    fontWeight: "900",
    letterSpacing: -0.4,
    color: T.ink,
    marginBottom: 8,
  },
  badgeRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
    marginBottom: 12,
  },
  targetBadge: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  targetBadgeText: {
    color: T.voltInk,
    fontSize: 12,
    fontWeight: "800",
  },
  sessionBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  sessionBadgeText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "700",
  },
  descriptionText: {
    color: T.inkSecondary,
    fontSize: 14,
    lineHeight: 21,
    marginBottom: 8,
  },
  hintRow: {
    marginTop: 6,
  },
  hintText: {
    color: T.inkMuted,
    fontSize: 12,
    fontWeight: "600",
  },
  cautionRow: {
    marginTop: 4,
  },
  cautionText: {
    color: T.statusExhausted,
    fontSize: 12,
    fontWeight: "600",
  },
  videoCard: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: T.border,
    gap: 10,
  },
  videoPlayer: {
    width: "100%",
    aspectRatio: 16 / 9,
    borderRadius: 12,
    backgroundColor: "#000",
  },
  sessionsCard: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: T.border,
  },
  sectionTitle: {
    fontSize: 16,
    fontWeight: "800",
    color: T.ink,
    marginBottom: 12,
  },
  sessionList: {
    gap: 10,
  },
  sessionItem: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    padding: 12,
    borderWidth: 1,
    borderColor: T.border,
  },
  sessionHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 6,
    gap: 8,
  },
  sessionLabelContainer: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 8,
    flex: 1,
  },
  sessionLabelBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 7,
    paddingVertical: 2,
    borderRadius: 6,
  },
  sessionLabelText: {
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "800",
  },
  sessionDateText: {
    color: T.inkMuted,
    fontSize: 12,
    fontWeight: "600",
  },
  provisionalBadge: {
    backgroundColor: T.statusWarningBg,
    borderWidth: 1,
    borderColor: T.statusWarningBorder,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 6,
  },
  provisionalText: {
    color: T.statusWarning,
    fontSize: 11,
    fontWeight: "700",
  },
  sessionReason: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 18,
    marginBottom: 6,
  },
  sessionHint: {
    color: T.inkMuted,
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
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },
  errorText: {
    color: T.statusExhausted,
    fontWeight: "600",
    fontSize: 14,
    textAlign: "center",
    marginBottom: 16,
  },
  retryButton: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 20,
    paddingVertical: 11,
    borderRadius: 10,
  },
  retryButtonText: {
    color: T.voltInk,
    fontWeight: "800",
    fontSize: 14,
  },
});
