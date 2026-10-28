package worker

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("server-split media mapping", func() {
	It("keeps source presentation times despite MP4 timestamp resets and rejects missing or reordered clips", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg is unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		directory, err := os.MkdirTemp("", "verified-split-test")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(directory)
		source := filepath.Join(directory, "source.mp4")
		output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "8.5", "-c:v", "libx264", "-g", "30", "-bf", "2", "-c:a", "aac", "-y", source).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(runFFmpegSplit(ctx, zap.NewNop(), source, filepath.Join(directory, "chunk_%03d.mp4"), 2)).To(Succeed())
		files, err := discoverChunkFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(files)).To(BeNumerically(">", 2))
		duration := probeVideoDuration(ctx, source)
		intervals, err := verifiedSplitMediaIntervals(ctx, source, directory, files, duration)
		Expect(err).NotTo(HaveOccurred())
		Expect(intervals[0].Start).To(BeNumerically("~", 0, 1e-6))
		// With B-frame decode delay, the muxer cuts at the 3-second keyframe.
		Expect(intervals[1].Start).To(BeNumerically("~", 3, 1e-6))
		Expect(intervals[len(intervals)-1].End).To(Equal(duration))
		// The fixture has B-frames and AAC encoder delay; accumulating each
		// standalone movie's duration creates an observable source-time drift.
		legacyCursor := 0.0
		maxDrift := 0.0
		for i, name := range files {
			maxDrift = math.Max(maxDrift, math.Abs(legacyCursor-intervals[i].Start))
			legacyCursor += probeVideoDuration(ctx, filepath.Join(directory, name))
			if i > 0 {
				Expect(intervals[i-1].End).To(Equal(intervals[i].Start))
			}
		}
		Expect(maxDrift).To(BeNumerically(">", 0.02))
		reordered := append([]string(nil), files...)
		reordered[0], reordered[1] = reordered[1], reordered[0]
		_, err = verifiedSplitMediaIntervals(ctx, source, directory, reordered, duration)
		Expect(err).To(HaveOccurred())
		_, err = verifiedSplitMediaIntervals(ctx, source, directory, files[:len(files)-1], duration)
		Expect(err).To(MatchError(ContainSubstring("omitted source video packets")))
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

	It("deduplicates repeated saveChunkResult writes and skips pause-shifted mobile intervals", func() {
		database, err := testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(database)
		profile := testhelpers.CreateProfile(database, &db.Profile{})
		w := &Worker{DB: database, logger: zap.NewNop()}

		sessionID := "split-idempotent-session"
		capStart, capEnd := 118.5, 128.5
		medStart, medEnd := 0.0, 10.0
		testhelpers.CreateChunkAnalysisResult(database, &db.ChunkAnalysisResult{
			SessionID:      sessionID,
			ProfileID:      profile.ID,
			FilePath:       "gs://test-bucket/videos/1/" + sessionID + "/mobile_000.mp4",
			Status:         "COMPLETED",
			HeartRateBPM:   154,
			StartSecs:      &capStart,
			EndSecs:        &capEnd,
			MediaStartSecs: &medStart,
			MediaEndSecs:   &medEnd,
		})

		Expect(w.chunkAlreadyAnalyzed(sessionID, 0.0)).To(BeTrue(), "media interval [0, 10) is already covered by mobile_000.mp4")
		Expect(w.chunkAlreadyAnalyzed(sessionID, 10.0)).To(BeFalse(), "media interval [10, 20) is uncovered")

		splitURI := "gs://test-bucket/videos/1/" + sessionID + "/split_chunk_001.mp4"
		p := VideoAnalysisPayload{SessionID: sessionID, ProfileID: profile.ID}
		w.saveChunkResult(p, splitURI, 10.0, 20.0, "FAILED", "", "An internal error occurred during chunk analysis.", "{}", nil, "")
		w.saveChunkResult(p, splitURI, 10.0, 20.0, "COMPLETED", "Burpee", "steady pace", "{}", nil, "")
		w.saveChunkResult(p, splitURI, 10.0, 20.0, "COMPLETED", "Burpee", "duplicate retry", "{}", nil, "")

		var splitRows []db.ChunkAnalysisResult
		Expect(database.Where("session_id = ? AND file_path = ?", sessionID, splitURI).Find(&splitRows).Error).To(Succeed())
		Expect(splitRows).To(HaveLen(1))
		Expect(splitRows[0].Status).To(Equal("COMPLETED"))
		Expect(splitRows[0].Output).To(Equal("steady pace"))
	})
})
