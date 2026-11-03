package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

var _ = Describe("selectHighlightGroupIndices", func() {
	It("skips a non-fitting candidate and chronologically orders later selections", func() {
		clips := []highlightClip{
			{StartSecs: 30, EndSecs: 39},
			{StartSecs: 20, EndSecs: 25},
			{StartSecs: 10, EndSecs: 13},
		}

		selected := selectHighlightGroupIndices(clips, []int{0, 1, 2, 1}, 8)

		Expect(selected).To(Equal([]int{2, 1}))
	})
})

var _ = Describe("buildHighlightGroups", func() {
	It("classifies parent events by observations and key-moment metadata without duplicating trims", func() {
		clips := []highlightClip{
			{
				Segment: HighlightSegment{
					Type: "mixed_form",
					Tags: []string{HighlightTagKeyMoment},
					Observations: []HighlightObservation{
						{Type: HighlightObservationPositiveForm},
						{Type: HighlightObservationFormIssue},
					},
				},
				StartSecs: 0,
				EndSecs:   5,
			},
			{
				Segment: HighlightSegment{
					Type:         "fatigue_point",
					Observations: []HighlightObservation{{Type: HighlightObservationFatigueOnset}},
				},
				StartSecs: 10,
				EndSecs:   15,
			},
			{
				Segment: HighlightSegment{
					Type:         "key_moment",
					Observations: []HighlightObservation{{Type: HighlightObservationTechnique}},
				},
				StartSecs: 20,
				EndSecs:   25,
			},
		}

		groups := buildHighlightGroups(clips, 60)

		Expect(groups).To(HaveLen(4))
		Expect(groups[0].Indices).To(Equal([]int{0, 1, 2}))
		Expect(groups[1].Indices).To(Equal([]int{0}))
		Expect(groups[2].Indices).To(Equal([]int{0, 1}))
		Expect(groups[3].Indices).To(Equal([]int{0, 2}))
		Expect(selectedHighlightIndices(clips, groups)).To(Equal([]int{0, 1, 2}))
	})
})

var _ = Describe("runFFmpegFinalPolish", func() {
	It("keeps final playback within the selected source-duration budget", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not found in PATH")
		}
		input := createTinyMP4(GinkgoT())
		sourceDuration := probeVideoDuration(context.Background(), input)
		Expect(sourceDuration).To(BeNumerically(">", 0))
		output := GinkgoT().TempDir() + "/polished.mp4"

		Expect(runFFmpegFinalPolish(
			context.Background(), zap.NewNop(), input, "", output, sourceDuration,
		)).To(Succeed())

		finalDuration := probeVideoDuration(context.Background(), output)
		Expect(finalDuration).To(BeNumerically(">", 0))
		Expect(finalDuration).To(BeNumerically("<=", sourceDuration+0.25))
	})

	It("rejects a source whose actual duration differs from the requested polish duration", func() {
		requireAdjacentMediaTools()
		input := createTinyMP4(GinkgoT())
		output := filepath.Join(GinkgoT().TempDir(), "invalid-polish.mp4")
		Expect(runFFmpegFinalPolish(context.Background(), zap.NewNop(), input, "", output, 0.2)).
			To(MatchError(ContainSubstring("invalid highlight polish source")))
		_, err := os.Stat(output)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
})

var _ = Describe("runFFmpegTrim media integrity", func() {
	BeforeEach(requireAdjacentMediaTools)

	DescribeTable("accurately extracts a non-keyframe interval into a canonical clip",
		func(hasAudio bool) {
			source := filepath.Join(GinkgoT().TempDir(), "long-gop.mp4")
			args := []string{"-f", "lavfi", "-i", "color=c=red:size=96x64:rate=25"}
			if hasAudio {
				args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono")
			}
			args = append(args, "-t", "12", "-vf", "drawbox=x=0:y=0:w=iw:h=ih:color=blue:t=fill:enable='gte(t,4)'",
				"-c:v", "libx264", "-g", "250", "-keyint_min", "250", "-sc_threshold", "0", "-bf", "2")
			if hasAudio {
				args = append(args, "-c:a", "aac")
			} else {
				args = append(args, "-an")
			}
			runAdjacentMediaFixture(append(args, source)...)
			output := filepath.Join(GinkgoT().TempDir(), "clip.mp4")

			Expect(runFFmpegTrim(context.Background(), zap.NewNop(), source, output, 4.01, 1)).To(Succeed())
			timeline, err := probeVideoTimeline(context.Background(), output)
			Expect(err).NotTo(HaveOccurred())
			Expect(timeline.FrameCount).To(Equal(30))
			Expect(timeline.Start).To(BeNumerically("~", 0, 0.0001))
			Expect(timeline.Duration()).To(BeNumerically("~", 1, 0.034))
			profile, err := probeChunkStreamProfile(context.Background(), output)
			Expect(err).NotTo(HaveOccurred())
			Expect(profile.VideoCodec).To(Equal("h264"))
			Expect(profile.VideoTimescale).To(Equal(canonicalConcatVideoTimescale))
			Expect(profile.PixFmt).To(Equal("yuv420p"))
			Expect(profile.HasAudio).To(Equal(hasAudio))
			if hasAudio {
				Expect(profile.AudioCodec).To(Equal("aac"))
				Expect(profile.SampleRate).To(Equal(44100))
				Expect(profile.Channels).To(Equal(2))
			}
			// Before the fix, stream copying retained the red footage before the
			// requested interval. Verify content as well as the resulting duration.
			frame, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-i", output,
				"-frames:v", "1", "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "-").Output()
			Expect(err).NotTo(HaveOccurred())
			Expect(frame).To(HaveLen(3))
			Expect(int(frame[0])).To(BeNumerically("<", 30))
			Expect(int(frame[2])).To(BeNumerically(">", 200))
		},
		Entry("without audio", false),
		Entry("with audio resampled to the concat format", true),
	)

	It("keeps a tail interval bounded and rejects a clip extending past EOF", func() {
		source := createNarrationSource(false)
		output := filepath.Join(GinkgoT().TempDir(), "tail.mp4")
		Expect(runFFmpegTrim(context.Background(), zap.NewNop(), source, output, 2.01, 0.99)).To(Succeed())
		Expect(verifyVideoDuration(context.Background(), output, 0.99, 0.1)).To(Succeed())
		Expect(runFFmpegTrim(context.Background(), zap.NewNop(), source, output, 2.5, 2)).
			To(MatchError(ContainSubstring("highlight clip does not match requested duration")))
	})

	It("concatenates separately extracted intervals without losing or duplicating frames", func() {
		source := createNarrationSource(true)
		directory := GinkgoT().TempDir()
		var entries []string
		var frames []videoFrameSignature
		var expectedDuration float64
		for i, start := range []float64{0.21, 1.51} {
			clip := filepath.Join(directory, fmt.Sprintf("clip-%d.mp4", i))
			Expect(runFFmpegTrim(context.Background(), zap.NewNop(), source, clip, start, 1)).To(Succeed())
			timeline, err := probeVideoTimeline(context.Background(), clip)
			Expect(err).NotTo(HaveOccurred())
			signature, err := readVideoFrameSignature(context.Background(), clip)
			Expect(err).NotTo(HaveOccurred())
			frames = append(frames, signature)
			expectedDuration += timeline.Duration()
			entries = append(entries, fmt.Sprintf("file '%s'\nduration %.9f", clip, timeline.Duration()))
		}
		manifest := filepath.Join(directory, "concat.txt")
		Expect(os.WriteFile(manifest, []byte(strings.Join(entries, "\n")), 0o600)).To(Succeed())
		output := filepath.Join(directory, "joined.mp4")
		Expect(runFFmpegStreamCopyConcat(context.Background(), zap.NewNop(), manifest, output, expectedDuration, true)).To(Succeed())
		Expect(verifyVideoFrameSequence(context.Background(), output, frames)).To(Succeed())
		Expect(verifyMediaEndpoints(context.Background(), output, expectedDuration, 0.1)).To(Succeed())
	})
})

var _ = Describe("HandleGenerateHighlightTask", func() {
	const highlightSegs = `[{"start":"0:00","end":"0:00.4","type":"best_form","reason":"완벽한 자세"},{"start":"0:00.4","end":"0:00.8","type":"key_moment","reason":"최고 페이스"}]`

	var (
		dbConn    *gorm.DB
		w         *Worker
		profileID uint
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

		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		profileID = p.ID
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
			ProfileID:         profileID,
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
			ProfileID:         profileID,
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

	It("uses the canonical merged video without requiring chunk records", func() {
		Expect(dbConn.Create(&db.AnalysisResult{
			SessionID:         "sess-hl-nochunks",
			ProfileID:         profileID,
			AnalysisType:      db.AnalysisTypeWOD,
			Status:            "COMPLETED",
			Output:            "분석",
			HighlightSegments: highlightSegs,
		}).Error).NotTo(HaveOccurred())

		storageTransport := testhelpers.NewMockTransport()
		storageClient, storageErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(storageErr).NotTo(HaveOccurred())
		w.StorageClient = storageClient
		storageTransport.New(testhelpers.GCSBaseURL).
			Get("/test-bucket/videos/" + fmt.Sprint(profileID) + "/sess-hl-nochunks/merged.mp4").
			Reply(http.StatusNotFound).
			BodyString("not found")

		task, err := NewGenerateHighlightTask("sess-hl-nochunks", profileID, 60)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleGenerateHighlightTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("failed to download merged highlight source")))
		Expect(storageTransport.Verify()).To(Succeed())
	})

	Context("when ffmpeg is available", func() {
		BeforeEach(func() {
			if !hasFfmpeg() {
				Skip("ffmpeg not found in PATH")
			}
		})

		It("normalizes legacy events and creates reels from one merged-media download", func() {
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID:         "sess-hl-happy",
				ProfileID:         profileID,
				AnalysisType:      db.AnalysisTypeWOD,
				Status:            "COMPLETED",
				Output:            "훌륭한 운동",
				HighlightSegments: highlightSegs,
			})

			tiny := createTinyMP4(GinkgoT())
			mp4Bytes, readErr := os.ReadFile(tiny)
			Expect(readErr).NotTo(HaveOccurred())

			// Create a fresh transport with all GCS expectations for this test
			ffmpegTransport := testhelpers.NewMockTransport()
			ffmpegStorageClient, sErr := testhelpers.NewStorageClient("test-bucket", ffmpegTransport)
			Expect(sErr).NotTo(HaveOccurred())
			w.StorageClient = ffmpegStorageClient

			mergedURI := "gs://test-bucket/videos/" + fmt.Sprint(profileID) + "/sess-hl-happy/merged.mp4"
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, mergedURI, mp4Bytes)
			// The adjacent best/key legacy segments normalize to one parent event
			// shared by the full, best, and key reels.
			for i := 0; i < 3; i++ {
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

			task, err := NewGenerateHighlightTask("sess-hl-happy", profileID+999, 60)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleGenerateHighlightTask(context.Background(), task)).To(Succeed())

			// At minimum the "Highlight Reel" (full) group is written
			var fullResult db.HighlightResult
			Expect(dbConn.Where("session_id = ? AND title = ?", "sess-hl-happy", "Highlight Reel").
				First(&fullResult).Error).NotTo(HaveOccurred())
			Expect(fullResult.Status).To(Equal("COMPLETED"))
			Expect(fullResult.GCSURI).To(ContainSubstring("sess-hl-happy"))
			Expect(fullResult.DurationSec).To(BeNumerically(">", 0))
			Expect(fullResult.ProfileID).To(Equal(profileID))

			var normalized []HighlightSegment
			Expect(json.Unmarshal([]byte(fullResult.Segments), &normalized)).To(Succeed())
			Expect(normalized).To(HaveLen(1))
			Expect(normalized[0].Version).To(Equal(2))
			Expect(normalized[0].Observations).To(HaveLen(2))

			var resultCount int64
			Expect(dbConn.Model(&db.HighlightResult{}).
				Where("session_id = ?", "sess-hl-happy").
				Count(&resultCount).Error).NotTo(HaveOccurred())
			Expect(resultCount).To(Equal(int64(3)))
			uploads := adjacentUploadedVideos(ffmpegTransport.Requests())
			Expect(uploads).To(HaveLen(3))
			var results []db.HighlightResult
			Expect(dbConn.Where("session_id = ?", "sess-hl-happy").Order("id ASC").Find(&results).Error).NotTo(HaveOccurred())
			for i, upload := range uploads {
				timeline, err := probeVideoTimeline(context.Background(), upload)
				Expect(err).NotTo(HaveOccurred())
				Expect(results[i].DurationSec).To(BeNumerically("~", timeline.Duration(), 0.000001))
			}
			Expect(ffmpegTransport.Verify()).To(Succeed())
			Expect(geminiTransport.Verify()).To(Succeed())
		})

		It("rejects an unverified source before uploading or marking a highlight complete", func() {
			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID: "sess-hl-damaged", ProfileID: profileID, Status: "COMPLETED", HighlightSegments: highlightSegs,
			})
			transport := testhelpers.NewMockTransport()
			client, err := testhelpers.NewStorageClient("test-bucket", transport)
			Expect(err).NotTo(HaveOccurred())
			w.StorageClient = client
			testhelpers.MockGCSDownloadWithBody(transport,
				fmt.Sprintf("gs://test-bucket/videos/%d/sess-hl-damaged/merged.mp4", profileID), []byte("damaged source"))
			task, err := NewGenerateHighlightTask("sess-hl-damaged", profileID, 60)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleGenerateHighlightTask(context.Background(), task)).To(MatchError(ContainSubstring("probe merged highlight source")))
			var count int64
			Expect(dbConn.Model(&db.HighlightResult{}).Where("session_id = ? AND status = ?", "sess-hl-damaged", "COMPLETED").Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(BeZero())
			Expect(adjacentUploadedVideos(transport.Requests())).To(BeEmpty())
			Expect(transport.Verify()).To(Succeed())
		})
	})
})
