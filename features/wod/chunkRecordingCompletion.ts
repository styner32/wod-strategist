/** VisionCamera stopRecording resolves before onRecordingFinished. */
export function createChunkRecordingCompletion(
  stopRecording: () => Promise<void>,
  timeoutMs: number | null = 5000,
) {
  let resolveFinished!: (path: string | null) => void;
  const finished = new Promise<string | null>((resolve) => {
    resolveFinished = resolve;
  });
  let didFinish = false;
  let stopPromise: Promise<string | null> | undefined;

  return {
    finish(path: string | null) {
      if (didFinish) return;
      didFinish = true;
      resolveFinished(path);
    },
    stop(): Promise<string | null> {
      if (didFinish) return finished;
      if (!stopPromise) {
        stopPromise = new Promise<string | null>((resolve, reject) => {
          // Continuous recording drains derivative exports after the writer closes.
          const timeout = timeoutMs === null ? null : setTimeout(() => resolve(null), timeoutMs);
          finished.then((path) => {
            if (timeout !== null) clearTimeout(timeout);
            resolve(path);
          });
          // Native stop only requests finalization. Wait for the callback above.
          Promise.resolve().then(stopRecording).catch((error) => {
            if (timeout !== null) clearTimeout(timeout);
            reject(error);
          });
        });
      }
      return stopPromise;
    },
  };
}
