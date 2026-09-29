import { useEffect, useId, useMemo, useState } from "react";
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { AnalysisResult } from "../../api/history";
import {
  enrichmentPending,
  getEnrichment,
  startEnrichment,
  type AgenticObservation,
} from "../../api/enrichment";
import {
  analysisSegments,
  legacyOverallSummary,
} from "../analysisPresentation";
import {
  formatHighlightTimestamp,
  parseHighlightSegments,
  parseHighlightTimestamp,
} from "../highlights";
import { AnalysisMarkdown, AnalysisOriginal } from "./AnalysisMarkdown";

const statuses: Record<string, string> = {
  pending: "대기 중",
  preparing: "영상 준비 중",
  running: "추가 분석 중",
  completed: "완료",
  partial: "일부 실패",
  failed: "실패",
  interrupted: "중단됨",
  stale: "원본 변경됨",
};
const classification: Record<string, string> = {
  best_form: "좋은 동작",
  worst_form: "확인할 자세",
  mixed_form: "복합 관찰",
  fatigue_point: "피로 징후",
  key_moment: "주요 장면",
};
const button =
  "rounded-lg border border-border px-3 py-2 text-sm text-accent hover:bg-accent/10 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40";
function AdditionalObservation({
  observation,
  seek,
  canSeek,
}: {
  observation: AgenticObservation;
  seek: (time: number) => void;
  canSeek: boolean;
}) {
  const target: Record<string, string> = {
    confirmed: "대상 식별됨",
    unclear: "대상 불명확",
    absent: "대상 보이지 않음",
  };
  const activity: Record<string, string> = {
    exercise: "운동 관찰",
    none: "운동 없음",
    unclear: "운동 여부 불명확",
  };
  return (
    <div className="space-y-3">
      <p className="text-xs text-text-secondary">
        {target[observation.target_status]} · {activity[observation.activity]}
      </p>
      <p className="font-medium">
        {observation.movement === "Unknown"
          ? "운동명 불명확"
          : observation.movement}
      </p>
      <AnalysisMarkdown text={observation.direct_observation} />
      <AnalysisMarkdown text={observation.continuity} />
      {observation.evidence?.map((e, i) => (
        <div key={i}>
          <button
            className={button}
            disabled={!canSeek}
            onClick={() => seek(e.start)}
          >
            {formatHighlightTimestamp(e.start)}–
            {formatHighlightTimestamp(e.end)} 이동
          </button>
          <AnalysisMarkdown text={e.observation} />
        </div>
      ))}
      {observation.noteworthy?.map((t, i) => (
        <AnalysisMarkdown key={i} text={t} />
      ))}
      {observation.limitations?.length > 0 && (
        <div className="rounded-lg bg-bg-secondary p-3">
          <h5 className="text-sm font-medium">관찰 한계</h5>
          {observation.limitations.map((t, i) => (
            <AnalysisMarkdown key={i} text={t} />
          ))}
        </div>
      )}
    </div>
  );
}
export function AnalysisOverview({
  analysis,
  seek,
  seekHighlight,
  canSeek,
}: {
  analysis: AnalysisResult;
  /** Seeks to an exact timestamp (observations and evidence). */
  seek: (time: number) => void;
  /** Seeks to a highlight start, applying the legacy pre-roll. */
  seekHighlight: (startSeconds: number, version?: number) => void;
  canSeek: boolean;
}) {
  const [open, setOpen] = useState<string | null>(null);
  const id = useId();
  const client = useQueryClient();
  // Not keyed on updated_at: unrelated row writes must not reset the query.
  // Callers that change the output invalidate ["analysis-enrichment", sessionId].
  const key = [
    "analysis-enrichment",
    analysis.session_id,
    analysis.highlight_segments,
  ];
  const query = useQuery({
    queryKey: key,
    queryFn: () => getEnrichment(analysis.session_id),
    enabled: analysis.status.toLowerCase() === "completed",
    placeholderData: keepPreviousData,
    retry: false,
    refetchInterval: (q) =>
      enrichmentPending(q.state.data?.analysis.status) ||
      enrichmentPending(q.state.data?.summary.status)
        ? 3000
        : false,
  });
  const mutation = useMutation({
    mutationFn: (agentic: boolean) =>
      startEnrichment(analysis.session_id, agentic),
    onSuccess: () => client.invalidateQueries({ queryKey: key }),
  });
  const summary = query.data?.summary ?? analysis.analysis_summary;
  const content = summary?.result ?? summary?.last_success;
  const legacy = legacyOverallSummary(analysis.output);
  const batch = query.data?.analysis;
  const costRevision = JSON.stringify([
    summary?.run_id, summary?.status, summary?.updated_at,
    batch?.run_id, batch?.status,
    batch?.items?.map((item) => [item.key, item.status]),
  ]);
  const hasEnrichment = Boolean(query.data);
  useEffect(() => {
    if (!hasEnrichment) return;
    void client.invalidateQueries({ queryKey: ["session-cost", analysis.session_id] });
    void client.invalidateQueries({ queryKey: ["total-cost"] });
  }, [client, analysis.session_id, costRevision, hasEnrichment]);
  const highlights = useMemo(
    () => parseHighlightSegments(analysis.highlight_segments),
    [analysis.highlight_segments],
  );
  const segments = useMemo(
    () => analysisSegments(analysis.output),
    [analysis.output],
  );
  return (
    <div className="min-w-0 space-y-6">
      <section aria-labelledby={`${id}-summary`} className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h3 id={`${id}-summary`} className="text-lg font-semibold">
            전체 요약
          </h3>
          <span className="text-xs text-text-secondary">
            {enrichmentPending(batch?.status)
              ? "추가 분석 중"
              : summary?.stage === "agentic" && summary.status === "completed"
                ? "Agentic 반영"
                : "기존 분석 기준"}
          </span>
        </div>
        {content ? (
          <div className="space-y-5">
            <AnalysisMarkdown text={content.overview} />
            {(
              [
                ["주요 강점", content.strengths],
                ["개선 및 확인 사항", content.improvements],
                ["분석 한계", content.limitations],
              ] as const
            ).map(
              ([title, values]) =>
                values?.length > 0 && (
                  <div key={title}>
                    <h4 className="mb-2 font-semibold">{title}</h4>
                    <ul className="space-y-2">
                      {values.map((value, i) => (
                        <li key={i}>
                          <AnalysisMarkdown text={value} />
                        </li>
                      ))}
                    </ul>
                  </div>
                ),
            )}
          </div>
        ) : legacy ? (
          <>
            <span className="text-xs text-text-secondary">
              기존 JSON의 임시 요약
            </span>
            <AnalysisMarkdown text={legacy} />
          </>
        ) : (
          <p className="text-sm text-text-secondary">
            전체 요약이 아직 없습니다. 기존 분석은 아래 원문에서 확인할 수
            있습니다.
          </p>
        )}
        {summary?.updated_at && (
          <p className="mt-3 text-xs text-text-secondary">
            갱신 {new Date(summary.updated_at).toLocaleString()}
          </p>
        )}
        {summary?.error && (
          <p role="status" className="mt-2 text-sm text-warning">
            {summary.error}
          </p>
        )}
        {!!summary?.failed && (
          <p className="mt-2 text-sm text-warning">
            추가 분석 {summary.failed}개 구간은 완료되지 않았습니다. 기존 관찰의
            정확성이 확인된 것은 아닙니다.
          </p>
        )}
        <button
          className={`${button} mt-3`}
          disabled={
            mutation.isPending ||
            enrichmentPending(summary?.status) ||
            analysis.status.toLowerCase() !== "completed"
          }
          onClick={() => mutation.mutate(false)}
        >
          {enrichmentPending(summary?.status)
            ? "요약 생성 중"
            : content
              ? "요약 재생성"
              : "요약 생성"}
        </button>
      </section>
      <section
        aria-labelledby={`${id}-highlights`}
        className="min-w-0 border-t border-border pt-5"
      >
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h3 id={`${id}-highlights`} className="text-lg font-semibold">
            Highlights{" "}
            <span className="text-sm text-text-secondary">{highlights.length}</span>
          </h3>
          {query.data?.enabled && (
            <button
              className={button}
              disabled={
                mutation.isPending ||
                enrichmentPending(batch?.status) ||
                !highlights.length
              }
              onClick={() => mutation.mutate(true)}
            >
              {enrichmentPending(batch?.status)
                ? "추가 분석 중"
                : batch?.run_id
                  ? "Agentic 재분석"
                  : "Agentic 추가 분석"}
            </button>
          )}
        </div>
        <p className="mb-4 text-xs leading-relaxed text-text-secondary">
          기존 관찰과 독립적인 추가 관찰을 비교합니다. 카드를 펼쳐도 영상은
          재생되지 않습니다.
        </p>
        {batch?.error && (
          <p className="mb-3 text-sm text-warning">{batch.error}</p>
        )}
        <div className="space-y-3">
          {highlights.map((h, index) => {
            const item =
              batch?.status !== "stale"
                ? batch?.items?.find(
                    (it) =>
                      parseHighlightTimestamp(it.highlight.start) ===
                        h.startSeconds &&
                      parseHighlightTimestamp(it.highlight.end) ===
                        h.endSeconds,
                  )
                : undefined;
            const cardKey = `${analysis.highlight_segments}:${index}`;
            const expanded = open === cardKey;
            const observation = item?.result ?? item?.last_success;
            const connected = segments.filter(
              (s) => s.start <= h.startSeconds && s.end >= h.endSeconds,
            );
            return (
              <article
                key={cardKey}
                className="min-w-0 overflow-hidden rounded-xl border border-border"
              >
                <h4>
                  <button
                    type="button"
                    className="flex w-full items-center justify-between gap-3 p-4 text-left focus-visible:outline-2 focus-visible:outline-accent"
                    aria-expanded={expanded}
                    aria-controls={`${id}-highlight-${index}`}
                    onClick={() => setOpen(expanded ? null : cardKey)}
                  >
                    <span className="min-w-0">
                      <span className="block font-semibold">
                        {h.movement || "운동명 미확인"}
                      </span>
                      <span className="mt-1 block text-xs text-text-secondary">
                        {h.startLabel}–{h.endLabel} ·{" "}
                        {classification[h.type] ?? h.type} · Agentic{" "}
                        {statuses[item?.status ?? ""] ?? "미실행"}
                      </span>
                    </span>
                    <span aria-hidden>{expanded ? "−" : "+"}</span>
                  </button>
                </h4>
                <div
                  id={`${id}-highlight-${index}`}
                  hidden={!expanded}
                  className="min-w-0 border-t border-border p-4"
                >
                  <div className="grid min-w-0 gap-5 xl:grid-cols-2">
                    <section className="min-w-0">
                      <h5 className="mb-3 font-semibold">기존 관찰</h5>
                      <AnalysisMarkdown
                        text={h.reason ?? "기존 관찰 설명 없음"}
                      />
                      {h.observations?.map((o, i) => (
                        <div key={i} className="mt-3">
                          <button
                            className={button}
                            disabled={!canSeek}
                            onClick={() => seek(o.startSeconds)}
                          >
                            {o.startLabel}–{o.endLabel} 이동
                          </button>
                          <AnalysisMarkdown text={o.reason ?? ""} />
                        </div>
                      ))}
                    </section>
                    <section className="min-w-0">
                      <h5 className="mb-3 font-semibold">Agentic 추가 관찰</h5>
                      {item?.error && (
                        <p className="mb-3 text-sm text-warning">
                          {item.error}
                        </p>
                      )}
                      {observation ? (
                        <>
                          {item?.status !== "completed" && (
                            <p className="mb-3 text-xs text-text-secondary">
                              마지막 성공 결과
                            </p>
                          )}
                          <AdditionalObservation
                            observation={observation}
                            seek={seek}
                            canSeek={canSeek}
                          />
                        </>
                      ) : (
                        <p className="text-sm text-text-secondary">
                          {statuses[item?.status ?? ""] ??
                            "아직 추가 분석하지 않았습니다."}
                        </p>
                      )}
                      {item?.metrics && (
                        <details className="mt-4 text-xs text-text-secondary">
                          <summary className="cursor-pointer">
                            처리 정보
                          </summary>
                          <p>
                            처리 시간 {item.metrics.elapsed_seconds?.toFixed(1)}
                            초 · 종료 {item.metrics.finish_reason || "미확인"}
                          </p>
                          <p>
                            MEDIA_PROCESSING 탐색{" "}
                            {item.metrics.agentic_observed
                              ? "확인"
                              : "확인되지 않음"}
                          </p>
                          {Object.entries(item.metrics.usage ?? {}).map(
                            ([k, v]) => (
                              <p key={k}>
                                {k}: {v ?? "미제공"}
                              </p>
                            ),
                          )}
                        </details>
                      )}
                    </section>
                  </div>
                  {connected.length === 1 ? (
                    <section className="mt-6 border-t border-border pt-4">
                      <h5 className="mb-3 font-semibold">
                        관련 세그먼트 분석 · {connected[0].title}
                      </h5>
                      <AnalysisMarkdown text={connected[0].body} />
                    </section>
                  ) : (
                    <p className="mt-4 text-xs text-text-secondary">
                      명확히 연결되는 세그먼트 본문이 없습니다. 원문을
                      확인하세요.
                    </p>
                  )}
                  <button
                    className={`${button} mt-4`}
                    disabled={!canSeek}
                    onClick={() => seekHighlight(h.startSeconds, h.version)}
                  >
                    이 구간으로 영상 이동
                  </button>
                </div>
              </article>
            );
          })}
        </div>
        {!highlights.length && (
          <p className="text-sm text-text-secondary">
            저장된 하이라이트가 없습니다.
          </p>
        )}
      </section>
      {mutation.isError && (
        <p role="alert" className="text-sm text-error">
          {mutation.error.message}
        </p>
      )}
      {query.isError && (
        <p role="status" className="text-sm text-warning">
          추가 분석 상태를 불러오지 못했습니다.{" "}
          <button className={button} onClick={() => void query.refetch()}>
            다시 조회
          </button>
        </p>
      )}
      <AnalysisOriginal text={analysis.output} />
    </div>
  );
}
