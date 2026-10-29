package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

func requireAdjacentMediaTools() {
	GinkgoHelper()
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			Skip(binary + " is required")
		}
	}
}

func runAdjacentMediaFixture(args ...string) {
	GinkgoHelper()
	args = append([]string{"-v", "error", "-nostdin", "-y"}, args...)
	output, err := exec.Command("ffmpeg", args...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "fixture creation failed: %s", output)
}

func createNarrationSource(hasAudio bool) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "source.mp4")
	args := []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=25"}
	if hasAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo")
	}
	args = append(args, "-t", "3", "-c:v", "libx264", "-g", "250", "-keyint_min", "250", "-sc_threshold", "0", "-bf", "2", "-video_track_timescale", "30000")
	if hasAudio {
		args = append(args, "-c:a", "aac")
	} else {
		args = append(args, "-an")
	}
	runAdjacentMediaFixture(append(args, path)...)
	return path
}

// Read the actual multipart media sent through the real GCS client, so handler
// specs can verify the published file after the worker removes its temp files.
func adjacentUploadedVideos(requests []testhelpers.CapturedRequest) []string {
	GinkgoHelper()
	var paths []string
	for _, request := range requests {
		if request.Method != http.MethodPost || !strings.Contains(request.URL, "/upload/storage/") {
			continue
		}
		_, params, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
		Expect(err).NotTo(HaveOccurred())
		reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			payload, err := io.ReadAll(part)
			Expect(err).NotTo(HaveOccurred())
			if !bytes.Contains(payload[:min(len(payload), 32)], []byte("ftyp")) {
				continue
			}
			path := filepath.Join(GinkgoT().TempDir(), fmt.Sprintf("uploaded-%d.mp4", len(paths)))
			Expect(os.WriteFile(path, payload, 0o600)).To(Succeed())
			paths = append(paths, path)
		}
	}
	return paths
}

var _ = Describe("runFFmpegMixAudio media integrity", func() {
	BeforeEach(requireAdjacentMediaTools)

	DescribeTable("preserves every source video frame and its duration",
		func(hasAudio bool, narrationDuration float64) {
			source := createNarrationSource(hasAudio)
			narration := filepath.Join(GinkgoT().TempDir(), "narration.wav")
			runAdjacentMediaFixture("-f", "lavfi", "-i", "sine=frequency=440:sample_rate=24000", "-t", fmt.Sprintf("%.3f", narrationDuration), narration)
			frames, err := readVideoFrameSignature(context.Background(), source)
			Expect(err).NotTo(HaveOccurred())
			output := filepath.Join(GinkgoT().TempDir(), "mixed.mp4")
			w := &Worker{logger: zap.NewNop()}

			Expect(w.runFFmpegMixAudio(context.Background(), source, narration, output)).To(Succeed())
			Expect(verifyVideoFrameSequence(context.Background(), output, []videoFrameSignature{frames})).To(Succeed())
			Expect(verifyVideoDuration(context.Background(), output, 3, 0.1)).To(Succeed())
			Expect(videoHasAudioStream(context.Background(), output)).To(BeTrue())
		},
		Entry("short narration over a truly audio-free video", false, 0.25),
		Entry("narration longer than an audio-free video", false, 5.0),
		Entry("short narration mixed with original audio", true, 0.25),
	)

	It("rejects an unreadable source before producing a replacement", func() {
		source := filepath.Join(GinkgoT().TempDir(), "damaged.mp4")
		Expect(os.WriteFile(source, []byte("incomplete video"), 0o600)).To(Succeed())
		w := &Worker{logger: zap.NewNop()}
		err := w.runFFmpegMixAudio(context.Background(), source, "unused.wav", filepath.Join(GinkgoT().TempDir(), "mixed.mp4"))
		Expect(err).To(MatchError(ContainSubstring("probe narration source video")))
	})
})

var _ = Describe("HandleGenerateHardSubTask media integrity", func() {
	It("publishes the complete silent-source video when TTS ends early", func() {
		requireAdjacentMediaTools()
		conn, err := testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(conn)
		profile := testhelpers.CreateProfile(conn, &db.Profile{})
		const sessionID = "sess-hardsub-media"
		testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: sessionID, ProfileID: profile.ID, Status: "COMPLETED"})
		testhelpers.CreateChunkAnalysisResult(conn, &db.ChunkAnalysisResult{
			SessionID: sessionID, ProfileID: profile.ID, Status: "COMPLETED", Output: "Keep moving.",
			StartSecs: ptr(0.0), EndSecs: ptr(3.0),
		})
		sourceBytes, err := os.ReadFile(createNarrationSource(false))
		Expect(err).NotTo(HaveOccurred())
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		testhelpers.MockGCSDownloadWithBody(transport, fmt.Sprintf("gs://test-bucket/videos/%d/%s/merged.mp4", profile.ID, sessionID), sourceBytes)
		testhelpers.MockGCSUpload(transport, "test-bucket", "hardsubbed")
		geminiTransport := testhelpers.NewMockTransport()
		const baseURL = "https://generativelanguage.googleapis.com"
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey: "test-api-key", BaseURL: baseURL, HTTPClient: &http.Client{Transport: geminiTransport},
			PollInterval: time.Millisecond, Sleep: func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())
		geminiTransport.New(baseURL).Post("/v1beta/models/" + gemini.ModelFlashTTS31Preview + ":generateContent").
			Reply(http.StatusOK).JSON(map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{{
			"inlineData": map[string]any{"mimeType": "audio/L16;codec=pcm;rate=24000", "data": base64.StdEncoding.EncodeToString(make([]byte, 12000))},
		}}}}}})
		w := &Worker{DB: conn, StorageClient: storageClient, GeminiClient: client, BucketName: "test-bucket", logger: zap.NewNop()}
		task, err := NewGenerateHardSubTask(sessionID, profile.ID, true)
		Expect(err).NotTo(HaveOccurred())

		Expect(w.HandleGenerateHardSubTask(context.Background(), task)).To(Succeed())
		uploads := adjacentUploadedVideos(transport.Requests())
		Expect(uploads).To(HaveLen(1))
		Expect(verifyVideoDuration(context.Background(), uploads[0], 3, 0.1)).To(Succeed())
		timeline, err := probeVideoTimeline(context.Background(), uploads[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.FrameCount).To(Equal(75))
		Expect(transport.Verify()).To(Succeed())
		Expect(geminiTransport.Verify()).To(Succeed())
	})
})
