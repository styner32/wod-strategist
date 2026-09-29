import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { environmentManager, QueryClient, QueryObserver } from '@tanstack/react-query';
import type { SensorTimelineData, SensorTimelinePoint, SensorTimelineResponse } from '../src/api/history';
import { SensorTimelinePanel } from '../src/history/components/SensorTimelinePanel';
import {
  buildSvgLinePath,
  captureToMedia,
  decimatePoints,
  findTimelinePoint,
  mediaToCapture,
  sensorTimelineQueryKey,
  sensorTimelinePollInterval,
  SENSOR_TIMELINE_POLL_LIMIT_MS,
  timelineValueAtTime,
} from '../src/history/timelineUtils';

function point(startMs: number, bpm: number | null = 100): SensorTimelinePoint {
  return {
    start_ms: startMs,
    end_ms: startMs + 1000,
    heart_rate_bpm: { value: bpm, status: bpm == null ? 'missing' : 'valid' },
    acc_magnitude_std_g: { value: 0.25, status: 'valid' },
  };
}

function timeline(points: SensorTimelinePoint[], durationMs = 10000): SensorTimelineData {
  return {
    schema_version: 1,
    clock: 'capture_clock',
    bucket_ms: 1000,
    duration_ms: durationMs,
    source: { sensor_version: '1', request_id: 'request', source_generation: '10', hr_calculation_version: 2 },
    points,
    gaps: [],
    pauses: [],
  };
}

const mapping = [{ capture_start_ms: 0, capture_end_ms: 10000, media_start_ms: 20000, media_end_ms: 28000 }];
const line = (points: SensorTimelinePoint[], width = 600) => buildSvgLinePath(
  decimatePoints(points, 'heart_rate_bpm', 0, 10000, width),
  (ms) => ms / 1000,
  (value) => value,
);

describe('sensor timeline rendering and selection', () => {
  it.each([600, 1])('breaks a sparse 2–8 second gap at width %i', (width) => {
    expect(line([point(0), point(1000), point(8000), point(9000)], width))
      .toBe('M 0.5 100.0 L 1.5 100.0 M 8.5 100.0 L 9.5 100.0');
  });

  it('retains an internal null and extrema when all points share one pixel bin', () => {
    const points = [point(0, 100), point(1000, 180), point(2000, 70), point(3000, 120), point(4000, null), point(5000, 110), point(6000, 160), point(7000, 80), point(8000, 110), point(9000, 120)];
    const samples = decimatePoints(points, 'heart_rate_bpm', 0, 10000, 1);
    expect(samples.map((sample) => sample.value)).toEqual([100, 180, 70, 120, null, 110, 160, 80, 120]);
    expect(buildSvgLinePath(samples, (ms) => ms / 1000, (value) => value).match(/M /g)).toHaveLength(2);
  });

  it('applies explicit gaps to only the affected channel', () => {
    const points = [point(0), point(1000), point(2000)];
    const gaps: SensorTimelineData['gaps'] = [{ start_ms: 1000, end_ms: 2000, channel: 'heart_rate', reason: 'packet_gap' }];
    expect(decimatePoints(points, 'heart_rate_bpm', 0, 3000, 1, gaps).map((sample) => sample.value))
      .toEqual([100, null, 100]);
    expect(decimatePoints(points, 'acc_magnitude_std_g', 0, 3000, 1, gaps).every((sample) => sample.value === 0.25))
      .toBe(true);
  });

  it('does not substitute a nearby point or snap the video when selecting missing time', () => {
    const data = timeline([point(0), point(1000), point(8000), point(9000)]);
    data.gaps = [{ start_ms: 2000, end_ms: 8000, channel: 'both', reason: 'no_samples' }];
    expect(findTimelinePoint(data.points, 5000, data.duration_ms)).toBeNull();
    expect(timelineValueAtTime(data, 'heart_rate_bpm', 5000)).toEqual({ value: null, status: 'missing', reason: 'no_samples' });
    expect(captureToMedia(5000, mapping)).toBe(24);
    expect(mediaToCapture(24, mapping)).toBe(5000);
    expect(captureToMedia(5000, [])).toBeNull();
  });

  it('uses half-open buckets and only includes the actual session endpoint', () => {
    const points = [point(0, 100), point(1000, 110), point(8000, 120), point(9000, 130)];
    expect(findTimelinePoint(points, 1000, 10000)?.heart_rate_bpm.value).toBe(110);
    expect(findTimelinePoint(points, 2000, 10000)).toBeNull();
    expect(findTimelinePoint(points, 10000, 10000)?.heart_rate_bpm.value).toBe(130);
    expect(findTimelinePoint(points, 10001, 10000)).toBeNull();
  });

  it('reports compressed pauses and independent HR/ACC quality', () => {
    const data = timeline([point(0), point(1000)]);
    data.pauses = [{ start_ms: 2000, end_ms: 4000 }];
    data.gaps = [{ start_ms: 1000, end_ms: 2000, channel: 'heart_rate', reason: 'no_hr' }];
    expect(timelineValueAtTime(data, 'heart_rate_bpm', 1500).status).toBe('missing');
    expect(timelineValueAtTime(data, 'acc_magnitude_std_g', 1500).value).toBe(0.25);
    expect(timelineValueAtTime(data, 'acc_magnitude_std_g', 2500).status).toBe('paused');
    expect(timelineValueAtTime(data, 'acc_magnitude_std_g', 4000).status).toBe('missing');
  });

  it('renders the full 0–10 second capture clock when data begins at 5 seconds', () => {
    const response: SensorTimelineResponse = {
      status: 'completed', timeline: timeline([point(5000), point(6000), point(7000), point(8000), point(9000)]),
      video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [] },
    };
    const markup = renderToStaticMarkup(createElement(SensorTimelinePanel, { timelineResponse: response }));
    expect(markup).toContain('00:00 – 00:10');
    expect(markup).not.toContain('00:15');
  });

  it('keeps the timeline and video navigation available when all sensor time is missing', () => {
    const data = timeline([]);
    data.gaps = [{ start_ms: 0, end_ms: 10000, channel: 'both', reason: 'no_samples' }];
    const response: SensorTimelineResponse = {
      status: 'limited', timeline: data,
      video_mapping: { kind: 'merged', method: 'chunk_linear', segments: mapping },
    };
    const markup = renderToStaticMarkup(createElement(SensorTimelinePanel, { timelineResponse: response }));
    expect(markup).toContain('<svg');
    expect(markup).toContain('00:00 – 00:10');
    expect(markup).not.toContain('센서 시계열 데이터가 없습니다.');
  });

  it('renders reprocess button on failure when onReprocess callback is supplied', () => {
    const response: SensorTimelineResponse = {
      status: 'failed',
      timeline: null,
      video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [] },
    };
    const onReprocess = jest.fn();
    const markup = renderToStaticMarkup(createElement(SensorTimelinePanel, {
      timelineResponse: response,
      onReprocess,
      isReprocessing: false,
    }));
    expect(markup).toContain('센서 데이터 재처리');
    expect(markup).toContain('센서 시계열 처리에 실패했습니다.');
    expect(markup).not.toContain('disabled=""');
  });

  it('renders disabled reprocessing label when reprocess is pending', () => {
    const response: SensorTimelineResponse = {
      status: 'failed',
      timeline: null,
      video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [] },
    };
    const onReprocess = jest.fn();
    const markup = renderToStaticMarkup(createElement(SensorTimelinePanel, {
      timelineResponse: response,
      onReprocess,
      isReprocessing: true,
    }));
    expect(markup).toContain('재처리 요청 중...');
    expect(markup).toContain('disabled=""');
  });

  it('omits reprocess button when onReprocess callback is not supplied', () => {
    const response: SensorTimelineResponse = {
      status: 'failed',
      timeline: null,
      video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [] },
    };
    const markup = renderToStaticMarkup(createElement(SensorTimelinePanel, {
      timelineResponse: response,
    }));
    expect(markup).toContain('센서 시계열 처리에 실패했습니다.');
    expect(markup).not.toContain('센서 데이터 재처리');
  });
});

describe('late video mapping query updates', () => {
  it('fetches again after completion when video availability or chunk boundaries change', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: Infinity } } });
    const chunk = { id: 1, start_secs: 0, end_secs: 10, media_start_secs: null, media_end_secs: null };
    const updatedChunk = { ...chunk, media_start_secs: 20, media_end_secs: 28 };
    const emptyResponse: SensorTimelineResponse = {
      status: 'completed', timeline: timeline([point(0)]),
      video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [] },
    };
    const mappedResponse = { ...emptyResponse, video_mapping: { ...emptyResponse.video_mapping, segments: mapping } };
    const queryFn = jest.fn().mockResolvedValueOnce(emptyResponse).mockResolvedValueOnce(emptyResponse).mockResolvedValueOnce(mappedResponse);
    const options = (chunks: Parameters<typeof sensorTimelineQueryKey>[2], ready: boolean) => ({
      queryKey: sensorTimelineQueryKey('session', 1, chunks, ready), queryFn,
    });
    const observer = new QueryObserver(client, options([chunk], false));
    const unsubscribe = observer.subscribe(() => undefined);
    try {
      await observer.getCurrentQuery().promise;
      expect(observer.getCurrentResult().data?.status).toBe('completed');
      observer.setOptions(options([chunk], true));
      await observer.getCurrentQuery().promise;
      observer.setOptions(options([updatedChunk], true));
      await observer.getCurrentQuery().promise;
      expect(observer.getCurrentResult().data?.video_mapping.segments).toEqual(mapping);
      expect(queryFn).toHaveBeenCalledTimes(3);
      // Re-rendering with identical metadata must not keep refetching completed data.
      observer.setOptions(options([{ ...updatedChunk }], true));
      await Promise.resolve();
      expect(queryFn).toHaveBeenCalledTimes(3);
    } finally {
      unsubscribe();
      client.clear();
    }
  });
});


describe('bounded sensor and video readiness polling', () => {
  let wasServer: boolean;
  beforeEach(() => {
    jest.useFakeTimers();
    wasServer = environmentManager.isServer();
    environmentManager.setIsServer(() => false);
  });
  afterEach(() => {
    environmentManager.setIsServer(() => wasServer);
    jest.useRealTimers();
  });

  function observe(queryFn: () => Promise<SensorTimelineResponse>, isVideoReady: () => boolean) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    const startedAt = Date.now();
    const options = {
      queryKey: ['readiness-poll'], queryFn, refetchIntervalInBackground: true,
      refetchInterval: (query: { state: { data?: SensorTimelineResponse; status: string } }) =>
        sensorTimelinePollInterval(query.state.data, isVideoReady(), Date.now() - startedAt, query.state.status === 'error'),
    };
    const observer = new QueryObserver(client, options);
    const unsubscribe = observer.subscribe(() => undefined);
    return { observer, options, close: () => { unsubscribe(); client.clear(); } };
  }

  const waitingResponse: SensorTimelineResponse = {
    status: 'completed', timeline: timeline([point(0)]),
    video_mapping: { kind: 'merged', method: 'chunk_linear', segments: [] },
  };

  it('keeps waiting after the initial missing video, then stops when video and mapping are ready', async () => {
    let videoReady = false;
    const mappedResponse = { ...waitingResponse, video_mapping: { ...waitingResponse.video_mapping, segments: mapping } };
    const queryFn = jest.fn().mockResolvedValueOnce(waitingResponse).mockResolvedValue(mappedResponse);
    const polling = observe(queryFn, () => videoReady);
    try {
      await jest.advanceTimersByTimeAsync(0);
      expect(queryFn).toHaveBeenCalledTimes(1);
      await jest.advanceTimersByTimeAsync(5000);
      expect(queryFn).toHaveBeenCalledTimes(2);
      // Mapping alone is insufficient while the initial video request is still 404.
      await jest.advanceTimersByTimeAsync(5000);
      expect(queryFn).toHaveBeenCalledTimes(3);
      videoReady = true;
      polling.observer.setOptions(polling.options);
      await jest.advanceTimersByTimeAsync(60000);
      expect(queryFn).toHaveBeenCalledTimes(3);
    } finally { polling.close(); }
  });

  it('stops waiting after five minutes even if a video never exists', async () => {
    const queryFn = jest.fn().mockResolvedValue(waitingResponse);
    const polling = observe(queryFn, () => false);
    try {
      await jest.advanceTimersByTimeAsync(SENSOR_TIMELINE_POLL_LIMIT_MS);
      const countAtDeadline = queryFn.mock.calls.length;
      expect(countAtDeadline).toBeGreaterThan(1);
      expect(countAtDeadline).toBeLessThanOrEqual(61);
      await jest.advanceTimersByTimeAsync(SENSOR_TIMELINE_POLL_LIMIT_MS);
      expect(queryFn).toHaveBeenCalledTimes(countAtDeadline);
    } finally { polling.close(); }
  });

  it('stops after a sensor API error instead of polling cached completed data', async () => {
    const queryFn = jest.fn().mockResolvedValueOnce(waitingResponse).mockRejectedValue(new Error('network unavailable'));
    const polling = observe(queryFn, () => false);
    try {
      await jest.advanceTimersByTimeAsync(5000);
      expect(queryFn).toHaveBeenCalledTimes(2);
      expect(polling.observer.getCurrentResult().isError).toBe(true);
      await jest.advanceTimersByTimeAsync(60000);
      expect(queryFn).toHaveBeenCalledTimes(2);
    } finally { polling.close(); }
  });

  it('does not poll a session without optional sensor data', async () => {
    const queryFn = jest.fn().mockResolvedValue({ ...waitingResponse, status: 'none', timeline: null });
    const polling = observe(queryFn, () => false);
    try {
      await jest.advanceTimersByTimeAsync(60000);
      expect(queryFn).toHaveBeenCalledTimes(1);
    } finally { polling.close(); }
  });
});
