import { HeartRateBadge } from "./components/HeartRateSummaryPanel";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { suggestWODEffort } from "../../../shared/wodTraining";
import { historyApi, type AnalysisResult, type HistoryMissingFilter } from "../api/history";
import { useAuth } from "../auth/useAuth";
import { HistoryArchiveAction } from "./components/HistoryArchiveAction";
import { historyStatusLabel } from "./historyArchive";

function HistoryCardSkeleton() {
  return (
    <div className="bg-bg-elevated border border-border rounded-xl p-5 animate-pulse">
      <div className="flex items-start justify-between mb-4">
        <div className="flex items-center gap-3 w-full">
          <div className="w-10 h-10 rounded-lg bg-bg-tertiary" />
          <div className="flex-1">
            <div className="h-4 bg-bg-tertiary rounded w-2/3 mb-2" />
            <div className="h-3 bg-bg-tertiary rounded w-1/3" />
          </div>
        </div>
        <div className="w-16 h-5 bg-bg-tertiary rounded-full" />
      </div>
      <div className="flex gap-2 mb-3">
        <div className="h-5 bg-bg-tertiary rounded w-16" />
        <div className="h-5 bg-bg-tertiary rounded w-16" />
      </div>
      <div className="space-y-2">
        <div className="h-3.5 bg-bg-tertiary rounded w-full" />
        <div className="h-3.5 bg-bg-tertiary rounded w-5/6" />
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    completed: "bg-success/10 text-success border-success/20",
    processing: "bg-warning/10 text-warning border-warning/20",
    failed: "bg-error/10 text-error border-error/20",
    pending: "bg-info/10 text-info border-info/20",
    cancelled: "bg-bg-secondary text-text-muted border-border",
  };
  const style = styles[status.toLowerCase()] || styles.pending;
  return (
    <span
      className={`text-xs font-medium px-2.5 py-0.5 rounded-full border ${style}`}
    >
      {historyStatusLabel(status)}
    </span>
  );
}

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function parseAnalysisOutput(output: string): {
  summary?: string;
  workoutType?: string;
} {
  try {
    const parsed = JSON.parse(output);
    return {
      summary: parsed.overall_summary || parsed.summary,
      workoutType: parsed.workout_type,
    };
  } catch {
    return {};
  }
}

function HistoryMissingInputActions({ result }: { result: AnalysisResult }) {
  const queryClient = useQueryClient();
  const inputStatus = result.input_status;
  const effortMissing = Boolean(inputStatus?.effort_missing);
  const performanceMissing = Boolean(inputStatus?.performance_missing);
  const suggested = useMemo(
    () => (effortMissing ? suggestWODEffort(result.heart_rate, result.session_score) : null),
    [effortMissing, result.heart_rate, result.session_score],
  );
  const [selectedEffort, setSelectedEffort] = useState<number>();
  const effectiveEffort = selectedEffort ?? suggested?.effort;

  const saveEffort = useMutation({
    mutationFn: (effort: number) => historyApi.updateWODEffort(result.session_id, effort),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["history-infinite"] }),
        queryClient.invalidateQueries({ queryKey: ["wod-training"] }),
        queryClient.invalidateQueries({ queryKey: ["wod-training-load"] }),
      ]);
    },
  });

  if (!effortMissing && !performanceMissing) return null;

  const baseSessionPath = `/sessions/${encodeURIComponent(result.session_id)}?profile_id=${result.profile_id}`;

  return (
    <div className="border-t border-border bg-bg-secondary/40 px-5 py-3 space-y-3">
      {performanceMissing && inputStatus && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-xs text-text-secondary">
            {inputStatus.needs_confirmation_count > 0
              ? `재분석 후 수행구간 재확인 필요 (${inputStatus.confirmed_interval_count}/${inputStatus.exercise_interval_count} 확정)`
              : `수행구간 확정 필요 (${inputStatus.confirmed_interval_count}/${inputStatus.exercise_interval_count} 확정)`}
          </span>
          <Link
            to={`${baseSessionPath}&tab=performance`}
            className="text-xs font-semibold text-accent hover:underline"
          >
            수행구간 확정하기 →
          </Link>
        </div>
      )}

      {effortMissing && (
        <div className="space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="text-xs text-text-secondary">
              <span className="font-medium text-text-primary">Effort 입력</span>
              {suggested ? (
                <span className="ml-1.5 text-accent">
                  · 추천 {suggested.effort}/10 (
                  {suggested.source === "sensor" ? "심박·움직임 센서" : "AI 분석 강도"}{" "}
                  {suggested.rawIntensity}/100 기준)
                </span>
              ) : (
                <span className="ml-1.5 text-text-muted">· 체감 난이도 1~10</span>
              )}
            </div>
            <Link
              to={`${baseSessionPath}&tab=details`}
              className="text-xs text-text-secondary hover:text-accent hover:underline"
            >
              상세·부하 보기 →
            </Link>
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => {
              const isSelected = effectiveEffort === n;
              const isSuggested = suggested?.effort === n;
              return (
                <button
                  key={n}
                  type="button"
                  aria-label={`Effort ${n}`}
                  aria-pressed={isSelected}
                  disabled={saveEffort.isPending}
                  onClick={() => setSelectedEffort(n)}
                  className={`h-7 min-w-7 rounded border px-1.5 text-xs font-medium transition-colors disabled:opacity-40 ${
                    isSelected
                      ? "border-accent bg-accent text-white"
                      : isSuggested
                        ? "border-accent/60 text-accent hover:bg-accent/10"
                        : "border-border bg-bg-elevated text-text-secondary hover:bg-bg-tertiary hover:text-text-primary"
                  }`}
                >
                  {n}
                </button>
              );
            })}
            <button
              type="button"
              disabled={effectiveEffort == null || saveEffort.isPending}
              onClick={() => {
                if (effectiveEffort != null) {
                  saveEffort.mutate(effectiveEffort);
                }
              }}
              className="ml-auto rounded-md bg-accent px-2.5 py-1 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40 transition-colors"
            >
              {saveEffort.isPending ? "저장 중..." : "Effort 저장"}
            </button>
          </div>

          {saveEffort.isError && (
            <p role="alert" className="text-xs text-error">
              {saveEffort.error instanceof Error
                ? saveEffort.error.message
                : "Effort를 저장하지 못했습니다."}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

const HistoryCard = memo(function HistoryCard({ result, archiveView }: { result: AnalysisResult; archiveView: boolean }) {
  const parsed = parseAnalysisOutput(result.output || "{}");
  // `||`, not `??`: an empty overview must fall back to the legacy summary.
  parsed.summary = result.analysis_summary?.result?.overview || result.analysis_summary?.last_success?.overview || parsed.summary;
  const stretchCount = useMemo(() => {
    if (!result.stretch_recommendations) return 0;
    try {
      const p =
        typeof result.stretch_recommendations === "string"
          ? JSON.parse(result.stretch_recommendations)
          : result.stretch_recommendations;
      return Array.isArray(p) ? p.length : 0;
    } catch {
      return 0;
    }
  }, [result.stretch_recommendations]);
  const inputStatus = !archiveView ? result.input_status : undefined;
  const isCompleted = result.status.toUpperCase() === "COMPLETED";

  return (
    <article className="bg-bg-elevated border border-border rounded-xl hover:border-accent/40 transition-all duration-200 group">
    <Link
      to={`/sessions/${encodeURIComponent(result.session_id)}?profile_id=${result.profile_id}${archiveView ? "&archived=true" : ""}`}
      className="block p-5 hover:bg-bg-elevated/80"
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center">
            <svg
              className="w-5 h-5 text-accent"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={1.5}
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z"
              />
            </svg>
          </div>
          <div>
            <p className="font-medium text-text-primary group-hover:text-accent transition-colors">
              {result.session_id.slice(0, 20)}...
            </p>
            <p className="text-xs text-text-muted">
              {formatDate(result.workout_at || result.created_at)}
            </p>
          </div>
        </div>
        <StatusBadge status={result.status} />
      </div>

      <div className="flex flex-wrap items-center gap-1.5 mb-2">
        {result.analysis_summary?.status && (
          <span className="text-xs text-text-secondary">요약 {result.analysis_summary.status === "completed" ? "완료" : result.analysis_summary.status === "pending" || result.analysis_summary.status === "running" ? "생성 중" : "확인 필요"}</span>
        )}
        {parsed.workoutType && (
          <span className="inline-block text-xs bg-bg-secondary text-text-secondary px-2 py-0.5 rounded-md">
            {parsed.workoutType}
          </span>
        )}
        {isCompleted && inputStatus?.effort_eligible && (
          inputStatus.effort_missing ? (
            <span className="inline-flex items-center gap-1 text-xs bg-warning/15 text-warning border border-warning/30 px-2 py-0.5 rounded-md font-medium">
              Effort 미입력
            </span>
          ) : inputStatus.effort != null ? (
            <span className="inline-flex items-center gap-1 text-xs bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 px-2 py-0.5 rounded-md font-medium">
              Effort {inputStatus.effort}/10
            </span>
          ) : null
        )}
        {isCompleted && inputStatus && inputStatus.exercise_interval_count > 0 && (
          inputStatus.performance_missing ? (
            <span className="inline-flex items-center gap-1 text-xs bg-warning/15 text-warning border border-warning/30 px-2 py-0.5 rounded-md font-medium">
              {inputStatus.needs_confirmation_count > 0
                ? `수행 재확인 필요 (${inputStatus.confirmed_interval_count}/${inputStatus.exercise_interval_count})`
                : `수행구간 미확정 (${inputStatus.confirmed_interval_count}/${inputStatus.exercise_interval_count})`}
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 text-xs bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 px-2 py-0.5 rounded-md font-medium">
              수행 확정 ({inputStatus.confirmed_interval_count}/{inputStatus.exercise_interval_count})
            </span>
          )
        )}
        <HeartRateBadge summary={result.heart_rate} />
        {result.session_fatigue && (
          result.session_fatigue.status === "insufficient_evidence" ? (
            <span className="inline-flex items-center gap-1 text-xs bg-bg-secondary text-text-muted border border-border px-2 py-0.5 rounded-md font-medium">
              <span>⚡</span> 분석 근거 부족
            </span>
          ) : result.session_fatigue.overall_score !== undefined && result.session_fatigue.overall_score !== null ? (
            <span className="inline-flex items-center gap-1 text-xs bg-amber-500/10 text-amber-400 border border-amber-500/20 px-2 py-0.5 rounded-md font-medium">
              <span>⚡</span>{" "}
              {result.session_fatigue.state_ko || result.session_fatigue.state}{" "}
              {result.session_fatigue.overall_score}
              {result.session_fatigue.status === "available" ? "/100" : "%"}
              {result.session_fatigue.heart_rate_adjusted && " (심박)"}
            </span>
          ) : null
        )}
        {stretchCount > 0 && (
          <span className="inline-flex items-center gap-1 text-xs bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 px-2 py-0.5 rounded-md font-medium">
            <span>🧘</span> {stretchCount} Stretches
          </span>
        )}
      </div>

      {result.wod_description?.trim() && (
        <div className="mb-2 rounded-lg border border-border/70 bg-bg-secondary/70 px-3 py-2">
          <p className="text-[11px] font-semibold uppercase tracking-wide text-text-muted">
            WOD
          </p>
          <p className="mt-0.5 text-xs text-text-primary line-clamp-2 whitespace-pre-wrap">
            {result.wod_description.trim()}
          </p>
        </div>
      )}

      {parsed.summary && (
        <p className="text-sm text-text-secondary line-clamp-2">
          {parsed.summary}
        </p>
      )}
    </Link>
    {!archiveView && isCompleted && <HistoryMissingInputActions result={result} />}
    <div className="flex justify-end border-t border-border px-5 py-3">
      <HistoryArchiveAction result={result} />
    </div>
    </article>
  );
});

export function HistoryListPage() {
  const { user } = useAuth();
  const profiles = user?.profiles || [];
  const [searchParams, setSearchParams] = useSearchParams();
  const archiveView = searchParams.get("archived") === "true";
  const rawMissing = searchParams.get("missing");
  const missingFilter: HistoryMissingFilter | undefined =
    !archiveView && (rawMissing === "any" || rawMissing === "effort" || rawMissing === "performance")
      ? rawMissing
      : undefined;
  const requestedProfileId = Number(searchParams.get("profile_id"));
  const selectedProfileId = profiles.find(profile => profile.id === requestedProfileId)?.id ?? profiles[0]?.id ?? null;
  const selectView = (profileId: number | null, archived: boolean) => {
    const next = new URLSearchParams(searchParams);
    if (profileId) next.set("profile_id", String(profileId));
    if (archived) {
      next.set("archived", "true");
      next.delete("missing");
    } else {
      next.delete("archived");
    }
    setSearchParams(next);
  };
  const selectMissingFilter = (nextMissing?: HistoryMissingFilter) => {
    const next = new URLSearchParams(searchParams);
    if (selectedProfileId) next.set("profile_id", String(selectedProfileId));
    next.delete("archived");
    if (nextMissing) next.set("missing", nextMissing);
    else next.delete("missing");
    setSearchParams(next);
  };

  const historyQueryKey = missingFilter
    ? ["history-infinite", selectedProfileId, "active", missingFilter]
    : ["history-infinite", selectedProfileId, archiveView ? "archived" : "active"];

  const {
    data,
    isLoading,
    error,
    fetchNextPage,
    refetch,
    hasNextPage,
    isFetching,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: historyQueryKey,
    queryFn: ({ pageParam }) =>
      missingFilter
        ? historyApi.list(selectedProfileId!, pageParam, undefined, archiveView, missingFilter)
        : historyApi.list(selectedProfileId!, pageParam, undefined, archiveView),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (lastPage) => {
      if (!lastPage || lastPage.length < 20) return undefined;
      return lastPage[lastPage.length - 1].id;
    },
    enabled: !!selectedProfileId,
  });

  const { data: totalCost } = useQuery({
    queryKey: ["total-cost", selectedProfileId],
    queryFn: () => historyApi.getTotalCost(selectedProfileId ?? undefined),
    staleTime: 30000,
  });

  const observerTarget = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const target = observerTarget.current;
    if (!target || !hasNextPage || isFetchingNextPage || isFetching || error)
      return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting && !isFetchingNextPage && !isFetching) {
          fetchNextPage();
        }
      },
      { threshold: 0.1 },
    );

    observer.observe(target);

    return () => {
      observer.disconnect();
    };
  }, [hasNextPage, isFetchingNextPage, isFetching, fetchNextPage, error]);

  const results = data ? data.pages.flatMap((page) => page) : [];

  // De-duplicate results by unique result.id to guarantee no duplicate items are ever rendered
  const uniqueResults = results.filter(
    (item, index, self) => self.findIndex((r) => r.id === item.id) === index,
  );

  return (
    <div className="max-w-5xl mx-auto">
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-text-primary">
            Workout History
          </h1>
          <p className="text-text-secondary mt-1">
            Review your past workout sessions
          </p>
        </div>

        {profiles.length > 1 && (
          <select
            value={selectedProfileId ?? ""}
            onChange={(e) => selectView(Number(e.target.value), archiveView)}
            className="bg-bg-secondary border border-border rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent/50"
          >
            {profiles.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        )}
      </div>

      <div className="mb-4 flex gap-2" role="group" aria-label="운동 기록 보기">
        <button type="button" aria-pressed={!archiveView} onClick={() => selectView(selectedProfileId, false)} className={`rounded-lg border px-4 py-2 text-sm ${!archiveView ? "border-accent text-accent" : "border-border text-text-secondary"}`}>운동 기록</button>
        <button type="button" aria-pressed={archiveView} onClick={() => selectView(selectedProfileId, true)} className={`rounded-lg border px-4 py-2 text-sm ${archiveView ? "border-accent text-accent" : "border-border text-text-secondary"}`}>보관함</button>
      </div>
      {!archiveView && (
        <div className="mb-6 flex flex-wrap gap-2" role="group" aria-label="미입력 항목 필터">
          {([
            { value: undefined, label: "전체" },
            { value: "any", label: "입력 필요" },
            { value: "effort", label: "Effort 미입력" },
            { value: "performance", label: "수행구간 미확정" },
          ] as const).map((chip) => {
            const active = missingFilter === chip.value;
            return (
              <button
                key={chip.label}
                type="button"
                aria-pressed={active}
                onClick={() => selectMissingFilter(chip.value)}
                className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                  active
                    ? "border-accent bg-accent/10 text-accent"
                    : "border-border bg-bg-elevated text-text-secondary hover:text-text-primary"
                }`}
              >
                {chip.label}
              </button>
            );
          })}
        </div>
      )}
      {archiveView && <p className="mb-6 text-sm text-text-secondary">보관한 기록은 피로도·훈련 부하·운동 비교에서 제외됩니다. 복원해도 중단된 분석은 자동 재개되지 않습니다.</p>}

      {totalCost && (totalCost.total_tokens > 0 || !!totalCost.unmeasured_calls || !!totalCost.unpriced_calls) && (
        <div className="bg-bg-elevated border border-border rounded-xl p-4 mb-6 flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-bg-secondary flex items-center justify-center text-lg">
              🪙
            </div>
            <div>
              <div className="text-xs text-text-muted">누적 AI 분석 비용</div>
              <div className="flex items-baseline gap-2">
                <span className="text-lg font-bold text-text-primary">
                  ${totalCost.cost_usd.toFixed(3)}
                  {!!totalCost.unmeasured_calls && <span className="ml-2 text-xs text-warning">사용량 미제공 {totalCost.unmeasured_calls}건</span>}
                  {!!totalCost.unpriced_calls && <span className="ml-2 text-xs text-warning">금액 미산정 {totalCost.unpriced_calls}건</span>}
                </span>
                <span className="text-xs font-medium text-text-muted">
                  (₩{Math.round(totalCost.cost_krw).toLocaleString()})
                </span>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-4 text-xs text-text-secondary">
            <div>
              <span className="text-text-muted block text-[11px]">총 사용 토큰</span>
              <span className="font-semibold text-text-primary">
                {totalCost.total_tokens.toLocaleString()}
              </span>
            </div>
            <div className="hidden sm:block">
              <span className="text-text-muted block text-[11px]">입력 (Prompt)</span>
              <span className="font-semibold text-text-primary">
                {totalCost.prompt_tokens.toLocaleString()}
              </span>
            </div>
            <div className="hidden sm:block">
              <span className="text-text-muted block text-[11px]">출력 (Candidate)</span>
              <span className="font-semibold text-text-primary">
                {totalCost.candidate_tokens.toLocaleString()}
              </span>
            </div>
          </div>
        </div>
      )}

      {isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          <HistoryCardSkeleton />
          <HistoryCardSkeleton />
          <HistoryCardSkeleton />
        </div>
      )}

      {error && uniqueResults.length === 0 && (
        <div className="bg-error/10 border border-error/30 text-error rounded-lg px-4 py-3 text-sm mb-6">
          <p>Failed to load history. Please try again.</p>
          <button type="button" onClick={() => void refetch()} disabled={isFetching} className="mt-2 rounded border border-error/30 px-3 py-1.5 font-medium disabled:opacity-50">Retry</button>
        </div>
      )}

      {!isLoading && !error && uniqueResults.length === 0 && (
        <div className="text-center py-20">
          <div className="w-16 h-16 rounded-full bg-bg-tertiary flex items-center justify-center mx-auto mb-4">
            <svg
              className="w-8 h-8 text-text-muted"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={1.5}
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z"
              />
            </svg>
          </div>
          <h3 className="text-lg font-medium text-text-primary mb-1">
            {archiveView
              ? "보관한 기록이 없습니다"
              : missingFilter
                ? "입력이 필요한 운동 기록이 없습니다"
                : "No workouts yet"}
          </h3>
          <p className="text-text-secondary text-sm">
            {archiveView
              ? "운동 기록에서 보관한 항목을 여기서 확인하고 복원할 수 있습니다."
              : missingFilter === "effort"
                ? "모든 대상 WOD에 Effort가 입력되어 있습니다."
                : missingFilter === "performance"
                  ? "모든 완료된 운동 구간이 확정되었습니다."
                  : missingFilter === "any"
                    ? "Effort 미입력 또는 수행구간 미확정 기록이 없습니다."
                    : "Upload a workout video to get started."}
          </p>
          {!archiveView && (
            missingFilter ? (
              <button
                type="button"
                onClick={() => selectMissingFilter(undefined)}
                className="inline-flex items-center gap-2 mt-4 bg-bg-secondary hover:bg-bg-tertiary text-text-primary border border-border font-medium rounded-lg px-4 py-2.5 transition-all duration-200"
              >
                전체 기록 보기
              </button>
            ) : (
              <Link
                to="/upload"
                className="inline-flex items-center gap-2 mt-4 bg-accent hover:bg-accent-hover text-white font-medium rounded-lg px-4 py-2.5 transition-all duration-200"
              >
                Upload Video
              </Link>
            )
          )}
        </div>
      )}

      {uniqueResults.length > 0 && (
        <>
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {uniqueResults.map((result) => (
              <HistoryCard key={result.id} result={result} archiveView={archiveView} />
            ))}
          </div>

          {/* Observer Target & Infinite Scroll Loading Indicator */}
          <div
            ref={observerTarget}
            className="mt-8 flex justify-center min-h-[50px]"
          >
            {isFetchingNextPage && (
              <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 w-full">
                <HistoryCardSkeleton />
                <HistoryCardSkeleton />
                <HistoryCardSkeleton />
              </div>
            )}
          </div>

          {error && (
            <div className="mt-8 flex flex-col items-center justify-center py-6 bg-error/5 border border-error/10 rounded-lg">
              <p className="text-error text-sm font-medium mb-2">
                Failed to load history
              </p>
              <button
                type="button"
                onClick={() => void refetch()}
                disabled={isFetching}
                className="px-4 py-2 bg-bg-secondary hover:bg-bg-tertiary text-text-primary border border-border rounded-lg text-sm font-medium transition-colors"
              >
                Retry
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
