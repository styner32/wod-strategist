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
		Entry("Box Step-up", "Box Step-up", "Box Step-up"),
		Entry("Box Step-Up", "Box Step-Up", "Box Step-up"),
		Entry("Box Step-Over", "Box Step-Over", "Box Step-over"),
		Entry("Dumbbell Box Step-Up", "Dumbbell Box Step-Up", "DB Box Step-up"),
		Entry("Dumbbell Box Step-Over", "Dumbbell Box Step-Over", "DB Box Step-over"),
		Entry("Weighted Box Step-up", "Weighted Box Step-up", "Weighted Box Step-up"),
		Entry("Sandbag Box Step-over", "Sandbag Box Step-over", "Sandbag Box Step-over"),
		Entry("Korean Box Step-up", "박스 스텝업", "Box Step-up"),
		Entry("Korean DB Box Step-over", "덤벨 박스 스텝오버", "DB Box Step-over"),
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

		// Lunges & Burpee Broad Jump
		Entry("Walking Lunge", "Walking Lunge", "Walking Lunge"),
		Entry("Walking Lunges plural", "Walking Lunges", "Walking Lunge"),
		Entry("Reverse Lunge", "Reverse Lunge", "Reverse Lunge"),
		Entry("Jumping Lunge", "Jumping Lunge", "Jumping Lunge"),
		Entry("Barbell Walking Lunge", "walking barbell lunge", "Barbell Walking Lunge"),
		Entry("Front Rack Lunge", "Front Rack Lunges", "Front Rack Lunge"),
		Entry("Overhead Walking Lunge", "walking overhead lunge", "Overhead Walking Lunge"),
		Entry("Dumbbell Overhead Lunge", "Dumbbell Overhead Lunge", "DB Overhead Lunge"),
		Entry("Single Arm Dumbbell Overhead Lunge", "single-arm dumbbell overhead lunges", "DB Overhead Lunge"),
		Entry("Dumbbell Walking Overhead Lunge", "dumbbell walking overhead lunge", "DB Overhead Walking Lunge"),
		Entry("DB Overhead Walking Lunge", "DB Overhead Walking Lunge", "DB Overhead Walking Lunge"),
		Entry("Goblet Lunge", "Kettlebell Goblet Lunge", "Goblet Lunge"),
		Entry("KB Overhead Walking Lunge", "kettlebell walking overhead lunge", "KB Overhead Walking Lunge"),
		Entry("Burpee Broad Jump", "Burpee Broad Jumps", "Burpee Broad Jump"),
		Entry("Korean Walking Lunge", "워킹 런지", "Walking Lunge"),
		Entry("Korean DB Overhead Lunge", "덤벨 오버헤드 런지", "DB Overhead Lunge"),
		Entry("Korean DB Overhead Walking Lunge", "덤벨 오버헤드 워킹 런지", "DB Overhead Walking Lunge"),
		Entry("Korean Burpee Broad Jump", "버피 브로드 점프", "Burpee Broad Jump"),
	)

	Describe("traveling and distance-lunge promotion", func() {
		It("identifies walking lunges and traveling movements without altering stationary movements", func() {
			Expect(movement.IsWalkingLunge("DB Overhead Walking Lunge")).To(BeTrue())
			Expect(movement.IsWalkingLunge("dumbbell walking overhead lunge")).To(BeTrue())
			Expect(movement.IsWalkingLunge("DB Overhead Lunge")).To(BeFalse())
			Expect(movement.IsWalkingLunge("Lunge")).To(BeFalse())

			Expect(movement.IsTraveling("DB Overhead Walking Lunge")).To(BeTrue())
			Expect(movement.IsTraveling("Burpee Broad Jump")).To(BeTrue())
			Expect(movement.IsTraveling("Handstand Walk")).To(BeTrue())
			Expect(movement.IsTraveling("Farmer's Carry")).To(BeTrue())
			Expect(movement.IsTraveling("Lunge")).To(BeFalse())
			Expect(movement.IsTraveling("Power Clean")).To(BeFalse())
			Expect(movement.IsTraveling("Row")).To(BeFalse())
			Expect(movement.IsTraveling("Box Jump Over")).To(BeFalse())

			Expect(movement.IsTravelingPolicyMovement("DB Overhead Walking Lunge")).To(BeTrue())
			Expect(movement.IsTravelingPolicyMovement("Burpee Broad Jump")).To(BeTrue())
			Expect(movement.IsTravelingPolicyMovement("Handstand Walk")).To(BeFalse())
			Expect(movement.IsTravelingPolicyMovement("Lunge")).To(BeFalse())
		})

		DescribeTable("PromoteDistanceLunge and PromoteDistanceLungeInWOD",
			func(rawMovement, wodDesc, expected string) {
				Expect(movement.PromoteDistanceLungeInWOD(rawMovement, wodDesc)).To(Equal(expected))
			},
			Entry("200ft dumbbell overhead lunge inline", "200ft dumbbell overhead lunge", "", "DB Overhead Walking Lunge"),
			Entry("Dumbbell Overhead Lunge with 200ft WOD clause", "Dumbbell Overhead Lunge", "For Time:\n200ft Dumbbell Overhead Lunge (50/35lb)\n20 Pull-ups", "DB Overhead Walking Lunge"),
			Entry("Front Rack Lunge with 25m WOD clause", "Front Rack Lunge", "3 Rounds:\n25m Front Rack Lunge\n15 Toes-to-Bar", "Front Rack Walking Lunge"),
			Entry("Korean distance lunge", "덤벨 오버헤드 런지", "20미터 덤벨 오버헤드 런지", "DB Overhead Walking Lunge"),
			Entry("Rep-based lunge is NOT promoted", "Dumbbell Overhead Lunge", "For Time:\n20 Dumbbell Overhead Lunges (50/35lb)", ""),
			Entry("AMRAP 10m time header does NOT promote stationary lunge", "DB Overhead Lunge", "AMRAP 10m:\n12 DB Overhead Lunges\n12 Pull-ups", ""),
			Entry("AMRAP 10m inline without colon does NOT promote stationary lunge", "DB Overhead Lunge", "AMRAP 10m 12 DB Overhead Lunges", ""),
			Entry("Different lunge on distance line does NOT promote stationary DB lunge", "DB Overhead Lunge", "200ft Walking Lunge\n20 DB Overhead Lunges", ""),
			Entry("Mobility stretch with lunge word is NOT promoted", "Samson (Hip Flexor Lunge) Stretch", "20m Samson (Hip Flexor Lunge) Stretch", ""),
			Entry("Slash-separated 400m Run / 20 DB Lunges does NOT promote DB Lunge", "DB Lunge", "400m Run / 20 DB Lunges", ""),
			Entry("Compact slash 400m Run/20 DB Lunges with Rx ratio does NOT promote DB Lunge", "DB Lunge", "400m Run/20 DB Lunges (50/35lb)", ""),
			Entry("Unpunctuated 400m Run 20 DB Lunges does NOT promote DB Lunge", "DB Lunge", "400m Run 20 DB Lunges", ""),
			Entry("Middle-dot 500m Row · 16 DB Overhead Lunges does NOT promote DB Overhead Lunge", "DB Overhead Lunge", "500m Row · 16 DB Overhead Lunges", ""),
			Entry("Slash-separated 400m Run / 200ft DB Overhead Lunges promotes DB Overhead Lunge", "DB Overhead Lunge", "400m Run / 200ft DB Overhead Lunges (50/35lb)", "DB Overhead Walking Lunge"),
			Entry("Rx distance ratio 200/150ft Dumbbell Overhead Lunge promotes to walking lunge", "Dumbbell Overhead Lunge", "200/150ft Dumbbell Overhead Lunge (50/35lb)", "DB Overhead Walking Lunge"),
			Entry("Mixed distance and stationary clauses of same lunge stay conservative on WOD fallback", "DB Overhead Lunge", "200ft DB Overhead Lunge / 20 DB Overhead Lunges", ""),
		)

		It("splits multi-workout single lines while preserving Rx weight and distance ratios", func() {
			Expect(movement.SplitWorkoutClauses("400m Run / 20 DB Lunges (50/35lb)")).To(Equal([]string{
				"400m Run",
				"20 DB Lunges (50/35lb)",
			}))
			Expect(movement.SplitWorkoutClauses("200/150ft Dumbbell Overhead Lunge (50/35lb)")).To(Equal([]string{
				"200/150ft Dumbbell Overhead Lunge (50/35lb)",
			}))
		})

		It("detects traveling policy context only when traveling lunge or Burpee Broad Jump is present", func() {
			Expect(movement.HasTravelingPolicyContext([]string{"Power Clean", "Thruster"}, "21-15-9 Thrusters and Pull-ups")).To(BeFalse())
			Expect(movement.HasTravelingPolicyContext([]string{"DB Overhead Lunge"}, "AMRAP 10m:\n12 DB Overhead Lunges")).To(BeFalse())
			Expect(movement.HasTravelingPolicyContext([]string{"Run", "DB Lunge"}, "400m Run / 20 DB Lunges")).To(BeFalse())
			Expect(movement.HasTravelingPolicyContext([]string{"Run", "DB Lunge"}, "400m Run 20 DB Lunges")).To(BeFalse())
			Expect(movement.HasTravelingPolicyContext(nil, "For Time:\n200ft dumbbell overhead lunge\n30 Pull-ups")).To(BeTrue())
			Expect(movement.HasTravelingPolicyContext(nil, "400m Run / 200ft DB Overhead Lunges")).To(BeTrue())
			Expect(movement.HasTravelingPolicyContext([]string{"DB Overhead Walking Lunge"}, "")).To(BeTrue())
			Expect(movement.HasTravelingPolicyContext(nil, "5 Rounds: 10 Burpee Broad Jumps")).To(BeTrue())
		})
	})
})
