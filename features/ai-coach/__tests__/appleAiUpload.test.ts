import { copyAsync, moveAsync, deleteAsync } from 'expo-file-system/legacy';
import { getUploadUrl, uploadSessionAssetToGcs } from '../../wod/api';
import { saveAppleAiObservation, flushAppleAiUploads, type AppleAiArchive } from '../appleAiUpload';
import type { AppleAiObservation } from '../appleAiObservation';

const mockFiles = new Map<string, string>();
let mockDocumentDirectory = 'file:///docs/';
let mockSequence = 0;
jest.mock('ulid', () => ({ ulid: () => `01ARZ3NDEKTSV4RRFFQ69G5F${String(++mockSequence).padStart(2, '0')}` }));
jest.mock('../../wod/api', () => ({ getUploadUrl: jest.fn(), uploadSessionAssetToGcs: jest.fn() }));
jest.mock('expo-file-system/legacy', () => ({
  get documentDirectory() { return mockDocumentDirectory; },
  makeDirectoryAsync: jest.fn(async () => {}),
  getInfoAsync: jest.fn(async (path: string) => ({ exists: mockFiles.has(path) || [...mockFiles.keys()].some(key => key.startsWith(path)) })),
  readDirectoryAsync: jest.fn(async (path: string) => [...new Set([...mockFiles.keys()].filter(key => key.startsWith(path)).map(key => key.slice(path.length).split('/')[0]))]),
  writeAsStringAsync: jest.fn(async (path: string, value: string) => { mockFiles.set(path, value); }),
  readAsStringAsync: jest.fn(async (path: string) => {
    if (!mockFiles.has(path)) throw new Error('Missing file');
    return mockFiles.get(path);
  }),
  copyAsync: jest.fn(async ({ from, to }: { from: string; to: string }) => {
    if (!mockFiles.has(from)) throw new Error('Missing image');
    mockFiles.set(to, mockFiles.get(from)!);
  }),
  moveAsync: jest.fn(async ({ from, to }: { from: string; to: string }) => {
    if (!mockFiles.has(from)) throw new Error('Missing file');
    mockFiles.set(to, mockFiles.get(from)!); mockFiles.delete(from);
  }),
  deleteAsync: jest.fn(async (path: string) => {
    for (const key of mockFiles.keys()) if (key === path || key.startsWith(path + (path.endsWith('/') ? '' : '/'))) mockFiles.delete(key);
  }),
}));

const manifestPaths = () => [...mockFiles.keys()].filter(path => path.endsWith('/manifest.json'));
const observation = (): AppleAiObservation => ({
  sessionId: 'WOD-20260921-01ARZ3NDEKTSV4RRFFQ69G5FAV',
  request: { requestId: 'request-1', wodDescription: '5 squats', movements: 'Squat', appearanceHints: '검은 상의, 분홍 운동화', language: 'ko',
    frames: [0, 1, 2].map(index => ({ path: `/tmp/frame-${index}.jpg`, capturedAt: 100_000 + index * 1000 })) },
  startedAt: 100_000, completedAt: 105_000, outcome: 'success', error: null,
  answer: { feedback: '관찰 원문', elapsedMs: 2500 },
});

const preparedObservation = (): AppleAiObservation => {
  const result = observation();
  result.request.frames = Array.from({ length: 6 }, (_, index) => ({ path: `/tmp/frame-${index}.jpg`, capturedAt: 100_000 + index * 800 }));
  result.inferenceStartedAt = 104_500;
  result.preparation = {
    version: 1, crop: { x: 0.2, y: 0.1, width: 0.6, height: 0.8 }, cropMode: 'person_with_context',
    fallbackReason: null, selectionMethod: 'endpoints_plus_visual_change', candidateCount: 6,
    personCounts: [1, 1, 1, 1, 1, 1], changeScores: [0, 0.1, 0.3, 0.4, 0.1, 0], elapsedMs: 100,
    frames: [0, 3, 5].map(index => ({ ...result.request.frames[index], path: `/tmp/input-${index}.jpg`,
      sourceIndex: index, width: 300, height: 500 })),
  };
  for (const frame of [...result.request.frames, ...result.preparation.frames]) mockFiles.set(`file://${frame.path}`, frame.path);
  return result;
};

describe('Apple AI durable evidence uploads', () => {
  beforeEach(() => {
    jest.clearAllMocks(); mockFiles.clear(); mockSequence = 0; mockDocumentDirectory = 'file:///docs/';
    for (let index = 0; index < 3; index++) mockFiles.set(`file:///tmp/frame-${index}.jpg`, `image-${index}`);
    jest.mocked(getUploadUrl).mockImplementation(async (_session, filename) => ({ upload_url: `https://upload/${filename}`, gcs_uri: 'gs://bucket/object' }));
    jest.mocked(uploadSessionAssetToGcs).mockImplementation(async (_url, path) => {
      if (!mockFiles.has(path)) throw new Error('Missing file');
    });
    jest.spyOn(console, 'warn').mockImplementation(() => {});
  });
  afterEach(() => jest.restoreAllMocks());

  it('persists original feedback, WOD, timing and copied images without temporary paths in JSON', async () => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    const archive: AppleAiArchive = JSON.parse(mockFiles.get(manifestPaths()[0])!);
    expect(archive).toMatchObject({ profileId: 7, sessionStartedAt: 90_000, outcome: 'success',
      answer: { feedback: '관찰 원문', elapsedMs: 2500 }, context: { movements: 'Squat', appearanceHints: '검은 상의, 분홍 운동화', language: 'ko' },
      clock: 'capture_epoch_ms_not_merged_video_time' });
    expect(archive.frames.map(frame => frame.captureOffsetMs)).toEqual([10_000, 11_000, 12_000]);
    expect(archive.frames[0].objectName).toBe(`videos/7/${archive.sessionId}/${archive.frames[0].filename}`);
    expect(JSON.stringify(archive)).not.toContain('/tmp/');
    expect(copyAsync).toHaveBeenCalledTimes(3);
    expect(moveAsync).toHaveBeenLastCalledWith({ from: manifestPaths()[0].replace('.json', '.tmp'), to: manifestPaths()[0] });
    expect(getUploadUrl).not.toHaveBeenCalled();
  });

  it('uploads images before the JSON, pins the profile, then deletes the committed local bundle', async () => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    await flushAppleAiUploads();
    expect(jest.mocked(uploadSessionAssetToGcs).mock.calls.map(call => call[2])).toEqual(['image/jpeg', 'image/jpeg', 'image/jpeg', 'application/json']);
    expect(jest.mocked(getUploadUrl).mock.calls.every(call => call[0] === observation().sessionId && call[2] === 7)).toBe(true);
    expect(manifestPaths()).toEqual([]);
    expect([...mockFiles.keys()].filter(path => path.includes('/apple-ai/'))).toEqual([]);
  });

  it.each(['image', 'manifest'])('retains the entire bundle after %s upload failure and retries the same object names', async failed => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    const original = [...mockFiles.entries()];
    jest.mocked(uploadSessionAssetToGcs).mockImplementation(async (_url, _path, mime) => {
      if (mime === (failed === 'image' ? 'image/jpeg' : 'application/json')) throw new Error('Offline');
    });
    await flushAppleAiUploads();
    expect([...mockFiles.entries()]).toEqual(original);
    const firstName = jest.mocked(getUploadUrl).mock.calls[0][1];
    if (failed === 'image') expect(uploadSessionAssetToGcs).toHaveBeenCalledTimes(1);
    jest.mocked(uploadSessionAssetToGcs).mockResolvedValue();
    jest.mocked(getUploadUrl).mockClear();
    await flushAppleAiUploads();
    expect(jest.mocked(getUploadUrl).mock.calls[0][1]).toBe(firstName);
    expect(manifestPaths()).toEqual([]);
  });

  it('recovers from a changed iOS Documents path using bundle-relative filenames', async () => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    for (const [path, value] of [...mockFiles]) {
      if (path.startsWith('file:///docs/')) { mockFiles.set(path.replace('file:///docs/', 'file:///new-docs/'), value); mockFiles.delete(path); }
    }
    mockDocumentDirectory = 'file:///new-docs/';
    await flushAppleAiUploads();
    expect(uploadSessionAssetToGcs).toHaveBeenCalledTimes(4);
    expect(jest.mocked(uploadSessionAssetToGcs).mock.calls.every(call => call[1].startsWith('file:///new-docs/'))).toBe(true);
  });

  it('does not expose partially saved bundles after a copy failure', async () => {
    jest.mocked(copyAsync).mockRejectedValueOnce(new Error('Disk full'));
    await expect(saveAppleAiObservation(observation(), 7, 90_000)).rejects.toThrow('Disk full');
    await flushAppleAiUploads();
    expect(getUploadUrl).not.toHaveBeenCalled();
    expect(mockFiles.has('file:///tmp/frame-0.jpg')).toBe(true);
  });

  it('cleans an already uploaded bundle after restart without sending it again', async () => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    const path = manifestPaths()[0];
    mockFiles.set(path.replace('manifest.json', 'uploaded.json'), mockFiles.get(path)!); mockFiles.delete(path);
    await flushAppleAiUploads();
    expect(getUploadUrl).not.toHaveBeenCalled();
    expect(deleteAsync).toHaveBeenCalled();
  });

  it('serializes concurrent flushes and includes an entry saved during an in-flight upload', async () => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    let release!: () => void;
    jest.mocked(uploadSessionAssetToGcs).mockImplementationOnce(() => new Promise(resolve => { release = resolve; }));
    const first = flushAppleAiUploads();
    for (let index = 0; index < 20; index++) await Promise.resolve();
    await saveAppleAiObservation({ ...observation(), sessionId: 'another-session' }, 8, 90_000);
    const second = flushAppleAiUploads();
    expect(first).toBe(second);
    release(); await first;
    expect(uploadSessionAssetToGcs).toHaveBeenCalledTimes(8);
    expect(manifestPaths()).toEqual([]);
  });

  it('archives error/cancelled partial batches without inventing a successful answer', async () => {
    const record = observation(); record.outcome = 'cancelled'; record.error = 'cancelled'; record.answer = null;
    record.request.frames = record.request.frames.slice(0, 1);
    await saveAppleAiObservation(record, 7, 90_000);
    expect(JSON.parse(mockFiles.get(manifestPaths()[0])!)).toMatchObject({ outcome: 'cancelled', answer: null, frames: [expect.any(Object)] });
    await flushAppleAiUploads();
    expect(uploadSessionAssetToGcs).toHaveBeenCalledTimes(2);
  });

  it('archives all six candidates and exact selected inputs with source references, without native paths', async () => {
    await saveAppleAiObservation(preparedObservation(), 7, 90_000);
    const archive: AppleAiArchive = JSON.parse(mockFiles.get(manifestPaths()[0])!);
    expect(archive).toMatchObject({ schemaVersion: 2, promptVersion: 3, inferenceStartedAt: 104_500,
      preparation: { candidateCount: 6, cropMode: 'person_with_context' } });
    expect(archive.frames).toHaveLength(6);
    expect(archive.preparation?.frames.map(frame => frame.sourceIndex)).toEqual([0, 3, 5]);
    expect(archive.preparation?.frames.map(frame => frame.captureOffsetMs)).toEqual([10_000, 12_400, 14_000]);
    expect(JSON.stringify(archive)).not.toContain('/tmp/');
    await flushAppleAiUploads();
    const calls = jest.mocked(getUploadUrl).mock.calls;
    expect(calls).toHaveLength(10);
    expect(calls[6][1]).toContain('_input_1.jpg');
    expect(calls[9][1]).toMatch(/\.json$/);
    expect(manifestPaths()).toEqual([]);
  });

  it('preserves original and selected inputs if copying a crop fails', async () => {
    const record = preparedObservation();
    mockFiles.delete('file:///tmp/input-3.jpg');
    await expect(saveAppleAiObservation(record, 7, 90_000)).rejects.toThrow('Missing image');
    expect(manifestPaths()).toEqual([]);
    expect(mockFiles.has('file:///tmp/input-0.jpg')).toBe(true);
    await flushAppleAiUploads();
    expect(getUploadUrl).not.toHaveBeenCalled();
  });

  it('retains a bundle when a prepared input upload fails, with no remote JSON commit', async () => {
    await saveAppleAiObservation(preparedObservation(), 7, 90_000);
    jest.mocked(uploadSessionAssetToGcs).mockImplementation(async (_url, path) => {
      if (path.endsWith('_input_2.jpg')) throw new Error('Offline');
    });
    await flushAppleAiUploads();
    expect(manifestPaths()).toHaveLength(1);
    expect(jest.mocked(uploadSessionAssetToGcs).mock.calls.some(call => call[2] === 'application/json')).toBe(false);
    jest.mocked(uploadSessionAssetToGcs).mockResolvedValue();
    await flushAppleAiUploads();
    expect(manifestPaths()).toEqual([]);
  });

  it.each([1, 2] as const)('continues uploading existing schema %s bundles without appearance context', async version => {
    await saveAppleAiObservation(observation(), 7, 90_000);
    const path = manifestPaths()[0];
    const old = JSON.parse(mockFiles.get(path)!);
    old.schemaVersion = version; old.promptVersion = version;
    delete old.context.appearanceHints;
    mockFiles.set(path, JSON.stringify(old));
    await flushAppleAiUploads();
    expect(uploadSessionAssetToGcs).toHaveBeenCalledTimes(4);
    expect(manifestPaths()).toEqual([]);
  });

  it('rejects invalid selected-frame references before any upload', async () => {
    await saveAppleAiObservation(preparedObservation(), 7, 90_000);
    const path = manifestPaths()[0];
    const archive = JSON.parse(mockFiles.get(path)!);
    archive.preparation.frames[1].sourceIndex = 99;
    mockFiles.set(path, JSON.stringify(archive));
    await flushAppleAiUploads();
    expect(getUploadUrl).not.toHaveBeenCalled();
    expect(manifestPaths()).toHaveLength(1);
  });
});
