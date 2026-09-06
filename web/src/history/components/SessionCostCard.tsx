import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { historyApi } from "../../api/history";

interface SessionCostCardProps {
  sessionId: string;
}

export function SessionCostCard({ sessionId }: SessionCostCardProps) {
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

      {showDetails && (
        <div className="mt-4 space-y-3 border-t border-border pt-3">
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
}
