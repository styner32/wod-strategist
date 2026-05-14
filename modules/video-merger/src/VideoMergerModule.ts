import { requireNativeModule } from 'expo';

interface MergeResult {
  success: boolean;
  outputPath: string;
}

interface VideoMergerModuleType {
  /**
   * Merge multiple video chunks into a single MP4 file.
   * Uses stream-copy (no re-encode) for fast, lossless concatenation.
   *
   * @param inputPaths Array of absolute file paths to source chunks.
   * @param outputPath Absolute file path for the merged output.
   * @returns Promise resolving to { success: true, outputPath: string }
   */
  mergeVideos(inputPaths: string[], outputPath: string): Promise<MergeResult>;
}

export default requireNativeModule<VideoMergerModuleType>('VideoMerger');
