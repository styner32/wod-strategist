package gemini

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("Retry-After and HTTP status extraction", func() {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	DescribeTable("parses Retry-After header values",
		func(raw string, wantDur time.Duration, wantOK bool) {
			got, ok := ParseRetryAfterHeader(raw, now)
			Expect(ok).To(Equal(wantOK))
			if wantOK {
				Expect(got).To(BeNumerically("~", wantDur, time.Millisecond))
			}
		},
		Entry("integer seconds", "7", 7*time.Second, true),
		Entry("zero seconds returns non-positive", "0", time.Duration(0), false),
		Entry("future HTTP-date", now.Add(15*time.Second).UTC().Format(http.TimeFormat), 15*time.Second, true),
		Entry("past HTTP-date returns non-positive", now.Add(-10*time.Second).UTC().Format(http.TimeFormat), time.Duration(0), false),
		Entry("empty header", "", time.Duration(0), false),
		Entry("negative seconds", "-3", time.Duration(0), false),
		Entry("malformed value", "not-a-duration", time.Duration(0), false),
	)

	It("surfaces HTTPStatusCode and RetryAfterDuration from real Gemini client APIError", func() {
		const baseURL = "https://example.test"
		transport := testhelpers.NewMockTransport()
		transport.New(baseURL).
			Post("/v1beta/models/"+ModelHydrogen4+":generateContent").
			Reply(http.StatusTooManyRequests).
			Header("Retry-After", "9").
			JSON(map[string]any{
				"error": map[string]any{
					"code":    429,
					"message": "Resource exhausted",
					"status":  "RESOURCE_EXHAUSTED",
				},
			})
		transport.New(baseURL).
			Post("/v1beta/models/" + ModelHydrogen4 + ":generateContent").
			Reply(http.StatusServiceUnavailable).
			JSON(map[string]any{
				"error": map[string]any{
					"code":    503,
					"message": "Backend unavailable",
					"status":  "UNAVAILABLE",
				},
			})

		client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
			APIKey:     "test-api-key",
			BaseURL:    baseURL,
			Model:      ModelHydrogen4,
			HTTPClient: &http.Client{Transport: transport},
			Sleep:      func(time.Duration) {},
		})
		Expect(err).NotTo(HaveOccurred())

		_, _, err429 := client.ParseText(context.Background(), "hello")
		Expect(err429).To(HaveOccurred())
		code429, has429 := HTTPStatusCode(err429)
		Expect(has429).To(BeTrue())
		Expect(code429).To(Equal(http.StatusTooManyRequests))
		dur, ok := RetryAfterDuration(err429)
		Expect(ok).To(BeTrue())
		Expect(dur).To(Equal(9 * time.Second))

		_, _, err503 := client.ParseText(context.Background(), "hello")
		Expect(err503).To(HaveOccurred())
		code503, has503 := HTTPStatusCode(err503)
		Expect(has503).To(BeTrue())
		Expect(code503).To(Equal(http.StatusServiceUnavailable))
		_, ok = RetryAfterDuration(err503)
		Expect(ok).To(BeFalse())

		Expect(transport.Verify()).To(Succeed())
	})
})
