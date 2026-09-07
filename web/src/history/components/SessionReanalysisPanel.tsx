import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import {
  historyApi,
  type AnalysisResult,
  type SessionReanalysisRun,
  type SessionReanalysisStatus,
} from '../../api/history';

interface Props {
  sessionId: string;
  originalAnalysis?: AnalysisResult;
  chunkBatchReady: boolean;
  chunkBatchBlockedReason?: string;
  unconfirmedCandidateCount: number;
}

function isTerminal(status?: SessionReanalysisStatus) {
  return status != null && status !== 'QUEUED' && status !== 'RUNNING';
}

function statusClasses(status: SessionReanalysisStatus) {
  switch (status) {
    case 'COMPLETED': return 'border-success/20 bg-success/10 text-success';
    case 'FAILED':
    case 'VIDEO_UNAVAILABLE':
    case 'CONTEXT_UNAVAILABLE':
      return 'border-error/20 bg-error/10 text-error';
    default: return 'border-warning/20 bg-warning/10 text-warning';
  }
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'The request failed.';
}

function formatDate(value?: string | null) {
  return value ? new Date(value).toLocaleString() : '—';
}

export function SessionReanalysisPanel({
  sessionId,
  originalAnalysis,
  chunkBatchReady,
  chunkBatchBlockedReason,
  unconfirmedCandidateCount,
}: Props) {
  const queryClient = useQueryClient();
  const [explicitRunId, setExplicitRunId] = useState<number>();
  const [appearanceHints, setAppearanceHints] = useState('');
  const [wodDescription, setWodDescription] = useState(originalAnalysis?.wod_description ?? '');
  const [isEditingWod, setIsEditingWod] = useState(false);

  useEffect(() => {
    if (originalAnalysis?.wod_description && !isEditingWod) {
      setWodDescription(originalAnalysis.wod_description);
    }
  }, [originalAnalysis?.wod_description, isEditingWod]);

  const {
    data: listResponse,
    error: listError,
    isLoading: listLoading,
  } = useQuery({
    queryKey: ['session-reanalyses', sessionId],
    queryFn: () => historyApi.listSessionReanalyses(sessionId),
    retry: false,
    refetchInterval: (query) => {
      const runs = query.state.data?.runs ?? [];
      if (!runs.some((run) => !isTerminal(run.status))) return false;
      const pollCount = Math.min(query.state.dataUpdateCount, 4);
      return Math.min(1000 * (2 ** pollCount), 8000);
    },
  });

  const runs = listResponse?.runs ?? [];
  const selectedRunId = explicitRunId ?? runs[0]?.id;
  const selectedListRun = runs.find((run) => run.id === selectedRunId);

  const { data: runDetail, error: runError } = useQuery({
    queryKey: ['session-reanalysis', sessionId, selectedRunId],
    queryFn: () => historyApi.getSessionReanalysis(sessionId, selectedRunId!),
    enabled: selectedRunId != null,
    retry: false,
    refetchInterval: (query) => {
      if (query.state.error) return false;
      const status = (query.state.data as SessionReanalysisRun | undefined)?.status;
      if (isTerminal(status)) return false;
      const pollCount = Math.min(query.state.dataUpdateCount, 4);
      return Math.min(1000 * (2 ** pollCount), 8000);
    },
  });

  const selectedRun = runDetail ?? selectedListRun;

  const createMutation = useMutation({
    mutationFn: () => {
      return historyApi.createSessionReanalysis(
        sessionId,
        crypto.randomUUID(),
        appearanceHints.trim() || undefined,
        undefined,
        wodDescription.trim() || undefined,
      );
    },
    onSuccess: async (response) => {
      setExplicitRunId(response.run_id);
      await queryClient.invalidateQueries({ queryKey: ['session-reanalyses', sessionId] });
    },
  });

  const applyMutation = useMutation({
    mutationFn: (runId: number) => historyApi.applySessionReanalysis(sessionId, runId),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['session-analysis', sessionId] }),
        queryClient.invalidateQueries({ queryKey: ['analysis', sessionId] }),
        queryClient.invalidateQueries({ queryKey: ['session', sessionId] }),
        queryClient.invalidateQueries({ queryKey: ['history'] }),
        queryClient.invalidateQueries({ queryKey: ['session-reanalyses', sessionId] }),
        queryClient.invalidateQueries({ queryKey: ['session-cost', sessionId] }),
        queryClient.invalidateQueries({ queryKey: ['total-cost'] }),
        queryClient.invalidateQueries({ queryKey: ['related-wods'] }),
      ]);
    },
  });

  const handleApply = (runId: number) => {
    const confirmed = window.confirm(
      '이 재분석 후보를 메인 세션 분석 결과로 적용(Apply)하시겠습니까?\n기존 분석 내용, 점수, 하이라이트 및 WOD 설명이 이 후보의 내용으로 갱신됩니다.',
    );
    if (confirmed) {
      applyMutation.mutate(runId);
    }
  };

  const readiness = listResponse?.readiness;
  const canCreate = chunkBatchReady
    && readiness?.can_create === true
    && !createMutation.isPending;
  const blockedReason = chunkBatchBlockedReason
    ?? readiness?.blocked_reason
    ?? (!listLoading && !listResponse ? 'Whole-workout re-analysis is unavailable.' : undefined);

  const requestReanalysis = () => {
    const confirmed = window.confirm(
      '수정된 WOD 설명 및 인상착의 힌트로 전체 운동 영상을 다시 분석하시겠습니까? 별도의 AI 분석 작업이 실행되며 완료 후 후보 결과를 메인 세션에 적용할 수 있습니다.',
    );
    if (confirmed) createMutation.mutate();
  };

  return (
    <section
      id="session-reanalysis-panel"
      className="mt-6 rounded-xl border border-border bg-bg-elevated p-5"
      aria-labelledby="session-reanalysis-heading"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="max-w-2xl">
          <h2 id="session-reanalysis-heading" className="text-lg font-semibold text-text-primary">
            Whole-workout re-analysis (전체 세션 재분석)
          </h2>
          <p className="mt-1 text-xs leading-relaxed text-text-muted">
            WOD 설명이 누락되었거나 동작/주요 인물이 잘못 인식된 경우, WOD 설명과 인상착의를 수정한 후 서버에 보관된 전체 영상으로 재분석할 수 있습니다.
            완료된 후보(Candidate)는 하단 버튼을 통해 메인 세션에 즉시 적용(Apply)할 수 있습니다.
          </p>
          <div className="mt-3 flex flex-col gap-2.5 text-xs">
            <label className="flex flex-col gap-1 text-text-secondary">
              <span className="font-semibold text-text-primary">WOD 설명 (수정 후 전체 재분석 가능)</span>
              <textarea
                rows={3}
                placeholder="예: 21-15-9 Thrusters (95/65 lb), Pull-ups"
                value={wodDescription}
                onChange={(e) => {
                  setIsEditingWod(true);
                  setWodDescription(e.target.value);
                }}
                className="w-full rounded-lg border border-border bg-bg-surface p-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent leading-relaxed"
              />
            </label>
            <label className="flex flex-col gap-1 text-text-secondary">
              <span className="font-semibold text-text-primary">인상착의 힌트 (Target appearance)</span>
              <input
                type="text"
                placeholder="예: 검은색 상의, 회색 반바지, 빨간 신발"
                value={appearanceHints}
                onChange={(e) => setAppearanceHints(e.target.value)}
                className="w-full rounded-lg border border-border bg-bg-surface px-2.5 py-1.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </label>
          </div>
        </div>
        <button
          type="button"
          onClick={requestReanalysis}
          disabled={!canCreate}
          className="rounded-lg border border-accent/40 bg-accent/10 px-4 py-2.5 text-sm font-semibold text-accent hover:bg-accent/20 focus:outline-none focus:ring-2 focus:ring-accent disabled:cursor-not-allowed disabled:opacity-40 transition-colors shadow-sm"
        >
          {createMutation.isPending ? 'Queueing…' : 'Re-analyze whole workout (전체 재분석)'}
        </button>
      </div>

      <div className="mt-3 text-sm" aria-live="polite">
        {blockedReason && !canCreate && <p className="text-warning">{blockedReason}</p>}
        {selectedRun && (
          <p className={isTerminal(selectedRun.status) ? 'text-text-secondary' : 'text-warning'}>
            Attempt #{selectedRun.id}: {selectedRun.status.replaceAll('_', ' ').toLowerCase()}.
          </p>
        )}
      </div>

      {unconfirmedCandidateCount > 0 && (
        <p className="mt-3 rounded-lg border border-warning/20 bg-warning/5 px-3 py-2 text-xs text-warning">
          {unconfirmedCandidateCount} completed chunk {unconfirmedCandidateCount === 1 ? 'candidate has' : 'candidates have'} not been saved as corrections.
          Whole-workout re-analysis uses only corrections you explicitly save.
        </p>
      )}

      {applyMutation.isSuccess && (
        <p className="mt-3 rounded-lg border border-success/30 bg-success/10 px-3 py-2 text-sm text-success">
          재분석 결과가 메인 세션 분석 결과로 성공적으로 적용되었습니다.
        </p>
      )}

      {(listError || runError || createMutation.isError || applyMutation.isError) && (
        <p role="alert" className="mt-3 rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {errorMessage(createMutation.error ?? applyMutation.error ?? runError ?? listError)}
        </p>
      )}

      {runs.length > 0 && (
        <label className="mt-4 block text-xs font-medium text-text-secondary">
          Attempt
          <select
            value={selectedRunId ?? ''}
            onChange={(event) => setExplicitRunId(Number(event.target.value))}
            className="mt-1 block w-full rounded-lg border border-border bg-bg-secondary px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent sm:max-w-md"
          >
            {runs.map((run) => (
              <option key={run.id} value={run.id}>
                #{run.id} · {run.status.replaceAll('_', ' ')} · {formatDate(run.created_at)}
              </option>
            ))}
          </select>
        </label>
      )}

      <div className="mt-4 grid gap-3 lg:grid-cols-2">
        <article className="min-w-0 rounded-lg border border-border bg-bg-secondary/60 p-4">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-semibold text-text-primary">Original analysis</h3>
            <span className="text-xs font-medium text-text-muted">Current</span>
          </div>
          {originalAnalysis?.wod_description && (
            <div className="mt-2 text-xs bg-bg-surface/60 p-2 rounded border border-border-subtle">
              <span className="font-semibold text-text-muted">WOD: </span>
              <span className="text-text-secondary">{originalAnalysis.wod_description}</span>
            </div>
          )}
          <pre className="mt-3 max-h-80 overflow-auto whitespace-pre-wrap font-sans text-xs leading-relaxed text-text-secondary">
            {originalAnalysis?.output || 'No original analysis is available.'}
          </pre>
        </article>

        <article className="min-w-0 rounded-lg border border-border bg-bg-secondary/60 p-4 flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between gap-2">
              <h3 className="text-sm font-semibold text-text-primary">Re-analysis candidate</h3>
              {selectedRun ? (
                <span className={`rounded border px-1.5 py-0.5 text-[11px] font-medium ${statusClasses(selectedRun.status)}`}>
                  {selectedRun.status.replaceAll('_', ' ')}
                </span>
              ) : (
                <span className="text-xs font-medium text-text-muted">Not applied</span>
              )}
            </div>
            {selectedRun?.wod_description && (
              <div className="mt-2 text-xs bg-bg-surface/60 p-2 rounded border border-border-subtle">
                <span className="font-semibold text-text-muted">Candidate WOD: </span>
                <span className="text-text-secondary">{selectedRun.wod_description}</span>
              </div>
            )}
            <pre className="mt-3 max-h-80 overflow-auto whitespace-pre-wrap font-sans text-xs leading-relaxed text-text-secondary">
              {selectedRun?.candidate?.output
                || (selectedRun && !isTerminal(selectedRun.status) ? 'Analysis is in progress…' : 'No candidate has been generated.')}
            </pre>
            {selectedRun && (
              <dl className="mt-3 grid grid-cols-2 gap-2 border-t border-border pt-3 text-xs">
                <div><dt className="text-text-muted">Model</dt><dd className="text-text-primary">{selectedRun.model || '—'}</dd></div>
                <div><dt className="text-text-muted">Duration</dt><dd className="text-text-primary">{selectedRun.duration_ms ? `${selectedRun.duration_ms} ms` : '—'}</dd></div>
                <div><dt className="text-text-muted">Started</dt><dd className="text-text-primary">{formatDate(selectedRun.started_at)}</dd></div>
                <div><dt className="text-text-muted">Completed</dt><dd className="text-text-primary">{formatDate(selectedRun.completed_at)}</dd></div>
              </dl>
            )}
          </div>

          {selectedRun?.status === 'COMPLETED' && (
            <div className="mt-4 flex items-center justify-between border-t border-border pt-3">
              <span className="text-[11px] text-text-muted">
                후보 결과를 메인 세션 분석 결과로 교체합니다.
              </span>
              <button
                type="button"
                onClick={() => handleApply(selectedRun.id)}
                disabled={applyMutation.isPending}
                className="rounded-lg bg-accent px-3.5 py-1.5 text-xs font-semibold text-white hover:bg-accent/90 focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50 transition-colors shadow-sm"
              >
                {applyMutation.isPending ? '적용 중…' : '메인 세션에 적용 (Apply)'}
              </button>
            </div>
          )}
        </article>
      </div>
    </section>
  );
}
