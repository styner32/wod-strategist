import Foundation
import CoreGraphics
import ImageIO
import Vision
import UniformTypeIdentifiers

struct AppleAiFrame: Sendable {
  let path: String
  let capturedAt: Double
}

struct AppleAiPreparedFrame: Sendable {
  let path: String
  let capturedAt: Double
  let sourceIndex: Int
  let width: Int
  let height: Int

  var dictionary: [String: Any] {
    ["path": path, "capturedAt": capturedAt, "sourceIndex": sourceIndex, "width": width, "height": height]
  }
}

struct AppleAiPreparation: Sendable {
  let frames: [AppleAiPreparedFrame]
  let crop: CGRect
  let reason: String?
  let changed: Bool
  let personCounts: [Int]
  let scores: [Double]
  let elapsedMs: Double

  var dictionary: [String: Any] {
    ["version": 1, "frames": frames.map(\.dictionary),
     "crop": ["x": crop.minX, "y": crop.minY, "width": crop.width, "height": crop.height],
     "cropMode": reason == nil ? "person_with_context" : "full_frame",
     "fallbackReason": reason as Any? ?? NSNull(),
     "selectionMethod": reason != nil && reason != "context_needs_full_frame" ? "uniform_unreliable_subject" : (changed ? "endpoints_plus_visual_change" : "uniform_low_change"),
     "candidateCount": personCounts.count, "personCounts": personCounts, "changeScores": scores,
     "elapsedMs": elapsedMs]
  }
}

enum AppleAiFramePreprocessor {
  static func prepare(_ frames: [AppleAiFrame]) throws -> AppleAiPreparation {
    guard frames.count == 6, frames.allSatisfy({ $0.capturedAt.isFinite }),
          zip(frames, frames.dropFirst()).allSatisfy({ $0.capturedAt < $1.capturedAt }) else {
      throw CocoaError(.coderInvalidValue)
    }
    let started = ProcessInfo.processInfo.systemUptime
    var people: [[CGRect]] = []
    var dimensions: [CGSize] = []
    var detectionFailed = false
    // Sequential bounded thumbnails; do not retain six full-resolution camera buffers.
    for frame in frames {
      try checkProtection()
      let result = try autoreleasepool { () -> ([CGRect], CGSize) in
        let image = try thumbnail(frame.path, maximum: 768)
        let request = VNDetectHumanRectanglesRequest()
        request.upperBodyOnly = false
        do {
          try VNImageRequestHandler(cgImage: image, orientation: .up).perform([request])
        } catch {
          detectionFailed = true
        }
        let boxes = (request.results ?? []).filter { $0.confidence >= 0.5 }.map { observation in
          let box = observation.boundingBox
          return CGRect(x: box.minX, y: 1 - box.maxY, width: box.width, height: box.height)
        }
        return (boxes, CGSize(width: image.width, height: image.height))
      }
      people.append(result.0)
      dimensions.append(result.1)
    }
    var decision = AppleAiFrameSelection.crop(people: people)
    if detectionFailed || Set(dimensions.map { "\($0.width)x\($0.height)" }).count != 1 {
      decision = AppleAiCropDecision(rect: AppleAiFrameSelection.full, subject: AppleAiFrameSelection.full,
                                    reason: detectionFailed ? "detection_failed" : "orientation_changed")
    }
    var descriptors: [[Double]] = []
    if decision.reliableSubject {
      for frame in frames {
        try checkProtection()
        descriptors.append(try autoreleasepool {
          let image = try thumbnail(frame.path, maximum: 768)
          // Use a fixed union of person bounds, not the background-heavy context margin.
          return try descriptor(cropped(image, to: decision.subject))
        })
      }
    }
    // With no reliable single subject, do not mistake background/passersby for its motion.
    let selection = decision.reliableSubject ? AppleAiFrameSelection.select(descriptors)
      : (indices: [0, 2, 5], scores: [Double](), changed: false)
    var outputs: [AppleAiPreparedFrame] = []
    var created: [URL] = []
    do {
      for index in selection.indices {
        try checkProtection()
        let output = try autoreleasepool { () -> AppleAiPreparedFrame in
          // Crop the same EXIF-corrected 768px source for all frames. Never upscale a crop.
          let source = try thumbnail(frames[index].path, maximum: 768)
          let image = try cropped(source, to: decision.rect)
          let url = FileManager.default.temporaryDirectory.appendingPathComponent("apple-input-\(UUID().uuidString).jpg")
          created.append(url)
          guard let destination = CGImageDestinationCreateWithURL(url as CFURL, UTType.jpeg.identifier as CFString, 1, nil) else {
            throw CocoaError(.fileWriteUnknown)
          }
          CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: 0.9] as CFDictionary)
          guard CGImageDestinationFinalize(destination) else { throw CocoaError(.fileWriteUnknown) }
          return AppleAiPreparedFrame(path: url.path, capturedAt: frames[index].capturedAt,
                                      sourceIndex: index, width: image.width, height: image.height)
        }
        outputs.append(output)
      }
      try checkProtection()
      return AppleAiPreparation(frames: outputs, crop: decision.rect, reason: decision.reason,
                                changed: selection.changed, personCounts: people.map(\.count), scores: selection.scores,
                                elapsedMs: (ProcessInfo.processInfo.systemUptime - started) * 1000)
    } catch {
      for url in created { try? FileManager.default.removeItem(at: url) }
      throw error
    }
  }

  private static func checkProtection() throws {
    try Task.checkCancellation()
    if ProcessInfo.processInfo.thermalState.rawValue >= 2 { throw CancellationError() }
  }

  static func thumbnail(_ path: String, maximum: Int) throws -> CGImage {
    let url = path.hasPrefix("file://") ? URL(string: path) : URL(fileURLWithPath: path)
    guard let url, url.isFileURL,
          let source = CGImageSourceCreateWithURL(url as CFURL, nil),
          let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maximum,
            kCGImageSourceShouldCacheImmediately: true
          ] as CFDictionary) else { throw CocoaError(.fileReadCorruptFile) }
    return image
  }

  private static func cropped(_ image: CGImage, to rect: CGRect) throws -> CGImage {
    let bounds = CGRect(x: 0, y: 0, width: image.width, height: image.height)
    let pixels = CGRect(x: rect.minX * bounds.width, y: rect.minY * bounds.height,
                        width: rect.width * bounds.width, height: rect.height * bounds.height).integral.intersection(bounds)
    guard let result = image.cropping(to: pixels) else { throw CocoaError(.fileReadCorruptFile) }
    return result
  }

  private static func descriptor(_ image: CGImage) throws -> [Double] {
    let side = 32
    var bytes = [UInt8](repeating: 0, count: side * side)
    let success = bytes.withUnsafeMutableBytes { buffer -> Bool in
      guard let context = CGContext(data: buffer.baseAddress, width: side, height: side, bitsPerComponent: 8,
                                    bytesPerRow: side, space: CGColorSpaceCreateDeviceGray(), bitmapInfo: 0) else { return false }
      context.interpolationQuality = .low
      context.draw(image, in: CGRect(x: 0, y: 0, width: side, height: side))
      return true
    }
    guard success else { throw CocoaError(.coderInvalidValue) }
    return bytes.map { Double($0) / 255 }
  }
}
