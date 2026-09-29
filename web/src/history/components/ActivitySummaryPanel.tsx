import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { activityReviewPending, activitySummaryPath, type ActivitySummary } from "../../../../shared/activity";

const labels: Record<ActivitySummary["review_state"], string> = {
  disabled: "Review disabled — provisional observations", unavailable: "Not evaluated", provisional: "Provisional observations", queued: "Review queued", running: "Review in progress",
  completed: "Review complete", partial: "Review with unresolved intervals", failed: "Review failed — provisional observations",
};

const clockLabels = { capture: "Recording elapsed time", media: "Video time", chunk: "Time within source clip" };
const gapReasons: Record<string, string> = {
  capture_gap: "Recording gap",
  review_failed: "Review unavailable for this interval",
  analysis_incomplete: "Analysis is incomplete",
  incomplete_cycle: "Only part of a repetition is visible",
  occlusion: "View obstructed",
  unsupported_movement_or_unit: "Movement could not be counted",
  unassessable_target_or_activity: "The athlete or movement is unclear",
};

export function ActivitySummaryPanel({ sessionId, profileId }: { sessionId: string; profileId: number }) {
  const { data } = useQuery({
    queryKey: ["activity-summary", profileId, sessionId],
    queryFn: () => api.get<ActivitySummary>(activitySummaryPath(sessionId, profileId)),
    enabled: !!sessionId && profileId > 0,
    refetchInterval: query => activityReviewPending(query.state.data) ? 5000 : false,
  });
  if (!data?.available) return null;
  return <section className="rounded-xl border border-slate-700 bg-slate-900 p-4 text-slate-100">
    <h2 className="font-semibold">Observed workout activity</h2>
    <p className="text-sm text-sky-300">{labels[data.review_state]}</p>
    <ul className="mt-2 space-y-1">{data.movements.map(item => <li key={`${item.movement}/${item.unit}`}>
      {item.movement}: {item.unit === "reps" ? `${item.count} observed repetitions` : `${Math.round(item.seconds * 10) / 10} observed seconds`}
    </li>)}</ul>
    {!data.movements.length && <p>No countable observations yet.</p>}
    {data.unassessed.length > 0 && <details className="mt-2 text-amber-300"><summary>{data.unassessed.length} unassessed intervals</summary>
      <ul>{data.unassessed.map((gap, index) => <li key={index}>{clockLabels[gap.clock]}: {gap.start_secs == null || gap.end_secs == null ? "Time unknown" : `${gap.start_secs.toFixed(1)}–${gap.end_secs.toFixed(1)}s`} · {gapReasons[gap.reason] ?? "This interval could not be assessed"}</li>)}</ul>
    </details>}
    <p className="mt-2 text-xs text-slate-400">Recorded observations only. Review does not guarantee an exact total or judge competition-valid repetitions.</p>
  </section>;
}
