package expo.modules.videomerger

/** Container track duration includes the final sample, unlike its last PTS. */
internal fun trackSampleEndUs(firstPtsUs: Long, lastPtsUs: Long, durationUs: Long): Long {
  require(durationUs > 0 && lastPtsUs >= firstPtsUs) { "Track has no verified sample duration" }
  val end = Math.addExact(firstPtsUs, durationUs)
  require(end > lastPtsUs) { "Track duration does not cover its last sample" }
  return end
}
