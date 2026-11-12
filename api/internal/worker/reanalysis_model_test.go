package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/gemini"
)

var _ = Describe("reanalysis model selection", func() {
	DescribeTable("resolves explicit models and defaults to Carbon", func(raw, expected string) {
		Expect(resolveReanalysisModel(raw)).To(Equal(expected))
	},
		Entry("Carbon ID", "gemini-4-carbon", gemini.ModelCarbon4),
		Entry("Carbon alias", "carbon", gemini.ModelCarbon4),
		Entry("Carbon version alias", "carbon4", gemini.ModelCarbon4),
		Entry("case and whitespace", " Gemini-4-Carbon ", gemini.ModelCarbon4),
		Entry("Hydrogen ID", "gemini-4-hydrogen", gemini.ModelHydrogen4),
		Entry("Hydrogen alias", "hydrogen", gemini.ModelHydrogen4),
		Entry("Hydrogen version alias", "hydrogen4", gemini.ModelHydrogen4),
		Entry("Argon ID", "gemini-4-argon", gemini.ModelArgon4),
		Entry("Argon alias", "argon", gemini.ModelArgon4),
		Entry("Argon version alias", "argon4", gemini.ModelArgon4),
		Entry("explicit Pro alias", "pro", gemini.ModelPro31Preview),
		Entry("explicit Gemini Pro alias", "gemini-pro", gemini.ModelPro31Preview),
		Entry("legacy Pro ID", "gemini-3.1-pro-preview", gemini.ModelPro31Preview),
		Entry("legacy Pro alias", "pro31", gemini.ModelPro31Preview),
		Entry("explicit Flash alias", "flash", gemini.ModelFlash38),
		Entry("explicit Flash 3.8 alias", "flash38", gemini.ModelFlash38),
		Entry("empty defaults to Carbon", "", gemini.ModelCarbon4),
		Entry("unknown model defaults to Carbon", "unknown", gemini.ModelCarbon4),
	)
	It("uses the configured default when a web request omits or specifies an unknown model", func() {
		Expect(resolveReanalysisModelWithDefault("", gemini.ModelCarbon4)).To(Equal(gemini.ModelCarbon4))
		Expect(resolveReanalysisModelWithDefault("unknown", gemini.ModelCarbon4)).To(Equal(gemini.ModelCarbon4))
		Expect(resolveReanalysisModelWithDefault("", gemini.ModelHydrogen4)).To(Equal(gemini.ModelHydrogen4))
		Expect(resolveReanalysisModelWithDefault("unknown", gemini.ModelHydrogen4)).To(Equal(gemini.ModelHydrogen4))
		Expect(resolveReanalysisModelWithDefault("flash", gemini.ModelCarbon4)).To(Equal(gemini.ModelFlash38))
		Expect(resolveReanalysisModelWithDefault("pro", gemini.ModelCarbon4)).To(Equal(gemini.ModelPro31Preview))
	})
})
