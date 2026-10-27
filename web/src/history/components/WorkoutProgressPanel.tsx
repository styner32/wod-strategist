import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { historyApi } from "../../api/history";
import ko from "../../../../features/i18n/locales/ko.json";
import {
  activePerformanceRecords,
  buildUnassessedIntervals,
  confirmedPerformanceInput,
  describeUnassessedReason,
  diagnosePerformanceDraft,
  extractSuggestedMovements,
  extractWodLoads,
  findCoveringConfirmedRecord,
  getContiguousExerciseSpan,
  incompleteRepCount,
  inferDefaultLoadMode,
  inferLoadBasis,
  isLikelyBodyweightMovement,
  observedReps,
  performanceDraft,
  timelineProgressIntervals,
  performanceSelectionCovered,
  progressTime,
  rememberedMovementDefaults,
  summarizeWorkoutMovements,
  type MovementDraftDefaults,
  type PerformanceDraft,
  type PerformanceRecord,
  type ProgressLoad,
  type WorkoutProgress,
  type WorkoutProgressInterval,
} from "../../../../shared/workoutProgress";

const words = ko.workoutProgress;
function text(key: string, values: Record<string, unknown> = {}) {
  let value = (words as unknown as Record<string, unknown>)[key];
  if (key.startsWith("differences.")) value = (words.differences as Record<string, string>)[key.slice(12)];
  return String(value ?? words.unknown).replace(/%\{(\w+)\}/g, (_, name: string) => String(values[name] ?? words.unknown));
}
const button = "rounded-lg border border-border bg-bg-secondary/60 px-3 py-1.5 text-xs font-medium text-text-primary hover:bg-bg-tertiary disabled:opacity-40 transition-colors";
const fieldControlClass = "h-10 w-full rounded-lg border border-border bg-bg-base px-3 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-40";
const labelClass = "flex flex-col gap-1.5 text-xs font-medium text-text-secondary";
const loadText = (r: { load: PerformanceRecord["load"] }) => r.load ? `${r.load.value} ${r.load.unit} · ${text(r.load.basis)}` : words.none;

function RestCycleComparisons({ record }: { record: PerformanceRecord }) {
  if (record.status !== "confirmed") return null;
  const seconds = (value: number | null) => value == null || !Number.isFinite(value) ? words.unknown : text("restSeconds", { value: value.toFixed(2) });
  return <>{(record.rest_comparisons ?? []).map((rest, i) => <p key={i} className="text-sm">
    {words.restCycles} · {progressTime(rest.rest_start_secs)}–{progressTime(rest.rest_end_secs)} · {text("restCycleSummary", { duration: seconds(rest.rest_elapsed_seconds), before: seconds(rest.before_median_cycle_seconds), after: seconds(rest.after_median_cycle_seconds) })} · {text("restCycleChange", { value: rest.cycle_change_percent == null || !Number.isFinite(rest.cycle_change_percent) ? words.unknown : `${rest.cycle_change_percent >= 0 ? "+" : ""}${rest.cycle_change_percent.toFixed(1)}%` })}
  </p>)}</>;
}

function PerformanceEditor({
  interval,
  progress,
  record,
  wodLoads = [],
  suggestedMovements = [],
  initialDraft,
  movementDefaults,
  resolveMovementDefaults,
  onDraftChange,
  saving,
  onSave,
  close,
  onReviewVideo,
}: {
  interval: WorkoutProgressInterval;
  record?: PerformanceRecord;
  wodLoads?: ProgressLoad[];
  suggestedMovements?: string[];
  initialDraft?: PerformanceDraft;
  movementDefaults?: MovementDraftDefaults;
  resolveMovementDefaults?: (movement?: string | null) => MovementDraftDefaults | undefined;
  onDraftChange?: (draft: PerformanceDraft) => void;
  saving: boolean;
  progress: WorkoutProgress;
  onReviewVideo?: (start: number, end: number) => void;
  onSave: (draft: PerformanceDraft) => void;
  close: () => void;
}) {
  const defaultFallbackLoad = interval.load == null && wodLoads.length === 1 ? wodLoads[0] : null;
  const [draft, setDraft] = useState(() =>
    initialDraft ?? performanceDraft(interval, record, defaultFallbackLoad, movementDefaults),
  );
  const contiguousSpan = useMemo(() => getContiguousExerciseSpan(interval, progress), [interval, progress]);
  const updateDraft = (updater: (prev: PerformanceDraft) => PerformanceDraft) =>
    setDraft(d => {
      const next = updater(d);
      onDraftChange?.(next);
      return next;
    });
  const applyTargetMovementDefaults = (prev: PerformanceDraft, targetMovement?: string | null): Partial<PerformanceDraft> => {
    const targetDefaults = resolveMovementDefaults?.(targetMovement);
    const patch: Partial<PerformanceDraft> = {};
    if (targetDefaults) {
      if (prev.loadMode === "unresolved" && targetDefaults.loadMode) {
        patch.loadMode = targetDefaults.loadMode;
      }
      if (prev.weight.trim() === "" && targetDefaults.weight) {
        patch.loadMode = targetDefaults.loadMode ?? "weighted";
        patch.weight = targetDefaults.weight;
        if (targetDefaults.unit) patch.unit = targetDefaults.unit;
        if (targetDefaults.basis) patch.basis = targetDefaults.basis;
      }
      if (targetDefaults.technique && targetDefaults.technique !== "unspecified") {
        patch.technique = targetDefaults.technique;
      }
      return patch;
    }
    if (prev.loadMode === "unresolved" && targetMovement?.trim()) {
      const inferredMode = inferDefaultLoadMode(targetMovement);
      if (inferredMode !== "unresolved") {
        patch.loadMode = inferredMode;
        patch.basis = inferLoadBasis(targetMovement);
        if (inferredMode === "weighted" && prev.weight.trim() === "" && wodLoads.length === 1) {
          patch.weight = String(wodLoads[0].value);
          patch.unit = wodLoads[0].unit;
          patch.basis = wodLoads[0].basis;
        }
      }
    }
    return patch;
  };
  const update = <K extends keyof PerformanceDraft>(key: K, value: PerformanceDraft[K]) =>
    updateDraft(d => {
      const next = { ...d, [key]: value, reviewed: key === "reviewed" ? Boolean(value) : false };
      if (key === "loadMode" && value === "weighted" && d.weight.trim() === "" && wodLoads.length > 0) {
        next.weight = String(wodLoads[0].value);
        next.unit = wodLoads[0].unit;
        next.basis = wodLoads[0].basis;
      }
      return next;
    });
  const field = (key: "start" | "end" | "reps" | "weight", title: string) => (
    <label className={labelClass}>
      {text(title)}
      <div className="flex items-center gap-1.5">
        <input
          className={fieldControlClass}
          aria-label={text(title)}
          type="number"
          step={key === "reps" ? "1" : "0.1"}
          value={draft[key]}
          disabled={saving}
          onChange={e => update(key, e.target.value)}
        />
        {key === "reps" && (
          <>
            <button
              type="button"
              aria-label="반복수 1 감소"
              disabled={saving}
              onClick={() => {
                const cur = Number.parseInt(draft.reps || "0", 10);
                const nextReps = Number.isFinite(cur) ? Math.max(0, cur - 1) : 0;
                update("reps", String(nextReps));
              }}
              className="h-10 shrink-0 rounded-lg border border-border bg-bg-secondary/60 px-2.5 font-mono text-xs font-semibold text-text-primary hover:bg-bg-tertiary disabled:opacity-40"
            >
              -1
            </button>
            <button
              type="button"
              aria-label="반복수 1 증가"
              disabled={saving}
              onClick={() => {
                const cur = Number.parseInt(draft.reps || "0", 10);
                const nextReps = Number.isFinite(cur) ? Math.max(0, cur + 1) : 1;
                update("reps", String(nextReps));
              }}
              className="h-10 shrink-0 rounded-lg border border-accent/40 bg-accent/15 px-2.5 font-mono text-xs font-semibold text-accent hover:bg-accent/25 disabled:opacity-40"
            >
              +1
            </button>
          </>
        )}
      </div>
    </label>
  );
  const valid = confirmedPerformanceInput(interval, draft, progress) != null;
  const diagnostic = draft.reviewed && !valid ? (diagnosePerformanceDraft(interval, draft, progress) ?? words.invalid) : null;
  const start = Number(draft.start), end = Number(draft.end);
  const effectiveForCoverage: WorkoutProgressInterval =
    draft.movement !== undefined
      ? { ...interval, activity: "exercise", movement: draft.movement.trim() }
      : interval;
  const canReview =
    draft.start.trim() !== "" &&
    draft.end.trim() !== "" &&
    performanceSelectionCovered(effectiveForCoverage, start, end, draft.measurement, progress);
  return (
    <div className="mt-3 space-y-3.5 rounded-xl border border-border bg-bg-secondary/40 p-4">
      <p className="text-xs leading-relaxed text-text-secondary">{words.reviewHelp}</p>

      <div className="space-y-2">
        <label className={labelClass}>
          운동 종목
          <input
            type="text"
            aria-label="운동 종목"
            placeholder="예: Clean & Jerk, Power Clean"
            className={fieldControlClass}
            value={draft.movement ?? ""}
            disabled={saving}
            onChange={e =>
              updateDraft(d => ({
                ...d,
                ...applyTargetMovementDefaults(d, e.target.value),
                movement: e.target.value,
                reviewed: false,
              }))
            }
          />
        </label>
        {suggestedMovements.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 text-xs">
            <span className="text-text-muted">종목 빠른 선택:</span>
            {suggestedMovements.map(mov => {
              const active = (draft.movement ?? "").trim().toLowerCase() === mov.toLowerCase();
              return (
                <button
                  key={`suggest-mov-${mov}`}
                  type="button"
                  disabled={saving}
                  onClick={() =>
                    updateDraft(d => ({
                      ...d,
                      ...applyTargetMovementDefaults(d, mov),
                      movement: mov,
                      reviewed: false,
                    }))
                  }
                  className={`rounded-md border px-2 py-0.5 font-medium transition-colors disabled:opacity-40 ${
                    active
                      ? "border-accent bg-accent/20 text-accent"
                      : "border-border bg-bg-secondary/60 text-text-secondary hover:bg-bg-tertiary"
                  }`}
                >
                  {mov}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {(contiguousSpan.startSecs < interval.start_secs || contiguousSpan.endSecs > interval.end_secs) &&
        (Number(draft.start) > contiguousSpan.startSecs || Number(draft.end) < contiguousSpan.endSecs) && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-accent/30 bg-accent/10 px-3 py-2 text-xs">
          <span className="text-text-secondary">
            {contiguousSpan.movementOptions && contiguousSpan.movementOptions.length > 1
              ? `연속된 운동 구간이 있습니다 (${progressTime(contiguousSpan.startSecs)}–${progressTime(contiguousSpan.endSecs)} · ${contiguousSpan.movementOptions.map(o => o.movement).join(" + ")}). 합칠 기준 동작을 선택하세요:`
              : `10초 세그먼트 경계로 나뉜 연속 동일 운동 구간이 있습니다 (${progressTime(contiguousSpan.startSecs)}–${progressTime(contiguousSpan.endSecs)}).`}
          </span>
          <div className="flex flex-wrap items-center gap-1.5">
            {contiguousSpan.movementOptions && contiguousSpan.movementOptions.length > 1 ? (
              contiguousSpan.movementOptions.map(opt => (
                <button
                  key={`merge-opt-${opt.intervalId}`}
                  data-testid={`merge-as-${opt.intervalId}`}
                  type="button"
                  disabled={saving}
                  onClick={() =>
                    updateDraft(d => ({
                      ...d,
                      ...applyTargetMovementDefaults(d, opt.movement),
                      movement: opt.movement,
                      start: String(contiguousSpan.startSecs),
                      end: String(contiguousSpan.endSecs),
                      targetIntervalId: opt.intervalId,
                      reps:
                        d.reps === "" || d.reps === String(observedReps(interval) ?? "")
                          ? String(opt.observedReps ?? contiguousSpan.observedReps ?? d.reps)
                          : d.reps,
                      reviewed: false,
                    }))
                  }
                  className="rounded-md border border-accent/40 bg-accent/20 px-2.5 py-1 font-semibold text-accent hover:bg-accent/30 disabled:opacity-40"
                >
                  {`${opt.movement} 기준으로 합치기`}
                </button>
              ))
            ) : (
              <button
                type="button"
                disabled={saving}
                onClick={() =>
                  updateDraft(d => ({
                    ...d,
                    start: String(contiguousSpan.startSecs),
                    end: String(contiguousSpan.endSecs),
                    reps:
                      d.reps === "" || d.reps === String(observedReps(interval) ?? "")
                        ? String(contiguousSpan.observedReps ?? d.reps)
                        : d.reps,
                    reviewed: false,
                  }))
                }
                className="rounded-md border border-accent/40 bg-accent/20 px-2.5 py-1 font-semibold text-accent hover:bg-accent/30 disabled:opacity-40"
              >
                연속된 구간 합치기
              </button>
            )}
          </div>
        </div>
      )}

      {contiguousSpan.movementOptions &&
        contiguousSpan.movementOptions.length > 1 &&
        Number(draft.start) <= contiguousSpan.startSecs &&
        Number(draft.end) >= contiguousSpan.endSecs && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-accent/25 bg-accent/5 px-3 py-2 text-xs">
          <span className="font-medium text-text-secondary">합친 구간 기준 동작:</span>
          <div className="flex flex-wrap items-center gap-1.5">
            {contiguousSpan.movementOptions.map(opt => {
              const active = (draft.targetIntervalId ?? interval.id) === opt.intervalId;
              return (
                <button
                  key={`select-merged-mov-${opt.intervalId}`}
                  type="button"
                  disabled={saving}
                  onClick={() =>
                    updateDraft(d => ({
                      ...d,
                      ...applyTargetMovementDefaults(d, opt.movement),
                      movement: opt.movement,
                      targetIntervalId: opt.intervalId,
                      reps: opt.observedReps != null ? String(opt.observedReps) : d.reps,
                      reviewed: false,
                    }))
                  }
                  className={`rounded-md border px-2.5 py-1 font-semibold transition-colors disabled:opacity-40 ${
                    active
                      ? "border-accent bg-accent/25 text-accent"
                      : "border-border bg-bg-secondary/60 text-text-secondary hover:bg-bg-tertiary"
                  }`}
                >
                  {opt.movement}
                </button>
              );
            })}
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        {field("start", "start")}
        {field("end", "end")}
        {field("reps", "reps")}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border/70 bg-bg-elevated/60 px-3 py-2 text-xs">
        <span className="text-text-secondary">
          동작은 인식되었으나 랩 기준 미달·노렙·실패·준비 동작인 경우:
        </span>
        <button
          type="button"
          disabled={saving}
          onClick={() => {
            // Keep the selected load: a failed 135 lb attempt is not a bodyweight record.
            const zeroDraft: PerformanceDraft = { ...draft, reps: "0", reviewed: true };
            setDraft(zeroDraft);
            onDraftChange?.(zeroDraft);
            if (confirmedPerformanceInput(interval, zeroDraft, progress) != null) {
              onSave(zeroDraft);
            }
          }}
          className="rounded-md border border-warning/40 bg-warning/15 px-2.5 py-1 font-semibold text-warning hover:bg-warning/25 disabled:opacity-40 transition-colors"
        >
          0회(미완료/노렙)로 확정
        </button>
      </div>

      <div className="space-y-2">
        <label className={labelClass}>
          {words.loadMode}
          <select
            className={fieldControlClass}
            disabled={saving}
            value={draft.loadMode}
            onChange={e => update("loadMode", e.target.value as PerformanceDraft["loadMode"])}
          >
            {["unresolved", "none", "weighted"].map(v => (
              <option key={v} value={v}>{text(v)}</option>
            ))}
          </select>
        </label>

        {wodLoads.length > 0 && (
          <div className="flex flex-wrap items-center gap-2 rounded-lg border border-accent/25 bg-accent/5 px-3 py-2 text-xs">
            <span className="font-medium text-text-secondary">WOD 설명 중량 빠른 적용:</span>
            {wodLoads.map((wl, idx) => (
              <button
                key={`wod-load-${idx}-${wl.value}-${wl.unit}-${wl.basis}`}
                type="button"
                disabled={saving}
                onClick={() =>
                  updateDraft(d => ({
                    ...d,
                    loadMode: "weighted",
                    weight: String(wl.value),
                    unit: wl.unit,
                    basis: wl.basis,
                    reviewed: false,
                  }))
                }
                className="rounded-md border border-accent/40 bg-accent/15 px-2.5 py-1 font-mono text-xs font-semibold text-accent hover:bg-accent/25 disabled:opacity-40"
              >
                {`${wl.value} ${wl.unit} 적용`}
              </button>
            ))}
          </div>
        )}
      </div>

      {draft.loadMode === "weighted" && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          {field("weight", "weight")}
          <label className={labelClass}>
            {words.unit}
            <select
              className={fieldControlClass}
              disabled={saving}
              value={draft.unit}
              onChange={e => update("unit", e.target.value as PerformanceDraft["unit"])}
            >
              <option value="lb">lb</option>
              <option value="kg">kg</option>
            </select>
          </label>
          <label className={labelClass}>
            {words.basis}
            <select
              className={fieldControlClass}
              disabled={saving}
              value={draft.basis}
              onChange={e => update("basis", e.target.value as PerformanceDraft["basis"])}
            >
              <option value="total">{words.total}</option>
              <option value="per_implement">{words.per_implement}</option>
            </select>
          </label>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label className={labelClass}>
          {words.measurement}
          <select
            className={fieldControlClass}
            disabled={saving}
            value={draft.measurement}
            onChange={e => update("measurement", e.target.value as PerformanceDraft["measurement"])}
          >
            <option value="set">{words.set}</option>
            <option value="window">{words.window}</option>
          </select>
        </label>
        <label className={labelClass}>
          {words.technique}
          <select
            className={fieldControlClass}
            disabled={saving}
            value={draft.technique}
            onChange={e => update("technique", e.target.value as PerformanceDraft["technique"])}
          >
            {["singles", "touch_and_go"].map(v => (
              <option key={v} value={v}>{text(v)}</option>
            ))}
          </select>
        </label>
      </div>

      {onReviewVideo && (
        <div>
          <button className={button} disabled={!canReview} onClick={() => onReviewVideo(start, end)}>
            {words.selectedPlay}
          </button>
        </div>
      )}

      <label className="flex items-center gap-2 pt-1 text-sm text-text-primary cursor-pointer">
        <input
          type="checkbox"
          className="h-4 w-4 rounded border-border accent-accent"
          aria-label={words.reviewed}
          checked={draft.reviewed}
          disabled={saving}
          onChange={e => update("reviewed", e.target.checked)}
        />
        {words.reviewed}
      </label>

      {diagnostic && (
        <div role="alert" className="rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs font-medium text-warning">
          {diagnostic}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 pt-1">
        <button
          className="rounded-lg border border-accent/40 bg-accent/15 px-3.5 py-2 text-xs font-semibold text-accent hover:bg-accent/25 disabled:opacity-40 transition-colors"
          disabled={!valid || saving}
          onClick={() => onSave(draft)}
        >
          {saving ? words.saving : words.save}
        </button>
        <button className={button} disabled={saving} onClick={close}>
          {words.cancel}
        </button>
      </div>
    </div>
  );
}

export function WorkoutProgressPanel({ sessionId, profileId, isOriginal, seek, sourceUpdatedAt, wodDescription }: {
  sessionId: string; profileId: number; isOriginal: boolean; seek: (seconds: number) => void; sourceUpdatedAt?: string; wodDescription?: string;
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
  const draftsRef = useRef<Record<string, PerformanceDraft>>({});
  const movementDefaultsRef = useRef<Record<string, MovementDraftDefaults>>({});
  const queryKey = ["workout-progress", sessionId, profileId, sourceUpdatedAt];
  const progressQuery = useQuery({ queryKey, queryFn: () => historyApi.getWorkoutProgress(sessionId), retry: false });
  const data = progressQuery.data;
  const currentSource = useRef(data?.progress?.source_version); currentSource.current = data?.progress?.source_version;
  const benchmarks = useQuery({ queryKey: ["performance-benchmarks", profileId, movement], queryFn: () => historyApi.getPerformanceBenchmarks(profileId, movement), enabled: !!movement, retry: false });
  useEffect(() => { draftsRef.current = {}; movementDefaultsRef.current = {}; setSelected(undefined); setMovement(""); setSaving(false); setError(""); setNotice(""); }, [requestScope]);
  useEffect(() => { draftsRef.current = {}; setSelected(undefined); setMovement(""); }, [data?.progress?.source_version]);
  const save = async (interval: WorkoutProgressInterval, draft: PerformanceDraft) => {
    const record = confirmedPerformanceInput(interval, draft, data?.progress ?? undefined);
    if (!record || !data?.progress || saving) return;
    const submittedSource = data.progress.source_version;
    setSaving(true); setError(""); setNotice("");
    try {
      const result = await historyApi.confirmPerformanceRecords(sessionId, { source_version: data.progress.source_version, records: [record] });
      if (currentScope.current !== requestScope || currentSource.current !== submittedSource) return;
      const confirmedInterval = data.progress.intervals.find(row => row.id === record.interval_id) ?? interval;
      const confirmedMovement = record.movement ?? confirmedInterval.movement ?? interval.movement ?? "";
      delete draftsRef.current[interval.id];
      delete draftsRef.current[record.interval_id];
      for (const row of data.progress.intervals) {
        if (
          row.activity === "exercise" &&
          row.start_secs >= record.start_secs - 1e-6 &&
          row.end_secs <= record.end_secs + 1e-6
        ) {
          delete draftsRef.current[row.id];
        }
      }
      if (confirmedMovement.trim() && (record.reps > 0 || record.load != null)) {
        movementDefaultsRef.current[confirmedMovement.trim().toLowerCase()] = {
          loadMode: draft.loadMode,
          weight: draft.weight,
          unit: draft.unit,
          basis: draft.basis,
          technique: draft.technique,
        };
      }
      client.setQueryData(queryKey, result); setSelected(undefined); setNotice(words.saved); setMovement(confirmedMovement);
      await client.invalidateQueries({ queryKey: ["performance-benchmarks", profileId] });
    } catch (e) {
      if (currentScope.current !== requestScope) return;
      if ((e as { status?: number })?.status === 409) {
        draftsRef.current = {};
        setSelected(undefined); setMovement("");
        await progressQuery.refetch();
        if (currentScope.current === requestScope) setError(words.stale);
      } else {
        const detail =
          e instanceof Error &&
          e.message &&
          e.message !== "offline" &&
          !e.message.startsWith("Request failed:")
            ? ` (${e.message})`
            : "";
        setError(`${words.failed}${detail}`);
      }
    } finally { if (currentScope.current === requestScope) setSaving(false); }
  };

  const activeRecords = useMemo(
    () => activePerformanceRecords(data?.records ?? []),
    [data?.records],
  );
  const suggestedMovements = useMemo(
    () => extractSuggestedMovements(wodDescription, data?.progress, activeRecords),
    [wodDescription, data?.progress, activeRecords],
  );
  const unassessedIntervals = useMemo(
    () => buildUnassessedIntervals(data?.progress, activeRecords),
    [data?.progress, activeRecords],
  );
  const timelineItems = useMemo(() => {
    if (!data?.progress) return [];
    const items: (
      | { kind: "interval"; start_secs: number; interval: WorkoutProgressInterval }
      | { kind: "unassessed"; start_secs: number; gap: (typeof unassessedIntervals)[number] }
    )[] = [
      ...timelineProgressIntervals(data.progress, activeRecords).map(interval => ({
        kind: "interval" as const,
        start_secs: interval.start_secs,
        interval,
      })),
      ...unassessedIntervals.map(gap => ({
        kind: "unassessed" as const,
        start_secs: gap.start_secs,
        gap,
      })),
    ];
    return items.sort((a, b) => a.start_secs - b.start_secs);
  }, [data?.progress, unassessedIntervals, activeRecords]);
  const movementSummaries = useMemo(
    () => summarizeWorkoutMovements(data?.progress, activeRecords),
    [data?.progress, activeRecords],
  );
  const totalExerciseIntervals = useMemo(
    () => movementSummaries.reduce((acc, item) => acc + item.intervalCount, 0),
    [movementSummaries],
  );
  const totalObservedReps = useMemo(
    () => movementSummaries.reduce((acc, item) => acc + item.observedReps, 0),
    [movementSummaries],
  );
  const totalConfirmedReps = useMemo(
    () => movementSummaries.reduce((acc, item) => acc + item.confirmedReps, 0),
    [movementSummaries],
  );
  const hasConfirmedIntervals = useMemo(
    () => movementSummaries.some(item => item.confirmedCount > 0),
    [movementSummaries],
  );

  const records = benchmarks.data?.records ?? [];
  return (
    <section className="space-y-4 rounded-xl border border-border bg-bg-elevated p-4">
      <div className="space-y-1">
        <h3 className="font-semibold text-text-primary">{words.title}</h3>
        <p className="text-sm text-text-secondary">{words.boundary}</p>
      </div>

      {progressQuery.isLoading && <p>{words.loading}</p>}
      {(error || progressQuery.isError) && (
        <div role="alert" className="space-y-2 rounded-lg border border-error/30 bg-error/10 p-3 text-sm text-error">
          <p>{error || words.failed}</p>
          <button className={button} onClick={() => { setSelected(undefined); setError(""); void progressQuery.refetch(); }}>
            {words.refresh}
          </button>
        </div>
      )}
      {!!notice && <p role="status" className="text-sm text-success">{notice}</p>}
      {data && !data.progress && <p className="text-sm">{words.unavailable}</p>}

      {wodDescription?.trim() && (
        <div
          data-testid="workout-progress-wod-description"
          className="space-y-1.5 rounded-xl border border-border bg-bg-secondary/60 p-3.5"
        >
          <span className="text-xs font-semibold text-text-muted">WOD 설명</span>
          <p className="whitespace-pre-wrap text-sm text-text-primary">
            {wodDescription.trim()}
          </p>
        </div>
      )}

      {/* Total performed movements & reps summary */}
      {movementSummaries.length > 0 && (
        <div
          data-testid="workout-movement-summary"
          className="space-y-2.5 rounded-xl border border-border bg-bg-secondary/50 p-3.5"
        >
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-xs font-semibold text-text-primary">
              총 수행 운동 및 개수 요약
            </span>
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <span className="rounded-md bg-bg-tertiary px-2 py-0.5 font-medium text-text-secondary">
                {`총 ${movementSummaries.length}종목 · ${totalExerciseIntervals}구간`}
              </span>
              <span className="rounded-md bg-accent/15 px-2 py-0.5 font-mono font-semibold text-accent">
                {`관찰 완료 총 ${totalObservedReps}회`}
              </span>
              {(totalConfirmedReps > 0 || hasConfirmedIntervals) && (
                <span className="rounded-md bg-success/15 px-2 py-0.5 font-mono font-semibold text-success">
                  {`확정 총 ${totalConfirmedReps}회`}
                </span>
              )}
            </div>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {movementSummaries.map(item => {
              const fallbackLoads = extractWodLoads(wodDescription, item.movement);
              const loadHint =
                item.loadLabel ??
                (fallbackLoads.length > 0
                  ? fallbackLoads.map(l => `${l.value} ${l.unit}`).join(" / ")
                  : null);
              return (
                <div
                  key={`summary-${item.movement}`}
                  className="flex items-center justify-between gap-2 rounded-lg border border-border/70 bg-bg-elevated px-3 py-2 text-xs"
                >
                  <div className="min-w-0 space-y-0.5">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-semibold text-text-primary">{item.movement}</span>
                      <span className="rounded bg-bg-tertiary px-1.5 py-0.5 text-[11px] text-text-muted">
                        {`${item.intervalCount}구간`}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5 text-text-secondary">
                      <span>
                        {item.observedSeconds > 0 && item.observedReps === 0
                          ? `관찰 총 ${item.observedSeconds.toFixed(0)}초`
                          : `관찰 완료 ${item.observedReps}회`}
                      </span>
                      {item.confirmedCount > 0 && (
                        <span className="font-medium text-success">
                          {`· 확정 ${item.confirmedReps}회`}
                        </span>
                      )}
                      {loadHint && (
                        <span className="font-mono text-text-muted">
                          {`· ${loadHint}`}
                        </span>
                      )}
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => setMovement(item.movement)}
                    className="shrink-0 rounded-md border border-border bg-bg-secondary px-2 py-1 text-[11px] font-medium text-text-secondary hover:border-accent/40 hover:text-accent"
                  >
                    비교 이력 보기
                  </button>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {timelineItems.map(item => {
        if (item.kind === "unassessed") {
          const { gap } = item;
          const record = activeRecords.find(r => r.interval_id === gap.id);
          const nearbyMovement = (() => {
            if (!data?.progress?.intervals) return "";
            let best = "";
            let bestDist = Infinity;
            for (const row of data.progress.intervals) {
              if (row.activity !== "exercise" || !row.movement?.trim()) continue;
              const dist =
                row.end_secs <= gap.start_secs
                  ? gap.start_secs - row.end_secs
                  : row.start_secs >= gap.end_secs
                    ? row.start_secs - gap.end_secs
                    : 0;
              if (dist < bestDist) {
                bestDist = dist;
                best = row.movement.trim();
              }
            }
            return bestDist <= 30 ? best : "";
          })();
          const defaultMovement =
            record?.movement?.trim() ||
            gap.movement?.trim() ||
            nearbyMovement ||
            suggestedMovements[0] ||
            "";
          const syntheticInterval: WorkoutProgressInterval = {
            id: gap.id,
            start_secs: gap.start_secs,
            end_secs: gap.end_secs,
            activity: "exercise",
            movement: defaultMovement,
            load: null,
            observation_refs: [],
            rep_events: [],
          };
          const coveringConfirmed = findCoveringConfirmedRecord(syntheticInterval, activeRecords);
          const isConfirmed = coveringConfirmed != null;
          const isNeedsConfirmation = !isConfirmed && record?.status === "needs_confirmation";
          const effectiveMovement = coveringConfirmed?.movement?.trim() || record?.movement?.trim() || gap.movement?.trim() || "";
          const wodLoads = extractWodLoads(wodDescription, effectiveMovement || defaultMovement);
          const cardToneClass = isConfirmed
            ? "border-success/50 bg-success/10"
            : isNeedsConfirmation
              ? "border-warning/50 bg-warning/10"
              : "border-warning/35 bg-warning/5";
          return (
            <div
              key={gap.id}
              data-testid={`unassessed-interval-${gap.id}`}
              className={`space-y-2 rounded-xl border p-3.5 transition-colors ${cardToneClass}`}
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h4 className="font-medium text-text-primary">
                  {`${progressTime(gap.start_secs)}–${progressTime(gap.end_secs)} · ${
                    coveringConfirmed?.movement?.trim() ||
                    record?.movement?.trim() ||
                    (gap.movement ? `${gap.movement} (청크 감지 후보)` : words.unassessed)
                  }`}
                </h4>
                <div className="flex flex-wrap items-center gap-1.5">
                  <span
                    data-testid={`unassessed-reason-${gap.id}`}
                    className="rounded-md border border-warning/40 bg-warning/15 px-2 py-0.5 text-xs font-medium text-warning"
                  >
                    {describeUnassessedReason(gap.reason)}
                  </span>
                  {isConfirmed && coveringConfirmed && (
                    <span
                      data-testid={`confirmed-badge-${gap.id}`}
                      className="rounded-md border border-success/40 bg-success/15 px-2 py-0.5 text-xs font-semibold text-success"
                    >
                      {coveringConfirmed.reps === 0
                        ? `✓ ${words.confirmed} (0회 · 미완료/노렙)`
                        : `✓ ${words.confirmed}`}
                    </span>
                  )}
                </div>
              </div>
              {coveringConfirmed && (
                <div className="space-y-1">
                  <p className="text-sm font-medium text-success">
                    {words.confirmed} · {coveringConfirmed.movement} · {progressTime(coveringConfirmed.start_secs)}–{progressTime(coveringConfirmed.end_secs)} ·{" "}
                    {text("recordSummary", {
                      count: coveringConfirmed.reps === 0 ? "0 (미완료/노렙)" : coveringConfirmed.reps,
                      load: loadText(coveringConfirmed),
                      duration: (coveringConfirmed.elapsed_seconds ?? (coveringConfirmed.end_secs - coveringConfirmed.start_secs)).toFixed(1),
                    })}
                  </p>
                  <RestCycleComparisons record={coveringConfirmed} />
                </div>
              )}
              <div className="flex flex-wrap items-center gap-2 pt-0.5">
                <button
                  className={button}
                  disabled={!isOriginal}
                  title={isOriginal ? undefined : words.originalOnly}
                  onClick={() => seek(gap.start_secs)}
                >
                  {words.play}
                </button>
                <button
                  className={button}
                  disabled={saving}
                  onClick={() => {
                    setSelected(gap.id);
                    if (defaultMovement) setMovement(defaultMovement);
                    setNotice("");
                  }}
                >
                  {isConfirmed ? words.edit : "+ 운동 기록 추가"}
                </button>
                {effectiveMovement && (
                  <button className={button} onClick={() => setMovement(effectiveMovement)}>
                    {words.benchmarks}
                  </button>
                )}
              </div>
              {selected === gap.id && (
                <PerformanceEditor
                  key={`${data?.progress?.source_version}:${gap.id}`}
                  interval={syntheticInterval}
                  progress={data!.progress!}
                  record={coveringConfirmed ?? record}
                  wodLoads={wodLoads}
                  suggestedMovements={suggestedMovements}
                  initialDraft={draftsRef.current[gap.id]}
                  movementDefaults={rememberedMovementDefaults(defaultMovement, activeRecords, movementDefaultsRef.current)}
                  resolveMovementDefaults={mov => rememberedMovementDefaults(mov, activeRecords, movementDefaultsRef.current)}
                  onDraftChange={nextDraft => {
                    draftsRef.current[gap.id] = nextDraft;
                    const movKey = (nextDraft.movement ?? defaultMovement).trim().toLowerCase();
                    if (movKey) {
                      movementDefaultsRef.current[movKey] = {
                        loadMode: nextDraft.loadMode,
                        weight: nextDraft.weight,
                        unit: nextDraft.unit,
                        basis: nextDraft.basis,
                        technique: nextDraft.technique,
                      };
                    }
                  }}
                  saving={saving}
                  onSave={draft => void save(syntheticInterval, draft)}
                  close={() => setSelected(undefined)}
                  onReviewVideo={isOriginal ? seek : undefined}
                />
              )}
            </div>
          );
        }

        const { interval } = item;
        if (interval.activity === "rest") {
          const restDuration = Math.max(0, interval.end_secs - interval.start_secs);
          const hasRestObservations = Boolean(
            interval.rep_events?.length ||
              interval.form_observations?.length ||
              interval.rom_observations?.length ||
              interval.pace_observations?.length,
          );
          return (
            <div
              key={interval.id}
              data-testid={`rest-interval-${interval.id}`}
              className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border/40 bg-bg-secondary/10 px-3 py-1.5 text-xs text-text-muted"
            >
              <span className="text-text-secondary">
                {progressTime(interval.start_secs)}–{progressTime(interval.end_secs)} · {words.rest}
                {restDuration > 0 ? ` (${Math.round(restDuration)}초)` : ""}
              </span>
              <div className="flex flex-wrap items-center gap-2">
                <button
                  className="rounded border border-border/60 bg-bg-secondary/40 px-2 py-0.5 text-[11px] font-medium text-text-secondary hover:bg-bg-tertiary disabled:opacity-40 transition-colors"
                  disabled={!isOriginal}
                  title={isOriginal ? undefined : words.originalOnly}
                  onClick={() => seek(interval.start_secs)}
                >
                  {words.play}
                </button>
                {hasRestObservations && (
                  <details className="text-[11px]">
                    <summary className="cursor-pointer text-text-muted">{words.evidence}</summary>
                    <div className="mt-1 space-y-1">
                      {([["form", interval.form_observations], ["rom", interval.rom_observations], ["pace", interval.pace_observations]] as const).flatMap(([kind, observations]) =>
                        (observations ?? []).map((o, i) => (
                          <p key={`${kind}:${i}`}>{text(kind)} · {progressTime(o.start_secs)}–{progressTime(o.end_secs)} · {o.evidence}</p>
                        )),
                      )}
                    </div>
                  </details>
                )}
              </div>
            </div>
          );
        }

        const record = activeRecords.find(r => r.interval_id === interval.id);
        const coveringConfirmed = findCoveringConfirmedRecord(interval, activeRecords);
        const isConfirmed = coveringConfirmed != null;
        const isNeedsConfirmation = !isConfirmed && record?.status === "needs_confirmation";
        const contiguousSpan = getContiguousExerciseSpan(interval, data?.progress);
        const isMergedContiguous =
          coveringConfirmed != null &&
          coveringConfirmed.start_secs <= contiguousSpan.startSecs + 1e-6 &&
          coveringConfirmed.end_secs >= contiguousSpan.endSecs - 1e-6;
        const canMergeContiguous =
          !isMergedContiguous &&
          (contiguousSpan.startSecs < interval.start_secs || contiguousSpan.endSecs > interval.end_secs);
        const cardToneClass = isConfirmed
          ? "border-success/50 bg-success/10"
          : isNeedsConfirmation
            ? "border-warning/50 bg-warning/10"
            : "border-border bg-bg-secondary/20";
        const displayMovement =
          record?.status === "confirmed" && record.movement?.trim()
            ? record.movement.trim()
            : interval.movement || words.unknown;
        const wodLoads = extractWodLoads(wodDescription, displayMovement !== words.unknown ? displayMovement : suggestedMovements[0]);
        const effectiveLoadDisplay = interval.load
          ? `${loadText(interval)} · ${words.candidate}`
          : wodLoads.length > 0
            ? `${wodLoads.map(l => `${l.value} ${l.unit} · ${text(l.basis)}`).join(" / ")} · WOD 설명 중량 후보`
            : isLikelyBodyweightMovement(displayMovement)
              ? words.none
              : words.unknown;
        const incompleteReps = incompleteRepCount(interval);
        return (
          <div
            key={interval.id}
            data-testid={`exercise-interval-${interval.id}`}
            className={`space-y-2.5 rounded-xl border p-3.5 transition-colors ${cardToneClass}`}
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h4 className="font-medium text-text-primary">
                {progressTime(interval.start_secs)}–{progressTime(interval.end_secs)} · {displayMovement}
                {record?.status === "confirmed" &&
                  record.movement?.trim() &&
                  interval.movement?.trim() &&
                  record.movement.trim() !== interval.movement.trim() && (
                    <span className="ml-1.5 text-xs font-normal text-text-muted">
                      {`(원본 감지: ${interval.movement.trim()})`}
                    </span>
                  )}
              </h4>
              <div className="flex flex-wrap items-center gap-1.5">
                {canMergeContiguous && (
                  <span
                    data-testid={`contiguous-badge-${interval.id}`}
                    className="rounded-md border border-accent/40 bg-accent/15 px-2 py-0.5 text-xs font-medium text-accent"
                  >
                    {`연속 구간 (${progressTime(contiguousSpan.startSecs)}–${progressTime(contiguousSpan.endSecs)}) 병합 가능`}
                  </span>
                )}
                {isConfirmed && coveringConfirmed && (
                  <span
                    data-testid={`confirmed-badge-${interval.id}`}
                    className="rounded-md border border-success/40 bg-success/15 px-2 py-0.5 text-xs font-semibold text-success"
                  >
                    {record?.status === "confirmed"
                      ? record.reps === 0
                        ? `✓ ${words.confirmed} (0회 · 미완료/노렙)`
                        : `✓ ${words.confirmed}`
                      : `✓ ${words.confirmed} (${progressTime(coveringConfirmed.start_secs)}–${progressTime(coveringConfirmed.end_secs)}${coveringConfirmed.movement && coveringConfirmed.movement !== interval.movement ? ` · ${coveringConfirmed.movement}` : ""})`}
                  </span>
                )}
                {isNeedsConfirmation && (
                  <span className="rounded-md border border-warning/40 bg-warning/15 px-2 py-0.5 text-xs font-semibold text-warning">
                    {words.needsConfirmation}
                  </span>
                )}
              </div>
            </div>
            {interval.activity === "exercise" && (
              <div className="space-y-1">
                <p className="text-sm text-text-secondary">{text("observedReps", { count: observedReps(interval) ?? words.unknown })}</p>
                {observedReps(interval) == null && incompleteReps > 0 && (
                  <p className="text-xs font-medium text-warning">
                    {`랩 기준 미달(불완전 반복) ${incompleteReps}회 감지 · 완료 반복 0회`}
                  </p>
                )}
                <p className="text-sm text-text-secondary">{effectiveLoadDisplay}</p>
                {record && (
                  <>
                    <p className={`text-sm font-medium ${record.status === "confirmed" ? "text-success" : "text-warning"}`}>
                      {record.status === "confirmed" ? words.confirmed : words.needsConfirmation} · {progressTime(record.start_secs)}–{progressTime(record.end_secs)} · {text("recordSummary", { count: record.reps === 0 ? "0 (미완료/노렙)" : record.reps, load: loadText(record), duration: (record.elapsed_seconds ?? (record.end_secs - record.start_secs)).toFixed(1) })}
                    </p>
                    <RestCycleComparisons record={record} />
                  </>
                )}
                {!record && coveringConfirmed && (
                  <p className="text-xs font-medium text-success">
                    {`${progressTime(coveringConfirmed.start_secs)}–${progressTime(coveringConfirmed.end_secs)}${coveringConfirmed.movement && coveringConfirmed.movement !== interval.movement ? ` · ${coveringConfirmed.movement}` : ""} 확정 기록(${coveringConfirmed.reps === 0 ? "0회 · 미완료/노렙" : `${coveringConfirmed.reps}회`})에 포함된 구간입니다.`}
                  </p>
                )}
              </div>
            )}
            <div className="flex flex-wrap items-center gap-2 pt-0.5">
              <button className={button} disabled={!isOriginal} title={isOriginal ? undefined : words.originalOnly} onClick={() => seek(interval.start_secs)}>
                {words.play}
              </button>
              {interval.activity === "exercise" && interval.movement ? (
                <button className={button} disabled={saving} onClick={() => { setSelected(interval.id); setMovement(displayMovement); setNotice(""); }}>
                  {words.edit}
                </button>
              ) : (
                <button className={button} disabled={saving} onClick={() => { setSelected(interval.id); if (suggestedMovements[0]) setMovement(suggestedMovements[0]); setNotice(""); }}>
                  {isConfirmed ? words.edit : "+ 운동 기록 추가"}
                </button>
              )}
              {interval.activity === "exercise" && interval.movement && (
                <button className={button} onClick={() => setMovement(displayMovement)}>
                  {words.benchmarks}
                </button>
              )}
            </div>
            <details className="rounded-lg border border-border/60 bg-bg-secondary/30 px-3 py-2 text-sm">
              <summary className="cursor-pointer font-medium text-text-secondary">{words.evidence}</summary>
              <div className="mt-2 space-y-1.5">
                {(interval.rep_events ?? []).map((event, i) => (
                  <div key={`rep:${i}`} className="flex flex-wrap items-center justify-between gap-2">
                    <p>{progressTime(event.start_secs)}–{progressTime(event.end_secs)} · {event.complete ? words.completeCycle : words.incompleteCycle} · {event.evidence}</p>
                    <button type="button" className="rounded border border-accent/30 bg-accent/10 px-2 py-0.5 font-mono text-xs text-accent hover:bg-accent/20 disabled:opacity-40" disabled={!isOriginal} onClick={() => seek(event.start_secs)}>
                      ▶ {progressTime(event.start_secs)}
                    </button>
                  </div>
                ))}
                {([["form", interval.form_observations], ["rom", interval.rom_observations], ["pace", interval.pace_observations]] as const).flatMap(([kind, observations]) =>
                  (observations ?? []).map((o, i) => (
                    <div key={`${kind}:${i}`} className="flex flex-wrap items-center justify-between gap-2">
                      <p>{text(kind)} · {progressTime(o.start_secs)}–{progressTime(o.end_secs)} · {o.evidence}</p>
                      <button type="button" className="rounded border border-accent/30 bg-accent/10 px-2 py-0.5 font-mono text-xs text-accent hover:bg-accent/20 disabled:opacity-40" disabled={!isOriginal} onClick={() => seek(o.start_secs)}>
                        ▶ {progressTime(o.start_secs)}
                      </button>
                    </div>
                  )),
                )}
              </div>
            </details>
            {selected === interval.id && (
              <PerformanceEditor
                key={`${data?.progress?.source_version}:${interval.id}`}
                interval={interval}
                progress={data!.progress!}
                record={coveringConfirmed ?? record}
                wodLoads={wodLoads}
                suggestedMovements={suggestedMovements}
                initialDraft={draftsRef.current[interval.id]}
                movementDefaults={rememberedMovementDefaults(displayMovement !== words.unknown ? displayMovement : suggestedMovements[0], activeRecords, movementDefaultsRef.current)}
                resolveMovementDefaults={mov => rememberedMovementDefaults(mov, activeRecords, movementDefaultsRef.current)}
                onDraftChange={nextDraft => {
                  draftsRef.current[interval.id] = nextDraft;
                  const movKey = (nextDraft.movement || interval.movement || "").trim().toLowerCase();
                  if (movKey) {
                    movementDefaultsRef.current[movKey] = {
                      loadMode: nextDraft.loadMode,
                      weight: nextDraft.weight,
                      unit: nextDraft.unit,
                      basis: nextDraft.basis,
                      technique: nextDraft.technique,
                    };
                  }
                }}
                saving={saving}
                onSave={draft => void save(interval, draft)}
                close={() => setSelected(undefined)}
                onReviewVideo={isOriginal ? seek : undefined}
              />
            )}
          </div>
        );
      })}
      {data?.progress?.unassessed.filter(gap => gap.clock === "capture").map((gap, i) => <p key={i} className="text-sm text-warning">{words.unassessed} · {words.captureClock} {progressTime(gap.start_secs)}–{progressTime(gap.end_secs)} · {words.unknown}</p>)}
      {activeRecords.filter(r => r.status === "needs_confirmation" && !data?.progress?.intervals.some(i => i.id === r.interval_id)).map(r => <p key={`stale:${r.id}`} className="text-sm text-warning">{words.needsConfirmation} · {r.movement} · {text("recordSummary", { count: r.reps, load: loadText(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })}</p>)}
      {benchmarks.isError && <div role="alert"><p>{words.benchmarkFailed}</p><button className={button} onClick={() => void benchmarks.refetch()}>{words.retry}</button></div>}
      {!!movement && <div className="space-y-2 pt-2 border-t border-border"><h4 className="font-medium text-text-primary">{words.history} · {movement}</h4>{benchmarks.isLoading && <p>{words.loading}</p>}{benchmarks.data && !records.length && <p className="text-sm text-text-secondary">{words.noRecords}</p>}
        {records.map(r => {
          const comparison = benchmarks.data?.comparisons.find(c => c.current_record_id === r.id);
          const baseline = records.find(b => b.id === comparison?.baseline_record_id);
          const heavierBaseline = records.find(b => b.id === comparison?.heavier_load_baseline_record_id);
          return <div key={r.id} className="space-y-1 rounded-lg border border-border bg-bg-secondary/30 p-3 text-sm">
            <p className="font-medium text-text-primary">{r.movement} · {r.performed_at ? new Date(r.performed_at).toLocaleDateString() : words.unknown}</p>
            <p>{text("recordSummary", { count: r.reps, load: loadText(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })}</p>
            <p className="text-text-secondary">{text(r.measurement)} · {text(r.technique)} · {r.workout_prescription || words.unknown}</p>
            <p>{text("rate", { value: Number.isFinite(r.reps_per_minute) ? r.reps_per_minute.toFixed(1) : words.unknown })} · {text(r.rate_basis)}</p>
            <p>{text("cycle", { value: r.median_cycle_seconds == null ? words.unknown : r.median_cycle_seconds.toFixed(2) })}</p>
            <RestCycleComparisons record={r} />
            {comparison && r.session_id === sessionId && <><p>{text(comparison.kind)}{baseline ? ` · ${baseline.reps} · ${loadText(baseline)}` : ""}</p>{comparison.condition_differences.length > 0 && <p>{words.difference}: {comparison.condition_differences.map(d => text(`differences.${d}`)).join(", ")}</p>}{comparison.rate_change_percent != null && <p>{text("change", { value: `${comparison.rate_change_percent >= 0 ? "+" : ""}${comparison.rate_change_percent.toFixed(1)}` })}</p>}{comparison.heavier_load_kg != null && comparison.heavier_load_kg > 0 && <p>{text("heavier", { value: comparison.heavier_load_kg.toFixed(1) })}{heavierBaseline ? ` · ${heavierBaseline.reps} · ${loadText(heavierBaseline)}` : ""}</p>}</>}
          </div>;
        })}
        {(benchmarks.data?.best ?? []).map(r => <p key={r.id} className="text-sm">{words.best}: {text("recordSummary", { count: r.reps, load: loadText(r), duration: (r.elapsed_seconds ?? (r.end_secs - r.start_secs)).toFixed(1) })} · {text("rate", { value: r.reps_per_minute.toFixed(1) })} · {text(r.measurement)} · {text(r.technique)} · {r.workout_prescription || words.unknown}</p>)}
        <p className="text-xs text-text-secondary">{words.scope}</p>
      </div>}
    </section>
  );
}
