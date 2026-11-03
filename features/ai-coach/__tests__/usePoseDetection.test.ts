import { act, renderHook } from '@testing-library/react-native';
import type { TensorflowModel } from 'react-native-fast-tflite';
import { runAtTargetFps, type Frame } from 'react-native-vision-camera';
import { createResizePlugin } from 'vision-camera-resize-plugin';
import { usePoseDetection } from '../frame-processors/usePoseDetection';

const mockLoadModel: jest.MockedFunction<typeof import('react-native-fast-tflite').loadTensorflowModel> = jest.fn();
let mockSdkInitializations = 0;
let mockSdkInitializationError: Error | null = null;
let mockInsideFrame = false;
let mockHoldBridge = false;
let mockPendingBridge: (() => void)[] = [];

jest.mock('../../../assets/models/movenet_thunder.tflite', () => 1);
jest.mock('../../../assets/models/movenet_thunder_f32.tflite', () => 2);
jest.mock('react-native-fast-tflite', () => {
  mockSdkInitializations++;
  if (mockSdkInitializationError) throw mockSdkInitializationError;
  return { loadTensorflowModel: (...args: Parameters<typeof mockLoadModel>) => mockLoadModel(...args) };
});
jest.mock('vision-camera-resize-plugin', () => ({ createResizePlugin: jest.fn() }));
jest.mock('react-native-reanimated', () => ({
  useSharedValue: (initial: unknown) => {
    const React = jest.requireActual('react');
    const ref = React.useRef(null);
    if (ref.current === null) {
      let value = initial;
      ref.current = {
        get value() { return value; },
        set(next: unknown) { this.value = next; },
        set value(next: unknown) {
          if (mockInsideFrame) throw new Error('A frame processor wrote a Reanimated value directly.');
          value = next;
        },
      };
    }
    return ref.current;
  },
}));
jest.mock('react-native-worklets-core', () => ({
  useSharedValue: (initial: unknown) => jest.requireActual('react').useRef({ value: initial }).current,
  useRunOnJS: (callback: (...args: unknown[]) => void, dependencies: unknown[]) => jest.requireActual('react').useMemo(
    () => (...args: unknown[]) => {
      const deliver = () => callback(...args);
      if (mockHoldBridge) mockPendingBridge.push(deliver);
      else return Promise.resolve().then(deliver);
    }, dependencies,
  ),
}));
jest.mock('react-native-vision-camera', () => ({
  useFrameProcessor: (callback: (frame: unknown) => void, dependencies: unknown[]) => jest.requireActual('react').useMemo(
    () => ({ type: 'readonly', frameProcessor: (frame: unknown) => {
      mockInsideFrame = true;
      try { callback(frame); } finally { mockInsideFrame = false; }
    } }), dependencies,
  ),
  runAtTargetFps: jest.fn((_fps: number, callback: () => void) => callback()),
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

function keypoints(y = 0.2, x = 0.4, score = 0.8) {
  return new Float32Array(Array.from({ length: 17 }, () => [y, x, score]).flat());
}

describe('preview-only MoveNet diagnostics', () => {
  let model: TensorflowModel;
  let resize: jest.Mock;
  let intervalSpy: jest.SpyInstance;
  let clearIntervalSpy: jest.SpyInstance;
  const frame = { width: 720, height: 1280, orientation: 'portrait', timestamp: 1000 } as Frame;
  const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
  const advance = (ms: number) => act(async () => { await jest.advanceTimersByTimeAsync(ms); });

  beforeEach(() => {
    jest.useFakeTimers();
    jest.resetAllMocks();
    intervalSpy = jest.spyOn(global, 'setInterval');
    clearIntervalSpy = jest.spyOn(global, 'clearInterval');
    jest.mocked(runAtTargetFps).mockImplementation((_fps, callback) => callback());
    mockInsideFrame = false;
    mockSdkInitializationError = null;
    mockHoldBridge = false;
    mockPendingBridge = [];
    model = {
      delegate: 'default', run: jest.fn(), runSync: jest.fn(() => [keypoints()]), inputs: [], outputs: [],
    };
    resize = jest.fn(() => new Uint8Array(256 * 256 * 3));
    mockLoadModel.mockResolvedValue(model);
    jest.mocked(createResizePlugin).mockReturnValue({ resize });
  });

  afterEach(() => {
    intervalSpy.mockRestore();
    clearIntervalSpy.mockRestore();
    jest.clearAllTimers();
    jest.useRealTimers();
  });

  it('does not create native plugins, load a model, infer, or poll while disabled', async () => {
    const initializations = mockSdkInitializations;
    const { result, unmount } = renderHook(() => usePoseDetection(false));
    await flush();
    await advance(10_000);
    expect(mockSdkInitializations).toBe(initializations);
    expect(mockLoadModel).not.toHaveBeenCalled();
    expect(createResizePlugin).not.toHaveBeenCalled();
    expect(model.runSync).not.toHaveBeenCalled();
    expect(intervalSpy).not.toHaveBeenCalled();
    expect(result.current).toMatchObject({ status: 'disabled', error: null, hasInference: false, isModelLoaded: false });
    expect(result.current.frameProcessor).toBeUndefined();
    unmount();
  });

  it('does not initialize the SDK when the preview leaves before lazy loading begins', async () => {
    const initializations = mockSdkInitializations;
    const { rerender, unmount } = renderHook<ReturnType<typeof usePoseDetection>, { enabled: boolean }>(({ enabled }) => usePoseDetection(enabled), {
      initialProps: { enabled: true },
    });
    rerender({ enabled: false });
    await flush();
    expect(mockSdkInitializations).toBe(initializations);
    expect(mockLoadModel).not.toHaveBeenCalled();
    expect(createResizePlugin).not.toHaveBeenCalled();
    expect(intervalSpy).not.toHaveBeenCalled();
    unmount();
  });

  it('shows SDK initialization errors without creating a resize plugin or throwing during render', async () => {
    mockSdkInitializationError = new Error('Native JSI unavailable');
    const { result, unmount } = renderHook(() => usePoseDetection());
    await flush();
    expect(result.current).toMatchObject({ status: 'error', error: 'Native JSI unavailable', hasInference: false });
    expect(createResizePlugin).not.toHaveBeenCalled();
    expect(mockLoadModel).not.toHaveBeenCalled();
    expect(intervalSpy).not.toHaveBeenCalled();
    unmount();
  });

  it('loads only on enable and distinguishes a loaded model from its first inference', async () => {
    const pending = deferred<TensorflowModel>();
    mockLoadModel.mockReturnValue(pending.promise);
    const { result, rerender, unmount } = renderHook<ReturnType<typeof usePoseDetection>, { enabled: boolean }>(({ enabled }) => usePoseDetection(enabled, { modelSource: 2, fps: 15 }), {
      initialProps: { enabled: false },
    });
    rerender({ enabled: true });
    expect(result.current.status).toBe('loading');
    expect(result.current.frameProcessor).toBeUndefined();
    await flush();
    expect(mockLoadModel).toHaveBeenCalledWith(2);
    expect(createResizePlugin).toHaveBeenCalledTimes(1);
    expect(intervalSpy).not.toHaveBeenCalled();
    await act(async () => pending.resolve(model));
    await flush();
    expect(result.current).toMatchObject({ status: 'loaded', isModelLoaded: true, hasInference: false });
    expect(intervalSpy).toHaveBeenCalledTimes(1);
    expect(intervalSpy).toHaveBeenCalledWith(expect.any(Function), 500);
    const timer = intervalSpy.mock.results[0].value;
    act(() => result.current.frameProcessor!.frameProcessor(frame));
    await flush();
    expect(result.current.hasInference).toBe(true);
    expect(result.current.monitorData.confidence).toBeCloseTo(0.8);
    expect(runAtTargetFps).toHaveBeenCalledWith(15, expect.any(Function));
    expect(resize).toHaveBeenCalledWith(frame, { scale: { width: 256, height: 256 }, pixelFormat: 'rgb', dataType: 'uint8' });
    unmount();
    expect(clearIntervalSpy).toHaveBeenCalledWith(timer);
  });


  it('ignores model loading results after disable and unmount', async () => {
    const first = deferred<TensorflowModel>();
    const second = deferred<TensorflowModel>();
    mockLoadModel.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { result, rerender, unmount } = renderHook<ReturnType<typeof usePoseDetection>, { enabled: boolean }>(({ enabled }) => usePoseDetection(enabled), { initialProps: { enabled: true } });
    await flush();
    rerender({ enabled: false });
    await act(async () => first.resolve(model));
    await flush();
    expect(result.current.status).toBe('disabled');
    expect(result.current.frameProcessor).toBeUndefined();
    expect(intervalSpy).not.toHaveBeenCalled();
    rerender({ enabled: true });
    await flush();
    unmount();
    await act(async () => second.resolve(model));
    await flush();
    expect(intervalSpy).not.toHaveBeenCalled();
  });

  it('drops an old JS delivery after disabling and starting a fresh preview', async () => {
    const { result, rerender, unmount } = renderHook<ReturnType<typeof usePoseDetection>, { enabled: boolean }>(({ enabled }) => usePoseDetection(enabled), { initialProps: { enabled: true } });
    await flush();
    const firstTimer = intervalSpy.mock.results[0].value;
    mockHoldBridge = true;
    act(() => result.current.frameProcessor!.frameProcessor(frame));
    expect(mockPendingBridge).toHaveLength(1);
    rerender({ enabled: false });
    expect(clearIntervalSpy).toHaveBeenCalledWith(firstTimer);
    rerender({ enabled: true });
    await flush();
    act(() => mockPendingBridge.splice(0).forEach(deliver => deliver()));
    await advance(500);
    expect(result.current.hasInference).toBe(false);
    expect(result.current.poseResult.value).toEqual([]);
    expect(result.current.monitorData.poseData).toEqual([]);
    unmount();
    expect(clearIntervalSpy).toHaveBeenCalledWith(intervalSpy.mock.results[1].value);
  });

  it('preserves float outputs, retains previous pose on the camera runtime, and computes motion', async () => {
    jest.mocked(model.runSync).mockReturnValueOnce([keypoints(1.2, 0.4)]).mockReturnValueOnce([keypoints(1.2, 0.5)]);
    const { result, unmount } = renderHook(() => usePoseDetection());
    await flush();
    act(() => result.current.frameProcessor!.frameProcessor(frame));
    await flush();
    await advance(500);
    expect(result.current.poseResult.value[0]).toBeCloseTo(1.2);
    expect(result.current.monitorData.confidence).toBeCloseTo(0.8);
    expect(result.current.monitorData.motion).toBe(0);
    expect(result.current.monitorData.isWorkingOut).toBe(false);
    act(() => result.current.frameProcessor!.frameProcessor(frame));
    await flush();
    await advance(500);
    expect(result.current.monitorData.motion).toBeCloseTo(0.03);
    expect(result.current.monitorData.isWorkingOut).toBe(true);
    expect(runAtTargetFps).toHaveBeenCalledWith(2, expect.any(Function));
    unmount();
  });

  it('ignores an in-flight JS result after the preview unmounts', async () => {
    const { result, unmount } = renderHook(() => usePoseDetection());
    await flush();
    mockHoldBridge = true;
    const pose = result.current.poseResult;
    act(() => result.current.frameProcessor!.frameProcessor(frame));
    unmount();
    act(() => mockPendingBridge.splice(0).forEach(deliver => deliver()));
    expect(pose.value).toEqual([]);
  });

  it.each(['success', 'failure'])('does not let an old native inference %s change a new preview history', async outcome => {
    const view = renderHook<ReturnType<typeof usePoseDetection>, { enabled: boolean }>(({ enabled }) => usePoseDetection(enabled), {
      initialProps: { enabled: true },
    });
    await flush();
    const oldProcessor = view.result.current.frameProcessor!;
    jest.mocked(model.runSync).mockImplementationOnce(() => {
      // The native inference is blocked while the JS runtime opens a new preview.
      mockInsideFrame = false;
      try {
        view.rerender({ enabled: false });
        expect(view.result.current.status).toBe('disabled');
        view.rerender({ enabled: true });
        expect(view.result.current.status).toBe('loading');
      } finally {
        mockInsideFrame = true;
      }
      if (outcome === 'failure') throw new Error('Previous model failed late');
      return [keypoints(0.2, 0.1)];
    });
    oldProcessor.frameProcessor(frame);
    await flush();
    expect(view.result.current.status).toBe('loaded');
    expect(view.result.current.hasInference).toBe(false);
    act(() => view.result.current.frameProcessor!.frameProcessor(frame));
    await flush();
    expect(view.result.current.hasInference).toBe(true);
    expect(view.result.current.monitorData.motion).toBe(0);
    expect(view.result.current.error).toBeNull();
    view.unmount();
  });

  it.each(['resize', 'inference', 'output length', 'non-finite output'])('reports %s failures and stops inference and polling', async failure => {
    if (failure === 'resize') resize.mockImplementationOnce(() => { throw new Error('Resize failed'); });
    if (failure === 'inference') jest.mocked(model.runSync).mockImplementationOnce(() => { throw new Error('Inference failed'); });
    if (failure === 'output length') jest.mocked(model.runSync).mockReturnValueOnce([new Float32Array(50)]);
    if (failure === 'non-finite output') { const invalid = keypoints(); invalid[4] = NaN; jest.mocked(model.runSync).mockReturnValueOnce([invalid]); }
    const { result, unmount } = renderHook(() => usePoseDetection());
    await flush();
    const oldProcessor = result.current.frameProcessor!;
    const timer = intervalSpy.mock.results[0].value;
    act(() => oldProcessor.frameProcessor(frame));
    await flush();
    expect(result.current.status).toBe('error');
    expect(result.current.error).toBeTruthy();
    expect(result.current.hasInference).toBe(false);
    expect(result.current.frameProcessor).toBeUndefined();
    expect(result.current.poseResult.value).toEqual([]);
    expect(clearIntervalSpy).toHaveBeenCalledWith(timer);
    const calls = resize.mock.calls.length;
    act(() => oldProcessor.frameProcessor(frame));
    expect(resize).toHaveBeenCalledTimes(calls);
    unmount();
  });

  it('surfaces native plugin creation and model-load errors without throwing from the hook', async () => {
    jest.mocked(createResizePlugin).mockImplementationOnce(() => { throw new Error('Resize plugin unavailable'); });
    const first = renderHook(() => usePoseDetection());
    await flush();
    expect(first.result.current).toMatchObject({ status: 'error', error: 'Resize plugin unavailable' });
    expect(mockLoadModel).not.toHaveBeenCalled();
    first.unmount();
    mockLoadModel.mockRejectedValueOnce(new Error('Model unavailable'));
    const second = renderHook(() => usePoseDetection());
    await flush();
    expect(second.result.current).toMatchObject({ status: 'error', error: 'Model unavailable' });
    expect(intervalSpy).not.toHaveBeenCalled();
    second.unmount();
  });
});
