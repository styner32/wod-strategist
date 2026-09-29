import ExpoModulesCore
import Foundation

private final class VideoMergeException: GenericException<String> {
  override var code: String { "VIDEO_MERGE_ERROR" }
  override var reason: String { param }
}

public class VideoMergerModule: Module {
  public func definition() -> ModuleDefinition {
    Name("VideoMerger")
    AsyncFunction("mergeVideos") { (inputPaths: [String], outputPath: String, promise: Promise) in
      DispatchQueue.global(qos: .userInitiated).async {
        do {
          promise.resolve(try PassthroughVideoMerger.merge(inputPaths: inputPaths, outputPath: outputPath))
        } catch {
          // Expo's debug bridge reads `reason`, not the initializer's description.
          promise.reject(VideoMergeException(error.localizedDescription))
        }
      }
    }
  }
}
