import { documentDirectory, getInfoAsync } from "expo-file-system/legacy";

import { VideoMergerModule } from "@/modules/video-merger";

/**
 * Merge every ordered chunk without deleting sources or a previous result.
 * iOS can return a MOV when the source codecs cannot pass through to MP4.
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
    throw new Error("mergeChunksLocal: no chunk paths provided");
  }

  for (const p of chunkPaths) {
    const info = await getInfoAsync(p);
    if (!info.exists) {
      throw new Error(`mergeChunksLocal: chunk not found: ${p}`);
    }
    if (info.isDirectory || info.size === 0) {
      throw new Error(`mergeChunksLocal: chunk is empty or unreadable: ${p}`);
    }
  }

  // Native code validates a unique temporary sibling before replacing a result.
  // Never pre-delete it here: a retry may fail.
  console.log(`🎬 Merging ${chunkPaths.length} chunks → ${outputPath}`);
  const start = Date.now();

  const result = await VideoMergerModule.mergeVideos(
    chunkPaths,
    outputPath,
  );

  // Older native binaries can silently omit a chunk. Require the new native
  // completion evidence before the retention store may publish/clean originals.
  if (!result.success || !result.outputPath || result.inputCount !== chunkPaths.length) {
    throw new Error("mergeChunksLocal: native merger did not preserve every input");
  }
  const outputInfo = await getInfoAsync(result.outputPath);
  if (!outputInfo.exists || outputInfo.isDirectory || outputInfo.size === 0) {
    throw new Error("mergeChunksLocal: native output is missing or empty");
  }

  const elapsed = ((Date.now() - start) / 1000).toFixed(1);
  console.log(`🎬 Merge complete in ${elapsed}s → ${result.outputPath}`);

  return result.outputPath;
}

/**
 * Generate a persistent output path for the merged video.
 * Uses documentDirectory so the file survives cache purges and remains
 * accessible from the app container until the user explicitly deletes it.
 */
export function mergedOutputPath(sessionId: string): string {
  return `${documentDirectory}merged_${sessionId}.mp4`;
}
