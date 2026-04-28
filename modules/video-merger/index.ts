/**
 * VideoMerger — native Expo module for merging video chunks.
 *
 * iOS: AVMutableComposition + AVAssetExportSession (passthrough/stream-copy)
 * Android: MediaExtractor + MediaMuxer (stream-copy)
 *
 * Both platforms produce a single MP4 from an ordered list of chunk files
 * without re-encoding (~1-2s for 30 chunks).
 */
export { default as VideoMergerModule } from './src/VideoMergerModule';
