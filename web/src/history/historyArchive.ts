import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { AnalysisResult, SessionAnalysisResponse } from "../api/history";

const archiveQueryPrefixes = [
  "history-infinite", "history-stretches", "analysis", "session-analysis",
  "related-wods", "performance-benchmarks", "workout-progress", "wod-training",
  "wod-training-load", "analysis-enrichment", "session-reanalyses", "session-reanalysis",
  "chunk-reanalyses", "chunk-reanalysis", "recognition-evaluation", "recognition-reviews",
  "recognition-review", "activity-summary", "sensor-timeline", "chunks",
] as const;

export const cancelArchiveQueries = (client: QueryClient) => Promise.all(
  archiveQueryPrefixes.map(key => client.cancelQueries({ queryKey: [key] })),
);

/** Apply only after the server accepts the mutation; canceled fetches cannot restore stale rows. */
export async function refreshArchiveQueries(client: QueryClient, result: AnalysisResult, archived: boolean) {
  await cancelArchiveQueries(client);
  const patch = (row: AnalysisResult): AnalysisResult => row.id === result.id
    ? { ...row, archived_at: archived ? row.archived_at ?? new Date().toISOString() : null }
    : row;
  client.setQueryData<AnalysisResult[]>(["analysis", result.session_id], rows => rows?.map(patch));
  client.setQueryData<SessionAnalysisResponse>(["session-analysis", result.session_id], data => data
    ? { ...data, analysis: data.analysis ? patch(data.analysis) : data.analysis }
    : data);
  for (const query of client.getQueryCache().findAll({ queryKey: ["history-infinite"] })) {
    const archiveView = query.queryKey[2] === "archived";
    client.setQueryData<InfiniteData<AnalysisResult[]>>(query.queryKey, data => data
      ? { ...data, pages: data.pages.map(page => page.filter(row => row.id !== result.id || archiveView === archived).map(patch)) }
      : data);
  }
  await Promise.all(archiveQueryPrefixes.map(key => client.invalidateQueries({ queryKey: [key] })));
}

export function historyLocation(profileId?: number | null, archived = false) {
  const params = new URLSearchParams();
  if (profileId) params.set("profile_id", String(profileId));
  if (archived) params.set("archived", "true");
  return `/${params.size ? `?${params}` : ""}`;
}

export const historyStatusLabel = (status: string) => status.toUpperCase() === "CANCELLED" ? "중단됨" : status;
