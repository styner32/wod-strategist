package movement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/movement"
)

var _ = Describe("movement catalog", func() {
	DescribeTable("NormalizeKey",
		func(input, expected string) {
			Expect(movement.NormalizeKey(input)).To(Equal(expected))
		},
		Entry("Power Clean", "Power Clean", "power clean"),
		Entry("Power-Clean with spaces", " Power-Clean ", "power clean"),
		Entry("power clean extra spaces", "power   clean", "power clean"),
		Entry("Pull-up", "Pull-up", "pull up"),
		Entry("Double-under", "  Double-under  ", "double under"),
		Entry("empty", "", ""),
		Entry("spaces only", "   ", ""),
		Entry("Clean & Jerk", "Clean & Jerk", "clean and jerk"),
		Entry("Clean and Jerk", "Clean and Jerk", "clean and jerk"),
		Entry("Clean&Jerk", "Clean&Jerk", "clean and jerk"),
	)

	It("All returns non-empty movement list starting with Power Snatch", func() {
		all := movement.All()
		Expect(all).NotTo(BeEmpty())
		Expect(all[0]).To(Equal("Power Snatch"))
	})

	DescribeTable("Canonical",
		func(input, expected string) {
			Expect(movement.Canonical(input)).To(Equal(expected))
		},
		// Exact canonical
		Entry("Deadlift", "Deadlift", "Deadlift"),
		Entry("deadlift", "deadlift", "Deadlift"),
		Entry("Double-under", "Double-under", "Double-under"),
		Entry("Row", "Row", "Row"),

		// Aliases
		Entry("Rowing", "Rowing", "Row"),
		Entry("rowing", "rowing", "Row"),
		Entry("rower", "rower", "Row"),
		Entry("running", "running", "Run"),
		Entry("Double Unders", "Double Unders", "Double-under"),
		Entry("Double-Unders", "Double-Unders", "Double-under"),
		Entry("double under", "double under", "Double-under"),
		Entry("du", "du", "Double-under"),
		Entry("dus", "dus", "Double-under"),
		Entry("Single Unders", "Single Unders", "Single-under"),
		Entry("su", "su", "Single-under"),
		Entry("c&j", "c&j", "Clean & Jerk"),
		Entry("Clean and Jerk", "Clean and Jerk", "Clean & Jerk"),
		Entry("hspu", "hspu", "Handstand Push-up"),
		Entry("pullup", "pullup", "Pull-up"),
		Entry("pullups", "pullups", "Pull-up"),
		Entry("pushup", "pushup", "Push-up"),
		Entry("situp", "situp", "Sit-up"),
		Entry("wall ball", "wall ball", "Wallball Shot"),
		Entry("kettlebell swing", "kettlebell swing", "KB Swing"),
		Entry("farmers carry", "farmers carry", "Farmer's Carry"),

		// Plural fallbacks
		Entry("Deadlifts", "Deadlifts", "Deadlift"),
		Entry("deadlifts", "deadlifts", "Deadlift"),
		Entry("Push Presses", "Push Presses", "Push Press"),
		Entry("Burpees", "Burpees", "Burpee"),
		Entry("Air Squats", "Air Squats", "Air Squat"),
		Entry("Box Jump Overs", "Box Jump Overs", "Box Jump Over"),
		Entry("Burpee Box Jump Overs", "Burpee Box Jump Overs", "Burpee Box Jump Over"),
		Entry("Shuttle Run", "Shuttle Run", "Shuttle Run"),
		Entry("Shuttle Runs", "Shuttle Runs", "Shuttle Run"),
		Entry("Plate Ground-to-Overhead", "Plate Ground-to-Overhead", "Plate Ground-to-Overhead"),
		Entry("Korean Clean & Jerk", "클린앤저크", "Clean & Jerk"),
		Entry("Korean Clean and Jerk spaced", "클린 앤 저크", "Clean & Jerk"),
		Entry("Korean Power Clean", "파워 클린", "Power Clean"),
		Entry("Korean Shuttle Run", "셔틀런", "Shuttle Run"),

		// Unknown / empty
		Entry("empty", "", ""),
		Entry("spaces", "   ", ""),
		Entry("invented", "Invented Squat", ""),
		Entry("unknown", "Some Random Unknown Movement", ""),
	)
})
