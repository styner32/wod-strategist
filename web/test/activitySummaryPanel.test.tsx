jest.mock('react', () => jest.requireActual('../../node_modules/react'));

import { createElement, type ReactElement } from 'react';
import { environmentManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ActivitySummary } from '../../shared/activity';
import { ActivitySummaryPanel } from '../src/history/components/ActivitySummaryPanel';

const mockGet = jest.fn();
jest.mock('../src/api/client', () => ({
  api: {
    get: (...args: unknown[]) => mockGet(...args),
  },
}));

type Node = string | { children: Node[] | null } | Node[] | null;
interface Renderer {
  toJSON: () => Node;
  unmount: () => void;
}
const { act, create } = require('react-test-renderer') as {
  act: (callback: () => void | Promise<void>) => Promise<void>;
  create: (element: ReactElement) => Renderer;
};

function renderText(node: Node): string {
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(renderText).join('');
  return node?.children?.map(renderText).join('') ?? '';
}

describe('ActivitySummaryPanel out-of-frame gap labels', () => {
  let wasServer: boolean;
  beforeEach(() => {
    wasServer = environmentManager.isServer();
    environmentManager.setIsServer(() => false);
  });
  afterEach(() => {
    environmentManager.setIsServer(() => wasServer);
  });

  it('renders readable labels for target_not_visible and target_not_visible_sensor_active intervals', async () => {
    const summary: ActivitySummary = {
      version: 1,
      review_version: 1,
      available: true,
      review_state: 'partial',
      coverage_scope: 'recorded_chunks',
      media_generation: '1',
      source_version: 'v1',
      movements: [],
      unassessed: [
        { chunk_id: 1, clock: 'media', start_secs: 10, end_secs: 20, reason: 'target_not_visible' },
        { chunk_id: 2, clock: 'media', start_secs: 20, end_secs: 30, reason: 'target_not_visible_sensor_active' },
      ],
      reviews: [],
    };
    mockGet.mockResolvedValue(summary);

    const client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity, retry: false } } });
    let renderer!: Renderer;
    try {
      await act(async () => {
        renderer = create(
          createElement(QueryClientProvider, { client }, createElement(ActivitySummaryPanel, { sessionId: 'WOD-20261027-TEST', profileId: 1 })),
        );
      });
      await act(async () => {
        await Promise.resolve();
      });
      const text = renderText(renderer.toJSON());
      expect(text).toContain('Target athlete out of camera view');
      expect(text).toContain('Target athlete out of camera view (sensor movement detected)');
    } finally {
      if (renderer) await act(async () => renderer.unmount());
      client.clear();
    }
  });
});
