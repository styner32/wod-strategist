package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("Workout progress reanalysis snapshot staleness", func() {
	It("checks evidence versions independently of assessment and score version", func() {
		current := db.NullableJSONDocument(`{"version":1,"source_version":"current"}`)
		Expect(progressSnapshotStale(db.JSONDocument(`{"session_score":"{}","workout_progress":{"version":1,"source_version":"current"}}`), current)).To(BeFalse())
		Expect(progressSnapshotStale(db.JSONDocument(`{"session_score":"{}","workout_progress":{"version":1,"source_version":"old"}}`), current)).To(BeTrue())
		Expect(progressSnapshotStale(db.JSONDocument(`{"workout_progress":{"version":2,"source_version":"current"}}`), current)).To(BeTrue())
		Expect(progressSnapshotStale(db.JSONDocument(`{}`), current)).To(BeTrue())
		Expect(progressSnapshotStale(db.JSONDocument(`{"workout_progress":null}`), current)).To(BeTrue())
		Expect(progressSnapshotStale(db.JSONDocument(`{"workout_progress":null}`), nil)).To(BeFalse())
		Expect(progressSnapshotStale(db.JSONDocument(`{}`), nil)).To(BeFalse())
	})
})
