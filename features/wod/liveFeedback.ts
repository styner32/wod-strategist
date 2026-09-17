import type { ChunkAnalysisResult } from "./api";

export function latestCaptureResult(
  previous: ChunkAnalysisResult | null,
  rows: ChunkAnalysisResult[],
  sessionId: string,
): ChunkAnalysisResult | null {
  const ordered = rows.filter(row => row.session_id === sessionId &&
    Number.isFinite(row.start_secs) && Number.isFinite(row.end_secs) &&
    row.start_secs! >= 0 && row.end_secs! > row.start_secs!).sort((a, b) =>
    b.end_secs! - a.end_secs! || b.start_secs! - a.start_secs! ||
    Number(b.status === "COMPLETED") - Number(a.status === "COMPLETED") || a.id - b.id);
  const candidate = ordered[0];
  if (previous?.session_id !== sessionId) previous = null;
  if (!candidate) return previous;
  if (previous && candidate.end_secs! < previous.end_secs!) return previous;
  if (previous?.id === candidate.id && Date.parse(candidate.updated_at) < Date.parse(previous.updated_at)) return previous;
  return candidate;
}

export function captureAdviceAt(chunk: ChunkAnalysisResult | null, elapsedSeconds: number, locale: string): string | null {
  if (!chunk || chunk.status !== "COMPLETED" || !Number.isFinite(chunk.end_secs)) return null;
  const age = elapsedSeconds - chunk.end_secs!;
  if (age < 0 || age > 30 || chunk.capture_assessment?.state !== "needs_adjustment") return null;
  return (locale === "ko" ? chunk.capture_assessment.advice.ko : chunk.capture_assessment.advice.en) || null;
}

export function coachingText(chunk: ChunkAnalysisResult | null, locale: string): string | null {
  if (!chunk || chunk.status !== "COMPLETED") return null;
  const contextual = chunk.contextual_coaching;
  if (contextual?.current_chunk_id === chunk.id) return (locale === "ko" ? contextual.text.ko : contextual.text.en) || chunk.output || null;
  return chunk.output || null;
}
