import type { RecognitionRecordEvaluation } from "../api/history";

const maxWindowSeconds = 150;
const internalContextSeconds = 5;

/** Mirrors the worker's 150-second core windows with five seconds on each internal side. */
export function recognitionReviewInputSeconds(rows: RecognitionRecordEvaluation[]): number {
  return rows.reduce((seconds, { record }) => {
    const duration = record.end_secs - record.start_secs;
    if (!Number.isFinite(duration) || duration <= 0) return seconds;
    return seconds + duration + 2 * internalContextSeconds * (Math.ceil(duration / maxWindowSeconds) - 1);
  }, 0);
}

export function recognitionReviewSelection(
  rows: RecognitionRecordEvaluation[],
  selectedIds: number[],
  start: number | null,
  end: number | null,
) {
  const valid = start != null && end != null && Number.isFinite(start) && Number.isFinite(end) && start >= 0 && end > start;
  const selected = new Set(selectedIds);
  const eligible = valid ? rows.filter(({ record }) => record.start_secs >= start! && record.end_secs <= end!) : [];
  const eligibleIds = new Set(eligible.map(({ record }) => record.id));
  const records = eligible.filter(({ record }) => selected.has(record.id));
  return {
    valid,
    eligibleIds,
    records,
    excludedCount: rows.filter(({ record }) => selected.has(record.id) && !eligibleIds.has(record.id)).length,
    inputSeconds: recognitionReviewInputSeconds(records),
  };
}
