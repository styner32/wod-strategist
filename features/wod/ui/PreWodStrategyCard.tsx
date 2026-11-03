import { IconSymbol } from "@/components/ui/icon-symbol";
import {
  animateTacticalLayout,
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
import type { PreWodAdviceResponse } from "../api";

const T = TacticalTheme.light;

interface PreWodStrategyCardProps {
  advice: PreWodAdviceResponse | null;
  loading?: boolean;
}

function formatWorkoutDuration(seconds?: number | null): string {
  if (seconds == null || !Number.isFinite(seconds) || seconds <= 0) {
    return t("preWod.durationUnconfirmed");
  }
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  return `${mins}:${String(secs).padStart(2, "0")}`;
}

export function PreWodStrategyCard({
  advice,
  loading,
}: PreWodStrategyCardProps) {
  useLocale();
  const reduceMotion = useReduceMotionEnabled();
  const [musclesExpanded, setMusclesExpanded] = useState(false);

  if (loading) {
    return (
      <View style={styles.card}>
        <View style={styles.loadingContainer}>
          <ActivityIndicator color={T.electricBlue} size="small" />
          <Text style={styles.loadingText}>
            {t("preWod.analyzingFatigue")}
          </Text>
        </View>
      </View>
    );
  }

  if (!advice) {
    return null;
  }

  const overallTone = getTacticalFatigueTone(advice.overall_state, "light");

  const handleToggleMuscles = () => {
    animateTacticalLayout(reduceMotion);
    setMusclesExpanded((prev) => !prev);
  };

  return (
    <View style={styles.card}>
      {/* Header */}
      <View style={styles.headerRow}>
        <View style={styles.headerLeft}>
          <IconSymbol name="flame.fill" size={18} color={T.statusFatigued} />
          <Text style={styles.title}>{t("preWod.title")}</Text>
        </View>
        <View style={styles.headerBadges}>
          {advice.evidence_status === "partial" && (
            <View style={styles.partialBadge}>
              <Text style={styles.partialBadgeText}>
                {t("preWod.partialHistoryBadge")}
              </Text>
            </View>
          )}
          <View
            style={[
              styles.overallBadge,
              {
                borderColor: overallTone.border,
                backgroundColor: overallTone.bg,
              },
            ]}
          >
            <Text style={[styles.overallBadgeText, { color: overallTone.fg }]}>
              {advice.overall_state_ko || advice.overall_state}
              {advice.overall_fatigue_score != null
                ? ` (${advice.overall_fatigue_score}/100)`
                : ""}
            </Text>
          </View>
        </View>
      </View>

      {/* Overall Summary Briefing */}
      {advice.overall_summary ? (
        <View
          style={[
            styles.summaryBox,
            { borderLeftColor: overallTone.bar },
          ]}
        >
          <Text style={styles.summaryText}>{advice.overall_summary}</Text>
        </View>
      ) : null}

      {/* Target RPE & Pace Strategy */}
      {advice.target_rpe ? (
        <View style={styles.rpeSection}>
          <View style={styles.rpeHeader}>
            <View style={styles.rpeBadge}>
              <Text style={styles.rpeBadgeText}>
                {advice.target_rpe.label || `RPE ${advice.target_rpe.score}`}
              </Text>
            </View>
            <Text style={styles.sectionLabel}>
              {t("preWod.pacingStrategy")}
            </Text>
          </View>
          {advice.target_rpe.pacing_strategy ? (
            <Text style={styles.pacingText}>
              {advice.target_rpe.pacing_strategy}
            </Text>
          ) : null}
          {advice.effort_reflection &&
          ((advice.effort_reflection.recent_workouts?.length ?? 0) > 0 ||
            !!advice.effort_reflection.reason) ? (
            <View style={styles.reflectionBox}>
              <Text style={styles.reflectionTitle}>
                {t("preWod.recentFeedbackTitle")}
              </Text>
              {advice.effort_reflection.applied &&
              advice.effort_reflection.reason ? (
                <Text style={styles.reflectionReason}>
                  {advice.effort_reflection.reason}
                </Text>
              ) : !advice.effort_reflection.applied ? (
                <Text style={styles.reflectionHint}>
                  {t("preWod.noEffortPrompt")}
                </Text>
              ) : null}
              {(advice.effort_reflection.recent_workouts ?? []).length > 0 ? (
                <View style={styles.recentWorkoutsList}>
                  {(advice.effort_reflection.recent_workouts ?? []).map((w) => {
                    const movText =
                      w.movements && w.movements.length > 0
                        ? w.movements.slice(0, 3).join(" · ")
                        : t("preWod.movementsUnknown");
                    return (
                      <View key={w.session_id} style={styles.recentWorkoutChip}>
                        <Text style={styles.recentWorkoutText}>
                          {movText} ·{" "}
                          {formatWorkoutDuration(w.duration_seconds)}
                        </Text>
                        <View style={styles.recentBadgesRow}>
                          <View
                            style={[
                              styles.effortPill,
                              w.effort == null && styles.effortPillMuted,
                            ]}
                          >
                            <Text
                              style={[
                                styles.effortPillText,
                                w.effort == null && styles.effortPillTextMuted,
                              ]}
                            >
                              {w.effort != null
                                ? t("preWod.effortBadge", { effort: w.effort })
                                : t("preWod.effortUnrated")}
                            </Text>
                          </View>
                          {w.hybrid_sensor_excluded_reason ? (
                            <View style={[styles.effortPill, styles.effortPillMuted]}>
                              <Text style={[styles.effortPillText, styles.effortPillTextMuted]}>
                                {t(w.hybrid_sensor_excluded_reason === "calibration_changed" ? "preWod.hybridSensorExcluded" : "preWod.hybridSensorProvenanceUnknown")}
                              </Text>
                            </View>
                          ) : w.hybrid_sensor != null ? (
                            <View style={styles.sensorPill}>
                              <Text style={styles.sensorPillText}>
                                {t("preWod.hybridSensorBadge", {
                                  score: Math.round(
                                    w.hybrid_sensor.hybrid_intensity,
                                  ),
                                })}
                              </Text>
                            </View>
                          ) : null}
                        </View>
                      </View>
                    );
                  })}
                </View>
              ) : null}
            </View>
          ) : null}
        </View>
      ) : null}

      {/* 6-Muscle Group Telemetry Grid (2x3 in compact mode) */}
      {advice.muscle_readiness && advice.muscle_readiness.length > 0 ? (
        <View style={styles.musclesSection}>
          <TouchableOpacity
            style={styles.expandHeader}
            onPress={handleToggleMuscles}
            activeOpacity={0.7}
          >
            <Text style={styles.sectionLabel}>
              {t("preWod.muscleReadiness")}
            </Text>
            <Text style={styles.expandChevron}>
              {musclesExpanded
                ? t("preWod.collapseDetails")
                : t("preWod.expandDetails")}
            </Text>
          </TouchableOpacity>

          {!musclesExpanded ? (
            <View style={styles.compactGrid}>
              {advice.muscle_readiness.map((m) => {
                const tone = getTacticalFatigueTone(m.state, "light");
                const isHighStrain =
                  m.fatigue_score >= 75 ||
                  m.state === "exhausted" ||
                  m.state === "fatigued";
                return (
                  <View
                    key={m.group}
                    style={[
                      styles.compactItem,
                      isHighStrain && {
                        backgroundColor: tone.bg,
                        borderColor: tone.border,
                      },
                    ]}
                  >
                    <View style={styles.compactItemHeader}>
                      <Text
                        style={[
                          styles.compactName,
                          isHighStrain && { color: tone.fg },
                        ]}
                        numberOfLines={1}
                      >
                        {m.name_ko}
                      </Text>
                      <Text
                        style={[styles.compactPercent, { color: tone.fg }]}
                      >
                        {m.fatigue_score}/100
                      </Text>
                    </View>
                    <View style={styles.progressBarBg}>
                      <View
                        style={[
                          styles.progressBarFill,
                          {
                            width: `${Math.min(100, Math.max(5, m.fatigue_score))}%`,
                            backgroundColor: tone.bar,
                          },
                        ]}
                      />
                    </View>
                  </View>
                );
              })}
            </View>
          ) : (
            <View style={styles.detailedList}>
              {advice.muscle_readiness.map((m) => {
                const tone = getTacticalFatigueTone(m.state, "light");
                return (
                  <View key={m.group} style={styles.detailedItem}>
                    <View style={styles.detailedHeader}>
                      <Text style={styles.detailedName}>{m.name_ko}</Text>
                      <View
                        style={[
                          styles.stateTag,
                          {
                            backgroundColor: tone.bg,
                            borderColor: tone.border,
                          },
                        ]}
                      >
                        <Text
                          style={[styles.stateTagText, { color: tone.fg }]}
                        >
                          {m.state_ko || m.state} ({m.fatigue_score}/100)
                        </Text>
                      </View>
                    </View>
                    <View style={styles.progressBarBg}>
                      <View
                        style={[
                          styles.progressBarFill,
                          {
                            width: `${Math.min(100, Math.max(5, m.fatigue_score))}%`,
                            backgroundColor: tone.bar,
                          },
                        ]}
                      />
                    </View>
                    {m.note ? (
                      <Text style={styles.muscleNote}>{m.note}</Text>
                    ) : null}
                  </View>
                );
              })}
            </View>
          )}
        </View>
      ) : null}

      {/* Scaling & Movement Modification Advice */}
      {advice.scaling_advice && advice.scaling_advice.length > 0 ? (
        <View style={styles.scalingSection}>
          <Text style={styles.sectionLabel}>{t("preWod.scalingAdvice")}</Text>
          {advice.scaling_advice.map((item, idx) => (
            <View key={idx} style={styles.scalingCard}>
              <View style={styles.scalingHeader}>
                <Text style={styles.scalingMovement}>{item.movement}</Text>
                <View style={styles.scalingBadge}>
                  <Text style={styles.scalingBadgeText}>
                    {item.recommendation}
                  </Text>
                </View>
              </View>
              <Text style={styles.scalingDetail}>{item.detail}</Text>
            </View>
          ))}
        </View>
      ) : null}

      {/* Pre-WOD Mobility / Warm-up */}
      {advice.mobility_warmup && advice.mobility_warmup.length > 0 ? (
        <View style={styles.mobilitySection}>
          <Text style={styles.sectionLabel}>
            {t("preWod.recommendedMobility")}
          </Text>
          {advice.mobility_warmup.map((item, idx) => (
            <View key={idx} style={styles.mobilityCard}>
              <View style={styles.mobilityHeader}>
                <Text style={styles.mobilityTitle}>{item.title}</Text>
                {item.duration ? (
                  <View style={styles.durationBadge}>
                    <Text style={styles.durationText}>{item.duration}</Text>
                  </View>
                ) : null}
              </View>
              {item.target_area ? (
                <Text style={styles.mobilityTarget}>
                  {t("preWod.mobilityTarget", { area: item.target_area })}
                </Text>
              ) : null}
              {item.reason ? (
                <Text style={styles.mobilityReason}>{item.reason}</Text>
              ) : null}
            </View>
          ))}
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    borderColor: T.border,
    marginTop: 14,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  loadingContainer: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 14,
    gap: 10,
  },
  loadingText: {
    color: T.inkMuted,
    fontSize: 13,
    fontWeight: "600",
    flexShrink: 1,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: 8,
    marginBottom: 12,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    flexShrink: 1,
  },
  title: {
    color: T.ink,
    fontSize: 15,
    fontWeight: "800",
    letterSpacing: 0.2,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  headerBadges: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 6,
  },
  partialBadge: {
    paddingHorizontal: 7,
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
  overallBadge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
  },
  overallBadgeText: {
    fontFamily: Fonts?.mono,
    fontSize: 11,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  summaryBox: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 10,
    padding: 12,
    borderWidth: 1,
    borderColor: T.border,
    borderLeftWidth: 3,
    marginBottom: 14,
  },
  summaryText: {
    color: T.ink,
    fontSize: 13,
    lineHeight: 19,
    fontWeight: "600",
  },
  rpeSection: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    padding: 12,
    marginBottom: 14,
    borderWidth: 1,
    borderColor: T.border,
  },
  rpeHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginBottom: 6,
  },
  rpeBadge: {
    backgroundColor: T.surfaceInverse,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  rpeBadgeText: {
    fontFamily: Fonts?.mono,
    color: T.volt,
    fontSize: 11,
    fontWeight: "800",
  },
  sectionLabel: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.6,
    flexShrink: 1,
  },
  pacingText: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 18,
  },
  reflectionBox: {
    marginTop: 10,
    paddingTop: 10,
    borderTopWidth: 1,
    borderTopColor: T.border,
    gap: 6,
  },
  reflectionTitle: {
    fontFamily: Fonts?.mono,
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 0.3,
  },
  reflectionReason: {
    color: T.ink,
    fontSize: 12,
    lineHeight: 17,
  },
  reflectionHint: {
    color: T.inkMuted,
    fontSize: 11,
    lineHeight: 16,
  },
  recentWorkoutsList: {
    gap: 6,
    marginTop: 2,
  },
  recentWorkoutChip: {
    backgroundColor: T.surface,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 7,
    borderWidth: 1,
    borderColor: T.border,
    gap: 4,
  },
  recentWorkoutText: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "700",
  },
  recentBadgesRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
  },
  effortPill: {
    backgroundColor: T.statusWarningBg,
    borderWidth: 1,
    borderColor: T.statusWarningBorder,
    borderRadius: 4,
    paddingHorizontal: 6,
    paddingVertical: 2,
  },
  effortPillMuted: {
    backgroundColor: T.surfaceMuted,
    borderColor: T.border,
  },
  effortPillText: {
    fontFamily: Fonts?.mono,
    color: T.statusWarning,
    fontSize: 10,
    fontWeight: "800",
  },
  effortPillTextMuted: {
    color: T.inkMuted,
    fontWeight: "600",
  },
  sensorPill: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    borderRadius: 4,
    paddingHorizontal: 6,
    paddingVertical: 2,
  },
  sensorPillText: {
    fontFamily: Fonts?.mono,
    color: T.electricBlue,
    fontSize: 10,
    fontWeight: "800",
  },
  musclesSection: {
    marginBottom: 14,
  },
  expandHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 8,
    gap: 8,
  },
  expandChevron: {
    fontFamily: Fonts?.mono,
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "800",
  },
  compactGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  compactItem: {
    width: "48%",
    backgroundColor: T.surfaceMuted,
    padding: 9,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: T.border,
  },
  compactItemHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 6,
    gap: 4,
  },
  compactName: {
    color: T.inkSecondary,
    fontSize: 11,
    fontWeight: "700",
    flex: 1,
  },
  compactPercent: {
    fontFamily: Fonts?.mono,
    fontSize: 11,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  progressBarBg: {
    height: 6,
    backgroundColor: T.surfaceContainer,
    borderRadius: 3,
    overflow: "hidden",
  },
  progressBarFill: {
    height: "100%",
    borderRadius: 3,
  },
  detailedList: {
    gap: 8,
  },
  detailedItem: {
    backgroundColor: T.surfaceMuted,
    padding: 10,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: T.border,
  },
  detailedHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 6,
    gap: 8,
  },
  detailedName: {
    color: T.ink,
    fontSize: 12,
    fontWeight: "800",
    flexShrink: 1,
  },
  stateTag: {
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
    borderWidth: 1,
  },
  stateTagText: {
    fontFamily: Fonts?.mono,
    fontSize: 10,
    fontWeight: "800",
  },
  muscleNote: {
    color: T.inkSecondary,
    fontSize: 11,
    marginTop: 6,
    lineHeight: 15,
  },
  scalingSection: {
    marginBottom: 14,
    gap: 8,
  },
  scalingCard: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 10,
    padding: 10,
    borderWidth: 1,
    borderColor: T.border,
    borderLeftWidth: 3,
    borderLeftColor: T.statusFatigued,
  },
  scalingHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
    gap: 8,
  },
  scalingMovement: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "800",
    flexShrink: 1,
  },
  scalingBadge: {
    backgroundColor: T.statusFatiguedBg,
    borderWidth: 1,
    borderColor: T.statusFatiguedBorder,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  scalingBadgeText: {
    color: T.statusFatigued,
    fontSize: 10,
    fontWeight: "800",
  },
  scalingDetail: {
    color: T.inkSecondary,
    fontSize: 12,
    lineHeight: 17,
  },
  mobilitySection: {
    gap: 8,
  },
  mobilityCard: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 10,
    padding: 10,
    borderWidth: 1,
    borderColor: T.border,
    borderLeftWidth: 3,
    borderLeftColor: T.statusFresh,
  },
  mobilityHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 2,
    gap: 6,
  },
  mobilityTitle: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "800",
    flex: 1,
  },
  durationBadge: {
    backgroundColor: T.statusFreshBg,
    borderWidth: 1,
    borderColor: T.statusFreshBorder,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  durationText: {
    fontFamily: Fonts?.mono,
    color: T.statusFresh,
    fontSize: 10,
    fontWeight: "800",
  },
  mobilityTarget: {
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "700",
    marginTop: 2,
  },
  mobilityReason: {
    color: T.inkSecondary,
    fontSize: 12,
    lineHeight: 16,
    marginTop: 4,
  },
});
