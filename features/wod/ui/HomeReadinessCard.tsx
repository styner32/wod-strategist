import { IconSymbol } from "@/components/ui/icon-symbol";
import {
  animateTacticalLayout,
  triggerLightHaptic,
  useReduceMotionEnabled,
} from "@/constants/motion";
import {
  Fonts,
  getTacticalFatigueTone,
  TacticalTheme,
} from "@/constants/theme";
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
import { getFatigueStateText, MUSCLE_GROUPS } from "./WorkoutFatigueCard";

const T = TacticalTheme.light;

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
  const reduceMotion = useReduceMotionEnabled();
  const [expanded, setExpanded] = useState(false);

  if (!hasProfile) {
    return (
      <View style={styles.card} testID="home-readiness-no-profile">
        <View style={styles.headerRow}>
          <View style={styles.headerLeft}>
            <IconSymbol name="bolt.fill" size={18} color={T.electricBlue} />
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
          <ActivityIndicator color={T.electricBlue} size="small" />
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
            <IconSymbol name="bolt.fill" size={18} color={T.inkMuted} />
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
            <IconSymbol name="bolt.fill" size={18} color={T.electricBlue} />
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
            <IconSymbol name="bolt.fill" size={18} color={T.inkMuted} />
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

  const overallScore = Math.min(
    100,
    Math.max(0, readiness.overall_fatigue_score ?? 0),
  );
  const overallTone = getTacticalFatigueTone(overallScore, "light");
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

  const handleToggleExpand = () => {
    triggerLightHaptic();
    animateTacticalLayout(reduceMotion);
    setExpanded((prev) => !prev);
  };

  return (
    <TouchableOpacity
      testID="home-readiness-card"
      style={styles.card}
      activeOpacity={0.88}
      onPress={handleToggleExpand}
      accessibilityRole="button"
      accessibilityState={{ expanded }}
    >
      {/* Header */}
      <View style={styles.headerRow}>
        <View style={styles.headerLeft}>
          <IconSymbol name="bolt.fill" size={18} color={overallTone.fg} />
          <View style={styles.headerTitleCol}>
            <Text style={styles.title} numberOfLines={2}>
              {t("home.readinessTitle")}
            </Text>
            <Text style={styles.subtitle} numberOfLines={1}>
              {t("home.readinessSubtitle")}
            </Text>
          </View>
        </View>
        <View
          style={[
            styles.overallBadge,
            {
              backgroundColor: overallTone.bg,
              borderColor: overallTone.border,
            },
          ]}
        >
          <Text style={[styles.overallBadgeText, { color: overallTone.fg }]}>
            {stateLabel} · {overallScore}/100
          </Text>
        </View>
      </View>

      {/* Overall Cumulative Strain Bar Gauge */}
      <View style={styles.strainGaugeSection}>
        <View style={styles.strainGaugeLabelRow}>
          <Text style={styles.strainGaugeLabel}>
            {t("home.readinessStrainLabel")}
          </Text>
          <Text style={[styles.strainGaugeState, { color: overallTone.fg }]}>
            {stateLabel}
          </Text>
        </View>
        <View style={styles.strainGaugeTrack}>
          <View
            style={[
              styles.strainGaugeFill,
              {
                width: `${Math.max(4, overallScore)}%`,
                backgroundColor: overallTone.bar,
              },
            ]}
          />
        </View>
      </View>

      {/* Partial evidence notice on its own row */}
      {readiness.evidence_status === "partial" && (
        <View style={styles.partialRow}>
          <View style={styles.partialBadge}>
            <Text style={styles.partialBadgeText}>
              {t("preWod.partialHistoryBadge")}
            </Text>
          </View>
        </View>
      )}

      {/* Critical Fatigue Focus Cell */}
      <View
        style={[
          styles.highlightBox,
          {
            borderLeftColor:
              watchMuscles.length > 0 ? overallTone.bar : T.statusFresh,
          },
        ]}
      >
        <Text style={styles.highlightEyebrow}>
          {t("home.readinessCriticalNodes")}
        </Text>
        {watchMuscles.length > 0 && (
          <View style={styles.watchChipsRow}>
            {watchMuscles.map((wm) => {
              const wmTone = getTacticalFatigueTone(wm.score, "light");
              return (
                <View
                  key={wm.group}
                  style={[
                    styles.watchChip,
                    {
                      backgroundColor: T.surface,
                      borderColor: wmTone.border,
                    },
                  ]}
                >
                  <Text style={[styles.watchChipText, { color: wmTone.fg }]}>
                    {wm.name} ({wm.score}%)
                  </Text>
                </View>
              );
            })}
          </View>
        )}
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
            const barTone = getTacticalFatigueTone(m.score, "light");
            return (
              <View key={m.group} style={styles.muscleRow}>
                <View style={styles.muscleLabelRow}>
                  <Text style={styles.muscleName} numberOfLines={1}>
                    {m.name}
                  </Text>
                  <Text style={[styles.muscleScore, { color: barTone.fg }]}>
                    {m.score}/100
                  </Text>
                </View>
                <View style={styles.barTrack}>
                  <View
                    style={[
                      styles.barFill,
                      {
                        width: `${Math.max(4, m.score)}%`,
                        backgroundColor: barTone.bar,
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
              activeOpacity={0.75}
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
          color={T.inkMuted}
        />
      </View>
    </TouchableOpacity>
  );
}

const styles = StyleSheet.create({
  card: {
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
  loadingRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingVertical: 8,
  },
  loadingText: {
    color: T.inkMuted,
    fontSize: 13,
    fontWeight: "600",
    flexShrink: 1,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "flex-start",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: 8,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 8,
    flex: 1,
    minWidth: 160,
  },
  headerTitleCol: {
    flex: 1,
  },
  title: {
    color: T.ink,
    fontSize: 15,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.2,
    flexShrink: 1,
  },
  subtitle: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "500",
    marginTop: 2,
    flexShrink: 1,
  },
  overallBadge: {
    paddingHorizontal: 9,
    paddingVertical: 4,
    borderRadius: 8,
    borderWidth: 1,
    flexShrink: 0,
  },
  overallBadgeText: {
    fontFamily: Fonts?.mono,
    fontSize: 12,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  mutedBadge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
  },
  mutedBadgeText: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "700",
  },
  emptyText: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 19,
    marginTop: 10,
  },
  strainGaugeSection: {
    marginTop: 12,
    gap: 6,
  },
  strainGaugeLabelRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    gap: 8,
  },
  strainGaugeLabel: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 10,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    flexShrink: 1,
  },
  strainGaugeState: {
    fontFamily: Fonts?.mono,
    fontSize: 11,
    fontWeight: "800",
  },
  strainGaugeTrack: {
    height: 8,
    borderRadius: 4,
    backgroundColor: T.surfaceContainer,
    overflow: "hidden",
  },
  strainGaugeFill: {
    height: "100%",
    borderRadius: 4,
  },
  partialRow: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 10,
  },
  partialBadge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: T.statusWarningBorder,
    backgroundColor: T.statusWarningBg,
  },
  partialBadgeText: {
    fontFamily: Fonts?.mono,
    color: T.statusWarning,
    fontSize: 11,
    fontWeight: "700",
  },
  highlightBox: {
    marginTop: 12,
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    padding: 12,
    borderWidth: 1,
    borderColor: T.border,
    borderLeftWidth: 3,
  },
  highlightEyebrow: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 10,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: 6,
  },
  watchChipsRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
    marginBottom: 6,
  },
  watchChip: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
  },
  watchChipText: {
    fontFamily: Fonts?.mono,
    fontSize: 11,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  highlightText: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "700",
    lineHeight: 18,
  },
  metaRow: {
    marginTop: 6,
    gap: 2,
  },
  metaText: {
    color: T.inkSecondary,
    fontSize: 12,
    lineHeight: 16,
  },
  expandedSection: {
    marginTop: 12,
    padding: 12,
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.border,
    gap: 10,
  },
  muscleRow: {
    gap: 4,
  },
  muscleLabelRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    gap: 8,
  },
  muscleName: {
    fontFamily: Fonts?.mono,
    color: T.inkSecondary,
    fontSize: 11,
    fontWeight: "700",
    textTransform: "uppercase",
    flexShrink: 1,
  },
  muscleScore: {
    fontFamily: Fonts?.mono,
    fontSize: 11,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  barTrack: {
    height: 6,
    borderRadius: 3,
    backgroundColor: T.surfaceContainer,
    overflow: "hidden",
  },
  barFill: {
    height: "100%",
    borderRadius: 3,
  },
  historyLinkBtn: {
    marginTop: 4,
    paddingVertical: 9,
    alignItems: "center",
    borderRadius: 10,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  historyLinkText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },
  footerRow: {
    marginTop: 12,
    paddingTop: 10,
    borderTopWidth: 1,
    borderTopColor: T.border,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  footerHint: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.4,
    flexShrink: 1,
  },
});
