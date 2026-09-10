import { createChunkRecordingCompletion } from '../chunkRecordingCompletion';

describe('chunk recording finalization', () => {
  beforeEach(() => jest.useFakeTimers());
  afterEach(() => jest.useRealTimers());

  it('waits for the final callback when user stop follows a timer stop', async () => {
    const nativeStop = jest.fn().mockResolvedValue(undefined);
    const completion = createChunkRecordingCompletion(nativeStop);

    // The 10-second timer requests stop. Native stop resolves immediately,
    // while the MOV writer is still finishing its file.
    const timerStop = completion.stop();
    await Promise.resolve();
    await Promise.resolve();
    expect(nativeStop).toHaveBeenCalledTimes(1);

    // The user stops the workout before onRecordingFinished arrives.
    const userStop = completion.stop();
    const merge = jest.fn();
    const localChunks: string[] = [];
    const afterStop = userStop.then(() => merge([...localChunks]));
    await Promise.resolve();
    expect(merge).not.toHaveBeenCalled();
    expect(nativeStop).toHaveBeenCalledTimes(1);

    // The callback registers the final path before allowing the merge.
    localChunks.push('/last-partial.mov');
    completion.finish('/last-partial.mov');
    await expect(timerStop).resolves.toBe('/last-partial.mov');
    await expect(userStop).resolves.toBe('/last-partial.mov');
    await afterStop;
    expect(merge).toHaveBeenCalledWith(['/last-partial.mov']);
    expect(jest.getTimerCount()).toBe(0);
  });

  it('unblocks stop for a discarded micro chunk or recording error', async () => {
    const completion = createChunkRecordingCompletion(jest.fn().mockResolvedValue(undefined));
    const stopping = completion.stop();
    completion.finish(null);
    await expect(stopping).resolves.toBeNull();
    expect(jest.getTimerCount()).toBe(0);
  });

  it('keeps the five-second safety limit when the native callback never arrives', async () => {
    const completion = createChunkRecordingCompletion(jest.fn().mockResolvedValue(undefined));
    const stopping = completion.stop();
    jest.advanceTimersByTime(4999);
    const finished = jest.fn();
    stopping.then(finished);
    await Promise.resolve();
    expect(finished).not.toHaveBeenCalled();
    jest.advanceTimersByTime(1);
    await expect(stopping).resolves.toBeNull();
  });

  it('propagates native stop errors without leaving a timeout running', async () => {
    const completion = createChunkRecordingCompletion(jest.fn().mockRejectedValue(new Error('camera stop failed')));
    await expect(completion.stop()).rejects.toThrow('camera stop failed');
    expect(jest.getTimerCount()).toBe(0);
  });

  it('does not stop an already finished chunk again', async () => {
    const nativeStop = jest.fn();
    const completion = createChunkRecordingCompletion(nativeStop);
    completion.finish('/finished.mov');
    await expect(completion.stop()).resolves.toBe('/finished.mov');
    expect(nativeStop).not.toHaveBeenCalled();
  });
});
