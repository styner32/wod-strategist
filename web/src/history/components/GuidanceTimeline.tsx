import { useEffect, useRef } from 'react';
import type {
  AnalysisFeedback,
  ChunkAnalysisResult,
} from '../../api/history';

interface Props {
  chunks: ChunkAnalysisResult[];
  currentTime: number;
  selectedChunkId?: number;
  feedbackByChunk: Map<number, AnalysisFeedback>;
  onSeek: (time: number) => void;
  onInspect: (chunk: ChunkAnalysisResult) => void;
  onCorrect: (chunk: ChunkAnalysisResult) => void;
  onReanalyze: (chunk: ChunkAnalysisResult) => void;
}

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

export function GuidanceTimeline({
  chunks,
  currentTime,
  selectedChunkId,
  feedbackByChunk,
  onSeek,
  onInspect,
  onCorrect,
  onReanalyze,
}: Props) {
  const sorted = [...chunks].sort((a, b) => {
    const aStart = chunkStart(a);
    const bStart = chunkStart(b);
    if (aStart == null) return bStart == null ? a.id - b.id : 1;
    if (bStart == null) return -1;
    return aStart - bStart;
  });
  const activeRef = useRef<HTMLLIElement | null>(null);
  const containerRef = useRef<HTMLOListElement | null>(null);
  const activeIndex = sorted.findIndex((chunk) => {
    const start = chunkStart(chunk);
    const end = chunkEnd(chunk);
    return start != null && end != null && currentTime >= start && currentTime < end;
  });

  useEffect(() => {
    if (!activeRef.current || !containerRef.current) return;
    const containerRect = containerRef.current.getBoundingClientRect();
    const activeRect = activeRef.current.getBoundingClientRect();
    if (activeRect.top < containerRect.top || activeRect.bottom > containerRect.bottom) {
      activeRef.current.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
  }, [activeIndex]);

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
      </div>

      {sorted.length === 0 ? (
        <p className="text-sm text-text-muted">No chunk analysis data is available.</p>
      ) : (
        <ol
          ref={containerRef}
          className="max-h-[620px] space-y-2 overflow-y-auto pr-1 scroll-smooth"
          style={{ scrollbarWidth: 'thin', scrollbarColor: '#3f3f46 transparent' }}
        >
          {sorted.map((chunk, index) => {
            const isActive = index === activeIndex;
            const isSelected = chunk.id === selectedChunkId;
            const feedback = feedbackByChunk.get(chunk.id);
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

                {coaching && (
                  <p className="mt-2 line-clamp-2 text-xs leading-relaxed text-text-muted">
                    {coaching}
                  </p>
                )}

                <div className="mt-3 flex flex-wrap gap-2">
                  <button
                    type="button"
                    onClick={() => {
                      if (start != null) onSeek(start);
                    }}
                    disabled={!canSeek}
                    className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    Seek
                  </button>
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
                    className="rounded-md border border-accent/40 px-2.5 py-1.5 text-xs font-medium text-accent hover:bg-accent/10 focus:outline-none focus:ring-2 focus:ring-accent"
                  >
                    Re-analyze
                  </button>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
