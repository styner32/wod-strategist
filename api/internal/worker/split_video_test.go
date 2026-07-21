package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("server-split media mapping", func() {
	It("uses cumulative probed durations without keyframe overlaps or gaps", func() {
		start0, end0 := splitChunkMediaInterval(0, 10.4, 30)
		start1, end1 := splitChunkMediaInterval(end0, 9.6, 30)
		start2, end2 := splitChunkMediaInterval(end1, 12, 30)

		Expect([]float64{start0, end0}).To(Equal([]float64{0, 10.4}))
		Expect([]float64{start1, end1}).To(Equal([]float64{10.4, 20}))
		Expect([]float64{start2, end2}).To(Equal([]float64{20, 30}))
	})

	It("persists the source-video-relative interval separately from capture timestamps", func() {
		database, err := testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(database)
		profile := testhelpers.CreateProfile(database, &db.Profile{})
		w := &Worker{DB: database, logger: zap.NewNop()}

		w.saveChunkResult(
			VideoAnalysisPayload{SessionID: "split-media-session", ProfileID: profile.ID},
			"gs://test-bucket/videos/1/split-media-session/split_chunk_000.mp4",
			30.25,
			60.5,
			"COMPLETED",
			"Pull-up",
			"coaching",
			"{}",
			nil,
			"",
		)

		var result db.ChunkAnalysisResult
		Expect(database.Where("session_id = ?", "split-media-session").First(&result).Error).To(Succeed())
		Expect(result.StartSecs).NotTo(BeNil())
		Expect(result.EndSecs).NotTo(BeNil())
		Expect(result.MediaStartSecs).NotTo(BeNil())
		Expect(result.MediaEndSecs).NotTo(BeNil())
		Expect(*result.StartSecs).To(Equal(30.25))
		Expect(*result.EndSecs).To(Equal(60.5))
		Expect(*result.MediaStartSecs).To(Equal(30.25))
		Expect(*result.MediaEndSecs).To(Equal(60.5))
	})
})
