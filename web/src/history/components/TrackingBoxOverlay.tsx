import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import type { ChunkAnalysisResult } from "../../api/history";
import {
  buildTrackingTimeline,
  containedVideoRect,
  describeTrackingOverlayState,
  overlayRect,
  readTrackingOverlayPreference,
  trackingBoxAt,
  writeTrackingOverlayPreference,
} from "../trackingBoxes";

type FrameCallbackVideo = HTMLVideoElement & {
  requestVideoFrameCallback?: (cb: () => void) => number;
  cancelVideoFrameCallback?: (handle: number) => void;
};

/**
 * Draws the tracked person's box over the session video. It only reads
 * `currentTime`; it never seeks or changes playback (see web-media-playback.md).
 */
export function TrackingBoxOverlay({
  videoRef,
  chunks,
  loading = false,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  chunks: ChunkAnalysisResult[];
  loading?: boolean;
}) {
  const timeline = useMemo(() => buildTrackingTimeline(chunks), [chunks]);
  const [enabled, setEnabled] = useState(() => readTrackingOverlayPreference());
  const [hasActiveBox, setHasActiveBox] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const video = videoRef.current as FrameCallbackVideo | null;
    const boxEl = boxRef.current;
    if (!enabled || !video || !boxEl || timeline.length === 0) {
      if (boxEl) boxEl.style.display = "none";
      setHasActiveBox(prev => (prev ? false : prev));
      return;
    }
    let cancelled = false;
    let handle = 0;
    const useFrameCallback = typeof video.requestVideoFrameCallback === "function";
    const paint = () => {
      const box = trackingBoxAt(timeline, video.currentTime);
      const active = box != null;
      setHasActiveBox(prev => (prev === active ? prev : active));
      const rect = containedVideoRect(video.clientWidth, video.clientHeight, video.videoWidth, video.videoHeight);
      if (!box || !rect) {
        boxEl.style.display = "none";
      } else {
        const r = overlayRect(box, rect);
        boxEl.style.display = "block";
        boxEl.style.left = `${r.left}px`;
        boxEl.style.top = `${r.top}px`;
        boxEl.style.width = `${r.width}px`;
        boxEl.style.height = `${r.height}px`;
      }
    };
    const schedule = () => {
      if (cancelled) return;
      paint();
      handle = useFrameCallback ? video.requestVideoFrameCallback!(schedule) : requestAnimationFrame(schedule);
    };
    schedule();
    // Frame callbacks stop while paused; repaint on read-only media/layout events too.
    const repaint = () => paint();
    const events = ["seeked", "loadedmetadata", "loadeddata", "canplay", "timeupdate"] as const;
    for (const eventName of events) {
      video.addEventListener(eventName, repaint);
    }
    const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(repaint) : null;
    observer?.observe(video);
    return () => {
      cancelled = true;
      if (useFrameCallback) video.cancelVideoFrameCallback?.(handle);
      else cancelAnimationFrame(handle);
      for (const eventName of events) {
        video.removeEventListener(eventName, repaint);
      }
      observer?.disconnect();
    };
  }, [enabled, timeline, videoRef]);

  const status = describeTrackingOverlayState(timeline, enabled, hasActiveBox, loading);
  return (
    <>
      <div
        ref={boxRef}
        data-testid="tracking-box"
        data-tracking-overlay="true"
        aria-hidden="true"
        className="pointer-events-none absolute z-10 border-2 border-emerald-400 shadow-[0_0_0_1px_rgba(0,0,0,0.6)]"
        style={{ display: "none" }}
      >
        <span className="absolute -top-5 left-0 rounded-sm bg-emerald-400 px-1 text-[10px] font-bold text-black">TARGET</span>
      </div>
      <div
        data-tracking-overlay="true"
        className="absolute right-2 top-2 z-10 flex max-w-[calc(100%-1rem)] items-center gap-1.5"
      >
        {status.notice && (
          <span
            data-testid="tracking-box-notice"
            className="pointer-events-none truncate rounded-md border border-white/15 bg-black/75 px-2 py-1 text-[11px] text-white/85"
          >
            {status.notice}
          </span>
        )}
        <button
          type="button"
          disabled={loading && !status.available}
          aria-pressed={enabled}
          title={
            status.available
              ? "청크마다 최대 3개 박스를 이어 표시하므로 빠르게 움직이는 동작에서는 박스가 몸보다 늦을 수 있습니다."
              : "추적 박스 데이터가 기록된 신규 청크 분석 세션부터 박스가 표시됩니다."
          }
          onClick={() => {
            const next = !enabled;
            setEnabled(next);
            writeTrackingOverlayPreference(next);
          }}
          className={`shrink-0 rounded-md border px-2 py-1 text-[11px] font-medium transition-colors disabled:opacity-40 ${
            enabled && status.available
              ? "border-emerald-400/80 bg-emerald-950/80 text-emerald-200 hover:bg-emerald-900/85"
              : enabled
                ? "border-amber-400/60 bg-black/75 text-amber-200 hover:bg-black/85"
                : "border-border bg-black/60 text-white hover:bg-black/80"
          }`}
        >
          {status.buttonLabel}
        </button>
      </div>
    </>
  );
}
