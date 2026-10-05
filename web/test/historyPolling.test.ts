import { environmentManager, QueryClient, QueryObserver } from '@tanstack/react-query';
import { historyPollInterval, historyPollingStopped, resetHistoryPolling } from '../src/history/historyPolling';

describe('history analysis polling budgets', () => {
  let client: QueryClient;
  let wasServer: boolean;
  const key = ['analysis-enrichment', 'session'];

  beforeEach(() => {
    jest.useFakeTimers();
    wasServer = environmentManager.isServer();
    environmentManager.setIsServer(() => false);
    client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity, retry: false } } });
  });
  afterEach(() => {
    client.clear();
    environmentManager.setIsServer(() => wasServer);
    jest.useRealTimers();
  });

  function observe(initialWork = 'run-1', limit = 40) {
    let work: string | undefined = initialWork;
    const fetch = jest.fn(async () => ({ work }));
    const observer = new QueryObserver(client, {
      queryKey: key,
      queryFn: fetch,
      refetchIntervalInBackground: true,
      refetchInterval: query => historyPollInterval(query, query.state.data?.work, true, limit),
    });
    const unsubscribe = observer.subscribe(() => undefined);
    return { observer, fetch, unsubscribe, setWork: (value: string | undefined) => { work = value; } };
  }

  it('stops a long pending run, then polls a new run despite cumulative cached updates', async () => {
    const poll = observe();
    try {
      await jest.advanceTimersByTimeAsync(500000);
      const count = poll.fetch.mock.calls.length;
      expect(count).toBe(41);
      expect(historyPollingStopped(client, key, poll.observer.getCurrentResult().dataUpdatedAt)).toBe(true);
      await jest.advanceTimersByTimeAsync(500000);
      expect(poll.fetch).toHaveBeenCalledTimes(count);
      poll.setWork('run-2');
      await client.invalidateQueries({ queryKey: key });
      await jest.advanceTimersByTimeAsync(2000);
      expect(poll.fetch).toHaveBeenCalledTimes(count + 2);
      expect(poll.observer.getCurrentQuery().state.dataUpdateCount).toBeGreaterThan(40);
      expect(historyPollingStopped(client, key, poll.observer.getCurrentResult().dataUpdatedAt)).toBe(false);
    } finally { poll.unsubscribe(); }
  });

  it('resumes the same run with status reads only and still stops on completion', async () => {
    const poll = observe();
    try {
      await jest.advanceTimersByTimeAsync(500000);
      resetHistoryPolling(client, key);
      await poll.observer.refetch();
      await jest.advanceTimersByTimeAsync(3000);
      expect(poll.fetch).toHaveBeenCalledTimes(43);
      poll.setWork(undefined);
      await poll.observer.refetch();
      const finalCount = poll.fetch.mock.calls.length;
      await jest.advanceTimersByTimeAsync(500000);
      expect(poll.fetch).toHaveBeenCalledTimes(finalCount);
      expect(historyPollingStopped(client, key, poll.observer.getCurrentResult().dataUpdatedAt)).toBe(false);
    } finally { poll.unsubscribe(); }
  });

  it('does not charge a new mounted observer for old cached reads', async () => {
    for (let i = 0; i < 80; i++) client.setQueryData(key, { work: 'old-run' });
    const poll = observe('new-run');
    try {
      await jest.advanceTimersByTimeAsync(2000);
      expect(poll.fetch).toHaveBeenCalledTimes(2);
      expect(historyPollingStopped(client, key, poll.observer.getCurrentResult().dataUpdatedAt)).toBe(false);
    } finally { poll.unsubscribe(); }
  });

  it('gives a queued review a new budget after provisional polling is exhausted', async () => {
    const poll = observe('provisional-source', 4);
    try {
      await jest.advanceTimersByTimeAsync(60000);
      expect(poll.fetch).toHaveBeenCalledTimes(5);
      poll.setWork('queued-source');
      await poll.observer.refetch();
      await jest.advanceTimersByTimeAsync(2000);
      expect(poll.fetch).toHaveBeenCalledTimes(7);
    } finally { poll.unsubscribe(); }
  });
});
