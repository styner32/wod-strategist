import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";

import { MarkdownText } from "@/components/ui/MarkdownText";
import { useProfileId } from "@/store/useProfileStore";
import { AnalysisResult, fetchAnalysisHistory } from "../history";

/** Extracts a human-readable label from session_id like "wod-20260401-143021" → "WOD" */
function formatSessionLabel(sessionId: string): string {
  const parts = sessionId.split("-");
  if (parts.length === 0) return "Workout";
  const type = parts[0].toUpperCase();
  switch (type) {
    case "WOD":
      return "WOD";
    case "STRENGTH":
      return "Strength";
    case "CARDIO":
      return "Cardio";
    case "FLEXIBILITY":
      return "Flexibility";
    case "HIIT":
      return "HIIT";
    default:
      return type;
  }
}

/** Badge config for analysis status */
function getStatusConfig(status: string) {
  switch (status) {
    case "COMPLETED":
      return { label: "Complete", color: "#30D158", bgColor: "rgba(48,209,88,0.15)" };
    case "FAILED":
      return { label: "Failed", color: "#FF453A", bgColor: "rgba(255,69,58,0.15)" };
    case "PENDING":
      return { label: "Pending", color: "#FFD60A", bgColor: "rgba(255,214,10,0.15)" };
    default:
      return { label: status, color: "#A0A0A0", bgColor: "rgba(160,160,160,0.15)" };
  }
}

/** Badge config for analysis type */
function getTypeBadge(type: string) {
  switch (type) {
    case "injury_supplement":
      return { label: "🩹 Injury", color: "#FF6B6B", bgColor: "rgba(255,107,107,0.12)" };
    case "wod":
    default:
      return { label: "🏋️ WOD", color: "#64D2FF", bgColor: "rgba(100,210,255,0.12)" };
  }
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffHours = diffMs / (1000 * 60 * 60);

  if (diffHours < 1) {
    const mins = Math.floor(diffMs / (1000 * 60));
    return `${mins}m ago`;
  }
  if (diffHours < 24) {
    return `${Math.floor(diffHours)}h ago`;
  }
  if (diffHours < 48) {
    return "Yesterday";
  }

  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function HistoryCard({ item }: { item: AnalysisResult }) {
  const [expanded, setExpanded] = useState(false);
  const statusConfig = getStatusConfig(item.status);
  const typeBadge = getTypeBadge(item.analysis_type);

  const hasOutput = item.output && item.output.trim().length > 0;
  const hasInjuryOutput = item.injury_output && item.injury_output.trim().length > 0;

  return (
    <View style={styles.card}>
      {/* Header Row */}
      <View style={styles.cardHeader}>
        <View style={styles.headerLeft}>
          <Text style={styles.sessionLabel}>
            {formatSessionLabel(item.session_id)}
          </Text>
          <View style={[styles.badge, { backgroundColor: typeBadge.bgColor }]}>
            <Text style={[styles.badgeText, { color: typeBadge.color }]}>
              {typeBadge.label}
            </Text>
          </View>
        </View>
        <View style={[styles.badge, { backgroundColor: statusConfig.bgColor }]}>
          <Text style={[styles.badgeText, { color: statusConfig.color }]}>
            {statusConfig.label}
          </Text>
        </View>
      </View>

      {/* Date */}
      <Text style={styles.date}>{formatDate(item.created_at)}</Text>

      {/* Content */}
      {hasOutput && (
        <TouchableOpacity
          activeOpacity={0.8}
          onPress={() => setExpanded((p) => !p)}
        >
          {expanded ? (
            <View style={styles.contentBody}>
              <MarkdownText>{item.output}</MarkdownText>

              {/* Injury supplement output */}
              {hasInjuryOutput && (
                <View style={styles.injurySection}>
                  <View style={styles.injurySectionHeader}>
                    <Text style={styles.injurySectionLabel}>
                      🩹 Injury Supplement Analysis
                    </Text>
                  </View>
                  <MarkdownText color="#FFB4B4">
                    {item.injury_output!}
                  </MarkdownText>
                </View>
              )}
            </View>
          ) : (
            <Text style={styles.previewText} numberOfLines={4}>
              {stripMarkdown(item.output)}
            </Text>
          )}

          <Text style={styles.expandHint}>
            {expanded ? "Show Less ▲" : "Show More ▼"}
          </Text>
        </TouchableOpacity>
      )}

      {/* Failed/Pending states */}
      {!hasOutput && item.status === "FAILED" && (
        <View style={styles.failedBanner}>
          <Text style={styles.failedText}>
            Analysis failed. The video may have been too short or unclear.
          </Text>
        </View>
      )}

      {!hasOutput && item.status === "PENDING" && (
        <View style={styles.pendingBanner}>
          <ActivityIndicator size="small" color="#FFD60A" />
          <Text style={styles.pendingText}>Analysis in progress...</Text>
        </View>
      )}
    </View>
  );
}

/** Strip markdown formatting for a plain-text preview */
function stripMarkdown(text: string): string {
  return text
    .replace(/```[\s\S]*?```/g, "") // Remove code blocks
    .replace(/^---+$/gm, "") // Remove HR
    .replace(/^#{1,3}\s+/gm, "") // Remove headings
    .replace(/\*\*(.*?)\*\*/g, "$1") // Remove bold
    .replace(/^\s*[-*]\s+/gm, "• ") // Normalize bullets
    .replace(/^\s*\d+\.\s+/gm, "") // Remove numbered list markers
    .replace(/\n{2,}/g, "\n") // Collapse newlines
    .trim();
}

export function HistoryList() {
  const profileId = useProfileId();
  const [data, setData] = useState<AnalysisResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  const loadData = useCallback(async () => {
    if (!profileId) {
      setData([]);
      setLoading(false);
      setRefreshing(false);
      return;
    }
    try {
      const history = await fetchAnalysisHistory(profileId);
      setData(history);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [profileId]);

  useEffect(() => {
    setLoading(true);
    loadData();
  }, [loadData]);

  const onRefresh = useCallback(() => {
    setRefreshing(true);
    loadData();
  }, [loadData]);

  const renderItem = useCallback(
    ({ item }: { item: AnalysisResult }) => <HistoryCard item={item} />,
    []
  );

  if (loading && !refreshing) {
    return (
      <View style={styles.loadingContainer}>
        <ActivityIndicator size="large" color="#64D2FF" />
        <Text style={styles.loadingText}>Loading history...</Text>
      </View>
    );
  }

  return (
    <FlatList
      data={data}
      keyExtractor={(item) => item.id.toString()}
      renderItem={renderItem}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={onRefresh}
          tintColor="#64D2FF"
        />
      }
      contentContainerStyle={styles.list}
      ListEmptyComponent={
        <View style={styles.emptyContainer}>
          <Text style={styles.emptyIcon}>📋</Text>
          <Text style={styles.emptyTitle}>No Analysis History</Text>
          <Text style={styles.emptySubtitle}>
            Record a workout to get AI coaching feedback.
          </Text>
        </View>
      }
      scrollEnabled={false}
    />
  );
}

const styles = StyleSheet.create({
  list: {
    paddingHorizontal: 0,
    paddingBottom: 40,
    gap: 12,
  },

  // Card
  card: {
    backgroundColor: "rgba(255,255,255,0.06)",
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: "rgba(255,255,255,0.08)",
  },
  cardHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  sessionLabel: {
    fontSize: 17,
    fontWeight: "700",
    color: "#FFFFFF",
  },

  // Badges
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  badgeText: {
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 0.3,
  },

  // Date
  date: {
    fontSize: 12,
    color: "#888",
    marginBottom: 12,
    marginTop: 2,
  },

  // Preview text (collapsed)
  previewText: {
    fontSize: 14,
    lineHeight: 21,
    color: "#C0C0C0",
  },

  // Content body (expanded)
  contentBody: {
    marginTop: 4,
  },

  // Expand toggle
  expandHint: {
    fontSize: 12,
    color: "#64D2FF",
    fontWeight: "600",
    marginTop: 10,
    textAlign: "center",
  },

  // Injury supplement section
  injurySection: {
    marginTop: 16,
    borderTopWidth: 1,
    borderTopColor: "rgba(255,107,107,0.2)",
    paddingTop: 12,
  },
  injurySectionHeader: {
    marginBottom: 8,
  },
  injurySectionLabel: {
    fontSize: 15,
    fontWeight: "700",
    color: "#FF6B6B",
  },

  // Failed/Pending banners
  failedBanner: {
    backgroundColor: "rgba(255,69,58,0.1)",
    borderRadius: 8,
    padding: 12,
    marginTop: 4,
  },
  failedText: {
    fontSize: 13,
    color: "#FF453A",
    lineHeight: 18,
  },
  pendingBanner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: "rgba(255,214,10,0.08)",
    borderRadius: 8,
    padding: 12,
    marginTop: 4,
  },
  pendingText: {
    fontSize: 13,
    color: "#FFD60A",
    lineHeight: 18,
  },

  // Loading
  loadingContainer: {
    alignItems: "center",
    paddingVertical: 40,
    gap: 12,
  },
  loadingText: {
    fontSize: 14,
    color: "#888",
  },

  // Empty state
  emptyContainer: {
    alignItems: "center",
    paddingVertical: 60,
    gap: 8,
  },
  emptyIcon: {
    fontSize: 48,
    marginBottom: 8,
  },
  emptyTitle: {
    fontSize: 18,
    fontWeight: "700",
    color: "#FFF",
  },
  emptySubtitle: {
    fontSize: 14,
    color: "#888",
    textAlign: "center",
  },
});
