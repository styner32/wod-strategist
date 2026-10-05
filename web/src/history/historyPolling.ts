import type { QueryClient, QueryKey } from '@tanstack/react-query';

type PollingQuery = { state: { dataUpdateCount: number; error?: unknown } };
const budgets = new WeakMap<PollingQuery, { work: string; baseline: number; limit: number }>();

/** Count successful refreshes for the current work, not the lifetime of its cache. */
export function historyPollInterval(
  query: PollingQuery,
  work: string | undefined,
  slow = false,
  limit = 40,
): number | false {
  if (!work || query.state.error) {
    budgets.delete(query);
    return false;
  }
  let budget = budgets.get(query);
  if (!budget || budget.work !== work) {
    budget = { work, baseline: query.state.dataUpdateCount, limit };
    budgets.set(query, budget);
  }
  const count = Math.max(0, query.state.dataUpdateCount - budget.baseline);
  if (count >= budget.limit) return false;
  return slow
    ? Math.min(2000 * 1.5 ** Math.min(count, 4), 10000)
    : Math.min(1000 * 2 ** Math.min(count, 4), 8000);
}

export function historyPollingStopped(client: QueryClient, key: QueryKey, dataUpdatedAt: number): boolean {
  const query = client.getQueryCache().find({ queryKey: key, exact: true });
  if (!query || query.state.dataUpdatedAt !== dataUpdatedAt) return false;
  const budget = budgets.get(query);
  return !!budget && query.state.dataUpdateCount - budget.baseline >= budget.limit;
}

/** Restart status reads only; this never submits another analysis request. */
export function resetHistoryPolling(client: QueryClient, key: QueryKey): void {
  for (const query of client.getQueryCache().findAll({ queryKey: key })) budgets.delete(query);
}
