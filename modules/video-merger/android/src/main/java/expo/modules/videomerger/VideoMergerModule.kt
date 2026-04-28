package expo.modules.videomerger

import android.media.MediaCodec
import android.media.MediaExtractor
import android.media.MediaFormat
import android.media.MediaMuxer
import expo.modules.kotlin.Promise
import expo.modules.kotlin.exception.CodedException
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.io.File
import java.nio.ByteBuffer

class VideoMergerModule : Module() {
  override fun definition() = ModuleDefinition {
    Name("VideoMerger")

    AsyncFunction("mergeVideos") { inputPaths: List<String>, outputPath: String, promise: Promise ->
      Thread {
        try {
          mergeSync(inputPaths, outputPath)
          promise.resolve(
            mapOf(
              "success" to true,
              "outputPath" to outputPath
            )
          )
        } catch (e: Exception) {
          promise.reject(CodedException("VIDEO_MERGE_ERROR", e.message ?: "Unknown error", e))
        }
      }.start()
    }
  }

  /**
   * Merge multiple MP4 chunks into a single MP4 using MediaExtractor + MediaMuxer.
   * This is a stream-copy (no re-encode) — fast and lossless.
   */
  private fun mergeSync(inputPaths: List<String>, outputPath: String) {
    require(inputPaths.isNotEmpty()) { "No input files provided" }

    // Validate all files exist
    inputPaths.forEachIndexed { index, path ->
      require(File(path).exists()) { "File not found at index $index: $path" }
    }

    // Delete existing output file
    val outputFile = File(outputPath)
    if (outputFile.exists()) {
      outputFile.delete()
    }

    val muxer = MediaMuxer(outputPath, MediaMuxer.OutputFormat.MUXER_OUTPUT_MPEG_4)
    var muxerStarted = false
    var videoTrackIndex = -1
    var audioTrackIndex = -1

    try {
      // Use the first chunk to determine track formats
      val firstExtractor = MediaExtractor()
      firstExtractor.setDataSource(cleanPath(inputPaths[0]))

      var videoFormat: MediaFormat? = null
      var audioFormat: MediaFormat? = null

      for (i in 0 until firstExtractor.trackCount) {
        val format = firstExtractor.getTrackFormat(i)
        val mime = format.getString(MediaFormat.KEY_MIME) ?: continue

        when {
          mime.startsWith("video/") && videoFormat == null -> {
            videoFormat = format
          }
          mime.startsWith("audio/") && audioFormat == null -> {
            audioFormat = format
          }
        }
      }
      firstExtractor.release()

      requireNotNull(videoFormat) { "First chunk has no video track" }

      // Add tracks to muxer
      videoTrackIndex = muxer.addTrack(videoFormat)
      if (audioFormat != null) {
        audioTrackIndex = muxer.addTrack(audioFormat)
      }

      muxer.start()
      muxerStarted = true

      // Buffer for sample data (1MB should be sufficient for any frame)
      val bufferSize = 1024 * 1024
      val buffer = ByteBuffer.allocate(bufferSize)
      val bufferInfo = MediaCodec.BufferInfo()

      var timeOffsetUs: Long = 0

      for (path in inputPaths) {
        val extractor = MediaExtractor()
        extractor.setDataSource(cleanPath(path))

        // Map extractor track indices to muxer track indices
        var extractorVideoTrack = -1
        var extractorAudioTrack = -1
        var maxPtsUs: Long = 0

        for (i in 0 until extractor.trackCount) {
          val format = extractor.getTrackFormat(i)
          val mime = format.getString(MediaFormat.KEY_MIME) ?: continue

          when {
            mime.startsWith("video/") && extractorVideoTrack == -1 -> {
              extractorVideoTrack = i
              extractor.selectTrack(i)
            }
            mime.startsWith("audio/") && extractorAudioTrack == -1 && audioTrackIndex >= 0 -> {
              extractorAudioTrack = i
              extractor.selectTrack(i)
            }
          }
        }

        // Read all samples from this chunk
        while (true) {
          buffer.clear()
          val sampleSize = extractor.readSampleData(buffer, 0)
          if (sampleSize < 0) break

          val trackIndex = extractor.sampleTrackIndex
          val muxerTrack = when (trackIndex) {
            extractorVideoTrack -> videoTrackIndex
            extractorAudioTrack -> audioTrackIndex
            else -> {
              extractor.advance()
              continue
            }
          }

          val presentationTimeUs = extractor.sampleTime + timeOffsetUs
          maxPtsUs = maxOf(maxPtsUs, presentationTimeUs)

          bufferInfo.offset = 0
          bufferInfo.size = sampleSize
          bufferInfo.presentationTimeUs = presentationTimeUs
          bufferInfo.flags = extractor.sampleFlags

          muxer.writeSampleData(muxerTrack, buffer, bufferInfo)
          extractor.advance()
        }

        // Offset next chunk's timestamps after this chunk's last PTS
        // Add a small gap (1ms) to avoid PTS collision
        timeOffsetUs = maxPtsUs + 1000

        extractor.release()
      }
    } finally {
      if (muxerStarted) {
        muxer.stop()
      }
      muxer.release()
    }
  }

  /**
   * Strip file:// prefix if present.
   */
  private fun cleanPath(path: String): String {
    return if (path.startsWith("file://")) {
      path.removePrefix("file://")
    } else {
      path
    }
  }
}
