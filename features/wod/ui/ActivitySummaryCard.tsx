import React, { useEffect, useState } from "react";
import { Text, TouchableOpacity, View } from "react-native";
import { t, useLocale } from "../../i18n";
import { fetchActivitySummary } from "../api";
import { activityReviewPending, type ActivitySummary } from "../../../shared/activity";

export function ActivitySummaryContent({ summary, compact = false }: { summary: ActivitySummary | null; compact?: boolean }) {
  useLocale();
  const [showGaps, setShowGaps] = useState(false);
  if (!summary?.available) return null;
  return <View style={{ padding: 12, borderRadius: 10, backgroundColor: "#172435", gap: 5 }}>
    <Text style={{ color: "#fff", fontWeight: "600" }}>{t("activity.title")}</Text>
    <Text style={{ color: "#b9d8ff" }}>{t(`activity.state.${summary.review_state}`)}</Text>
    {(compact ? summary.movements.slice(-3) : summary.movements).map(item => <Text key={`${item.movement}/${item.unit}`} style={{ color: "#fff" }}>
      {item.movement}: {item.unit === "reps" ? t("activity.reps", { count: item.count }) : t("activity.seconds", { count: Math.round(item.seconds * 10) / 10 })}
    </Text>)}
    {summary.movements.length === 0 && <Text style={{ color: "#b9d8ff" }}>{t("activity.noEvidence")}</Text>}
    {compact && summary.movements.length > 3 && <Text style={{ color: "#b9d8ff" }}>{t("activity.more", { count: summary.movements.length - 3 })}</Text>}
    {summary.unassessed.length > 0 && <TouchableOpacity disabled={compact} onPress={() => setShowGaps(value => !value)} accessibilityRole="button">
      <Text style={{ color: "#ffd28a" }}>{t("activity.gaps", { count: summary.unassessed.length })}</Text>
    </TouchableOpacity>}
    {!compact && showGaps && summary.unassessed.map((gap, index) => <Text key={index} style={{ color: "#ffd28a", fontSize: 12 }}>
      {t(gap.clock === "media" ? "activity.mediaTime" : gap.clock === "chunk" ? "activity.chunkTime" : "activity.captureTime")}: {gap.start_secs == null || gap.end_secs == null ? t("activity.unknownTime") : `${gap.start_secs.toFixed(1)}–${gap.end_secs.toFixed(1)}s`}
    </Text>)}
    {!compact && <Text style={{ color: "#b6bfca", fontSize: 11 }}>{t("activity.observedOnly")}</Text>}
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
