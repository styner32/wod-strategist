import AVFoundation
import Foundation

@main
struct AudioStartupSmoke {
  static func main() throws {
    let queue = DispatchQueue(label: "wod.audio.startup.test")
    let format = try audioFormat()
    let sample = try audioSample(at: .zero, format: format)
    let done = DispatchSemaphore(value: 0)
    var outcomes = 0
    var startup: SegmentedAudioStartup!
    queue.sync {
      startup = SegmentedAudioStartup(queue: queue) { _, result in
        let audio = try! result.get()
        precondition(audio.settings[AVSampleRateKey] as? Double == 48000)
        precondition(audio.settings[AVNumberOfChannelsKey] as? UInt32 == 1)
        precondition(audio.settings[AVFormatIDKey] as? UInt32 == kAudioFormatMPEG4AAC)
        outcomes += 1
        done.signal()
      }
      precondition(outcomes == 0, "Writer must wait for microphone data")
    }
    startup.receive(sample, recommended: nil)
    startup.receive(sample, recommended: nil)
    try check(done.wait(timeout: .now() + 2) == .success, "Delayed audio did not start writer")
    queue.sync { precondition(outcomes == 1) }

    // The first real sample must determine rate/channels, including stereo 44.1kHz.
    var asbd = AudioStreamBasicDescription(mSampleRate: 44100, mFormatID: kAudioFormatLinearPCM,
      mFormatFlags: kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked,
      mBytesPerPacket: 4, mFramesPerPacket: 1, mBytesPerFrame: 4,
      mChannelsPerFrame: 2, mBitsPerChannel: 16, mReserved: 0)
    var stereo: CMAudioFormatDescription?
    CMAudioFormatDescriptionCreate(allocator: kCFAllocatorDefault, asbd: &asbd,
      layoutSize: 0, layout: nil, magicCookieSize: 0, magicCookie: nil, extensions: nil,
      formatDescriptionOut: &stereo)
    let fallback = try SegmentedAudioConfiguration(format: stereo!, recommended: [:])
    try check(fallback.settings[AVSampleRateKey] as? Double == 44100, "Fallback changed sample rate")
    try check(fallback.settings[AVNumberOfChannelsKey] as? UInt32 == 2, "Fallback downmixed channels")
    let recommended: [String: Any] = [AVFormatIDKey: kAudioFormatMPEG4AAC, AVSampleRateKey: 44100,
      AVNumberOfChannelsKey: 2, AVEncoderBitRateKey: 192000]
    let configured = try SegmentedAudioConfiguration(format: stereo!, recommended: recommended)
    try check(configured.settings[AVEncoderBitRateKey] as? Int == 192000, "Ignored available recommendation")

    var canceled: SegmentedAudioStartup!
    queue.sync {
      canceled = SegmentedAudioStartup(queue: queue) { _, result in
        guard case .failure(let error) = result else { fatalError("Canceled startup created a writer") }
        precondition(error.localizedDescription == "stopped")
        outcomes += 1
      }
      // Audio may already be queued when the user stops. Stop on the camera queue wins.
      canceled.receive(sample, recommended: nil)
      canceled.cancel(NSError(domain: "test", code: 1, userInfo: [NSLocalizedDescriptionKey: "stopped"]))
    }
    canceled.receive(sample, recommended: nil)
    queue.sync { precondition(outcomes == 2) }

    let timedOut = DispatchSemaphore(value: 0)
    var missing: SegmentedAudioStartup!
    queue.sync {
      missing = SegmentedAudioStartup(queue: queue, timeout: 0.02) { _, result in
        guard case .failure(let error) = result else { fatalError("Missing microphone created a writer") }
        precondition(error.localizedDescription.contains("no usable audio buffer"))
        outcomes += 1
        timedOut.signal()
      }
    }
    try check(timedOut.wait(timeout: .now() + 2) == .success, "Missing microphone never timed out")
    missing.receive(sample, recommended: nil)
    queue.sync { precondition(outcomes == 3) }
    // A distinct retry accepts audio after the previous startup timed out.
    let retryDone = DispatchSemaphore(value: 0)
    let retry = SegmentedAudioStartup(queue: queue) { _, result in
      _ = try! result.get(); retryDone.signal()
    }
    retry.receive(sample, recommended: nil)
    try check(retryDone.wait(timeout: .now() + 2) == .success, "Retry stayed blocked")
    print("PASS: real-buffer startup, missing recommendations, mono/stereo rates, stop race, timeout, late callbacks and retry")
  }
}
