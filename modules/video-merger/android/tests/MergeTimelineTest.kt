package expo.modules.videomerger

fun main() {
  // 60 ten-second clips must stay 600 seconds, rather than losing most of one
  // 30-fps frame per boundary through the old last-PTS-plus-one-ms rule.
  var end = 0L
  repeat(60) { end += trackSampleEndUs(0, 9_966_667, 10_000_000) }
  check(end == 600_000_000L)
  check(trackSampleEndUs(1_500_000, 1_566_667, 100_000) == 1_600_000L)
  check(trackSampleEndUs(0, 0, 16_667) == 16_667L) // valid single-frame final tail
  check(trackSampleEndUs(0, 43_000, 57_000) == 57_000L) // VFR final sample
  check(trackSampleEndUs(-21_333, 978_667, 1_024_000) == 1_002_667L) // audio priming PTS
  for (invalid in listOf(0L, 1L, 33_333L)) {
    check(runCatching { trackSampleEndUs(0, 33_333, invalid) }.isFailure)
  }
  check(runCatching { trackSampleEndUs(20, 10, 100) }.isFailure)
  check(runCatching { trackSampleEndUs(Long.MAX_VALUE - 1, Long.MAX_VALUE, 10) }.isFailure)
  println("MergeTimeline: cumulative duration, nonzero/negative anchors, micro-tail, VFR and invalid metadata passed")
}
