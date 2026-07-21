import { useEffect, useRef, useState } from 'react';
import type {
  AnalysisFeedback,
  ChunkAnalysisResult,
} from '../../api/history';

interface Props {
  chunks: ChunkAnalysisResult[];
  currentTime: number;
  selectedChunkId?: number;
  feedbackByChunk: Map<number, AnalysisFeedback>;
  selectedReanalysisChunkIds: ReadonlySet<number>;
  bulkRunStatusByChunk: ReadonlyMap<number, string>;
  reanalysisBusy: boolean;
  reanalysisSelectionLimitReached: boolean;
  onSeek: (time: number) => void;
  onInspect: (chunk: ChunkAnalysisResult) => void;
  onCorrect: (chunk: ChunkAnalysisResult) => void;
  onReanalyze: (chunk: ChunkAnalysisResult) => void;
  onToggleReanalysisSelection: (chunkId: number) => void;
}

const CHUNK_TIMELINE_PREVIEW_COUNT = 1;

function chunkStart(chunk: ChunkAnalysisResult) {
  return chunk.media_start_secs ?? null;
}

function chunkEnd(chunk: ChunkAnalysisResult) {
  return chunk.media_end_secs ?? null;
}

function formatTime(secs: number | null) {
  if (secs == null || !Number.isFinite(secs) || secs < 0) return '—';
  const m = Math.floor(secs / 60);
  const s = Math.floor(secs % 60);
  return `${m}:${s.toString().padStart(2, '0')}`;
}

function coachingText(output: string) {
  try {
    const parsed = JSON.parse(output) as Record<string, unknown>;
    const coaching = parsed.coaching_feedback ?? parsed.feedback;
    return typeof coaching === 'string' ? coaching : output;
  } catch {
    return output;
  }
}

function HeartRateBadge({ bpm }: { bpm: number }) {
  if (bpm <= 0) return null;
  const color = bpm > 160
    ? 'text-error bg-error/10'
    : bpm > 130
      ? 'text-warning bg-warning/10'
      : 'text-success bg-success/10';
  return (
    <span className={`inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-xs ${color}`}>
      <svg className="h-3 w-3" fill="currentColor" viewBox="0 0 20 20" aria-hidden="true">
        <path d="M3.172 5.172a4 4 0 015.656 0L10 6.343l1.172-1.171a4 4 0 115.656 5.656L10 17.657l-6.828-6.829a4 4 0 010-5.656z" />
      </svg>
      {bpm}
      <span className="sr-only">beats per minute</span>
    </span>
  );
}

function StatusBadge({ status }: { status: string }) {
  const normalized = status.toUpperCase();
  const classes = normalized === 'COMPLETED'
    ? 'bg-success/10 text-success'
    : normalized === 'FAILED'
      ? 'bg-error/10 text-error'
      : 'bg-warning/10 text-warning';
  return (
    <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${classes}`}>
      {normalized.replaceAll('_', ' ')}
    </span>
  );
}

function correctedLabel(feedback: AnalysisFeedback) {
  const correction = feedback.correction;
  if (correction?.movement_name) return correction.movement_name;
  if (correction?.activity_state) return correction.activity_state.replaceAll('_', ' ');
  if (feedback.corrected_movement) return feedback.corrected_movement;
  if (feedback.corrected_activity_state) {
    return feedback.corrected_activity_state.replaceAll('_', ' ');
  }
  return 'Correction saved';
}

function bulkStatusClasses(status: string) {
  if (status === 'COMPLETED') return 'border-success/20 bg-success/5 text-success';
  if (status === 'QUEUED' || status === 'RUNNING') {
    return 'border-warning/20 bg-warning/5 text-warning';
  }
  return 'border-error/20 bg-error/5 text-error';
}

export function GuidanceTimeline({
  chunks,
  currentTime,
  selectedChunkId,
  feedbackByChunk,
  selectedReanalysisChunkIds,
  bulkRunStatusByChunk,
  reanalysisBusy,
  reanalysisSelectionLimitReached,
  onSeek,
  onInspect,
  onCorrect,
  onReanalyze,
  onToggleReanalysisSelection,
}: Props) {
  const sorted = [...chunks].sort((a, b) => {
    const aStart = chunkStart(a);
    const bStart = chunkStart(b);
    if (aStart == null) return bStart == null ? a.id - b.id : 1;
    if (bStart == null) return -1;
    return aStart - bStart;
  });
  const [showAllChunks, setShowAllChunks] = useState(false);
  const activeIndex = sorted.findIndex((chunk) => {
    const start = chunkStart(chunk);
    const end = chunkEnd(chunk);
    return start != null && end != null && currentTime >= start && currentTime < end;
  });
  const activeChunkId = sorted[activeIndex]?.id;
  const previewChunk = sorted.find((chunk) => chunk.id === activeChunkId)
    ?? sorted.find((chunk) => chunk.id === selectedChunkId)
    ?? sorted[0];
  const visibleChunks = showAllChunks
    ? sorted
    : previewChunk
      ? [previewChunk]
      : [];
  const activeRef = useRef<HTMLLIElement | null>(null);
  const containerRef = useRef<HTMLOListElement | null>(null);

  useEffect(() => {
    if (!activeRef.current || !containerRef.current) return;
    const containerRect = containerRef.current.getBoundingClientRect();
    const activeRect = activeRef.current.getBoundingClientRect();
    if (activeRect.top < containerRect.top || activeRect.bottom > containerRect.bottom) {
      activeRef.current.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
  }, [activeIndex, showAllChunks]);

  return (
    <section className="rounded-xl border border-border bg-bg-elevated p-5" aria-labelledby="chunk-timeline-heading">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h2 id="chunk-timeline-heading" className="text-lg font-semibold text-text-primary">
            Chunk Timeline
          </h2>
          <p className="mt-0.5 text-xs text-text-muted">
            {sorted.length} received {sorted.length === 1 ? 'chunk' : 'chunks'}
          </p>
        </div>
        {sorted.length > 0 && (
          <span className="shrink-0 rounded-md bg-bg-tertiary px-2 py-1 text-xs font-medium text-text-secondary">
            {visibleChunks.length}/{sorted.length}
          </span>
        )}
      </div>

      {sorted.length === 0 ? (
        <p className="text-sm text-text-muted">No chunk analysis data is available.</p>
      ) : (
        <ol
          id="chunk-timeline-list"
          ref={containerRef}
          className="max-h-[620px] space-y-2 overflow-y-auto pr-1 scroll-smooth"
          style={{ scrollbarWidth: 'thin', scrollbarColor: '#3f3f46 transparent' }}
        >
          {visibleChunks.map((chunk) => {
            const isActive = chunk.id === activeChunkId;
            const isSelected = chunk.id === selectedChunkId;
            const isSelectedForReanalysis = selectedReanalysisChunkIds.has(chunk.id);
            const feedback = feedbackByChunk.get(chunk.id);
            const bulkRunStatus = bulkRunStatusByChunk.get(chunk.id);
            const movement = chunk.exercise_type
              ?? chunk.structured_inference?.observed_movement_name
              ?? null;
            const coaching = coachingText(chunk.output || '');
            const start = chunkStart(chunk);
            const end = chunkEnd(chunk);
            const canSeek = start != null && Number.isFinite(start) && start >= 0;

            return (
              <li
                key={chunk.id}
                ref={isActive ? activeRef : null}
                className={`rounded-lg border p-3 transition-colors ${
                  isSelected
                    ? 'border-accent/60 bg-accent/10'
                    : isActive
                      ? 'border-accent/30 bg-accent/5'
                      : 'border-transparent bg-bg-secondary/60'
                }`}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <label className="inline-flex cursor-pointer items-center gap-1.5 rounded px-1 py-0.5 text-xs font-medium text-text-secondary focus-within:ring-2 focus-within:ring-accent">
                    <input
                      type="checkbox"
                      checked={isSelectedForReanalysis}
                      onChange={() => onToggleReanalysisSelection(chunk.id)}
                      disabled={reanalysisBusy || (!isSelectedForReanalysis && reanalysisSelectionLimitReached)}
                      className="h-3.5 w-3.5 accent-accent disabled:cursor-not-allowed"
                    />
                    Select
                    <span className="sr-only">chunk {chunk.id} for bulk re-analysis</span>
                  </label>
                  <span className="font-mono text-xs text-text-muted">
                    {formatTime(start)}–{formatTime(end)}
                  </span>
                  <StatusBadge status={chunk.status} />
                  {movement && (
                    <span className="rounded bg-bg-tertiary px-1.5 py-0.5 text-xs font-medium text-text-secondary">
                      {movement}
                    </span>
                  )}
                  <div className="ml-auto"><HeartRateBadge bpm={chunk.heart_rate_bpm || 0} /></div>
                </div>

                {feedback && (
                  <p className="mt-2 rounded-md border border-success/20 bg-success/5 px-2 py-1.5 text-xs text-success">
                    Corrected by you: {correctedLabel(feedback)}
                  </p>
                )}

                {bulkRunStatus && (
                  <p className={`mt-2 rounded-md border px-2 py-1.5 text-xs ${bulkStatusClasses(bulkRunStatus)}`}>
                    Bulk re-analysis: {bulkRunStatus.replaceAll('_', ' ').toLowerCase()}
                  </p>
                )}

                {coaching && (
                  <p className="mt-2 line-clamp-2 text-xs leading-relaxed text-text-muted">
                    {coaching}
                  </p>
                )}

                <div className="mt-3 flex flex-wrap gap-2">
                  {canSeek ? (
                    <button
                      type="button"
                      onClick={() => {
                        if (start != null) onSeek(start);
                      }}
                      className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
                    >
                      Seek
                    </button>
                  ) : chunk.start_secs != null && Number.isFinite(chunk.start_secs) && chunk.start_secs >= 0 ? (
                    <button
                      type="button"
                      onClick={() => {
                        if (chunk.start_secs != null) onSeek(chunk.start_secs);
                      }}
                      title="Jump using capture-clock start time (may be inaccurate for legacy concatenated videos)"
                      className="rounded-md border border-warning/30 bg-warning/5 px-2.5 py-1.5 text-xs font-medium text-warning hover:bg-warning/10 focus:outline-none focus:ring-2 focus:ring-warning"
                    >
                      Jump
                    </button>
                  ) : (
                    <button
                      type="button"
                      disabled
                      className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary opacity-40 cursor-not-allowed"
                    >
                      Seek
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => onInspect(chunk)}
                    aria-pressed={isSelected}
                    className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
                  >
                    Inspect
                  </button>
                  <button
                    type="button"
                    onClick={() => onCorrect(chunk)}
                    className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary focus:outline-none focus:ring-2 focus:ring-accent"
                  >
                    {feedback ? 'Edit correction' : 'Correct'}
                  </button>
                  <button
                    type="button"
                    onClick={() => onReanalyze(chunk)}
                    disabled={reanalysisBusy}
                    className="rounded-md border border-accent/40 px-2.5 py-1.5 text-xs font-medium text-accent hover:bg-accent/10 focus:outline-none focus:ring-2 focus:ring-accent disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    Re-analyze
                  </button>
                </div>
              </li>
            );
          })}
        </ol>
      )}

      {sorted.length > CHUNK_TIMELINE_PREVIEW_COUNT && (
        <button
          type="button"
          onClick={() => setShowAllChunks((current) => !current)}
          aria-expanded={showAllChunks}
          aria-controls="chunk-timeline-list"
          className="mt-3 w-full rounded-lg border border-border px-3 py-2 text-sm font-semibold text-accent transition-colors hover:border-accent/40 hover:bg-accent/10 focus:outline-none focus:ring-2 focus:ring-accent"
        >
          {showAllChunks ? 'Show Less' : 'Show More'}
        </button>
      )}
    </section>
  );
}
