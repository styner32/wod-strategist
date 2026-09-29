import AVFoundation
import Foundation

/// AVAssetExportSession uses a 600-tick movie clock, which rounds AAC tails and
/// gaps between runs. Repair movie/edit-list timing on the unpublished artifact.
/// Compressed samples, their file offsets and codec atoms stay untouched.
enum PassthroughMovieTimeline {
  private struct Atom {
    let type: String
    let start: Int
    let body: Int
    let end: Int
  }
  private struct Edit {
    let duration: CMTime
    let isEmpty: Bool
  }

  static func preserve(_ composition: AVComposition, in url: URL) throws {
    var timelines: [String: [Edit]] = [:]
    var scale: Int64 = 1
    for track in composition.tracks {
      let edits = try sourceEdits(track)
      timelines[track.mediaType.rawValue] = edits
      for edit in edits {
        for time in [edit.duration] {
          guard time.isNumeric, time.timescale > 0, time.value >= 0 else { throw failure("Invalid composition clock") }
          // Nanosecond capture clocks often describe simple frame boundaries.
          // Use their reduced denominator instead of the raw clock frequency.
          let clock = Int64(time.timescale) / gcd(time.value, Int64(time.timescale))
          scale = scale / gcd(scale, clock) * clock
          guard scale <= Int32.max else { throw failure("Cannot preserve exact movie clock") }
        }
      }
    }
    let handle = try FileHandle(forUpdating: url)
    defer { try? handle.close() }
    let fileSize = try handle.seekToEnd()
    var offset: UInt64 = 0
    var movieOffset: UInt64?
    var bytes = Data()
    while offset + 8 <= fileSize {
      try handle.seek(toOffset: offset)
      guard let header = try handle.read(upToCount: 16), header.count >= 8 else { throw failure("Unreadable movie atom") }
      var size = try uint(header, 0, 4)
      if size == 1 { size = try uint(header, 8, 8) }
      if size == 0 { size = fileSize - offset }
      guard size >= 8, size <= fileSize - offset else { throw failure("Invalid movie atom size") }
      if String(data: header[4..<8], encoding: .ascii) == "moov" {
        guard size <= 64 * 1024 * 1024 else { throw failure("Movie timing metadata exceeds verification limit") }
        try handle.seek(toOffset: offset)
        guard let data = try handle.read(upToCount: Int(size)), data.count == Int(size) else { throw failure("Incomplete movie metadata") }
        movieOffset = offset
        bytes = data
        break
      }
      offset += size
    }
    guard let movieOffset, let movie = try children(bytes, from: 0, to: bytes.count).first,
          let header = try children(bytes, from: movie.body, to: movie.end).first(where: { $0.type == "mvhd" }) else {
      throw failure("Missing movie timing metadata")
    }
    guard header.body < header.end else { throw failure("Truncated movie header") }
    let movieVersion = bytes[header.body]
    guard movieVersion <= 1, header.end - header.body >= (movieVersion == 1 ? 32 : 20) else {
      throw failure("Unsupported or truncated movie header")
    }
    let scaleOffset = header.body + (movieVersion == 1 ? 20 : 12)
    let oldScale = try uint(bytes, scaleOffset, 4)
    guard oldScale > 0 else { throw failure("Invalid exported movie clock") }
    if oldScale == scale { return }
    try put(&bytes, scaleOffset, 4, UInt64(scale))
    try put(&bytes, scaleOffset + 4, movieVersion == 1 ? 8 : 4, try ticks(composition.duration, scale))
    var replacements: [Int: Data] = [:]

    for trackAtom in try children(bytes, from: movie.body, to: movie.end) where trackAtom.type == "trak" {
      let trackChildren = try children(bytes, from: trackAtom.body, to: trackAtom.end)
      guard let trackHeader = trackChildren.first(where: { $0.type == "tkhd" }),
            let media = trackChildren.first(where: { $0.type == "mdia" }),
            let handler = try children(bytes, from: media.body, to: media.end).first(where: { $0.type == "hdlr" }),
            handler.body + 12 <= handler.end,
            let type = String(data: bytes[(handler.body + 8)..<(handler.body + 12)], encoding: .ascii),
            let track = composition.tracks.first(where: { $0.mediaType.rawValue == type }) else {
        throw failure("Cannot match exported track to source timeline")
      }
      guard trackHeader.body < trackHeader.end else { throw failure("Truncated track header") }
      let trackVersion = bytes[trackHeader.body]
      guard trackVersion <= 1, trackHeader.end - trackHeader.body >= (trackVersion == 1 ? 36 : 24) else {
        throw failure("Unsupported or truncated track header")
      }
      try put(&bytes, trackHeader.body + (trackVersion == 1 ? 28 : 20), trackVersion == 1 ? 8 : 4,
              try ticks(CMTimeRangeGetEnd(track.timeRange), scale))
      guard let segments = timelines[type] else { throw failure("Missing exact source edits") }
      guard let edits = trackChildren.first(where: { $0.type == "edts" }),
            let list = try children(bytes, from: edits.body, to: edits.end).first(where: { $0.type == "elst" }) else {
        // No edit list is valid only for a single contiguous media span.
        guard !segments.contains(where: { $0.isEmpty }) else { throw failure("Missing gap timing metadata") }
        continue
      }
      guard list.end - list.body >= 8 else { throw failure("Truncated edit list") }
      let version = bytes[list.body]
      guard version <= 1 else { throw failure("Unsupported edit list") }
      let width = version == 1 ? 8 : 4
      let stride = width * 2 + 4
      let count = Int(try uint(bytes, list.body + 4, 4))
      guard count > 0, count <= (list.end - list.body - 8) / stride else { throw failure("Invalid edit count") }
      var segmentIndex = 0
      var entries = Data()
      let emptyMediaTime = width == 8 ? UInt64.max : UInt64(UInt32.max)
      func appendEdit(_ duration: CMTime, mediaTime: UInt64) throws {
        var entry = Data(count: stride)
        try put(&entry, 0, width, try ticks(duration, scale))
        try put(&entry, width, width, mediaTime)
        try put(&entry, width * 2, 4, 65536)
        entries.append(entry)
      }
      for entry in 0..<count {
        let position = list.body + 8 + entry * stride
        let oldDuration = try uint(bytes, position, width)
        let mediaTime = try uint(bytes, position + width, width)
        let empty = mediaTime == (width == 8 ? UInt64.max : UInt64(UInt32.max))
        guard try uint(bytes, position + width * 2, 4) == 65536 else { throw failure("Unsupported edit playback rate") }
        // The exporter omits empty edits that round to zero movie ticks.
        // Restore only verified sub-tick gaps; never invent or omit media.
        while !empty, segmentIndex < segments.count, segments[segmentIndex].isEmpty,
              segments[segmentIndex].duration.seconds < 1.0 / Double(oldScale) {
          try appendEdit(segments[segmentIndex].duration, mediaTime: emptyMediaTime)
          segmentIndex += 1
        }
        var duration = CMTime.zero
        var matched = false
        // Export may coalesce contiguous source segments. Match only the same
        // gap/media kind and an unambiguous duration within one old movie tick.
        while segmentIndex < segments.count, segments[segmentIndex].isEmpty == empty {
          duration = duration + segments[segmentIndex].duration
          segmentIndex += 1
          if abs(duration.seconds - Double(oldDuration) / Double(oldScale)) <= 1.0 / Double(oldScale) + 0.000000001 {
            matched = true
            break
          }
          if duration.seconds > Double(oldDuration + 1) / Double(oldScale) { break }
        }
        guard matched else { throw failure("Exported edits do not match the complete source timeline") }
        try appendEdit(duration, mediaTime: mediaTime)
      }
      while segmentIndex < segments.count, segments[segmentIndex].isEmpty,
            segments[segmentIndex].duration.seconds < 1.0 / Double(oldScale) {
        try appendEdit(segments[segmentIndex].duration, mediaTime: emptyMediaTime)
        segmentIndex += 1
      }
      guard segmentIndex == segments.count else { throw failure("Export omitted source timeline edits") }
      var body = Data(bytes[list.body..<(list.body + 8)])
      try put(&body, 4, 4, UInt64(entries.count / stride))
      body.append(entries)
      replacements[list.start] = try atom("elst", body: body)
    }
    let corrected = try rebuilding(movie, in: bytes, replacements: replacements)
    if corrected.count == bytes.count {
      try handle.seek(toOffset: movieOffset)
      try handle.write(contentsOf: corrected)
    } else {
      // Relocate only metadata to EOF, leaving every stco/co64 media offset
      // valid even when moov precedes mdat. The old metadata becomes free space.
      try handle.seekToEnd()
      try handle.write(contentsOf: corrected)
      try handle.synchronize()
      try handle.seek(toOffset: movieOffset + 4)
      try handle.write(contentsOf: Data("free".utf8))
    }
    try handle.synchronize()
  }

  private static func atom(_ type: String, body: Data) throws -> Data {
    guard body.count <= Int(UInt32.max) - 8 else { throw failure("Movie metadata size overflow") }
    var result = Data(count: 4)
    try put(&result, 0, 4, UInt64(body.count + 8))
    result.append(Data(type.utf8))
    result.append(body)
    return result
  }

  private static func rebuilding(_ node: Atom, in data: Data, replacements: [Int: Data]) throws -> Data {
    if let replacement = replacements[node.start] { return replacement }
    guard ["moov", "trak", "edts"].contains(node.type) else { return Data(data[node.start..<node.end]) }
    var body = Data()
    for child in try children(data, from: node.body, to: node.end) {
      body.append(try rebuilding(child, in: data, replacements: replacements))
    }
    return try atom(node.type, body: body)
  }

  private static func sourceEdits(_ track: AVAssetTrack) throws -> [Edit] {
    var result: [Edit] = []
    for segment in track.segments {
      let mapping = segment.timeMapping
      if segment.isEmpty {
        result.append(Edit(duration: mapping.target.duration, isEmpty: true))
        continue
      }
      guard CMTimeCompare(mapping.source.duration, mapping.target.duration) == 0,
            let compositionSegment = segment as? AVCompositionTrackSegment,
            let url = compositionSegment.sourceURL,
            let source = AVURLAsset(url: url).track(withTrackID: compositionSegment.sourceTrackID) else {
        throw failure("Cannot recover exact source edit timing")
      }
      // A source track timeRange includes its internal edit-list gaps. Flatten
      // them before matching the export's edits (for example delayed audio).
      var covered = CMTime.zero
      for sourceSegment in source.segments {
        let intersection = CMTimeRangeGetIntersection(mapping.source, otherRange: sourceSegment.timeMapping.target)
        if intersection.isValid, intersection.duration > .zero {
          result.append(Edit(duration: intersection.duration, isEmpty: sourceSegment.isEmpty))
          covered = covered + intersection.duration
        }
      }
      guard CMTimeCompare(covered, mapping.target.duration) == 0 else { throw failure("Incomplete source edit coverage") }
    }
    return result
  }

  private static func children(_ data: Data, from start: Int, to end: Int) throws -> [Atom] {
    var result: [Atom] = []
    var position = start
    while position < end {
      guard end - position >= 8 else { throw failure("Truncated movie atom") }
      var size = try uint(data, position, 4)
      var header = 8
      if size == 1 { size = try uint(data, position + 8, 8); header = 16 }
      if size == 0 { size = UInt64(end - position) }
      guard size >= header, size <= end - position,
            let type = String(data: data[(position + 4)..<(position + 8)], encoding: .ascii) else { throw failure("Invalid movie atom") }
      result.append(Atom(type: type, start: position, body: position + header, end: position + Int(size)))
      position += Int(size)
    }
    return result
  }

  private static func ticks(_ time: CMTime, _ scale: Int64) throws -> UInt64 {
    let value = CMTimeConvertScale(time, timescale: CMTimeScale(scale), method: .roundHalfAwayFromZero)
    guard value.isNumeric, value.value >= 0, CMTimeCompare(value, time) == 0 else { throw failure("Inexact movie timing conversion") }
    return UInt64(value.value)
  }

  private static func gcd(_ first: Int64, _ second: Int64) -> Int64 {
    var a = first, b = second
    while b != 0 { let remainder = a % b; a = b; b = remainder }
    return a
  }

  private static func uint(_ data: Data, _ offset: Int, _ width: Int) throws -> UInt64 {
    guard offset >= 0, width > 0, width <= 8, offset <= data.count - width else { throw failure("Truncated timing field") }
    var value: UInt64 = 0
    for byte in data[offset..<(offset + width)] { value = value << 8 | UInt64(byte) }
    return value
  }

  private static func put(_ data: inout Data, _ offset: Int, _ width: Int, _ value: UInt64) throws {
    guard offset >= 0, offset <= data.count - width, width == 8 || value <= UInt32.max else { throw failure("Movie timing field overflow") }
    for i in 0..<width { data[offset + i] = UInt8(truncatingIfNeeded: value >> (8 * (width - i - 1))) }
  }

  private static func failure(_ message: String) -> NSError {
    NSError(domain: "VideoMerger", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
  }
}
