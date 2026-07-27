package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Target Person Helpers", func() {
	Describe("formatTargetPersonContext", func() {
		It("formats appearance string into prompt context block", func() {
			res := formatTargetPersonContext("검은 반팔, 회색 반바지, 빨간 나이키 신발")
			Expect(res).To(ContainSubstring("검은 반팔, 회색 반바지, 빨간 나이키 신발"))
			Expect(res).To(ContainSubstring("## 대상 인물 확정"))
		})
	})

	Describe("extractAppearanceText", func() {
		It("extracts appearance text from new JSON format", func() {
			input := `{"appearance":"검은 반팔, 회색 반바지"}`
			Expect(extractAppearanceText([]byte(input))).To(Equal("검은 반팔, 회색 반바지"))
		})

		It("extracts and joins appearance text from legacy nested JSON format", func() {
			input := `{"persistent":{"shoes":"흰색 나이키","hair":"검은 머리"},"session":{"top":"검은 반팔"},"removable":["무릎보호대"]}`
			Expect(extractAppearanceText([]byte(input))).To(Equal("검은 반팔, 흰색 나이키, 검은 머리, 무릎보호대"))
		})

		It("extracts appearance text from raw JSON string", func() {
			input := `"파란 나이키"`
			Expect(extractAppearanceText([]byte(input))).To(Equal("파란 나이키"))
		})
	})

	Describe("parseTargetPerson", func() {
		It("parses a valid target person JSON block", func() {
			input := `Some analysis text
` + "```" + `json
{
  "matched_cues": ["Black t-shirt", "Grey shorts"],
  "unmatched_cues": [],
  "confidence": 0.9,
  "ambiguous": false
}
` + "```" + `
More analysis text`
			cues, conf := parseTargetPerson(input)
			Expect(conf).To(Equal(0.9))
			Expect(cues).To(ContainSubstring("matched_cues"))
		})

		It("clamps confidence when target person block is ambiguous", func() {
			input := `Some analysis text
` + "```" + `target_person
{
  "matched_cues": ["Red Metcon"],
  "unmatched_cues": ["White top"],
  "confidence": 0.8,
  "ambiguous": true
}
` + "```"

			cues, conf := parseTargetPerson(input)
			Expect(conf).To(Equal(0.5))
			Expect(cues).To(ContainSubstring("matched_cues"))
		})

		It("returns default values when target person block is missing", func() {
			cues, conf := parseTargetPerson("Plain analysis without JSON block")
			Expect(conf).To(Equal(0.5))
			Expect(cues).To(Equal("{}"))
		})
	})

	Describe("stripTargetPerson", func() {
		It("removes the target_person code block while retaining body text", func() {
			input := `Beginning of response
` + "```" + `target_person
{
  "matched_cues": ["Red Metcon"],
  "confidence": 0.9
}
` + "```" + `
Rest of analysis text.`

			stripped := stripTargetPerson(input)
			Expect(stripped).NotTo(ContainSubstring("target_person"))
			Expect(stripped).To(ContainSubstring("Beginning of response"))
			Expect(stripped).To(ContainSubstring("Rest of analysis text."))
		})
	})
})
