package movement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/movement"
)

var _ = Describe("Evidence-preserving movement aliases", func() {
	It("leaves unspecified variants and implements unconfirmed", func() {
		for _, raw := range []string{"Snatch", "Snatches", "Muscle-up", "Muscle Ups", "Dip", "Dips", "Squat", "Squats", "Press", "Overhead Press", "Shoulder Presses", "OHP", "Jump Rope", "Skipping Rope", "GHD"} {
			Expect(movement.Canonical(raw)).To(BeEmpty(), raw)
			Expect(movement.IsAmbiguous(raw)).To(BeTrue(), raw)
		}
	})
	It("preserves known variants, abbreviations, and conjunction spelling", func() {
		for raw, canonical := range map[string]string{
			"Clean and Jerk": "Clean & Jerk", "Clean&Jerk": "Clean & Jerk",
			"Power Clean and Jerks": "Power Clean & Jerk", "Squat Clean and Jerk": "Squat Clean & Jerk",
			"Power Snatches": "Power Snatch", "Squat Snatch": "Squat Snatch",
			"Hang Power Clean": "Hang Power Clean", "Hang Power Cleans": "Hang Power Clean", "HPC": "Hang Power Clean",
			"Hang Squat Clean": "Hang Squat Clean", "Hang Squat Cleans": "Hang Squat Clean", "HSC": "Hang Squat Clean",
			"Hang Power Snatch": "Hang Power Snatch", "Hang Power Snatches": "Hang Power Snatch", "HPS": "Hang Power Snatch",
			"Hang Squat Snatch": "Hang Squat Snatch", "Hang Squat Snatches": "Hang Squat Snatch", "HSS": "Hang Squat Snatch",
			"Muscle Clean": "Muscle Clean", "Hang Muscle Clean": "Hang Muscle Clean",
			"Muscle Snatch": "Muscle Snatch", "Hang Muscle Snatch": "Hang Muscle Snatch",
			"Push Jerk": "Push Jerk", "Push Jerks": "Push Jerk", "PJ": "Push Jerk",
			"Split Jerk": "Split Jerk", "Split Jerks": "Split Jerk", "SJ": "Split Jerk",
			"Behind-the-Neck Push Press": "Push Press", "BTN Push Press": "Push Press",
			"Deadlift": "Deadlift", "Deadlifts": "Deadlift", "DL": "Deadlift",
			"BMU": "Bar Muscle-up", "RMU": "Ring Muscle-up", "Ring Dips": "Ring Dip",
			"Air Squats": "Air Squat", "Strict Presses": "Strict Press",
			"Double Unders": "Double-under", "Single Unders": "Single-under",
		} {
			Expect(movement.Canonical(raw)).To(Equal(canonical), raw)
		}
	})
})
