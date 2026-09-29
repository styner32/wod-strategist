// Shared deterministic media fixtures for the native recording checks.
import AVFoundation
import CryptoKit
import Foundation

func check(_ value: Bool, _ message: String) throws {
  if !value { throw NSError(domain: "VideoSmoke", code: 1, userInfo: [NSLocalizedDescriptionKey: message]) }
}

func videoSample(at timestamp: CMTime, index: Int, fps: Int32 = 30, invalidDuration: Bool = false) throws -> CMSampleBuffer {
  var pixel: CVPixelBuffer?
  let status = CVPixelBufferCreate(kCFAllocatorDefault, 320, 180, kCVPixelFormatType_32BGRA,
    [kCVPixelBufferIOSurfacePropertiesKey: [:]] as CFDictionary, &pixel)
  try check(status == kCVReturnSuccess && pixel != nil, "Cannot allocate test frame")
  let buffer = pixel!
  CVPixelBufferLockBaseAddress(buffer, [])
  memset(CVPixelBufferGetBaseAddress(buffer)!, Int32(index % 255), CVPixelBufferGetDataSize(buffer))
  CVPixelBufferUnlockBaseAddress(buffer, [])
  var format: CMVideoFormatDescription?
  CMVideoFormatDescriptionCreateForImageBuffer(allocator: kCFAllocatorDefault, imageBuffer: buffer, formatDescriptionOut: &format)
  var timing = CMSampleTimingInfo(duration: invalidDuration ? .invalid : CMTime(value: 1, timescale: fps), presentationTimeStamp: timestamp, decodeTimeStamp: .invalid)
  var sample: CMSampleBuffer?
  CMSampleBufferCreateReadyWithImageBuffer(allocator: kCFAllocatorDefault, imageBuffer: buffer, formatDescription: format!, sampleTiming: &timing, sampleBufferOut: &sample)
  try check(sample != nil, "Cannot create video sample")
  return sample!
}

func audioFormat() throws -> CMAudioFormatDescription {
  var asbd = AudioStreamBasicDescription(mSampleRate: 48000, mFormatID: kAudioFormatLinearPCM,
    mFormatFlags: kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked, mBytesPerPacket: 2,
    mFramesPerPacket: 1, mBytesPerFrame: 2, mChannelsPerFrame: 1, mBitsPerChannel: 16, mReserved: 0)
  var format: CMAudioFormatDescription?
  let status = CMAudioFormatDescriptionCreate(allocator: kCFAllocatorDefault, asbd: &asbd,
    layoutSize: 0, layout: nil, magicCookieSize: 0, magicCookie: nil, extensions: nil, formatDescriptionOut: &format)
  try check(status == noErr && format != nil, "Cannot create audio format")
  return format!
}

func audioSample(at timestamp: CMTime, format: CMAudioFormatDescription, sampleCount: Int = 1600, startSample: Int = 0, toneBaseHz: Double = 220) throws -> CMSampleBuffer {
  var block: CMBlockBuffer?
  CMBlockBufferCreateWithMemoryBlock(allocator: kCFAllocatorDefault, memoryBlock: nil, blockLength: sampleCount * 2,
    blockAllocator: kCFAllocatorDefault, customBlockSource: nil, offsetToData: 0, dataLength: sampleCount * 2,
    flags: 0, blockBufferOut: &block)
  // Phase uses the absolute PCM sample index, so each block continues the same chirp.
  // f(t) = base + 30*t Hz; integrating frequency gives base*t + 15*t*t cycles.
  var pcm = [Int16](repeating: 0, count: sampleCount)
  for index in pcm.indices {
    let time = Double(startSample + index) / 48000
    pcm[index] = Int16((12000 * sin(2 * Double.pi * (toneBaseHz * time + 15 * time * time))).rounded())
  }
  let copyStatus = pcm.withUnsafeBytes {
    CMBlockBufferReplaceDataBytes(with: $0.baseAddress!, blockBuffer: block!, offsetIntoDestination: 0, dataLength: $0.count)
  }
  try check(copyStatus == noErr, "Cannot populate chirp PCM samples")
  var timing = CMSampleTimingInfo(duration: CMTime(value: 1, timescale: 48000), presentationTimeStamp: timestamp, decodeTimeStamp: .invalid)
  var size = 2
  var sample: CMSampleBuffer?
  let status = CMSampleBufferCreateReady(allocator: kCFAllocatorDefault, dataBuffer: block,
    formatDescription: format, sampleCount: sampleCount, sampleTimingEntryCount: 1, sampleTimingArray: &timing,
    sampleSizeEntryCount: 1, sampleSizeArray: &size, sampleBufferOut: &sample)
  try check(status == noErr && sample != nil, "Cannot create audio sample")
  return sample!
}

func countSamples(_ url: URL, _ media: AVMediaType) throws -> Int {
  let asset = AVURLAsset(url: url, options: [AVURLAssetPreferPreciseDurationAndTimingKey: true])
  guard let track = asset.tracks(withMediaType: media).first else { return 0 }
  let reader = try AVAssetReader(asset: asset)
  let output = AVAssetReaderTrackOutput(track: track, outputSettings: nil)
  reader.add(output)
  try check(reader.startReading(), "Cannot read test media")
  var count = 0
  while let sample = output.copyNextSampleBuffer() { count += CMSampleBufferGetNumSamples(sample) }
  try check(reader.status == .completed, "Media read failed")
  return count
}

func videoPayloadHashes(_ url: URL) throws -> [SHA256.Digest] {
  let asset = AVURLAsset(url: url)
  guard let track = asset.tracks(withMediaType: .video).first else { return [] }
  let reader = try AVAssetReader(asset: asset)
  let output = AVAssetReaderTrackOutput(track: track, outputSettings: nil)
  reader.add(output)
  try check(reader.startReading(), "Cannot read compressed video packets")
  var hashes: [SHA256.Digest] = []
  while let sample = output.copyNextSampleBuffer() {
    guard CMSampleBufferGetNumSamples(sample) > 0, let block = CMSampleBufferGetDataBuffer(sample) else { continue }
    var bytes = Data(count: CMBlockBufferGetDataLength(block))
    let status = bytes.withUnsafeMutableBytes {
      CMBlockBufferCopyDataBytes(block, atOffset: 0, dataLength: $0.count, destination: $0.baseAddress!)
    }
    try check(status == noErr, "Cannot inspect compressed video packet")
    hashes.append(SHA256.hash(data: bytes))
  }
  try check(reader.status == .completed, "Compressed video packet read failed")
  return hashes
}

