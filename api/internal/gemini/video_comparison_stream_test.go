package gemini

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"google.golang.org/genai"
)

// Gate the second HTTP body read on consumer progress (or cancellation). This
// detects accidental ReadAll buffering before the SDK yields the first event.
type gatedStreamTransport struct {
	base  http.RoundTripper
	gate  <-chan struct{}
	first int
}

func (t gatedStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	r.Body = &gatedStreamBody{ReadCloser: r.Body, ctx: req.Context(), gate: t.gate, left: t.first}
	return r, nil
}

type gatedStreamBody struct {
	io.ReadCloser
	ctx  context.Context
	gate <-chan struct{}
	left int
}

func (b *gatedStreamBody) Read(p []byte) (int, error) {
	if b.left == 0 {
		select {
		case <-b.gate:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	} else if len(p) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	if b.left > 0 {
		b.left -= n
	}
	return n, err
}

var _ = Describe("Streaming video comparison", func() {
	const base = "https://example.test"
	const path = "/v1beta/models/" + ModelFlash38 + ":streamGenerateContent?alt=sse"
	const first = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"toolCall\":{\"toolType\":\"MEDIA_PROCESSING\"},\"text\":\"private thought\"}]}}]}\n\n"
	const last = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"toolResponse\":{\"toolType\":\"MEDIA_PROCESSING\"}},{\"text\":\"[]\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":0,\"totalTokenCount\":12},\"futureField\":true}\n\n"
	var transport *testhelpers.MockTransport
	newClient := func(rt http.RoundTripper) *Client {
		c, err := NewComparisonClient(context.Background(), Options{APIKey: "test-key", BaseURL: base, HTTPClient: &http.Client{Transport: rt}, ThinkingLevel: "HIGH"})
		Expect(err).NotTo(HaveOccurred())
		return c
	}
	BeforeEach(func() { transport = testhelpers.NewMockTransport() })
	AfterEach(func() { Expect(transport.Verify()).To(Succeed()) })

	It("consumes incrementally and preserves original SSE and nullable usage", func() {
		body := first + last
		transport.New(base).Post(path).MatchBodyContains(`"mediaProcessing":"AGENTIC"`).Reply(200).Header("Content-Type", "text/event-stream").Body([]byte(body))
		gate := make(chan struct{})
		var raw bytes.Buffer
		var events []StreamProgress
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r := newClient(gatedStreamTransport{base: transport, gate: gate, first: len(first)}).CompareVideoModeStream(ctx, base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic, StreamComparisonOptions{RawResponse: &raw, OnEvent: func(p StreamProgress) {
			events = append(events, p)
			if p.Event == 1 {
				close(gate)
			}
		}})
		Expect(r.Outcome).To(Equal("completed"))
		Expect(r.BodyEOF).To(BeTrue())
		Expect(r.EventCount).To(Equal(2))
		Expect(events[0].ToolCalls).To(Equal(1))
		Expect(r.Response).To(Equal([]byte(body)))
		Expect(raw.String()).To(Equal(body))
		Expect(r.AgenticObserved).To(BeTrue())
		Expect(r.Text).To(Equal("[]"))
		Expect(r.Usage.Input).NotTo(BeNil())
		Expect(*r.Usage.Input).To(BeZero())
		Expect(r.Usage.Cached).To(BeNil())
		Expect(r.FirstEventSeconds).NotTo(BeNil())
		Expect(r.HTTPAttempts).To(Equal(1))
		Expect(string(r.Request)).To(ContainSubstring(`"thinkingLevel":"HIGH"`))
		Expect(string(r.Request)).To(ContainSubstring(`"mediaResolution":"MEDIA_RESOLUTION_HIGH"`))
		Expect(string(r.Request)).NotTo(ContainSubstring("videoMetadata"))
	})

	It("can lower media resolution while keeping HIGH thinking and Agentic processing", func() {
		transport.New(base).Post(path).MatchBodyContains(`"mediaResolution":"MEDIA_RESOLUTION_LOW"`).Reply(200).Header("Content-Type", "text/event-stream").Body([]byte(last))
		r := newClient(transport).CompareVideoModeStream(context.Background(), base+"/files/f", "video/mp4", "DB windows only", genai.MediaProcessingAgentic, StreamComparisonOptions{MediaResolution: genai.MediaResolutionLow})
		Expect(r.Outcome).To(Equal("completed"))
		Expect(string(r.Request)).To(ContainSubstring(`"thinkingLevel":"HIGH"`))
		Expect(string(r.Request)).To(ContainSubstring(`"mediaProcessing":"AGENTIC"`))
		Expect(string(r.Request)).NotTo(ContainSubstring("videoMetadata"))
		Expect(r.HTTPAttempts).To(Equal(1))
	})

	It("retains partial tool evidence on timeout instead of returning nil", func() {
		transport.New(base).Post(path).Reply(200).Body([]byte(first + last))
		gate := make(chan struct{})
		var raw bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		r := newClient(gatedStreamTransport{base: transport, gate: gate, first: len(first)}).CompareVideoModeStream(ctx, base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic, StreamComparisonOptions{RawResponse: &raw})
		Expect(r.Outcome).To(Equal("failed"))
		Expect(r.Error).To(ContainSubstring("deadline exceeded"))
		Expect(r.MediaToolCalls).To(Equal(1))
		Expect(r.MediaToolResponses).To(BeZero())
		Expect(r.AgenticObserved).To(BeFalse())
		Expect(r.EventCount).To(Equal(1))
		Expect(r.BodyEOF).To(BeFalse())
		Expect(raw.String()).To(Equal(first))
	})

	It("records ID-only tool events without asserting MEDIA_PROCESSING", func() {
		body := strings.ReplaceAll(first+last, `"toolType":"MEDIA_PROCESSING"`, `"id":"call_507536"`)
		transport.New(base).Post(path).Reply(200).Body([]byte(body))
		var events []StreamProgress
		r := newClient(transport).CompareVideoModeStream(context.Background(), base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic, StreamComparisonOptions{OnEvent: func(p StreamProgress) { events = append(events, p) }})
		Expect(r.Outcome).To(Equal("completed"))
		Expect(r.UntypedToolCalls).To(Equal(1))
		Expect(r.UntypedToolResponses).To(Equal(1))
		Expect(events[0].UntypedToolCalls).To(Equal(1))
		Expect(r.MediaToolCalls).To(BeZero())
		Expect(r.AgenticObserved).To(BeFalse())
	})
	It("preserves a terminal tool response in an additional unindexed candidate", func() {
		body := "data: " + `{"candidates":[{"index":0,"finishReason":"TOO_MANY_TOOL_CALLS","content":{"parts":[{"toolCall":{"id":"terminal"}},{"text":""}]}},{"content":{"parts":[{"toolResponse":{"id":"terminal"}}]}}]}` + "\n\n"
		transport.New(base).Post(path).Reply(200).Body([]byte(body))
		var progress StreamProgress
		r := newClient(transport).CompareVideoModeStream(context.Background(), base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic, StreamComparisonOptions{OnEvent: func(p StreamProgress) { progress = p }})
		Expect(r.FinishReason).To(Equal("TOO_MANY_TOOL_CALLS"))
		Expect(r.BodyEOF).To(BeTrue())
		Expect(r.Outcome).To(Equal("failed"))
		Expect(r.UntypedToolCalls).To(Equal(1))
		Expect(r.UntypedToolResponses).To(Equal(1))
		Expect(progress.UntypedToolResponses).To(Equal(1))
	})

	DescribeTable("does not turn empty or failed streams into success", func(status int, body, outcome string) {
		transport.New(base).Post(path).Reply(status).Body([]byte(body))
		r := newClient(transport).CompareVideoModeStream(context.Background(), base+"/files/f", "video/mp4", "prompt", genai.MediaProcessingAgentic, StreamComparisonOptions{})
		Expect(r.Outcome).To(Equal(outcome))
		Expect(r.Response).To(Equal([]byte(body)))
		Expect(r.HTTPAttempts).To(Equal(1))
	}, Entry("HTTP error is not retried", 503, `{"error":{"code":503,"message":"busy","status":"UNAVAILABLE"}}`, "failed"),
		Entry("empty stream", 200, "", "failed"), Entry("bad event after tool call", 200, first+"data: broken\n\n", "failed"),
		Entry("missing final STOP", 200, strings.Replace(last, `"STOP"`, `"MAX_TOKENS"`, 1), "incomplete"))
})
