package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("Session analysis helpers", func() {
	It("normalizes movement hints without imposing a closed movement list", func() {
		hints := normalizeMovementHints([]string{" Pull-up ", "pull-up", "Sandbag Over Shoulder", ""})

		Expect(hints).To(Equal([]string{"Pull-up", "Sandbag Over Shoulder"}))
	})

	It("keeps genuinely unlisted observed movements separate from entered hints", func() {
		chunks := []db.ChunkAnalysisResult{
			{Status: "COMPLETED", ExerciseType: "Pull-up"},
			{Status: "COMPLETED", ExerciseType: "Rope Climb"},
			{Status: "COMPLETED", ExerciseType: "rope climb"},
			{Status: "COMPLETED", ExerciseType: "Walking"},
			{Status: "COMPLETED", ExerciseType: "Unknown"},
			{Status: "COMPLETED", ExerciseType: "Sandbag Over Shoulder"},
			{Status: "FAILED", ExerciseType: "Unverified Carry"},
		}

		Expect(additionalObservedMovements(chunks, []string{"Pull-up"})).To(Equal([]string{
			"Rope Climb",
			"Sandbag Over Shoulder",
		}))
	})

	It("decodes an empty movement hint document as an empty array", func() {
		hints, err := decodeMovementHints(nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(hints).To(BeEmpty())
		Expect(hints).NotTo(BeNil())
	})
})
