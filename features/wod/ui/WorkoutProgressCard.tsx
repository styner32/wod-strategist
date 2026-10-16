import React, { useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { t, useLocale } from "@/features/i18n";
import { confirmPerformanceRecords, getPerformanceBenchmarks, getWorkoutProgress } from "../api";
import {
  confirmedPerformanceInput, findCoveringConfirmedRecord, observedReps, performanceDraft, performanceSelectionCovered, progressTime,
  type PerformanceBenchmarksResponse, type PerformanceDraft, type PerformanceRecord,
  type WorkoutProgress, type WorkoutProgressInterval, type WorkoutProgressResponse,
} from "../../../shared/workoutProgress";

const label = (key: string, values?: Record<string, unknown>) => t(`workoutProgress.${key}`, values);
const loadLabel = (record: { load?: PerformanceRecord["load"] }) => record.load ? `${record.load.value} ${record.load.unit} · ${label(record.load.basis)}` : label("none");

function RestCycleComparisons({ record }: { record: PerformanceRecord }) {
  if (record.status !== "confirmed") return null;
  const seconds = (value: number | null) => value == null || !Number.isFinite(value) ? label("unknown") : label("restSeconds", { value: value.toFixed(2) });
  return <>{(record.rest_comparisons ?? []).map((rest, i) => <Text key={i} style={styles.text}>
    {label("restCycles")} · {progressTime(rest.rest_start_secs)}–{progressTime(rest.rest_end_secs)} · {label("restCycleSummary", { duration: seconds(rest.rest_elapsed_seconds), before: seconds(rest.before_median_cycle_seconds), after: seconds(rest.after_median_cycle_seconds) })} · {label("restCycleChange", { value: rest.cycle_change_percent == null || !Number.isFinite(rest.cycle_change_percent) ? label("unknown") : `${rest.cycle_change_percent >= 0 ? "+" : ""}${rest.cycle_change_percent.toFixed(1)}%` })}
  </Text>)}</>;
}

function ObservationEvidence({ interval }: { interval: WorkoutProgressInterval }) {
  const [visible, setVisible] = useState(false);
  const groups = [["form", interval.form_observations], ["rom", interval.rom_observations], ["pace", interval.pace_observations]] as const;
  return <View style={styles.gap}><Pressable accessibilityRole="button" accessibilityState={{ expanded: visible }} onPress={() => setVisible(v => !v)}><Text style={styles.text}>{label("evidence")} · {visible ? "−" : "+"}</Text></Pressable>{visible && <>
    {(interval.rep_events ?? []).map((event, i) => <Text key={`rep:${i}`} style={styles.text}>{progressTime(event.start_secs)}–{progressTime(event.end_secs)} · {label(event.complete ? "completeCycle" : "incompleteCycle")} · {event.evidence}</Text>)}
    {groups.flatMap(([kind, observations]) => (observations ?? []).map((o, i) => <Text key={`${kind}:${i}`} style={styles.text}>{label(kind)} · {progressTime(o.start_secs)}–{progressTime(o.end_secs)} · {o.evidence}</Text>))}
    {!(interval.rep_events?.length || groups.some(([, observations]) => observations?.length)) && <Text style={styles.text}>{label("unknown")}</Text>}
  </>}</View>;
}

function RecordEditor({ interval, progress, record, saving, onSave, onClose, onReviewVideo }: {
  interval: WorkoutProgressInterval; record?: PerformanceRecord; saving: boolean;
  progress: WorkoutProgress; onReviewVideo?: (start: number, end: number) => void;
  onSave: (draft: PerformanceDraft) => void; onClose: () => void;
}) {
  const [draft, setDraft] = useState(() => performanceDraft(interval, record));
  const update = <K extends keyof PerformanceDraft>(key: K, value: PerformanceDraft[K]) => setDraft(d => ({ ...d, [key]: value, reviewed: key === "reviewed" ? Boolean(value) : false }));
  const field = (key: "start" | "end" | "reps" | "weight", title: string) => <View style={styles.field}>
    <Text style={styles.text}>{label(title)}</Text>
    <TextInput accessibilityLabel={label(title)} keyboardType="decimal-pad" value={draft[key]} editable={!saving} onChangeText={v => update(key, v)} style={styles.input} />
  </View>;
  function choices<K extends keyof PerformanceDraft>(key: K, values: PerformanceDraft[K][], title: string) {
    return <View style={styles.gap}><Text style={styles.text}>{label(title)}</Text><View style={styles.row}>{values.map(v => <Pressable accessibilityRole="button" accessibilityState={{ selected: draft[key] === v }} disabled={saving} key={String(v)} onPress={() => update(key, v)} style={[styles.button, draft[key] === v && styles.selected]}><Text style={styles.text}>{key === "unit" ? String(v) : label(String(v))}</Text></Pressable>)}</View></View>;
  }
  const valid = confirmedPerformanceInput(interval, draft, progress) != null;
  const start = Number(draft.start), end = Number(draft.end);
  const canReview = draft.start.trim() !== "" && draft.end.trim() !== "" && performanceSelectionCovered(interval, start, end, draft.measurement, progress);
  return <View style={styles.editor}>
    <Text style={styles.text}>{label("reviewHelp")}</Text>
    <View style={styles.row}>{field("start", "start")}{field("end", "end")}{field("reps", "reps")}</View>
    {choices("loadMode", ["unresolved", "none", "weighted"], "loadMode")}
    {draft.loadMode === "weighted" && <>{field("weight", "weight")}{choices("unit", ["lb", "kg"], "unit")}{choices("basis", ["total", "per_implement"], "basis")}</>}
    {choices("measurement", ["set", "window"], "measurement")}
    {choices("technique", ["singles", "touch_and_go"], "technique")}
    {onReviewVideo && <Pressable accessibilityRole="button" disabled={!canReview} onPress={() => onReviewVideo(start, end)} style={[styles.button, !canReview && styles.disabled]}><Text style={styles.text}>{label("selectedPlay")}</Text></Pressable>}
    <Pressable accessibilityRole="checkbox" accessibilityLabel={label("reviewed")} accessibilityState={{ checked: draft.reviewed }} disabled={saving} onPress={() => update("reviewed", !draft.reviewed)} style={styles.button}><Text style={styles.text}>{draft.reviewed ? "☑" : "☐"} {label("reviewed")}</Text></Pressable>
    {draft.reviewed && !valid && <Text accessibilityRole="alert" style={styles.error}>{label("invalid")}</Text>}
    <View style={styles.row}><Pressable accessibilityRole="button" disabled={!valid || saving} onPress={() => onSave(draft)} style={[styles.button, (!valid || saving) && styles.disabled]}><Text style={styles.text}>{label(saving ? "saving" : "save")}</Text></Pressable><Pressable accessibilityRole="button" disabled={saving} onPress={onClose} style={styles.button}><Text style={styles.text}>{label("cancel")}</Text></Pressable></View>
  </View>;
}

function BenchmarkHistory({ data, sessionId }: { data: PerformanceBenchmarksResponse; sessionId: string }) {
  const records = data.records ?? [];
  return <View style={styles.gap}>
    <Text style={styles.title}>{label("history")}</Text>
    {records.length === 0 && <Text style={styles.text}>{label("noRecords")}</Text>}
    {records.map(r => {
      const comparison = (data.comparisons ?? []).find(c => c.current_record_id === r.id);
      const baseline = records.find(b => b.id === comparison?.baseline_record_id);
      const heavierBaseline = records.find(b => b.id === comparison?.heavier_load_baseline_record_id);
      return <View key={r.id} style={styles.editor}>
        <Text style={styles.text}>{r.movement} · {r.performed_at ? new Date(r.performed_at).toLocaleDateString() : label("unknown")}</Text>
        <Text style={styles.text}>{label("recordSummary", { count: r.reps, load: loadLabel(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })}</Text>
        <Text style={styles.text}>{label(r.measurement)} · {label(r.technique)} · {r.workout_prescription || label("unknown")}</Text>
        <Text style={styles.text}>{label("rate", { value: Number.isFinite(r.reps_per_minute) ? r.reps_per_minute.toFixed(1) : label("unknown") })} · {label(r.rate_basis)}</Text>
        <Text style={styles.text}>{label("cycle", { value: r.median_cycle_seconds == null ? label("unknown") : r.median_cycle_seconds.toFixed(2) })}</Text>
        <RestCycleComparisons record={r} />
        {comparison && r.session_id === sessionId && <>
          <Text style={styles.text}>{label(comparison.kind)}{baseline ? ` · ${baseline.reps} · ${loadLabel(baseline)}` : ""}</Text>
          {comparison.condition_differences.length > 0 && <Text style={styles.text}>{label("difference")}: {comparison.condition_differences.map(d => label(`differences.${d}`)).join(", ")}</Text>}
          {comparison.rate_change_percent != null && <Text style={styles.text}>{label("change", { value: `${comparison.rate_change_percent >= 0 ? "+" : ""}${comparison.rate_change_percent.toFixed(1)}` })}</Text>}
          {comparison.heavier_load_kg != null && comparison.heavier_load_kg > 0 && <Text style={styles.text}>{label("heavier", { value: comparison.heavier_load_kg.toFixed(1) })}{heavierBaseline ? ` · ${heavierBaseline.reps} · ${loadLabel(heavierBaseline)}` : ""}</Text>}
        </>}
      </View>;
    })}
    {(data.best ?? []).map(r => <Text key={r.id} style={styles.text}>{label("best")}: {label("recordSummary", { count: r.reps, load: loadLabel(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })} · {label("rate", { value: r.reps_per_minute.toFixed(1) })} · {label(r.measurement)} · {label(r.technique)} · {r.workout_prescription || label("unknown")}</Text>)}
    <Text style={styles.text}>{label("scope")}</Text>
  </View>;
}

export function WorkoutProgressCard({ sessionId, profileId, onReviewVideo }: {
  sessionId: string; profileId?: number; onReviewVideo?: (start: number, end: number) => void;
}) {
  useLocale();
  const scope = `${profileId}:${sessionId}`;
  const activeScope = useRef(scope);
  activeScope.current = scope;
  const request = useRef(0);
  const [expanded, setExpanded] = useState(false);
  const [data, setData] = useState<WorkoutProgressResponse>();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<string>();
  const [benchmarks, setBenchmarks] = useState<PerformanceBenchmarksResponse>();
  const [benchmarkError, setBenchmarkError] = useState(false);
  const [movement, setMovement] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const number = ++request.current;
    setData(undefined); setSelected(undefined); setBenchmarks(undefined); setMovement(""); setNotice(""); setError(""); setSaving(false); setBenchmarkError(false);
    if (!expanded || !profileId) return;
    const controller = new AbortController();
    setLoading(true);
    getWorkoutProgress(sessionId, controller.signal).then(r => { if (request.current === number) setData(r); }).catch(() => { if (request.current === number) setError(label("failed")); }).finally(() => { if (request.current === number) setLoading(false); });
    return () => { ++request.current; controller.abort(); };
  }, [scope, sessionId, profileId, expanded, revision]);
  useEffect(() => {
    setBenchmarks(undefined); setBenchmarkError(false);
    if (!expanded || !profileId || !movement) return;
    const controller = new AbortController(); let active = true;
    getPerformanceBenchmarks(profileId, movement, controller.signal).then(r => { if (active) setBenchmarks(r); }).catch(() => { if (active) setBenchmarkError(true); });
    return () => { active = false; controller.abort(); };
  }, [scope, profileId, movement, expanded, data]);
  const save = async (interval: WorkoutProgressInterval, draft: PerformanceDraft) => {
    const record = confirmedPerformanceInput(interval, draft, data?.progress ?? undefined);
    if (!record || !data?.progress || saving) return;
    const number = request.current;
    setSaving(true); setError(""); setNotice("");
    try {
      const result = await confirmPerformanceRecords(sessionId, { source_version: data.progress.source_version, records: [record] });
      if (number !== request.current || activeScope.current !== scope) return;
      setData(result); setSelected(undefined); setMovement(interval.movement ?? ""); setNotice(label("saved"));
    } catch (e) {
      if (number !== request.current || activeScope.current !== scope) return;
      if ((e as { status?: number })?.status === 409) {
        setSelected(undefined); setBenchmarks(undefined); setError(label("stale"));
        try { const latest = await getWorkoutProgress(sessionId); if (number === request.current && activeScope.current === scope) { setData(latest); setMovement(""); } }
        catch { /* Keep the review notice; the explicit reload remains available. */ }
      } else setError(label("failed"));
    } finally { if (number === request.current && activeScope.current === scope) setSaving(false); }
  };
  if (!profileId) return null;
  return <View style={styles.card}>
    <Pressable accessibilityRole="button" accessibilityState={{ expanded }} onPress={() => setExpanded(v => !v)}><Text style={styles.title}>{label("title")} · {expanded ? "−" : "+"}</Text></Pressable>
    {expanded && <>
      <Text style={styles.text}>{label("boundary")}</Text>
      {loading && <ActivityIndicator accessibilityLabel={label("loading")} />}
      {!!error && <><Text accessibilityRole="alert" style={styles.error}>{error}</Text><Pressable accessibilityRole="button" onPress={() => setRevision(v => v + 1)} style={styles.button}><Text style={styles.text}>{label("refresh")}</Text></Pressable></>}
      {!!notice && <Text style={styles.text}>{notice}</Text>}
      {data && !data.progress && <Text style={styles.text}>{label("unavailable")}</Text>}
      {data?.progress?.intervals.map(interval => {
        if (interval.activity === "rest") {
          return <View key={interval.id} testID={`rest-interval-${interval.id}`} style={styles.restRow}>
            <Text style={styles.restText}>{progressTime(interval.start_secs)}–{progressTime(interval.end_secs)} · {label("rest")}</Text>
            {onReviewVideo && <Pressable accessibilityRole="button" onPress={() => onReviewVideo(interval.start_secs, interval.end_secs)} style={styles.compactButton}><Text style={styles.restText}>{label("play")}</Text></Pressable>}
          </View>;
        }
        const record = data.records.find(r => r.interval_id === interval.id);
        const coveringConfirmed = findCoveringConfirmedRecord(interval, data.records);
        const isConfirmed = coveringConfirmed != null;
        return <View key={interval.id} testID={`exercise-interval-${interval.id}`} style={[styles.editor, isConfirmed && styles.confirmedEditor]}>
          <View style={styles.headerRow}>
            <Text style={styles.title}>{progressTime(interval.start_secs)}–{progressTime(interval.end_secs)} · {interval.movement || label("unknown")}</Text>
            {isConfirmed && <Text style={styles.confirmedBadge}>✓ {label("confirmed")}</Text>}
          </View>
          {interval.activity === "exercise" && <>
            <Text style={styles.text}>{label("observedReps", { count: observedReps(interval) ?? label("unknown") })}</Text>
            <Text style={styles.text}>{interval.load ? `${loadLabel(interval)} · ${label("candidate")}` : label("unknown")}</Text>
            {record && <><Text style={[styles.text, record.status === "confirmed" && styles.confirmedBadge]}>{label(record.status === "confirmed" ? "confirmed" : "needsConfirmation")} · {progressTime(record.start_secs)}–{progressTime(record.end_secs)} · {label("recordSummary", { count: record.reps, load: loadLabel(record), duration: (record.elapsed_seconds ?? (record.end_secs - record.start_secs)).toFixed(1) })}</Text><RestCycleComparisons record={record} /></>}
          </>}
          <View style={styles.row}>
            {onReviewVideo && <Pressable accessibilityRole="button" onPress={() => onReviewVideo(interval.start_secs, interval.end_secs)} style={styles.button}><Text style={styles.text}>{label("play")}</Text></Pressable>}
            {interval.activity === "exercise" && interval.movement && <Pressable accessibilityRole="button" disabled={saving} onPress={() => { setSelected(interval.id); setMovement(interval.movement!); setNotice(""); }} style={styles.button}><Text style={styles.text}>{label("edit")}</Text></Pressable>}
            {interval.activity === "exercise" && interval.movement && <Pressable accessibilityRole="button" onPress={() => setMovement(interval.movement!)} style={styles.button}><Text style={styles.text}>{label("benchmarks")}</Text></Pressable>}
          </View>
          <ObservationEvidence interval={interval} />
          {selected === interval.id && <RecordEditor key={`${data.progress?.source_version}:${interval.id}`} interval={interval} progress={data.progress!} record={record} saving={saving} onSave={draft => void save(interval, draft)} onClose={() => setSelected(undefined)} onReviewVideo={onReviewVideo} />}
        </View>;
      })}
      {data?.progress?.unassessed.map((gap, i) => <Text key={i} style={styles.error}>{label("unassessed")} · {label(gap.clock === "capture" ? "captureClock" : "mediaClock")} {progressTime(gap.start_secs)}–{progressTime(gap.end_secs)} · {label("unknown")}</Text>)}
      {data?.records.filter(r => r.status === "needs_confirmation" && !data.progress?.intervals.some(i => i.id === r.interval_id)).map(r => <Text key={`stale:${r.id}`} style={styles.error}>{label("needsConfirmation")} · {r.movement} · {label("recordSummary", { count: r.reps, load: loadLabel(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })}</Text>)}
      {benchmarkError && <><Text accessibilityRole="alert" style={styles.error}>{label("benchmarkFailed")}</Text><Pressable accessibilityRole="button" onPress={() => setData(d => d ? { ...d } : d)} style={styles.button}><Text style={styles.text}>{label("retry")}</Text></Pressable></>}
      {benchmarks && <BenchmarkHistory data={benchmarks} sessionId={sessionId} />}
    </>}
  </View>;
}
const styles = StyleSheet.create({
  card: { backgroundColor: "#1C1C1E", borderRadius: 12, padding: 12, marginVertical: 8, gap: 10 },
  editor: { padding: 10, borderWidth: 1, borderColor: "#555", borderRadius: 8, gap: 8 },
  confirmedEditor: { borderColor: "#2ea043", backgroundColor: "rgba(46, 160, 67, 0.12)" },
  restRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 6, paddingHorizontal: 10, borderWidth: 1, borderColor: "#3a3a3c", borderRadius: 8, backgroundColor: "rgba(255,255,255,0.03)", gap: 8 },
  restText: { color: "#999", fontSize: 11 },
  compactButton: { paddingVertical: 4, paddingHorizontal: 8, borderWidth: 1, borderColor: "#555", borderRadius: 6 },
  headerRow: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 6 },
  confirmedBadge: { color: "#4ade80", fontSize: 12, fontWeight: "600" },
  gap: { gap: 8 }, title: { color: "#f4f4f4", fontWeight: "600", fontSize: 14 }, text: { color: "#ddd", fontSize: 12 },
  row: { flexDirection: "row", flexWrap: "wrap", gap: 8 }, field: { gap: 4, minWidth: 95 },
  input: { padding: 10, borderWidth: 1, borderColor: "#777", borderRadius: 6, color: "#fff" },
  button: { padding: 10, borderWidth: 1, borderColor: "#666", borderRadius: 8 }, selected: { backgroundColor: "#3868b0" },
  error: { color: "#ffbe72" }, disabled: { opacity: 0.4 },
});
