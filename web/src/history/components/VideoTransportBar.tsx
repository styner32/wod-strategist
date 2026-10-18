import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChunkAnalysisResult } from "../../api/history";
import type { HighlightSegment } from "../highlights";

export const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2] as const;

export function clampSeekTime(targetSeconds: number, durationSeconds?: number): number {
  if (!Number.isFinite(targetSeconds)) return 0;
  const clampedMin = Math.max(0, targetSeconds);
  if (durationSeconds != null && Number.isFinite(durationSeconds) && durationSeconds > 0) {
    return Math.min(clampedMin, durationSeconds);
  }
  return clampedMin;
}

export function formatPreciseTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00.0";
  const wholeTenths = Math.round(seconds * 10);
  const tenths = wholeTenths % 10;
  const totalSeconds = Math.floor(wholeTenths / 10);
  const hours = Math.floor(totalSeconds / 3600);
  const mins = Math.floor((totalSeconds % 3600) / 60);
  const secs = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}:${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}.${tenths}`;
  }
  return `${mins}:${String(secs).padStart(2, "0")}.${tenths}`;
}

export function isInteractiveKeyTarget(target: EventTarget | null): boolean {
  if (!target || typeof target !== "object") return false;
  const el = target as {
    tagName?: string;
    isContentEditable?: boolean;
    getAttribute?: (name: string) => string | null;
  };
  if (el.isContentEditable) return true;
  const tag = el.tagName?.toUpperCase();
  if (
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    tag === "SELECT" ||
    tag === "BUTTON" ||
    tag === "A" ||
    tag === "VIDEO" ||
    tag === "AUDIO" ||
    tag === "SUMMARY"
  ) {
    return true;
  }
  const role = el.getAttribute?.("role");
  return role === "tab" || role === "button" || role === "slider" || role === "textbox";
}

function highlightColor(type: string): string {
  switch (type) {
    case "best_form":
      return "bg-success/80";
    case "worst_form":
      return "bg-error/80";
    case "fatigue_point":
      return "bg-warning/80";
    default:
      return "bg-accent/80";
  }
}

export interface VideoTransportBarProps {
  currentTime: number;
  duration?: number;
  isPaused: boolean;
  playbackRate: number;
  canControl: boolean;
  chunks?: ChunkAnalysisResult[];
  highlights?: HighlightSegment[];
  compact?: boolean;
  onSeek: (seconds: number) => void;
  onTogglePlay: () => void;
  onRateChange: (rate: number) => void;
}

export const VideoTransportBar = memo(function VideoTransportBar({
  currentTime,
  duration,
  isPaused,
  playbackRate,
  canControl,
  chunks = [],
  highlights = [],
  compact = false,
  onSeek,
  onTogglePlay,
  onRateChange,
}: VideoTransportBarProps) {
  const trackRef = useRef<HTMLDivElement | null>(null);
  const [hoverTime, setHoverTime] = useState<number | null>(null);
  const [isDragging, setIsDragging] = useState(false);

  const effectiveDuration = useMemo(() => {
    if (duration != null && Number.isFinite(duration) && duration > 0) {
      return duration;
    }
    let maxEnd = 0;
    for (const chunk of chunks) {
      if (chunk.media_end_secs != null && chunk.media_end_secs > maxEnd) {
        maxEnd = chunk.media_end_secs;
      }
    }
    for (const hl of highlights) {
      if (hl.endSeconds > maxEnd) {
        maxEnd = hl.endSeconds;
      }
    }
    return maxEnd;
  }, [duration, chunks, highlights]);

  const validChunks = useMemo(
    () =>
      chunks.filter(
        (c) =>
          c.media_start_secs != null &&
          c.media_end_secs != null &&
          c.media_end_secs > c.media_start_secs,
      ),
    [chunks],
  );

  const stepBy = useCallback(
    (deltaSeconds: number) => {
      if (!canControl) return;
      onSeek(clampSeekTime(currentTime + deltaSeconds, effectiveDuration || duration));
    },
    [canControl, currentTime, effectiveDuration, duration, onSeek],
  );

  useEffect(() => {
    if (
      !canControl ||
      typeof window === "undefined" ||
      typeof window.addEventListener !== "function"
    ) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isInteractiveKeyTarget(event.target)) return;

      if (event.key === "ArrowLeft") {
        event.preventDefault();
        stepBy(event.shiftKey ? -1 : -5);
      } else if (event.key === "ArrowRight") {
        event.preventDefault();
        stepBy(event.shiftKey ? 1 : 5);
      } else if (event.key === "j" || event.key === "J") {
        event.preventDefault();
        stepBy(-10);
      } else if (event.key === "l" || event.key === "L") {
        event.preventDefault();
        stepBy(10);
      } else if (event.key === "k" || event.key === "K") {
        event.preventDefault();
        onTogglePlay();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener?.("keydown", handleKeyDown);
  }, [canControl, stepBy, onTogglePlay]);

  const clientXToTime = useCallback(
    (clientX: number): number | null => {
      if (!trackRef.current || effectiveDuration <= 0) return null;
      const rect = trackRef.current.getBoundingClientRect();
      if (rect.width <= 0) return null;
      const ratio = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
      return clampSeekTime(ratio * effectiveDuration, effectiveDuration);
    },
    [effectiveDuration],
  );

  const hoverContext = useMemo(() => {
    if (hoverTime == null) return null;
    const hl = highlights.find((h) => hoverTime >= h.startSeconds && hoverTime <= h.endSeconds);
    const chunk = validChunks.find(
      (c) => hoverTime >= (c.media_start_secs ?? 0) && hoverTime < (c.media_end_secs ?? 0),
    );
    const movement =
      hl?.movement ||
      chunk?.exercise_type ||
      chunk?.structured_inference?.observed_movement_name;
    return {
      timeLabel: formatPreciseTime(hoverTime),
      movement,
    };
  }, [hoverTime, highlights, validChunks]);

  const playheadPercent =
    effectiveDuration > 0
      ? Math.max(0, Math.min(100, (currentTime / effectiveDuration) * 100))
      : 0;

  const stepOffsets = compact ? [-5, -1, 1, 5] : [-10, -5, -1, 1, 5, 10];

  return (
    <div
      data-testid="video-transport-bar"
      className="rounded-xl border border-border bg-bg-secondary/90 p-2.5 space-y-2"
    >
      {/* Mini timeline scrubber */}
      {effectiveDuration > 0 && (
        <div className="space-y-1">
          <div
            ref={trackRef}
            data-testid="mini-timeline-track"
            role="slider"
            aria-label="전체 구간 미니 타임라인 탐색"
            aria-valuemin={0}
            aria-valuemax={Math.round(effectiveDuration)}
            aria-valuenow={Math.round(currentTime)}
            tabIndex={canControl ? 0 : -1}
            onKeyDown={(event) => {
              if (!canControl) return;
              if (event.key === "ArrowLeft") {
                event.preventDefault();
                stepBy(event.shiftKey ? -1 : -5);
              } else if (event.key === "ArrowRight") {
                event.preventDefault();
                stepBy(event.shiftKey ? 1 : 5);
              }
            }}
            onPointerDown={(event) => {
              if (!canControl) return;
              setIsDragging(true);
              event.currentTarget.setPointerCapture?.(event.pointerId);
              const t = clientXToTime(event.clientX);
              if (t != null) onSeek(t);
            }}
            onPointerMove={(event) => {
              const t = clientXToTime(event.clientX);
              setHoverTime(t);
              if (isDragging && canControl && t != null) {
                onSeek(t);
              }
            }}
            onPointerUp={(event) => {
              if (isDragging) {
                setIsDragging(false);
                event.currentTarget.releasePointerCapture?.(event.pointerId);
              }
            }}
            onPointerLeave={() => {
              if (!isDragging) setHoverTime(null);
            }}
            className={`relative h-4 w-full rounded-md bg-bg-tertiary overflow-hidden select-none ${
              canControl ? "cursor-pointer" : "opacity-50 cursor-not-allowed"
            }`}
          >
            {/* Chunk interval bands */}
            {validChunks.map((chunk) => {
              const start = chunk.media_start_secs!;
              const end = chunk.media_end_secs!;
              const left = Math.max(0, Math.min(100, (start / effectiveDuration) * 100));
              const width = Math.max(0.8, Math.min(100 - left, ((end - start) / effectiveDuration) * 100));
              return (
                <div
                  key={`chunk-band-${chunk.id}`}
                  className="absolute top-0 bottom-0 bg-accent/20 border-r border-bg-primary/40"
                  style={{ left: `${left}%`, width: `${width}%` }}
                />
              );
            })}

            {/* Highlight interval markers */}
            {highlights.map((hl, index) => {
              const left = Math.max(0, Math.min(100, (hl.startSeconds / effectiveDuration) * 100));
              const width = Math.max(
                1.5,
                Math.min(100 - left, ((hl.endSeconds - hl.startSeconds) / effectiveDuration) * 100),
              );
              return (
                <div
                  key={`hl-band-${index}-${hl.startSeconds}`}
                  title={`${hl.startLabel}–${hl.endLabel} ${hl.movement ?? hl.type}`}
                  className={`absolute top-0.5 bottom-0.5 rounded-xs ${highlightColor(hl.type)}`}
                  style={{ left: `${left}%`, width: `${width}%` }}
                />
              );
            })}

            {/* Played progress fill */}
            <div
              className="absolute inset-y-0 left-0 bg-accent/30 pointer-events-none"
              style={{ width: `${playheadPercent}%` }}
            />

            {/* Playhead indicator */}
            <div
              className="absolute top-0 bottom-0 w-0.5 bg-white shadow pointer-events-none"
              style={{ left: `${playheadPercent}%` }}
            />
          </div>

          {!compact && (
            <div className="flex items-center justify-between gap-2 text-[11px] text-text-muted">
              <span>
                {hoverContext
                  ? `탐색 위치 ${hoverContext.timeLabel}${hoverContext.movement ? ` · ${hoverContext.movement}` : ""}`
                  : `구간 ${validChunks.length}개 · 하이라이트 ${highlights.length}개`}
              </span>
              {highlights.length > 0 && (
                <div className="flex flex-wrap items-center gap-1">
                  {highlights.slice(0, 4).map((hl, idx) => (
                    <button
                      key={`quick-hl-${idx}-${hl.startSeconds}`}
                      type="button"
                      disabled={!canControl}
                      onClick={() => onSeek(clampSeekTime(hl.startSeconds, effectiveDuration))}
                      className="rounded border border-border bg-bg-tertiary/70 px-1.5 py-0.5 font-mono text-[10px] text-text-secondary hover:border-accent/40 hover:text-accent disabled:opacity-40"
                    >
                      {hl.startLabel}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Precision transport controls */}
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <div className="flex flex-wrap items-center gap-1">
          <button
            type="button"
            disabled={!canControl}
            onClick={onTogglePlay}
            aria-label={isPaused ? "영상 재생" : "영상 일시정지"}
            className="rounded-md bg-accent/15 border border-accent/40 px-2.5 py-1 text-xs font-semibold text-accent hover:bg-accent/25 focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-40"
          >
            {isPaused ? "▶ 재생" : "⏸ 일시정지"}
          </button>

          {stepOffsets.map((offset) => (
            <button
              key={offset}
              type="button"
              disabled={!canControl}
              onClick={() => stepBy(offset)}
              aria-label={`${offset > 0 ? `+${offset}` : offset}초 이동`}
              className="rounded-md border border-border bg-bg-tertiary/60 px-2 py-1 font-mono text-xs font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-40"
            >
              {offset > 0 ? `+${offset}s` : `${offset}s`}
            </button>
          ))}
        </div>

        <div className="flex items-center gap-2">
          <span
            className="font-mono text-xs text-text-secondary"
            aria-label="현재 영상 위치"
          >
            {formatPreciseTime(currentTime)}
            {effectiveDuration > 0 && !compact ? ` / ${formatPreciseTime(effectiveDuration)}` : ""}
            <span className="ml-1 text-text-muted">({currentTime.toFixed(1)}s)</span>
          </span>

          <label className="inline-flex items-center gap-1 text-xs text-text-muted">
            <span className="sr-only">재생 배속</span>
            <select
              aria-label="재생 배속"
              disabled={!canControl}
              value={playbackRate}
              onChange={(event) => onRateChange(Number(event.target.value))}
              className="rounded-md border border-border bg-bg-tertiary px-1.5 py-1 font-mono text-xs text-text-primary focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-40"
            >
              {PLAYBACK_RATES.map((rate) => (
                <option key={rate} value={rate}>
                  {rate}x
                </option>
              ))}
            </select>
          </label>
        </div>
      </div>
    </div>
  );
});
