package timeline

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	WorkoutAtSourceSessionULID             = "session_ulid"
	WorkoutAtSourceLegacyLocalTime         = "legacy_local_time"
	WorkoutAtSourceSessionCreatedFallback  = "session_created_fallback"
	WorkoutAtSourceAnalysisCreatedFallback = "analysis_created_fallback"
)

var (
	// Current format: {TYPE}-YYYYMMDD-{ULID}
	currentSessionPattern = regexp.MustCompile(`^(?i)(WOD|WARMUP|ACCESSORY|COOLDOWN)-(\d{4})(\d{2})(\d{2})-([0123456789ABCDEFGHJKMNPQRSTVWXYZ]{26})$`)
	// Previous server format: WOD-YYYYMMDDHHMM-{ULID}
	prevServerSessionPattern = regexp.MustCompile(`^(?i)WOD-(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})-([0123456789ABCDEFGHJKMNPQRSTVWXYZ]{26})$`)
	// Legacy format: P{id}-WOD-YYYY-MM-DD-HH-MM
	legacySessionPattern = regexp.MustCompile(`^(?i)P\d+-(?:WOD|WARMUP|ACCESSORY|COOLDOWN|\w+)-(\d{4})-(\d{2})-(\d{2})-(\d{2})-(\d{2})$`)
)

func isValidDate(year, month, day int) bool {
	if year < 2000 || year > 2100 || month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t.Year() == year && int(t.Month()) == month && t.Day() == day
}

func isValidDateTime(year, month, day, hour, min int) bool {
	if !isValidDate(year, month, day) || hour < 0 || hour > 23 || min < 0 || min > 59 {
		return false
	}
	return true
}

func getLegacyLocation() *time.Location {
	tz := strings.TrimSpace(os.Getenv("LEGACY_WORKOUT_TIMEZONE"))
	if tz == "" {
		tz = "Asia/Seoul"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.FixedZone("KST", 9*60*60)
	}
	return loc
}

// ResolveWorkoutAt determines the canonical workout timestamp, source, and whether
// the timestamp is reliable for readiness evaluation.
// Priority:
// 1. Current {TYPE}-YYYYMMDD-{ULID} or WOD-YYYYMMDDHHMM-{ULID} (UTC from ULID 48-bit time) -> session_ulid, reliable=true
// 2. Legacy P{id}-WOD-YYYY-MM-DD-HH-MM (interpreted in LEGACY_WORKOUT_TIMEZONE, default Asia/Seoul) -> legacy_local_time, reliable=true
// 3. Fallback to sessionCreatedAt -> session_created_fallback, reliable=false
// 4. Fallback to analysisCreatedAt -> analysis_created_fallback, reliable=false
//
// Dates with invalid months/days, ULID overflow, or times > 5 minutes in the future relative to
// the persisted baseline created_at fall through to the next candidate.
func ResolveWorkoutAt(
	sessionID string,
	sessionCreatedAt *time.Time,
	analysisCreatedAt *time.Time,
) (time.Time, string, bool) {
	trimmedID := strings.TrimSpace(sessionID)

	var baseline time.Time
	if sessionCreatedAt != nil && !sessionCreatedAt.IsZero() {
		baseline = *sessionCreatedAt
	} else if analysisCreatedAt != nil && !analysisCreatedAt.IsZero() {
		baseline = *analysisCreatedAt
	}

	maxFuture := time.Time{}
	if !baseline.IsZero() {
		maxFuture = baseline.Add(5 * time.Minute)
	}

	// 1. Candidate 1: ULID based formats
	if match := currentSessionPattern.FindStringSubmatch(trimmedID); len(match) == 6 {
		year, _ := strconv.Atoi(match[2])
		month, _ := strconv.Atoi(match[3])
		day, _ := strconv.Atoi(match[4])
		ulidStr := match[5]

		if isValidDate(year, month, day) {
			parsedULID, err := ulid.Parse(ulidStr)
			if err == nil {
				ulidTime := time.UnixMilli(int64(parsedULID.Time())).UTC()
				// Check overflow / negative or future limit
				if maxFuture.IsZero() || !ulidTime.After(maxFuture) {
					return ulidTime, WorkoutAtSourceSessionULID, true
				}
			}
		}
	} else if match := prevServerSessionPattern.FindStringSubmatch(trimmedID); len(match) == 7 {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		hour, _ := strconv.Atoi(match[4])
		min, _ := strconv.Atoi(match[5])
		ulidStr := match[6]

		if isValidDateTime(year, month, day, hour, min) {
			parsedULID, err := ulid.Parse(ulidStr)
			if err == nil {
				ulidTime := time.UnixMilli(int64(parsedULID.Time())).UTC()
				if maxFuture.IsZero() || !ulidTime.After(maxFuture) {
					return ulidTime, WorkoutAtSourceSessionULID, true
				}
			}
		}
	}

	// 2. Candidate 2: Legacy local time format
	if match := legacySessionPattern.FindStringSubmatch(trimmedID); len(match) == 6 {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		hour, _ := strconv.Atoi(match[4])
		min, _ := strconv.Atoi(match[5])

		if isValidDateTime(year, month, day, hour, min) {
			loc := getLegacyLocation()
			legacyTime := time.Date(year, time.Month(month), day, hour, min, 0, 0, loc).UTC()
			if maxFuture.IsZero() || !legacyTime.After(maxFuture) {
				return legacyTime, WorkoutAtSourceLegacyLocalTime, true
			}
		}
	}

	// 3. Candidate 3: Fallbacks (unreliable for readiness)
	if sessionCreatedAt != nil && !sessionCreatedAt.IsZero() {
		return sessionCreatedAt.UTC(), WorkoutAtSourceSessionCreatedFallback, false
	}
	if analysisCreatedAt != nil && !analysisCreatedAt.IsZero() {
		return analysisCreatedAt.UTC(), WorkoutAtSourceAnalysisCreatedFallback, false
	}

	return time.Time{}, "", false
}
