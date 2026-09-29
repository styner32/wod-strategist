// Compile with actual RecordingSession, Track, and TrackTimeline sources.
// Only unrelated UI/orientation/logger/metadata glue is replaced for this macOS test.
import AVFoundation
import Foundation
struct MetadataProvider { func createVideoMetadata() -> [AVMetadataItem] { [] } }
enum Orientation { case portrait; var affineTransform: CGAffineTransform { .identity } }
enum CameraError: Error { case capture(Capture); enum Capture { case createRecorderError(message: String); case unknown(message: String) } }
enum VisionLogger { enum Level { case info, warning, error, debug }; static func log(level: Level, message: String) {} }
enum CameraQueues { static let cameraQueue = DispatchQueue(label: "legacy.test.camera") }

@main struct LegacyStopSmoke {
 static func main() throws {
  for frames in [0,10] {
   let url=URL(fileURLWithPath:"/private/tmp/wod-legacy-detach-\(UUID()).mov")
   let done=DispatchSemaphore(value:0)
   let lock=NSLock(); var calls=0
   let session=try RecordingSession(url:url,fileType:.mov,metadataProvider:MetadataProvider(),clock:CMClockGetHostTimeClock(),orientation:.portrait) { _,_,_ in lock.lock();calls += 1;lock.unlock();done.signal() }
   try session.initializeVideoTrack(withSettings:[AVVideoCodecKey:AVVideoCodecType.h264,AVVideoWidthKey:320,AVVideoHeightKey:180])
   try session.start()
   for index in 0..<frames {
    let sample=try videoSample(at:CMClockGetTime(CMClockGetHostTimeClock()),index:index)
    try session.append(buffer:sample,ofType:.video)
    Thread.sleep(forTimeInterval:0.036)
   }
   session.stop();session.stop()
   precondition(done.wait(timeout:.now()+5) == .success)
   Thread.sleep(forTimeInterval:0.25)
   precondition(calls==1,"Duplicate final callback after JS+native detach stop")
   if frames>0 { let count=try countSamples(url,.video); precondition(count>0,"Legacy source did not finalize") }
   print("PASS: legacy double stop, receivedFrames=\(frames), completionCount=\(calls), path=\(url.path)")
  }
 }
}
