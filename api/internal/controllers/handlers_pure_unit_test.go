package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("escapeLikePattern", func() {
	DescribeTable("escapes LIKE wildcard and backslash characters",
		func(input, expected string) {
			Expect(escapeLikePattern(input)).To(Equal(expected))
		},
		Entry("normal text without wildcards", "pull-up", "pull-up"),
		Entry("percent sign", "50% snatch", `50\% snatch`),
		Entry("underscore", "dead_lift", `dead\_lift`),
		Entry("backslash", `back\squat`, `back\\squat`),
		Entry("combined special characters", `100%_clean\jerk`, `100\%\_clean\\jerk`),
		Entry("empty string", "", ""),
	)
})
