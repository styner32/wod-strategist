import { Fonts, TacticalTheme } from "@/constants/theme";
import { t } from "@/features/i18n";
import React from "react";
import { StyleSheet, Text, View } from "react-native";

const T = TacticalTheme.light;

export interface NormalizedMovement {
  movement: string;
  weight_raw?: string;
  weight_kg?: number | null;
  reps?: string;
  is_main: boolean;
}

export interface RelatedWODItem {
  session_id: string;
  analysis_id: number;
  created_at: string;
  wod_description: string;
  movement: NormalizedMovement;
  session_score?: string;
  score: number;
  score_parts?: {
    recency: number;
    weight: number;
    main: number;
  };
}

interface RelatedWodsCardProps {
  related: RelatedWODItem[];
}

function parseOverallScore(sessionScoreJson?: string): number | null {
  if (!sessionScoreJson) return null;
  try {
    const parsed = JSON.parse(sessionScoreJson);
    if (typeof parsed.overall === "number") {
      return parsed.overall;
    }
  } catch {}
  return null;
}

function formatDate(dateStr: string): string {
  try {
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return dateStr;
    return d.toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return dateStr;
  }
}

export function RelatedWodsCard({ related }: RelatedWodsCardProps) {
  if (!related || related.length === 0) {
    return null;
  }

  return (
    <View style={styles.card}>
      <Text style={styles.title}>{t("relatedWods.title")}</Text>
      <View style={styles.list}>
        {related.map((item) => {
          const overallScore = parseOverallScore(item.session_score);
          const weightLabel =
            item.movement.weight_raw ||
            (item.movement.weight_kg != null
              ? `${item.movement.weight_kg}kg`
              : "");

          return (
            <View key={item.session_id} style={styles.itemRow}>
              <View style={styles.headerRow}>
                <Text style={styles.dateText}>
                  {formatDate(item.created_at)}
                </Text>
                {overallScore != null && (
                  <View style={styles.scoreBadge}>
                    <Text style={styles.scoreText}>
                      {t("relatedWods.score")} {overallScore}
                    </Text>
                  </View>
                )}
              </View>

              {item.wod_description ? (
                <Text style={styles.descText} numberOfLines={2}>
                  {item.wod_description}
                </Text>
              ) : null}

              <View style={styles.chipRow}>
                <View style={styles.movementChip}>
                  <Text style={styles.chipText}>
                    {item.movement.movement}
                    {weightLabel ? ` · ${weightLabel}` : ""}
                  </Text>
                </View>
                {item.movement.is_main && (
                  <View style={styles.mainBadge}>
                    <Text style={styles.mainBadgeText}>
                      {t("relatedWods.mainBadge")}
                    </Text>
                  </View>
                )}
              </View>
            </View>
          );
        })}
      </View>
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
  title: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.3,
    marginBottom: 10,
  },
  list: {
    gap: 10,
  },
  itemRow: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    padding: 12,
    borderWidth: 1,
    borderColor: T.border,
  },
  headerRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
    gap: 8,
  },
  dateText: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "600",
    flexShrink: 1,
  },
  scoreBadge: {
    backgroundColor: T.electricBlueBg,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  scoreText: {
    fontFamily: Fonts?.mono,
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  descText: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 18,
    marginBottom: 8,
  },
  chipRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
    alignItems: "center",
  },
  movementChip: {
    backgroundColor: T.surface,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  chipText: {
    fontFamily: Fonts?.mono,
    color: T.ink,
    fontSize: 11,
    fontWeight: "700",
  },
  mainBadge: {
    backgroundColor: T.surfaceInverse,
    paddingHorizontal: 7,
    paddingVertical: 3,
    borderRadius: 6,
  },
  mainBadgeText: {
    fontFamily: Fonts?.mono,
    color: T.volt,
    fontSize: 10,
    fontWeight: "800",
    textTransform: "uppercase",
  },
});
