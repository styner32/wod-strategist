import { useQuery } from '@tanstack/react-query';
import { useMemo, useRef, useState } from 'react';
import {
  historyApi,
  type AnalysisFeedback,
  type ChunkAnalysisResult,
  type ChunkReanalysisCandidate,
  type ChunkReanalysisRun,
  type FeedbackCorrection,
} from '../../api/history';

interface Props {
  sessionId: string;
  chunk: ChunkAnalysisResult;
  feedback?: AnalysisFeedback;
  runs: ChunkReanalysisRun[];
  runsLoading: boolean;
  creatingRun: boolean;
  reanalysisError?: string;
  onCreateRun: () => void;
  onCorrect: () => void;
  onUndoCorrection?: () => void;
  onUseCandidate: (correction: FeedbackCorrection, runId: number) => void;
}

function runId(run: ChunkReanalysisRun) {
  return run.id ?? run.run_id;
}

function mediaStart(chunk: ChunkAnalysisResult) {
  return chunk.media_start_secs ?? null;
}

function mediaEnd(chunk: ChunkAnalysisResult) {
  return chunk.media_end_secs ?? null;
}

function formatTime(secs: number | null | undefined) {
  if (secs == null || !Number.isFinite(secs)) return '—';
  const minutes = Math.floor(secs / 60);
  const seconds = Math.floor(secs % 60);
  return `${minutes}:${seconds.toString().padStart(2, '0')}`;
}

function formatDate(value?: string) {
  if (!value) return '—';
  return new Date(value).toLocaleString();
}

function candidateFor(run?: ChunkReanalysisRun) {
  if (!run) return undefined;
  const candidate = run.candidate ?? run.candidate_result ?? undefined;
  if (candidate) return candidate;
  if (run.candidate_exercise_type || run.candidate_output) {
    return {
      exercise_type: run.candidate_exercise_type,
      output: run.candidate_output ?? undefined,
    } satisfies ChunkReanalysisCandidate;
  }
  return undefined;
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (value == null) return undefined;
  if (typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  if (typeof value !== 'string') return undefined;
  try {
    const parsed = JSON.parse(value) as unknown;
    return typeof parsed === 'object' && parsed != null && !Array.isArray(parsed)
      ? parsed as Record<string, unknown>
      : undefined;
  } catch {
    return undefined;
  }
}

function stringField(record: Record<string, unknown> | undefined, ...keys: string[]) {
  for (const key of keys) {
    const value = record?.[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return undefined;
}

function correctionMovement(feedback?: AnalysisFeedback) {
  return feedback?.correction?.movement_name
    ?? feedback?.corrected_movement
    ?? null;
}

function correctionActivity(feedback?: AnalysisFeedback) {
  return feedback?.correction?.activity_state
    ?? feedback?.corrected_activity_state
    ?? null;
}

function correctionFatigue(feedback?: AnalysisFeedback) {
  return feedback?.correction?.fatigue_state
    ?? feedback?.corrected_fatigue_state
    ?? null;
}

function candidateCorrection(candidate: ChunkReanalysisCandidate): FeedbackCorrection {
  const structured = candidate.structured_inference;
  const signals = asRecord(candidate.observed_signals);
  const movement = candidate.exercise_type
    ?? structured?.observed_movement_name
    ?? stringField(signals, 'observed_movement_name', 'exercise_type', 'movement');
  const activity = structured?.activity_state
    ?? stringField(signals, 'activity_state')
    ?? (typeof movement === 'string' && movement ? 'exercise' : 'unknown');
  const fatigue = structured?.fatigue_state ?? stringField(signals, 'fatigue_state');
  return {
    activity_state: typeof activity === 'string' ? activity : 'unknown',
    movement_name: typeof movement === 'string' ? movement : undefined,
    fatigue_state: typeof fatigue === 'string' ? fatigue : undefined,
  };
}

function statusClasses(status: string) {
  switch (status) {
    case 'COMPLETED': return 'bg-success/10 text-success border-success/20';
    case 'FAILED':
    case 'VIDEO_UNAVAILABLE':
    case 'INTERVAL_UNAVAILABLE':
      return 'bg-error/10 text-error border-error/20';
    default: return 'bg-warning/10 text-warning border-warning/20';
  }
}

function ResultCard({
  title,
  movement,
  activity,
  fatigue,
  output,
  badge,
}: {
  title: string;
  movement?: string | null;
  activity?: string | null;
  fatigue?: string | null;
  output?: string | null;
  badge?: string;
}) {
  return (
    <article className="min-w-0 rounded-lg border border-border bg-bg-secondary/60 p-3">
      <div className="flex items-center justify-between gap-2">
        <h4 className="text-sm font-semibold text-text-primary">{title}</h4>
        {badge && <span className="text-xs font-medium text-success">{badge}</span>}
      </div>
      <dl className="mt-3 space-y-2 text-xs">
        <div>
          <dt className="text-text-muted">Movement</dt>
          <dd className="mt-0.5 text-text-primary">{movement || 'Not identified'}</dd>
        </div>
        <div>
          <dt className="text-text-muted">Activity</dt>
          <dd className="mt-0.5 text-text-primary">{activity?.replaceAll('_', ' ') || 'Not provided'}</dd>
        </div>
        <div>
          <dt className="text-text-muted">Fatigue</dt>
          <dd className="mt-0.5 text-text-primary">{fatigue?.replaceAll('_', ' ') || 'Not provided'}</dd>
        </div>
      </dl>
      {output && (
        <p className="mt-3 max-h-32 overflow-y-auto whitespace-pre-wrap border-t border-border pt-3 text-xs leading-relaxed text-text-secondary">
          {output}
        </p>
      )}
    </article>
  );
}

export function ChunkInspector({
  sessionId,
  chunk,
  feedback,
  runs,
  runsLoading,
  creatingRun,
  reanalysisError,
  onCreateRun,
  onCorrect,
  onUndoCorrection,
  onUseCandidate,
}: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [explicitRunId, setExplicitRunId] = useState<number | undefined>();
  const sortedRuns = useMemo(
    () => [...runs].sort((a, b) => {
      const timeDiff = Date.parse(b.created_at ?? '') - Date.parse(a.created_at ?? '');
      if (Number.isFinite(timeDiff) && timeDiff !== 0) return timeDiff;
      return (runId(b) ?? 0) - (runId(a) ?? 0);
    }),
    [runs],
  );
  const selectedRunId = explicitRunId ?? runId(sortedRuns[0]);

  const { data: playData, error: playError, isLoading: playLoading } = useQuery({
    queryKey: ['chunk-play-url', sessionId, chunk.id],
    queryFn: () => historyApi.getChunkPlayUrl(sessionId, chunk.id),
    staleTime: 10 * 60 * 1000,
  });

  const { data: runDetail } = useQuery({
    queryKey: ['chunk-reanalysis', sessionId, chunk.id, selectedRunId],
    queryFn: () => historyApi.getChunkReanalysis(sessionId, chunk.id, selectedRunId!),
    enabled: selectedRunId != null,
    refetchInterval: (query) => {
      const status = (query.state.data as ChunkReanalysisRun | undefined)?.status;
      if (status !== 'QUEUED' && status !== 'RUNNING') return false;
      const pollCount = Math.min(query.state.dataUpdateCount, 4);
      return Math.min(1000 * (2 ** pollCount), 8000);
    },
  });

  const selectedRun = runDetail
    ?? sortedRuns.find((run) => runId(run) === selectedRunId);
  const candidate = candidateFor(selectedRun);
  const structuredCandidate = candidate?.structured_inference;
  const originalStructured = chunk.structured_inference;
  const originalSignals = asRecord(chunk.observed_signals);
  const candidateSignals = asRecord(candidate?.observed_signals);
  const candidateMovement = candidate?.exercise_type
    ?? structuredCandidate?.observed_movement_name
    ?? stringField(candidateSignals, 'observed_movement_name', 'exercise_type', 'movement');
  const candidateActivity = structuredCandidate?.activity_state
    ?? stringField(candidateSignals, 'activity_state');
  const candidateFatigue = structuredCandidate?.fatigue_state
    ?? stringField(candidateSignals, 'fatigue_state');
  const start = playData?.source_kind === 'chunk'
    ? playData.media_start_secs ?? 0
    : playData?.media_start_secs ?? mediaStart(chunk);
  const end = playData?.source_kind === 'chunk'
    ? playData.media_end_secs ?? null
    : playData?.media_end_secs ?? mediaEnd(chunk);
  const playUrl = playData?.play_url ?? playData?.download_url;
  const hasActiveRun = runs.some(
    (run) => run.status === 'QUEUED' || run.status === 'RUNNING',
  );

  const playInterval = () => {
    if (!videoRef.current || start == null || !Number.isFinite(start)) return;
    videoRef.current.currentTime = start;
    void videoRef.current.play();
  };

  const handleTimeUpdate = () => {
    const video = videoRef.current;
    if (!video) return;
    if (start != null && Number.isFinite(start) && video.currentTime < start) {
      video.currentTime = start;
      return;
    }
    if (end != null && Number.isFinite(end) && video.currentTime >= end) {
      video.currentTime = end;
      video.pause();
    }
  };

  return (
    <section className="mt-6 rounded-xl border border-border bg-bg-elevated p-5" aria-labelledby="chunk-inspector-heading">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id="chunk-inspector-heading" className="text-lg font-semibold text-text-primary">
            Chunk Inspector
          </h2>
          <p className="mt-1 font-mono text-xs text-text-muted">
            Chunk {chunk.id} · {formatTime(start)}–{formatTime(end)} · {chunk.status}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            onClick={onCorrect}
            className="rounded-lg border border-border px-3 py-2 text-sm font-medium text-text-secondary hover:bg-bg-tertiary focus:outline-none focus:ring-2 focus:ring-accent"
          >
            {feedback ? 'Edit correction' : 'Correct result'}
          </button>
          <button
            type="button"
            onClick={onCreateRun}
            disabled={creatingRun || hasActiveRun}
            className="rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover focus:outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-bg-elevated disabled:cursor-not-allowed disabled:opacity-50"
          >
            {creatingRun ? 'Queueing…' : hasActiveRun ? 'Re-analysis running…' : 'Re-analyze chunk'}
          </button>
        </div>
      </div>
      <p className="mt-2 text-xs text-text-muted">
        Re-analysis runs the current AI analyzer and may incur usage cost. It does not change the original result.
      </p>

      {(creatingRun || hasActiveRun) && (
        <p aria-live="polite" className="mt-3 rounded-lg border border-warning/20 bg-warning/5 px-3 py-2 text-sm text-warning">
          {creatingRun ? 'Requesting a new analysis…' : 'The selected chunk is being re-analyzed. Results will update automatically.'}
        </p>
      )}
      <p aria-live="polite" className="sr-only">
        {sortedRuns[0] ? `Latest re-analysis status: ${sortedRuns[0].status.replaceAll('_', ' ')}` : ''}
      </p>
      {reanalysisError && (
        <p role="alert" className="mt-3 rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {reanalysisError}
        </p>
      )}

      <div className="mt-5 grid gap-5 xl:grid-cols-5">
        <div className="xl:col-span-3">
          <h3 className="mb-2 text-sm font-medium text-text-secondary">Exact video interval</h3>
          {playUrl ? (
            <div className="overflow-hidden rounded-lg border border-border bg-black">
              <video
                ref={videoRef}
                src={playUrl}
                controls
                onLoadedMetadata={() => {
                  if (videoRef.current && start != null && Number.isFinite(start)) {
                    videoRef.current.currentTime = start;
                  }
                }}
                onTimeUpdate={handleTimeUpdate}
                className="aspect-video w-full"
              />
              <div className="flex items-center justify-between gap-3 bg-bg-secondary px-3 py-2">
                <span className="text-xs text-text-muted">
                  {end == null
                    ? 'Playback uses the complete retained chunk.'
                    : `Playback stops at ${formatTime(end)}`}
                </span>
                <button
                  type="button"
                  onClick={playInterval}
                  className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary hover:bg-bg-tertiary focus:outline-none focus:ring-2 focus:ring-accent"
                >
                  {end == null ? 'Play chunk' : 'Play interval'}
                </button>
              </div>
            </div>
          ) : (
            <div className="flex aspect-video items-center justify-center rounded-lg border border-border bg-bg-secondary p-6 text-center text-sm text-text-muted">
              {playLoading
                ? 'Loading the session video…'
                : playError
                  ? 'The exact session-video interval is unavailable.'
                  : 'Video is unavailable.'}
            </div>
          )}
        </div>

        <div className="xl:col-span-2">
          <h3 className="mb-2 text-sm font-medium text-text-secondary">Signals and evidence</h3>
          <dl className="grid grid-cols-2 gap-3 rounded-lg border border-border bg-bg-secondary/60 p-3 text-xs">
            <div><dt className="text-text-muted">Heart rate</dt><dd className="mt-0.5 text-text-primary">{chunk.heart_rate_bpm > 0 ? `${chunk.heart_rate_bpm} bpm` : 'Unavailable'}</dd></div>
            <div><dt className="text-text-muted">Motion score</dt><dd className="mt-0.5 text-text-primary">{chunk.motion_score ?? 'Unavailable'}</dd></div>
            <div><dt className="text-text-muted">Activity</dt><dd className="mt-0.5 text-text-primary">{(originalStructured?.activity_state ?? stringField(originalSignals, 'activity_state'))?.replaceAll('_', ' ') ?? 'Unavailable'}</dd></div>
            <div><dt className="text-text-muted">Confidence</dt><dd className="mt-0.5 text-text-primary">{originalStructured?.movement_confidence ?? 'Unavailable'}</dd></div>
          </dl>
          {(originalStructured?.visible_evidence != null || originalSignals != null) && (
            <details className="mt-3 rounded-lg border border-border bg-bg-secondary/60 p-3">
              <summary className="cursor-pointer text-xs font-medium text-text-secondary">Structured evidence</summary>
              <pre className="mt-2 max-h-44 overflow-auto whitespace-pre-wrap text-xs text-text-muted">
                {JSON.stringify(originalStructured?.visible_evidence ?? originalSignals, null, 2)}
              </pre>
            </details>
          )}
        </div>
      </div>

      <div className="mt-6">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-sm font-medium text-text-secondary">Original, correction, and candidate</h3>
          {feedback && onUndoCorrection && (
            <button
              type="button"
              onClick={onUndoCorrection}
              className="text-xs font-medium text-error hover:underline focus:outline-none focus:ring-2 focus:ring-error"
            >
              Undo correction
            </button>
          )}
        </div>
        <div className="grid gap-3 lg:grid-cols-3">
          <ResultCard
            title="Original"
            movement={chunk.exercise_type ?? originalStructured?.observed_movement_name}
            activity={originalStructured?.activity_state ?? stringField(originalSignals, 'activity_state')}
            fatigue={originalStructured?.fatigue_state ?? stringField(originalSignals, 'fatigue_state')}
            output={chunk.output}
          />
          <ResultCard
            title="Corrected"
            badge={feedback ? 'Corrected by you' : undefined}
            movement={correctionMovement(feedback) ?? chunk.exercise_type}
            activity={correctionActivity(feedback) ?? originalStructured?.activity_state}
            fatigue={correctionFatigue(feedback) ?? originalStructured?.fatigue_state}
            output={feedback?.note || (feedback ? 'Original coaching text remains unchanged.' : 'No correction submitted.')}
          />
          <div>
            <ResultCard
              title="Candidate"
              movement={candidateMovement}
              activity={candidateActivity}
              fatigue={candidateFatigue}
              output={candidate?.output ?? (selectedRun ? `Status: ${selectedRun.status}` : 'No re-analysis attempt yet.')}
            />
            {candidate && selectedRunId != null && (
              <button
                type="button"
                onClick={() => onUseCandidate(candidateCorrection(candidate), selectedRunId)}
                className="mt-2 w-full rounded-lg border border-accent/40 px-3 py-2 text-sm font-medium text-accent hover:bg-accent/10 focus:outline-none focus:ring-2 focus:ring-accent"
              >
                Use candidate as correction
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="mt-6">
        <h3 className="text-sm font-medium text-text-secondary">Re-analysis attempts</h3>
        {runsLoading ? (
          <p className="mt-2 text-sm text-text-muted">Loading attempts…</p>
        ) : sortedRuns.length === 0 ? (
          <p className="mt-2 text-sm text-text-muted">No attempts for this chunk.</p>
        ) : (
          <div className="mt-2 overflow-x-auto rounded-lg border border-border">
            <table className="w-full min-w-[760px] text-left text-xs">
              <thead className="bg-bg-secondary text-text-muted">
                <tr>
                  <th className="px-3 py-2 font-medium">Attempt</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Analyzer</th>
                  <th className="px-3 py-2 font-medium">Tokens</th>
                  <th className="px-3 py-2 font-medium">Duration</th>
                  <th className="px-3 py-2 font-medium">Created</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {sortedRuns.map((run) => {
                  const id = runId(run);
                  const totalTokens = run.token_usage?.total_tokens
                    ?? ((run.input_tokens ?? 0) + (run.output_tokens ?? 0));
                  return (
                    <tr key={id ?? `${run.created_at}-${run.status}`} className={id === selectedRunId ? 'bg-accent/5' : 'bg-bg-elevated'}>
                      <td className="px-3 py-2">
                        {id != null ? (
                          <button
                            type="button"
                            onClick={() => setExplicitRunId(id)}
                            aria-pressed={id === selectedRunId}
                            className="font-mono text-accent hover:underline focus:outline-none focus:ring-2 focus:ring-accent"
                          >
                            #{id}
                          </button>
                        ) : '—'}
                      </td>
                      <td className="px-3 py-2">
                        <span className={`rounded border px-1.5 py-0.5 font-medium ${statusClasses(run.status)}`}>
                          {run.status.replaceAll('_', ' ')}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-text-secondary">{run.model ?? 'Current'}{run.prompt_version ? ` · ${run.prompt_version}` : ''}</td>
                      <td className="px-3 py-2 font-mono text-text-secondary">{totalTokens || '—'}</td>
                      <td className="px-3 py-2 font-mono text-text-secondary">{run.duration_ms != null ? `${run.duration_ms} ms` : '—'}</td>
                      <td className="px-3 py-2 text-text-secondary">{formatDate(run.created_at)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {selectedRun?.error && (
          <p role="alert" className="mt-3 rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
            {selectedRun.error}
          </p>
        )}
      </div>
    </section>
  );
}
