package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("session appearance sanitization", func() {
	DescribeTable("sanitizeAppearanceValue",
		func(input, expected string) {
			Expect(sanitizeAppearanceValue(input)).To(Equal(expected))
		},
		Entry("trims whitespace", "  Red Nike Metcon  ", "Red Nike Metcon"),
		Entry("replaces newlines and strips backticks", "Line1\nLine2`backtick`", "Line1 Line2backtick"),
	)

	It("normalizes appearance struct", func() {
		in := &AppearanceInput{
			Appearance: "  Black t-shirt, grey shorts\nred shoes  ",
		}

		out := normalizeAppearance(in)
		Expect(out.Appearance).To(Equal("Black t-shirt, grey shorts red shoes"))
	})
})
