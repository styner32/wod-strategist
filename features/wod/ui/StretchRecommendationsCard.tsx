import React from "react";
import { View, Text, StyleSheet } from "react-native";
import { t } from "@/features/i18n";

export interface StretchRecommendationItem {
  stretch: string;
  target_area: string;
  reason: string;
  duration_hint?: string;
  caution?: string;
  provisional?: boolean;
}

interface StretchRecommendationsCardProps {
  recommendations: StretchRecommendationItem[];
}

export function StretchRecommendationsCard({ recommendations }: StretchRecommendationsCardProps) {
  if (!recommendations || recommendations.length === 0) {
    return null;
  }

  return (
    <View style={styles.card}>
      <Text style={styles.title}>{t("stretchRecs.title")}</Text>
      <View style={styles.list}>
        {recommendations.map((item, idx) => (
          <View key={`${item.stretch}-${idx}`} style={styles.itemRow}>
            <View style={styles.headerRow}>
              <View style={styles.leftHeader}>
                <Text style={styles.stretchName}>{item.stretch}</Text>
                <View style={styles.targetBadge}>
                  <Text style={styles.targetText}>{item.target_area}</Text>
                </View>
              </View>
              {item.provisional && (
                <View style={styles.provisionalBadge}>
                  <Text style={styles.provisionalText}>{t("stretchRecs.provisional")}</Text>
                </View>
              )}
            </View>

            <Text style={styles.reasonText}>{item.reason}</Text>

            {item.duration_hint ? (
              <Text style={styles.hintText}>⏱️ {item.duration_hint}</Text>
            ) : null}

            {item.caution ? (
              <Text style={styles.cautionText}>⚠️ {item.caution}</Text>
            ) : null}
          </View>
        ))}
      </View>
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
  title: {
    color: "#FFFFFF",
    fontSize: 15,
    fontWeight: "700",
    marginBottom: 10,
  },
  list: {
    gap: 10,
  },
  itemRow: {
    backgroundColor: "#2C2C2E",
    borderRadius: 10,
    padding: 12,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 6,
  },
  leftHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  stretchName: {
    color: "#FFD60A",
    fontSize: 14,
    fontWeight: "700",
  },
  targetBadge: {
    backgroundColor: "rgba(255, 214, 10, 0.15)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  targetText: {
    color: "#FFD60A",
    fontSize: 11,
    fontWeight: "600",
  },
  provisionalBadge: {
    backgroundColor: "rgba(255, 159, 10, 0.2)",
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  provisionalText: {
    color: "#FF9F0A",
    fontSize: 11,
    fontWeight: "600",
  },
  reasonText: {
    color: "#E5E5EA",
    fontSize: 13,
    lineHeight: 18,
  },
  hintText: {
    color: "#8E8E93",
    fontSize: 11,
    marginTop: 6,
  },
  cautionText: {
    color: "#FF453A",
    fontSize: 11,
    marginTop: 4,
  },
});
