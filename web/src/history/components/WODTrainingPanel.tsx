import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { historyApi } from "../../api/history";
import { newWODScore, wodSeconds } from "../../../../shared/wodTraining";
import { historyPollInterval, historyPollingStopped, resetHistoryPolling } from "../historyPolling";

export function WODTrainingPanel({ sessionId, profileId, scoreJSON, currentTime, isOriginal, seek }: {
  sessionId: string; profileId: number; scoreJSON?: string; currentTime: number; isOriginal: boolean; seek: (seconds: number) => void;
}) {
  const client = useQueryClient();
  const key = `wod-start-run:${profileId}:${sessionId}`;
  const [runId, setRunId] = useState<number | undefined>(() => { const n = Number(localStorage.getItem(key)); return n > 0 ? n : undefined; });
  const [startInput, setStartInput] = useState("");
  const [effort, setEffort] = useState<number>();
  const eligible = !!newWODScore(scoreJSON);
  const training = useQuery({ queryKey: ["wod-training", sessionId, profileId], queryFn: () => historyApi.getWODTraining(sessionId), enabled: eligible });
  const loads = useQuery({ queryKey: ["wod-training-load", profileId], queryFn: () => historyApi.getWODTrainingLoad(profileId), enabled: eligible });
  const run = useQuery({ queryKey: ["session-reanalysis", sessionId, runId], queryFn: () => historyApi.getSessionReanalysis(sessionId, runId!), enabled: eligible && runId != null, retry: false,
    refetchInterval: q => historyPollInterval(q, q.state.data?.status === "QUEUED" || q.state.data?.status === "RUNNING" ? String(runId) : undefined) });
  const stopped = historyPollingStopped(client, ["session-reanalysis", sessionId, runId], run.dataUpdatedAt);
  const refresh = async () => { await Promise.all(["wod-training", "wod-training-load", "session-analysis", "analysis", "analysis-enrichment", "session-reanalyses"].map(k => client.invalidateQueries({ queryKey: [k] }))); };
  const save = useMutation({ mutationFn: (value: number) => historyApi.updateWODEffort(sessionId, value), onSuccess: async () => { setEffort(undefined); await refresh(); } });
  const create = useMutation({ mutationFn: (seconds: number | null) => historyApi.createSessionReanalysis(sessionId, crypto.randomUUID(), undefined, undefined, undefined, seconds), onSuccess: r => { localStorage.setItem(key, String(r.run_id)); resetHistoryPolling(client, ["session-reanalysis", sessionId]); setRunId(r.run_id); } });
  const apply = useMutation({ mutationFn: (id: number) => historyApi.applySessionReanalysis(sessionId, id), onSuccess: async () => { localStorage.removeItem(key); setRunId(undefined); await refresh(); } });
  useEffect(() => { if (runId != null && run.data?.status === "COMPLETED" && !apply.isPending && !apply.isError) apply.mutate(runId); }, [runId, run.data?.status, apply.isPending, apply.isError, apply.mutate]);
  if (!eligible) return null;
  const data = training.data;
  const start = data?.wod_start;
  const comparison = loads.data?.comparison;
  const waiting = runId != null && (!run.data || run.data.status === "RUNNING" || run.data.status === "QUEUED" || run.data.status === "COMPLETED");
  const error = training.error || loads.error || save.error || create.error || apply.error || run.error;
  const button = "rounded border border-border px-3 py-2 text-sm disabled:opacity-40";
  return <section className="space-y-3 rounded-xl border border-border bg-bg-elevated p-4">
    <h3 className="font-semibold">WOD 훈련 부하</h3>
    <p>본 WOD 시작: {wodSeconds(start?.seconds)} · {start?.source === "manual" ? "직접 지정" : "자동 추정"}{start?.status !== "confirmed" ? " · 시작 확인 필요" : ""}</p>
    {start?.seconds != null && <button className={button} disabled={!isOriginal} onClick={() => seek(start.seconds!)}>시작 영상으로 이동</button>}
    {start?.seconds != null && start.end_seconds != null && <div className="relative h-3 rounded bg-accent/20" role="img" aria-label={`시작 ${wodSeconds(start.seconds)}, 마지막 수행 ${wodSeconds(start.end_seconds)}`}><span className="absolute inset-y-0 w-1 bg-accent" style={{ left: `${start.seconds / start.end_seconds * 100}%` }} /></div>}
    <p className="text-xs text-text-secondary">{start?.evidence} 시작 전 대기는 제외하고, WOD 도중 휴식은 포함합니다.</p>
    <div className="flex flex-wrap items-center gap-2">
      <label>시작(초) <input aria-label="WOD 시작 초" className="w-24 rounded border border-border bg-bg-base p-2" type="number" min="0" step="0.1" value={startInput} onChange={e => setStartInput(e.target.value)} /></label>
      <button className={button} disabled={!isOriginal || waiting} onClick={() => setStartInput(currentTime.toFixed(1))}>현재 영상 위치 지정</button>
      <button className={button} disabled={waiting || create.isPending || startInput === "" || !Number.isFinite(Number(startInput)) || Number(startInput) < 0} onClick={() => { apply.reset(); create.mutate(Number(startInput)); }}>시작 수정 후 재평가</button>
      <button className={button} disabled={waiting || create.isPending} onClick={() => { apply.reset(); create.mutate(null); }}>자동 추정 복원</button>
    </div>
    {waiting && <p>시작 시점을 반영해 전체 WOD를 재평가하고 있습니다.</p>}
    {runId != null && <button className={button} disabled={apply.isPending} onClick={() => { localStorage.removeItem(key); setRunId(undefined); apply.reset(); }}>자동 적용 대기 취소</button>}
    {stopped && <button className={button} onClick={() => { resetHistoryPolling(client, ["session-reanalysis", sessionId]); void run.refetch(); }}>상태 확인 재개</button>}
    {apply.isError && runId != null && <button className={button} onClick={() => apply.mutate(runId)}>결과 적용 다시 시도</button>}
    {run.data && ["FAILED", "VIDEO_UNAVAILABLE", "CONTEXT_UNAVAILABLE"].includes(run.data.status) && <p role="alert">재평가 실패: {run.data.error}</p>}
    <p>Intensity: {data?.score?.intensity ?? "판단 불가"} · 종합 점수: {data?.score?.overall ?? "판단 불가"}</p>
    <p>Effort는 운동이 본인에게 얼마나 힘들었는지 나타냅니다. 1(매우 쉬움)~10(최대 노력)</p>
    <div className="flex flex-wrap gap-2">{Array.from({ length: 10 }, (_, i) => i + 1).map(n => <button key={n} className={`${button} ${(effort ?? data?.effort) === n ? "bg-accent text-white" : ""}`} aria-pressed={(effort ?? data?.effort) === n} disabled={save.isPending} onClick={() => setEffort(n)}>{n}</button>)}</div>
    <button className={button} disabled={effort == null || save.isPending} onClick={() => save.mutate(effort!)}>Effort 저장</button>
    <p>운동별 부하: {data?.session_load == null ? "시간·Effort 확인 필요" : `${data.session_load.toFixed(1)} AU`}</p>
    {comparison && <><p>7일 일평균: {comparison.seven.daily_average.toFixed(1)} AU · 28일 일평균: {comparison.twenty_eight.daily_average.toFixed(1)} AU</p>
      <p>{comparison.change_percent != null ? `28일 기준 대비 ${comparison.change_percent >= 0 ? "+" : ""}${comparison.change_percent.toFixed(1)}%` : comparison.status === "collecting_baseline" ? "28일 기준 수집 중" : comparison.status === "no_baseline" ? "비교할 부하 기준이 없습니다." : "입력·분석이 부족한 기록이 있어 비교를 보류합니다."}</p>
      <p className="text-xs text-text-secondary">28일 누락: Effort {comparison.twenty_eight.missing_effort}건 · 시간 {comparison.twenty_eight.missing_duration}건 · 운동 일시 {loads.data?.missing_workout_time ?? 0}건{(loads.data?.unevaluated_legacy_workouts ?? 0) > 0 ? ` · 재분석 미적용 이전 WOD ${loads.data?.unevaluated_legacy_workouts}건` : ""}. 합계는 확인된 기록만 반영합니다.</p></>}
    {error && <p role="alert">{error instanceof Error ? error.message : "저장하거나 불러오지 못했습니다."}</p>}
  </section>;
}
