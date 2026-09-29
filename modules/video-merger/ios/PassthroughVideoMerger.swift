import AVFoundation
import CryptoKit
import Darwin
import Foundation

/// No source deletion, transcoding, or best-effort skipping is allowed here.
enum PassthroughVideoMerger {
  private struct Coverage {
    var video: Int64 = 0
    var audio: Int64 = 0
    var videoHashes: [SHA256.Digest] = []
  }
  private struct Placement {
    let url: URL
    let offset: CMTime
  }
  private final class CopyFailure {
    private let lock = NSLock()
    private var stored: Error?
    func set(_ error: Error) { lock.lock(); defer { lock.unlock() }; if stored == nil { stored = error } }
    func get() -> Error? { lock.lock(); defer { lock.unlock() }; return stored }
  }

  static func merge(inputPaths: [String], outputPath: String) throws -> [String: Any] {
    guard !inputPaths.isEmpty else { throw failure("No input files provided") }
    let inputs = try inputPaths.map(fileURL)
    let requested = try fileURL(outputPath)
    let destinations: [(AVFileType, URL)] = [
      (.mp4, requested.deletingPathExtension().appendingPathExtension("mp4")),
      (.mov, requested.deletingPathExtension().appendingPathExtension("mov")),
    ]
    for (_, destination) in destinations {
      guard !inputs.contains(where: { $0.resolvingSymlinksInPath() == destination.resolvingSymlinksInPath() }) else {
        throw failure("Output would overwrite a source chunk")
      }
    }

    let composition = AVMutableComposition()
    guard let video = composition.addMutableTrack(withMediaType: .video, preferredTrackID: kCMPersistentTrackID_Invalid),
          let audio = composition.addMutableTrack(withMediaType: .audio, preferredTrackID: kCMPersistentTrackID_Invalid) else {
      throw failure("Could not create composition tracks")
    }
    var cursor = CMTime.zero
    var expected = Coverage()
    var orientation: CGAffineTransform?
    var size: CGSize?
    var videoFormats: [CMFormatDescription]?
    var audioFormats: [CMFormatDescription]?
    var placements: [Placement] = []
    var expectedVideoStart = CMTime.zero
    var expectedAudioStart = CMTime.zero
    var expectedVideoEnd = CMTime.zero
    var expectedAudioEnd = CMTime.zero
    for (index, url) in inputs.enumerated() {
      try requireNonemptyFile(url)
      let asset = AVURLAsset(url: url, options: [AVURLAssetPreferPreciseDurationAndTimingKey: true])
      let videos = asset.tracks(withMediaType: .video)
      let audios = asset.tracks(withMediaType: .audio)
      guard videos.count == 1, audios.count <= 1 else {
        throw failure("Chunk \(index) must have exactly one video track and at most one audio track")
      }
      let sourceVideo = videos[0]
      guard valid(sourceVideo.timeRange) else { throw failure("Chunk \(index) has an unreadable video duration") }
      if let orientation, !sameTransform(orientation, sourceVideo.preferredTransform) {
        throw failure("Chunk \(index) has an incompatible orientation; originals retained")
      }
      if let size, size != sourceVideo.naturalSize {
        throw failure("Chunk \(index) has incompatible dimensions; originals retained")
      }
      let sourceVideoFormats = try formats(sourceVideo)
      if let videoFormats, !sameFormats(videoFormats, sourceVideoFormats) {
        throw failure("Chunk \(index) has an incompatible video codec or format; originals retained")
      }
      if let sourceAudio = audios.first {
        let sourceAudioFormats = try formats(sourceAudio)
        if let audioFormats, !sameFormats(audioFormats, sourceAudioFormats) {
          throw failure("Chunk \(index) has an incompatible audio codec or format; originals retained")
        }
        audioFormats = sourceAudioFormats
      }
      videoFormats = sourceVideoFormats
      orientation = sourceVideo.preferredTransform
      size = sourceVideo.naturalSize
      video.preferredTransform = sourceVideo.preferredTransform

      // Keep track start offsets and the full audio tail. No zero-based reads or
      // trimming audio to the container/video duration.
      var start = sourceVideo.timeRange.start
      var end = CMTimeRangeGetEnd(sourceVideo.timeRange)
      if let sourceAudio = audios.first {
        guard valid(sourceAudio.timeRange) else { throw failure("Chunk \(index) has an unreadable audio duration") }
        start = CMTimeMinimum(start, sourceAudio.timeRange.start)
        end = CMTimeMaximum(end, CMTimeRangeGetEnd(sourceAudio.timeRange))
      }
      let coverage = try sampleCoverage(asset)
      guard coverage.video > 0, audios.isEmpty || coverage.audio > 0 else {
        throw failure("Chunk \(index) contains an empty or unreadable track")
      }
      try video.insertTimeRange(sourceVideo.timeRange, of: sourceVideo,
                                at: cursor + sourceVideo.timeRange.start - start)
      if let sourceAudio = audios.first {
        try audio.insertTimeRange(sourceAudio.timeRange, of: sourceAudio,
                                  at: cursor + sourceAudio.timeRange.start - start)
      }
      expected.video += coverage.video
      expected.videoHashes.append(contentsOf: coverage.videoHashes)
      expected.audio += coverage.audio
      if index == 0 {
        expectedVideoStart = sourceVideo.timeRange.start - start
        if let sourceAudio = audios.first { expectedAudioStart = sourceAudio.timeRange.start - start }
      }
      placements.append(Placement(url: url, offset: cursor - start))
      expectedVideoEnd = cursor + CMTimeRangeGetEnd(sourceVideo.timeRange) - start
      if let sourceAudio = audios.first { expectedAudioEnd = cursor + CMTimeRangeGetEnd(sourceAudio.timeRange) - start }
      cursor = cursor + end - start
    }
    if expected.audio == 0 { composition.removeTrack(audio) }
    // A camera run may legitimately have no audio track. Its video must remain
    // in the movie, with an empty audio edit until the next recorded audio span.
    if expected.audio > 0 {
      expectedAudioStart = audio.timeRange.start
      expectedAudioEnd = CMTimeRangeGetEnd(audio.timeRange)
    }
    // AAC packets can contain only priming/padding; packet equality alone is
    // neither necessary nor sufficient for presented-audio preservation.
    let expectedAudio = expected.audio > 0 ? try audioFingerprint(composition) : nil

    var errors: [String] = []
    // Composition exports are preferred. Some fragmented sources expose a
    // shorter audio timeRange than their compressed samples; if an export
    // loses packets, remux those samples with no decoder or encoder instead.
    let attempts = destinations.map { ($0.0, $0.1, false) } + destinations.map { ($0.0, $0.1, true) }
    for (fileType, destination, copyCompressed) in attempts {
      let temp = destination.deletingLastPathComponent()
        .appendingPathComponent(".merge-\(UUID().uuidString).\(destination.pathExtension)")
      defer { try? FileManager.default.removeItem(at: temp) }
      do {
        if copyCompressed {
          try remux(placements, to: temp, type: fileType, hasAudio: expected.audio > 0, duration: cursor)
        } else {
          try export(composition, to: temp, type: fileType)
          try PassthroughMovieTimeline.preserve(composition, in: temp)
        }
        try requireNonemptyFile(temp)
        let output = AVURLAsset(url: temp, options: [AVURLAssetPreferPreciseDurationAndTimingKey: true])
        let coverage = try sampleCoverage(output)
        guard coverage.video == expected.video, (expected.audio == 0 ? coverage.audio == 0 : coverage.audio > 0) else {
          throw failure("Export lost media samples (video \(coverage.video)/\(expected.video), audio \(coverage.audio)/\(expected.audio))")
        }
        guard coverage.videoHashes == expected.videoHashes else {
          throw failure("Export changed the compressed video samples or their order")
        }
        let duration = output.duration.seconds
        guard output.duration.isNumeric, abs(duration - cursor.seconds) <= 0.1 else {
          throw failure("Export duration does not match the complete source timeline")
        }
        guard let outputVideo = output.tracks(withMediaType: .video).first,
              let orientation, let size, let videoFormats,
              sameTransform(orientation, outputVideo.preferredTransform), size == outputVideo.naturalSize,
              sameFormats(videoFormats, try formats(outputVideo)) else {
          throw failure("Export changed the video codec, dimensions or orientation")
        }
        guard matchesTime(outputVideo.timeRange.start, expectedVideoStart, scale: outputVideo.naturalTimeScale),
              matchesTime(CMTimeRangeGetEnd(outputVideo.timeRange), expectedVideoEnd, scale: outputVideo.naturalTimeScale) else {
          throw failure("Export changed the video sample start or endpoint")
        }
        if let audioFormats {
          guard let outputAudio = output.tracks(withMediaType: .audio).first,
                sameFormats(audioFormats, try formats(outputAudio)) else {
            throw failure("Export changed the audio codec or format")
          }
          guard matchesTime(outputAudio.timeRange.start, expectedAudioStart, scale: outputAudio.naturalTimeScale),
                matchesTime(CMTimeRangeGetEnd(outputAudio.timeRange), expectedAudioEnd, scale: outputAudio.naturalTimeScale) else {
            throw failure("Export changed the audio sample start or endpoint")
          }
          guard try audioFingerprint(output) == expectedAudio else {
            throw failure("Export changed the presented audio samples or alignment")
          }
        }
        // A same-directory rename replaces a prior result atomically only after
        // validation. A failed export/rename leaves that result untouched.
        guard rename(temp.path, destination.path) == 0 else {
          throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
        }
        return ["success": true, "outputPath": destination.absoluteString,
                "inputCount": inputs.count, "durationSeconds": duration,
                "method": copyCompressed ? "compressed_remux" : "composition_passthrough"]
      } catch {
        errors.append("\(destination.pathExtension) \(copyCompressed ? "remux" : "export"): \(error.localizedDescription)")
      }
    }
    throw failure("Passthrough merge failed; originals and prior output retained. " + errors.joined(separator: "; "))
  }

  private static func export(_ asset: AVAsset, to url: URL, type: AVFileType) throws {
    guard let exporter = AVAssetExportSession(asset: asset, presetName: AVAssetExportPresetPassthrough),
          exporter.supportedFileTypes.contains(type) else { throw failure("Unsupported passthrough container") }
    exporter.outputURL = url
    exporter.outputFileType = type
    let finished = DispatchSemaphore(value: 0)
    exporter.exportAsynchronously { finished.signal() }
    guard finished.wait(timeout: .now() + 300) == .success else {
      exporter.cancelExport()
      throw failure("Passthrough export timed out; originals retained")
    }
    guard exporter.status == .completed else {
      throw exporter.error ?? failure("Passthrough export failed (\(exporter.status.rawValue))")
    }
  }

  private static func remux(_ placements: [Placement], to url: URL, type: AVFileType, hasAudio: Bool, duration: CMTime) throws {
    let writer = try AVAssetWriter(outputURL: url, fileType: type)
    let types: [AVMediaType] = hasAudio ? [.video, .audio] : [.video]
    var inputs: [(AVMediaType, AVAssetWriterInput)] = []
    var movieScale: Int64 = 1
    for placement in placements {
      let asset = AVURLAsset(url: placement.url)
      for mediaType in types {
        guard let track = asset.tracks(withMediaType: mediaType).first else {
          if mediaType == .audio { continue }
          throw failure("Missing remux source track")
        }
        let scale = Int64(track.naturalTimeScale)
        guard scale > 0 else { throw failure("Unknown source media timescale") }
        movieScale = (movieScale / greatestCommonDivisor(movieScale, scale)) * scale
        guard movieScale <= Int32.max else { throw failure("Source media timescales cannot be preserved") }
      }
    }
    for mediaType in types {
      let sourceTrack = placements.lazy.compactMap { AVURLAsset(url: $0.url).tracks(withMediaType: mediaType).first }.first
      guard let track = sourceTrack else { throw failure("Missing remux source track") }
      // nil settings mean compressed packet passthrough, never encoding.
      let input = AVAssetWriterInput(mediaType: mediaType, outputSettings: nil, sourceFormatHint: try formats(track)[0])
      input.expectsMediaDataInRealTime = false
      if mediaType == .video {
        input.mediaTimeScale = track.naturalTimeScale
        input.transform = track.preferredTransform
      }
      guard writer.canAdd(input) else { throw failure("Unsupported passthrough writer container") }
      writer.add(input)
      inputs.append((mediaType, input))
    }
    // A 600-tick movie clock truncates valid sub-millisecond AAC tails. Use a
    // common exact clock while preserving each compressed track's own scale.
    writer.movieTimeScale = CMTimeScale(movieScale)
    for (mediaType, input) in inputs where mediaType == .video { input.mediaTimeScale = CMTimeScale(movieScale) }
    guard writer.startWriting() else { throw writer.error ?? failure("Could not start compressed remux") }
    writer.startSession(atSourceTime: .zero)
    let group = DispatchGroup()
    let failed = CopyFailure()
    for (mediaType, input) in inputs {
      group.enter()
      DispatchQueue.global(qos: .userInitiated).async {
        defer { group.leave() }
        do {
          for placement in placements {
            let asset = AVURLAsset(url: placement.url)
            guard let track = asset.tracks(withMediaType: mediaType).first else {
              if mediaType == .audio { continue }
              throw failure("Missing remux source track")
            }
            let reader = try AVAssetReader(asset: asset)
            let output = AVAssetReaderTrackOutput(track: track, outputSettings: nil)
            output.alwaysCopiesSampleData = false
            guard reader.canAdd(output) else { throw failure("Cannot read compressed source track") }
            reader.add(output)
            guard reader.startReading() else { throw reader.error ?? failure("Cannot read compressed source") }
            defer { reader.cancelReading() }
            while let sample = output.copyNextSampleBuffer() {
              // Reader also emits empty boundary markers. They contain no
              // media samples and are not passed to a compressed writer.
              if CMSampleBufferGetNumSamples(sample) == 0 { continue }
              let deadline = Date().addingTimeInterval(300)
              while !input.isReadyForMoreMediaData {
                guard writer.status == .writing, Date() < deadline else {
                  throw writer.error ?? failure("Compressed remux timed out or was cancelled")
                }
                Thread.sleep(forTimeInterval: 0.002)
              }
              let adjusted = try retime(sample, offset: placement.offset)
              guard input.append(adjusted) else { throw writer.error ?? failure("Could not copy compressed sample") }
            }
            guard reader.status == .completed else { throw reader.error ?? failure("Incomplete compressed source read") }
          }
          input.markAsFinished()
        } catch {
          failed.set(error)
          writer.cancelWriting()
        }
      }
    }
    guard group.wait(timeout: .now() + 300) == .success else {
      writer.cancelWriting()
      throw failure("Compressed remux timed out; originals retained")
    }
    if let error = failed.get() { throw error }
    writer.endSession(atSourceTime: duration)
    let finished = DispatchSemaphore(value: 0)
    writer.finishWriting { finished.signal() }
    guard finished.wait(timeout: .now() + 300) == .success, writer.status == .completed else {
      writer.cancelWriting()
      throw writer.error ?? failure("Compressed remux finalization failed")
    }
  }

  private static func retime(_ sample: CMSampleBuffer, offset: CMTime) throws -> CMSampleBuffer {
    var count = 0
    guard CMSampleBufferGetSampleTimingInfoArray(sample, entryCount: 0, arrayToFill: nil, entriesNeededOut: &count) == noErr,
          count > 0 else { throw failure("Missing compressed sample timing") }
    var timing = Array(repeating: CMSampleTimingInfo(), count: count)
    guard CMSampleBufferGetSampleTimingInfoArray(sample, entryCount: count, arrayToFill: &timing, entriesNeededOut: &count) == noErr else {
      throw failure("Unreadable compressed sample timing")
    }
    for i in timing.indices {
      if timing[i].presentationTimeStamp.isNumeric { timing[i].presentationTimeStamp = timing[i].presentationTimeStamp + offset }
      if timing[i].decodeTimeStamp.isNumeric { timing[i].decodeTimeStamp = timing[i].decodeTimeStamp + offset }
    }
    var result: CMSampleBuffer?
    guard CMSampleBufferCreateCopyWithNewTiming(allocator: kCFAllocatorDefault, sampleBuffer: sample,
      sampleTimingEntryCount: timing.count, sampleTimingArray: &timing, sampleBufferOut: &result) == noErr,
      let result else { throw failure("Could not preserve compressed sample timing") }
    return result
  }

  private static func sampleCoverage(_ asset: AVAsset) throws -> Coverage {
    var result = Coverage()
    for type in [AVMediaType.video, AVMediaType.audio] {
      let tracks = asset.tracks(withMediaType: type)
      guard tracks.count <= 1, type != .video || tracks.count == 1 else { throw failure("Unexpected output tracks") }
      guard let track = tracks.first else { continue }
      let reader = try AVAssetReader(asset: asset)
      let output = AVAssetReaderTrackOutput(track: track, outputSettings: nil)
      output.alwaysCopiesSampleData = false
      guard reader.canAdd(output) else { throw failure("Cannot inspect compressed samples") }
      reader.add(output)
      guard reader.startReading() else { throw reader.error ?? failure("Cannot read source samples") }
      var count: Int64 = 0
      while let sample = output.copyNextSampleBuffer() {
        guard CMSampleBufferDataIsReady(sample) else { throw failure("Unreadable compressed sample") }
        count += Int64(CMSampleBufferGetNumSamples(sample))
        if type == .video, CMSampleBufferGetNumSamples(sample) > 0 {
          guard let block = CMSampleBufferGetDataBuffer(sample) else { throw failure("Missing compressed video data") }
          let size = CMBlockBufferGetDataLength(block)
          guard size > 0 else { throw failure("Empty compressed video data") }
          var bytes = Data(count: size)
          let status = bytes.withUnsafeMutableBytes {
            CMBlockBufferCopyDataBytes(block, atOffset: 0, dataLength: size, destination: $0.baseAddress!)
          }
          guard status == noErr else { throw failure("Unreadable compressed video data") }
          result.videoHashes.append(SHA256.hash(data: bytes))
        }
      }
      guard reader.status == .completed else { throw reader.error ?? failure("Incomplete media read") }
      if type == .video { result.video = count } else { result.audio = count }
    }
    return result
  }

  /// Canonical interleaved float PCM at the original sample rate, with explicit
  /// silence for track gaps. Only verification decodes; output remains copied
  /// compressed media. Batching differences do not affect this fingerprint.
  private static func audioFingerprint(_ asset: AVAsset) throws -> String {
    guard let track = asset.tracks(withMediaType: .audio).first,
          let description = (track.formatDescriptions as? [CMFormatDescription])?.first,
          let stream = CMAudioFormatDescriptionGetStreamBasicDescription(description) else {
      throw failure("Cannot verify source audio format")
    }
    let rate = stream.pointee.mSampleRate
    let channels = Int(stream.pointee.mChannelsPerFrame)
    guard rate > 0, rate <= Double(Int32.max), rate == rate.rounded(), channels > 0 else {
      throw failure("Unsupported presented audio clock")
    }
    let reader = try AVAssetReader(asset: asset)
    let settings: [String: Any] = [AVFormatIDKey: kAudioFormatLinearPCM, AVLinearPCMBitDepthKey: 32,
      AVLinearPCMIsFloatKey: true, AVLinearPCMIsBigEndianKey: false, AVLinearPCMIsNonInterleaved: false]
    let output = AVAssetReaderTrackOutput(track: track, outputSettings: settings)
    guard reader.canAdd(output) else { throw failure("Cannot verify presented audio") }
    reader.add(output)
    guard reader.startReading() else { throw reader.error ?? failure("Cannot decode verification audio") }
    defer { reader.cancelReading() }
    var hasher = SHA256()
    var frameCursor: Int64 = 0
    let bytesPerFrame = channels * MemoryLayout<Float>.size
    let silenceFrames = 4096
    let silence = Data(count: silenceFrames * bytesPerFrame)
    while let sample = output.copyNextSampleBuffer() {
      let frames = CMSampleBufferGetNumSamples(sample)
      if frames == 0 { continue }
      let pts = CMSampleBufferGetPresentationTimeStamp(sample)
      guard pts.isNumeric, let block = CMSampleBufferGetDataBuffer(sample) else {
        throw failure("Unreadable presented audio sample")
      }
      let start = CMTimeConvertScale(pts, timescale: CMTimeScale(rate), method: .roundHalfAwayFromZero).value
      guard start >= frameCursor else { throw failure("Overlapping presented audio timestamps") }
      while frameCursor < start {
        let count = min(Int64(silenceFrames), start - frameCursor)
        hasher.update(data: silence.prefix(Int(count) * bytesPerFrame))
        frameCursor += count
      }
      let size = CMBlockBufferGetDataLength(block)
      guard size == frames * bytesPerFrame else { throw failure("Unexpected presented audio sample layout") }
      var bytes = Data(count: size)
      let status = bytes.withUnsafeMutableBytes { destination in
        CMBlockBufferCopyDataBytes(block, atOffset: 0, dataLength: size, destination: destination.baseAddress!)
      }
      guard status == noErr else { throw failure("Cannot read presented audio samples") }
      // AAC's decoder can return either IEEE signed zero for identical digital
      // silence. Normalize only that representation; every nonzero bit stays
      // exact, so changed waveform samples are still rejected.
      bytes.withUnsafeMutableBytes { (raw: UnsafeMutableRawBufferPointer) in
        for i in stride(from: 0, to: raw.count, by: MemoryLayout<Float>.size) {
          if raw[i] == 0, raw[i + 1] == 0, raw[i + 2] == 0, raw[i + 3] == 0x80 { raw[i + 3] = 0 }
        }
      }
      hasher.update(data: bytes)
      frameCursor += Int64(frames)
    }
    guard reader.status == .completed else { throw reader.error ?? failure("Incomplete audio verification") }
    return "\(frameCursor):" + hasher.finalize().map { String(format: "%02x", $0) }.joined()
  }

  private static func valid(_ range: CMTimeRange) -> Bool {
    range.isValid && range.start.isNumeric && range.duration.isNumeric && range.duration > .zero
  }

  private static func matchesTime(_ actual: CMTime, _ expected: CMTime, scale: CMTimeScale) -> Bool {
    let tolerance = 1.0 / Double(max(scale, expected.timescale))
    return actual.isNumeric && abs(actual.seconds - expected.seconds) <= tolerance + 0.000000001
  }

  private static func greatestCommonDivisor(_ a: Int64, _ b: Int64) -> Int64 {
    var x = a, y = b
    while y != 0 { let remainder = x % y; x = y; y = remainder }
    return x
  }

  private static func formats(_ track: AVAssetTrack) throws -> [CMFormatDescription] {
    guard let descriptions = track.formatDescriptions as? [CMFormatDescription], !descriptions.isEmpty else {
      throw failure("Missing codec format description")
    }
    return descriptions
  }

  private static func sameFormats(_ a: [CMFormatDescription], _ b: [CMFormatDescription]) -> Bool {
    // Average bitrate changes with chunk content/length, including a valid
    // single-frame tail. Ignore only that informational atom, keeping codec
    // configuration, dimensions, color, audio rate and channel layout exact.
    !a.isEmpty && !b.isEmpty && a.allSatisfy { first in b.allSatisfy { second in
      CMFormatDescriptionEqualIgnoringExtensionKeys(first, otherFormatDescription: second,
        extensionKeysToIgnore: nil, sampleDescriptionExtensionAtomKeysToIgnore: "btrt" as CFString)
    }
    }
  }

  private static func sameTransform(_ a: CGAffineTransform, _ b: CGAffineTransform) -> Bool {
    zip([a.a, a.b, a.c, a.d, a.tx, a.ty], [b.a, b.b, b.c, b.d, b.tx, b.ty])
      .allSatisfy { abs($0 - $1) < 0.00001 }
  }

  private static func requireNonemptyFile(_ url: URL) throws {
    let values = try url.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey])
    guard values.isRegularFile == true, (values.fileSize ?? 0) > 0 else { throw failure("Empty or missing chunk: \(url.path)") }
  }

  private static func fileURL(_ path: String) throws -> URL {
    if path.hasPrefix("file:") {
      guard let url = URL(string: path), url.isFileURL, url.host == nil || url.host == "" || url.host == "localhost" else {
        throw failure("Invalid local file URI")
      }
      return url.standardizedFileURL
    }
    guard path.hasPrefix("/") else { throw failure("An absolute local file path is required") }
    return URL(fileURLWithPath: path).standardizedFileURL
  }

  private static func failure(_ message: String) -> NSError {
    NSError(domain: "VideoMerger", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
  }
}
