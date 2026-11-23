package session

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/wodeval"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CreateSessionAttr struct {
	ProfileID      uint
	SessionID      string
	WorkoutType    string
	IdempotencyKey string
	WODDescription string
	Movements      []string
}

var canonicalSessionIDPattern = regexp.MustCompile(`^(WOD|WARMUP|ACCESSORY|COOLDOWN)-(\d{8})-([0-9A-HJKMNP-TV-Z]{26})$`)

var ErrInvalidSessionID = errors.New("invalid session ID")
var ErrProfileNotFound = errors.New("profile not found")
var ErrWorkoutTypeMismatch = errors.New("workout type mismatch")
var ErrSessionProfileConflict = errors.New("session profile conflict")
var ErrInvalidProfileID = errors.New("invalid profile ID")
var ErrIdempotencyConflict = errors.New("idempotency key conflict")

const WorkoutTypeWOD string = "wod"
const WorkoutTypeWARMUP string = "warmup"
const WorkoutTypeACCESSORY string = "accessory"
const WorkoutTypeCOOLDOWN string = "cooldown"

func Create(ctx context.Context, dbConn *gorm.DB, input CreateSessionAttr) (*db.Session, error) {
	if input.ProfileID <= 0 {
		return nil, ErrInvalidProfileID
	}

	var profile *db.Profile
	if err := dbConn.Model(&db.Profile{}).Where("id = ?", input.ProfileID).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProfileNotFound
		}
		return nil, err
	}

	sessionID := input.SessionID
	idempotencyKey := strings.TrimSpace(strings.ToLower(input.IdempotencyKey))

	// check if session ID already exists
	existingSessionByID := &db.Session{}
	if sessionID != "" {
		err := dbConn.Where("session_id = ?", sessionID).First(&existingSessionByID).Error
		if err == nil {
			if existingSessionByID.ProfileID != input.ProfileID {
				return nil, ErrSessionProfileConflict
			}
		}

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	// check if idempotency key already exists
	existingSessionByKey := &db.Session{}
	if idempotencyKey != "" {
		err := dbConn.Where("idempotency_key = ? AND profile_id = ?", idempotencyKey, input.ProfileID).First(&existingSessionByKey).Error
		if err == nil {
			if sessionID != "" && existingSessionByKey.SessionID != sessionID {
				return nil, ErrIdempotencyConflict
			}

			return existingSessionByKey, nil
		}

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	if existingSessionByID.ID > 0 {
		existingIdempotencyKey := strings.TrimSpace(strings.ToLower(existingSessionByID.IdempotencyKey))
		if idempotencyKey == "" && existingIdempotencyKey == "" {
			return existingSessionByID, nil
		}

		if idempotencyKey != "" && existingIdempotencyKey != "" {
			if idempotencyKey == existingIdempotencyKey {
				return existingSessionByID, nil
			}

			return nil, ErrIdempotencyConflict
		}

		// don't allow to update idempotency key from non-empty to empty
		if idempotencyKey == "" && existingIdempotencyKey != "" {
			return nil, ErrIdempotencyConflict
		}

		// check if idempotency key is already bound to a different session
		if existingSessionByKey.ID > 0 {
			if existingSessionByID.ID != existingSessionByKey.ID {
				return nil, ErrIdempotencyConflict
			}
		}

		// update idempotency key to the new value if it was empty
		result := dbConn.WithContext(ctx).Model(&db.Session{}).
			Where("id = ? AND idempotency_key = ''", existingSessionByID.ID).
			Update("idempotency_key", idempotencyKey)

		if result.Error != nil {
			return nil, result.Error
		}

		if result.RowsAffected > 0 {
			return existingSessionByID, nil
		}
	}

	wodType := input.WorkoutType
	if sessionID == "" {
		wodType = normalizeWorkoutType(wodType)
		sessionID = strings.ToUpper(string(wodType)) + "-" + time.Now().Format("20060102") + "-" + ulid.Make().String()
	} else {
		wodType = strings.TrimSpace(strings.ToLower(wodType))
		matchedWodType, err := validateCanonicalSessionID(sessionID, wodType)
		if err != nil {
			return nil, err
		}
		if wodType == "" {
			wodType = normalizeWorkoutType(matchedWodType)
		}
	}

	movements := NormalizeMovementHints(input.Movements)
	wodDescription := strings.TrimSpace(input.WODDescription)
	if wodDescription == "" && len(input.Movements) > 0 {
		wodDescription = "Individual movements: " + strings.Join(movements, ",")
	}

	var err error
	var movementHints []byte

	if len(movements) > 0 {
		movementHints, err = json.Marshal(movements)
		if err != nil {
			return nil, err
		}
	} else {
		movementHints = []byte("[]")
	}

	wodVersion := wodeval.Version
	if wodType != WorkoutTypeWOD {
		wodVersion = 0
	}

	newSession := &db.Session{
		ProfileID:            input.ProfileID,
		SessionID:            sessionID,
		WorkoutType:          string(wodType),
		WODDescription:       wodDescription,
		WODEvaluationVersion: wodVersion,
		Status:               db.SessionStatusStarted,
		MovementHints:        db.JSONDocument(movementHints),
		IdempotencyKey:       idempotencyKey,
	}

	result := dbConn.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(newSession)
	if err := result.Error; err != nil {
		return nil, err
	}

	if result.RowsAffected > 0 {
		return newSession, nil
	}

	existingSession := &db.Session{}
	if idempotencyKey != "" {
		err = dbConn.Where("idempotency_key = ? AND profile_id = ?", idempotencyKey, input.ProfileID).First(&existingSession).Error
	} else if sessionID != "" {
		err = dbConn.Where("session_id = ? AND profile_id = ?", sessionID, input.ProfileID).First(&existingSession).Error
	}

	if err != nil {
		return nil, err
	}

	return existingSession, nil
}

func validateCanonicalSessionID(sessionID string, workoutType string) (string, error) {
	matches := canonicalSessionIDPattern.FindStringSubmatch(sessionID)
	if len(matches) != 4 {
		return "", ErrInvalidSessionID
	}

	matchedWorkoutType := matches[1]
	date := matches[2]
	ulidStr := matches[3]

	if workoutType != "" && strings.ToLower(workoutType) != strings.ToLower(matchedWorkoutType) {
		return "", ErrWorkoutTypeMismatch
	}

	_, err := time.Parse("20060102", date)
	if err != nil {
		return "", ErrInvalidSessionID
	}

	_, err = ulid.ParseStrict(ulidStr)
	if err != nil {
		return "", ErrInvalidSessionID
	}

	return matchedWorkoutType, nil
}

func normalizeWorkoutType(workoutType string) string {
	switch strings.ToLower(strings.TrimSpace(workoutType)) {
	case "", "wod":
		return WorkoutTypeWOD
	case "warmup":
		return WorkoutTypeWARMUP
	case "accessory":
		return WorkoutTypeACCESSORY
	case "cooldown":
		return WorkoutTypeCOOLDOWN
	}
	return WorkoutTypeWOD
}
