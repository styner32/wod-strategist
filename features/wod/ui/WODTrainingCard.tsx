import React, { useEffect, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { t, useLocale } from "@/features/i18n";
import { getWODTraining, getWODTrainingLoad, updateWODEffort } from "../api";
import { newWODScore, wodSeconds, type WODTraining, type WODTrainingLoad } from "../../../shared/wodTraining";

export function WODTrainingCard({ sessionId, profileId, scoreJSON }: { sessionId: string; profileId?: number; scoreJSON?: string }) {
  useLocale();
  const score = newWODScore(scoreJSON);
  const [expanded, setExpanded] = useState(false);
  const [data, setData] = useState<WODTraining>();
  const [loads, setLoads] = useState<WODTrainingLoad>();
  const [effort, setEffort] = useState<number>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!score || !profileId || !expanded) return;
    let active = true;
    Promise.all([getWODTraining(sessionId), getWODTrainingLoad(profileId)]).then(([a, b]) => { if (active) { setData(a); setLoads(b); setError(""); } }).catch(() => { if (active) setError(t("wodTraining.failed")); });
    return () => { active = false; };
  }, [sessionId, profileId, expanded, scoreJSON]);
  if (!score || !profileId) return null;
  const save = async () => {
    if (effort == null || saving) return;
    setSaving(true); setError("");
    try { const result = await updateWODEffort(sessionId, effort); setData(result); setEffort(undefined); setLoads(await getWODTrainingLoad(profileId)); }
    catch { setError(t("wodTraining.failed")); }
    finally { setSaving(false); }
  };
  const comparison = loads?.comparison;
  return <View style={styles.card}>
    <Pressable accessibilityRole="button" accessibilityState={{ expanded }} onPress={() => setExpanded(!expanded)}><Text style={styles.title}>{t("wodTraining.title")} · {expanded ? "−" : "+"}</Text></Pressable>
    <Text style={styles.text}>{t("wodTraining.start", { time: wodSeconds(score.wod_start.seconds) })}</Text>
    <Text style={styles.text}>{t("wodTraining.intensity", { score: score.intensity ?? t("wodTraining.unknown") })}</Text>
    {expanded && <>
      <Text style={styles.text}>{t("wodTraining.effortHelp")}</Text>
      <View style={styles.row}>{Array.from({ length: 10 }, (_, i) => i + 1).map(n => <Pressable key={n} accessibilityRole="button" accessibilityLabel={t("wodTraining.effortChoice", { score: n })} accessibilityState={{ selected: (effort ?? data?.effort) === n }} disabled={saving} onPress={() => setEffort(n)} style={[styles.choice, (effort ?? data?.effort) === n && styles.selected]}><Text style={styles.text}>{n}</Text></Pressable>)}</View>
      <Pressable accessibilityRole="button" disabled={effort == null || saving} onPress={() => void save()} style={styles.choice}><Text style={styles.text}>{t(saving ? "wodTraining.saving" : "wodTraining.save")}</Text></Pressable>
      <Text style={styles.text}>{t("wodTraining.sessionLoad", { value: data?.session_load == null ? t("wodTraining.unknown") : data.session_load.toFixed(1) })}</Text>
      {comparison && <>
        <Text style={styles.text}>{t("wodTraining.averages", { seven: comparison.seven.daily_average.toFixed(1), twentyEight: comparison.twenty_eight.daily_average.toFixed(1) })}</Text>
        <Text style={styles.text}>{comparison.change_percent == null ? t(`wodTraining.${comparison.status}`) : t("wodTraining.change", { value: `${comparison.change_percent >= 0 ? "+" : ""}${comparison.change_percent.toFixed(1)}` })}</Text>
        <Text style={styles.text}>{t("wodTraining.missing", { effort: comparison.twenty_eight.missing_effort, duration: comparison.twenty_eight.missing_duration, time: loads?.missing_workout_time ?? 0 })}</Text>
        {(loads?.unevaluated_legacy_workouts ?? 0) > 0 && <Text style={styles.text}>{t("wodTraining.missingLegacy", { count: loads?.unevaluated_legacy_workouts ?? 0 })}</Text>}
      </>}
      <Text style={styles.text}>{t("wodTraining.scope")}</Text>
      {!!error && <Text accessibilityRole="alert" style={styles.error}>{error}</Text>}
    </>}
  </View>;
}
const styles = StyleSheet.create({
  card: { backgroundColor: "#1C1C1E", borderRadius: 12, padding: 12, marginVertical: 8, gap: 8 },
  title: { color: "#f4f4f4", fontWeight: "600", fontSize: 14 },
  text: { color: "#ddd", fontSize: 12 },
  row: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  choice: { padding: 12, borderRadius: 8, borderWidth: 1, borderColor: "#666" },
  selected: { backgroundColor: "#3868b0" },
  error: { color: "#ffbe72" },
});
