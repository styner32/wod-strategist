import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
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
  const [appearanceHints, setAppearanceHints] = useState('');

  const createMutation = useMutation({
    mutationFn: () => {
      return historyApi.createSessionReanalysis(
        sessionId,
        crypto.randomUUID(),
        appearanceHints.trim() || undefined,
      );
    },
    onSuccess: async (response) => {
      setExplicitRunId(response.run_id);
      await queryClient.invalidateQueries({ queryKey: ['session-reanalyses', sessionId] });
    },
  });

  const readiness = listResponse?.readiness;
  const canCreate = chunkBatchReady
    && readiness?.can_create === true
    && !createMutation.isPending;
  const blockedReason = chunkBatchBlockedReason
    ?? readiness?.blocked_reason
    ?? (!listLoading && !listResponse ? 'Whole-workout re-analysis is unavailable.' : undefined);

  const requestReanalysis = () => {
    const confirmed = window.confirm(
      'Re-analyze the complete workout video with the current analyzer? This starts a separate AI analysis job that may make multiple model calls, uses only saved corrections, and will not replace the original analysis or regenerate derived media.',
    );
    if (confirmed) createMutation.mutate();
  };

  return (
    <section
      className="mt-6 rounded-xl border border-border bg-bg-elevated p-5"
      aria-labelledby="session-reanalysis-heading"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id="session-reanalysis-heading" className="text-lg font-semibold text-text-primary">
            Whole-workout re-analysis
          </h2>
          <p className="mt-1 max-w-3xl text-xs leading-relaxed text-text-muted">
            Run the current analyzer against the complete server-stored workout video. The original result remains immutable,
            and no highlights, subtitles, hardsubs, injury analysis, or TTS are regenerated. One job may make multiple model calls.
          </p>
          <div className="mt-3 flex flex-wrap gap-2 text-xs">
            <input
              type="text"
              placeholder="Target appearance (e.g., black t-shirt, grey shorts, red shoes)"
              value={appearanceHints}
              onChange={(e) => setAppearanceHints(e.target.value)}
              className="w-80 rounded border border-border bg-bg-surface px-2.5 py-1 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </div>
        </div>
        <button
          type="button"
          onClick={requestReanalysis}
          disabled={!canCreate}
          className="rounded-lg border border-accent/40 px-3 py-2 text-sm font-semibold text-accent hover:bg-accent/10 focus:outline-none focus:ring-2 focus:ring-accent disabled:cursor-not-allowed disabled:opacity-40"
        >
          {createMutation.isPending ? 'Queueing…' : 'Re-analyze whole workout'}
        </button>
      </div>

      <div className="mt-3 text-sm" aria-live="polite">
        {blockedReason && !canCreate && <p className="text-warning">{blockedReason}</p>}
        {selectedRun && (
          <p className={isTerminal(selectedRun.status) ? 'text-text-secondary' : 'text-warning'}>
            Attempt {selectedRun.id}: {selectedRun.status.replaceAll('_', ' ').toLowerCase()}.
          </p>
        )}
      </div>

      {unconfirmedCandidateCount > 0 && (
        <p className="mt-3 rounded-lg border border-warning/20 bg-warning/5 px-3 py-2 text-xs text-warning">
          {unconfirmedCandidateCount} completed chunk {unconfirmedCandidateCount === 1 ? 'candidate has' : 'candidates have'} not been saved as corrections.
          Whole-workout re-analysis uses only corrections you explicitly save.
        </p>
      )}

      {(listError || runError || createMutation.isError) && (
        <p role="alert" className="mt-3 rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {errorMessage(createMutation.error ?? runError ?? listError)}
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
            <span className="text-xs font-medium text-text-muted">Immutable</span>
          </div>
          <pre className="mt-3 max-h-80 overflow-auto whitespace-pre-wrap font-sans text-xs leading-relaxed text-text-secondary">
            {originalAnalysis?.output || 'No original analysis is available.'}
          </pre>
        </article>

        <article className="min-w-0 rounded-lg border border-border bg-bg-secondary/60 p-4">
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
        </article>
      </div>
    </section>
  );
}
