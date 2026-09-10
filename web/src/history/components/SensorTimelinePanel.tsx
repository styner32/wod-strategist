import { useCallback, useId, useMemo, useRef, useState } from "react";
import type { SensorTimelineResponse } from "../../api/history";
import {
  buildSvgLinePath,
  captureToMedia,
  decimatePoints,
  formatTimelineTime,
  mediaToCapture,
  timelineValueAtTime,
} from "../timelineUtils";
import ko from "../../../../features/i18n/locales/ko.json";

const labels = ko.sensorTimeline;

export interface SensorTimelinePanelProps {
  timelineResponse?: SensorTimelineResponse | null;
  isLoading?: boolean;
  currentTime?: number;
  onSeekMedia?: (mediaSec: number) => void;
  isMergedVideo?: boolean;
}

function getStatusLabel(status?: string): string {
  if (!status) return labels.noData;
  switch (status) {
    case "paused":
      return labels.paused;
    case "invalid":
    case "unstable":
      return labels.invalid;
    case "gap":
    case "missing":
      return labels.gap;
    default:
      return labels.noData;
  }
}

export function SensorTimelinePanel({
  timelineResponse,
  isLoading,
  currentTime,
  onSeekMedia,
  isMergedVideo = true,
}: SensorTimelinePanelProps) {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const [zoomRange, setZoomRange] = useState<[number, number] | null>(null);
  const [hoverMs, setHoverMs] = useState<number | null>(null);
  const [dragState, setDragState] = useState<{ startMs: number; currentMs: number } | null>(null);
  const filterId = useId();

  const timeline = timelineResponse?.timeline;
  const segments = timelineResponse?.video_mapping?.segments ?? [];

  const points = useMemo(() => timeline?.points ?? [], [timeline]);

  const timelineStartMs = 0;
  const timelineEndMs = Math.max(0, timeline?.duration_ms ?? 0);
  const activeStartMs = zoomRange ? zoomRange[0] : timelineStartMs;
  const activeEndMs = zoomRange ? zoomRange[1] : timelineEndMs;
  const activeDurationMs = Math.max(1, activeEndMs - activeStartMs);

  // SVG dimensions
  const svgWidth = 800;
  const svgHeight = 290;
  const margin = { top: 20, right: 30, bottom: 25, left: 55 };
  const chartWidth = svgWidth - margin.left - margin.right;

  const topChartY = margin.top;
  const topChartH = 100;

  const botChartY = 150;
  const botChartH = 100;

  // X scale helpers
  const xScale = useCallback(
    (ms: number) => {
      const clamped = Math.max(activeStartMs, Math.min(activeEndMs, ms));
      return margin.left + ((clamped - activeStartMs) / activeDurationMs) * chartWidth;
    },
    [activeStartMs, activeEndMs, activeDurationMs, margin.left, chartWidth],
  );

  const invertX = useCallback(
    (svgX: number) => {
      const relX = Math.max(margin.left, Math.min(margin.left + chartWidth, svgX));
      const fraction = (relX - margin.left) / chartWidth;
      return activeStartMs + fraction * activeDurationMs;
    },
    [activeStartMs, activeDurationMs, margin.left, chartWidth],
  );

  // Heart Rate Y domain & scale
  const { minHR, maxHR } = useMemo(() => {
    let rawMin = Infinity;
    let rawMax = -Infinity;
    for (const point of points) {
      const { value, status } = point.heart_rate_bpm;
      if (point.end_ms > activeStartMs && point.start_ms < activeEndMs &&
          status === "valid" && value != null && Number.isFinite(value)) {
        rawMin = Math.min(rawMin, value);
        rawMax = Math.max(rawMax, value);
      }
    }
    if (rawMin === Infinity) return { minHR: 50, maxHR: 180 };
    const min = Math.max(30, Math.floor((rawMin - 10) / 10) * 10);
    const max = Math.min(230, Math.ceil((rawMax + 10) / 10) * 10);
    return { minHR: min, maxHR: Math.max(min + 20, max) };
  }, [points, activeStartMs, activeEndMs]);

  const yScaleHR = useCallback(
    (val: number) => {
      const clamped = Math.max(minHR, Math.min(maxHR, val));
      return topChartY + topChartH - ((clamped - minHR) / (maxHR - minHR)) * topChartH;
    },
    [minHR, maxHR, topChartY, topChartH],
  );

  // Movement Variance Y domain & scale (starts at 0.0)
  const { minAcc, maxAcc } = useMemo(() => {
    let rawMax = -Infinity;
    for (const point of points) {
      const { value, status } = point.acc_magnitude_std_g;
      if (point.end_ms > activeStartMs && point.start_ms < activeEndMs &&
          status === "valid" && value != null && Number.isFinite(value)) {
        rawMax = Math.max(rawMax, value);
      }
    }
    if (rawMax === -Infinity) rawMax = 0.5;
    const roundedMax = Math.max(0.5, Math.ceil(rawMax * 1.25 * 10) / 10);
    return { minAcc: 0.0, maxAcc: roundedMax };
  }, [points, activeStartMs, activeEndMs]);

  const yScaleAcc = useCallback(
    (val: number) => {
      const clamped = Math.max(minAcc, Math.min(maxAcc, val));
      return botChartY + botChartH - ((clamped - minAcc) / (maxAcc - minAcc)) * botChartH;
    },
    [minAcc, maxAcc, botChartY, botChartH],
  );

  // Downsampled points and paths
  const hrSamples = useMemo(
    () => decimatePoints(points, "heart_rate_bpm", activeStartMs, activeEndMs, chartWidth, timeline?.gaps),
    [points, activeStartMs, activeEndMs, chartWidth, timeline?.gaps],
  );

  const accSamples = useMemo(
    () => decimatePoints(points, "acc_magnitude_std_g", activeStartMs, activeEndMs, chartWidth, timeline?.gaps),
    [points, activeStartMs, activeEndMs, chartWidth, timeline?.gaps],
  );

  const hrLinePath = useMemo(
    () => buildSvgLinePath(hrSamples, xScale, yScaleHR),
    [hrSamples, xScale, yScaleHR],
  );

  const accLinePath = useMemo(
    () => buildSvgLinePath(accSamples, xScale, yScaleAcc),
    [accSamples, xScale, yScaleAcc],
  );

  // Playhead position in capture clock
  const playheadCaptureMs = useMemo(() => {
    if (currentTime == null || !Number.isFinite(currentTime) || !isMergedVideo) {
      return null;
    }
    return mediaToCapture(currentTime, segments);
  }, [currentTime, isMergedVideo, segments]);

  const playheadX = useMemo(() => {
    if (
      playheadCaptureMs == null ||
      playheadCaptureMs < activeStartMs ||
      playheadCaptureMs > activeEndMs
    ) {
      return null;
    }
    return xScale(playheadCaptureMs);
  }, [playheadCaptureMs, activeStartMs, activeEndMs, xScale]);

  // Selection stays at the actual capture time, including missing sensor intervals.
  const hoverValues = useMemo(() => {
    if (hoverMs == null || !timeline) return null;
    return {
      heartRate: timelineValueAtTime(timeline, "heart_rate_bpm", hoverMs),
      movement: timelineValueAtTime(timeline, "acc_magnitude_std_g", hoverMs),
    };
  }, [hoverMs, timeline]);
  const hoverMediaSec = hoverMs == null ? null : captureToMedia(hoverMs, segments);
  const hoverX = useMemo(() => {
    if (hoverMs == null || hoverMs < activeStartMs || hoverMs > activeEndMs) return null;
    return xScale(hoverMs);
  }, [hoverMs, activeStartMs, activeEndMs, xScale]);

  // Time ticks
  const xTicks = useMemo(() => {
    const tickCount = 6;
    const step = activeDurationMs / (tickCount - 1);
    const ticks: { ms: number; label: string; x: number }[] = [];
    for (let i = 0; i < tickCount; i++) {
      const ms = activeStartMs + i * step;
      ticks.push({
        ms,
        label: formatTimelineTime(ms),
        x: xScale(ms),
      });
    }
    return ticks;
  }, [activeStartMs, activeDurationMs, xScale]);

  // HR Y-axis ticks
  const hrTicks = useMemo(() => {
    const mid = Math.round((minHR + maxHR) / 2);
    return [
      { val: minHR, y: yScaleHR(minHR) },
      { val: mid, y: yScaleHR(mid) },
      { val: maxHR, y: yScaleHR(maxHR) },
    ];
  }, [minHR, maxHR, yScaleHR]);

  // Acc Y-axis ticks
  const accTicks = useMemo(() => {
    const mid = Number(((minAcc + maxAcc) / 2).toFixed(2));
    return [
      { val: minAcc.toFixed(1), y: yScaleAcc(minAcc) },
      { val: mid.toFixed(2), y: yScaleAcc(mid) },
      { val: maxAcc.toFixed(2), y: yScaleAcc(maxAcc) },
    ];
  }, [minAcc, maxAcc, yScaleAcc]);

  // Pointer event handlers for crosshair, drag-to-zoom, and seek
  const getSvgPoint = useCallback((e: React.PointerEvent<SVGSVGElement>) => {
    if (!svgRef.current) return null;
    const rect = svgRef.current.getBoundingClientRect();
    const relX = (e.clientX - rect.left) / rect.width;
    const svgX = relX * svgWidth;
    return svgX;
  }, [svgWidth]);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      const svgX = getSvgPoint(e);
      if (svgX == null) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      const ms = invertX(svgX);
      setDragState({ startMs: ms, currentMs: ms });
      setHoverMs(ms);
    },
    [getSvgPoint, invertX],
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      const svgX = getSvgPoint(e);
      if (svgX == null) return;
      const ms = invertX(svgX);
      setHoverMs(ms);

      if (dragState) {
        setDragState((prev) => (prev ? { ...prev, currentMs: ms } : null));
      }
    },
    [getSvgPoint, invertX, dragState],
  );

  const handlePointerUp = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      if (e.currentTarget.hasPointerCapture(e.pointerId)) {
        e.currentTarget.releasePointerCapture(e.pointerId);
      }
      if (!dragState) return;
      const startX = xScale(dragState.startMs);
      const endX = xScale(dragState.currentMs);
      const distance = Math.abs(endX - startX);

      if (distance >= 15) {
        // Drag to zoom
        const newStart = Math.min(dragState.startMs, dragState.currentMs);
        const newEnd = Math.max(dragState.startMs, dragState.currentMs);
        if (newEnd - newStart >= 3000) {
          setZoomRange([newStart, newEnd]);
        }
      } else {
        // Single click -> seek video if available
        const clickedMs = dragState.startMs;
        const mediaSec = captureToMedia(clickedMs, segments);
        if (mediaSec != null && onSeekMedia && isMergedVideo) {
          onSeekMedia(mediaSec);
        }
      }

      setDragState(null);
    },
    [dragState, xScale, segments, onSeekMedia, isMergedVideo],
  );

  const handlePointerLeave = useCallback(() => {
    if (!dragState) {
      setHoverMs(null);
    }
  }, [dragState]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (timelineEndMs <= 0) return;
      const stepMs = e.shiftKey ? 5000 : 1000;
      const current = hoverMs ?? activeStartMs;

      if (e.key === "ArrowLeft") {
        e.preventDefault();
        const nextMs = Math.max(activeStartMs, current - stepMs);
        setHoverMs(nextMs);
      } else if (e.key === "ArrowRight") {
        e.preventDefault();
        const nextMs = Math.min(activeEndMs, current + stepMs);
        setHoverMs(nextMs);
      } else if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        setHoverMs(current);
        if (onSeekMedia && isMergedVideo) {
          const mediaSec = captureToMedia(current, segments);
          if (mediaSec != null) {
            onSeekMedia(mediaSec);
          }
        }
      } else if (e.key === "Escape") {
        setZoomRange(null);
      }
    },
    [
      timelineEndMs,
      hoverMs,
      activeStartMs,
      activeEndMs,
      onSeekMedia,
      isMergedVideo,
      segments,
    ],
  );

  // States: loading, pending, empty, failed
  if (isLoading || timelineResponse?.status === "pending") {
    return (
      <section
        className="rounded-xl border border-border bg-bg-elevated p-5 mb-6"
        aria-label={labels.title}
      >
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-semibold text-text-primary">{labels.title}</h2>
          <span className="inline-flex rounded-md bg-accent/20 px-2 py-1 text-xs text-accent">
            {labels.processing}
          </span>
        </div>
        <div className="flex items-center justify-center py-12 text-sm text-text-muted">
          <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin mr-3" />
          {labels.processing}
        </div>
      </section>
    );
  }

  if (timelineResponse?.status === "failed") {
    return (
      <section
        className="rounded-xl border border-border bg-bg-elevated p-5 mb-6"
        aria-label={labels.title}
      >
        <h2 className="font-semibold text-text-primary mb-2">{labels.title}</h2>
        <p className="text-sm text-error">{labels.failed}</p>
      </section>
    );
  }

  if (!timeline || timelineEndMs <= 0) {
    const isPastSession = timelineResponse?.reason === "timeline_not_generated";
    return (
      <section
        className="rounded-xl border border-border bg-bg-elevated p-5 mb-6"
        aria-label={labels.title}
      >
        <h2 className="font-semibold text-text-primary mb-2">{labels.title}</h2>
        <p className="text-sm text-text-muted">
          {isPastSession ? labels.unavailable : labels.empty}
        </p>
      </section>
    );
  }

  return (
    <section
      className="rounded-xl border border-border bg-bg-elevated p-5 mb-6 focus:outline-none focus:ring-1 focus:ring-accent"
      aria-label={labels.title}
      tabIndex={0}
      onKeyDown={handleKeyDown}
      role="region"
    >
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
        <div className="flex items-center gap-3">
          <h2 className="font-semibold text-text-primary">{labels.title}</h2>
          <span className="text-xs text-text-secondary">
            {formatTimelineTime(activeStartMs)} – {formatTimelineTime(activeEndMs)}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {zoomRange && (
            <button
              type="button"
              onClick={() => setZoomRange(null)}
              className="rounded-lg border border-border px-2.5 py-1 text-xs font-medium text-text-secondary hover:bg-bg-secondary focus:outline-none focus:ring-2 focus:ring-accent"
            >
              {labels.resetZoom}
            </button>
          )}
          <span className="text-xs text-text-muted hidden sm:inline">
            {labels.zoomHint}
          </span>
        </div>
      </div>

      {/* Video sync notice if not merged */}
      {!isMergedVideo && (
        <div className="mb-3 rounded-md bg-warning/10 border border-warning/30 px-3 py-2 text-xs text-warning flex items-center gap-2">
          <span>ℹ️</span>
          <span>{labels.videoSyncNotice}</span>
        </div>
      )}

      {/* Hover Information Bar */}
      <div className="flex flex-wrap items-center gap-4 text-xs font-mono mb-2 px-1 min-h-[20px]">
        {hoverValues && hoverMs != null ? (
          <>
            <span className="text-text-primary">
              ⏱ {formatTimelineTime(hoverMs)}
            </span>
            <span className="text-rose-400">
              ♥ {labels.heartRate}:{" "}
              {hoverValues.heartRate.status === "valid"
                ? `${hoverValues.heartRate.value} ${labels.heartRateUnit}`
                : getStatusLabel(hoverValues.heartRate.status)}
            </span>
            <span className="text-sky-400">
              ⚡ {labels.movementVariance}:{" "}
              {hoverValues.movement.status === "valid" && hoverValues.movement.value != null
                ? `${hoverValues.movement.value.toFixed(2)} ${labels.movementVarianceUnit}`
                : getStatusLabel(hoverValues.movement.status)}
            </span>
            {isMergedVideo && (
              <span className="text-text-muted">
                🎬 {hoverMediaSec != null ? formatTimelineTime(hoverMediaSec * 1000) : "—"}
              </span>
            )}
          </>
        ) : (
          <span className="text-text-muted font-sans text-xs">
            차트 위를 가리키거나 클릭하여 해당 시점의 영상을 탐색하세요.
          </span>
        )}
      </div>

      {/* Interactive Dual SVG Chart */}
      <div className="relative select-none overflow-hidden rounded-lg bg-bg-secondary/40 border border-border/50">
        <svg
          ref={svgRef}
          viewBox={`0 0 ${svgWidth} ${svgHeight}`}
          className="w-full h-auto cursor-crosshair touch-none"
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerUp}
          onPointerLeave={handlePointerLeave}
          onPointerCancel={() => setDragState(null)}
          onDoubleClick={() => setZoomRange(null)}
          aria-label={labels.title}
        >
          <defs>
            <clipPath id={`${filterId}-clip`}>
              <rect
                x={margin.left}
                y={margin.top}
                width={chartWidth}
                height={botChartY + botChartH - margin.top}
              />
            </clipPath>
          </defs>

          {/* Grid lines and ticks for Heart Rate */}
          {hrTicks.map((tick) => (
            <g key={`hr-${tick.val}`}>
              <line
                x1={margin.left}
                y1={tick.y}
                x2={margin.left + chartWidth}
                y2={tick.y}
                stroke="currentColor"
                className="text-border/40"
                strokeDasharray="3 3"
              />
              <text
                x={margin.left - 8}
                y={tick.y + 4}
                textAnchor="end"
                className="fill-text-muted text-[10px] font-mono"
              >
                {tick.val}
              </text>
            </g>
          ))}

          {/* Grid lines and ticks for Movement Variance */}
          {accTicks.map((tick) => (
            <g key={`acc-${tick.val}`}>
              <line
                x1={margin.left}
                y1={tick.y}
                x2={margin.left + chartWidth}
                y2={tick.y}
                stroke="currentColor"
                className="text-border/40"
                strokeDasharray="3 3"
              />
              <text
                x={margin.left - 8}
                y={tick.y + 4}
                textAnchor="end"
                className="fill-text-muted text-[10px] font-mono"
              >
                {tick.val}
              </text>
            </g>
          ))}

          {/* Subchart Labels */}
          <text
            x={margin.left + 8}
            y={topChartY + 14}
            className="fill-rose-400 text-[11px] font-semibold"
          >
            ♥ {labels.heartRate} ({labels.heartRateUnit})
          </text>
          <text
            x={margin.left + 8}
            y={botChartY + 14}
            className="fill-sky-400 text-[11px] font-semibold"
          >
            ⚡ {labels.movementVariance} ({labels.movementVarianceUnit})
          </text>

          {/* Data content group with clipping */}
          <g clipPath={`url(#${filterId}-clip)`}>
            {/* Pause Regions */}
            {(timeline.pauses ?? []).map((pause, idx) => {
              const pStart = Math.max(activeStartMs, pause.start_ms);
              const pEnd = Math.min(activeEndMs, pause.end_ms);
              if (pEnd <= pStart) return null;
              const x1 = xScale(pStart);
              const x2 = xScale(pEnd);
              const w = Math.max(1, x2 - x1);
              return (
                <g key={`pause-${idx}`}>
                  <rect
                    x={x1}
                    y={topChartY}
                    width={w}
                    height={botChartY + botChartH - topChartY}
                    fill="currentColor"
                    className="text-zinc-500/20"
                  />
                  {w > 40 && (
                    <text
                      x={x1 + w / 2}
                      y={(topChartY + botChartY + botChartH) / 2}
                      textAnchor="middle"
                      className="fill-text-muted text-[10px]"
                    >
                      {labels.paused}
                    </text>
                  )}
                </g>
              );
            })}

            {/* Long Gap Regions */}
            {(timeline.gaps ?? []).map((gap, idx) => {
              const gStart = Math.max(activeStartMs, gap.start_ms);
              const gEnd = Math.min(activeEndMs, gap.end_ms);
              if (gEnd <= gStart) return null;
              const x1 = xScale(gStart);
              const x2 = xScale(gEnd);
              const w = Math.max(1, x2 - x1);

              const inTop = gap.channel === "heart_rate" || gap.channel === "both";
              const inBot = gap.channel === "acc" || gap.channel === "both";

              return (
                <g key={`gap-${idx}`}>
                  {inTop && (
                    <rect
                      x={x1}
                      y={topChartY}
                      width={w}
                      height={topChartH}
                      fill="currentColor"
                      className="text-amber-500/15"
                    />
                  )}
                  {inBot && (
                    <rect
                      x={x1}
                      y={botChartY}
                      width={w}
                      height={botChartH}
                      fill="currentColor"
                      className="text-amber-500/15"
                    />
                  )}
                </g>
              );
            })}

            {/* Top Chart Line (Heart Rate) */}
            {hrLinePath && (
              <path
                d={hrLinePath}
                fill="none"
                stroke="#f43f5e"
                strokeWidth={1.75}
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            )}

            {/* Bottom Chart Line (Movement Variance) */}
            {accLinePath && (
              <path
                d={accLinePath}
                fill="none"
                stroke="#38bdf8"
                strokeWidth={1.75}
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            )}

            {/* Playhead line (synced with video playback) */}
            {playheadX != null && (
              <g>
                <line
                  x1={playheadX}
                  y1={topChartY}
                  x2={playheadX}
                  y2={botChartY + botChartH}
                  stroke="#ef4444"
                  strokeWidth={2}
                />
                <polygon
                  points={`${playheadX - 4},${topChartY} ${playheadX + 4},${topChartY} ${playheadX},${topChartY + 6}`}
                  fill="#ef4444"
                />
              </g>
            )}

            {/* Crosshair cursor */}
            {hoverX != null && (
              <line
                x1={hoverX}
                y1={topChartY}
                x2={hoverX}
                y2={botChartY + botChartH}
                stroke="currentColor"
                className="text-text-primary"
                strokeWidth={1}
                strokeDasharray="2 2"
              />
            )}

            {/* Drag-to-zoom selection overlay */}
            {dragState && (
              <rect
                x={Math.min(xScale(dragState.startMs), xScale(dragState.currentMs))}
                y={topChartY}
                width={Math.abs(xScale(dragState.currentMs) - xScale(dragState.startMs))}
                height={botChartY + botChartH - topChartY}
                fill="rgba(56, 189, 248, 0.2)"
                stroke="#38bdf8"
                strokeWidth={1}
                strokeDasharray="2 2"
              />
            )}
          </g>

          {/* Time axis ticks at bottom */}
          {xTicks.map((t, i) => (
            <g key={`x-${t.ms}-${i}`}>
              <line
                x1={t.x}
                y1={botChartY + botChartH}
                x2={t.x}
                y2={botChartY + botChartH + 5}
                stroke="currentColor"
                className="text-border"
              />
              <text
                x={t.x}
                y={botChartY + botChartH + 16}
                textAnchor="middle"
                className="fill-text-muted text-[10px] font-mono"
              >
                {t.label}
              </text>
            </g>
          ))}
        </svg>
      </div>

      {/* Footnote / Reference Note */}
      <p className="mt-3 text-xs text-text-muted">{labels.referenceOnlyNotice}</p>
    </section>
  );
}
