import { IconSymbol } from "@/components/ui/icon-symbol";
import { t, useLocale } from "@/features/i18n";
import React, { useState } from "react";
import {
  ActivityIndicator,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import type { ProfileReadinessResponse } from "../api";
import {
  getFatigueColor,
  getFatigueStateText,
  MUSCLE_GROUPS,
} from "./WorkoutFatigueCard";

interface HomeReadinessCardProps {
  readiness: ProfileReadinessResponse | null;
  loading?: boolean;
  error?: boolean;
  hasProfile: boolean;
  onPressHistory?: () => void;
}

function formatRelativeTime(isoStr?: string | null): string | null {
  if (!isoStr) return null;
  const date = new Date(isoStr);
  if (Number.isNaN(date.getTime())) return null;

  const diffMs = Date.now() - date.getTime();
  if (diffMs < 0) return null;
  const diffHours = diffMs / (1000 * 60 * 60);

  if (diffHours < 1) {
    const mins = Math.max(1, Math.floor(diffMs / (1000 * 60)));
    return t("historyList.minutesAgo", { count: mins });
  }
  if (diffHours < 24) {
    return t("historyList.hoursAgo", { count: Math.floor(diffHours) });
  }
  if (diffHours < 48) {
    return t("historyList.yesterday");
  }
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

export function HomeReadinessCard({
  readiness,
  loading = false,
  error = false,
  hasProfile,
  onPressHistory,
}: HomeReadinessCardProps) {
  const locale = useLocale();
  const [expanded, setExpanded] = useState(false);

  if (!hasProfile) {
    return (
      <View style={styles.card} testID="home-readiness-no-profile">
        <View style={styles.headerRow}>
          <View style={styles.headerLeft}>
            <IconSymbol name="bolt.fill" size={18} color="#64D2FF" />
            <Text style={styles.title}>{t("home.readinessTitle")}</Text>
          </View>
        </View>
        <Text style={styles.emptyText}>{t("home.readinessNoProfile")}</Text>
      </View>
    );
  }

  if (loading && !readiness) {
    return (
      <View style={styles.card} testID="home-readiness-loading">
        <View style={styles.loadingRow}>
          <ActivityIndicator color="#00E5FF" size="small" />
          <Text style={styles.loadingText}>{t("home.readinessLoading")}</Text>
        </View>
      </View>
    );
  }

  if (error && !readiness) {
    return (
      <View style={styles.card} testID="home-readiness-error">
        <View style={styles.headerRow}>
          <View style={styles.headerLeft}>
            <IconSymbol name="bolt.fill" size={18} color="#8E9BAE" />
            <Text style={styles.title}>{t("home.readinessTitle")}</Text>
          </View>
        </View>
        <Text style={styles.emptyText}>{t("home.readinessError")}</Text>
      </View>
    );
  }

  if (!readiness || readiness.evidence_status === "no_history") {
    return (
      <View style={styles.card} testID="home-readiness-no-history">
        <View style={styles.headerRow}>
          <View style={styles.headerLeft}>
            <IconSymbol name="bolt.fill" size={18} color="#64D2FF" />
            <Text style={styles.title}>{t("home.readinessTitle")}</Text>
          </View>
        </View>
        <Text style={styles.emptyText}>{t("home.readinessNoHistory")}</Text>
      </View>
    );
  }

  if (readiness.evidence_status === "insufficient") {
    return (
      <View style={styles.card} testID="home-readiness-insufficient">
        <View style={styles.headerRow}>
          <View style={styles.headerLeft}>
            <IconSymbol name="bolt.fill" size={18} color="#8E9BAE" />
            <Text style={styles.title}>{t("home.readinessTitle")}</Text>
          </View>
          <View style={styles.mutedBadge}>
            <Text style={styles.mutedBadgeText}>
              {t("historyList.insufficientEvidence")}
            </Text>
          </View>
        </View>
        <Text style={styles.emptyText}>{t("home.readinessInsufficient")}</Text>
      </View>
    );
  }

  const overallScore = readiness.overall_fatigue_score ?? 0;
  const overallColor = getFatigueColor(overallScore);
  const stateLabel =
    locale === "ko" && readiness.overall_state_ko
      ? readiness.overall_state_ko
      : getFatigueStateText(readiness.overall_state, overallScore);

  const muscleMap = new Map(
    (readiness.muscle_readiness ?? []).map((m) => [m.group, m]),
  );

  const orderedMuscles = MUSCLE_GROUPS.map((groupKey) => {
    const item = muscleMap.get(groupKey);
    const score = Math.min(100, Math.max(0, item?.fatigue_score ?? 0));
    return {
      group: groupKey,
      name: t(`muscleGroups.${groupKey}` as any),
      score,
      state: item?.state ?? "fresh",
    };
  });

  const watchMuscles = [...orderedMuscles]
    .filter((m) => m.score >= 40)
    .sort((a, b) => b.score - a.score)
    .slice(0, 2);

  const watchSummary =
    watchMuscles.length > 0
      ? t("home.readinessWatchMuscles", {
          muscles: watchMuscles
            .map((m) => `${m.name} (${m.score})`)
            .join(" · "),
        })
      : t("home.readinessAllFresh");

  const lastWorkoutRelative = formatRelativeTime(readiness.last_workout_at);
  const latestWod = readiness.recent_workouts?.[0];
  const latestWodMovements =
    latestWod?.movements && latestWod.movements.length > 0
      ? latestWod.movements.slice(0, 3).join(" · ")
      : null;

  return (
    <TouchableOpacity
      testID="home-readiness-card"
      style={styles.card}
      activeOpacity={0.85}
      onPress={() => setExpanded((prev) => !prev)}
      accessibilityRole="button"
      accessibilityState={{ expanded }}
    >
      {/* Header */}
      <View style={styles.headerRow}>
        <View style={styles.headerLeft}>
          <IconSymbol name="bolt.fill" size={18} color={overallColor} />
          <Text style={styles.title} numberOfLines={2}>
            {t("home.readinessTitle")}
          </Text>
        </View>
        <View
          style={[
            styles.overallBadge,
            {
              backgroundColor: overallColor + "20",
              borderColor: overallColor + "60",
            },
          ]}
        >
          <Text style={[styles.overallBadgeText, { color: overallColor }]}>
            {stateLabel} · {overallScore}/100
          </Text>
        </View>
      </View>

      {readiness.evidence_status === "partial" && (
        <View style={styles.partialRow}>
          <View style={styles.partialBadge}>
            <Text style={styles.partialBadgeText}>
              {t("preWod.partialHistoryBadge")}
            </Text>
          </View>
        </View>
      )}

      {/* Primary summary line */}
      <View
        style={[
          styles.highlightBox,
          {
            borderLeftColor:
              watchMuscles.length > 0 ? overallColor : "#30D158",
          },
        ]}
      >
        <Text style={styles.highlightText}>{watchSummary}</Text>
        {(lastWorkoutRelative || latestWodMovements) && (
          <View style={styles.metaRow}>
            {lastWorkoutRelative && (
              <Text style={styles.metaText}>
                {t("home.readinessLastWorkout", { time: lastWorkoutRelative })}
              </Text>
            )}
            {latestWodMovements && (
              <Text style={styles.metaText} numberOfLines={1}>
                {t("home.readinessRecentWod", { summary: latestWodMovements })}
              </Text>
            )}
          </View>
        )}
      </View>

      {/* Expanded 6-muscle group breakdown */}
      {expanded && (
        <View style={styles.expandedSection} testID="home-readiness-muscles">
          {orderedMuscles.map((m) => {
            const barColor = getFatigueColor(m.score);
            return (
              <View key={m.group} style={styles.muscleRow}>
                <View style={styles.muscleLabelRow}>
                  <Text style={styles.muscleName}>{m.name}</Text>
                  <Text style={[styles.muscleScore, { color: barColor }]}>
                    {m.score}/100
                  </Text>
                </View>
                <View style={styles.barTrack}>
                  <View
                    style={[
                      styles.barFill,
                      {
                        width: `${Math.max(4, m.score)}%`,
                        backgroundColor: barColor,
                      },
                    ]}
                  />
                </View>
              </View>
            );
          })}

          {onPressHistory && (
            <TouchableOpacity
              testID="home-readiness-history-link"
              style={styles.historyLinkBtn}
              onPress={onPressHistory}
              activeOpacity={0.7}
            >
              <Text style={styles.historyLinkText}>
                {t("home.readinessViewHistory")}
              </Text>
            </TouchableOpacity>
          )}
        </View>
      )}

      {/* Progressive disclosure footer */}
      <View style={styles.footerRow}>
        <Text style={styles.footerHint}>
          {expanded
            ? t("home.readinessTapCollapse")
            : t("home.readinessTapExpand")}
        </Text>
        <IconSymbol
          name={expanded ? "chevron.up" : "chevron.down"}
          size={16}
          color="#64748B"
        />
      </View>
    </TouchableOpacity>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: "#121720",
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: "#1E2633",
  },
  loadingRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingVertical: 8,
  },
  loadingText: {
    color: "#8E9BAE",
    fontSize: 13,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: 8,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    flex: 1,
    minWidth: 150,
  },
  title: {
    color: "#F8FAFC",
    fontSize: 15,
    fontWeight: "700",
    flexShrink: 1,
  },
  partialRow: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 8,
  },
  partialBadge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: "rgba(255, 159, 10, 0.4)",
    backgroundColor: "rgba(255, 159, 10, 0.14)",
  },
  partialBadgeText: {
    color: "#FF9F0A",
    fontSize: 11,
    fontWeight: "600",
  },
  overallBadge: {
    paddingHorizontal: 9,
    paddingVertical: 4,
    borderRadius: 8,
    borderWidth: 1,
    flexShrink: 0,
  },
  overallBadgeText: {
    fontSize: 12,
    fontWeight: "700",
  },
  mutedBadge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    backgroundColor: "rgba(142, 155, 174, 0.15)",
    flexShrink: 0,
  },
  mutedBadgeText: {
    color: "#8E9BAE",
    fontSize: 11,
    fontWeight: "600",
  },
  emptyText: {
    color: "#8E9BAE",
    fontSize: 13,
    lineHeight: 19,
    marginTop: 10,
  },
  highlightBox: {
    marginTop: 12,
    backgroundColor: "#0B0F16",
    borderRadius: 10,
    paddingVertical: 10,
    paddingHorizontal: 12,
    borderLeftWidth: 3,
    gap: 6,
  },
  highlightText: {
    color: "#E2E8F0",
    fontSize: 13,
    fontWeight: "600",
    lineHeight: 18,
  },
  metaRow: {
    gap: 2,
  },
  metaText: {
    color: "#94A3B8",
    fontSize: 12,
  },
  expandedSection: {
    marginTop: 14,
    paddingTop: 12,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: "#243042",
    gap: 10,
  },
  muscleRow: {
    gap: 4,
  },
  muscleLabelRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
  },
  muscleName: {
    color: "#CBD5E1",
    fontSize: 12,
    fontWeight: "500",
  },
  muscleScore: {
    fontSize: 12,
    fontWeight: "700",
  },
  barTrack: {
    height: 6,
    backgroundColor: "rgba(255, 255, 255, 0.08)",
    borderRadius: 3,
    overflow: "hidden",
  },
  barFill: {
    height: "100%",
    borderRadius: 3,
  },
  historyLinkBtn: {
    marginTop: 4,
    paddingVertical: 6,
    alignSelf: "flex-end",
  },
  historyLinkText: {
    color: "#00E5FF",
    fontSize: 12,
    fontWeight: "700",
  },
  footerRow: {
    marginTop: 10,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  footerHint: {
    color: "#64748B",
    fontSize: 11,
    fontWeight: "600",
  },
});
