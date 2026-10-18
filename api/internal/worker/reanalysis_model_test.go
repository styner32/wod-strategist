package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/gemini"
)

var _ = Describe("reanalysis model selection", func() {
	DescribeTable("resolves explicit models without downgrading Argon to Flash", func(raw, expected string) {
		Expect(resolveReanalysisModel(raw)).To(Equal(expected))
	},
		Entry("Argon ID", "gemini-4-argon", gemini.ModelArgon4),
		Entry("Argon alias", "argon", gemini.ModelArgon4),
		Entry("Argon version alias", "argon4", gemini.ModelArgon4),
		Entry("case and whitespace", " Gemini-4-Argon ", gemini.ModelArgon4),
		Entry("Pro alias", "pro", gemini.ModelArgon4),
		Entry("Gemini Pro alias", "gemini-pro", gemini.ModelArgon4),
		Entry("legacy Pro ID", "gemini-3.1-pro-preview", gemini.ModelPro31Preview),
		Entry("legacy Pro alias", "pro31", gemini.ModelPro31Preview),
		Entry("Flash alias", "flash", gemini.ModelFlash38),
		Entry("unknown model preserves existing fallback", "unknown", gemini.ModelFlash38),
	)
	It("uses the configured default when a web request omits the model", func() {
		Expect(resolveReanalysisModelWithDefault("", gemini.ModelArgon4)).To(Equal(gemini.ModelArgon4))
		Expect(resolveReanalysisModelWithDefault("", gemini.ModelFlash38)).To(Equal(gemini.ModelFlash38))
		Expect(resolveReanalysisModelWithDefault("flash", gemini.ModelArgon4)).To(Equal(gemini.ModelFlash38))
	})
})
