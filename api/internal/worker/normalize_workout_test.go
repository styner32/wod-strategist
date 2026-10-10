package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
})
