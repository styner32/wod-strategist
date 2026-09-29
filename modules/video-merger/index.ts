/**
 * VideoMerger — native Expo module for merging video chunks.
 *
 * iOS: AVMutableComposition + AVAssetExportSession (passthrough/stream-copy)
 * Android: MediaExtractor + MediaMuxer (stream-copy)
 *
 * Both platforms retain every input and avoid re-encoding. Android produces
 * MP4; iOS can fall back to MOV when MP4 cannot carry the source codecs.
 */
export { default as VideoMergerModule } from './src/VideoMergerModule';
