import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { historyApi } from "../../api/history";
import ko from "../../../../features/i18n/locales/ko.json";
import {
  confirmedPerformanceInput, observedReps, performanceDraft, performanceSelectionCovered, progressTime,
  type PerformanceDraft, type PerformanceRecord, type WorkoutProgress, type WorkoutProgressInterval,
} from "../../../../shared/workoutProgress";

const words = ko.workoutProgress;
function text(key: string, values: Record<string, unknown> = {}) {
  let value = (words as unknown as Record<string, unknown>)[key];
  if (key.startsWith("differences.")) value = (words.differences as Record<string, string>)[key.slice(12)];
  return String(value ?? words.unknown).replace(/%\{(\w+)\}/g, (_, name: string) => String(values[name] ?? words.unknown));
}
const button = "rounded border border-border px-3 py-2 text-sm disabled:opacity-40";
const input = "w-28 rounded border border-border bg-bg-base p-2";
const loadText = (r: { load: PerformanceRecord["load"] }) => r.load ? `${r.load.value} ${r.load.unit} · ${text(r.load.basis)}` : words.none;

function RestCycleComparisons({ record }: { record: PerformanceRecord }) {
  if (record.status !== "confirmed") return null;
  const seconds = (value: number | null) => value == null || !Number.isFinite(value) ? words.unknown : text("restSeconds", { value: value.toFixed(2) });
  return <>{(record.rest_comparisons ?? []).map((rest, i) => <p key={i} className="text-sm">
    {words.restCycles} · {progressTime(rest.rest_start_secs)}–{progressTime(rest.rest_end_secs)} · {text("restCycleSummary", { duration: seconds(rest.rest_elapsed_seconds), before: seconds(rest.before_median_cycle_seconds), after: seconds(rest.after_median_cycle_seconds) })} · {text("restCycleChange", { value: rest.cycle_change_percent == null || !Number.isFinite(rest.cycle_change_percent) ? words.unknown : `${rest.cycle_change_percent >= 0 ? "+" : ""}${rest.cycle_change_percent.toFixed(1)}%` })}
  </p>)}</>;
}

function PerformanceEditor({ interval, progress, record, saving, onSave, close, onReviewVideo }: {
  interval: WorkoutProgressInterval; record?: PerformanceRecord; saving: boolean;
  progress: WorkoutProgress; onReviewVideo?: (start: number, end: number) => void;
  onSave: (draft: PerformanceDraft) => void; close: () => void;
}) {
  const [draft, setDraft] = useState(() => performanceDraft(interval, record));
  const update = <K extends keyof PerformanceDraft>(key: K, value: PerformanceDraft[K]) => setDraft(d => ({ ...d, [key]: value, reviewed: key === "reviewed" ? Boolean(value) : false }));
  const field = (key: "start" | "end" | "reps" | "weight", title: string) => <label className="flex flex-col gap-1 text-sm">{text(title)}<input className={input} aria-label={text(title)} type="number" step={key === "reps" ? "1" : "0.1"} value={draft[key]} disabled={saving} onChange={e => update(key, e.target.value)} /></label>;
  const valid = confirmedPerformanceInput(interval, draft, progress) != null;
  const start = Number(draft.start), end = Number(draft.end);
  const canReview = draft.start.trim() !== "" && draft.end.trim() !== "" && performanceSelectionCovered(interval, start, end, draft.measurement, progress);
  return <div className="space-y-3 rounded border border-border p-3">
    <p className="text-sm text-text-secondary">{words.reviewHelp}</p>
    <div className="flex flex-wrap gap-3">{field("start", "start")}{field("end", "end")}{field("reps", "reps")}</div>
    <label className="flex flex-col gap-1 text-sm">{words.loadMode}<select className={input + " w-auto"} disabled={saving} value={draft.loadMode} onChange={e => update("loadMode", e.target.value as PerformanceDraft["loadMode"])}>{["unresolved", "none", "weighted"].map(v => <option key={v} value={v}>{text(v)}</option>)}</select></label>
    {draft.loadMode === "weighted" && <div className="flex flex-wrap gap-3">{field("weight", "weight")}<label>{words.unit}<select className={input} disabled={saving} value={draft.unit} onChange={e => update("unit", e.target.value as PerformanceDraft["unit"])}><option>lb</option><option>kg</option></select></label><label>{words.basis}<select className={input + " w-auto"} disabled={saving} value={draft.basis} onChange={e => update("basis", e.target.value as PerformanceDraft["basis"])}><option value="total">{words.total}</option><option value="per_implement">{words.per_implement}</option></select></label></div>}
    <div className="flex flex-wrap gap-3"><label>{words.measurement}<select className={input + " w-auto"} disabled={saving} value={draft.measurement} onChange={e => update("measurement", e.target.value as PerformanceDraft["measurement"])}><option value="set">{words.set}</option><option value="window">{words.window}</option></select></label><label>{words.technique}<select className={input + " w-auto"} disabled={saving} value={draft.technique} onChange={e => update("technique", e.target.value as PerformanceDraft["technique"])}>{["unspecified", "singles", "touch_and_go"].map(v => <option key={v} value={v}>{text(v)}</option>)}</select></label></div>
    {onReviewVideo && <button className={button} disabled={!canReview} onClick={() => onReviewVideo(start, end)}>{words.selectedPlay}</button>}
    <label className="flex items-center gap-2 text-sm"><input type="checkbox" aria-label={words.reviewed} checked={draft.reviewed} disabled={saving} onChange={e => update("reviewed", e.target.checked)} />{words.reviewed}</label>
    {draft.reviewed && !valid && <p role="alert" className="text-sm text-warning">{words.invalid}</p>}
    <div className="flex gap-2"><button className={button} disabled={!valid || saving} onClick={() => onSave(draft)}>{saving ? words.saving : words.save}</button><button className={button} disabled={saving} onClick={close}>{words.cancel}</button></div>
  </div>;
}

export function WorkoutProgressPanel({ sessionId, profileId, isOriginal, seek, sourceUpdatedAt }: {
  sessionId: string; profileId: number; isOriginal: boolean; seek: (seconds: number) => void; sourceUpdatedAt?: string;
}) {
  const client = useQueryClient();
  const scope = `${profileId}:${sessionId}`;
  const requestScope = `${scope}:${sourceUpdatedAt ?? ""}`;
  const currentScope = useRef(requestScope); currentScope.current = requestScope;
  const [selected, setSelected] = useState<string>();
  const [movement, setMovement] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const queryKey = ["workout-progress", sessionId, profileId, sourceUpdatedAt];
  const progressQuery = useQuery({ queryKey, queryFn: () => historyApi.getWorkoutProgress(sessionId), retry: false });
  const data = progressQuery.data;
  const currentSource = useRef(data?.progress?.source_version); currentSource.current = data?.progress?.source_version;
  const benchmarks = useQuery({ queryKey: ["performance-benchmarks", profileId, movement], queryFn: () => historyApi.getPerformanceBenchmarks(profileId, movement), enabled: !!movement, retry: false });
  useEffect(() => { setSelected(undefined); setMovement(""); setSaving(false); setError(""); setNotice(""); }, [requestScope]);
  useEffect(() => { setSelected(undefined); setMovement(""); }, [data?.progress?.source_version]);
  const save = async (interval: WorkoutProgressInterval, draft: PerformanceDraft) => {
    const record = confirmedPerformanceInput(interval, draft, data?.progress ?? undefined);
    if (!record || !data?.progress || saving) return;
    const submittedSource = data.progress.source_version;
    setSaving(true); setError(""); setNotice("");
    try {
      const result = await historyApi.confirmPerformanceRecords(sessionId, { source_version: data.progress.source_version, records: [record] });
      if (currentScope.current !== requestScope || currentSource.current !== submittedSource) return;
      client.setQueryData(queryKey, result); setSelected(undefined); setNotice(words.saved); setMovement(interval.movement ?? "");
      await client.invalidateQueries({ queryKey: ["performance-benchmarks", profileId, interval.movement] });
    } catch (e) {
      if (currentScope.current !== requestScope) return;
      if ((e as { status?: number })?.status === 409) {
        setSelected(undefined); setMovement("");
        await progressQuery.refetch();
        if (currentScope.current === requestScope) setError(words.stale);
      } else setError(words.failed);
    } finally { if (currentScope.current === requestScope) setSaving(false); }
  };
  const records = benchmarks.data?.records ?? [];
  return <section className="space-y-3 rounded-xl border border-border bg-bg-elevated p-4">
    <h3 className="font-semibold">{words.title}</h3><p className="text-sm text-text-secondary">{words.boundary}</p>
    {progressQuery.isLoading && <p>{words.loading}</p>}
    {(error || progressQuery.isError) && <div role="alert"><p>{error || words.failed}</p><button className={button} onClick={() => { setSelected(undefined); setError(""); void progressQuery.refetch(); }}>{words.refresh}</button></div>}
    {!!notice && <p role="status">{notice}</p>}
    {data && !data.progress && <p className="text-sm">{words.unavailable}</p>}
    {data?.progress?.intervals.map(interval => {
      const record = data.records.find(r => r.interval_id === interval.id);
      return <div key={interval.id} className="space-y-2 rounded border border-border p-3">
        <h4 className="font-medium">{progressTime(interval.start_secs)}–{progressTime(interval.end_secs)} · {interval.movement || (interval.activity === "rest" ? words.rest : words.unknown)}</h4>
        {interval.activity === "exercise" && <><p className="text-sm">{text("observedReps", { count: observedReps(interval) ?? words.unknown })}</p><p className="text-sm">{interval.load ? `${loadText(interval)} · ${words.candidate}` : words.unknown}</p>{record && <><p className="text-sm">{record.status === "confirmed" ? words.confirmed : words.needsConfirmation} · {progressTime(record.start_secs)}–{progressTime(record.end_secs)} · {text("recordSummary", { count: record.reps, load: loadText(record), duration: (record.elapsed_seconds ?? (record.end_secs - record.start_secs)).toFixed(1) })}</p><RestCycleComparisons record={record} /></>}</>}
        <div className="flex flex-wrap gap-2"><button className={button} disabled={!isOriginal} title={isOriginal ? undefined : words.originalOnly} onClick={() => seek(interval.start_secs)}>{words.play}</button>{interval.activity === "exercise" && interval.movement && <button className={button} disabled={saving} onClick={() => { setSelected(interval.id); setMovement(interval.movement!); setNotice(""); }}>{words.edit}</button>}</div>
        <details className="text-sm"><summary className="cursor-pointer">{words.evidence}</summary><div className="mt-2 space-y-1">{(interval.rep_events ?? []).map((event, i) => <p key={`rep:${i}`}>{progressTime(event.start_secs)}–{progressTime(event.end_secs)} · {event.complete ? words.completeCycle : words.incompleteCycle} · {event.evidence}</p>)}{([["form", interval.form_observations], ["rom", interval.rom_observations], ["pace", interval.pace_observations]] as const).flatMap(([kind, observations]) => (observations ?? []).map((o, i) => <p key={`${kind}:${i}`}>{text(kind)} · {progressTime(o.start_secs)}–{progressTime(o.end_secs)} · {o.evidence}</p>))}</div></details>
        {interval.activity === "exercise" && interval.movement && <button className={button} onClick={() => setMovement(interval.movement!)}>{words.benchmarks}</button>}
        {selected === interval.id && <PerformanceEditor key={`${data.progress?.source_version}:${interval.id}`} interval={interval} progress={data.progress!} record={record} saving={saving} onSave={draft => void save(interval, draft)} close={() => setSelected(undefined)} onReviewVideo={isOriginal ? seek : undefined} />}
      </div>;
    })}
    {data?.progress?.unassessed.map((gap, i) => <p key={i} className="text-sm text-warning">{words.unassessed} · {gap.clock === "capture" ? words.captureClock : words.mediaClock} {progressTime(gap.start_secs)}–{progressTime(gap.end_secs)} · {words.unknown}</p>)}
    {data?.records.filter(r => r.status === "needs_confirmation" && !data.progress?.intervals.some(i => i.id === r.interval_id)).map(r => <p key={`stale:${r.id}`} className="text-sm text-warning">{words.needsConfirmation} · {r.movement} · {text("recordSummary", { count: r.reps, load: loadText(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })}</p>)}
    {benchmarks.isError && <div role="alert"><p>{words.benchmarkFailed}</p><button className={button} onClick={() => void benchmarks.refetch()}>{words.retry}</button></div>}
    {!!movement && <div className="space-y-2"><h4 className="font-medium">{words.history} · {movement}</h4>{benchmarks.isLoading && <p>{words.loading}</p>}{benchmarks.data && !records.length && <p>{words.noRecords}</p>}
      {records.map(r => {
        const comparison = benchmarks.data?.comparisons.find(c => c.current_record_id === r.id);
        const baseline = records.find(b => b.id === comparison?.baseline_record_id);
      const heavierBaseline = records.find(b => b.id === comparison?.heavier_load_baseline_record_id);
        return <div key={r.id} className="space-y-1 rounded border border-border p-2 text-sm">
          <p>{r.movement} · {r.performed_at ? new Date(r.performed_at).toLocaleDateString() : words.unknown}</p>
          <p>{text("recordSummary", { count: r.reps, load: loadText(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })}</p>
          <p>{text(r.measurement)} · {text(r.technique)} · {r.workout_prescription || words.unknown}</p>
          <p>{text("rate", { value: Number.isFinite(r.reps_per_minute) ? r.reps_per_minute.toFixed(1) : words.unknown })} · {text(r.rate_basis)}</p>
          <p>{text("cycle", { value: r.median_cycle_seconds == null ? words.unknown : r.median_cycle_seconds.toFixed(2) })}</p>
          <RestCycleComparisons record={r} />
          {comparison && r.session_id === sessionId && <><p>{text(comparison.kind)}{baseline ? ` · ${baseline.reps} · ${loadText(baseline)}` : ""}</p>{comparison.condition_differences.length > 0 && <p>{words.difference}: {comparison.condition_differences.map(d => text(`differences.${d}`)).join(", ")}</p>}{comparison.rate_change_percent != null && <p>{text("change", { value: `${comparison.rate_change_percent >= 0 ? "+" : ""}${comparison.rate_change_percent.toFixed(1)}` })}</p>}{comparison.heavier_load_kg != null && comparison.heavier_load_kg > 0 && <p>{text("heavier", { value: comparison.heavier_load_kg.toFixed(1) })}{heavierBaseline ? ` · ${heavierBaseline.reps} · ${loadText(heavierBaseline)}` : ""}</p>}</>}
        </div>;
      })}
      {(benchmarks.data?.best ?? []).map(r => <p key={r.id} className="text-sm">{words.best}: {text("recordSummary", { count: r.reps, load: loadText(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })} · {text("rate", { value: r.reps_per_minute.toFixed(1) })} · {text(r.measurement)} · {text(r.technique)} · {r.workout_prescription || words.unknown}</p>)}
      <p className="text-xs text-text-secondary">{words.scope}</p>
    </div>}
  </section>;
}
