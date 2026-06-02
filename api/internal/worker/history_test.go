package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ── parseSessionScore ──────────────────────────────────────────────────────────

var _ = Describe("parseSessionScore", func() {
	It("extracts valid score JSON from a ```score block", func() {
		input := `분석 완료.

` + "```score\n" + `{"overall":74,"form":68,"intensity":82,"consistency":72,"movements":{"Snatch":{"form":65,"intensity":80}},"summary":"스내치 풀 익스텐션이 개선되었습니다."}
` + "```"

		got := parseSessionScore(input)
		Expect(got).NotTo(Equal("{}"))
		Expect(got).To(ContainSubstring("74"))
		Expect(got).To(ContainSubstring("Snatch"))
	})

	It("returns {} when no score block is present", func() {
		got := parseSessionScore("분석만 있고 점수 블록 없음.")
		Expect(got).To(Equal("{}"))
	})

	It("returns {} for malformed JSON inside a score block", func() {
		got := parseSessionScore("```score\n{not valid json}\n```")
		Expect(got).To(Equal("{}"))
	})

	It("parses valid JSON with all-zero scores", func() {
		// A valid JSON with all-zero values still parses (zero is a valid score)
		input := "```score\n{\"overall\":0,\"form\":0,\"intensity\":0,\"consistency\":0,\"movements\":{},\"summary\":\"\"}\n```"
		got := parseSessionScore(input)
		// Should parse successfully and return the zero-score JSON
		Expect(got).NotTo(Equal("{}"))
	})
})

// ── buildWODContext ───────────────────────────────────────────────────────────

var _ = Describe("buildWODContext", func() {
	It("returns empty string for empty WOD", func() {
		Expect(buildWODContext("")).To(BeEmpty())
	})

	It("returns empty string for whitespace-only WOD", func() {
		Expect(buildWODContext("   ")).To(BeEmpty())
	})

	It("includes WOD description and hint for named WOD 'Fran'", func() {
		got := buildWODContext("Fran")
		Expect(got).To(ContainSubstring("Fran"))
		Expect(got).To(ContainSubstring("WOD 구성을 참고"))
	})

	It("includes WOD description and hint for named WOD 'Grace'", func() {
		got := buildWODContext("Grace")
		Expect(got).To(ContainSubstring("Grace"))
		Expect(got).To(ContainSubstring("WOD 구성을 참고"))
	})

	It("includes 'For Time' context for custom For Time WOD", func() {
		got := buildWODContext("For Time: 5 rounds of 10 Deadlifts + 15 Box Jumps")
		Expect(got).To(ContainSubstring("For Time"))
	})

	It("includes 'AMRAP' context for custom AMRAP WOD", func() {
		got := buildWODContext("AMRAP 20: 5 Pull-ups, 10 Push-ups, 15 Air Squats")
		Expect(got).To(ContainSubstring("AMRAP"))
	})

	It("includes EMOM context with form emphasis", func() {
		got := buildWODContext("EMOM 12: 8 KB Swings")
		Expect(got).To(ContainSubstring("EMOM"))
		Expect(got).To(ContainSubstring("form"))
	})
})

// ── buildHistoryContext (nil-safe checks) ─────────────────────────────────────

var _ = Describe("buildHistoryContext", func() {
	It("returns empty string when DB is nil", func() {
		w := &Worker{DB: nil}
		Expect(w.buildHistoryContext(1, 5)).To(BeEmpty())
	})

	It("returns empty string when profileID is 0", func() {
		w := &Worker{DB: nil}
		Expect(w.buildHistoryContext(0, 5)).To(BeEmpty())
	})
})
