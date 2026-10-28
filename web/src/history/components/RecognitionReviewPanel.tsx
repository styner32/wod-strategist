import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { formatStartTimestamp, parseStartTimestamp } from "../../../../shared/wodTraining";
import { describeUnassessedReason } from "../../../../shared/workoutProgress";
import { historyApi, type CreateRecognitionReviewRequest, type RecognitionReviewFinding, type RecognitionReviewStatus } from "../../api/history";
import { historyPollInterval, historyPollingStopped, resetHistoryPolling } from "../historyPolling";
import { recognitionReviewSelection } from "../recognitionReview";

const button = "rounded-lg border border-border bg-bg-secondary px-3 py-2 text-xs font-medium text-text-primary hover:bg-bg-tertiary disabled:opacity-40";
const field = "rounded-lg border border-border bg-bg-base px-3 py-2 text-sm text-text-primary";
const labels: Record<RecognitionReviewStatus, string> = { QUEUED: "대기 중", RUNNING: "진단 중", COMPLETED: "진단 완료", PARTIAL: "일부 구간 진단 완료", FAILED: "진단 실패" };
const evidenceLabels: Record<RecognitionReviewFinding["evidence_level"], string> = { stored_fact: "저장 기록으로 확인", video_supported_hypothesis: "영상 근거에 따른 추정", undetermined: "확인 불가" };
const stageLabels: Record<string, string> = { chunk_observation: "짧은 구간 관찰", segment_selection: "분석 구간 선택", deep_observation: "심층 관찰", aggregation: "결과 집계", unknown: "확인 불가" };
const terminal = (status?: RecognitionReviewStatus) => status != null && status !== "QUEUED" && status !== "RUNNING";
const tokens = (value?: number | null) => value == null ? "확인 불가" : value.toLocaleString();
const seconds = (value: number) => `${Number(value.toFixed(1))}초`;
const errorText = (error: unknown) => error instanceof Error ? error.message : "요청을 완료하지 못했습니다.";

interface Props {
  sessionId: string;
  profileId: number;
  sourceUpdatedAt?: string;
  currentTime?: number;
  duration?: number;
  isOriginal: boolean;
  seek: (seconds: number) => void;
}

export function RecognitionReviewPanel(props: Props) {
  // A changed source gets its own form and pending-request guard immediately.
  return <RecognitionReviewContent key={`${props.profileId}:${props.sessionId}:${props.sourceUpdatedAt ?? ""}`} {...props} />;
}

function RecognitionReviewContent({ sessionId, profileId, sourceUpdatedAt, currentTime, duration, isOriginal, seek }: Props) {
  const client = useQueryClient();
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  const [startInput, setStartInput] = useState("00:00");
  const [endInput, setEndInput] = useState("");
  const [explicitRunId, setExplicitRunId] = useState<number>();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(true);
  const pending = useRef(false);
  const requestIdentity = useRef<{ payload: string; id: string } | undefined>(undefined);
  const evaluationKey = ["recognition-evaluation", sessionId, profileId, sourceUpdatedAt];
  const listKey = ["recognition-reviews", sessionId, profileId];
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const evaluationQuery = useQuery({ queryKey: evaluationKey, queryFn: () => historyApi.getRecognitionEvaluation(sessionId), retry: false });
  const evaluation = evaluationQuery.data;
  const fingerprint = evaluation?.fingerprint;
  const currentFingerprint = useRef(fingerprint); currentFingerprint.current = fingerprint;
  const initializedFingerprint = useRef<string | undefined>(undefined);
  const rows = useMemo(() => evaluation?.records ?? [], [evaluation?.records]);

  useEffect(() => {
    if (!evaluation || initializedFingerprint.current === evaluation.fingerprint) return;
    initializedFingerprint.current = evaluation.fingerprint;
    setSelectedIds(evaluation.records.filter(row => row.status !== "matched").map(row => row.record.id));
    setStartInput("00:00");
    // Preserve full precision of the saved bound; formatting to tenths could exclude it.
    setEndInput(String(Math.max(0, ...evaluation.records.map(row => row.record.end_secs))));
    requestIdentity.current = undefined;
  }, [evaluation]);

  const listQuery = useQuery({
    queryKey: listKey,
    queryFn: () => historyApi.listRecognitionReviews(sessionId),
    retry: false,
    refetchInterval: query => {
      const active = (query.state.data?.runs ?? []).filter(run => !terminal(run.status)).map(run => run.id).sort((a, b) => a - b);
      return historyPollInterval(query, active.length ? JSON.stringify(active) : undefined);
    },
  });
  const runs = listQuery.data?.runs ?? [];
  const activeRun = runs.find(run => !terminal(run.status));
  const runId = explicitRunId ?? runs[0]?.id;
  const detailKey = ["recognition-review", sessionId, profileId, runId];
  const detailQuery = useQuery({
    queryKey: detailKey,
    queryFn: () => historyApi.getRecognitionReview(sessionId, runId!),
    enabled: runId != null,
    retry: false,
    refetchInterval: query => historyPollInterval(query, runId != null && !terminal(query.state.data?.status) ? String(runId) : undefined),
  });
  const selectedRun = detailQuery.data ?? runs.find(run => run.id === runId);
  const hasActiveRun = !!activeRun || (!!selectedRun && !terminal(selectedRun.status)) || (explicitRunId != null && !selectedRun);
  const completedSeen = useRef(new Set<string>());
  useEffect(() => {
    if (!selectedRun || !terminal(selectedRun.status)) return;
    const identity = `${selectedRun.id}:${selectedRun.updated_at}`;
    if (completedSeen.current.has(identity)) return;
    completedSeen.current.add(identity);
    void client.invalidateQueries({ queryKey: ["session-cost", sessionId] });
  }, [client, sessionId, selectedRun]);

  const start = parseStartTimestamp(startInput);
  const end = parseStartTimestamp(endInput);
  const selection = recognitionReviewSelection(rows, selectedIds, start, end);
  const beyondDuration = isOriginal && duration != null && Number.isFinite(duration) && duration > 0 && end != null && end > duration + 0.001;
  const pollingStopped = historyPollingStopped(client, listKey, listQuery.dataUpdatedAt) || historyPollingStopped(client, detailKey, detailQuery.dataUpdatedAt);
  const resume = () => {
    for (const key of [listKey, detailKey]) {
      resetHistoryPolling(client, key);
      void client.refetchQueries({ queryKey: key, exact: true, type: "active" });
    }
  };
  const submit = async () => {
    if (!evaluation || !selection.valid || beyondDuration || !selection.records.length || selection.records.length > 100 || pending.current || hasActiveRun) return;
    const submittedFingerprint = evaluation.fingerprint;
    const payload = {
      record_ids: selection.records.map(row => row.record.id).sort((a, b) => a - b),
      start_secs: start!, end_secs: end!, source_version: evaluation.source_version,
      evaluation_fingerprint: evaluation.fingerprint,
    };
    const identity = JSON.stringify(payload);
    if (requestIdentity.current?.payload !== identity) {
      requestIdentity.current = { payload: identity, id: crypto.randomUUID() };
    }
    const body: CreateRecognitionReviewRequest = { ...payload, client_request_id: requestIdentity.current.id };
    pending.current = true; setSubmitting(true); setError("");
    try {
      const response = await historyApi.createRecognitionReview(sessionId, body);
      if (!mounted.current || currentFingerprint.current !== submittedFingerprint) return;
      requestIdentity.current = undefined;
      setExplicitRunId(response.run_id);
      resetHistoryPolling(client, listKey);
      await client.invalidateQueries({ queryKey: listKey });
    } catch (e) {
      if (!mounted.current || currentFingerprint.current !== submittedFingerprint) return;
      if ((e as { status?: number })?.status === 409) {
        setError("분석 또는 확정 기록이 변경되었거나 다른 진단이 진행 중입니다. 최신 상태를 확인해 주세요.");
        await Promise.all([evaluationQuery.refetch(), listQuery.refetch()]);
      } else {
        setError(`진단을 요청하지 못했습니다. 같은 선택으로 다시 시도하면 중복 요청을 방지합니다. ${errorText(e)}`);
      }
    } finally {
      pending.current = false;
      if (mounted.current) setSubmitting(false);
    }
  };

  const hasRows = rows.length > 0;
  const outdated = selectedRun && (selectedRun.outdated || (!!fingerprint && selectedRun.evaluation_fingerprint !== fingerprint));
  return <section aria-label="영상 분석 평가" className="space-y-4 rounded-xl border border-border bg-bg-secondary/30 p-4">
    <div className="space-y-1">
      <h3 className="font-semibold text-text-primary">영상 분석 평가</h3>
      <p className="text-xs text-text-secondary">직접 확정한 구간을 기준으로 기존 인식을 비교합니다. 미확정 구간을 포함한 영상 전체의 정확도는 아닙니다.</p>
      <p className="text-xs text-text-muted">기록 비교에는 AI를 호출하지 않습니다. 영상 오류 진단은 아래 버튼으로 직접 실행합니다.</p>
    </div>
    {evaluationQuery.isLoading && <p role="status">확정 기록을 비교하고 있습니다.</p>}
    {evaluationQuery.isError && <div role="alert"><p>영상 분석 평가를 불러오지 못했습니다.</p><button className={button} onClick={() => void evaluationQuery.refetch()}>평가 다시 불러오기</button></div>}
    {evaluation && <p data-testid="recognition-evaluation-summary" className="text-sm text-text-primary">확정 {evaluation.summary.total}구간 · 일치 {evaluation.summary.matched} · 불일치 {evaluation.summary.mismatch} · 근거 부족 {evaluation.summary.insufficient_evidence}</p>}
    {evaluation && !hasRows && <p className="text-sm text-text-secondary">운동명과 횟수를 확정하면 영상 분석을 평가할 수 있습니다.</p>}
    {hasRows && <>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <caption className="sr-only">확정 기록과 기존 인식 비교</caption>
          <thead><tr className="border-b border-border text-text-secondary"><th className="p-2">선택</th><th className="p-2">확정 구간</th><th className="p-2">기존 인식</th><th className="p-2">사용자 확정</th><th className="p-2">운동명</th><th className="p-2">횟수 차이</th><th className="p-2">관찰 근거</th></tr></thead>
          <tbody>{rows.map(row => <tr key={row.record.id} className="border-b border-border/50 align-top text-text-primary">
            <td className="p-2"><input type="checkbox" aria-label={`진단 기록 ${row.record.id} 선택`} checked={selectedIds.includes(row.record.id)} onChange={event => setSelectedIds(ids => event.target.checked ? [...new Set([...ids, row.record.id])] : ids.filter(id => id !== row.record.id))} /></td>
            <td className="whitespace-nowrap p-2"><button className="text-accent disabled:text-text-muted" disabled={!isOriginal} onClick={() => seek(row.record.start_secs)}>{formatStartTimestamp(row.record.start_secs)}–{formatStartTimestamp(row.record.end_secs)}</button></td>
            <td className="min-w-32 p-2">{row.predicted_movements.length ? row.predicted_movements.map(item => {
              const movement = item.movement || "운동명 확인 불가";
              return row.evidence_complete ? `${movement} ${item.reps}회` : item.reps === 0 ? `${movement} · 횟수 확인 불가` : `${movement} · 확인된 ${item.reps}회`;
            }).join(" / ") : row.observed_complete_reps === 0 ? "완료 반복 0회" : "확인 불가"}{!row.evidence_complete && row.predicted_movements.length > 0 && <span className="block text-warning">일부 관찰만 확인됨</span>}</td>
            <td className="min-w-28 p-2">{row.record.movement} {row.record.reps}회</td>
            <td className="p-2">{row.movement_match == null ? "확인 불가" : row.movement_match ? "일치" : "불일치"}</td>
            <td className="whitespace-nowrap p-2">{row.reps_delta == null ? "확인 불가" : `${row.reps_delta > 0 ? "+" : ""}${row.reps_delta}회`}<span className="block text-text-muted">기존 인식 − 확정</span></td>
            <td className="min-w-28 p-2">{row.evidence_complete ? "확인됨" : "근거 부족"}{row.gaps.length > 0 && <details><summary className="cursor-pointer">미판독 구간 {row.gaps.length}개</summary>{row.gaps.map((gap, index) => <p key={index}>{formatStartTimestamp(gap.start_secs)}–{formatStartTimestamp(gap.end_secs)} · {describeUnassessedReason(gap.reason)}</p>)}</details>}</td>
          </tr>)}</tbody>
        </table>
      </div>
      <div className="flex flex-wrap gap-2">
        <button className={button} onClick={() => setSelectedIds(rows.map(row => row.record.id))}>전체 선택</button>
        <button className={button} onClick={() => setSelectedIds(rows.filter(row => row.status !== "matched").map(row => row.record.id))}>불일치·근거 부족 선택</button>
        <button className={button} onClick={() => setSelectedIds([])}>선택 해제</button>
      </div>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-text-secondary">영상 진단 시작시간<input aria-label="영상 진단 시작시간" className={field} value={startInput} placeholder="00:00" onChange={event => setStartInput(event.target.value)} /></label>
        <button className={button} disabled={!isOriginal || currentTime == null || !Number.isFinite(currentTime)} onClick={() => currentTime != null && setStartInput(String(Math.round(currentTime * 1000) / 1000))}>현재 영상 위치 사용</button>
        <label className="flex flex-col gap-1 text-xs text-text-secondary">영상 진단 종료시간<input aria-label="영상 진단 종료시간" className={field} value={endInput} placeholder="마지막 확정 구간 끝" onChange={event => setEndInput(event.target.value)} /></label>
      </div>
      <p className="text-xs text-text-secondary">분:초, 시:분:초 또는 초를 입력하세요. 범위 안에 완전히 포함된 확정 기록만 진단하며, 선택 구간 사이의 영상은 보내지 않습니다.</p>
      {!selection.valid && <p role="alert" className="text-sm text-warning">시작시간과 그보다 큰 종료시간을 입력해 주세요.</p>}
      {beyondDuration && <p role="alert" className="text-sm text-warning">종료시간이 원본 영상 길이를 넘습니다.</p>}
      {selection.excludedCount > 0 && <p role="status" className="text-sm text-warning">선택한 기록 중 {selection.excludedCount}개는 지정 범위 밖이거나 경계에 걸쳐 제외됩니다.</p>}
      <p data-testid="recognition-review-preview" className="text-sm text-text-primary">진단 대상 {selection.records.length}구간 · 실제 영상 입력 {seconds(selection.inputSeconds)}<span className="ml-1 text-xs text-text-secondary">(긴 구간의 경계 확인용 중복 영상 포함)</span></p>
      {selection.records.length > 100 && <p role="alert" className="text-warning">한 번에 최대 100개 기록을 선택해 주세요.</p>}
      {hasActiveRun && <p role="status" className="text-sm text-warning">진행 중인 진단이 있습니다. 완료 후 새 진단을 실행할 수 있습니다.</p>}
      <button className={`${button} border-accent/40 bg-accent/10`} disabled={submitting || hasActiveRun || !selection.valid || beyondDuration || !selection.records.length || selection.records.length > 100} onClick={() => void submit()}>{submitting ? "진단 요청 중…" : "선택 구간 오류 진단"}</button>
      {error && <p role="alert" className="text-sm text-error">{error}</p>}
    </>}
    <div className="space-y-3 border-t border-border pt-3">
      <h4 className="text-sm font-medium text-text-primary">오류 진단 이력</h4>
      {listQuery.isError && <p role="alert" className="text-sm text-error">진단 이력을 불러오지 못했습니다.</p>}
      {(pollingStopped || listQuery.isError || detailQuery.isError) && <button className={button} onClick={resume}>진단 상태 다시 확인</button>}
      {pollingStopped && <p className="text-xs text-text-secondary">자동 상태 조회가 멈췄습니다. 다시 확인해도 AI 진단을 새로 실행하지 않습니다.</p>}
      {!runs.length && !listQuery.isLoading && !listQuery.isError && <p className="text-xs text-text-muted">아직 실행한 진단이 없습니다.</p>}
      {!!runs.length && <label className="flex flex-col gap-1 text-xs text-text-secondary">진단 실행 선택<select className={field} aria-label="진단 실행 선택" value={runId ?? ""} onChange={event => setExplicitRunId(Number(event.target.value))}>{runs.map(run => <option key={run.id} value={run.id}>{new Date(run.created_at).toLocaleString()} · {labels[run.status]} · {run.record_ids.length}구간</option>)}</select></label>}
      {detailQuery.isError && <p role="alert" className="text-sm text-error">진단 상세 결과를 불러오지 못했습니다.</p>}
      {selectedRun && <div className="space-y-3" data-testid="recognition-review-result">
        <p role="status" className="text-sm font-medium">{labels[selectedRun.status]} · {selectedRun.batches.filter(batch => batch.status === "COMPLETED").length}/{selectedRun.batches.length}개 영상 구간 완료</p>
        {outdated && <p role="alert" className="rounded-lg border border-warning/40 p-3 text-sm text-warning">변경 전 기록을 기준으로 작성한 보고서입니다. 현재 분석 또는 확정값과 다를 수 있습니다.</p>}
        {selectedRun.status === "PARTIAL" && <p className="text-sm text-warning">일부 영상 구간의 진단이 실패했습니다. 아래 성공한 구간의 결과만 확인할 수 있습니다.</p>}
        <p className="text-xs text-text-secondary">모델 {selectedRun.model || "확인 불가"} · 입력 토큰 {tokens(selectedRun.input_tokens)} · 출력 토큰 {tokens(selectedRun.output_tokens)} · 총 토큰 {tokens(selectedRun.total_tokens)}{selectedRun.duration_ms != null && selectedRun.duration_ms > 0 ? ` · 소요 ${seconds(selectedRun.duration_ms / 1000)}` : ""}</p>
        {selectedRun.error && <p role="alert" className="text-sm text-error">{selectedRun.error}</p>}
        {selectedRun.batches.map(batch => <div key={batch.id} className="space-y-2 rounded-lg border border-border p-3">
          {(() => { const saved = selectedRun.evaluation?.records.find(row => row.record.id === batch.record_id)?.record; return saved ? <p className="text-xs font-medium text-text-secondary">확정 기록 전체: {saved.movement} {saved.reps}회 ({formatStartTimestamp(saved.start_secs)}–{formatStartTimestamp(saved.end_secs)})</p> : null; })()}
          <p className="text-xs text-text-secondary">검토 구간: {formatStartTimestamp(batch.core_start_secs)}–{formatStartTimestamp(batch.core_end_secs)}</p>
          {batch.error && <p role="alert" className="text-sm text-error">이 구간을 진단하지 못했습니다. {batch.error}</p>}
          {batch.status === "COMPLETED" && !(batch.findings ?? []).length && <p className="text-sm text-text-secondary">이 구간에서 설명할 수 있는 추가 오류 근거를 찾지 못했습니다.</p>}
          {(batch.findings ?? []).map((finding, index) => <article key={index} className="space-y-2 rounded-lg bg-bg-base p-3 text-sm">
            <div className="flex flex-wrap items-center justify-between gap-2"><span className="text-xs font-semibold text-accent">{evidenceLabels[finding.evidence_level] ?? "확인 불가"}</span><button className={button} disabled={!isOriginal} title={isOriginal ? undefined : "원본 영상에서 시간대를 확인할 수 있습니다."} onClick={() => seek(finding.start_secs)}>{formatStartTimestamp(finding.start_secs)}–{formatStartTimestamp(finding.end_secs)} 영상 근거</button></div>
            <p className="font-medium text-text-primary">{finding.difference}</p>
            <dl className="space-y-2 whitespace-pre-wrap text-text-secondary">
              <div><dt className="font-medium">원본 판단 근거</dt><dd>{finding.original_evidence || "확인 불가"}</dd></div>
              <div><dt className="font-medium">영상에서 보이는 근거</dt><dd>{finding.visual_evidence || "확인 불가"}</dd></div>
              <div><dt className="font-medium">발생 단계와 가능한 이유</dt><dd>{stageLabels[finding.stage] ?? "확인 불가"} · {finding.possible_reason || "확인 불가"}</dd></div>
              <div><dt className="font-medium">확인의 한계</dt><dd>{finding.limitations || "특정 반복의 누락이나 모델 내부 판단 이유를 확정하는 결과가 아닙니다."}</dd></div>
            </dl>
          </article>)}
        </div>)}
      </div>}
    </div>
  </section>;
}
