import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { historyApi, type AnalysisResult } from '../api/history';
import { useAuth } from '../auth/useAuth';

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
  const videos = result.available_videos ?? [];
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
        {videos.includes('hardsubbed') && (
          <span className="inline-flex items-center gap-1 text-xs bg-success/10 text-success px-2 py-0.5 rounded-md">
            📝 Guided
          </span>
        )}
        {videos.includes('merged') && !videos.includes('hardsubbed') && (
          <span className="inline-flex items-center gap-1 text-xs bg-info/10 text-info px-2 py-0.5 rounded-md">
            📹 Video
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

  const { data: results, isLoading, error } = useQuery({
    queryKey: ['history', selectedProfileId],
    queryFn: () => historyApi.list(selectedProfileId!),
    enabled: !!selectedProfileId,
  });

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
        <div className="flex items-center justify-center py-20">
          <div className="w-8 h-8 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        </div>
      )}

      {error && (
        <div className="bg-error/10 border border-error/30 text-error rounded-lg px-4 py-3 text-sm">
          Failed to load history. Please try again.
        </div>
      )}

      {results && results.length === 0 && (
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

      {results && results.length > 0 && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {results.map((result) => (
            <HistoryCard key={result.id} result={result} />
          ))}
        </div>
      )}
    </div>
  );
}
