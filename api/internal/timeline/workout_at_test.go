package timeline_test

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/wod-strategist/api/internal/timeline"
)

func TestResolveWorkoutAt_CurrentFormat(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 30, 0, 0, time.UTC)
	entropy := rand.Reader
	u := ulid.MustNew(ulid.Timestamp(now), entropy)

	sessionID := fmt.Sprintf("WOD-20260907-%s", u.String())
	baseline := now.Add(1 * time.Minute)

	workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
	if !reliable {
		t.Errorf("expected reliable=true, got false")
	}
	if source != timeline.WorkoutAtSourceSessionULID {
		t.Errorf("expected source %s, got %s", timeline.WorkoutAtSourceSessionULID, source)
	}
	if workoutAt.UnixMilli() != now.UnixMilli() {
		t.Errorf("expected timestamp %v, got %v", now, workoutAt)
	}
}

func TestResolveWorkoutAt_WarmupType(t *testing.T) {
	now := time.Date(2026, 4, 7, 8, 15, 0, 0, time.UTC)
	u := ulid.MustNew(ulid.Timestamp(now), rand.Reader)

	sessionID := fmt.Sprintf("WARMUP-20260407-%s", u.String())
	baseline := now.Add(2 * time.Minute)

	workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, &baseline, nil)
	if !reliable || source != timeline.WorkoutAtSourceSessionULID {
		t.Errorf("expected reliable session_ulid, got %s, reliable=%v", source, reliable)
	}
	if workoutAt.UnixMilli() != now.UnixMilli() {
		t.Errorf("expected %v, got %v", now, workoutAt)
	}
}

func TestResolveWorkoutAt_PrevServerFormat(t *testing.T) {
	now := time.Date(2026, 8, 1, 14, 20, 0, 0, time.UTC)
	u := ulid.MustNew(ulid.Timestamp(now), rand.Reader)

	sessionID := fmt.Sprintf("WOD-202608011420-%s", u.String())
	baseline := now.Add(30 * time.Second)

	workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
	if !reliable || source != timeline.WorkoutAtSourceSessionULID {
		t.Errorf("expected reliable session_ulid, got %s, reliable=%v", source, reliable)
	}
	if workoutAt.UnixMilli() != now.UnixMilli() {
		t.Errorf("expected %v, got %v", now, workoutAt)
	}
}

func TestResolveWorkoutAt_LegacyFormat(t *testing.T) {
	sessionID := "P42-WOD-2026-09-07-15-30"
	loc, _ := time.LoadLocation("Asia/Seoul")
	expectedUTC := time.Date(2026, 9, 7, 15, 30, 0, 0, loc).UTC()
	baseline := expectedUTC.Add(10 * time.Minute)

	workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
	if !reliable {
		t.Errorf("expected reliable=true for legacy local time")
	}
	if source != timeline.WorkoutAtSourceLegacyLocalTime {
		t.Errorf("expected source %s, got %s", timeline.WorkoutAtSourceLegacyLocalTime, source)
	}
	if !workoutAt.Equal(expectedUTC) {
		t.Errorf("expected %v, got %v", expectedUTC, workoutAt)
	}
}

func TestResolveWorkoutAt_InvalidDateFallsThrough(t *testing.T) {
	// Month 13 is invalid
	u := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)
	sessionID := fmt.Sprintf("WOD-20261301-%s", u.String())

	createdAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, &createdAt, nil)
	if reliable {
		t.Errorf("expected fallback to be unreliable")
	}
	if source != timeline.WorkoutAtSourceSessionCreatedFallback {
		t.Errorf("expected %s, got %s", timeline.WorkoutAtSourceSessionCreatedFallback, source)
	}
	if !workoutAt.Equal(createdAt) {
		t.Errorf("expected %v, got %v", createdAt, workoutAt)
	}
}

func TestResolveWorkoutAt_FutureDateFallsThrough(t *testing.T) {
	// ULID timestamp is 1 hour ahead of baseline created_at -> exceeds 5 minute threshold
	baseline := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	futureTime := baseline.Add(1 * time.Hour)
	u := ulid.MustNew(ulid.Timestamp(futureTime), rand.Reader)
	sessionID := fmt.Sprintf("WOD-20260907-%s", u.String())

	workoutAt, source, reliable := timeline.ResolveWorkoutAt(sessionID, nil, &baseline)
	if reliable {
		t.Errorf("expected future timestamp to be rejected and fall back to baseline")
	}
	if source != timeline.WorkoutAtSourceAnalysisCreatedFallback {
		t.Errorf("expected %s, got %s", timeline.WorkoutAtSourceAnalysisCreatedFallback, source)
	}
	if !workoutAt.Equal(baseline) {
		t.Errorf("expected %v, got %v", baseline, workoutAt)
	}
}

func TestResolveWorkoutAt_UnparseableNoFallback(t *testing.T) {
	workoutAt, source, reliable := timeline.ResolveWorkoutAt("completely-random-string", nil, nil)
	if reliable || source != "" || !workoutAt.IsZero() {
		t.Errorf("expected zero result for unparseable ID with no fallback, got %v, %s, %v", workoutAt, source, reliable)
	}
}
