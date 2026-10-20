package worker

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

func makeSolidFrame(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

var _ = Describe("frame_grid", func() {
	DescribeTable("formatFrameTimestamp",
		func(d time.Duration, expected string) {
			Expect(formatFrameTimestamp(d)).To(Equal(expected))
		},
		Entry("negative duration clamps to zero", -500*time.Millisecond, "00:00:00.000"),
		Entry("exact 10m24s", 10*time.Minute+24*time.Second, "00:10:24.000"),
		Entry("quarter second step", 10*time.Minute+24*time.Second+250*time.Millisecond, "00:10:24.250"),
		Entry("end of 8s window at 4 FPS", 10*time.Minute+31*time.Second+750*time.Millisecond, "00:10:31.750"),
		Entry("over 1 hour", time.Hour+2*time.Minute+3*time.Second+50*time.Millisecond, "01:02:03.050"),
	)

	Describe("composeFrameGridImages", func() {
		It("composes 32 frames into a single 8x4 contact sheet with burned-in timestamps", func() {
			const (
				tileW = 160
				tileH = 100
			)
			frames := make([]image.Image, 32)
			for i := range frames {
				frames[i] = makeSolidFrame(tileW, tileH, color.RGBA{R: 180, G: 180, B: 180, A: 255})
			}

			baseOffset := 10*time.Minute + 24*time.Second
			sheets, err := composeFrameGridImages(frames, baseOffset, 8*time.Second, FrameGridOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(sheets).To(HaveLen(1))

			sheet := sheets[0]
			Expect(sheet.MIMEType).To(Equal("image/jpeg"))
			Expect(sheet.FrameCount).To(Equal(32))
			Expect(sheet.Cols).To(Equal(8))
			Expect(sheet.Rows).To(Equal(4))
			Expect(sheet.Start).To(Equal(baseOffset))
			Expect(sheet.End).To(Equal(baseOffset + 8*time.Second))

			decoded, err := jpeg.Decode(bytes.NewReader(sheet.ImageBytes))
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded.Bounds().Dx()).To(Equal(8*tileW + 7*defaultFrameGridBorderPx))
			Expect(decoded.Bounds().Dy()).To(Equal(4*tileH + 3*defaultFrameGridBorderPx))
		})

		It("splits intervals longer than 8 seconds (>32 frames at 4 FPS) into multiple sheets", func() {
			frames := make([]image.Image, 40) // 10 seconds at 4 FPS
			for i := range frames {
				frames[i] = makeSolidFrame(120, 80, color.RGBA{R: 120, G: 140, B: 160, A: 255})
			}

			baseOffset := 2 * time.Minute
			sheets, err := composeFrameGridImages(frames, baseOffset, 10*time.Second, FrameGridOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(sheets).To(HaveLen(2))

			Expect(sheets[0].FrameCount).To(Equal(32))
			Expect(sheets[0].Cols).To(Equal(8))
			Expect(sheets[0].Rows).To(Equal(4))
			Expect(sheets[0].Start).To(Equal(2 * time.Minute))
			Expect(sheets[0].End).To(Equal(2*time.Minute + 8*time.Second))

			Expect(sheets[1].FrameCount).To(Equal(8))
			Expect(sheets[1].Cols).To(Equal(8))
			Expect(sheets[1].Rows).To(Equal(1))
			Expect(sheets[1].Start).To(Equal(2*time.Minute + 8*time.Second))
			Expect(sheets[1].End).To(Equal(2*time.Minute + 10*time.Second))

			images := FrameGridImageBytes(sheets)
			Expect(images).To(HaveLen(2))
		})

		It("keeps reanalysis frame grids at or above the minimum FPS and refuses longer intervals", func() {
			// 12 sheets x 32 frames = 384 frames: 192s is the longest interval at 2 FPS.
			Expect(frameGridEligible(192*time.Second, FrameGridOptions{})).To(BeTrue())
			Expect(frameGridEligible(193*time.Second, FrameGridOptions{})).To(BeFalse())
			Expect(frameGridEligible(0, FrameGridOptions{})).To(BeFalse())

			w := &Worker{GeminiClient: &gemini.Client{}}
			_, sheets, usage, err := w.AnalyzeVideoIntervalWithFrameGrids(context.Background(), "unused.mp4", 0, 5*time.Minute, "prompt", gemini.ModelArgon4, FrameGridOptions{})
			Expect(err).To(MatchError(errFrameGridIntervalTooLong))
			Expect(sheets).To(BeEmpty(), "no sheets lets callers fall back to VideoMetadata analysis")
			Expect(usage).To(BeNil())
		})

		It("adaptively reduces FPS and caps sheet count at MaxSheets for long intervals", func() {
			opts := resolveFrameGridOptionsForDuration(240*time.Second, FrameGridOptions{})
			Expect(opts.MaxSheets).To(Equal(12))
			Expect(opts.FPS).To(BeNumerically("~", 1.6, 0.001))

			frames := make([]image.Image, 100)
			for i := range frames {
				frames[i] = makeSolidFrame(80, 60, color.RGBA{R: 100, G: 100, B: 100, A: 255})
			}
			sheets, err := composeFrameGridImages(frames, 0, 50*time.Second, FrameGridOptions{
				Cols:      4,
				Rows:      4,
				MaxSheets: 2, // max 32 frames across 2 sheets
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(sheets).To(HaveLen(2))
			Expect(sheets[0].FrameCount).To(Equal(16))
			Expect(sheets[1].FrameCount).To(Equal(16))
		})
	})

	Describe("GenerateFrameGrids and AnalyzeVideoIntervalWithFrameGrids", func() {
		It("extracts multi-sheet frame grids with ffmpeg and sends them to Gemini inline", func() {
			if !hasFfmpeg() {
				Skip("ffmpeg is required for GenerateFrameGrids integration test")
			}

			tmpDir := GinkgoT().TempDir()
			videoPath := filepath.Join(tmpDir, "sample.mp4")
			cmd := exec.Command(
				"ffmpeg", "-v", "error",
				"-f", "lavfi", "-i", "testsrc2=size=320x200:rate=20",
				"-t", "10.5",
				"-c:v", "libx264", "-pix_fmt", "yuv420p",
				"-y", videoPath,
			)
			output, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "ffmpeg test video creation failed: %s", string(output))

			const (
				baseURL = "https://example.test"
				apiKey  = "test-api-key"
			)
			transport := testhelpers.NewMockTransport()
			transport.New(baseURL).
				Post("/v1beta/models/"+gemini.ModelArgon4+":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{
								{"text": `{"exercise_type":"Burpee Over Bar","output":"4 reps verified from frame grid"}`},
							},
						},
					}},
					"usageMetadata": map[string]any{
						"promptTokenCount":     2240,
						"candidatesTokenCount": 90,
						"totalTokenCount":      2330,
					},
				})

			client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				Model:      gemini.ModelArgon4,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			w := &Worker{
				GeminiClient: client,
				logger:       zap.NewNop(),
			}

			// 9.5 seconds at 4 FPS = 38 frames -> 2 sheets (32 frames + 6 frames)
			analysis, sheets, usage, err := w.AnalyzeVideoIntervalWithFrameGrids(
				context.Background(),
				videoPath,
				500*time.Millisecond,
				10*time.Second,
				"Analyze the workout interval.",
				gemini.ModelArgon4,
				FrameGridOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(sheets).To(HaveLen(2))
			Expect(sheets[0].FrameCount).To(Equal(32))
			Expect(sheets[1].FrameCount).To(BeNumerically(">", 0))
			Expect(analysis).To(ContainSubstring("Burpee Over Bar"))
			Expect(usage).NotTo(BeNil())
			Expect(usage.TotalTokens).To(Equal(int32(2330)))
			Expect(transport.Verify()).To(Succeed())

			reqBody := string(transport.Requests()[0].Body)
			Expect(reqBody).To(ContainSubstring("00:00:00.500"))
			Expect(reqBody).To(ContainSubstring("MEDIA_RESOLUTION_HIGH"))
		})
	})
})
