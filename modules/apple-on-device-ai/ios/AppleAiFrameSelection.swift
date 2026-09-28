import Foundation

// All rectangles use EXIF-corrected image coordinates, normalized, TOP-left origin.
// Kept independent of Vision so geometry and selection can be tested deterministically.
struct AppleAiCropDecision {
  let rect: CGRect
  let subject: CGRect
  let reason: String?
  var reliableSubject: Bool { reason == nil || reason == "context_needs_full_frame" }
}

enum AppleAiFrameSelection {
  static let full = CGRect(x: 0, y: 0, width: 1, height: 1)

  static func crop(people: [[CGRect]]) -> AppleAiCropDecision {
    func fallback(_ reason: String) -> AppleAiCropDecision {
      AppleAiCropDecision(rect: full, subject: full, reason: reason)
    }
    guard !people.isEmpty else { return fallback("no_person") }
    if people.contains(where: { $0.count > 1 }) { return fallback("multiple_people") }
    if people.contains(where: { $0.isEmpty }) { return fallback("person_missing") }
    let boxes = people.map { $0[0] }
    for box in boxes {
      guard [box.minX, box.minY, box.width, box.height].allSatisfy({ $0.isFinite }),
            box.width >= 0.04, box.height >= 0.12, full.contains(box) else {
        return fallback("unreliable_bounds")
      }
      // Edge-touching detections may be partial bodies: do not crop them further.
      if box.minX < 0.015 || box.minY < 0.015 || box.maxX > 0.985 || box.maxY > 0.985 {
        return fallback("subject_at_edge")
      }
    }
    for (a, b) in zip(boxes, boxes.dropFirst()) {
      let intersection = a.intersection(b)
      let overlap = intersection.isNull ? 0 : intersection.width * intersection.height
      if overlap / min(a.width * a.height, b.width * b.height) < 0.2 {
        return fallback("unstable_subject")
      }
    }
    let union = boxes.reduce(CGRect.null) { $0.union($1) }
    // Context margin for bars, plates, hands and feet. This is NOT an equipment detector.
    let expanded = CGRect(x: union.minX - union.width * 0.65,
                          y: union.minY - union.height * 0.4,
                          width: union.width * 2.3, height: union.height * 1.65).intersection(full)
    if expanded.width * expanded.height > 0.9 {
      return AppleAiCropDecision(rect: full, subject: union, reason: "context_needs_full_frame")
    }
    return AppleAiCropDecision(rect: expanded, subject: union, reason: nil)
  }

  // Brightness-centered thumbnail difference, not a movement classifier or pose score.
  static func distance(_ a: [Double], _ b: [Double]) -> Double {
    guard !a.isEmpty, a.count == b.count else { return 0 }
    let meanA = a.reduce(0, +) / Double(a.count)
    let meanB = b.reduce(0, +) / Double(b.count)
    return zip(a, b).reduce(0) { $0 + abs(($1.0 - meanA) - ($1.1 - meanB)) } / Double(a.count)
  }

  static func select(_ descriptors: [[Double]]) -> (indices: [Int], scores: [Double], changed: Bool) {
    guard descriptors.count >= 3 else { return (Array(descriptors.indices), [], false) }
    let last = descriptors.count - 1
    let scores = descriptors.map { min(distance($0, descriptors[0]), distance($0, descriptors[last])) }
    let middle = last / 2
    let best = (1..<last).max {
      if abs(scores[$0] - scores[$1]) < 0.0001 { return abs($0 - middle) > abs($1 - middle) }
      return scores[$0] < scores[$1]
    } ?? middle
    // Keep both temporal boundaries; use uniform coverage for static/noisy near-duplicates.
    let changed = scores[best] >= 0.015
    return ([0, changed ? best : middle, last], scores, changed)
  }
}
