import type { AnalysisResult, ChunkAnalysisResult, StretchRecommendation } from "../../api/history";
import { historyStatusLabel } from "../historyArchive";
import { AnalysisOverview } from "./AnalysisOverview";

/** Stored evidence stays inspectable while archived; computation controls are unavailable. */
export function ArchivedSessionContent({ analysis, chunks, seek, seekHighlight, canSeek }: {
  analysis: AnalysisResult;
  chunks: ChunkAnalysisResult[];
  seek: (seconds: number) => void;
  seekHighlight: (seconds: number, version?: number) => void;
  canSeek: boolean;
}) {
  let stretches: StretchRecommendation[] = [];
  try {
    const parsed = JSON.parse(analysis.stretch_recommendations || "[]");
    if (Array.isArray(parsed)) stretches = parsed.filter(item => typeof item?.stretch === "string" && typeof item?.reason === "string");
  } catch { /* Older malformed recommendations do not prevent reading the analysis. */ }
  return <div className="space-y-5">
    <section className="rounded-xl border border-border bg-bg-elevated p-4">
      <h2 className="font-semibold text-text-primary">보관한 운동 기록</h2>
      <p className="mt-2 break-all text-xs text-text-muted">{analysis.session_id}</p>
      <p className="mt-2 text-sm text-text-secondary">상태: {historyStatusLabel(analysis.status)}</p>
      {analysis.wod_description && <p className="mt-3 whitespace-pre-wrap text-sm text-text-primary">{analysis.wod_description}</p>}
    </section>
    {analysis.status === "COMPLETED"
      ? <AnalysisOverview analysis={analysis} seek={seek} seekHighlight={seekHighlight} canSeek={canSeek} readOnly />
      : <section className="rounded-xl border border-border bg-bg-elevated p-4">
          <h2 className="mb-2 font-semibold text-text-primary">저장된 분석 원문</h2>
          <p className="whitespace-pre-wrap text-sm text-text-secondary">{analysis.output || "저장된 분석 결과가 없습니다."}</p>
        </section>}
    {chunks.length > 0 && <section className="rounded-xl border border-border bg-bg-elevated p-4">
      <h2 className="mb-3 font-semibold text-text-primary">저장된 구간 분석</h2>
      {chunks.map(chunk => <details key={chunk.id} className="border-t border-border py-3">
        <summary className="cursor-pointer text-sm text-text-secondary">구간 {chunk.id} · {historyStatusLabel(chunk.status)}</summary>
        <p className="mt-2 whitespace-pre-wrap text-sm text-text-secondary">{chunk.output || "저장된 결과가 없습니다."}</p>
      </details>)}
    </section>}
    {stretches.length > 0 && <section className="rounded-xl border border-border bg-bg-elevated p-4">
      <h2 className="mb-3 font-semibold text-text-primary">저장된 스트레칭</h2>
      {stretches.map((stretch, index) => <article key={`${stretch.stretch}:${index}`} className="border-t border-border py-3 text-sm">
        <h3 className="font-medium text-text-primary">{stretch.stretch}</h3>
        <p className="mt-1 text-text-secondary">{stretch.reason}</p>
        {stretch.duration_hint && <p className="mt-1 text-text-muted">{stretch.duration_hint}</p>}
        {stretch.caution && <p className="mt-1 text-text-secondary">{stretch.caution}</p>}
      </article>)}
    </section>}
  </div>;
}
