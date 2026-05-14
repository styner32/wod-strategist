/**
 * Unit tests for mergeChunksLocal.
 *
 * The native VideoMergerModule is mocked — we only test the TS wrapper logic:
 *   - validation (empty paths, missing files)
 *   - existing output cleanup
 *   - correct delegation to native mergeVideos
 *   - output path generation
 */

const mockMergeVideos = jest.fn();
const mockGetInfoAsync = jest.fn();
const mockDeleteAsync = jest.fn().mockResolvedValue(undefined);

jest.mock('expo-file-system/legacy', () => ({
  documentDirectory: '/mock/documents/',
  getInfoAsync: (...args: unknown[]) => mockGetInfoAsync(...args),
  deleteAsync: (...args: unknown[]) => mockDeleteAsync(...args),
}));

jest.mock('@/modules/video-merger', () => ({
  VideoMergerModule: {
    mergeVideos: (...args: unknown[]) => mockMergeVideos(...args),
  },
}));

import { mergeChunksLocal, mergedOutputPath } from '../mergeChunksLocal';

beforeEach(() => {
  jest.clearAllMocks();
  // Default: all files exist, output does NOT exist
  mockGetInfoAsync.mockResolvedValue({ exists: true });
  mockMergeVideos.mockResolvedValue({ success: true, outputPath: '/out.mp4' });
});

describe('mergeChunksLocal', () => {
  it('throws if no chunk paths provided', async () => {
    await expect(mergeChunksLocal([], '/out.mp4'))
      .rejects.toThrow('no chunk paths provided');
  });

  it('throws if a chunk file does not exist', async () => {
    mockGetInfoAsync
      .mockResolvedValueOnce({ exists: true })    // first chunk
      .mockResolvedValueOnce({ exists: false });   // second chunk

    await expect(
      mergeChunksLocal(['/a.mp4', '/b.mp4'], '/out.mp4')
    ).rejects.toThrow('chunk not found: /b.mp4');
  });

  it('delegates to VideoMergerModule.mergeVideos', async () => {
    // Output doesn't exist yet
    mockGetInfoAsync.mockImplementation(async (path: string) => {
      if (path === '/out.mp4') return { exists: false };
      return { exists: true };
    });

    const result = await mergeChunksLocal(
      ['/chunk0.mp4', '/chunk1.mp4'],
      '/out.mp4',
    );

    expect(mockMergeVideos).toHaveBeenCalledWith(
      ['/chunk0.mp4', '/chunk1.mp4'],
      '/out.mp4',
    );
    expect(result).toBe('/out.mp4');
  });

  it('deletes existing output file before merging', async () => {
    mockGetInfoAsync.mockImplementation(async (path: string) => {
      // All files exist, including the output
      return { exists: true };
    });

    await mergeChunksLocal(['/chunk0.mp4'], '/out.mp4');

    expect(mockDeleteAsync).toHaveBeenCalledWith('/out.mp4', { idempotent: true });
    expect(mockMergeVideos).toHaveBeenCalled();
  });

  it('does not delete output if it does not exist', async () => {
    mockGetInfoAsync.mockImplementation(async (path: string) => {
      if (path === '/out.mp4') return { exists: false };
      return { exists: true };
    });

    await mergeChunksLocal(['/chunk0.mp4'], '/out.mp4');

    expect(mockDeleteAsync).not.toHaveBeenCalled();
  });

  it('propagates native merge errors', async () => {
    mockGetInfoAsync.mockResolvedValue({ exists: true });
    mockGetInfoAsync.mockImplementation(async (path: string) => {
      if (path === '/out.mp4') return { exists: false };
      return { exists: true };
    });
    mockMergeVideos.mockRejectedValue(new Error('native crash'));

    await expect(
      mergeChunksLocal(['/chunk0.mp4'], '/out.mp4')
    ).rejects.toThrow('native crash');
  });
});

describe('mergedOutputPath', () => {
  it('generates a path in documentDirectory with session ID', () => {
    const path = mergedOutputPath('WOD-20260428-ABC123');
    expect(path).toBe('/mock/documents/merged_WOD-20260428-ABC123.mp4');
  });
});
