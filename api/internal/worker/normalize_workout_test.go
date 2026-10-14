package worker

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("normalize workout", func() {
	It("parses normalized workout output correctly", func() {
		raw := "Here is the parsed result:\n\n```movements\n{\n  \"movements\": [\n    {\n      \"movement\": \"power clean\",\n      \"weight_raw\": \"135lb\",\n      \"weight_kg\": 61.2,\n      \"reps\": \"5\",\n      \"is_main\": true\n    }\n  ]\n}\n```\nHope that helps!"

		movements, err := ParseNormalizedWorkoutOutput(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(movements).To(HaveLen(1))

		m := movements[0]
		Expect(m.Movement).To(Equal("power clean"))
		Expect(m.WeightRaw).To(Equal("135lb"))
		Expect(m.WeightKG).NotTo(BeNil())
		Expect(*m.WeightKG).To(Equal(61.2))
		Expect(m.Reps).To(Equal("5"))
		Expect(m.IsMain).To(BeTrue())
	})

	It("builds non-empty normalized workout prompt", func() {
		prompt := BuildNormalizedWorkoutPrompt("5x5 Power Clean 135lb", []string{"Power Clean"}, "male")
		Expect(prompt).NotTo(BeEmpty())
	})

	It("forwards token usage to the optional observe callback when normalizing workout", func() {
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:     "test-api-key",
			BaseURL:    "https://normalize-workout.test",
			HTTPClient: &http.Client{Transport: transport},
		})
		Expect(err).NotTo(HaveOccurred())

		transport.New("https://normalize-workout.test").
			Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").
			MatchBodyContains("weight_raw").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{"parts": []map[string]any{{
						"text": "```movements\n{\"movements\":[{\"movement\":\"Pull-up\",\"reps\":\"10\",\"is_main\":true}]}\n```",
					}}},
				}},
				"usageMetadata": map[string]any{
					"promptTokenCount":     31,
					"candidatesTokenCount": 14,
					"totalTokenCount":      45,
				},
			})

		w := &Worker{GeminiClient: client, logger: zap.NewNop()}
		var observedTask string
		var observedUsage *gemini.TokenUsage
		out := w.normalizeWorkout(context.Background(), "WOD-20261014-01JTEST", 1, "10 Pull-ups", nil, func(task string, usage *gemini.TokenUsage) {
			observedTask = task
			observedUsage = usage
		})
		Expect(transport.Verify()).To(Succeed())
		Expect(out).To(ContainSubstring("Pull-up"))
		Expect(observedTask).To(Equal("session:normalize-workout"))
		Expect(observedUsage).NotTo(BeNil())
		Expect(observedUsage.PromptTokens).To(Equal(int32(31)))
		Expect(observedUsage.CandidateTokens).To(Equal(int32(14)))
		Expect(observedUsage.TotalTokens).To(Equal(int32(45)))
	})
})
