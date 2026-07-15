import { useParams, Link } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useMemo, useRef, useState } from 'react';
import {
  historyApi,
  type AnalysisFeedback,
  type ChunkAnalysisResult,
  type FeedbackCorrection,
  type FeedbackListResponse,
  type ReanalysisListResponse,
  type VideoKind,
} from '../api/history';
import { useAuth } from '../auth/useAuth';
import { ChunkMetricsChart } from './components/ChunkMetricsChart';
import { GuidanceTimeline } from './components/GuidanceTimeline';
import {
  FeedbackDialog,
  type FeedbackDialogValue,
} from './components/FeedbackDialog';
import { ChunkInspector } from './components/ChunkInspector';

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

const VIDEO_KIND_LABELS: Record<VideoKind, { label: string; icon: string; desc: string }> = {
  merged: { label: 'Original', icon: '📹', desc: 'Raw workout video' },
  hardsubbed: { label: 'Guided', icon: '📝', desc: 'With AI coaching overlay' },
  encoded: { label: 'Encoded', icon: '🎬', desc: 'Compressed version' },
};

interface FeedbackDialogState {
  targetType: 'session' | 'chunk';
  chunk?: ChunkAnalysisResult;
  existing?: AnalysisFeedback;
  preset?: FeedbackCorrection;
  reanalysisRunId?: number;
}

function normalizeFeedback(response?: FeedbackListResponse) {
  if (!response) return [];
  if (Array.isArray(response)) {
    return response.filter((feedback) => !feedback.retracted && !feedback.retracted_at);
  }
  return response.current ?? [];
}

function normalizeRuns(response?: ReanalysisListResponse) {
  if (!response) return [];
  return Array.isArray(response) ? response : response.runs;
}

function feedbackChunkId(feedback: AnalysisFeedback) {
  return feedback.chunk_id ?? feedback.target_id ?? undefined;
}

function feedbackError(error: unknown) {
  return error instanceof Error ? error.message : 'Unable to save feedback.';
}

export function SessionDetailPage() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const { user } = useAuth();
  const queryClient = useQueryClient();

  const videoRef = useRef<HTMLVideoElement>(null);
  const inspectorRef = useRef<HTMLDivElement>(null);
  const [currentTime, setCurrentTime] = useState(0);
  const [selectedKind, setSelectedKind] = useState<VideoKind>();
  const [showFullAnalysis, setShowFullAnalysis] = useState(false);
  const [selectedChunkId, setSelectedChunkId] = useState<number>();
  const [dialog, setDialog] = useState<FeedbackDialogState>();
  const closeFeedbackDialog = useCallback(() => setDialog(undefined), []);

  const { data: analyses, isLoading: analysisLoading } = useQuery({
    queryKey: ['analysis', sessionId],
    queryFn: () => historyApi.getAnalysis(sessionId!),
    enabled: !!sessionId,
  });

  const { data: sessionAnalysis } = useQuery({
    queryKey: ['session-analysis', sessionId],
    queryFn: () => historyApi.getSessionAnalysis(sessionId!),
    enabled: !!sessionId,
    retry: false,
  });

  const analysis = sessionAnalysis?.analysis ?? analyses?.[0];

  const { data: legacyChunks } = useQuery({
    queryKey: ['chunks', sessionId],
    queryFn: () => historyApi.getChunkAnalysis(sessionId!),
    enabled: !!sessionId,
  });

  const chunks = useMemo(() => {
    const byId = new Map<number, ChunkAnalysisResult>();
    for (const chunk of legacyChunks ?? []) byId.set(chunk.id, chunk);
    for (const chunk of sessionAnalysis?.chunks ?? []) {
      byId.set(chunk.id, { ...byId.get(chunk.id), ...chunk });
    }
    return [...byId.values()];
  }, [legacyChunks, sessionAnalysis?.chunks]);
  const profileId = analysis?.profile_id
    ?? chunks[0]?.profile_id
    ?? user?.profiles?.[0]?.id;

  const { data: feedbackResponse } = useQuery({
    queryKey: ['session-feedback', sessionId],
    queryFn: () => historyApi.listFeedback(sessionId!),
    enabled: !!sessionId,
    retry: false,
  });

  const { data: movementSuggestions = [] } = useQuery({
    queryKey: ['movement-suggestions'],
    queryFn: historyApi.getMovementSuggestions,
    retry: false,
  });

  const currentFeedback = useMemo(() => {
    if (feedbackResponse !== undefined) return normalizeFeedback(feedbackResponse);
    return (sessionAnalysis?.feedback ?? []).filter(
      (feedback) => !feedback.retracted && !feedback.retracted_at,
    );
  }, [feedbackResponse, sessionAnalysis?.feedback]);

  const feedbackByChunk = useMemo(() => {
    const result = new Map<number, AnalysisFeedback>();
    for (const feedback of currentFeedback) {
      if (feedback.target_type !== 'chunk') continue;
      const chunkId = feedbackChunkId(feedback);
      if (chunkId == null) continue;
      const current = result.get(chunkId);
      if (!current || Date.parse(feedback.created_at) >= Date.parse(current.created_at)) {
        result.set(chunkId, feedback);
      }
    }
    return result;
  }, [currentFeedback]);

  const sessionFeedback = currentFeedback.find(
    (feedback) => feedback.target_type === 'session' && feedback.category === 'session_accuracy',
  );
  const hasChunkCorrections = feedbackByChunk.size > 0;
  const selectedChunk = chunks.find((chunk) => chunk.id === selectedChunkId)
    ?? chunks[0];

  const { data: runResponse, isLoading: runsLoading } = useQuery({
    queryKey: ['chunk-reanalyses', sessionId, selectedChunk?.id],
    queryFn: () => historyApi.listChunkReanalyses(sessionId!, selectedChunk!.id),
    enabled: !!sessionId && !!selectedChunk,
    retry: false,
    refetchInterval: (query) => {
      const runs = normalizeRuns(query.state.data as ReanalysisListResponse | undefined);
      if (!runs.some((run) => run.status === 'QUEUED' || run.status === 'RUNNING')) {
        return false;
      }
      const pollCount = Math.min(query.state.dataUpdateCount, 4);
      return Math.min(1000 * (2 ** pollCount), 8000);
    },
  });
  const runs = normalizeRuns(runResponse);

  const invalidateFeedback = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['session-feedback', sessionId] }),
      queryClient.invalidateQueries({ queryKey: ['session-analysis', sessionId] }),
    ]);
  };

  const feedbackMutation = useMutation({
    mutationFn: async ({
      state,
      value,
    }: {
      state: FeedbackDialogState;
      value: FeedbackDialogValue;
    }) => {
      if (state.existing) {
        return historyApi.updateFeedback(sessionId!, state.existing.id, {
          client_request_id: crypto.randomUUID(),
          expected_revision: state.existing.revision,
          category: value.category,
          correction: value.correction,
          note: value.note || undefined,
          consent_to_improve: value.consent_to_improve,
          reanalysis_run_id: state.reanalysisRunId,
        });
      }
      return historyApi.createFeedback(sessionId!, {
        client_request_id: crypto.randomUUID(),
        target_type: state.targetType,
        chunk_id: state.chunk?.id,
        category: value.category,
        correction: value.correction,
        note: value.note || undefined,
        consent_to_improve: value.consent_to_improve,
        reanalysis_run_id: state.reanalysisRunId,
      });
    },
    onSuccess: invalidateFeedback,
  });

  const undoFeedbackMutation = useMutation({
    mutationFn: (feedback: AnalysisFeedback) => historyApi.deleteFeedback(
      sessionId!,
      feedback.id,
      feedback.revision,
      crypto.randomUUID(),
    ),
    onSuccess: invalidateFeedback,
  });

  const reanalysisMutation = useMutation({
    mutationFn: (chunk: ChunkAnalysisResult) => historyApi.createChunkReanalysis(
      sessionId!,
      chunk.id,
      crypto.randomUUID(),
    ),
    onSuccess: async (_result, chunk) => {
      await queryClient.invalidateQueries({
        queryKey: ['chunk-reanalyses', sessionId, chunk.id],
      });
    },
  });

  const openCorrection = (chunk: ChunkAnalysisResult) => {
    feedbackMutation.reset();
    undoFeedbackMutation.reset();
    setDialog({
      targetType: 'chunk',
      chunk,
      existing: feedbackByChunk.get(chunk.id),
    });
  };

  const openSessionMistake = () => {
    feedbackMutation.reset();
    undoFeedbackMutation.reset();
    setDialog({ targetType: 'session', existing: sessionFeedback });
  };

  const submitFeedback = async (value: FeedbackDialogValue) => {
    if (!dialog) return;
    await feedbackMutation.mutateAsync({ state: dialog, value });
    setDialog(undefined);
  };

  const undoFeedback = async (feedback: AnalysisFeedback) => {
    if (!window.confirm('Undo this correction? The original analysis will remain unchanged.')) return;
    try {
      await undoFeedbackMutation.mutateAsync(feedback);
      setDialog(undefined);
    } catch {
      // Mutation state keeps the error visible in the current surface.
    }
  };

  const markSessionAccurate = async () => {
    feedbackMutation.reset();
    try {
      await feedbackMutation.mutateAsync({
        state: { targetType: 'session', existing: sessionFeedback },
        value: {
          category: 'session_accuracy',
          correction: { accurate: true },
          note: '',
          consent_to_improve: sessionFeedback?.consent_to_improve ?? false,
        },
      });
    } catch {
      // Mutation state renders the request error next to the session controls.
    }
  };

  const showInspector = (chunk: ChunkAnalysisResult) => {
    setSelectedChunkId(chunk.id);
    window.setTimeout(() => {
      inspectorRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }, 0);
  };

  const requestReanalysis = (chunk: ChunkAnalysisResult) => {
    showInspector(chunk);
    const confirmed = window.confirm(
      'Re-analyze this exact video interval with the current AI analyzer? This incurs an AI call and will not change the original result.',
    );
    if (confirmed) reanalysisMutation.mutate(chunk);
  };

  const openCandidateCorrection = (
    chunk: ChunkAnalysisResult,
    correction: FeedbackCorrection,
    reanalysisRunId: number,
  ) => {
    feedbackMutation.reset();
    setDialog({
      targetType: 'chunk',
      chunk,
      existing: feedbackByChunk.get(chunk.id),
      preset: correction,
      reanalysisRunId,
    });
  };

  // Fetch available videos from history to know which kinds exist
  const { data: historyList } = useQuery({
    queryKey: ['history', profileId],
    queryFn: () => historyApi.list(profileId!),
    enabled: !!profileId,
  });

  const historyItem = historyList?.find((h) => h.session_id === sessionId);
  const availableVideos = (historyItem?.available_videos ?? []) as VideoKind[];
  const effectiveVideoKind = selectedKind && availableVideos.includes(selectedKind)
    ? selectedKind
    : availableVideos.includes('hardsubbed')
      ? 'hardsubbed'
      : availableVideos.includes('merged')
        ? 'merged'
        : availableVideos.includes('encoded')
          ? 'encoded'
          : 'merged';

  const { data: videoUrl } = useQuery({
    queryKey: ['video-url', sessionId, effectiveVideoKind],
    queryFn: () =>
      historyApi.getVideoDownloadUrl(sessionId!, profileId!, effectiveVideoKind),
    enabled: !!sessionId && !!profileId && availableVideos.includes(effectiveVideoKind),
    staleTime: 10 * 60 * 1000, // 10 min (signed URLs expire)
  });

  const parsedOutput = (() => {
    try {
      return analysis?.output ? JSON.parse(analysis.output) : null;
    } catch {
      return null;
    }
  })();

  // Track video playback position
  const handleTimeUpdate = useCallback(() => {
    if (videoRef.current) {
      setCurrentTime(videoRef.current.currentTime);
    }
  }, []);

  // Seek video to a specific time
  const handleSeek = useCallback((time: number) => {
    if (videoRef.current) {
      videoRef.current.currentTime = time;
      videoRef.current.play().catch(() => {});
    }
  }, []);

  if (analysisLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="w-8 h-8 border-2 border-accent border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  return (
    <div className="max-w-7xl mx-auto">
      <div className="flex items-center gap-3 mb-6">
        <Link
          to="/"
          className="text-text-muted hover:text-text-primary transition-colors"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5 8.25 12l7.5-7.5" />
          </svg>
        </Link>
        <h1 className="text-xl font-bold text-text-primary">Session Detail</h1>
      </div>

      {/* Side-by-side layout: Video + Guidance */}
      <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
        {/* Left: Video player (3/5 width) */}
        <div className="lg:col-span-3 space-y-4">
          <div className="bg-bg-elevated border border-border rounded-xl overflow-hidden">
            {videoUrl?.download_url ? (
              <video
                ref={videoRef}
                src={videoUrl.download_url}
                controls
                onTimeUpdate={handleTimeUpdate}
                className="w-full aspect-video bg-black"
              />
            ) : (
              <div className="w-full aspect-video bg-bg-secondary flex items-center justify-center">
                <div className="text-center">
                  <svg className="w-12 h-12 text-text-muted mx-auto mb-2" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
                  </svg>
                  <p className="text-text-muted text-sm">
                    {availableVideos.length === 0
                      ? 'Video not available'
                      : 'Loading video...'}
                  </p>
                </div>
              </div>
            )}
          </div>

          {/* Video kind selector */}
          {availableVideos.length > 1 && (
            <div className="flex gap-2">
              {availableVideos.map((kind) => {
                const cfg = VIDEO_KIND_LABELS[kind];
                const isSelected = kind === effectiveVideoKind;
                return (
                  <button
                    key={kind}
                    onClick={() => setSelectedKind(kind)}
                    className={`
                      flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm font-medium
                      transition-all duration-200 border
                      ${
                        isSelected
                          ? 'bg-accent/15 border-accent/40 text-accent'
                          : 'bg-bg-secondary border-transparent text-text-secondary hover:text-text-primary hover:bg-bg-tertiary'
                      }
                    `}
                    title={cfg.desc}
                  >
                    <span>{cfg.icon}</span>
                    {cfg.label}
                  </button>
                );
              })}
            </div>
          )}

          {/* Session metadata */}
          {analysis && (
            <div className="bg-bg-elevated border border-border rounded-xl p-4">
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div>
                  <p className="text-text-muted">Session ID</p>
                  <p className="text-text-primary font-mono text-xs mt-0.5 break-all">{analysis.session_id}</p>
                </div>
                <div>
                  <p className="text-text-muted">Status</p>
                  <p className="text-text-primary mt-0.5 capitalize">{analysis.status}</p>
                </div>
                <div>
                  <p className="text-text-muted">Created</p>
                  <p className="text-text-primary mt-0.5">{formatDate(analysis.created_at)}</p>
                </div>
                <div>
                  <p className="text-text-muted">Workout Type</p>
                  <p className="text-text-primary mt-0.5 capitalize">{analysis.workout_type || '—'}</p>
                </div>
                {sessionAnalysis?.coverage_status && (
                  <div>
                    <p className="text-text-muted">Media coverage</p>
                    <p className="text-text-primary mt-0.5 capitalize">
                      {sessionAnalysis.coverage_status.replaceAll('_', ' ')}
                    </p>
                  </div>
                )}
                {analysis.wod_description && (
                  <div className="col-span-2 border-t border-border pt-3 mt-1">
                    <p className="text-text-muted">WOD Description</p>
                    <p className="text-text-primary text-sm mt-1 whitespace-pre-wrap bg-bg-secondary p-3 rounded-lg border border-border">
                      {analysis.wod_description}
                    </p>
                  </div>
                )}
                {sessionAnalysis?.additional_observed_movements && sessionAnalysis.additional_observed_movements.length > 0 && (
                  <div className="col-span-2 border-t border-border pt-3 mt-1">
                    <p className="text-text-muted">Additional observed movements</p>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {sessionAnalysis.additional_observed_movements.map((movement) => (
                        <span key={movement} className="rounded-md bg-info/10 px-2 py-1 text-xs text-info">
                          {movement}
                        </span>
                      ))}
                    </div>
                    <p className="mt-2 text-xs text-text-muted">
                      Video observations are shown separately and do not change the entered WOD.
                    </p>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* AI Analysis */}
          <div className="bg-bg-elevated border border-border rounded-xl p-5">
            <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
              <h2 className="text-lg font-semibold text-text-primary">AI Analysis</h2>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => void markSessionAccurate()}
                  disabled={feedbackMutation.isPending}
                  className="rounded-lg border border-success/30 px-3 py-1.5 text-xs font-medium text-success hover:bg-success/10 focus:outline-none focus:ring-2 focus:ring-success disabled:opacity-50"
                >
                  Accurate
                </button>
                <button
                  type="button"
                  onClick={openSessionMistake}
                  disabled={feedbackMutation.isPending}
                  className="rounded-lg border border-border px-3 py-1.5 text-xs font-medium text-text-secondary hover:bg-bg-tertiary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
                >
                  Report a mistake
                </button>
              </div>
            </div>
            {hasChunkCorrections && (
              <div className="mb-4 rounded-lg border border-warning/30 bg-warning/5 px-3 py-2 text-sm text-warning">
                Generated before your corrections. The original session summary has not been regenerated.
              </div>
            )}
            {sessionFeedback && (
              <div className="mb-4 flex items-center justify-between gap-3 rounded-lg border border-success/20 bg-success/5 px-3 py-2 text-xs text-success">
                <p>
                  {sessionFeedback.correction?.accurate
                    ? 'You marked this session analysis as accurate.'
                    : 'Your session feedback was saved.'}
                </p>
                <button
                  type="button"
                  onClick={() => void undoFeedback(sessionFeedback)}
                  disabled={undoFeedbackMutation.isPending}
                  className="shrink-0 font-medium text-text-secondary hover:text-text-primary hover:underline focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
                >
                  Clear
                </button>
              </div>
            )}
            {feedbackMutation.isError && !dialog && (
              <p role="alert" className="mb-4 rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                {feedbackError(feedbackMutation.error)}
              </p>
            )}
            {parsedOutput ? (
              <div className="space-y-4">
                {parsedOutput.overall_summary && (
                  <div>
                    <h3 className="text-sm font-medium text-text-secondary mb-1">Summary</h3>
                    <p className="text-text-primary text-sm leading-relaxed whitespace-pre-wrap">
                      {parsedOutput.overall_summary}
                    </p>
                  </div>
                )}
                {parsedOutput.coaching_feedback && (
                  <div>
                    <h3 className="text-sm font-medium text-text-secondary mb-1">Coaching Feedback</h3>
                    <p className="text-text-primary text-sm leading-relaxed whitespace-pre-wrap">
                      {parsedOutput.coaching_feedback}
                    </p>
                  </div>
                )}
                {parsedOutput.key_observations && (
                  <div>
                    <h3 className="text-sm font-medium text-text-secondary mb-1">Key Observations</h3>
                    <ul className="text-text-primary text-sm space-y-1">
                      {(Array.isArray(parsedOutput.key_observations) ? parsedOutput.key_observations : []).map(
                        (obs: string, i: number) => (
                          <li key={i} className="flex gap-2">
                            <span className="text-accent mt-1">•</span>
                            <span>{obs}</span>
                          </li>
                        ),
                      )}
                    </ul>
                  </div>
                )}
              </div>
            ) : analysis?.output ? (() => {
              const lines = analysis.output.split('\n');
              const isTruncated = lines.length > 10;
              const displayedText = showFullAnalysis || !isTruncated
                ? analysis.output
                : lines.slice(0, 10).join('\n') + '\n...';
              return (
                <div className="bg-bg-secondary p-4 rounded-xl border border-border">
                  <div className="text-text-primary text-sm leading-relaxed whitespace-pre-wrap font-sans">
                    {displayedText}
                  </div>
                  {isTruncated && (
                    <button
                      onClick={() => setShowFullAnalysis(!showFullAnalysis)}
                      className="mt-3 text-sm font-semibold text-accent hover:underline transition-colors cursor-pointer"
                    >
                      {showFullAnalysis ? 'Show Less' : 'Show More'}
                    </button>
                  )}
                </div>
              );
            })() : analysis?.status?.toLowerCase() === 'completed' ? (
              <p className="text-text-muted text-sm">Analysis output not available.</p>
            ) : analysis?.status?.toLowerCase() === 'processing' || analysis?.status?.toLowerCase() === 'pending' ? (
              <div className="flex items-center gap-3 text-text-secondary">
                <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
                <span className="text-sm">Analysis in progress...</span>
              </div>
            ) : (
              <p className="text-text-muted text-sm">No analysis data.</p>
            )}
          </div>
        </div>

        {/* Right: Guidance Timeline (2/5 width) */}
        <div className="lg:col-span-2">
          <div className="lg:sticky lg:top-4">
            <GuidanceTimeline
              chunks={chunks}
              currentTime={currentTime}
              selectedChunkId={selectedChunk?.id}
              feedbackByChunk={feedbackByChunk}
              onSeek={handleSeek}
              onInspect={showInspector}
              onCorrect={openCorrection}
              onReanalyze={requestReanalysis}
            />
          </div>
        </div>
      </div>

      {selectedChunk && (
        <div ref={inspectorRef}>
          <ChunkInspector
            key={selectedChunk.id}
            sessionId={sessionId!}
            chunk={selectedChunk}
            feedback={feedbackByChunk.get(selectedChunk.id)}
            runs={runs}
            runsLoading={runsLoading}
            creatingRun={reanalysisMutation.isPending}
            reanalysisError={reanalysisMutation.isError
              ? feedbackError(reanalysisMutation.error)
              : undefined}
            onCreateRun={() => requestReanalysis(selectedChunk)}
            onCorrect={() => openCorrection(selectedChunk)}
            onUndoCorrection={feedbackByChunk.has(selectedChunk.id)
              ? () => void undoFeedback(feedbackByChunk.get(selectedChunk.id)!)
              : undefined}
            onUseCandidate={(correction, runId) => openCandidateCorrection(selectedChunk, correction, runId)}
          />
        </div>
      )}

      {/* Chunk metrics chart — full width below */}
      {chunks.length > 0 && (
        <div className="mt-6">
          <ChunkMetricsChart
            chunks={chunks}
            currentTime={currentTime}
            onSeek={handleSeek}
          />
        </div>
      )}

      {dialog && (
        <FeedbackDialog
          targetType={dialog.targetType}
          chunkLabel={dialog.chunk
            ? `chunk ${dialog.chunk.id} (${dialog.chunk.media_start_secs ?? 'unknown'}s–${dialog.chunk.media_end_secs ?? 'unknown'}s)`
            : undefined}
          movementSuggestions={movementSuggestions}
          existing={dialog.existing}
          preset={dialog.preset}
          pending={feedbackMutation.isPending || undoFeedbackMutation.isPending}
          error={feedbackMutation.isError
            ? feedbackError(feedbackMutation.error)
            : undoFeedbackMutation.isError
              ? feedbackError(undoFeedbackMutation.error)
              : undefined}
          onClose={closeFeedbackDialog}
          onSubmit={submitFeedback}
          onUndo={dialog.existing
            ? () => undoFeedback(dialog.existing!)
            : undefined}
        />
      )}
    </div>
  );
}
