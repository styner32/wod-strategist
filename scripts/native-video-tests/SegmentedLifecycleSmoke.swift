import Foundation

@main
struct LifecycleSmoke {
  static func main() {
    let queue = DispatchQueue(label: "wod.lifecycle.test")
    let sourceReady = DispatchSemaphore(value: 0)
    let done = DispatchSemaphore(value: 0)
    var activeRun = "old"
    var audioStops = 0
    var finalCalls = 0
    let completion = SegmentedRecordingCompletion(queue: queue, releaseActive: { released in
      precondition(activeRun == "old")
      audioStops += 1
      activeRun = ""
      released()
    }, onSource: { _ in
      precondition(activeRun.isEmpty)
      activeRun = "new" // Resume before the old analysis queue drains.
      sourceReady.signal()
    }, onFinish: { _, _ in
      precondition(activeRun == "new", "Old completion cleared the new run")
      precondition(audioStops == 1, "Old completion stopped new audio")
      finalCalls += 1
      done.signal()
    })
    completion.sourceFinalized(["path": "old.mp4"])
    precondition(sourceReady.wait(timeout: .now() + 5) == .success)
    completion.drained(["path": "old.mp4"], error: nil)
    completion.drained(["path": "old.mp4"], error: nil)
    precondition(done.wait(timeout: .now() + 5) == .success)
    queue.sync { precondition(finalCalls == 1) }
    print("PASS: old source release permits a new run before preparation drains; old completion delivered once without clearing new capture/audio")
  }
}
