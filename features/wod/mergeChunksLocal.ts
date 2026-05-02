/**
 * Local video merge utility — concatenates camera chunk files into a single
 * MP4 using the native VideoMerger module (stream-copy, no re-encode).
 *
 * iOS:     AVMutableComposition + AVAssetExportPresetPassthrough
 * Android: MediaExtractor + MediaMuxer
 *
 * Typical merge of 30 chunks completes in ~1-2 seconds.
 */
import {
  cacheDirectory,
  getInfoAsync,
  deleteAsync,
} from 'expo-file-system/legacy';

import { VideoMergerModule } from '@/modules/video-merger';

/**
 * Merge an ordered list of chunk files into a single output MP4.
 *
 * @param chunkPaths Absolute file paths to the source chunks, in order.
 * @param outputPath Absolute file path for the merged result.
 * @returns The output path on success.
 * @throws If no chunks, a chunk is missing, or native merge fails.
 */
export async function mergeChunksLocal(
  chunkPaths: string[],
  outputPath: string,
): Promise<string> {
  if (chunkPaths.length === 0) {
    throw new Error('mergeChunksLocal: no chunk paths provided');
  }

  // Validate all chunks exist before attempting merge
  for (const p of chunkPaths) {
    const info = await getInfoAsync(p);
    if (!info.exists) {
      throw new Error(`mergeChunksLocal: chunk not found: ${p}`);
    }
  }

  // Remove existing output file if present
  const outputInfo = await getInfoAsync(outputPath);
  if (outputInfo.exists) {
    await deleteAsync(outputPath, { idempotent: true });
  }

  console.log(`🎬 Merging ${chunkPaths.length} chunks → ${outputPath}`);
  const start = Date.now();

  const result = await VideoMergerModule.mergeVideos(chunkPaths, outputPath);

  const elapsed = ((Date.now() - start) / 1000).toFixed(1);
  console.log(`🎬 Merge complete in ${elapsed}s → ${result.outputPath}`);

  return result.outputPath;
}

/**
 * Generate a temp output path for the merged video.
 * Uses the session ID to make it identifiable in logs.
 */
export function mergedOutputPath(sessionId: string): string {
  return `${cacheDirectory}merged_${sessionId}.mp4`;
}
