import { requireNativeModule } from 'expo';

interface MergeResult {
  success: boolean;
  outputPath: string;
  inputCount: number;
  durationSeconds?: number;
}

interface VideoMergerModuleType {
  /**
   * Merge all chunks by passthrough. iOS may return a MOV fallback.
   * Uses stream-copy (no re-encode) for fast, lossless concatenation.
   *
   * @param inputPaths Array of absolute file paths to source chunks.
   * @param outputPath Absolute file path for the merged output.
   * @returns Validated result with its actual output path. Sources are retained.
   */
  mergeVideos(inputPaths: string[], outputPath: string): Promise<MergeResult>;
}

export default requireNativeModule<VideoMergerModuleType>('VideoMerger');
