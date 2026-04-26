package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("NewGenerateHighlightTask", func() {

	It("creates a task with the correct payload", func() {
		task, err := NewGenerateHighlightTask("session-1", 7, 90)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeGenerateHighlight))

		var payload HighlightPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.ProfileID).To(Equal(uint(7)))
		Expect(payload.MaxDuration).To(Equal(90))
	})

	It("defaults MaxDuration to 60 when 0 is passed", func() {
		task, err := NewGenerateHighlightTask("session-2", 0, 0)
		Expect(err).NotTo(HaveOccurred())

		var payload HighlightPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.MaxDuration).To(Equal(60))
	})
})

var _ = Describe("HandleGenerateHighlightTask", func() {
	const highlightSegs = `[{"start":"0:00","end":"0:01","type":"best_form","reason":"완벽한 자세"},{"start":"0:01","end":"0:02","type":"key_moment","reason":"최고 페이스"}]`

	var (
		dbConn *gorm.DB
		w      *Worker
	)

	const (
		geminiBaseURL = "https://generativelanguage.googleapis.com"
		geminiAPIKey  = "test-api-key"
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		storageTransport := testhelpers.NewMockTransport()
		storageClient, sErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(sErr).NotTo(HaveOccurred())

		// Real Gemini client backed by MockTransport — GenerateWorkoutMusic
		// calls generateContent on the lyria model. We return an empty response
		// (no InlineData) so the call is best-effort and fails gracefully.
		geminiTransport := testhelpers.NewMockTransport()
		geminiClient, gErr := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:       geminiAPIKey,
			BaseURL:      geminiBaseURL,
			HTTPClient:   &http.Client{Transport: geminiTransport},
			PollInterval: time.Millisecond,
			Sleep:        func(time.Duration) {},
		})
		Expect(gErr).NotTo(HaveOccurred())

		w = &Worker{
			DB:            dbConn,
			StorageClient: storageClient,
			GeminiClient:  geminiClient,
			QueueClient:   testhelpers.NewQueueClient(),
			BucketName:    "test-bucket",
			logger:        zap.NewNop(),
		}
	})

	It("returns SkipRetry when no completed WOD analysis exists", func() {
		task, err := NewGenerateHighlightTask("sess-hl-noresult", 0, 60)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleGenerateHighlightTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no completed WOD analysis found")))
	})

	It("returns SkipRetry when WOD analysis has no highlight segments", func() {
		Expect(dbConn.Create(&db.AnalysisResult{
			SessionID:         "sess-hl-nosegments",
			AnalysisType:      db.AnalysisTypeWOD,
			Status:            "COMPLETED",
			Output:            "분석 완료",
			HighlightSegments: "",
		}).Error).NotTo(HaveOccurred())

		task, err := NewGenerateHighlightTask("sess-hl-nosegments", 0, 60)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleGenerateHighlightTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no highlight segments available")))
	})

	It("returns SkipRetry when highlight segments JSON is invalid", func() {
		Expect(dbConn.Create(&db.AnalysisResult{
			SessionID:         "sess-hl-badjson",
			AnalysisType:      db.AnalysisTypeWOD,
			Status:            "COMPLETED",
			Output:            "분석",
			HighlightSegments: "{not valid json}",
		}).Error).NotTo(HaveOccurred())

		task, err := NewGenerateHighlightTask("sess-hl-badjson", 0, 60)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleGenerateHighlightTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid highlight segments JSON")))
	})

	It("returns SkipRetry when no chunk records with file_path are found", func() {
		// No ChunkAnalysisResult records with file_path → returns error
		Expect(dbConn.Create(&db.AnalysisResult{
			SessionID:         "sess-hl-nochunks",
			AnalysisType:      db.AnalysisTypeWOD,
			Status:            "COMPLETED",
			Output:            "분석",
			HighlightSegments: highlightSegs,
		}).Error).NotTo(HaveOccurred())

		task, err := NewGenerateHighlightTask("sess-hl-nochunks", 0, 60)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleGenerateHighlightTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no chunk records found")))
	})

	Context("when ffmpeg is available", func() {
		BeforeEach(func() {
			if !hasFfmpeg() {
				Skip("ffmpeg not found in PATH")
			}
		})

		It("creates COMPLETED HighlightResult records for each theme group", func() {
			Expect(dbConn.Create(&db.AnalysisResult{
				SessionID:         "sess-hl-happy",
				AnalysisType:      db.AnalysisTypeWOD,
				Status:            "COMPLETED",
				Output:            "훌륭한 운동",
				HighlightSegments: highlightSegs,
			}).Error).NotTo(HaveOccurred())

			start1, end1 := 0.0, 1.0
			start2, end2 := 1.0, 2.0

			tiny := createTinyMP4(GinkgoT())
			mp4Bytes, readErr := os.ReadFile(tiny)
			Expect(readErr).NotTo(HaveOccurred())

			chunk1URI := "gs://test-bucket/videos/sess-hl-happy/chunk_001.mp4"
			chunk2URI := "gs://test-bucket/videos/sess-hl-happy/chunk_002.mp4"

			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "sess-hl-happy", Status: "COMPLETED", FilePath: chunk1URI,
				Output: "좋아요", StartSecs: &start1, EndSecs: &end1,
			}).Error).NotTo(HaveOccurred())
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "sess-hl-happy", Status: "COMPLETED", FilePath: chunk2URI,
				Output: "계속하세요", StartSecs: &start2, EndSecs: &end2,
			}).Error).NotTo(HaveOccurred())

			// Create a fresh transport with all GCS expectations for this test
			ffmpegTransport := testhelpers.NewMockTransport()
			ffmpegStorageClient, sErr := testhelpers.NewStorageClient("test-bucket", ffmpegTransport)
			Expect(sErr).NotTo(HaveOccurred())
			w.StorageClient = ffmpegStorageClient

			// MockGCSDownload for each chunk (serve real mp4 bytes so ffmpeg can process it)
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, chunk1URI, mp4Bytes)
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, chunk2URI, mp4Bytes)
			// Uploads for each highlight group (full, best, key) + potentially music
			// The handler makes multiple uploads; register enough mocks
			for i := 0; i < 5; i++ {
				testhelpers.MockGCSUpload(ffmpegTransport, "test-bucket", "highlight")
			}

			// Wire a Gemini transport that returns empty audio (best-effort music fails gracefully)
			geminiTransport := testhelpers.NewMockTransport()
			geminiClient, gErr := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
				APIKey:       geminiAPIKey,
				BaseURL:      geminiBaseURL,
				HTTPClient:   &http.Client{Transport: geminiTransport},
				PollInterval: time.Millisecond,
				Sleep:        func(time.Duration) {},
			})
			Expect(gErr).NotTo(HaveOccurred())
			w.GeminiClient = geminiClient

			// Music generation: generateContent returns no InlineData → best-effort fails gracefully
			geminiTransport.New(geminiBaseURL).
				Post("/v1beta/models/lyria-3-clip-preview:generateContent").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": "no audio"}},
						},
					}},
				})

			task, err := NewGenerateHighlightTask("sess-hl-happy", 0, 60)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleGenerateHighlightTask(context.Background(), task)).To(Succeed())

			// At minimum the "Highlight Reel" (full) group is written
			var fullResult db.HighlightResult
			Expect(dbConn.Where("session_id = ? AND title = ?", "sess-hl-happy", "Highlight Reel").
				First(&fullResult).Error).NotTo(HaveOccurred())
			Expect(fullResult.Status).To(Equal("COMPLETED"))
			Expect(fullResult.GCSURI).To(ContainSubstring("sess-hl-happy"))
			Expect(fullResult.DurationSec).To(BeNumerically(">", 0))
		})
	})
})
