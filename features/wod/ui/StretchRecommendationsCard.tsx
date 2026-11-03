import React from "react";
import { View, Text, StyleSheet, TouchableOpacity } from "react-native";
import { TacticalTheme } from "@/constants/theme";
import { t } from "@/features/i18n";

const T = TacticalTheme.light;

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
  onPressItem?: (item: StretchRecommendationItem) => void;
}

export function StretchRecommendationsCard({
  recommendations,
  onPressItem,
}: StretchRecommendationsCardProps) {
  if (!recommendations || recommendations.length === 0) {
    return null;
  }

  return (
    <View style={styles.card}>
      <Text style={styles.title}>{t("stretchRecs.title")}</Text>
      <View style={styles.list}>
        {recommendations.map((item, idx) => {
          const content = (
            <>
              <View style={styles.headerRow}>
                <View style={styles.leftHeader}>
                  <Text style={styles.stretchName}>{item.stretch}</Text>
                  <View style={styles.targetBadge}>
                    <Text style={styles.targetText}>{item.target_area}</Text>
                  </View>
                </View>
                <View style={styles.rightHeader}>
                  {item.provisional && (
                    <View style={styles.provisionalBadge}>
                      <Text style={styles.provisionalText}>
                        {t("stretchRecs.provisional")}
                      </Text>
                    </View>
                  )}
                  {onPressItem && (
                    <Text style={styles.chevron}>›</Text>
                  )}
                </View>
              </View>

              <Text style={styles.reasonText}>{item.reason}</Text>

              {item.duration_hint ? (
                <Text style={styles.hintText}>⏱️ {item.duration_hint}</Text>
              ) : null}

              {item.caution ? (
                <Text style={styles.cautionText}>⚠️ {item.caution}</Text>
              ) : null}

              {onPressItem ? (
                <Text style={styles.tapDetailHint}>
                  {t("stretchRecs.tapForDetail")}
                </Text>
              ) : null}
            </>
          );

          if (onPressItem) {
            return (
              <TouchableOpacity
                key={`${item.stretch}-${idx}`}
                style={styles.itemRow}
                activeOpacity={0.7}
                onPress={() => onPressItem(item)}
              >
                {content}
              </TouchableOpacity>
            );
          }

          return (
            <View key={`${item.stretch}-${idx}`} style={styles.itemRow}>
              {content}
            </View>
          );
        })}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    padding: 14,
    marginTop: 12,
    borderWidth: 1,
    borderColor: T.border,
  },
  title: {
    color: T.ink,
    fontSize: 15,
    fontWeight: "800",
    marginBottom: 10,
  },
  list: {
    gap: 10,
  },
  itemRow: {
    backgroundColor: T.surface,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.border,
    padding: 12,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 6,
    gap: 8,
  },
  leftHeader: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 8,
    flex: 1,
  },
  rightHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  chevron: {
    color: T.inkMuted,
    fontSize: 18,
    fontWeight: "700",
    marginLeft: 4,
  },
  stretchName: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "800",
    flexShrink: 1,
  },
  targetBadge: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 7,
    paddingVertical: 2,
    borderRadius: 6,
  },
  targetText: {
    color: T.voltInk,
    fontSize: 11,
    fontWeight: "800",
  },
  provisionalBadge: {
    backgroundColor: T.statusWarningBg,
    borderWidth: 1,
    borderColor: T.statusWarningBorder,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 6,
  },
  provisionalText: {
    color: T.statusWarning,
    fontSize: 11,
    fontWeight: "700",
  },
  reasonText: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 18,
  },
  hintText: {
    color: T.inkMuted,
    fontSize: 11,
    marginTop: 6,
  },
  cautionText: {
    color: T.statusExhausted,
    fontWeight: "600",
    fontSize: 11,
    marginTop: 4,
  },
  tapDetailHint: {
    color: T.electricBlue,
    fontSize: 11,
    marginTop: 6,
    fontWeight: "700",
  },
});
