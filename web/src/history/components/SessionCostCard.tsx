import { memo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { historyApi } from "../../api/history";

export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "0초";
  const totalSeconds = Math.max(1, Math.round(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}시간 ${minutes}분 ${seconds}초`;
  }
  if (minutes > 0) {
    return seconds > 0 ? `${minutes}분 ${seconds}초` : `${minutes}분`;
  }
  return `${seconds}초`;
}

const STAGE_LABELS: Record<string, string> = {
  video_analysis: "본 분석 (Pass 1 & 2)",
  injury_analysis: "부상 위험 분석",
  verify_highlights: "하이라이트 검증",
  session_reanalysis: "전체 재분석",
  chunk_reanalysis: "청크 재분석",
};

interface SessionCostCardProps {
  sessionId: string;
  fallbackDurationMs?: number | null;
  isProcessing?: boolean;
}

export const SessionCostCard = memo(function SessionCostCard({
  sessionId,
  fallbackDurationMs,
  isProcessing = false,
}: SessionCostCardProps) {
  const [showDetails, setShowDetails] = useState(false);

  const { data: cost, isLoading, error } = useQuery({
    queryKey: ["session-cost", sessionId],
    queryFn: () => historyApi.getSessionCost(sessionId),
    retry: false,
    staleTime: 30000,
  });

  if (isLoading) {
    return (
      <div className="rounded-xl border border-border bg-bg-elevated p-4 animate-pulse">
        <div className="h-4 bg-bg-tertiary rounded w-1/3 mb-3" />
        <div className="h-7 bg-bg-tertiary rounded w-1/2 mb-2" />
        <div className="h-3 bg-bg-tertiary rounded w-2/3" />
      </div>
    );
  }

  if (error || !cost) {
    return null;
  }

  const formatTokens = (n: number) => n.toLocaleString();
  const formatUSD = (usd: number) => `$${usd.toFixed(4)}`;
  const formatKRW = (krw: number) => `₩${Math.round(krw).toLocaleString()}`;
  const effectiveDurationMs =
    cost.total_duration_ms && cost.total_duration_ms > 0
      ? cost.total_duration_ms
      : fallbackDurationMs && fallbackDurationMs > 0
        ? fallbackDurationMs
        : 0;

  return (
    <section
      className="rounded-xl border border-border bg-bg-elevated p-4 text-xs transition-all"
      aria-labelledby="session-cost-heading"
    >
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-base" role="img" aria-label="cost">
            🪙
          </span>
          <h2 id="session-cost-heading" className="text-sm font-semibold text-text-primary">
            AI 분석 비용
          </h2>
        </div>
        <button
          type="button"
          onClick={() => setShowDetails(!showDetails)}
          className="text-[11px] font-medium text-accent hover:underline focus:outline-none"
        >
          {showDetails ? "상세 접기" : "상세 내역 보기"}
        </button>
      </div>

      <div className="mt-3 flex items-baseline gap-2">
        <span className="text-xl font-bold text-text-primary">
          {formatUSD(cost.cost_usd)}
        </span>
        <span className="text-sm font-medium text-text-muted">
          ({formatKRW(cost.cost_krw)})
        </span>
      </div>

      {effectiveDurationMs > 0 && (
        <div
          className="mt-2 flex items-center justify-between rounded-lg bg-bg-secondary px-2.5 py-1.5 text-[11px]"
          data-testid="session-total-duration"
        >
          <span className="text-text-muted">
            ⏱️ {isProcessing ? "현재 분석 경과 시간" : "총 분석 소요 시간"}
          </span>
          <span className="font-semibold text-text-primary">
            {formatDurationMs(effectiveDurationMs)}
          </span>
        </div>
      )}

      <div className="mt-2 grid grid-cols-3 gap-2 border-t border-border-subtle pt-2 text-[11px] text-text-secondary">
        <div>
          <span className="text-text-muted block">총 토큰</span>
          <span className="font-medium text-text-primary">
            {formatTokens(cost.total_tokens)}
          </span>
        </div>
        <div>
          <span className="text-text-muted block">입력 (Prompt)</span>
          <span className="font-medium text-text-primary">
            {formatTokens(cost.prompt_tokens)}
          </span>
        </div>
        <div>
          <span className="text-text-muted block">출력 (Candidate)</span>
          <span className="font-medium text-text-primary">
            {formatTokens(cost.candidate_tokens)}
          </span>
        </div>
      </div>

      <p className="mt-2 text-[11px] text-text-muted">
        Thinking {formatTokens(cost.thinking_tokens ?? 0)} · 도구 입력 {formatTokens(cost.tool_use_tokens ?? 0)} · 캐시 {formatTokens(cost.cached_tokens ?? 0)}
      </p>
      {!!cost.unmeasured_calls && (
        <p className="mt-2 text-[11px] text-text-muted">
          일시 응답 지연 등으로 토큰 수가 측정되지 않은 요청 {cost.unmeasured_calls}건 제외 (정상 완료된 요청 기준 추정)
        </p>
      )}
      {!!cost.unpriced_calls && <p className="mt-2 text-xs text-warning">별도 과금 모델 {cost.unpriced_calls}건은 금액에 포함되지 않았습니다.</p>}
      <p className="mt-2 text-[11px] text-text-muted">앱에 설정된 단가 기준 추정 비용입니다. 실제 청구 금액과 다를 수 있습니다.</p>

      {showDetails && (
        <div className="mt-4 space-y-3 border-t border-border pt-3">
          {cost.by_stage && cost.by_stage.length > 0 && (
            <div>
              <h3 className="font-semibold text-text-secondary mb-1.5">
                단계별 소요 시간
              </h3>
              <div className="space-y-1.5">
                {cost.by_stage.map((item) => (
                  <div
                    key={item.stage}
                    className="flex items-center justify-between rounded bg-bg-secondary px-2 py-1.5"
                  >
                    <span className="text-text-primary text-[11px]">
                      {STAGE_LABELS[item.stage] ?? item.stage}
                    </span>
                    <div className="text-right">
                      <span className="text-text-primary font-medium">
                        {formatDurationMs(item.duration_ms)}
                      </span>
                      {item.api_calls > 0 && (
                        <span className="text-text-muted ml-1.5">
                          ({item.api_calls}회 호출)
                        </span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {cost.by_task_type && cost.by_task_type.length > 0 && (
            <div>
              <h3 className="font-semibold text-text-secondary mb-1.5">
                작업 단계별 비용
              </h3>
              <div className="space-y-1.5">
                {cost.by_task_type.map((item) => (
                  <div
                    key={item.key}
                    className="flex items-center justify-between rounded bg-bg-secondary px-2 py-1.5"
                  >
                    <span className="font-mono text-text-primary text-[11px]">
                      {item.key}
                    </span>
                    <div className="text-right">
                      <span className="text-text-primary font-medium">
                        {formatUSD(item.cost_usd)}
                      </span>
                      <span className="text-text-muted ml-1.5">
                        ({formatTokens(item.total_tokens)} tok)
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {cost.by_model && cost.by_model.length > 0 && (
            <div>
              <h3 className="font-semibold text-text-secondary mb-1.5">
                모델별 사용량
              </h3>
              <div className="space-y-1.5">
                {cost.by_model.map((item) => (
                  <div
                    key={item.key}
                    className="flex items-center justify-between rounded bg-bg-secondary px-2 py-1.5"
                  >
                    <span className="font-mono text-text-primary text-[11px]">
                      {item.key}
                    </span>
                    <div className="text-right">
                      <span className="text-text-primary font-medium">
                        {formatUSD(item.cost_usd)}
                      </span>
                      <span className="text-text-muted ml-1.5">
                        ({formatTokens(item.total_tokens)} tok)
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </section>
  );
});

