import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import type { ChunkAnalysisResult } from "../../api/history";
import {
  buildTrackingTimeline,
  containedVideoRect,
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
export function TrackingBoxOverlay({ videoRef, chunks }: { videoRef: RefObject<HTMLVideoElement | null>; chunks: ChunkAnalysisResult[] }) {
  const timeline = useMemo(() => buildTrackingTimeline(chunks), [chunks]);
  const [enabled, setEnabled] = useState(() => readTrackingOverlayPreference());
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const video = videoRef.current as FrameCallbackVideo | null;
    const boxEl = boxRef.current;
    if (!enabled || !video || !boxEl || timeline.length === 0) {
      if (boxEl) boxEl.style.display = "none";
      return;
    }
    let cancelled = false;
    let handle = 0;
    const useFrameCallback = typeof video.requestVideoFrameCallback === "function";
    const paint = () => {
      const box = trackingBoxAt(timeline, video.currentTime);
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
    // Frame callbacks stop while paused; repaint on seeks and resizes too.
    const repaint = () => paint();
    video.addEventListener("seeked", repaint);
    video.addEventListener("loadedmetadata", repaint);
    const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(repaint) : null;
    observer?.observe(video);
    return () => {
      cancelled = true;
      if (useFrameCallback) video.cancelVideoFrameCallback?.(handle);
      else cancelAnimationFrame(handle);
      video.removeEventListener("seeked", repaint);
      video.removeEventListener("loadedmetadata", repaint);
      observer?.disconnect();
    };
  }, [enabled, timeline, videoRef]);

  const available = timeline.length > 0;
  return (
    <>
      <div
        ref={boxRef}
        data-testid="tracking-box"
        aria-hidden="true"
        className="pointer-events-none absolute border-2 border-emerald-400 shadow-[0_0_0_1px_rgba(0,0,0,0.6)]"
        style={{ display: "none" }}
      >
        <span className="absolute -top-5 left-0 rounded-sm bg-emerald-400 px-1 text-[10px] font-bold text-black">TARGET</span>
      </div>
      <button
        type="button"
        disabled={!available}
        aria-pressed={enabled && available}
        title={
          available
            ? "청크마다 최대 3개 박스를 이어 표시하므로 빠르게 움직이는 동작에서는 박스가 몸보다 늦을 수 있습니다."
            : "이 세션에는 추적 박스 데이터가 없습니다."
        }
        onClick={() => {
          const next = !enabled;
          setEnabled(next);
          writeTrackingOverlayPreference(next);
        }}
        className="absolute right-2 top-2 rounded-md border border-border bg-black/60 px-2 py-1 text-[11px] font-medium text-white hover:bg-black/80 disabled:opacity-40"
      >
        추적 박스 {enabled && available ? "끄기" : "표시"}
      </button>
    </>
  );
}
