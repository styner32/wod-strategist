import type { ChunkAnalysisResult } from '../../api/history';

interface Props {
  chunks: ChunkAnalysisResult[];
  currentTime?: number;
  onSeek?: (time: number) => void;
}

export function ChunkMetricsChart({ chunks, currentTime, onSeek }: Props) {
  const chunkStart = (chunk: ChunkAnalysisResult) => chunk.media_start_secs ?? null;
  const chunkEnd = (chunk: ChunkAnalysisResult) => chunk.media_end_secs ?? null;
  const sorted = [...chunks].sort((a, b) => {
    const aStart = chunkStart(a);
    const bStart = chunkStart(b);
    if (aStart == null) return bStart == null ? a.id - b.id : 1;
    if (bStart == null) return -1;
    return aStart - bStart;
  });
  const maxHR = Math.max(...sorted.map((c) => c.heart_rate_bpm || 0), 200);
  const maxTime = sorted.reduce(
    (maximum, chunk) => Math.max(maximum, chunkEnd(chunk) ?? 0),
    0,
  );

  const hasHeartRate = sorted.some((c) => c.heart_rate_bpm > 0);

  function formatTime(secs: number | null) {
    if (secs == null || !Number.isFinite(secs)) return '—';
    const m = Math.floor(secs / 60);
    const s = Math.floor(secs % 60);
    return `${m}:${s.toString().padStart(2, '0')}`;
  }

  return (
    <div className="bg-bg-elevated border border-border rounded-xl p-5">
      <h2 className="text-lg font-semibold text-text-primary mb-4">
        Chunk Timeline
        <span className="text-sm font-normal text-text-muted ml-2">
          {sorted.length} chunks
        </span>
      </h2>

      {/* Simple bar chart of chunks */}
      <div className="space-y-2">
        {sorted.map((chunk, i) => {
          const start = chunkStart(chunk);
          const end = chunkEnd(chunk);
          const hasMediaInterval = start != null && end != null && end > start;
          const widthPercent = maxTime > 0 && hasMediaInterval
            ? ((end! - start!) / maxTime) * 100
            : 100 / sorted.length;
          const hrPercent = maxHR > 0 ? (chunk.heart_rate_bpm / maxHR) * 100 : 0;

          const isActive =
            hasMediaInterval &&
            currentTime != null &&
            currentTime >= start! &&
            currentTime < end!;

          const movement = chunk.exercise_type
            ?? chunk.structured_inference?.observed_movement_name;

          return (
            <div
              key={chunk.id}
              className="group w-full"
            >
              <div className="flex items-center gap-3">
                <span
                  className={`text-xs w-14 text-right font-mono shrink-0 ${
                    isActive ? 'text-accent font-semibold' : 'text-text-muted'
                  }`}
                >
                  {formatTime(start)}
                </span>

                <div className="flex-1 relative">
                  <div className="h-8 bg-bg-secondary rounded-md overflow-hidden relative">
                    <div
                      className={`h-full rounded-md flex items-center px-2 transition-colors ${
                        isActive
                          ? 'bg-accent/30'
                          : 'bg-accent/20 group-hover:bg-accent/30'
                      }`}
                      style={{ width: `${Math.max(widthPercent, 10)}%` }}
                    >
                      <span className="text-xs text-text-secondary truncate">
                        {movement || `Chunk ${i + 1}`}
                      </span>
                    </div>

                    {/* Active playback indicator */}
                    {isActive && currentTime != null && (
                      <div
                        className="absolute top-0 bottom-0 w-0.5 bg-accent z-10"
                        style={{
                          left: `${
                            ((currentTime - start!) /
                              (end! - start!)) *
                            Math.max(widthPercent, 10)
                          }%`,
                        }}
                      />
                    )}

                    {/* Heart rate indicator */}
                    {hasHeartRate && chunk.heart_rate_bpm > 0 && (
                      <div
                        className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1"
                      >
                        <svg className="w-3 h-3 text-error" fill="currentColor" viewBox="0 0 20 20">
                          <path d="M3.172 5.172a4 4 0 015.656 0L10 6.343l1.172-1.171a4 4 0 115.656 5.656L10 17.657l-6.828-6.829a4 4 0 010-5.656z" />
                        </svg>
                        <span className="text-xs text-text-muted font-mono">
                          {chunk.heart_rate_bpm}
                        </span>
                      </div>
                    )}
                  </div>

                  {/* HR bar underneath */}
                  {hasHeartRate && chunk.heart_rate_bpm > 0 && (
                    <div className="h-1 mt-0.5 bg-bg-secondary rounded-full overflow-hidden">
                      <div
                        className="h-full rounded-full transition-all duration-300"
                        style={{
                          width: `${hrPercent}%`,
                          backgroundColor: chunk.heart_rate_bpm > 160
                            ? 'var(--color-error)'
                            : chunk.heart_rate_bpm > 130
                              ? 'var(--color-warning)'
                              : 'var(--color-success)',
                        }}
                      />
                    </div>
                  )}
                </div>

                <div className="flex w-16 shrink-0 flex-col items-end gap-1">
                  <span className="text-xs text-text-muted font-mono">
                    {formatTime(end)}
                  </span>
                  {onSeek && (
                    hasMediaInterval ? (
                      <button
                        type="button"
                        onClick={() => {
                          if (start != null) onSeek(start);
                        }}
                        aria-label={`Seek to chunk ${i + 1} at ${formatTime(start)}`}
                        className="rounded border border-border px-1.5 py-0.5 text-[11px] font-medium text-text-secondary hover:bg-bg-tertiary focus:outline-none focus:ring-2 focus:ring-accent"
                      >
                        Seek
                      </button>
                    ) : chunk.start_secs != null && Number.isFinite(chunk.start_secs) && chunk.start_secs >= 0 ? (
                      <button
                        type="button"
                        onClick={() => {
                          if (chunk.start_secs != null) onSeek(chunk.start_secs);
                        }}
                        title="Jump using capture-clock start time (may be inaccurate)"
                        className="rounded border border-warning/30 bg-warning/5 px-1.5 py-0.5 text-[11px] font-medium text-warning hover:bg-warning/10 focus:outline-none focus:ring-2 focus:ring-warning"
                      >
                        Jump
                      </button>
                    ) : (
                      <button
                        type="button"
                        disabled
                        aria-label={`Verified media interval unavailable for chunk ${i + 1}`}
                        className="rounded border border-border px-1.5 py-0.5 text-[11px] font-medium text-text-secondary opacity-40 cursor-not-allowed"
                      >
                        Seek
                      </button>
                    )
                  )}
                </div>
              </div>
            </div>
          );
        })}
      </div>

      {/* Legend */}
      {hasHeartRate && (
        <div className="flex items-center gap-4 mt-4 text-xs text-text-muted">
          <div className="flex items-center gap-1.5">
            <span className="w-3 h-1.5 rounded-full bg-success" />
            &lt;130 bpm
          </div>
          <div className="flex items-center gap-1.5">
            <span className="w-3 h-1.5 rounded-full bg-warning" />
            130-160 bpm
          </div>
          <div className="flex items-center gap-1.5">
            <span className="w-3 h-1.5 rounded-full bg-error" />
            &gt;160 bpm
          </div>
        </div>
      )}
    </div>
  );
}
