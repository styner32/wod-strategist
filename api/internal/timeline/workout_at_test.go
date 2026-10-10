package timeline_test

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/timeline"
)

var _ = Describe("ResolveWorkoutAt", func() {
	It("resolves workoutAt from current format ULID", func() {
		now := time.Date(2026, 9, 7, 10, 30, 0, 0, time.UTC)
		entropy := rand.Reader
		u := ulid.MustNew(ulid.Timestamp(now), entropy)

		sessionID := fmt.Sprintf("WOD-20260907-%s", u.String())
		baseline := now.Add(1 * time.Minute)

		workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
		Expect(reliable).To(BeTrue())
		Expect(source).To(Equal(timeline.WorkoutAtSourceSessionULID))
		Expect(workoutAt.UnixMilli()).To(Equal(now.UnixMilli()))
	})

	It("resolves workoutAt for WARMUP type", func() {
		now := time.Date(2026, 4, 7, 8, 15, 0, 0, time.UTC)
		u := ulid.MustNew(ulid.Timestamp(now), rand.Reader)

		sessionID := fmt.Sprintf("WARMUP-20260407-%s", u.String())
		baseline := now.Add(2 * time.Minute)

		workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, &baseline, nil)
		Expect(reliable).To(BeTrue())
		Expect(source).To(Equal(timeline.WorkoutAtSourceSessionULID))
		Expect(workoutAt.UnixMilli()).To(Equal(now.UnixMilli()))
	})

	It("resolves workoutAt from previous server format", func() {
		now := time.Date(2026, 8, 1, 14, 20, 0, 0, time.UTC)
		u := ulid.MustNew(ulid.Timestamp(now), rand.Reader)

		sessionID := fmt.Sprintf("WOD-202608011420-%s", u.String())
		baseline := now.Add(30 * time.Second)

		workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
		Expect(reliable).To(BeTrue())
		Expect(source).To(Equal(timeline.WorkoutAtSourceSessionULID))
		Expect(workoutAt.UnixMilli()).To(Equal(now.UnixMilli()))
	})

	It("resolves workoutAt from legacy format local time", func() {
		sessionID := "P42-WOD-2026-09-07-15-30"
		loc, _ := time.LoadLocation("Asia/Seoul")
		expectedUTC := time.Date(2026, 9, 7, 15, 30, 0, 0, loc).UTC()
		baseline := expectedUTC.Add(10 * time.Minute)

		workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
		Expect(reliable).To(BeTrue())
		Expect(source).To(Equal(timeline.WorkoutAtSourceLegacyLocalTime))
		Expect(workoutAt.Equal(expectedUTC)).To(BeTrue())
	})

	It("falls through to session created fallback on invalid date", func() {
		// Month 13 is invalid
		u := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)
		sessionID := fmt.Sprintf("WOD-20261301-%s", u.String())

		createdAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, &createdAt, nil)
		Expect(reliable).To(BeFalse())
		Expect(source).To(Equal(timeline.WorkoutAtSourceSessionCreatedFallback))
		Expect(workoutAt.Equal(createdAt)).To(BeTrue())
	})

	It("falls through to analysis created fallback on future date", func() {
		// ULID timestamp is 1 hour ahead of baseline created_at -> exceeds 5 minute threshold
		baseline := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
		futureTime := baseline.Add(1 * time.Hour)
		u := ulid.MustNew(ulid.Timestamp(futureTime), rand.Reader)
		sessionID := fmt.Sprintf("WOD-20260907-%s", u.String())

		workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
		Expect(reliable).To(BeFalse())
		Expect(source).To(Equal(timeline.WorkoutAtSourceAnalysisCreatedFallback))
		Expect(workoutAt.Equal(baseline)).To(BeTrue())
	})

	It("returns zero result for unparseable ID without fallback", func() {
		workoutAt, source, reliable := timeline.ResolveWorkoutAt("completely-random-string", nil, nil)
		Expect(reliable).To(BeFalse())
		Expect(source).To(BeEmpty())
		Expect(workoutAt.IsZero()).To(BeTrue())
	})
})
