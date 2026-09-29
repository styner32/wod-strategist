import { useCallback, useEffect, useRef, useState } from 'react';
import { EnvironmentRecorder } from './recorder';
import type { Context, EnvironmentRecord, SourceChunk } from './types';
export function useEnvironmentRecorder(enabled: boolean) {
  const current = useRef<EnvironmentRecorder | null>(null);
  const [state, setState] = useState<{ status: string; record?: EnvironmentRecord }>({ status: 'off' });
  const start = useCallback((identity: { sessionId: string; profileId: number; startedAt: number }, context: Context, settings: Record<string, unknown>) => {
    if (!enabled || current.current) return;
    current.current = new EnvironmentRecorder(identity, context, settings, setState);
  }, [enabled]);
  const stop = useCallback(async (reason = 'stop') => {
    const recorder = current.current;
    if (!recorder) return;
    await recorder.stop(reason);
    if (current.current === recorder) current.current = null;
  }, []);
  useEffect(() => () => { void stop('unmounted').catch(() => {}); }, [stop]);
  return { ...state, start, stop,
    offerChunk: (chunk: SourceChunk) => current.current?.offerChunk(chunk),
    pause: () => current.current?.suspend(), resume: () => current.current?.resume(),
    event: (event: string, data?: Record<string, unknown>) => current.current?.event(event, data),
  };
}
