import React, { useEffect, useState } from "react";
import { Text, TouchableOpacity, View } from "react-native";
import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "../../i18n";
import { fetchActivitySummary } from "../api";
import { activityReviewPending, type ActivitySummary } from "../../../shared/activity";

const T = TacticalTheme.light;

export function ActivitySummaryContent({ summary, compact = false }: { summary: ActivitySummary | null; compact?: boolean }) {
  useLocale();
  const [showGaps, setShowGaps] = useState(false);
  if (!summary?.available) return null;
  const containerStyle = compact
    ? { padding: 8, borderRadius: 8, backgroundColor: "#172435", gap: 3 }
    : {
        padding: 12,
        borderRadius: 12,
        backgroundColor: T.surfaceMuted,
        borderWidth: 1,
        borderColor: T.border,
        marginVertical: 6,
        gap: 5,
      };
  const titleColor = compact ? "#fff" : T.ink;
  const bodyColor = compact ? "#fff" : T.inkSecondary;
  const accentColor = compact ? "#b9d8ff" : T.electricBlue;
  const warningColor = compact ? "#ffd28a" : T.statusWarning;
  const noteColor = compact ? "#b6bfca" : T.inkMuted;

  return <View style={containerStyle}>
    <Text style={{ color: titleColor, fontWeight: compact ? "600" : "800", fontSize: compact ? 12 : 14 }}>{t("activity.title")}</Text>
    <Text style={{ color: accentColor, fontWeight: compact ? "500" : "600", fontSize: compact ? 11 : 13 }}>{t(`activity.state.${summary.review_state}`)}</Text>
    {(compact ? summary.movements.slice(-3) : summary.movements).map(item => <Text key={`${item.movement}/${item.unit}`} style={{ color: bodyColor, fontSize: compact ? 12 : 13 }}>
      {item.movement}: {item.unit === "reps" ? t("activity.reps", { count: item.count }) : t("activity.seconds", { count: Math.round(item.seconds * 10) / 10 })}
    </Text>)}
    {summary.movements.length === 0 && <Text style={{ color: accentColor, fontSize: compact ? 11 : 13 }}>{t("activity.noEvidence")}</Text>}
    {compact && summary.movements.length > 3 && <Text style={{ color: accentColor, fontSize: 11 }}>{t("activity.more", { count: summary.movements.length - 3 })}</Text>}
    {summary.unassessed.length > 0 && <TouchableOpacity disabled={compact} onPress={() => setShowGaps(value => !value)} accessibilityRole="button">
      <Text style={{ color: warningColor, fontWeight: "600", fontSize: compact ? 11 : 13 }}>{t("activity.gaps", { count: summary.unassessed.length })}</Text>
    </TouchableOpacity>}
    {!compact && showGaps && summary.unassessed.map((gap, index) => <Text key={index} style={{ color: warningColor, fontSize: 12 }}>
      {t(gap.clock === "media" ? "activity.mediaTime" : gap.clock === "chunk" ? "activity.chunkTime" : "activity.captureTime")}: {gap.start_secs == null || gap.end_secs == null ? t("activity.unknownTime") : `${gap.start_secs.toFixed(1)}–${gap.end_secs.toFixed(1)}s`}{gap.movement ? ` · ${gap.movement}` : ""}
    </Text>)}
    {!compact && <Text style={{ color: noteColor, fontSize: 11 }}>{t("activity.observedOnly")}</Text>}
  </View>;
}

export function ActivitySummaryCard({ sessionId, profileId }: { sessionId: string; profileId?: number }) {
  const [summary, setSummary] = useState<ActivitySummary | null>(null);
  useEffect(() => {
    setSummary(null);
    if (!profileId) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    const started = Date.now();
    const load = async () => {
      try {
        const value = await fetchActivitySummary(sessionId, profileId, controller.signal);
        if (!active) return;
        setSummary(value);
        if (activityReviewPending(value) && Date.now() - started < 30 * 60 * 1000) timer = setTimeout(() => void load(), 5000);
      } catch {
        if (active && Date.now() - started < 30 * 60 * 1000) timer = setTimeout(() => void load(), 15000);
      }
    };
    void load();
    return () => { active = false; controller.abort(); clearTimeout(timer); };
  }, [sessionId, profileId]);
  return <ActivitySummaryContent summary={summary} />;
}
