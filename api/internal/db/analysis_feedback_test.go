package db

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Analysis feedback", func() {
	It("round-trips JSON documents as JSON", func() {
		document := JSONDocument(`{"movement_name":"Pull-up"}`)
		encoded, err := json.Marshal(document)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`{"movement_name":"Pull-up"}`))

		value, err := document.Value()
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal(`{"movement_name":"Pull-up"}`))

		var decoded JSONDocument
		Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
		Expect(string(decoded)).To(Equal(string(document)))
	})

	It("builds hints only from the same correction across distinct sessions", func() {
		now := time.Now()
		events := []AnalysisFeedback{
			movementFeedbackEvent("session-3", "ROPE   CLIMB", "Pull-up", now),
			movementFeedbackEvent("session-2", "Rope Climb", "Pull-up", now.Add(-time.Minute)),
			movementFeedbackEvent("session-1", "Hang Clean", "Power Clean", now.Add(-2*time.Minute)),
		}

		hints := buildPersonalMovementHints(events)
		Expect(hints).To(Equal([]PersonalMovementHint{{
			PredictedMovement: "ROPE   CLIMB",
			CorrectedMovement: "Pull-up",
			SessionCount:      2,
		}}))
	})

	It("excludes retracted and same-session duplicates", func() {
		now := time.Now()
		retracted := movementFeedbackEvent("session-2", "Rope Climb", "Pull-up", now)
		retracted.Retracted = true
		events := []AnalysisFeedback{
			movementFeedbackEvent("session-1", "Rope Climb", "Pull-up", now),
			movementFeedbackEvent("session-1", "Rope Climb", "Pull-up", now.Add(-time.Minute)),
			retracted,
		}

		Expect(buildPersonalMovementHints(events)).To(BeEmpty())
	})
})

func movementFeedbackEvent(sessionID, predicted, corrected string, createdAt time.Time) AnalysisFeedback {
	original, err := json.Marshal(map[string]string{"exercise_type": predicted})
	Expect(err).NotTo(HaveOccurred())
	correction, err := json.Marshal(map[string]string{"movement_name": corrected})
	Expect(err).NotTo(HaveOccurred())
	return AnalysisFeedback{
		SessionID:          sessionID,
		OriginalPrediction: JSONDocument(original),
		Correction:         JSONDocument(correction),
		CreatedAt:          createdAt,
	}
}
