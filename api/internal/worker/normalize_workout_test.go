package worker

import (
	"context"
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
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

	It("promotes distance-prescribed lunges to * Walking Lunge without altering rep-based lunges", func() {
		rawDistance := "```movements\n{\n  \"movements\": [\n    {\n      \"movement\": \"Dumbbell Overhead Lunge\",\n      \"weight_raw\": \"50lb\",\n      \"weight_kg\": 22.7,\n      \"reps\": \"200ft\",\n      \"is_main\": true\n    },\n    {\n      \"movement\": \"Dumbbell Overhead Lunge\",\n      \"weight_raw\": \"50lb\",\n      \"weight_kg\": 22.7,\n      \"reps\": \"20\",\n      \"is_main\": false\n    }\n  ]\n}\n```"
		movements, err := ParseNormalizedWorkoutOutput(rawDistance)
		Expect(err).NotTo(HaveOccurred())
		Expect(movements).To(HaveLen(2))
		Expect(movements[0].Movement).To(Equal("DB Overhead Walking Lunge"))
		Expect(movements[1].Movement).To(Equal("Dumbbell Overhead Lunge"))
	})

	It("splits combined single-line workouts like '400m Run / 20 DB Lunges' and keeps stationary lunges unaffected by Run distance", func() {
		rawCombined := "```movements\n{\n  \"movements\": [\n    {\n      \"movement\": \"400m Run / 20 DB Lunges (50/35lb)\",\n      \"weight_raw\": \"\",\n      \"weight_kg\": null,\n      \"reps\": \"\",\n      \"is_main\": true\n    }\n  ]\n}\n```"
		movements, err := ParseNormalizedWorkoutOutput(rawCombined)
		Expect(err).NotTo(HaveOccurred())
		Expect(movements).To(HaveLen(2))
		Expect(movements[0].Movement).To(Equal("Run"))
		Expect(movements[0].Reps).To(Equal("400m"))
		Expect(movements[0].WeightRaw).To(BeEmpty())
		Expect(movements[0].IsMain).To(BeFalse())
		Expect(movements[1].Movement).To(Equal("DB Lunge"))
		Expect(movements[1].Reps).To(Equal("20"))
		Expect(movements[1].WeightRaw).To(Equal("50/35lb"))
		Expect(movements[1].IsMain).To(BeTrue())

		promoteDistanceLungesInNormalizedMovements(movements, "3 Rounds For Time: 400m Run / 20 DB Lunges (50/35lb)")
		Expect(movements[0].Movement).To(Equal("Run"))
		Expect(movements[1].Movement).To(Equal("DB Lunge"))
	})

	It("splits combined single-line workouts and promotes when the lunge clause itself has a distance", func() {
		rawCombinedDist := "```movements\n{\n  \"movements\": [\n    {\n      \"movement\": \"400m Run / 200ft DB Overhead Lunges\",\n      \"weight_raw\": \"50lb\",\n      \"weight_kg\": 22.7,\n      \"reps\": \"\",\n      \"is_main\": true\n    }\n  ]\n}\n```"
		movements, err := ParseNormalizedWorkoutOutput(rawCombinedDist)
		Expect(err).NotTo(HaveOccurred())
		Expect(movements).To(HaveLen(2))
		Expect(movements[0].Movement).To(Equal("Run"))
		Expect(movements[0].Reps).To(Equal("400m"))
		Expect(movements[0].WeightRaw).To(BeEmpty())
		Expect(movements[0].IsMain).To(BeFalse())
		Expect(movements[1].Movement).To(Equal("DB Overhead Walking Lunge"))
		Expect(movements[1].Reps).To(Equal("200ft"))
		Expect(movements[1].WeightRaw).To(Equal("50lb"))
		Expect(movements[1].IsMain).To(BeTrue())
	})

	It("uses structured lunge prescriptions before WOD fallback", func() {
		items := []db.NormalizedMovement{
			{Movement: "DB Lunge", Reps: "20"},
			{Movement: "DB Lunge", Reps: "21-15-9"},
			{Movement: "DB Lunge", Reps: "200/150ft"},
			{Movement: "DB Lunge"},
		}
		promoteDistanceLungesInNormalizedMovements(items, "200ft DB Lunge")
		Expect(items[0].Movement).To(Equal("DB Lunge"))
		Expect(items[1].Movement).To(Equal("DB Lunge"))
		Expect(items[2].Movement).To(Equal("DB Walking Lunge"))
		Expect(items[3].Movement).To(Equal("DB Walking Lunge"))
	})

	It("preserves explicit structured counts while parsing inline lunge distances", func() {
		input := []db.NormalizedMovement{
			{Movement: "DB Lunge:20m", Reps: "20"},
			{Movement: "200ft DB Lunge", Reps: "20"},
			{Movement: "DB Walking Lunge:20m", Reps: "20"},
			{Movement: "20 DB Lunge", Reps: "200ft"},
		}
		raw, err := json.Marshal(map[string]any{"movements": input})
		Expect(err).NotTo(HaveOccurred())
		items, err := ParseNormalizedWorkoutOutput(string(raw))
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(4))
		Expect(items[0].Movement).To(Equal("DB Lunge"))
		Expect(items[1].Movement).To(Equal("DB Lunge"))
		Expect(items[2].Movement).To(Equal("DB Walking Lunge"))
		Expect(items[3].Movement).To(Equal("DB Walking Lunge"))
		for i := range items {
			Expect(items[i].Reps).To(Equal(input[i].Reps))
		}
	})

	DescribeTable("preserves normalized local lunge quantities and load ratios",
		func(clause, expectedReps string) {
			items := splitCombinedNormalizedMovements([]db.NormalizedMovement{{Movement: clause, IsMain: true}})
			Expect(items).To(HaveLen(1))
			Expect(items[0].Movement).To(Equal("DB Walking Lunge"))
			Expect(items[0].Reps).To(Equal(expectedReps))
			Expect(items[0].WeightRaw).To(Equal("50/35lb"))
			Expect(items[0].WeightKG).To(BeNil())
			Expect(items[0].IsMain).To(BeTrue())
		},
		Entry("movement colon distance", "DB Lunge:20m (50/35lb)", "20m"),
		Entry("parenthesized distance", "DB Lunge (20m) (50/35lb)", "20m"),
		Entry("distance ratio is not a weight ratio", "DB Lunge (200/150ft) (50/35lb)", "200/150ft"),
	)

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

	It("reuses existing NormalizedWorkout from analysis_results without calling Gemini when WODDescription is unchanged", func() {
		conn, err := testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(conn)
		profile := testhelpers.CreateProfile(conn, &db.Profile{})
		session := testhelpers.CreateSession(conn, &db.Session{
			SessionID:      "WOD-20261014-01JREUSENORM00000",
			ProfileID:      profile.ID,
			WODDescription: "10 Pull-ups",
			WorkoutType:    "wod",
		})
		cachedNorm := `[{"movement":"Pull-up","reps":"10","is_main":true}]`
		testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{
			SessionID:         session.SessionID,
			ProfileID:         profile.ID,
			Status:            "COMPLETED",
			WODDescription:    "10 Pull-ups",
			NormalizedWorkout: cachedNorm,
		})

		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:     "test-api-key",
			BaseURL:    "https://normalize-workout.test",
			HTTPClient: &http.Client{Transport: transport},
		})
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{DB: conn, GeminiClient: client, logger: zap.NewNop()}
		calledObserve := false
		out := w.normalizeWorkout(context.Background(), session.SessionID, profile.ID, " 10 Pull-ups ", nil, func(string, *gemini.TokenUsage) {
			calledObserve = true
		})
		Expect(out).To(Equal(cachedNorm))
		Expect(calledObserve).To(BeFalse())
		Expect(transport.Requests()).To(BeEmpty())
	})
})
