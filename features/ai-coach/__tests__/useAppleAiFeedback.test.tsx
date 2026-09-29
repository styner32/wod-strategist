import { act, renderHook } from '@testing-library/react-native';
import * as Battery from 'expo-battery';
import { deleteAsync } from 'expo-file-system/legacy';
import { appleOnDeviceAi, type AppleAiAnswer } from '../../../modules/apple-on-device-ai';
import { useAppleAiFeedback, type AppleAiFeedbackOptions } from '../useAppleAiFeedback';

jest.mock('expo-battery', () => ({ getPowerStateAsync: jest.fn() }));
jest.mock('expo-file-system/legacy', () => ({ deleteAsync: jest.fn() }));
jest.mock('../../../modules/apple-on-device-ai', () => ({ appleOnDeviceAi: {
  getAvailability: jest.fn(), getThermalState: jest.fn(), prepareFrames: jest.fn(), analyzeFrames: jest.fn(), cancel: jest.fn(),
} }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe('Apple AI recording feedback', () => {
  let options: AppleAiFeedbackOptions;
  const flush = () => act(async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); });
  const advance = (ms: number) => act(async () => { await jest.advanceTimersByTimeAsync(ms); });
  beforeEach(() => {
    jest.useFakeTimers(); jest.setSystemTime(100_000); jest.resetAllMocks();
    jest.mocked(appleOnDeviceAi.getAvailability).mockResolvedValue('available');
    jest.mocked(appleOnDeviceAi.getThermalState).mockResolvedValue(0);
    jest.mocked(appleOnDeviceAi.cancel).mockResolvedValue();
    jest.mocked(appleOnDeviceAi.prepareFrames).mockImplementation(async (_id, frames) => ({
      version: 1, frames: [0, 3, 5].map(index => ({ ...frames[index], path: `/tmp/input-${index}.jpg`,
        sourceIndex: index, width: 300, height: 500 })),
      crop: { x: 0.2, y: 0.1, width: 0.6, height: 0.8 }, cropMode: 'person_with_context', fallbackReason: null,
      selectionMethod: 'endpoints_plus_visual_change', candidateCount: 6, personCounts: [1, 1, 1, 1, 1, 1],
      changeScores: [0, 0.1, 0.2, 0.4, 0.1, 0], elapsedMs: 100,
    }));
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockResolvedValue({ feedback: 'Keep the visible position.', elapsedMs: 800 });
    jest.mocked(Battery.getPowerStateAsync).mockResolvedValue({ batteryLevel: 0.8, lowPowerMode: false, batteryState: 1 });
    jest.mocked(deleteAsync).mockResolvedValue();
    let frame = 0;
    options = { enabled: true, running: true, foreground: true, sessionId: 'session-a',
      wodDescription: 'AMRAP 10: 5 squats, 5 push-ups', movements: 'Squat, Push-up', language: 'ko',
      appearanceHints: '검은 상의, 남색 반바지, 분홍 운동화',
      capture: jest.fn(async () => ({ path: `/tmp/frame-${++frame}.jpg` })),
      onObservation: jest.fn(async () => {}),
    };
  });
  afterEach(() => jest.useRealTimers());

  it('does no work when the option is OFF', async () => {
    const { result, unmount } = renderHook(() => useAppleAiFeedback({ ...options, enabled: false }));
    await flush(); await advance(60_000);
    expect(appleOnDeviceAi.getAvailability).not.toHaveBeenCalled();
    expect(options.capture).not.toHaveBeenCalled();
    expect(appleOnDeviceAi.analyzeFrames).not.toHaveBeenCalled();
    expect(appleOnDeviceAi.prepareFrames).not.toHaveBeenCalled();
    expect(options.onObservation).not.toHaveBeenCalled();
    expect(result.current.status).toBe('off'); unmount();
  });

  it('archives the original response and input batch before deleting camera snapshots', async () => {
    const saved = deferred<void>();
    jest.mocked(options.onObservation!).mockReturnValueOnce(saved.promise);
    const { unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    expect(options.onObservation).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: 'session-a', outcome: 'success', error: null,
      answer: { feedback: 'Keep the visible position.', elapsedMs: 800 },
      request: expect.objectContaining({ frames: expect.arrayContaining([expect.objectContaining({ path: '/tmp/frame-1.jpg' })]) }),
    }));
    expect(deleteAsync).not.toHaveBeenCalled();
    await act(async () => saved.resolve()); await flush();
    expect(deleteAsync).toHaveBeenCalledTimes(9);
    unmount(); await flush();
  });

  it('reports persistence failure and preserves input files without stopping feedback', async () => {
    jest.mocked(options.onObservation!).mockRejectedValueOnce(new Error('Disk full'));
    const warning = jest.spyOn(console, 'warn').mockImplementation(() => {});
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    expect(result.current.archiveError).toBe(true);
    expect(result.current.result?.feedback).toBe('Keep the visible position.');
    expect(deleteAsync).not.toHaveBeenCalled();
    unmount(); await flush(); warning.mockRestore();
  });

  it('waits for final persistence on stop and labels a late response cancelled', async () => {
    const native = deferred<AppleAiAnswer>(); const saved = deferred<void>();
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockReturnValueOnce(native.promise);
    jest.mocked(options.onObservation!).mockReturnValueOnce(saved.promise);
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    let stopped = false;
    act(() => { void result.current.stop().then(() => { stopped = true; }); });
    await act(async () => native.resolve({ feedback: 'late', elapsedMs: 5000 })); await flush();
    expect(options.onObservation).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: 'session-a', outcome: 'cancelled', error: 'cancelled', answer: null,
    }));
    expect(stopped).toBe(false);
    await act(async () => saved.resolve()); await flush();
    expect(stopped).toBe(true); expect(deleteAsync).toHaveBeenCalledTimes(9);
    unmount(); await flush();
  });

  it('archives model errors, including refusal, without treating them as posture feedback', async () => {
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockResolvedValueOnce({ feedback: '', elapsedMs: 0, error: 'refused' });
    const { unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    expect(options.onObservation).toHaveBeenCalledWith(expect.objectContaining({
      outcome: 'error', error: 'refused', answer: { feedback: '', elapsedMs: 0, error: 'refused' },
    }));
    unmount(); await flush();
  });

  it('collects six candidates, passes only three selected crops with original times, and waits after completion', async () => {
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    expect(appleOnDeviceAi.analyzeFrames).toHaveBeenCalledTimes(1);
    const request = jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls[0][0];
    expect(request).toMatchObject({ wodDescription: options.wodDescription, movements: options.movements,
      appearanceHints: options.appearanceHints, language: 'ko' });
    expect(request.frames.map(f => f.capturedAt)).toEqual([100_000, 102_400, 104_000]);
    expect(request.frames.map(f => f.path)).toEqual(['/tmp/input-0.jpg', '/tmp/input-3.jpg', '/tmp/input-5.jpg']);
    expect(jest.mocked(appleOnDeviceAi.prepareFrames).mock.calls[0][1]).toHaveLength(6);
    expect(options.onObservation).toHaveBeenCalledWith(expect.objectContaining({
      preparation: expect.objectContaining({ candidateCount: 6 }), inferenceStartedAt: 104_000,
      request: expect.objectContaining({ frames: expect.arrayContaining([expect.objectContaining({ path: '/tmp/frame-6.jpg' })]) }),
    }));
    expect(deleteAsync).toHaveBeenCalledTimes(9);
    expect(result.current.result).toMatchObject({ feedback: 'Keep the visible position.', capturedAt: 100_000, lastCapturedAt: 104_000, elapsedMs: 800 });
    await advance(9999); expect(options.capture).toHaveBeenCalledTimes(6);
    await advance(1); expect(options.capture).toHaveBeenCalledTimes(7);
    // Existing observation time must not advance while collecting a new request.
    expect(result.current.result?.capturedAt).toBe(100_000); unmount(); await flush();
  });

  it('waits for cancelled native inference to settle before a new session and discards its result', async () => {
    const pending = deferred<AppleAiAnswer>();
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockReturnValueOnce(pending.promise);
    const { result, rerender, unmount } = renderHook((props: AppleAiFeedbackOptions) => useAppleAiFeedback(props), { initialProps: options });
    await flush(); await advance(4000);
    const oldId = jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls[0][0].requestId;
    rerender({ ...options, running: false }); await flush();
    expect(appleOnDeviceAi.cancel).toHaveBeenCalledWith(oldId);
    const nextObserver = jest.fn(async () => {});
    rerender({ ...options, sessionId: 'session-b', appearanceHints: '흰 상의', movements: 'Row', onObservation: nextObserver }); await flush(); await advance(20_000);
    expect(options.capture).toHaveBeenCalledTimes(6);
    expect(deleteAsync).not.toHaveBeenCalled();
    await act(async () => { pending.resolve({ feedback: 'STALE', elapsedMs: 20_000 }); });
    await flush();
    expect(result.current.result).toBeNull();
    expect(options.onObservation).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'session-a', outcome: 'cancelled',
      request: expect.objectContaining({ appearanceHints: options.appearanceHints, movements: options.movements }) }));
    expect(nextObserver).not.toHaveBeenCalled();
    expect(deleteAsync).toHaveBeenCalledTimes(9);
    expect(options.capture).toHaveBeenCalledTimes(7);
    await advance(4000);
    expect(jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls[1][0].requestId).toContain('session-b');
    expect(jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls[1][0]).toMatchObject({ appearanceHints: '흰 상의', movements: 'Row' });
    expect(nextObserver).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'session-b', outcome: 'success' }));
    expect(result.current.result?.feedback).not.toBe('STALE'); unmount(); await flush();
  });

  it('cleans a snapshot which finishes after stop without sending it to the model', async () => {
    const snapshot = deferred<{ path: string }>();
    jest.mocked(options.capture).mockReturnValueOnce(snapshot.promise);
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); act(() => { void result.current.stop(); });
    await act(async () => { snapshot.resolve({ path: 'file:///tmp/late.jpg' }); });
    await flush();
    expect(deleteAsync).toHaveBeenCalledWith('file:///tmp/late.jpg', { idempotent: true });
    expect(appleOnDeviceAi.analyzeFrames).not.toHaveBeenCalled(); unmount();
  });

  it('injects the same appearance, planned movements and WOD into every request and archive', async () => {
    const { unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(18_000);
    expect(appleOnDeviceAi.analyzeFrames).toHaveBeenCalledTimes(2);
    for (const [request] of jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls) {
      expect(request).toMatchObject({ appearanceHints: options.appearanceHints,
        movements: options.movements, wodDescription: options.wodDescription });
    }
    for (const [observation] of jest.mocked(options.onObservation!).mock.calls) {
      expect(observation.request).toMatchObject({ appearanceHints: options.appearanceHints,
        movements: options.movements, wodDescription: options.wodDescription });
    }
    unmount(); await flush();
  });

  it('cancels an old target when appearance changes within a session and discards its late feedback', async () => {
    const pending = deferred<AppleAiAnswer>();
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockReturnValueOnce(pending.promise);
    const { result, rerender, unmount } = renderHook((props: AppleAiFeedbackOptions) => useAppleAiFeedback(props), { initialProps: options });
    await flush(); await advance(4000);
    rerender({ ...options, appearanceHints: '흰 상의' }); await flush();
    expect(appleOnDeviceAi.cancel).toHaveBeenCalled();
    await act(async () => pending.resolve({ feedback: 'old athlete', elapsedMs: 1000 })); await flush();
    expect(result.current.result).toBeNull();
    expect(jest.mocked(options.onObservation!).mock.calls[0][0]).toMatchObject({
      outcome: 'cancelled', answer: null, request: { appearanceHints: options.appearanceHints },
    });
    await advance(4000);
    expect(jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls[1][0].appearanceHints).toBe('흰 상의');
    unmount(); await flush();
  });

  it.each([undefined, '   '])('keeps omitted appearance empty rather than inventing a target (%s)', async appearanceHints => {
    const { unmount } = renderHook(() => useAppleAiFeedback({ ...options, appearanceHints }));
    await flush(); await advance(4000);
    expect(jest.mocked(appleOnDeviceAi.analyzeFrames).mock.calls[0][0].appearanceHints).toBe('');
    expect(jest.mocked(options.onObservation!).mock.calls[0][0].request.appearanceHints).toBe('');
    unmount(); await flush();
  });

  it('waits for preprocessing cancellation and archives selected inputs without starting inference', async () => {
    const pending = deferred<Awaited<ReturnType<typeof appleOnDeviceAi.prepareFrames>>>();
    const prepare = jest.mocked(appleOnDeviceAi.prepareFrames).getMockImplementation()!;
    jest.mocked(appleOnDeviceAi.prepareFrames).mockReturnValueOnce(pending.promise);
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    let stopped = false;
    act(() => { void result.current.stop().then(() => { stopped = true; }); });
    expect(stopped).toBe(false);
    expect(deleteAsync).not.toHaveBeenCalled();
    const args = jest.mocked(appleOnDeviceAi.prepareFrames).mock.calls[0];
    await act(async () => pending.resolve(await prepare(...args))); await flush();
    expect(stopped).toBe(true);
    expect(appleOnDeviceAi.analyzeFrames).not.toHaveBeenCalled();
    const observation = jest.mocked(options.onObservation!).mock.calls[0][0];
    expect(observation).toMatchObject({ outcome: 'cancelled', answer: null, preparation: { candidateCount: 6 } });
    expect(observation.inferenceStartedAt).toBeUndefined();
    expect(deleteAsync).toHaveBeenCalledTimes(9);
    unmount(); await flush();
  });

  it('archives candidate evidence when preprocessing fails and never sends uncropped substitutes', async () => {
    jest.mocked(appleOnDeviceAi.prepareFrames).mockRejectedValueOnce(new Error('image write failed'));
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    expect(result.current.error).toBe('analysis_failed');
    expect(appleOnDeviceAi.analyzeFrames).not.toHaveBeenCalled();
    expect(jest.mocked(options.onObservation!).mock.calls[0][0].request.frames).toHaveLength(6);
    expect(deleteAsync).toHaveBeenCalledTimes(6);
    unmount(); await flush();
  });

  it('shows a timeout without overlapping a native request that has not acknowledged cancellation', async () => {
    const pending = deferred<AppleAiAnswer>();
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockReturnValueOnce(pending.promise);
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(34_000);
    expect(result.current.error).toBe('timeout');
    expect(result.current.status).toBe('error');
    expect(appleOnDeviceAi.cancel).toHaveBeenCalledTimes(1);
    await advance(30_000);
    expect(appleOnDeviceAi.analyzeFrames).toHaveBeenCalledTimes(1);
    expect(options.capture).toHaveBeenCalledTimes(6);
    expect(deleteAsync).not.toHaveBeenCalled();
    await act(async () => { pending.resolve({ feedback: 'TOO LATE', elapsedMs: 60_000 }); });
    await flush();
    expect(result.current.result).toBeNull();
    expect(result.current.error).toBe('timeout');
    expect(deleteAsync).toHaveBeenCalledTimes(9);
    await advance(10_000); expect(options.capture).toHaveBeenCalledTimes(7);
    unmount(); await flush();
  });

  it('cleans partial capture on failure and retries without retaining an image queue', async () => {
    jest.mocked(options.capture).mockResolvedValueOnce({ path: '/tmp/first.jpg' }).mockRejectedValueOnce(new Error('capture failed'));
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(1000);
    expect(result.current.status).toBe('error');
    expect(deleteAsync).toHaveBeenCalledWith('file:///tmp/first.jpg', { idempotent: true });
    expect(appleOnDeviceAi.analyzeFrames).not.toHaveBeenCalled();
    await advance(14_000); expect(appleOnDeviceAi.analyzeFrames).toHaveBeenCalledTimes(1);
    unmount(); await flush();
  });

  it.each(['unsupported_device', 'model_not_ready', 'vision_unsupported', 'language_unsupported'])(
    'shows %s and never captures or silently falls back', async status => {
      jest.mocked(appleOnDeviceAi.getAvailability).mockResolvedValue(status);
      const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
      await flush(); await advance(20_000);
      expect(result.current.status).toBe(status); expect(options.capture).not.toHaveBeenCalled();
      expect(appleOnDeviceAi.analyzeFrames).not.toHaveBeenCalled(); unmount();
    },
  );

  it('cancels for low battery, waits for recovery, and suspends on background', async () => {
    const pending = deferred<AppleAiAnswer>();
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockReturnValueOnce(pending.promise);
    const { result, rerender, unmount } = renderHook((props: AppleAiFeedbackOptions) => useAppleAiFeedback(props), { initialProps: options });
    await flush(); await advance(4000);
    jest.mocked(Battery.getPowerStateAsync).mockResolvedValue({ batteryLevel: 0.15, lowPowerMode: false, batteryState: 1 });
    await advance(1000); expect(result.current.status).toBe('power'); expect(appleOnDeviceAi.cancel).toHaveBeenCalled();
    await act(async () => { pending.resolve({ feedback: 'STALE', elapsedMs: 3000 }); });
    jest.mocked(Battery.getPowerStateAsync).mockResolvedValue({ batteryLevel: 0.2, lowPowerMode: false, batteryState: 1 });
    await advance(5000); expect(result.current.status).toBe('recovering');
    await advance(29_999); expect(options.capture).toHaveBeenCalledTimes(6);
    await advance(1); expect(options.capture).toHaveBeenCalledTimes(7);
    rerender({ ...options, foreground: false }); await flush();
    const count = jest.mocked(options.capture).mock.calls.length;
    await advance(60_000); expect(options.capture).toHaveBeenCalledTimes(count); unmount();
  });

  it.each(['timeout', 'refused', 'context_too_large'])('keeps %s separate from posture feedback and cleans files', async error => {
    jest.mocked(appleOnDeviceAi.analyzeFrames).mockResolvedValue({ feedback: '', elapsedMs: 0, error });
    const { result, unmount } = renderHook(() => useAppleAiFeedback(options));
    await flush(); await advance(4000);
    expect(result.current.status).toBe('error'); expect(result.current.error).toBe(error);
    expect(result.current.result).toBeNull(); expect(deleteAsync).toHaveBeenCalledTimes(9);
    unmount(); await flush();
  });
});
