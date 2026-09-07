import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
import React, { useState } from "react";
import {
  ActivityIndicator,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import type { PreWodAdviceResponse } from "../api";

interface PreWodStrategyCardProps {
  advice: PreWodAdviceResponse | null;
  loading?: boolean;
}

function getReadinessColor(state: string): string {
  switch (state) {
    case "fresh":
      return "#00E676"; // Green
    case "moderate":
      return "#00E5FF"; // Cyan
    case "fatigued":
      return "#FF9800"; // Orange
    case "exhausted":
      return "#FF5252"; // Red
    default:
      return "#00E5FF";
  }
}

export function PreWodStrategyCard({
  advice,
  loading,
}: PreWodStrategyCardProps) {
  const [musclesExpanded, setMusclesExpanded] = useState(false);

  if (loading) {
    return (
      <View style={styles.card}>
        <View style={styles.loadingContainer}>
          <ActivityIndicator color="#00E5FF" size="small" />
          <Text style={styles.loadingText}>
            {t("preWod.analyzingFatigue") ||
              "신체 부위별 피로도 및 맞춤 전략 분석 중..."}
          </Text>
        </View>
      </View>
    );
  }

  if (!advice) {
    return null;
  }

  const overallColor = getReadinessColor(advice.overall_state);

  return (
    <View style={styles.card}>
      {/* Header */}
      <View style={styles.headerRow}>
        <View style={styles.headerLeft}>
          <IconSymbol name="flame.fill" size={18} color="#00E5FF" />
          <Text style={styles.title}>
            {t("preWod.title") || "오늘의 맞춤 WOD 전략 & 피로도"}
          </Text>
        </View>
        <View style={styles.headerBadges}>
          {advice.evidence_status === "partial" && (
            <View style={styles.partialBadge}>
              <Text style={styles.partialBadgeText}>
                {t("preWod.partialHistoryBadge") || "일부 기록만 반영됨"}
              </Text>
            </View>
          )}
          <View
            style={[
              styles.overallBadge,
              {
                borderColor: overallColor + "60",
                backgroundColor: overallColor + "15",
              },
            ]}
          >
            <Text style={[styles.overallBadgeText, { color: overallColor }]}>
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
        <View style={styles.summaryBox}>
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
              {t("preWod.pacingStrategy") || "페이스 운용 전략"}
            </Text>
          </View>
          {advice.target_rpe.pacing_strategy ? (
            <Text style={styles.pacingText}>
              {advice.target_rpe.pacing_strategy}
            </Text>
          ) : null}
        </View>
      ) : null}

      {/* 6-Muscle Group Readiness Bars */}
      {advice.muscle_readiness && advice.muscle_readiness.length > 0 ? (
        <View style={styles.musclesSection}>
          <TouchableOpacity
            style={styles.expandHeader}
            onPress={() => setMusclesExpanded(!musclesExpanded)}
            activeOpacity={0.7}
          >
            <Text style={styles.sectionLabel}>
              {t("preWod.muscleReadiness") || "신체 6대 부위별 피로도 분석"}
            </Text>
            <Text style={styles.expandChevron}>
              {musclesExpanded ? "▲ 접기" : "▼ 상세보기"}
            </Text>
          </TouchableOpacity>

          {/* Compact Bar Summary (when collapsed) */}
          {!musclesExpanded ? (
            <View style={styles.compactGrid}>
              {advice.muscle_readiness.map((m) => {
                const color = getReadinessColor(m.state);
                return (
                  <View key={m.group} style={styles.compactItem}>
                    <View style={styles.compactItemHeader}>
                      <Text style={styles.compactName} numberOfLines={1}>
                        {m.name_ko}
                      </Text>
                      <Text style={[styles.compactPercent, { color }]}>
                        {m.fatigue_score}/100
                      </Text>
                    </View>
                    <View style={styles.progressBarBg}>
                      <View
                        style={[
                          styles.progressBarFill,
                          {
                            width: `${Math.min(100, Math.max(5, m.fatigue_score))}%`,
                            backgroundColor: color,
                          },
                        ]}
                      />
                    </View>
                  </View>
                );
              })}
            </View>
          ) : (
            /* Detailed Cards (when expanded) */
            <View style={styles.detailedList}>
              {advice.muscle_readiness.map((m) => {
                const color = getReadinessColor(m.state);
                return (
                  <View key={m.group} style={styles.detailedItem}>
                    <View style={styles.detailedHeader}>
                      <Text style={styles.detailedName}>{m.name_ko}</Text>
                      <View
                        style={[
                          styles.stateTag,
                          { backgroundColor: color + "20" },
                        ]}
                      >
                        <Text style={[styles.stateTagText, { color }]}>
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
                            backgroundColor: color,
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
          <Text style={styles.sectionLabel}>
            {t("preWod.scalingAdvice") || "스케일링 및 동작 조절 권장"}
          </Text>
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
            {t("preWod.recommendedMobility") ||
              "프리-WOD 필수 모빌리티 / 워밍업"}
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
                  타겟: {item.target_area}
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
    backgroundColor: "#141A22",
    borderRadius: 14,
    padding: 16,
    borderWidth: 1,
    borderColor: "#1E2630",
    marginTop: 16,
  },
  loadingContainer: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 14,
    gap: 10,
  },
  loadingText: {
    color: "#8E9BAE",
    fontSize: 13,
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
    gap: 8,
  },
  title: {
    color: "#00E5FF",
    fontSize: 15,
    fontWeight: "700",
    letterSpacing: 0.3,
  },
  headerBadges: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  partialBadge: {
    paddingHorizontal: 6,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: "rgba(255, 152, 0, 0.4)",
    backgroundColor: "rgba(255, 152, 0, 0.15)",
  },
  partialBadgeText: {
    color: "#FF9800",
    fontSize: 11,
    fontWeight: "600",
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
  summaryBox: {
    backgroundColor: "#0D131A",
    borderRadius: 10,
    padding: 12,
    borderLeftWidth: 3,
    borderLeftColor: "#00E5FF",
    marginBottom: 14,
  },
  summaryText: {
    color: "#E2E8F0",
    fontSize: 13,
    lineHeight: 19,
    fontWeight: "500",
  },
  rpeSection: {
    backgroundColor: "#1A222D",
    borderRadius: 10,
    padding: 12,
    marginBottom: 14,
    borderWidth: 1,
    borderColor: "#243040",
  },
  rpeHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginBottom: 6,
  },
  rpeBadge: {
    backgroundColor: "#FF9800",
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  rpeBadgeText: {
    color: "#000",
    fontSize: 11,
    fontWeight: "800",
  },
  sectionLabel: {
    color: "#8E9BAE",
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  pacingText: {
    color: "#CBD5E1",
    fontSize: 13,
    lineHeight: 18,
  },
  musclesSection: {
    marginBottom: 14,
  },
  expandHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 8,
  },
  expandChevron: {
    color: "#00E5FF",
    fontSize: 11,
    fontWeight: "600",
  },
  compactGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  compactItem: {
    width: "48%",
    backgroundColor: "#1A222D",
    padding: 8,
    borderRadius: 8,
  },
  compactItemHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  },
  compactName: {
    color: "#CBD5E1",
    fontSize: 11,
    fontWeight: "600",
    flex: 1,
    marginRight: 4,
  },
  compactPercent: {
    fontSize: 11,
    fontWeight: "700",
  },
  progressBarBg: {
    height: 4,
    backgroundColor: "#2D3748",
    borderRadius: 2,
    overflow: "hidden",
  },
  progressBarFill: {
    height: "100%",
    borderRadius: 2,
  },
  detailedList: {
    gap: 8,
  },
  detailedItem: {
    backgroundColor: "#1A222D",
    padding: 10,
    borderRadius: 8,
  },
  detailedHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 6,
  },
  detailedName: {
    color: "#FFF",
    fontSize: 12,
    fontWeight: "700",
  },
  stateTag: {
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  stateTagText: {
    fontSize: 10,
    fontWeight: "700",
  },
  muscleNote: {
    color: "#94A3B8",
    fontSize: 11,
    marginTop: 6,
    lineHeight: 15,
  },
  scalingSection: {
    marginBottom: 14,
    gap: 8,
  },
  scalingCard: {
    backgroundColor: "#1A222D",
    borderRadius: 8,
    padding: 10,
    borderLeftWidth: 3,
    borderLeftColor: "#FF9800",
  },
  scalingHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  },
  scalingMovement: {
    color: "#FFF",
    fontSize: 13,
    fontWeight: "700",
  },
  scalingBadge: {
    backgroundColor: "#FF980025",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  scalingBadgeText: {
    color: "#FF9800",
    fontSize: 10,
    fontWeight: "700",
  },
  scalingDetail: {
    color: "#CBD5E1",
    fontSize: 12,
    lineHeight: 16,
  },
  mobilitySection: {
    gap: 8,
  },
  mobilityCard: {
    backgroundColor: "#1A222D",
    borderRadius: 8,
    padding: 10,
    borderLeftWidth: 3,
    borderLeftColor: "#00E676",
  },
  mobilityHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 2,
  },
  mobilityTitle: {
    color: "#FFF",
    fontSize: 13,
    fontWeight: "700",
    flex: 1,
  },
  durationBadge: {
    backgroundColor: "#00E67620",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
    marginLeft: 6,
  },
  durationText: {
    color: "#00E676",
    fontSize: 10,
    fontWeight: "700",
  },
  mobilityTarget: {
    color: "#00E5FF",
    fontSize: 11,
    fontWeight: "600",
    marginTop: 2,
  },
  mobilityReason: {
    color: "#94A3B8",
    fontSize: 12,
    lineHeight: 16,
    marginTop: 4,
  },
});
