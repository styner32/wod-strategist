import { QueryClient, QueryObserver } from '@tanstack/react-query';
import type { SensorTimelineResponse } from '../src/api/history';
import { refreshSensorVideoMetadata, type SensorMetadataRefresh } from '../src/history/sensorTimelineRecovery';
import { sensorTimelinePollInterval, sensorTimelineQueryKey } from '../src/history/timelineUtils';

const mapped: SensorTimelineResponse = {
  status: 'completed',
  timeline: {
    schema_version: 1, clock: 'capture_clock', bucket_ms: 1000, duration_ms: 10000,
    source: { sensor_version: '1', request_id: 'request', source_generation: '1', hr_calculation_version: 2 },
    points: [], pauses: [], gaps: [],
  },
  video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [
    { capture_start_ms: 0, capture_end_ms: 10000, media_start_ms: 0, media_end_ms: 10000 },
  ] },
};

describe('metadata recovery after video merge', () => {
  let client: QueryClient;
  let refresh: SensorMetadataRefresh;
  const unsubscribe: (() => void)[] = [];
  beforeEach(() => {
    client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity, staleTime: Infinity, retry: false } } });
    refresh = {};
  });
  afterEach(() => { unsubscribe.splice(0).forEach(fn => fn()); client.clear(); });

  function observe(key: string, data: unknown, queryFn: () => Promise<unknown>) {
    const queryKey = [key, 'session'];
    client.setQueryData(queryKey, data);
    unsubscribe.push(new QueryObserver(client, { queryKey, queryFn, staleTime: Infinity }).subscribe(() => undefined));
  }

  it('updates null chunk boundaries on the final mapped response even with an existing video URL', async () => {
    const stale = { id: 1, start_secs: 0, end_secs: 10, media_start_secs: null, media_end_secs: null };
    const ready = { ...stale, media_start_secs: 0, media_end_secs: 10 };
    const chunks = jest.fn(async () => [ready]);
    const analysis = jest.fn(async () => ({ status: 'COMPLETED', chunks: [ready] }));
    observe('chunks', [stale], chunks);
    observe('session-analysis', { status: 'PROCESSING', chunks: [stale] }, analysis);
    const initialKey = sensorTimelineQueryKey('session', 3, [stale], true);
    expect(sensorTimelinePollInterval(mapped, true, 1000, false)).toBe(false);
    await refreshSensorVideoMetadata(client, mapped, 'session', 3, refresh);
    expect(client.getQueryData(['chunks', 'session'])).toEqual([ready]);
    expect(client.getQueryData(['session-analysis', 'session'])).toEqual({ status: 'COMPLETED', chunks: [ready] });
    expect(sensorTimelineQueryKey('session', 3, [ready], true)).not.toEqual(initialKey);
    await refreshSensorVideoMetadata(client, mapped, 'session', 3, refresh);
    expect(chunks).toHaveBeenCalledTimes(1);
    expect(analysis).toHaveBeenCalledTimes(1);
  });

  it('refreshes new sources and mappings once, without a repeated metadata fetch loop', async () => {
    const fetch = jest.fn(async () => ({}));
    observe('chunks', {}, fetch);
    observe('session-analysis', {}, fetch);
    const empty = { ...mapped, video_mapping: { ...mapped.video_mapping, segments: [] } };
    await refreshSensorVideoMetadata(client, empty, 'session', 3, refresh);
    expect(fetch).not.toHaveBeenCalled();
    await Promise.all([
      refreshSensorVideoMetadata(client, mapped, 'session', 3, refresh),
      refreshSensorVideoMetadata(client, mapped, 'session', 3, refresh),
    ]);
    expect(fetch).toHaveBeenCalledTimes(2);
    const next = { ...mapped, timeline: { ...mapped.timeline!, source: { ...mapped.timeline!.source, request_id: 'next' } } };
    await refreshSensorVideoMetadata(client, next, 'session', 3, refresh);
    await refreshSensorVideoMetadata(client, next, 'session', 3, refresh);
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it('keeps failed metadata refreshes eligible for retry', async () => {
    const fetch = jest.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue({});
    observe('chunks', {}, fetch);
    observe('session-analysis', {}, async () => ({}));
    await expect(refreshSensorVideoMetadata(client, mapped, 'session', 3, refresh)).rejects.toThrow('offline');
    expect(refresh.failed).toBe(true);
    expect(sensorTimelinePollInterval(mapped, !refresh.failed, 1000, false)).toBe(5000);
    await refreshSensorVideoMetadata(client, mapped, 'session', 3, refresh);
    expect(refresh.failed).toBe(false);
    expect(sensorTimelinePollInterval(mapped, !refresh.failed, 6000, false)).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
