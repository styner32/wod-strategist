import React from "react";
import { View, Text, StyleSheet } from "react-native";
import { t } from "@/features/i18n";

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
                <Text style={styles.dateText}>{formatDate(item.created_at)}</Text>
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
    backgroundColor: "#141A22",
    borderRadius: 12,
    padding: 14,
    borderWidth: 1,
    borderColor: "#222",
    marginTop: 16,
  },
  title: {
    color: "#00E5FF",
    fontSize: 14,
    fontWeight: "600",
    marginBottom: 10,
  },
  list: {
    gap: 10,
  },
  itemRow: {
    backgroundColor: "#1A222D",
    borderRadius: 8,
    padding: 10,
    borderWidth: 1,
    borderColor: "#2A3442",
  },
  headerRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  },
  dateText: {
    color: "#888",
    fontSize: 12,
  },
  scoreBadge: {
    backgroundColor: "#1E2A38",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  scoreText: {
    color: "#00E5FF",
    fontSize: 11,
    fontWeight: "600",
  },
  descText: {
    color: "#E0E0E0",
    fontSize: 13,
    lineHeight: 18,
    marginBottom: 6,
  },
  chipRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  movementChip: {
    backgroundColor: "#243040",
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  chipText: {
    color: "#FFF",
    fontSize: 12,
    fontWeight: "500",
  },
  mainBadge: {
    backgroundColor: "#FF9800",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  mainBadgeText: {
    color: "#000",
    fontSize: 10,
    fontWeight: "700",
  },
});
