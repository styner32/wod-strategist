package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("Generation usage accounting", func() {
	var client *Client
	var transport *testhelpers.MockTransport
	BeforeEach(func() {
		transport = testhelpers.NewMockTransport()
		var err error
		client, err = NewClientWithOptions(context.Background(), zap.NewNop(), Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() { Expect(transport.Verify()).To(Succeed()) })
	It("preserves usage for empty responses, with explicit zero distinct from missing", func() {
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + ModelFlash38 + ":generateContent").Reply(200).Body([]byte(`{"usageMetadata":{"promptTokenCount":0,"thoughtsTokenCount":37,"toolUsePromptTokenCount":400,"cachedContentTokenCount":0,"totalTokenCount":437}}`))
		_, usage, err := client.ParseText(context.Background(), "prompt")
		Expect(err).To(HaveOccurred())
		Expect(usage.Details.Input).To(HaveValue(Equal(int64(0))))
		Expect(usage.Details.Output).To(BeNil())
		Expect(usage.Details.Thinking).To(HaveValue(Equal(int64(37))))
		Expect(usage.Details.ToolUse).To(HaveValue(Equal(int64(400))))
		Expect(usage.Details.Cached).To(HaveValue(Equal(int64(0))))
	})
	It("records an unmeasured request on HTTP error without retrying", func() {
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + ModelFlash38 + ":generateContent").Reply(503).Body([]byte(`{"error":{"code":503,"message":"unavailable"}}`))
		_, usage, err := client.ParseText(context.Background(), "prompt")
		Expect(err).To(HaveOccurred())
		Expect(usage.Model).To(Equal(ModelFlash38))
		Expect(usage.Details).To(BeNil())
	})
	It("keeps music generation usage even when the response has no audio", func() {
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/lyria-3-clip-preview:generateContent").Reply(200).Body([]byte(`{"usageMetadata":{"promptTokenCount":10,"totalTokenCount":10}}`))
		dir, err := os.MkdirTemp("", "music-usage-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(dir)
		usage, err := client.GenerateWorkoutMusic(context.Background(), "lyria-3-clip-preview", "music", filepath.Join(dir, "music.mp3"))
		Expect(err).To(HaveOccurred())
		Expect(usage.Details.Total).To(HaveValue(Equal(int64(10))))
	})
	It("requests thought summaries when IncludeThoughts is enabled, separates thought parts from answer text, and omits ThoughtSummary in JSON", func() {
		thoughtClient, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
			APIKey:          "test",
			HTTPClient:      &http.Client{Transport: transport},
			ThinkingLevel:   "MEDIUM",
			IncludeThoughts: true,
		})
		Expect(err).NotTo(HaveOccurred())

		replyBody := []byte(`{"candidates":[{"content":{"parts":[{"thought":true,"text":"checking frame 12.50s"},{"text":"clean answer"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":12,"thoughtsTokenCount":250,"totalTokenCount":362}}`)
		transport.New("https://generativelanguage.googleapis.com").
			Post("/v1beta/models/" + ModelFlash38 + ":generateContent").
			MatchBodyContains(`"includeThoughts":true`).
			Reply(200).
			Body(replyBody)
		text, usage, err := thoughtClient.ParseText(context.Background(), "prompt")
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal("clean answer"))
		Expect(usage.ThoughtSummary).To(Equal("checking frame 12.50s"))
		Expect(usage.Details.Thinking).To(HaveValue(Equal(int64(250))))
		encodedUsage, err := json.Marshal(usage)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encodedUsage)).NotTo(ContainSubstring("checking frame 12.50s"))

		transport.New("https://generativelanguage.googleapis.com").
			Post("/v1beta/models/" + ModelFlash38 + ":generateContent").
			MatchBodyContains(`"includeThoughts":true`).
			Reply(200).
			Body(replyBody)
		gridText, gridUsage, err := thoughtClient.AnalyzeFrameGridsWithModel(
			context.Background(), [][]byte{[]byte("fake-jpeg")}, "grid prompt", "MEDIUM", ModelFlash38,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(gridText).To(Equal("clean answer"))
		Expect(gridUsage.ThoughtSummary).To(Equal("checking frame 12.50s"))

		transport.New("https://generativelanguage.googleapis.com").
			Post("/v1beta/models/" + ModelFlash38 + ":generateContent").
			MatchBodyContains(`"includeThoughts":true`).
			Reply(200).
			Body(replyBody)
		segText, segUsage, err := thoughtClient.AnalyzeSegmentWithModel(
			context.Background(), "https://generativelanguage.googleapis.com/files/f1", "video/mp4", 0, 10*time.Second, "seg prompt", ModelFlash38,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(segText).To(Equal("clean answer"))
		Expect(segUsage.ThoughtSummary).To(Equal("checking frame 12.50s"))
	})
})
