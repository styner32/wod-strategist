import { useEffect, useRef } from 'react';
import type { ChunkAnalysisResult } from '../../api/history';

interface Props {
  chunks: ChunkAnalysisResult[];
  currentTime: number;
  onSeek: (time: number) => void;
}

function formatTime(secs: number) {
  const m = Math.floor(secs / 60);
  const s = Math.floor(secs % 60);
  return `${m}:${s.toString().padStart(2, '0')}`;
}

function parseChunkOutput(output: string): {
  exerciseType?: string;
  coaching?: string;
} {
  try {
    const parsed = JSON.parse(output);
    return {
      exerciseType: parsed.exercise_type,
      coaching: parsed.coaching_feedback || parsed.feedback || output,
    };
  } catch {
    // Raw text output — use as coaching directly
    return { coaching: output };
  }
}

function HeartRateBadge({ bpm }: { bpm: number }) {
  if (bpm <= 0) return null;
  const color =
    bpm > 160
      ? 'text-error bg-error/10'
      : bpm > 130
        ? 'text-warning bg-warning/10'
        : 'text-success bg-success/10';
  return (
    <span
      className={`inline-flex items-center gap-1 text-xs font-mono px-1.5 py-0.5 rounded-md ${color}`}
    >
      <svg className="w-3 h-3" fill="currentColor" viewBox="0 0 20 20">
        <path d="M3.172 5.172a4 4 0 015.656 0L10 6.343l1.172-1.171a4 4 0 115.656 5.656L10 17.657l-6.828-6.829a4 4 0 010-5.656z" />
      </svg>
      {bpm}
    </span>
  );
}

export function GuidanceTimeline({ chunks, currentTime, onSeek }: Props) {
  const sorted = [...chunks]
    .filter((c) => c.status === 'COMPLETED' && c.start_secs != null)
    .sort((a, b) => a.start_secs - b.start_secs);

  const activeRef = useRef<HTMLDivElement | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

  // Find active chunk index
  const activeIndex = sorted.findIndex(
    (c) => currentTime >= c.start_secs && currentTime < c.end_secs,
  );

  // Auto-scroll to active chunk
  useEffect(() => {
    if (activeRef.current && containerRef.current) {
      const container = containerRef.current;
      const el = activeRef.current;
      const containerRect = container.getBoundingClientRect();
      const elRect = el.getBoundingClientRect();

      // Only scroll if the element is outside the visible area
      if (
        elRect.top < containerRect.top ||
        elRect.bottom > containerRect.bottom
      ) {
        el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      }
    }
  }, [activeIndex]);

  if (sorted.length === 0) {
    return (
      <div className="bg-bg-elevated border border-border rounded-xl p-5">
        <h2 className="text-lg font-semibold text-text-primary mb-2">
          Guidance Timeline
        </h2>
        <p className="text-sm text-text-muted">
          No chunk analysis data available for this session.
        </p>
      </div>
    );
  }

  return (
    <div className="bg-bg-elevated border border-border rounded-xl p-5">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold text-text-primary">
          Guidance Timeline
        </h2>
        <span className="text-xs text-text-muted">
          {sorted.length} segments • Click to seek
        </span>
      </div>

      <div
        ref={containerRef}
        className="space-y-1.5 max-h-[480px] overflow-y-auto pr-1 scroll-smooth"
        style={{ scrollbarWidth: 'thin', scrollbarColor: '#3f3f46 transparent' }}
      >
        {sorted.map((chunk, i) => {
          const isActive = i === activeIndex;
          const parsed = parseChunkOutput(chunk.output || '{}');

          return (
            <div
              key={chunk.id}
              ref={isActive ? activeRef : null}
              onClick={() => onSeek(chunk.start_secs)}
              className={`
                group rounded-lg p-3 cursor-pointer transition-all duration-200 border
                ${
                  isActive
                    ? 'bg-accent/10 border-accent/40 ring-1 ring-accent/20'
                    : 'bg-bg-secondary/50 border-transparent hover:bg-bg-secondary hover:border-border'
                }
              `}
            >
              {/* Header: timestamps + exercise + HR */}
              <div className="flex items-center gap-2 mb-1">
                <span
                  className={`text-xs font-mono shrink-0 ${
                    isActive ? 'text-accent' : 'text-text-muted'
                  }`}
                >
                  {formatTime(chunk.start_secs)} – {formatTime(chunk.end_secs)}
                </span>

                {parsed.exerciseType && (
                  <span
                    className={`text-xs font-medium px-1.5 py-0.5 rounded ${
                      isActive
                        ? 'bg-accent/20 text-accent'
                        : 'bg-bg-tertiary text-text-secondary'
                    }`}
                  >
                    {parsed.exerciseType}
                  </span>
                )}

                <div className="flex-1" />

                <HeartRateBadge bpm={chunk.heart_rate_bpm} />

                {isActive && (
                  <span className="relative flex h-2 w-2">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-accent opacity-75" />
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-accent" />
                  </span>
                )}
              </div>

              {/* Coaching text */}
              {parsed.coaching && (
                <p
                  className={`text-xs leading-relaxed ${
                    isActive
                      ? 'text-text-primary line-clamp-4'
                      : 'text-text-muted line-clamp-2 group-hover:text-text-secondary'
                  }`}
                >
                  {parsed.coaching}
                </p>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
