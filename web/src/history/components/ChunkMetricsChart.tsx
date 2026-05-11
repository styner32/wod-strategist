import type { ChunkAnalysisResult } from '../../api/history';

interface Props {
  chunks: ChunkAnalysisResult[];
}

export function ChunkMetricsChart({ chunks }: Props) {
  const sorted = [...chunks].sort((a, b) => a.start_secs - b.start_secs);
  const maxHR = Math.max(...sorted.map((c) => c.heart_rate_bpm || 0), 200);
  const maxTime = sorted.length > 0 ? sorted[sorted.length - 1].end_secs : 0;

  const hasHeartRate = sorted.some((c) => c.heart_rate_bpm > 0);

  function formatTime(secs: number) {
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
          const widthPercent = maxTime > 0
            ? ((chunk.end_secs - chunk.start_secs) / maxTime) * 100
            : 100 / sorted.length;
          const hrPercent = maxHR > 0 ? (chunk.heart_rate_bpm / maxHR) * 100 : 0;

          let parsedOutput: { exercise_type?: string } = {};
          try {
            parsedOutput = JSON.parse(chunk.output || '{}');
          } catch { /* skip */ }

          return (
            <div key={chunk.id} className="group">
              <div className="flex items-center gap-3">
                <span className="text-xs text-text-muted w-14 text-right font-mono shrink-0">
                  {formatTime(chunk.start_secs)}
                </span>

                <div className="flex-1 relative">
                  <div className="h-8 bg-bg-secondary rounded-md overflow-hidden relative">
                    <div
                      className="h-full bg-accent/20 group-hover:bg-accent/30 transition-colors rounded-md flex items-center px-2"
                      style={{ width: `${Math.max(widthPercent, 10)}%` }}
                    >
                      <span className="text-xs text-text-secondary truncate">
                        {parsedOutput.exercise_type || `Chunk ${i + 1}`}
                      </span>
                    </div>

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

                <span className="text-xs text-text-muted w-14 font-mono shrink-0">
                  {formatTime(chunk.end_secs)}
                </span>
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
