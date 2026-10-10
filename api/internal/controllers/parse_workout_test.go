package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseWorkoutBlock", func() {
	It("parses valid workout block", func() {
		input := "Here is the workout:\n```workout\n" +
			`{"wod_description":"Fran","movements":["Thruster","Pull-up"],"raw_text":"FRAN\n21-15-9\nThrusters 43kg\nPull-ups"}` +
			"\n```\n"

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.WODDescription).To(Equal("Fran"))
		Expect(resp.Movements).To(Equal([]string{"Thruster", "Pull-up"}))
		Expect(resp.RawText).NotTo(BeEmpty())
	})

	It("returns error when no workout block is present", func() {
		input := "I cannot read the whiteboard clearly."

		_, err := parseWorkoutBlock(input)
		Expect(err).To(HaveOccurred())
	})

	It("returns error for invalid JSON in workout block", func() {
		input := "```workout\nnot valid json\n```\n"

		_, err := parseWorkoutBlock(input)
		Expect(err).To(HaveOccurred())
	})

	It("parses empty workout block", func() {
		input := "```workout\n" +
			`{"wod_description":"","movements":[],"raw_text":""}` +
			"\n```\n"

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.WODDescription).To(BeEmpty())
		Expect(resp.Movements).To(BeEmpty())
	})

	It("parses For Time workout block", func() {
		input := "```workout\n" +
			`{"wod_description":"For Time: 5 rounds of 10 Deadlifts (60kg) + 15 Box Jumps (24in)","movements":["Deadlift","Box Jump"],"raw_text":"FOR TIME\n5RDS\n10 DL 60kg\n15 BJ 24\""}` +
			"\n```\n"

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.WODDescription).To(Equal("For Time: 5 rounds of 10 Deadlifts (60kg) + 15 Box Jumps (24in)"))
		Expect(resp.Movements).To(Equal([]string{"Deadlift", "Box Jump"}))
	})

	It("parses AMRAP workout block", func() {
		input := "```workout\n" +
			`{"wod_description":"AMRAP 20: 5 Pull-ups, 10 Push-ups, 15 Air Squats","movements":["Pull-up","Push-up","Air Squat"],"raw_text":"AMRAP 20분\n5 풀업\n10 푸쉬업\n15 에어스쿼트"}` +
			"\n```\n"

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Movements).To(HaveLen(3))
	})

	It("parses json fence", func() {
		input := "```json\n" +
			`{"wod_description":"Grace","movements":["Clean and Jerk"],"raw_text":"GRACE\n30 C&J"}` +
			"\n```\n"

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.WODDescription).To(Equal("Grace"))
		Expect(resp.Movements).To(Equal([]string{"Clean and Jerk"}))
	})

	It("handles case-insensitive fence", func() {
		input := "```Workout\n" +
			`{"wod_description":"Cindy","movements":["Pull-up","Push-up","Air Squat"],"raw_text":"CINDY"}` +
			"\n```\n"

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.WODDescription).To(Equal("Cindy"))
	})

	It("parses raw JSON without code fences", func() {
		input := `Here is the extracted workout: {"wod_description":"Murph","movements":["Run","Pull-up","Push-up","Air Squat"],"raw_text":"MURPH"} Hope this helps!`

		resp, err := parseWorkoutBlock(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.WODDescription).To(Equal("Murph"))
		Expect(resp.Movements).To(Equal([]string{"Run", "Pull-up", "Push-up", "Air Squat"}))
	})
})
