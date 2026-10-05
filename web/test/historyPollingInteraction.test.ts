jest.mock('react', () => jest.requireActual('../../node_modules/react'));
jest.mock('../src/history/components/AnalysisMarkdown', () => ({ AnalysisMarkdown: () => null, AnalysisOriginal: () => null }));

import { createElement, type ReactElement } from 'react';
import { environmentManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AnalysisResult } from '../src/api/history';
import { AnalysisOverview } from '../src/history/components/AnalysisOverview';

let mockRun = 'run-1';
let mockStatus = 'running';
const mockRead = jest.fn(async () => ({ enabled: false, analysis: {}, summary: { run_id: mockRun, status: mockStatus } }));
const mockStart = jest.fn(async (..._args: unknown[]) => { mockRun = 'run-2'; mockStatus = 'pending'; return { run_id: mockRun }; });
jest.mock('../src/api/enrichment', () => ({
  getEnrichment: () => mockRead(),
  startEnrichment: (...args: unknown[]) => mockStart(...args),
  enrichmentPending: (status?: string) => ['pending', 'preparing', 'running'].includes(status ?? ''),
}));

type Node = string | { children: Node[] | null } | Node[] | null;
interface Renderer {
  root: { findAllByType: (type: string) => { props: { children?: string; onClick: () => void } }[] };
  toJSON: () => Node;
  unmount: () => void;
}
const { act, create } = require('react-test-renderer') as {
  act: (callback: () => void | Promise<void>) => Promise<void>;
  create: (element: ReactElement) => Renderer;
};
const analysis = { session_id: 'session', status: 'COMPLETED', output: '{}', highlight_segments: '[]' } as AnalysisResult;
const key = ['analysis-enrichment', 'session', '[]'];

describe('analysis status pause and resume controls', () => {
  let client: QueryClient;
  let renderer: Renderer;
  let wasServer: boolean;
  beforeEach(() => {
    jest.useFakeTimers();
    wasServer = environmentManager.isServer();
    environmentManager.setIsServer(() => false);
    mockRun = 'run-1'; mockStatus = 'running';
    mockRead.mockClear(); mockStart.mockClear();
    client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity, retry: false, refetchIntervalInBackground: true } } });
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    client.clear();
    environmentManager.setIsServer(() => wasServer);
    jest.useRealTimers();
  });
  async function mount() {
    await act(async () => {
      renderer = create(createElement(QueryClientProvider, { client }, createElement(AnalysisOverview, {
        analysis, seek: () => {}, seekHighlight: () => {}, canSeek: false,
      })));
    });
    await act(async () => { await jest.advanceTimersByTimeAsync(0); });
  }
  function text(node: Node = renderer.toJSON()): string {
    if (typeof node === 'string') return node;
    if (Array.isArray(node)) return node.map(text).join('');
    return node?.children?.map(text).join('') ?? '';
  }
  async function click(label: string) {
    const button = renderer.root.findAllByType('button').find(node => node.props.children === label);
    expect(button).toBeDefined();
    await act(async () => button!.props.onClick());
    await act(async () => { await jest.advanceTimersByTimeAsync(0); });
  }

  it('shows the pause and retrieves completion without requesting another AI analysis', async () => {
    await mount();
    await act(async () => { await jest.advanceTimersByTimeAsync(500000); });
    expect(text()).toContain('자동 상태 조회를 잠시 중단했습니다');
    mockStatus = 'completed';
    await click('다시 조회');
    expect(text()).not.toContain('자동 상태 조회를 잠시 중단했습니다');
    expect(text()).toContain('요약 생성');
    expect(mockStart).not.toHaveBeenCalled();
  });

  it('continues polling a newly requested job after more than forty cached updates', async () => {
    mockStatus = 'completed';
    for (let i = 0; i < 80; i++) client.setQueryData(key, await mockRead());
    mockRead.mockClear();
    await mount();
    await click('요약 생성');
    const readsAfterCreation = mockRead.mock.calls.length;
    await act(async () => { await jest.advanceTimersByTimeAsync(3000); });
    expect(mockRead.mock.calls.length).toBeGreaterThan(readsAfterCreation);
    expect(mockStart).toHaveBeenCalledTimes(1);
    expect(text()).toContain('요약 생성 중');
    expect(text()).not.toContain('자동 상태 조회를 잠시 중단했습니다');
  });
});
