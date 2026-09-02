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

import { t } from "@/features/i18n";
import { useProfileId } from "@/store/useProfileStore";
import { fetchRecommendedStretches, RecommendedStretch } from "@/features/stretch/api";

export default function StretchesScreen() {
  const profileId = useProfileId();
  const [stretches, setStretches] = useState<RecommendedStretch[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadData = useCallback(async (isPullToRefresh = false) => {
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
  }, [profileId]);

  useFocusEffect(
    useCallback(() => {
      loadData(false);
    }, [loadData])
  );

  const onRefresh = useCallback(() => {
    loadData(true);
  }, [loadData]);

  const renderItem = ({ item }: { item: RecommendedStretch }) => {
    const mostRecentReason = item.sessions?.[0]?.reason || "";
    const hasImage = Boolean(item.image_url);

    return (
      <TouchableOpacity
        style={styles.card}
        activeOpacity={0.7}
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
    <SafeAreaView style={styles.container} edges={["left", "right", "bottom"]}>
      {loading && !refreshing ? (
        <View style={styles.centerContainer}>
          <ActivityIndicator size="large" color="#64D2FF" />
        </View>
      ) : error ? (
        <View style={styles.centerContainer}>
          <Text style={styles.errorText}>{error}</Text>
          <TouchableOpacity style={styles.retryButton} onPress={() => loadData(false)}>
            <Text style={styles.retryButtonText}>{t("common.ok")}</Text>
          </TouchableOpacity>
        </View>
      ) : (
        <FlatList
          data={stretches}
          keyExtractor={(item) => item.normalized_key}
          renderItem={renderItem}
          contentContainerStyle={styles.listContent}
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={onRefresh}
              tintColor="#64D2FF"
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
    backgroundColor: "#3A3A3C",
  },
  centerContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 24,
  },
  listContent: {
    padding: 16,
    paddingBottom: 40,
    gap: 12,
  },
  card: {
    backgroundColor: "#1C1C1E",
    borderRadius: 14,
    padding: 12,
    flexDirection: "row",
    gap: 12,
    borderWidth: 1,
    borderColor: "#2C2C2E",
  },
  thumbnailContainer: {
    width: 80,
    height: 80,
    borderRadius: 10,
    overflow: "hidden",
  },
  thumbnail: {
    width: "100%",
    height: "100%",
  },
  placeholderThumbnail: {
    width: "100%",
    height: "100%",
    backgroundColor: "#2C2C2E",
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
  },
  stretchName: {
    fontSize: 16,
    fontWeight: "700",
    color: "#FFFFFF",
    flex: 1,
  },
  badgeRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
    marginBottom: 6,
  },
  targetBadge: {
    backgroundColor: "rgba(255, 214, 10, 0.15)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  targetBadgeText: {
    color: "#FFD60A",
    fontSize: 11,
    fontWeight: "600",
  },
  sessionBadge: {
    backgroundColor: "rgba(100, 210, 255, 0.15)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  sessionBadgeText: {
    color: "#64D2FF",
    fontSize: 11,
    fontWeight: "600",
  },
  reasonText: {
    color: "#8E8E93",
    fontSize: 12,
    lineHeight: 16,
  },
  emptyContainer: {
    paddingVertical: 60,
    alignItems: "center",
    justifyContent: "center",
    gap: 10,
  },
  emptyIcon: {
    fontSize: 48,
    marginBottom: 8,
  },
  emptyTitle: {
    fontSize: 17,
    fontWeight: "700",
    color: "#FFFFFF",
  },
  emptySubtitle: {
    fontSize: 13,
    color: "#8E8E93",
    textAlign: "center",
    paddingHorizontal: 32,
    lineHeight: 18,
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
