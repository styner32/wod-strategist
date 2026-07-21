import { useState, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { useInfiniteQuery } from '@tanstack/react-query';
import { historyApi, type AnalysisResult } from '../api/history';
import { useAuth } from '../auth/useAuth';

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
    completed: 'bg-success/10 text-success border-success/20',
    processing: 'bg-warning/10 text-warning border-warning/20',
    failed: 'bg-error/10 text-error border-error/20',
    pending: 'bg-info/10 text-info border-info/20',
  };
  const style = styles[status.toLowerCase()] || styles.pending;
  return (
    <span className={`text-xs font-medium px-2.5 py-0.5 rounded-full border ${style}`}>
      {status}
    </span>
  );
}

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function parseAnalysisOutput(output: string): { summary?: string; workoutType?: string } {
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

function HistoryCard({ result }: { result: AnalysisResult }) {
  const parsed = parseAnalysisOutput(result.output || '{}');
  return (
    <Link
      to={`/sessions/${result.session_id}`}
      className="block bg-bg-elevated border border-border rounded-xl p-5 hover:border-accent/40 hover:bg-bg-elevated/80 transition-all duration-200 group"
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center">
            <svg className="w-5 h-5 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
            </svg>
          </div>
          <div>
            <p className="font-medium text-text-primary group-hover:text-accent transition-colors">
              {result.session_id.slice(0, 20)}...
            </p>
            <p className="text-xs text-text-muted">{formatDate(result.created_at)}</p>
          </div>
        </div>
        <StatusBadge status={result.status} />
      </div>

      <div className="flex flex-wrap items-center gap-1.5 mb-2">
        {parsed.workoutType && (
          <span className="inline-block text-xs bg-bg-secondary text-text-secondary px-2 py-0.5 rounded-md">
            {parsed.workoutType}
          </span>
        )}
      </div>

      {parsed.summary && (
        <p className="text-sm text-text-secondary line-clamp-2">{parsed.summary}</p>
      )}
    </Link>
  );
}

export function HistoryListPage() {
  const { user } = useAuth();
  const profiles = user?.profiles || [];
  const [selectedProfileId, setSelectedProfileId] = useState<number | null>(
    profiles.length > 0 ? profiles[0].id : null,
  );

  const {
    data,
    isLoading,
    error,
    fetchNextPage,
    hasNextPage,
    isFetching,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: ['history-infinite', selectedProfileId],
    queryFn: ({ pageParam }) => historyApi.list(selectedProfileId!, pageParam),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (lastPage) => {
      if (!lastPage || lastPage.length < 20) return undefined;
      return lastPage[lastPage.length - 1].id;
    },
    enabled: !!selectedProfileId,
  });

  const observerTarget = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const target = observerTarget.current;
    if (!target || !hasNextPage || isFetchingNextPage || isFetching || error) return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting && !isFetchingNextPage && !isFetching) {
          fetchNextPage();
        }
      },
      { threshold: 0.1 }
    );

    observer.observe(target);

    return () => {
      if (target) {
        observer.unobserve(target);
      }
    };
  }, [hasNextPage, isFetchingNextPage, isFetching, fetchNextPage, error]);

  const results = data ? data.pages.flatMap((page) => page) : [];

  // De-duplicate results by unique result.id to guarantee no duplicate items are ever rendered
  const uniqueResults = results.filter(
    (item, index, self) => self.findIndex((r) => r.id === item.id) === index
  );

  return (
    <div className="max-w-5xl mx-auto">
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-text-primary">Workout History</h1>
          <p className="text-text-secondary mt-1">Review your past workout sessions</p>
        </div>

        {profiles.length > 1 && (
          <select
            value={selectedProfileId ?? ''}
            onChange={(e) => setSelectedProfileId(Number(e.target.value))}
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

      {isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          <HistoryCardSkeleton />
          <HistoryCardSkeleton />
          <HistoryCardSkeleton />
        </div>
      )}

      {error && uniqueResults.length === 0 && (
        <div className="bg-error/10 border border-error/30 text-error rounded-lg px-4 py-3 text-sm mb-6">
          Failed to load history. Please try again.
        </div>
      )}

      {!isLoading && !error && uniqueResults.length === 0 && (
        <div className="text-center py-20">
          <div className="w-16 h-16 rounded-full bg-bg-tertiary flex items-center justify-center mx-auto mb-4">
            <svg className="w-8 h-8 text-text-muted" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
            </svg>
          </div>
          <h3 className="text-lg font-medium text-text-primary mb-1">No workouts yet</h3>
          <p className="text-text-secondary text-sm">
            Upload a workout video to get started.
          </p>
          <Link
            to="/upload"
            className="inline-flex items-center gap-2 mt-4 bg-accent hover:bg-accent-hover text-white font-medium rounded-lg px-4 py-2.5 transition-all duration-200"
          >
            Upload Video
          </Link>
        </div>
      )}

      {uniqueResults.length > 0 && (
        <>
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {uniqueResults.map((result) => (
              <HistoryCard key={result.id} result={result} />
            ))}
          </div>

          {/* Observer Target & Infinite Scroll Loading Indicator */}
          <div ref={observerTarget} className="mt-8 flex justify-center min-h-[50px]">
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
              <p className="text-error text-sm font-medium mb-2">Failed to load more history</p>
              <button
                type="button"
                onClick={() => fetchNextPage()}
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
