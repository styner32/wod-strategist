package expo.modules.videomerger

import expo.modules.kotlin.Promise
import expo.modules.kotlin.exception.CodedException
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition

class VideoMergerModule : Module() {
  override fun definition() = ModuleDefinition {
    Name("VideoMerger")
    AsyncFunction("mergeVideos") { inputPaths: List<String>, outputPath: String, promise: Promise ->
      Thread {
        try {
          promise.resolve(PassthroughVideoMerger.merge(inputPaths, outputPath))
        } catch (e: Exception) {
          promise.reject(CodedException("VIDEO_MERGE_ERROR", e.message ?: "Unknown error", e))
        }
      }.start()
    }
  }
}
