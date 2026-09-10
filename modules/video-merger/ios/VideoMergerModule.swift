import ExpoModulesCore
import AVFoundation

public class VideoMergerModule: Module {
  public func definition() -> ModuleDefinition {
    Name("VideoMerger")

    /// Merge multiple video chunks into a single MP4 file.
    ///
    /// - Parameter inputPaths: Array of absolute file paths to the source chunks.
    /// - Parameter outputPath: Absolute file path for the merged output.
    /// - Returns: Dictionary with `success: true` and `outputPath`.
    AsyncFunction("mergeVideos") { (inputPaths: [String], outputPath: String, promise: Promise) in
      DispatchQueue.global(qos: .userInitiated).async {
        do {
          let result = try self.mergeSync(inputPaths: inputPaths, outputPath: outputPath)
          promise.resolve(result)
        } catch {
          promise.reject("VIDEO_MERGE_ERROR", error.localizedDescription)
        }
      }
    }
  }

  // MARK: - Core merge logic

  private func mergeSync(inputPaths: [String], outputPath: String) throws -> [String: Any] {
    guard !inputPaths.isEmpty else {
      throw MergeError.noInputFiles
    }

    let composition = AVMutableComposition()

    guard let videoTrack = composition.addMutableTrack(
      withMediaType: .video,
      preferredTrackID: kCMPersistentTrackID_Invalid
    ) else {
      throw MergeError.trackCreationFailed("video")
    }

    // Audio track is optional — some chunks may be video-only
    let audioTrack = composition.addMutableTrack(
      withMediaType: .audio,
      preferredTrackID: kCMPersistentTrackID_Invalid
    )

    var insertionTime: CMTime = .zero

    for (index, path) in inputPaths.enumerated() {
      let url = Self.fileURL(from: path)

      guard FileManager.default.fileExists(atPath: url.path) else {
        throw MergeError.fileNotFound(path)
      }

      let asset = AVURLAsset(url: url, options: [AVURLAssetPreferPreciseDurationAndTimingKey: true])

      // Video track (required)
      guard let sourceVideoTrack = asset.tracks(withMediaType: .video).first else {
        NSLog("⚠️ [VideoMerger] Chunk %d has no video track, skipping: %@", index, path)
        continue
      }

      var duration = asset.duration
      if duration <= .zero {
        duration = sourceVideoTrack.timeRange.duration
      }

      // Skip empty or micro chunks (e.g. < 0.1s from immediate stop)
      if duration <= .zero || duration.seconds < 0.1 {
        NSLog("⚠️ [VideoMerger] Chunk %d has zero or negligible duration (%.3fs), skipping: %@", index, duration.seconds, path)
        continue
      }

      let timeRange = CMTimeRange(start: .zero, duration: duration)
      try videoTrack.insertTimeRange(timeRange, of: sourceVideoTrack, at: insertionTime)

      // Preserve the first valid chunk's transform (orientation) for the entire composition
      if insertionTime == .zero {
        videoTrack.preferredTransform = sourceVideoTrack.preferredTransform
      }

      // Audio track (optional — skip silently if chunk has no audio)
      if let sourceAudioTrack = asset.tracks(withMediaType: .audio).first,
         let audioTrack = audioTrack {
        let audioDuration = min(duration.seconds, sourceAudioTrack.timeRange.duration.seconds)
        if audioDuration > 0 {
          let audioRange = CMTimeRange(start: .zero, duration: CMTime(seconds: audioDuration, preferredTimescale: duration.timescale))
          try? audioTrack.insertTimeRange(audioRange, of: sourceAudioTrack, at: insertionTime)
        }
      }

      insertionTime = CMTimeAdd(insertionTime, duration)
    }

    guard insertionTime > .zero else {
      throw MergeError.noValidVideoTracks
    }

    // Remove empty audio track if no audio was inserted across all chunks
    if let audioTrack = audioTrack, audioTrack.timeRange.duration <= .zero {
      composition.removeTrack(audioTrack)
    }

    // Remove existing output file if present
    let outputURL = Self.fileURL(from: outputPath)
    if FileManager.default.fileExists(atPath: outputURL.path) {
      try FileManager.default.removeItem(at: outputURL)
    }

    // Export with passthrough preset (stream-copy, no re-encode)
    guard let exportSession = AVAssetExportSession(
      asset: composition,
      presetName: AVAssetExportPresetPassthrough
    ) else {
      throw MergeError.exportSessionFailed
    }

    exportSession.outputURL = outputURL
    exportSession.outputFileType = .mp4

    let semaphore = DispatchSemaphore(value: 0)
    var exportError: Error?

    exportSession.exportAsynchronously {
      if exportSession.status == .failed {
        exportError = exportSession.error
      }
      semaphore.signal()
    }

    semaphore.wait()

    if let error = exportError {
      throw error
    }

    guard exportSession.status == .completed else {
      throw MergeError.exportFailed(exportSession.status.rawValue)
    }

    return [
      "success": true,
      "outputPath": outputPath,
    ]
  }

  // MARK: - Helpers

  private static func fileURL(from path: String) -> URL {
    if path.hasPrefix("file://") {
      return URL(string: path)!
    }
    return URL(fileURLWithPath: path)
  }

  // MARK: - Errors

  private enum MergeError: LocalizedError {
    case noInputFiles
    case fileNotFound(String)
    case trackCreationFailed(String)
    case missingTrack(String, Int)
    case noValidVideoTracks
    case exportSessionFailed
    case exportFailed(Int)

    var errorDescription: String? {
      switch self {
      case .noInputFiles:
        return "No input files provided"
      case .fileNotFound(let path):
        return "File not found: \(path)"
      case .trackCreationFailed(let type):
        return "Failed to create \(type) track in composition"
      case .missingTrack(let type, let index):
        return "Chunk \(index) has no \(type) track"
      case .noValidVideoTracks:
        return "No valid video tracks found in input files"
      case .exportSessionFailed:
        return "Failed to create export session"
      case .exportFailed(let status):
        return "Export failed with status: \(status)"
      }
    }
  }
}
