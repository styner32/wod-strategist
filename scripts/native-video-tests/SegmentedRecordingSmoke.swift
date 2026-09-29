// Compile with the patched SegmentedRecordingSession.swift; runs without a camera.
// Synthetic buffers are paced approximately in real time. This verifies containers/sample
// preservation, not real-device timing, power, camera drops, or H10 synchronization.
import AVFoundation
import Foundation
import CryptoKit

@main
struct Smoke {
  static func main() throws {
    let root = URL(fileURLWithPath: CommandLine.arguments.dropFirst().first ?? "/private/tmp/wod-segmented-smoke-\(UUID().uuidString)", isDirectory: true)
    try run(root: root, frames: 633)
    try run(root: root.appendingPathComponent("second-run"), frames: 93, toneBaseHz: 660)
    try run(root: root.appendingPathComponent("prep-failure"), frames: 6, forcePrepFailure: true)
    try run(root: root.appendingPathComponent("tail-timeout"), frames: 6, forceTailTimeout: true)
    try run(root: root.appendingPathComponent("24fps-invalid-duration"), frames: 6, fps: 24, invalidDuration: true)
    print(root.path)
  }

  static func run(root: URL, frames: Int, forcePrepFailure: Bool = false, forceTailTimeout: Bool = false, fps: Int32 = 30, invalidDuration: Bool = false, toneBaseHz: Double = 220) throws {
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    let source = root.appendingPathComponent("original.mp4")
    let completed = DispatchSemaphore(value: 0)
    let mutex = NSLock()
    var events: [[String: Any]] = []
    var finalized = false
    var finalCount = 0
    var result: [String: Any] = [:]
    var error: Error?
    let format = try audioFormat()
    let session = try SegmentedRecordingSession(sourceURL: source,
      videoSettings: [AVVideoCodecKey: AVVideoCodecType.h264, AVVideoWidthKey: 320, AVVideoHeightKey: 180,
        AVVideoCompressionPropertiesKey: [AVVideoAverageBitRateKey: 600000, AVVideoExpectedSourceFrameRateKey: fps,
                                         AVVideoAllowFrameReorderingKey: false]],
      audioSettings: try SegmentedAudioConfiguration(format: format, recommended: nil).settings,
      audioFormat: format, transform: .identity, metadata: [], clock: CMClockGetHostTimeClock(),
      frameDuration: CMTime(value: 1, timescale: fps),
      onSegment: { event in mutex.lock(); events.append(event); mutex.unlock() },
      onSourceFinalized: { _ in mutex.lock(); finalized = true; mutex.unlock() },
      onFinish: { metadata, failure in
        mutex.lock(); result = metadata; error = failure; finalCount += 1; mutex.unlock(); completed.signal()
      })
    if forcePrepFailure {
      try Data("existing analysis output".utf8).write(to: root.appendingPathComponent("analysis/original-segment-1.mp4"))
    }
    try session.start()
    let anchor = CMClockGetTime(CMClockGetHostTimeClock()) + CMTime(seconds: 0.1, preferredTimescale: 60000)
    for index in 0..<frames {
      let timestamp = anchor + CMTime(value: Int64(index), timescale: fps)
      session.append(buffer: try videoSample(at: timestamp, index: index, fps: fps, invalidDuration: invalidDuration), isVideo: true)
      session.append(buffer: try audioSample(at: timestamp, format: format, sampleCount: 48000 / Int(fps),
        startSample: index * (48000 / Int(fps)), toneBaseHz: toneBaseHz), isVideo: false)
      Thread.sleep(forTimeInterval: 1 / Double(fps) + 0.002)
    }
    session.stop()
    let sentinel = anchor + CMTime(seconds: 30, preferredTimescale: 60000)
    if !forceTailTimeout {
      session.append(buffer: try videoSample(at: sentinel, index: 0), isVideo: true)
      session.append(buffer: try audioSample(at: sentinel, format: format), isVideo: false)
    }
    try check(completed.wait(timeout: .now() + 60) == .success, "Recording completion timed out")
    if let error { throw error }
    try check(finalized, "Source-finalized callback missing")
    try check(abs((result["duration"] as? Double ?? 0) - Double(frames) / Double(fps)) < 0.001,
              "Last sample duration does not match configured capture FPS")
    try check(result["droppedVideoFrames"] as? Int == 0, "Writer dropped synthetic video frames")
    try check(result["droppedAudioBuffers"] as? Int == 0, "Writer dropped synthetic audio buffers")
    try check(finalCount == 1, "Terminal callback was duplicated")
    try check(result["tailDrainTimedOut"] as? Bool == forceTailTimeout, "Missing/inaccurate tail drain diagnostic")
    try check(events.count == (frames > 600 ? 3 : 1), "Unexpected segment count: \(events)")
    let terminalEvents = result["segments"] as? [[String: Any]] ?? []
    try check(terminalEvents.count == events.count, "Terminal event replay list is incomplete")
    try check(terminalEvents.filter { $0["isLast"] as? Bool == true }.count == 1 && terminalEvents.last?["isLast"] as? Bool == true,
              "Terminal final-fragment marker is missing or duplicated")
    try check(try countSamples(source, .video) == frames, "Original source lost video samples")
    if forcePrepFailure {
      try check(events.first?["status"] as? String == "failed", "Expected analysis packaging failure")
      try check(result["failedSegmentCount"] as? Int == 1, "Missing failed preparation counter")
      try check(events.first?["captureClockOffsetMs"] as? Double != nil, "Failure event lost capture clock metadata")
      print("PASS: failed analysis packaging retains \(frames)-frame original and durable-source callback")
      return
    }
    var analysisHashes: [SHA256.Digest] = []
    var analysisFrames = 0
    for event in events {
      try check(event["status"] as? String == "ready", "Analysis derivative failed: \(event)")
      let output = URL(string: event["path"] as! String)!
      analysisFrames += try countSamples(output, .video)
      analysisHashes += try videoPayloadHashes(output)
      try check(try countSamples(output, .audio) > 0, "Analysis derivative lost audio")
      let asset = AVURLAsset(url: output)
      let generator = AVAssetImageGenerator(asset: asset)
      generator.requestedTimeToleranceBefore = .zero
      generator.requestedTimeToleranceAfter = .zero
      var actual = CMTime.invalid
      _ = try generator.copyCGImage(at: .zero, actualTime: &actual)
      try check(abs(actual.seconds) < 0.001, "Analysis derivative is not decodable at zero")
    }
    try check(analysisFrames == frames, "Analysis segmentation lost or duplicated video samples")
    try check(try videoPayloadHashes(source) == analysisHashes, "Analysis packaging reencoded or reordered compressed video packets")
    print("PASS: \(frames) original/derivative frames with identical compressed video packets, \(events.count) AAC/video segments, zero-based decode, tail timeout=\(forceTailTimeout)")
  }
}
