package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateSessionID", func() {
	DescribeTable("validates session ID format and security",
		func(sessionID string, wantErr bool) {
			err := validateSessionID(sessionID)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("valid current", "WOD-20240101-ABCD1234", false),
		Entry("valid legacy", "P12-WOD-2026-04-01-14-30", false),
		Entry("valid legacy no profile", "WOD-2026-03-30-10-34", false),
		Entry("valid test pattern", "session-hints", false),
		Entry("valid test pattern split", "split-media-session", false),
		Entry("invalid format no dashes", "WOD20240101ABCD1234", true),
		Entry("path traversal unix", "../etc/passwd", true),
		Entry("path traversal windows", `..\Windows\System32`, true),
		Entry("path traversal mixed", `../Windows\System32`, true),
		Entry("contains slash", "WOD/20240101", true),
		Entry("contains backslash", `WOD\20240101`, true),
		Entry("contains dot", "WOD.20240101", true),
		Entry("only dot", ".", true),
		Entry("only dot-dot", "..", true),
		Entry("empty", "", true),
	)
})

var _ = Describe("workoutTypeFromSessionID", func() {
	DescribeTable("reads the type prefix of current and legacy session IDs",
		func(sessionID, want string) {
			Expect(workoutTypeFromSessionID(sessionID)).To(Equal(want))
		},
		Entry("current warmup", "WARMUP-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF", WorkoutTypeWarmup),
		Entry("current wod", "WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF", WorkoutTypeWOD),
		Entry("legacy warmup", "P12-WARMUP-2025-03-01-08-00", WorkoutTypeWarmup),
		Entry("legacy cooldown lowercase", "p7-cooldown-2025-02-01-07-30", WorkoutTypeCooldown),
		Entry("legacy accessory without profile", "ACCESSORY-2025-01-01-10-00", WorkoutTypeAccessory),
		Entry("legacy wod", "P3-WOD-2025-01-01-10-00", WorkoutTypeWOD),
		Entry("no type prefix", "session-hints", ""),
		Entry("type word later in the ID", "split-WARMUP-session", ""),
	)
})
