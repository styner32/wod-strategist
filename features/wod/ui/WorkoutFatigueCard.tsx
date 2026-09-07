import { t } from "@/features/i18n";
import React from "react";
import { StyleSheet, Text, View } from "react-native";
import type { SessionFatigue } from "../history";

interface WorkoutFatigueCardProps {
  fatigue?: SessionFatigue | null;
}

export const MUSCLE_GROUPS = [
  "shoulders_push",
  "upper_pull_grip",
  "posterior_chain",
  "quads_squat",
  "core_midline",
  "cardio_metabolic",
] as const;

export type MuscleGroupKey = (typeof MUSCLE_GROUPS)[number];

export function getFatigueColor(score: number): string {
  if (score <= 25) return "#30D158"; // Green
  if (score <= 50) return "#64D2FF"; // Cyan
  if (score <= 75) return "#FF9F0A"; // Orange
  return "#FF453A"; // Red
}

export function getFatigueStateText(state: string, score: number): string {
  if (state === "fresh" || score <= 25) return t("historyList.fresh");
  if (state === "moderate" || score <= 50) return t("historyList.moderate");
  if (state === "fatigued" || score <= 75) return t("historyList.fatigued");
  return t("historyList.exhausted");
}

export function WorkoutFatigueCard({ fatigue }: WorkoutFatigueCardProps) {
  if (!fatigue || !fatigue.muscles) {
    return null;
  }

  const overallColor = getFatigueColor(fatigue.overall_score);
  const stateLabel =
    fatigue.state_ko ||
    getFatigueStateText(fatigue.state, fatigue.overall_score);

  // Identify top-loaded muscle groups
  const sortedMuscles = MUSCLE_GROUPS.map((key) => ({
    key,
    name: t(`muscleGroups.${key}` as any),
    score: fatigue.muscles[key] ?? 0,
  }))
    .filter((m) => m.score > 30)
    .sort((a, b) => b.score - a.score);

  const topMusclesText =
    sortedMuscles.length > 0
      ? sortedMuscles
          .slice(0, 2)
          .map((m) => `${m.name} (${m.score}%)`)
          .join(", ")
      : null;

  return (
    <View style={styles.card}>
      {/* Header */}
      <View style={styles.headerRow}>
        <View style={styles.headerLeft}>
          <Text style={styles.icon}>⚡</Text>
          <Text style={styles.title}>{t("historyList.fatigueCardTitle")}</Text>
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
            {stateLabel} ({fatigue.overall_score}%)
          </Text>
        </View>
      </View>

      {/* 6 Muscle Group Bars */}
      <View style={styles.musclesList}>
        {MUSCLE_GROUPS.map((groupKey) => {
          const score = Math.min(
            100,
            Math.max(0, fatigue.muscles[groupKey] ?? 0),
          );
          const muscleColor = getFatigueColor(score);
          const name = t(`muscleGroups.${groupKey}` as any);

          return (
            <View key={groupKey} style={styles.muscleRow}>
              <View style={styles.muscleLabelRow}>
                <Text style={styles.muscleName}>{name}</Text>
                <Text style={[styles.muscleScore, { color: muscleColor }]}>
                  {score}%
                </Text>
              </View>
              <View style={styles.barTrack}>
                <View
                  style={[
                    styles.barFill,
                    {
                      width: `${score}%`,
                      backgroundColor: muscleColor,
                    },
                  ]}
                />
              </View>
            </View>
          );
        })}
      </View>

      {/* Top loaded summary notice */}
      {topMusclesText ? (
        <View style={styles.summaryBox}>
          <Text style={styles.summaryText}>
            {t("historyList.topLoadedMuscles", { muscles: topMusclesText })}
          </Text>
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: "#1C1C1E",
    borderRadius: 12,
    padding: 14,
    marginTop: 12,
    borderWidth: 1,
    borderColor: "#2C2C2E",
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 12,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    flex: 1,
  },
  icon: {
    fontSize: 16,
  },
  title: {
    color: "#FFFFFF",
    fontSize: 14,
    fontWeight: "700",
  },
  overallBadge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
  },
  overallBadgeText: {
    fontSize: 12,
    fontWeight: "700",
  },
  musclesList: {
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
    color: "#C7C7CC",
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
  summaryBox: {
    marginTop: 12,
    paddingTop: 10,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: "rgba(255, 255, 255, 0.12)",
  },
  summaryText: {
    color: "#8E8E93",
    fontSize: 12,
    lineHeight: 16,
  },
});
