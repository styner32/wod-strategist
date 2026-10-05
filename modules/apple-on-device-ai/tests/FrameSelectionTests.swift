import Foundation
import CoreGraphics
import ImageIO
import UniformTypeIdentifiers

@main
struct FrameSelectionTests {
  static func main() throws {
    var count = 0
    func check(_ value: @autoclosure () -> Bool, _ message: String) {
      precondition(value(), message)
      count += 1
    }
    let body = CGRect(x: 0.4, y: 0.3, width: 0.15, height: 0.4)
    let moved = body.offsetBy(dx: 0.04, dy: -0.06)
    let decision = AppleAiFrameSelection.crop(people: [[body], [moved], [body]])
    check(decision.reason == nil, "single consistent person is cropped")
    check(decision.rect.contains(body) && decision.rect.contains(moved), "common crop preserves all person positions")
    check(decision.rect.minY < moved.minY && decision.rect.maxY > body.maxY, "headroom and floor context")
    check(decision.rect.minX < body.minX && decision.rect.maxX > moved.maxX, "horizontal equipment context")
    check(AppleAiFrameSelection.full.contains(decision.rect), "crop stays within image")
    check(AppleAiFrameSelection.crop(people: [[body], []]).reason == "person_missing", "missing detection keeps full image")
    check(AppleAiFrameSelection.crop(people: [[body, moved]]).reason == "multiple_people", "multiple people are not silently selected")
    check(AppleAiFrameSelection.crop(people: [[body], [body.offsetBy(dx: 0.4, dy: 0)]]).reason == "unstable_subject", "identity jumps keep full image")
    check(AppleAiFrameSelection.crop(people: [[CGRect(x: 0, y: 0.1, width: 0.2, height: 0.6)]]).reason == "subject_at_edge", "partial body keeps full image")
    check(AppleAiFrameSelection.crop(people: [[CGRect(x: 0.4, y: 0.4, width: 0.01, height: 0.04)]]).reason == "unreliable_bounds", "tiny detections keep full image")
    check(AppleAiFrameSelection.crop(people: [[CGRect(x: 0.1, y: 0.1, width: 0.8, height: 0.8)]]).reason == "context_needs_full_frame", "large subject preserves full equipment context")
    check(AppleAiFrameSelection.crop(people: [[CGRect(x: 0.1, y: 0.1, width: 0.8, height: 0.8)]]).reliableSubject, "full context can still have a reliable motion subject")

    let rest = [0.1, 0.2, 0.3, 0.4]
    let peak = [0.4, 0.3, 0.2, 0.1]
    let selected = AppleAiFrameSelection.select([rest, rest, rest, rest, peak, rest])
    check(selected.indices == [0, 4, 5], "select the transient change rather than fixed middle")
    check(selected.changed, "changed selection is labeled")
    let stationary = AppleAiFrameSelection.select(Array(repeating: rest, count: 6))
    check(stationary.indices == [0, 2, 5] && !stationary.changed, "duplicates keep uniform temporal coverage")
    let exposureOnly = AppleAiFrameSelection.select((0..<6).map { i in rest.map { $0 + Double(i) * 0.01 } })
    check(!exposureOnly.changed, "uniform exposure changes are not labeled movement")
    check(AppleAiFrameSelection.distance(rest, peak) > 0.1, "spatial changes survive brightness centering")

    // EXIF orientation must be applied before using Vision's image-space crop coordinates.
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }
    let url = dir.appendingPathComponent("rotated.jpg")
    let context = CGContext(data: nil, width: 120, height: 80, bitsPerComponent: 8, bytesPerRow: 0,
                            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
    context.setFillColor(CGColor(red: 0, green: 0, blue: 0, alpha: 1))
    context.fill(CGRect(x: 0, y: 0, width: 120, height: 80))
    let destination = CGImageDestinationCreateWithURL(url as CFURL, UTType.jpeg.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(destination, context.makeImage()!, [kCGImagePropertyOrientation: 6] as CFDictionary)
    check(CGImageDestinationFinalize(destination), "fixture JPEG encoded")
    let corrected = try AppleAiFramePreprocessor.thumbnail(url.path, maximum: 768)
    check(corrected.width == 80 && corrected.height == 120, "EXIF rotation is applied exactly once")

    let frames = (0..<6).map { AppleAiFrame(path: url.path, capturedAt: Double($0 * 800)) }
    let prepared = try AppleAiFramePreprocessor.prepare(frames)
    defer { for frame in prepared.frames { try? FileManager.default.removeItem(atPath: frame.path) } }
    check(prepared.frames.map(\.sourceIndex) == [0, 2, 5], "static images retain temporal anchors")
    check(prepared.reason != nil && prepared.crop == AppleAiFrameSelection.full, "no human: explicit full-frame fallback")
    check(prepared.scores.isEmpty && prepared.dictionary["selectionMethod"] as? String == "uniform_unreliable_subject", "missing subject does not produce background motion scores")
    check(prepared.frames.allSatisfy { $0.width == 80 && $0.height == 120 }, "no upscaling")
    check(prepared.frames.map(\.capturedAt) == [0, 1600, 4000], "original capture timestamps preserved")
    for frame in prepared.frames {
      let decoded = try AppleAiFramePreprocessor.thumbnail(frame.path, maximum: 768)
      check(decoded.width == frame.width && decoded.height == frame.height, "archived model inputs decode without extra rotation")
    }
    do {
      _ = try AppleAiFramePreprocessor.prepare(Array(frames.prefix(3)))
      preconditionFailure("partial candidate batches must not enter selection")
    } catch { count += 1 }
    do {
      _ = try AppleAiFramePreprocessor.prepare(Array(repeating: frames[0], count: 6))
      preconditionFailure("duplicate timestamps must be rejected")
    } catch { count += 1 }
    print("PASS: \(count) native crop/selection/image checks")
  }
}
