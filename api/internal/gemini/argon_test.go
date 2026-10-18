package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
	"google.golang.org/genai"
)

// Observes the stdlib HTTP request while the real MockTransport handles it.
type argonRequestObserver func(*http.Request) (*http.Response, error)

func (f argonRequestObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var _ = Describe("Argon generation", func() {
	const baseURL = "https://argon.test"
	var transport *testhelpers.MockTransport
	var client *Client
	BeforeEach(func() {
		transport = testhelpers.NewMockTransport()
		budget := int32(4096)
		var err error
		client, err = NewClientWithOptions(context.Background(), zap.NewNop(), Options{
			APIKey: "test", BaseURL: baseURL, HTTPClient: &http.Client{Transport: transport},
			ThinkingBudget: &budget,
		})
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() { Expect(transport.Verify()).To(Succeed()) })

	It("defaults to HIGH without a budget and preserves exact segment metadata", func() {
		Expect(client.Model()).To(Equal(ModelArgon4))
		transport.New(baseURL).Post("/v1beta/models/" + ModelArgon4 + ":generateContent").Reply(200).
			BodyString(`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"observed movement"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":40,"totalTokenCount":160}}`)
		text, usage, err := client.AnalyzeSegment(context.Background(), baseURL+"/files/video", "video/mp4", 10250*time.Millisecond, 20750*time.Millisecond, "analyze")
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal("observed movement"))
		Expect(usage.Model).To(Equal(ModelArgon4))
		body := string(transport.Requests()[0].Body)
		Expect(body).To(ContainSubstring(`"thinkingLevel":"HIGH"`))
		Expect(body).NotTo(ContainSubstring("thinkingBudget"))
		Expect(body).To(ContainSubstring(`"maxOutputTokens":32768`))
		Expect(body).To(ContainSubstring(`"startOffset":"10.250s"`))
		Expect(body).To(ContainSubstring(`"endOffset":"20.750s"`))
		Expect(body).To(ContainSubstring(`"fps":5`))
		Expect(body).To(ContainSubstring(`"mediaResolution":"MEDIA_RESOLUTION_MEDIUM"`))
		Expect(usage.Details.Thinking).To(HaveValue(Equal(int64(40))))
	})

	DescribeTable("rejects incomplete and empty answers while retaining usage",
		func(answer, message string) {
			transport.New(baseURL).Post("/v1beta/models/" + ModelArgon4 + ":generateContent").Reply(200).BodyString(answer)
			text, usage, err := client.AnalyzeSegment(context.Background(), baseURL+"/files/video", "video/mp4", 0, time.Second, "analyze")
			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(text).To(BeEmpty())
			Expect(usage.Model).To(Equal(ModelArgon4))
			Expect(usage.Details.Total).To(HaveValue(Equal(int64(32768))))
			Expect(transport.Requests()).To(HaveLen(1))
		},
		Entry("truncated text", `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"partial observation"}]}}],"usageMetadata":{"thoughtsTokenCount":32760,"totalTokenCount":32768}}`, "MAX_TOKENS"),
		Entry("thinking only", `{"candidates":[{"finishReason":"MAX_TOKENS"}],"usageMetadata":{"thoughtsTokenCount":32768,"totalTokenCount":32768}}`, "MAX_TOKENS"),
		Entry("continuation without text", `{"continuationToken":"checkpoint","usageMetadata":{"thoughtsTokenCount":32768,"totalTokenCount":32768}}`, "continuation is required"),
		Entry("empty completed answer", `{"candidates":[],"usageMetadata":{"totalTokenCount":32768}}`, "no content"),
	)

	It("bounds the generation deadline to 20 minutes and respects an earlier caller deadline", func() {
		for _, duration := range []time.Duration{time.Hour, time.Minute} {
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			callerDeadline, _ := ctx.Deadline()
			client.client.ClientConfig().HTTPClient.Transport = argonRequestObserver(func(req *http.Request) (*http.Response, error) {
				deadline, ok := req.Context().Deadline()
				Expect(ok).To(BeTrue())
				remaining := time.Until(deadline)
				if duration == time.Hour {
					Expect(remaining).To(BeNumerically(">", 19*time.Minute))
					Expect(remaining).To(BeNumerically("<=", 20*time.Minute))
				} else {
					Expect(deadline).To(Equal(callerDeadline))
				}
				return transport.RoundTrip(req)
			})
			transport.New(baseURL).Post("/v1beta/models/" + ModelArgon4 + ":generateContent").Reply(200).
				BodyString(`{"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`)
			_, _, err := client.AnalyzeSegment(ctx, baseURL+"/files/video", "video/mp4", 0, time.Second, "analyze")
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("returns a provider timeout with missing usage and no hidden retry", func() {
		transport.New(baseURL).Post("/v1beta/models/" + ModelArgon4 + ":generateContent").Reply(504).
			BodyString(`{"error":{"code":504,"message":"deadline exceeded","status":"DEADLINE_EXCEEDED"}}`)
		text, usage, err := client.AnalyzeSegment(context.Background(), baseURL+"/files/video", "video/mp4", 0, time.Second, "analyze")
		Expect(err).To(HaveOccurred())
		Expect(text).To(BeEmpty())
		Expect(usage.Model).To(Equal(ModelArgon4))
		Expect(usage.Details).To(BeNil())
		Expect(transport.Requests()).To(HaveLen(1))
	})

	It("keeps indexing and text parsing on Flash after changing the client default", func() {
		for range 2 {
			transport.New(baseURL).Post("/v1beta/models/" + ModelFlash38 + ":generateContent").Reply(200).
				BodyString(`{"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`)
		}
		_, usage, err := client.IndexVideo(context.Background(), baseURL+"/files/video", "video/mp4", "index")
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.Model).To(Equal(ModelFlash38))
		_, usage, err = client.ParseText(context.Background(), "summarize")
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.Model).To(Equal(ModelFlash38))
		for _, request := range transport.Requests() {
			var body map[string]any
			Expect(json.Unmarshal(request.Body, &body)).To(Succeed())
			config, _ := body["generationConfig"].(map[string]any)
			Expect(config).NotTo(HaveKey("maxOutputTokens"))
		}
	})

	It("retains explicit legacy Pro requests for comparison and rollback", func() {
		transport.New(baseURL).Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").Reply(200).
			BodyString(`{"candidates":[{"content":{"parts":[{"text":"legacy answer"}]}}]}`)
		text, usage, err := client.AnalyzeSegmentWithModel(context.Background(), baseURL+"/files/video", "video/mp4", 0, time.Second, "analyze", ModelPro31Preview)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal("legacy answer"))
		Expect(usage.Model).To(Equal(ModelPro31Preview))
		Expect(string(transport.Requests()[0].Body)).NotTo(ContainSubstring("maxOutputTokens"))
	})

	It("uses HIGH thinking when analyzing segments with Flash 3.8", func() {
		transport.New(baseURL).Post("/v1beta/models/" + ModelFlash38 + ":generateContent").Reply(200).
			BodyString(`{"candidates":[{"content":{"parts":[{"text":"unknown segment answer"}]}}]}`)
		text, usage, err := client.AnalyzeSegmentWithModel(context.Background(), baseURL+"/files/video", "video/mp4", 45*time.Second, 50*time.Second, "analyze unknown", ModelFlash38)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal("unknown segment answer"))
		Expect(usage.Model).To(Equal(ModelFlash38))
		body := string(transport.Requests()[0].Body)
		Expect(body).To(ContainSubstring(`"thinkingLevel":"HIGH"`))
		Expect(body).To(ContainSubstring(`"mediaResolution":"MEDIA_RESOLUTION_MEDIUM"`))
	})

	It("honors an explicit thinking level while stripping the unsupported budget", func() {
		config := &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevelMedium, ThinkingBudget: genai.Ptr(int32(4096)),
		}}
		transport.New(baseURL).Post("/v1beta/models/" + ModelArgon4 + ":generateContent").Reply(200).
			BodyString(`{"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`)
		_, _, err := client.generateContent(context.Background(), ModelArgon4, genai.Text("analyze"), config)
		Expect(err).NotTo(HaveOccurred())
		body := string(transport.Requests()[0].Body)
		Expect(body).To(ContainSubstring(`"thinkingLevel":"MEDIUM"`))
		Expect(body).NotTo(ContainSubstring("thinkingBudget"))
		Expect(config.ThinkingConfig.ThinkingBudget).NotTo(BeNil(), "do not mutate caller configuration")
		Expect(config.MaxOutputTokens).To(BeZero())
	})
})
