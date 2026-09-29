package expo.modules.videomerger

import android.media.MediaCodec
import android.media.MediaExtractor
import android.media.MediaFormat
import android.media.MediaMuxer
import android.net.Uri
import android.os.Build
import android.system.Os
import java.io.File
import java.nio.ByteBuffer
import kotlin.math.abs

/** Copies every source sample; publishes only a validated, atomically replaced MP4. */
internal object PassthroughVideoMerger {
  private data class Track(
    val index: Int,
    val format: MediaFormat,
    val firstUs: Long,
    val endUs: Long,
    val samples: Long,
    val maxSampleSize: Int,
  )
  private data class Source(val file: File, val video: Track, val audio: Track?) {
    val startUs = minOf(video.firstUs, audio?.firstUs ?: video.firstUs)
    val endUs = maxOf(video.endUs, audio?.endUs ?: video.endUs)
    val durationUs = endUs - startUs
  }

  fun merge(inputPaths: List<String>, outputPath: String): Map<String, Any> {
    require(inputPaths.isNotEmpty()) { "No input files provided" }
    val requested = localFile(outputPath)
    val destination = File(requested.parentFile, requested.nameWithoutExtension + ".mp4").canonicalFile
    val sources = inputPaths.map { inspect(localFile(it)) }
    require(sources.none { it.file.canonicalFile == destination }) { "Output would overwrite a source chunk" }
    val videoFormat = sources.first().video.format
    val audioFormat = sources.firstNotNullOfOrNull { it.audio?.format }
    for (source in sources) {
      require((source.audio != null) == (audioFormat != null)) { "Inconsistent audio coverage; originals retained" }
      requireCompatible(videoFormat, source.video.format)
      source.audio?.let { requireCompatible(requireNotNull(audioFormat), it.format) }
    }
    val temp = File.createTempFile(".merge-", ".mp4", destination.parentFile)
    try {
      val durationUs = copySamples(sources, temp, videoFormat, audioFormat)
      val output = inspect(temp)
      require(output.video.samples == sources.sumOf { it.video.samples } &&
        (output.audio?.samples ?: 0) == sources.sumOf { it.audio?.samples ?: 0 }) {
        "Export lost media samples; originals and previous output retained"
      }
      require(abs(output.durationUs - durationUs) <= 100_000) {
        "Export duration does not cover the complete source timeline"
      }
      requireCompatible(videoFormat, output.video.format)
      if (audioFormat != null) requireCompatible(audioFormat, requireNotNull(output.audio).format)
      // POSIX same-directory rename replaces the destination atomically. There
      // is deliberately no pre-delete of either the result or any source.
      Os.rename(temp.absolutePath, destination.absolutePath)
      return mapOf("success" to true, "outputPath" to Uri.fromFile(destination).toString(),
        "inputCount" to sources.size, "durationSeconds" to durationUs / 1_000_000.0)
    } finally {
      temp.delete()
    }
  }

  private fun copySamples(sources: List<Source>, temp: File, videoFormat: MediaFormat, audioFormat: MediaFormat?): Long {
    val muxer = MediaMuxer(temp.absolutePath, MediaMuxer.OutputFormat.MUXER_OUTPUT_MPEG_4)
    var started = false
    try {
      val videoOut = muxer.addTrack(videoFormat)
      val audioOut = audioFormat?.let { muxer.addTrack(it) }
      muxer.setOrientationHint(integer(videoFormat, MediaFormat.KEY_ROTATION))
      muxer.start()
      started = true
      val maxSize = sources.maxOf { maxOf(it.video.maxSampleSize, it.audio?.maxSampleSize ?: 0) }
      val buffer = ByteBuffer.allocateDirect(maxSize)
      val info = MediaCodec.BufferInfo()
      var cursorUs = 0L
      var videoEndUs = 0L
      var audioEndUs = 0L
      for (source in sources) {
        val extractor = MediaExtractor()
        try {
          extractor.setDataSource(source.file.absolutePath)
          extractor.selectTrack(source.video.index)
          source.audio?.let { extractor.selectTrack(it.index) }
          while (extractor.sampleTrackIndex >= 0) {
            buffer.clear()
            val bytes = extractor.readSampleData(buffer, 0)
            require(bytes > 0 && bytes <= buffer.capacity()) { "Unreadable source sample" }
            val target = when (extractor.sampleTrackIndex) {
              source.video.index -> videoOut
              source.audio?.index -> requireNotNull(audioOut)
              else -> error("Unexpected selected track")
            }
            // Extractor flags are not MediaCodec flags (encrypted=2, partial=4).
            require(extractor.sampleFlags and MediaExtractor.SAMPLE_FLAG_ENCRYPTED == 0 &&
              extractor.sampleFlags and MediaExtractor.SAMPLE_FLAG_PARTIAL_FRAME == 0) { "Unsupported encrypted or partial sample" }
            info.set(0, bytes, Math.addExact(cursorUs, extractor.sampleTime - source.startUs),
              if (extractor.sampleFlags and MediaExtractor.SAMPLE_FLAG_SYNC != 0) MediaCodec.BUFFER_FLAG_KEY_FRAME else 0)
            muxer.writeSampleData(target, buffer, info)
            extractor.advance()
          }
          videoEndUs = cursorUs + source.video.endUs - source.startUs
          source.audio?.let { audioEndUs = cursorUs + it.endUs - source.startUs }
          cursorUs = Math.addExact(cursorUs, source.durationUs)
        } finally {
          extractor.release()
        }
      }
      // Explicit EOS duration keeps even a one-frame final tail. Without it,
      // MediaMuxer guesses the last duration from the preceding sample.
      val empty = ByteBuffer.allocate(0)
      info.set(0, 0, videoEndUs, MediaCodec.BUFFER_FLAG_END_OF_STREAM)
      muxer.writeSampleData(videoOut, empty, info)
      if (audioOut != null) {
        info.set(0, 0, audioEndUs, MediaCodec.BUFFER_FLAG_END_OF_STREAM)
        muxer.writeSampleData(audioOut, empty, info)
      }
      muxer.stop()
      started = false
      return cursorUs
    } finally {
      // Stop/release failures must reject the operation; no result is published.
      try { if (started) muxer.stop() } finally { muxer.release() }
    }
  }

  private fun inspect(file: File): Source {
    require(file.isFile && file.length() > 0) { "Missing or empty source: ${file.path}" }
    val extractor = MediaExtractor()
    try {
      extractor.setDataSource(file.absolutePath)
      var video: Track? = null
      var audio: Track? = null
      for (i in 0 until extractor.trackCount) {
        val format = extractor.getTrackFormat(i)
        val mime = format.getString(MediaFormat.KEY_MIME) ?: error("Track has no MIME type")
        when {
          mime.startsWith("video/") -> {
            require(video == null) { "Multiple video tracks are unsupported" }
            video = inspectTrack(file, i, format)
          }
          mime.startsWith("audio/") -> {
            require(audio == null) { "Multiple audio tracks are unsupported" }
            audio = inspectTrack(file, i, format)
          }
        }
      }
      return Source(file, requireNotNull(video) { "Source has no video track: ${file.path}" }, audio)
    } finally {
      extractor.release()
    }
  }

  private fun inspectTrack(file: File, index: Int, format: MediaFormat): Track {
    val extractor = MediaExtractor()
    try {
      extractor.setDataSource(file.absolutePath)
      extractor.selectTrack(index)
      var first = Long.MAX_VALUE
      var last = Long.MIN_VALUE
      var count = 0L
      var maxSize = 0
      var buffer = ByteBuffer.allocateDirect(maxOf(integer(format, MediaFormat.KEY_MAX_INPUT_SIZE), 1024 * 1024))
      while (extractor.sampleTrackIndex >= 0) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
          val sampleSize = extractor.sampleSize
          require(sampleSize in 1..Int.MAX_VALUE.toLong()) { "Invalid sample size" }
          if (sampleSize > buffer.capacity()) buffer = ByteBuffer.allocateDirect(sampleSize.toInt())
        }
        buffer.clear()
        val bytes = extractor.readSampleData(buffer, 0)
        require(bytes > 0 && bytes <= buffer.capacity()) { "Unreadable source sample" }
        first = minOf(first, extractor.sampleTime)
        last = maxOf(last, extractor.sampleTime)
        maxSize = maxOf(maxSize, bytes)
        count++
        extractor.advance()
      }
      require(count > 0 && format.containsKey(MediaFormat.KEY_DURATION)) { "Empty track or unknown track duration" }
      return Track(index, format, first, trackSampleEndUs(first, last, format.getLong(MediaFormat.KEY_DURATION)), count, maxSize)
    } finally {
      extractor.release()
    }
  }

  private fun requireCompatible(a: MediaFormat, b: MediaFormat) {
    require(a.getString(MediaFormat.KEY_MIME) == b.getString(MediaFormat.KEY_MIME)) { "Incompatible codecs" }
    val keys = listOf(MediaFormat.KEY_WIDTH, MediaFormat.KEY_HEIGHT, MediaFormat.KEY_ROTATION,
      MediaFormat.KEY_SAMPLE_RATE, MediaFormat.KEY_CHANNEL_COUNT,
      MediaFormat.KEY_COLOR_STANDARD, MediaFormat.KEY_COLOR_RANGE, MediaFormat.KEY_COLOR_TRANSFER)
    require(keys.all { integer(a, it) == integer(b, it) }) { "Incompatible track format or orientation" }
    for (key in listOf("csd-0", "csd-1", "csd-2", MediaFormat.KEY_HDR_STATIC_INFO)) {
      val left = if (a.containsKey(key)) a.getByteBuffer(key)?.duplicate() else null
      val right = if (b.containsKey(key)) b.getByteBuffer(key)?.duplicate() else null
      require(left == right) { "Incompatible codec configuration" }
    }
  }

  private fun integer(format: MediaFormat, key: String) = if (format.containsKey(key)) format.getInteger(key) else 0

  private fun localFile(path: String): File {
    val file = if (path.startsWith("file:")) {
      val uri = Uri.parse(path)
      require(uri.scheme == "file" && (uri.host.isNullOrEmpty() || uri.host == "localhost")) { "Invalid local file URI" }
      File(requireNotNull(uri.path) { "Missing file URI path" })
    } else File(path)
    require(file.isAbsolute) { "An absolute local file path is required" }
    return file.canonicalFile
  }
}
