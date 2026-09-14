// The web package and the Expo test renderer each install React. Use the renderer's
// React instance for this interaction suite without changing the repository config.
jest.mock('react', () => jest.requireActual('../../node_modules/react'));

import { createElement, type ReactElement } from 'react';

type RenderedNode = string | { children: RenderedNode[] | null } | RenderedNode[] | null;
interface TestRenderer {
  root: { findByType: (type: string) => { props: Record<string, (event: unknown) => void> } };
  toJSON: () => RenderedNode;
  update: (element: ReactElement) => void;
  unmount: () => void;
}
// The repository already installs this runtime without its optional type package.
const { act, create } = require('react-test-renderer') as {
  act: (callback: () => void | Promise<void>) => Promise<void>;
  create: (element: ReactElement, options: { createNodeMock: (element: { type: unknown }) => unknown }) => TestRenderer;
};
import type { SensorTimelineResponse } from '../src/api/history';
import { SensorTimelinePanel } from '../src/history/components/SensorTimelinePanel';

const response: SensorTimelineResponse = {
  status: 'completed',
  timeline: {
    schema_version: 1, clock: 'capture_clock', bucket_ms: 1000, duration_ms: 10000,
    source: { sensor_version: '1', request_id: 'request', source_generation: '10', hr_calculation_version: 2 },
    points: [0, 1000, 8000, 9000].map((start) => ({
      start_ms: start, end_ms: start + 1000,
      heart_rate_bpm: { value: 100, status: 'valid' },
      acc_magnitude_std_g: { value: 0.25, status: 'valid' },
    })),
    gaps: [{ start_ms: 2000, end_ms: 8000, channel: 'both', reason: 'no_samples' }],
    pauses: [],
  },
  video_mapping: {
    kind: 'merged', method: 'chunk_linear',
    segments: [{ capture_start_ms: 0, capture_end_ms: 10000, media_start_ms: 20000, media_end_ms: 28000 }],
  },
};

const svgNode = {
  getBoundingClientRect: () => ({ left: 0, width: 800 }),
  setPointerCapture: jest.fn(),
  hasPointerCapture: () => true,
  releasePointerCapture: jest.fn(),
};

function pointerAt(captureMs: number) {
  return { clientX: 55 + captureMs / 10000 * 715, pointerId: 1, currentTarget: svgNode };
}

function renderedText(renderer: TestRenderer): string {
  const walk = (node: RenderedNode): string => {
    if (typeof node === 'string') return node;
    if (Array.isArray(node)) return node.map(walk).join('');
    return node?.children?.map(walk).join('') ?? '';
  };
  return walk(renderer.toJSON());
}

describe('sensor timeline interaction', () => {
  let renderer: TestRenderer;
  const onSeek = jest.fn();

  beforeEach(async () => {
    onSeek.mockClear();
    await act(async () => {
      renderer = create(createElement(SensorTimelinePanel, { key: 'session-a', timelineResponse: response, onSeekMedia: onSeek }), {
        createNodeMock: (element) => element.type === 'svg' ? svgNode : null,
      });
    });
  });

  afterEach(async () => {
    await act(async () => renderer.unmount());
  });

  it('shows the missing 5-second selection and seeks that exact time on click', async () => {
    await act(async () => renderer.root.findByType('svg').props.onPointerDown(pointerAt(5000)));
    await act(async () => renderer.root.findByType('svg').props.onPointerUp(pointerAt(5000)));
    expect(onSeek).toHaveBeenCalledWith(24);
    expect(renderedText(renderer)).toContain('⏱ 00:05');
    expect(renderedText(renderer)).toContain('심박수: 미측정 구간');
    expect(renderedText(renderer)).toContain('움직임 변화량: 미측정 구간');
    expect(renderedText(renderer)).not.toContain('100 bpm');
  });

  it('uses the same missing-time selection for keyboard seeking', async () => {
    await act(async () => renderer.root.findByType('section').props.onKeyDown({ key: 'ArrowRight', shiftKey: true, preventDefault: jest.fn() }));
    await act(async () => renderer.root.findByType('section').props.onKeyDown({ key: 'Enter', shiftKey: false, preventDefault: jest.fn() }));
    expect(onSeek).toHaveBeenCalledWith(24);
    expect(renderedText(renderer)).toContain('⏱ 00:05');
    expect(renderedText(renderer)).toContain('심박수: 미측정 구간');
  });

  it('does not seek an unmapped capture time', async () => {
    const noMapping = { ...response, video_mapping: { ...response.video_mapping, segments: [] } };
    await act(async () => renderer.update(createElement(SensorTimelinePanel, { key: 'session-a', timelineResponse: noMapping, onSeekMedia: onSeek })));
    await act(async () => renderer.root.findByType('svg').props.onPointerDown(pointerAt(5000)));
    await act(async () => renderer.root.findByType('svg').props.onPointerUp(pointerAt(5000)));
    expect(onSeek).not.toHaveBeenCalled();
    expect(renderedText(renderer)).toContain('⏱ 00:05');
  });

  it('resets a zoomed selection when the parent switches session keys', async () => {
    await act(async () => renderer.root.findByType('svg').props.onPointerDown(pointerAt(2000)));
    await act(async () => renderer.root.findByType('svg').props.onPointerMove(pointerAt(8000)));
    await act(async () => renderer.root.findByType('svg').props.onPointerUp(pointerAt(8000)));
    expect(renderedText(renderer)).toContain('00:02 – 00:08');
    await act(async () => renderer.update(createElement(SensorTimelinePanel, { key: 'session-b', timelineResponse: response, onSeekMedia: onSeek })));
    expect(renderedText(renderer)).toContain('00:00 – 00:10');
    expect(renderedText(renderer)).not.toContain('⏱');
  });
});
