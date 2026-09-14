import React, { useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { t, useLocale } from "@/features/i18n";
import {
  HEART_RATE_ZONE_COLORS, hrNumber, hrPercent, type HeartRateSummary,
} from "@/shared/heartRateSummary";

export function HeartRateSummaryCard({ summary }: { summary?: HeartRateSummary }) {
  useLocale();
  const [expanded, setExpanded] = useState(false);
  const status = summary?.status ?? "unavailable";
  const label = (key: string) => t(`heartRate.${key}`);
  const seconds = (v?: number) => hrNumber(v, ` ${label("seconds")}`);
  const rows: [string, string][] = summary ? [
    ["average", hrNumber(summary.avg_bpm, " bpm")],
    ["peak", hrNumber(summary.peak_bpm, " bpm")],
    ["minimum", hrNumber(summary.min_bpm, " bpm")],
    ["coverage", hrPercent(summary.coverage)],
    ["valid", seconds(summary.valid_seconds)],
    ["excluded", seconds(summary.excluded_seconds)],
    ["unknown", seconds(summary.unknown_seconds)],
  ] : [];

  return (
    <View style={styles.card}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={label("details")}
        accessibilityState={{ expanded }}
        onPress={() => setExpanded(!expanded)}
      >
        <Text style={styles.title}>
          ♥ {summary?.device_name || label("title")} · {label(status)} {expanded ? "⌃" : "⌄"}
        </Text>
        {summary?.avg_bpm !== undefined && (
          <Text style={styles.text}>
            {label("average")} {hrNumber(summary.avg_bpm)} bpm · {label("peak")} {hrNumber(summary.peak_bpm)} bpm
          </Text>
        )}
        {summary?.calculation_version === 1 && <Text style={styles.warning}>{label("legacy")}</Text>}
      </Pressable>
      {expanded && summary && status !== "none" && (
        <View style={styles.details}>
          {summary.coverage !== undefined && rows.map(([key, value]) => (
            <View style={styles.row} key={key}>
              <Text style={styles.text}>{label(key)}</Text><Text style={styles.text}>{value}</Text>
            </View>
          ))}
          <Text style={styles.text}>{label(summary.application_reason)}</Text>
          {summary.cardio_delta !== undefined && (
            <>
              <Text style={styles.text}>
                {label("cardio")}: {hrNumber(summary.cardio_before)} → {hrNumber(summary.cardio_after)} (+{summary.cardio_delta})
              </Text>
              <Text style={styles.text}>{label("cardioNote")}</Text>
            </>
          )}
          {summary.coverage !== undefined && (
            <>
              <Text style={styles.text}>
                {summary.contact_coverage
                  ? `${label("contactCoverage")}: ${hrPercent(summary.contact_coverage)}`
                  : label("contactUnknown")}
              </Text>
              {Object.entries(summary.excluded_by_reason ?? {})
                .filter(([, time]) => time > 0)
                .map(([reason, time]) => (
                  <Text key={reason} style={styles.warning}>{label(reason)}: {seconds(time)}</Text>
                ))}
              {!!summary.low_bpm_seconds && (
                <Text style={styles.warning}>{label("lowDuration")}: {seconds(summary.low_bpm_seconds)}</Text>
              )}
              {summary.zones?.length ? (
                <>
                  <Text style={styles.title}>{label("zones")}</Text>
                  <View style={styles.bar}>
                    {summary.zones.map(z => (
                      <View key={z.zone} style={{ flex: Math.max(0, z.ratio), backgroundColor: HEART_RATE_ZONE_COLORS[z.zone - 1] }} />
                    ))}
                  </View>
                  {summary.zones.map(z => (
                    <Text key={z.zone} style={styles.text}>
                      <Text style={{ color: HEART_RATE_ZONE_COLORS[z.zone - 1] }}>● </Text>
                      {label("zone")} {z.zone}: {seconds(z.seconds)} · {hrPercent(z.ratio)}
                    </Text>
                  ))}
                  <Text style={styles.text}>
                    {label("maxEstimate")}: {hrNumber(summary.max_bpm)} bpm · {label(summary.max_bpm_source === "estimated_220_minus_age" ? "estimated_220_minus_age" : "unknownEstimate")}
                  </Text>
                </>
              ) : summary.max_bpm === undefined ? <Text style={styles.text}>{label("zoneUnavailable")}</Text> : null}
            </>
          )}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  card: { backgroundColor: "#1C1C1E", borderRadius: 12, padding: 12, marginVertical: 8 },
  title: { color: "#f4f4f4", fontWeight: "600", fontSize: 13, marginBottom: 4 },
  text: { color: "#ddd", fontSize: 12, marginVertical: 2 },
  warning: { color: "#ffbe72", fontSize: 12, marginVertical: 2 },
  details: { marginTop: 8, gap: 4 },
  row: { flexDirection: "row", justifyContent: "space-between" },
  bar: { flexDirection: "row", height: 12, borderRadius: 6, overflow: "hidden", backgroundColor: "#444" },
});
