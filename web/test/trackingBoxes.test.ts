import {
  buildTrackingTimeline,
  containedVideoRect,
  overlayRect,
  readTrackingOverlayPreference,
  trackingBoxAt,
  writeTrackingOverlayPreference,
  TRACKING_OVERLAY_STORAGE_KEY,
} from "../src/history/trackingBoxes";
import type { ChunkAnalysisResult } from "../src/api/history";

const chunk = (overrides: Partial<ChunkAnalysisResult> & { target_cues?: unknown }): ChunkAnalysisResult =>
  ({
    id: 1,
    session_id: "WOD-20261026-01M5TWZSMV81CW4DPHQY10DW75",
    profile_id: 1,
    status: "COMPLETED",
    output: "",
    heart_rate_bpm: 0,
    created_at: "",
    start_secs: 1000,
    end_secs: 1010,
    media_start_secs: 100,
    media_end_secs: 110,
    target_cues: JSON.stringify({
      matched_cues: [],
      ambiguous: false,
      confidence: 0.9,
      boxes: [
        { t: 1, box_2d: [100, 200, 900, 400] },
        { t: 9, box_2d: [100, 600, 900, 800] },
      ],
    }),
    ...overrides,
  }) as ChunkAnalysisResult;

describe("tracking box overlay helpers", () => {
  it("maps chunk-local box times onto the merged-media timeline and interpolates within a chunk", () => {
    const timeline = buildTrackingTimeline([chunk({})]);
    expect(timeline).toHaveLength(1);
    expect(timeline[0].samples.map(s => s.media_secs)).toEqual([101, 109]);
    expect(trackingBoxAt(timeline, 100.5)).toEqual([100, 200, 900, 400]);
    expect(trackingBoxAt(timeline, 105)).toEqual([100, 400, 900, 600]);
    expect(trackingBoxAt(timeline, 109.5)).toEqual([100, 600, 900, 800]);
    expect(trackingBoxAt(timeline, 110)).toBeNull();
    expect(trackingBoxAt(timeline, 99)).toBeNull();
  });

  it("scales by the chunk's capture duration when media and capture lengths differ", () => {
    const timeline = buildTrackingTimeline([chunk({ start_secs: 0, end_secs: 20 })]);
    expect(timeline[0].samples.map(s => s.media_secs)).toEqual([100.5, 104.5]);
  });

  it("ignores chunks without verified media offsets, uncertain targets and malformed boxes", () => {
    expect(buildTrackingTimeline([chunk({ media_start_secs: null })])).toEqual([]);
    expect(buildTrackingTimeline([chunk({ status: "FAILED" })])).toEqual([]);
    expect(buildTrackingTimeline([chunk({ target_cues: JSON.stringify({ ambiguous: true, confidence: 0.9, boxes: [{ t: 1, box_2d: [1, 2, 3, 4] }] }) })])).toEqual([]);
    expect(buildTrackingTimeline([chunk({ target_cues: JSON.stringify({ confidence: 0.5, boxes: [{ t: 1, box_2d: [1, 2, 3, 4] }] }) })])).toEqual([]);
    expect(buildTrackingTimeline([chunk({ target_cues: JSON.stringify({ confidence: 0.9, boxes: [{ t: 1, box_2d: [500, 2, 300, 4] }, { t: 2, box_2d: [1, 2, 3] }] }) })])).toEqual([]);
    expect(buildTrackingTimeline([chunk({ target_cues: "not json" })])).toEqual([]);
  });

  it("places normalized boxes inside the letterboxed video area", () => {
    const rect = containedVideoRect(800, 600, 1920, 1080)!;
    expect(rect.left).toBe(0);
    expect(rect.width).toBe(800);
    expect(rect.height).toBe(450);
    expect(rect.top).toBe(75);
    expect(overlayRect([0, 500, 1000, 1000], rect)).toEqual({ left: 400, top: 75, width: 400, height: 450 });
    expect(containedVideoRect(800, 600, 0, 0)).toBeNull();
  });

  it("persists the overlay toggle and defaults to off", () => {
    const store = new Map<string, string>();
    const storage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) };
    expect(readTrackingOverlayPreference(storage)).toBe(false);
    writeTrackingOverlayPreference(true, storage);
    expect(store.get(TRACKING_OVERLAY_STORAGE_KEY)).toBe("on");
    expect(readTrackingOverlayPreference(storage)).toBe(true);
  });
});
