package db

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func floatPtr(v float64) *float64 {
	return &v
}

var _ = Describe("Chunk media coverage and split_chunk filtering", func() {
	Describe("MaxCompletedChunkMediaEndSecs", func() {
		It("uses verified media_end_secs instead of pause-shifted capture end_secs", func() {
			chunks := []ChunkAnalysisResult{
				{
					SessionID:      "s1",
					FilePath:       "gs://b/videos/1/s1/chunk_000.mp4",
					Status:         "COMPLETED",
					StartSecs:      floatPtr(118.5),
					EndSecs:        floatPtr(128.5),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					SessionID:      "s1",
					FilePath:       "gs://b/videos/1/s1/chunk_001.mp4",
					Status:         "COMPLETED",
					StartSecs:      floatPtr(128.5),
					EndSecs:        floatPtr(138.4),
					MediaStartSecs: floatPtr(10.0),
					MediaEndSecs:   floatPtr(19.9),
				},
				{
					// Unmapped mobile chunk must not contribute capture-clock EndSecs.
					SessionID: "s1",
					FilePath:  "gs://b/videos/1/s1/chunk_002.mp4",
					Status:    "COMPLETED",
					StartSecs: floatPtr(500.0),
					EndSecs:   floatPtr(510.0),
				},
				{
					// FAILED rows do not count as analyzed coverage.
					SessionID:      "s1",
					FilePath:       "gs://b/videos/1/s1/chunk_003.mp4",
					Status:         "FAILED",
					MediaStartSecs: floatPtr(19.9),
					MediaEndSecs:   floatPtr(29.9),
				},
			}

			Expect(MaxCompletedChunkMediaEndSecs(chunks)).To(BeNumerically("~", 19.9, 1e-6))
		})

		It("accepts legacy split_chunk_* end_secs when media_end_secs is nil", func() {
			chunks := []ChunkAnalysisResult{
				{
					SessionID: "s2",
					FilePath:  "gs://b/videos/1/s2/split_chunk_000.mp4",
					Status:    "COMPLETED",
					StartSecs: floatPtr(0.0),
					EndSecs:   floatPtr(10.0),
				},
			}
			Expect(MaxCompletedChunkMediaEndSecs(chunks)).To(Equal(10.0))
		})
	})

	Describe("IntervalCoveredByChunks", func() {
		It("matches pause-shifted mobile chunks via media_start_secs/media_end_secs and leaves true gaps uncovered", func() {
			chunks := []ChunkAnalysisResult{
				{
					SessionID:      "s1",
					FilePath:       "gs://b/videos/1/s1/mobile_0.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   148,
					StartSecs:      floatPtr(118.5),
					EndSecs:        floatPtr(128.4),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(9.9),
				},
				{
					SessionID:      "s1",
					FilePath:       "gs://b/videos/1/s1/mobile_1.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   152,
					StartSecs:      floatPtr(128.4),
					EndSecs:        floatPtr(138.4),
					MediaStartSecs: floatPtr(9.9),
					MediaEndSecs:   floatPtr(19.9),
				},
			}

			Expect(IntervalCoveredByChunks(0.0, 10.0, chunks)).To(BeTrue())
			Expect(IntervalCoveredByChunks(10.0, 20.0, chunks)).To(BeTrue())
			Expect(IntervalCoveredByChunks(20.0, 30.0, chunks)).To(BeFalse())
			// Capture-clock range [118.5, 128.5) is NOT media-covered when video is only 0..19.9s.
			Expect(IntervalCoveredByChunks(118.5, 128.5, chunks)).To(BeFalse())
		})
	})

	Describe("FilterRedundantSplitChunks", func() {
		It("removes duplicate and overlapping split_chunk_* rows while preserving BLE heart rate mobile chunks", func() {
			chunks := []ChunkAnalysisResult{
				{
					ID:             1,
					ProfileID:      1,
					SessionID:      "WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0",
					FilePath:       "gs://b/videos/1/WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0/mobile_000.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   142,
					StartSecs:      floatPtr(118.5),
					EndSecs:        floatPtr(128.5),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					ID:             2,
					ProfileID:      1,
					SessionID:      "WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0",
					FilePath:       "gs://b/videos/1/WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0/split_chunk_000.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   0,
					StartSecs:      floatPtr(0.0),
					EndSecs:        floatPtr(10.0),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					ID:             3,
					ProfileID:      1,
					SessionID:      "WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0",
					FilePath:       "gs://b/videos/1/WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0/split_chunk_000.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   0,
					StartSecs:      floatPtr(0.0),
					EndSecs:        floatPtr(10.0),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					// Inflated tail split_chunk beyond mapped mobile timeline [0, 20.0).
					ID:             4,
					ProfileID:      1,
					SessionID:      "WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0",
					FilePath:       "gs://b/videos/1/WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0/split_chunk_099.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   0,
					StartSecs:      floatPtr(25.0),
					EndSecs:        floatPtr(35.0),
					MediaStartSecs: floatPtr(25.0),
					MediaEndSecs:   floatPtr(35.0),
				},
				{
					ID:             5,
					ProfileID:      1,
					SessionID:      "WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0",
					FilePath:       "gs://b/videos/1/WOD-20261020-01M5BDXZ9KGNFRHZVRRWPKG5H0/mobile_001.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   149,
					StartSecs:      floatPtr(128.5),
					EndSecs:        floatPtr(138.5),
					MediaStartSecs: floatPtr(10.0),
					MediaEndSecs:   floatPtr(20.0),
				},
			}

			got := FilterRedundantSplitChunks(chunks)
			Expect(got).To(HaveLen(2))
			Expect(got[0].ID).To(Equal(uint(1)))
			Expect(got[0].HeartRateBPM).To(Equal(142))
			Expect(got[1].ID).To(Equal(uint(5)))
			Expect(got[1].HeartRateBPM).To(Equal(149))
		})

		It("retains deduplicated split_chunk_* rows for uploaded sessions without mobile chunks", func() {
			chunks := []ChunkAnalysisResult{
				{
					ID:             10,
					ProfileID:      1,
					SessionID:      "upload-only",
					FilePath:       "gs://b/videos/1/upload-only/split_chunk_000.mp4",
					Status:         "FAILED",
					StartSecs:      floatPtr(0.0),
					EndSecs:        floatPtr(10.0),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					ID:             11,
					ProfileID:      1,
					SessionID:      "upload-only",
					FilePath:       "gs://b/videos/1/upload-only/split_chunk_000.mp4",
					Status:         "COMPLETED",
					StartSecs:      floatPtr(0.0),
					EndSecs:        floatPtr(10.0),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					ID:             12,
					ProfileID:      1,
					SessionID:      "upload-only",
					FilePath:       "gs://b/videos/1/upload-only/split_chunk_001.mp4",
					Status:         "COMPLETED",
					StartSecs:      floatPtr(10.0),
					EndSecs:        floatPtr(20.0),
					MediaStartSecs: floatPtr(10.0),
					MediaEndSecs:   floatPtr(20.0),
				},
			}

			got := FilterRedundantSplitChunks(chunks)
			Expect(got).To(HaveLen(2))
			Expect(got[0].ID).To(Equal(uint(11)))
			Expect(got[1].ID).To(Equal(uint(12)))
		})

		It("retains gap-filling split_chunk_* rows where an original mobile chunk FAILED", func() {
			chunks := []ChunkAnalysisResult{
				{
					ID:             1,
					ProfileID:      1,
					SessionID:      "gap-session",
					FilePath:       "gs://b/videos/1/gap-session/mobile_000.mp4",
					Status:         "COMPLETED",
					HeartRateBPM:   140,
					StartSecs:      floatPtr(0.0),
					EndSecs:        floatPtr(10.0),
					MediaStartSecs: floatPtr(0.0),
					MediaEndSecs:   floatPtr(10.0),
				},
				{
					ID:             2,
					ProfileID:      1,
					SessionID:      "gap-session",
					FilePath:       "gs://b/videos/1/gap-session/mobile_001.mp4",
					Status:         "FAILED",
					StartSecs:      floatPtr(10.0),
					EndSecs:        floatPtr(20.0),
					MediaStartSecs: floatPtr(10.0),
					MediaEndSecs:   floatPtr(20.0),
				},
				{
					ID:             3,
					ProfileID:      1,
					SessionID:      "gap-session",
					FilePath:       "gs://b/videos/1/gap-session/split_chunk_001.mp4",
					Status:         "COMPLETED",
					StartSecs:      floatPtr(10.0),
					EndSecs:        floatPtr(20.0),
					MediaStartSecs: floatPtr(10.0),
					MediaEndSecs:   floatPtr(20.0),
				},
			}

			got := FilterRedundantSplitChunks(chunks)
			Expect(got).To(HaveLen(3))
			Expect([]uint{got[0].ID, got[1].ID, got[2].ID}).To(Equal([]uint{1, 2, 3}))
		})

		It("never deduplicates rows with empty FilePath and preserves their input order", func() {
			chunks := []ChunkAnalysisResult{
				{ID: 3, SessionID: "s", Status: "COMPLETED"},
				{ID: 1, SessionID: "s", Status: "COMPLETED"},
				{ID: 2, SessionID: "s", Status: "FAILED"},
			}
			got := FilterRedundantSplitChunks(chunks)
			Expect([]uint{got[0].ID, got[1].ID, got[2].ID}).To(Equal([]uint{3, 1, 2}))
		})
	})
})
