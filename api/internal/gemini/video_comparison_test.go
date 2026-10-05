package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"google.golang.org/genai"
)

// Delayed body consumption exercises timing/cancellation after HTTP headers.
// Requests still go through the shared MockTransport and the real SDK client.
type comparisonDelayedBodyTransport struct {
	base  http.RoundTripper
	delay time.Duration
}

func (t comparisonDelayedBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &comparisonDelayedBody{ReadCloser: resp.Body, ctx: req.Context(), delay: t.delay}
	return resp, nil
}

type comparisonDelayedBody struct {
	io.ReadCloser
	ctx    context.Context
	delay  time.Duration
	waited bool
}

func (b *comparisonDelayedBody) Read(p []byte) (int, error) {
	if !b.waited {
		b.waited = true
		select {
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		case <-time.After(b.delay):
		}
	}
	return b.ReadCloser.Read(p)
}

var _ = Describe("Video mode comparison", func() {
	const base = "https://example.test"
	const path = "/v1beta/models/" + ModelFlash38 + ":generateContent"
	var transport *testhelpers.MockTransport
	newClient := func(rt http.RoundTripper) *Client {
		c, err := NewComparisonClient(context.Background(), Options{APIKey: "secret-key", BaseURL: base, HTTPClient: &http.Client{Transport: rt}, ThinkingLevel: "HIGH"})
		Expect(err).NotTo(HaveOccurred())
		return c
	}
	BeforeEach(func() { transport = testhelpers.NewMockTransport() })
	AfterEach(func() { Expect(transport.Verify()).To(Succeed()) })

	It("changes only processing mode and preserves raw response including new fields", func() {
		body := []byte(`{ "candidates":[{"content":{"parts":[{"thought":true,"text":"hidden"},{"toolCall":{"toolType":"MEDIA_PROCESSING"}},{"toolResponse":{"toolType":"MEDIA_PROCESSING"}},{"text":"[]"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0,"totalTokenCount":19},"futureField":{"kept":true} }`)
		for _, mode := range []string{"STATIC", "AGENTIC"} {
			transport.New(base).Post(path).MatchBodyContains(`"mediaProcessing":"` + mode + `"`).Reply(200).Body(body)
		}
		c := newClient(transport)
		a := c.CompareVideoMode(context.Background(), base+"/files/same", "video/mp4", "same prompt", genai.MediaProcessingStatic)
		b := c.CompareVideoMode(context.Background(), base+"/files/same", "video/mp4", "same prompt", genai.MediaProcessingAgentic)
		Expect(a.Outcome).To(Equal("completed"))
		Expect(b.Response).To(Equal(body))
		Expect(b.Text).To(Equal("[]"))
		Expect(b.AgenticObserved).To(BeTrue())
		Expect(a.AgenticObserved).To(BeFalse())
		Expect(b.Usage.Input).NotTo(BeNil())
		Expect(*b.Usage.Input).To(Equal(int64(0)))
		Expect(b.Usage.Thinking).To(BeNil())
		Expect(b.HTTPAttempts).To(Equal(1))
		Expect(string(b.Request)).NotTo(ContainSubstring("secret-key"))
		Expect(string(b.Request)).NotTo(ContainSubstring("videoMetadata"))
		Expect(strings.ReplaceAll(string(a.Request), "STATIC", "AGENTIC")).To(Equal(string(b.Request)))
	})

	It("does not infer agentic execution from HTTP success alone", func() {
		transport.New(base).Post(path).Reply(200).Body([]byte(`{"candidates":[{"content":{"parts":[{"text":"[]"}]},"finishReason":"STOP"}]}`))
		r := newClient(transport).CompareVideoMode(context.Background(), base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic)
		Expect(r.Outcome).To(Equal("completed"))
		Expect(r.AgenticObserved).To(BeFalse())
		Expect(r.Usage).To(BeNil())
	})

	DescribeTable("preserves failures without retrying", func(status int, body string, outcome string) {
		transport.New(base).Post(path).Reply(status).Body([]byte(body))
		r := newClient(transport).CompareVideoMode(context.Background(), base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingStatic)
		Expect(r.Outcome).To(Equal(outcome))
		Expect(r.Response).To(Equal([]byte(body)))
		Expect(r.HTTPAttempts).To(Equal(1))
		Expect(transport.Requests()).To(HaveLen(1))
	},
		Entry("transient server error", 503, `{"error":{"code":503,"message":"temporary","status":"UNAVAILABLE"}}`, "failed"),
		Entry("empty candidates", 200, `{"candidates":[]}`, "failed"),
		Entry("empty body", 200, ``, "failed"),
		Entry("invalid body", 200, `not json`, "failed"),
		Entry("output truncated", 200, `{"candidates":[{"content":{"parts":[{"text":"["}]},"finishReason":"MAX_TOKENS"}]}`, "incomplete"),
	)

	It("includes body transfer in latency", func() {
		transport.New(base).Post(path).Reply(200).Body([]byte(`{"candidates":[]}`))
		r := newClient(comparisonDelayedBodyTransport{base: transport, delay: 25 * time.Millisecond}).CompareVideoMode(context.Background(), base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingStatic)
		Expect(r.ElapsedSeconds).To(BeNumerically(">=", 0.025))
	})

	It("preserves status and elapsed time when response consumption times out", func() {
		transport.New(base).Post(path).Reply(200).Body([]byte(`{"candidates":[]}`))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		r := newClient(comparisonDelayedBodyTransport{base: transport, delay: time.Second}).CompareVideoMode(ctx, base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic)
		Expect(r.Outcome).To(Equal("failed"))
		Expect(r.Error).To(ContainSubstring("deadline exceeded"))
		Expect(r.HTTPStatus).To(Equal(200))
		Expect(r.ElapsedSeconds).To(BeNumerically(">", 0))
		Expect(r.HTTPAttempts).To(Equal(1))
	})

	It("serializes unknown usage as null", func() {
		data, err := json.Marshal(ComparisonUsage{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"thoughtsTokenCount":null`))
	})
})
