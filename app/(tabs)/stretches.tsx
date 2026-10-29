import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Image } from "expo-image";
import { router, useFocusEffect } from "expo-router";
import { StatusBar } from "expo-status-bar";

import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useProfileId } from "@/store/useProfileStore";
import {
  fetchRecommendedStretches,
  RecommendedStretch,
} from "@/features/stretch/api";

const T = TacticalTheme.light;

export default function StretchesScreen() {
  useLocale();
  const profileId = useProfileId();
  const [stretches, setStretches] = useState<RecommendedStretch[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadData = useCallback(
    async (isPullToRefresh = false) => {
      if (!profileId) {
        setStretches([]);
        setLoading(false);
        setRefreshing(false);
        return;
      }

      if (isPullToRefresh) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }
      setError(null);

      try {
        const data = await fetchRecommendedStretches(profileId);
        setStretches(data);
      } catch (e: any) {
        setError(e?.message || t("stretches.loadFailed"));
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [profileId],
  );

  useFocusEffect(
    useCallback(() => {
      loadData(false);
    }, [loadData]),
  );

  const onRefresh = useCallback(() => {
    loadData(true);
  }, [loadData]);

  const renderHeader = () => (
    <View style={styles.topBar}>
      <View style={styles.eyebrowRow}>
        <View style={styles.eyebrowDot} />
        <Text style={styles.eyebrowText}>{t("stretches.eyebrow")}</Text>
      </View>
      <View style={styles.titleRow}>
        <Text style={styles.title} numberOfLines={1}>
          {t("tabs.stretches")}
        </Text>
        <View style={styles.voltDot} />
      </View>
    </View>
  );

  const renderItem = ({ item }: { item: RecommendedStretch }) => {
    const mostRecentReason = item.sessions?.[0]?.reason || "";
    const hasImage = Boolean(item.image_url);

    return (
      <TouchableOpacity
        style={styles.card}
        activeOpacity={0.75}
        onPress={() =>
          router.push({
            pathname: "/stretch/[key]" as any,
            params: { key: item.normalized_key, name: item.name },
          })
        }
      >
        <View style={styles.thumbnailContainer}>
          {hasImage ? (
            <Image
              source={{
                uri: item.image_url,
                cacheKey: `stretch-${item.id}-image-${item.updated_at}`,
              }}
              cachePolicy="memory-disk"
              style={styles.thumbnail}
              contentFit="cover"
            />
          ) : (
            <View style={styles.placeholderThumbnail}>
              <Text style={styles.placeholderIcon}>🧘</Text>
            </View>
          )}
        </View>

        <View style={styles.contentContainer}>
          <View style={styles.headerRow}>
            <Text style={styles.stretchName} numberOfLines={1}>
              {item.name}
            </Text>
            <Text style={styles.chevron}>›</Text>
          </View>

          <View style={styles.badgeRow}>
            {item.target_area ? (
              <View style={styles.targetBadge}>
                <Text style={styles.targetBadgeText}>{item.target_area}</Text>
              </View>
            ) : null}

            <View style={styles.sessionBadge}>
              <Text style={styles.sessionBadgeText}>
                {t("stretches.sessionCount", { count: item.session_count })}
              </Text>
            </View>
          </View>

          {mostRecentReason ? (
            <Text style={styles.reasonText} numberOfLines={2}>
              {mostRecentReason}
            </Text>
          ) : null}
        </View>
      </TouchableOpacity>
    );
  };

  return (
    <SafeAreaView style={styles.container} edges={["top", "left", "right"]}>
      <StatusBar style="dark" />
      {loading && !refreshing ? (
        <View style={styles.listContent}>
          {renderHeader()}
          <View style={styles.centerContainer}>
            <ActivityIndicator size="large" color={T.electricBlue} />
          </View>
        </View>
      ) : error ? (
        <View style={styles.listContent}>
          {renderHeader()}
          <View style={styles.centerContainer}>
            <Text style={styles.errorText}>{error}</Text>
            <TouchableOpacity
              style={styles.retryButton}
              onPress={() => loadData(false)}
            >
              <Text style={styles.retryButtonText}>{t("common.ok")}</Text>
            </TouchableOpacity>
          </View>
        </View>
      ) : (
        <FlatList
          data={stretches}
          keyExtractor={(item) => item.normalized_key}
          renderItem={renderItem}
          ListHeaderComponent={renderHeader}
          contentContainerStyle={styles.listContent}
          showsVerticalScrollIndicator={false}
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={onRefresh}
              tintColor={T.electricBlue}
            />
          }
          ListEmptyComponent={
            <View style={styles.emptyContainer}>
              <Text style={styles.emptyIcon}>🧘</Text>
              <Text style={styles.emptyTitle}>{t("stretches.empty")}</Text>
              <Text style={styles.emptySubtitle}>{t("stretches.emptyDesc")}</Text>
            </View>
          }
        />
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: T.canvas,
  },
  topBar: {
    marginBottom: 8,
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
  centerContainer: {
    paddingVertical: 64,
    justifyContent: "center",
    alignItems: "center",
    paddingHorizontal: 24,
  },
  listContent: {
    paddingHorizontal: 18,
    paddingTop: 10,
    paddingBottom: 112,
    gap: 12,
  },
  card: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 12,
    flexDirection: "row",
    gap: 12,
    borderWidth: 1,
    borderColor: T.border,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  thumbnailContainer: {
    width: 80,
    height: 80,
    borderRadius: 12,
    overflow: "hidden",
    borderWidth: 1,
    borderColor: T.border,
  },
  thumbnail: {
    width: "100%",
    height: "100%",
  },
  placeholderThumbnail: {
    width: "100%",
    height: "100%",
    backgroundColor: T.surfaceMuted,
    justifyContent: "center",
    alignItems: "center",
  },
  placeholderIcon: {
    fontSize: 28,
  },
  contentContainer: {
    flex: 1,
    justifyContent: "center",
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 6,
    gap: 8,
  },
  stretchName: {
    fontSize: 16,
    fontWeight: "800",
    color: T.ink,
    flex: 1,
    flexShrink: 1,
  },
  chevron: {
    fontSize: 20,
    fontWeight: "600",
    color: T.inkMuted,
  },
  badgeRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
    marginBottom: 6,
  },
  targetBadge: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 7,
    paddingVertical: 2,
    borderRadius: 6,
  },
  targetBadgeText: {
    color: T.voltInk,
    fontSize: 11,
    fontWeight: "800",
  },
  sessionBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 7,
    paddingVertical: 2,
    borderRadius: 6,
  },
  sessionBadgeText: {
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "700",
  },
  reasonText: {
    color: T.inkSecondary,
    fontSize: 12,
    lineHeight: 17,
  },
  emptyContainer: {
    paddingVertical: 48,
    paddingHorizontal: 24,
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
    marginTop: 8,
  },
  emptyIcon: {
    fontSize: 44,
    marginBottom: 4,
  },
  emptyTitle: {
    fontSize: 17,
    fontWeight: "800",
    color: T.ink,
  },
  emptySubtitle: {
    fontSize: 13,
    color: T.inkMuted,
    textAlign: "center",
    lineHeight: 19,
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
    paddingHorizontal: 22,
    paddingVertical: 11,
    borderRadius: 10,
  },
  retryButtonText: {
    color: T.voltInk,
    fontWeight: "800",
    fontSize: 14,
  },
});
