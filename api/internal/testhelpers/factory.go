package testhelpers

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	g "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func InitDB() (*gorm.DB, error) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgresql://sunjinlee@localhost:5432/wod_test?sslmode=disable"
	}

	gormConfig := &gorm.Config{}
	if os.Getenv("SHOW_LOG") == "true" || os.Getenv("SHOW_SQL_LOG") == "true" {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Info)
	} else {
		gormConfig.Logger = gormlogger.Discard
	}

	gdb, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("testDB: open: %w", err)
	}

	return gdb, nil
}

func CleanupDB(db *gorm.DB) {
	var dbName string
	if err := db.Raw("SELECT current_database()").Scan(&dbName).Error; err != nil {
		panic(fmt.Sprintf("failed to get current database name: %v", err))
	}

	// if database name is not end with _test, panic
	g.Expect(strings.HasSuffix(dbName, "_test")).To(g.BeTrue(),
		"database name '%s' does not end with _test, skipping cleanup to prevent data loss", dbName)

	var tables []string

	err := db.Raw("SELECT tablename FROM pg_tables WHERE schemaname = 'public'").Scan(&tables).Error
	g.Expect(err).NotTo(g.HaveOccurred())

	if len(tables) == 0 {
		return
	}

	for _, table := range tables {
		if table == "spatial_ref_sys" || table == "schema_migrations" {
			continue
		}

		query := fmt.Sprintf("TRUNCATE TABLE \"%s\" RESTART IDENTITY CASCADE", table)
		err := db.Exec(query).Error
		g.Expect(err).NotTo(g.HaveOccurred(), "Failed to truncate table: "+table)
	}
}

func CreateUser(dbConn *gorm.DB, userAttr *db.User) db.User {
	u := db.User{
		Username:     userAttr.Username,
		PasswordHash: userAttr.PasswordHash,
		DeletedAt:    userAttr.DeletedAt,
	}

	g.Expect(dbConn.Create(&u).Error).NotTo(g.HaveOccurred())
	return u
}

var userCounter uint64

func CreateProfile(dbConn *gorm.DB, profileAttr *db.Profile) db.Profile {
	p := db.Profile{
		UserID:       profileAttr.UserID,
		Name:         profileAttr.Name,
		BirthYear:    profileAttr.BirthYear,
		BirthMonth:   profileAttr.BirthMonth,
		BirthDay:     profileAttr.BirthDay,
		Gender:       profileAttr.Gender,
		HeightCm:     profileAttr.HeightCm,
		WeightKg:     profileAttr.WeightKg,
		FitnessLevel: profileAttr.FitnessLevel,
		Injuries:     profileAttr.Injuries,
		ArchivedAt:   profileAttr.ArchivedAt,
	}

	if p.UserID == 0 {
		val := atomic.AddUint64(&userCounter, 1)
		u := CreateUser(dbConn, &db.User{
			Username: "test-user-" + strconv.FormatUint(val, 10),
		})
		p.UserID = u.ID
	}

	g.Expect(dbConn.Create(&p).Error).NotTo(g.HaveOccurred())
	return p
}

func CreateSession(dbConn *gorm.DB, sessionAttr *db.Session) db.Session {
	s := db.Session{
		SessionID:      sessionAttr.SessionID,
		ProfileID:      sessionAttr.ProfileID,
		Status:         sessionAttr.Status,
		IdempotencyKey: sessionAttr.IdempotencyKey,
		WODDescription: sessionAttr.WODDescription,
		MovementHints:  sessionAttr.MovementHints,
		WorkoutType:    sessionAttr.WorkoutType,
	}

	if s.IdempotencyKey == "" {
		s.IdempotencyKey = uuid.NewString()
	}
	if len(s.MovementHints) == 0 {
		s.MovementHints = db.JSONDocument(`[]`)
	}

	g.Expect(dbConn.Create(&s).Error).NotTo(g.HaveOccurred())
	return s
}

func CreateAnalysisResult(dbConn *gorm.DB, resultAttr *db.AnalysisResult) db.AnalysisResult {
	result := db.AnalysisResult{
		SessionID:           resultAttr.SessionID,
		ProfileID:           resultAttr.ProfileID,
		AnalysisType:        resultAttr.AnalysisType,
		Status:              resultAttr.Status,
		Output:              resultAttr.Output,
		GeminiFileURI:       resultAttr.GeminiFileURI,
		GeminiFileName:      resultAttr.GeminiFileName,
		GeminiMIMEType:      resultAttr.GeminiMIMEType,
		GeminiFileExpiresAt: resultAttr.GeminiFileExpiresAt,
		HighlightSegments:   resultAttr.HighlightSegments,
		WODDescription:      resultAttr.WODDescription,
		SessionScore:           resultAttr.SessionScore,
		NormalizedWorkout:      resultAttr.NormalizedWorkout,
		MobilityObservations:   resultAttr.MobilityObservations,
		StretchRecommendations: resultAttr.StretchRecommendations,
		ArchivedAt:              resultAttr.ArchivedAt,
	}
	if result.AnalysisType == "" {
		result.AnalysisType = db.AnalysisTypeWOD
	}
	if result.MobilityObservations == "" {
		result.MobilityObservations = "[]"
	}
	if result.StretchRecommendations == "" {
		result.StretchRecommendations = "[]"
	}

	g.Expect(dbConn.Create(&result).Error).NotTo(g.HaveOccurred())

	if !resultAttr.CreatedAt.IsZero() {
		g.Expect(dbConn.Model(&result).UpdateColumn("created_at", resultAttr.CreatedAt).Error).NotTo(g.HaveOccurred())
		result.CreatedAt = resultAttr.CreatedAt
	}

	return result
}

func CreateChunkAnalysisResult(dbConn *gorm.DB, resultAttr *db.ChunkAnalysisResult) db.ChunkAnalysisResult {
	result := db.ChunkAnalysisResult{
		SessionID:         resultAttr.SessionID,
		ProfileID:         resultAttr.ProfileID,
		FilePath:          resultAttr.FilePath,
		ExerciseType:      resultAttr.ExerciseType,
		Status:            resultAttr.Status,
		Output:            resultAttr.Output,
		ObservedSignals:   resultAttr.ObservedSignals,
		HeartRateBPM:      resultAttr.HeartRateBPM,
		StartSecs:         resultAttr.StartSecs,
		EndSecs:           resultAttr.EndSecs,
		MediaStartSecs:    resultAttr.MediaStartSecs,
		MediaEndSecs:      resultAttr.MediaEndSecs,
		WorkoutConfidence: resultAttr.WorkoutConfidence,
		MotionScore:       resultAttr.MotionScore,
		SkipReason:        resultAttr.SkipReason,
	}

	g.Expect(dbConn.Create(&result).Error).NotTo(g.HaveOccurred())
	return result
}

func CreateAnalysisFeedback(dbConn *gorm.DB, feedbackAttr *db.AnalysisFeedback) db.AnalysisFeedback {
	feedback := db.AnalysisFeedback{
		FeedbackKey:           feedbackAttr.FeedbackKey,
		ProfileID:             feedbackAttr.ProfileID,
		SessionID:             feedbackAttr.SessionID,
		TargetType:            feedbackAttr.TargetType,
		ChunkAnalysisResultID: feedbackAttr.ChunkAnalysisResultID,
		Category:              feedbackAttr.Category,
		OriginalPrediction:    feedbackAttr.OriginalPrediction,
		Correction:            feedbackAttr.Correction,
		Note:                  feedbackAttr.Note,
		ConsentToImprove:      feedbackAttr.ConsentToImprove,
		ClientRequestID:       feedbackAttr.ClientRequestID,
		Revision:              feedbackAttr.Revision,
		SupersedesFeedbackID:  feedbackAttr.SupersedesFeedbackID,
		Retracted:             feedbackAttr.Retracted,
		ReanalysisRunID:       feedbackAttr.ReanalysisRunID,
	}
	if feedback.FeedbackKey == "" {
		feedback.FeedbackKey = uuid.NewString()
	}
	if feedback.ClientRequestID == "" {
		feedback.ClientRequestID = uuid.NewString()
	}
	if feedback.Revision == 0 {
		feedback.Revision = 1
	}
	if len(feedback.OriginalPrediction) == 0 {
		feedback.OriginalPrediction = db.JSONDocument(`{}`)
	}
	if len(feedback.Correction) == 0 {
		feedback.Correction = db.JSONDocument(`{}`)
	}

	g.Expect(dbConn.Create(&feedback).Error).NotTo(g.HaveOccurred())
	return feedback
}

func CreateChunkReanalysisRun(dbConn *gorm.DB, runAttr *db.ChunkReanalysisRun) db.ChunkReanalysisRun {
	run := db.ChunkReanalysisRun{
		SessionID:                  runAttr.SessionID,
		ProfileID:                  runAttr.ProfileID,
		ChunkAnalysisResultID:      runAttr.ChunkAnalysisResultID,
		ClientRequestID:            runAttr.ClientRequestID,
		TaskID:                     runAttr.TaskID,
		Status:                     runAttr.Status,
		SourceKind:                 runAttr.SourceKind,
		SourceGCSURI:               runAttr.SourceGCSURI,
		SourceContextSnapshot:      runAttr.SourceContextSnapshot,
		OriginalPredictionSnapshot: runAttr.OriginalPredictionSnapshot,
		MediaStartSecs:             runAttr.MediaStartSecs,
		MediaEndSecs:               runAttr.MediaEndSecs,
		Model:                      runAttr.Model,
		PromptVersion:              runAttr.PromptVersion,
		PromptHash:                 runAttr.PromptHash,
		SchemaVersion:              runAttr.SchemaVersion,
		RawOutput:                  runAttr.RawOutput,
		StructuredCandidate:        runAttr.StructuredCandidate,
		PromptTokens:               runAttr.PromptTokens,
		CandidateTokens:            runAttr.CandidateTokens,
		TotalTokens:                runAttr.TotalTokens,
		DurationMs:                 runAttr.DurationMs,
		SafeError:                  runAttr.SafeError,
		GeminiFileURI:              runAttr.GeminiFileURI,
		GeminiFileName:             runAttr.GeminiFileName,
		GeminiMIMEType:             runAttr.GeminiMIMEType,
		GeminiFileExpiresAt:        runAttr.GeminiFileExpiresAt,
		StartedAt:                  runAttr.StartedAt,
		CompletedAt:                runAttr.CompletedAt,
		CreatedAt:                  runAttr.CreatedAt,
		UpdatedAt:                  runAttr.UpdatedAt,
	}
	if run.ClientRequestID == "" {
		run.ClientRequestID = uuid.NewString()
	}
	if run.Status == "" {
		run.Status = db.ChunkReanalysisStatusQueued
	}
	if len(run.SourceContextSnapshot) == 0 {
		run.SourceContextSnapshot = db.JSONDocument(`{}`)
	}
	if len(run.OriginalPredictionSnapshot) == 0 {
		run.OriginalPredictionSnapshot = db.JSONDocument(`{}`)
	}
	if len(run.StructuredCandidate) == 0 {
		run.StructuredCandidate = db.JSONDocument(`{}`)
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}

	g.Expect(dbConn.Create(&run).Error).NotTo(g.HaveOccurred())
	return run
}

func CreateSessionReanalysisRun(dbConn *gorm.DB, runAttr *db.SessionReanalysisRun) db.SessionReanalysisRun {
	run := db.SessionReanalysisRun{
		SessionID:                runAttr.SessionID,
		ProfileID:                runAttr.ProfileID,
		ClientRequestID:          runAttr.ClientRequestID,
		TaskID:                   runAttr.TaskID,
		Status:                   runAttr.Status,
		SourceGCSURI:             runAttr.SourceGCSURI,
		SourceContextSnapshot:    runAttr.SourceContextSnapshot,
		OriginalAnalysisSnapshot: runAttr.OriginalAnalysisSnapshot,
		Output:                   runAttr.Output,
		HighlightSegments:        runAttr.HighlightSegments,
		SessionScore:             runAttr.SessionScore,
		WorkoutType:              runAttr.WorkoutType,
		Model:                    runAttr.Model,
		PromptVersion:            runAttr.PromptVersion,
		PromptHash:               runAttr.PromptHash,
		SchemaVersion:            runAttr.SchemaVersion,
		PromptTokens:             runAttr.PromptTokens,
		CandidateTokens:          runAttr.CandidateTokens,
		TotalTokens:              runAttr.TotalTokens,
		DurationMs:               runAttr.DurationMs,
		SafeError:                runAttr.SafeError,
		GeminiFileURI:            runAttr.GeminiFileURI,
		GeminiFileName:           runAttr.GeminiFileName,
		GeminiMIMEType:           runAttr.GeminiMIMEType,
		GeminiFileExpiresAt:      runAttr.GeminiFileExpiresAt,
		StartedAt:                runAttr.StartedAt,
		CompletedAt:              runAttr.CompletedAt,
		CreatedAt:                runAttr.CreatedAt,
		UpdatedAt:                runAttr.UpdatedAt,
	}
	if run.ClientRequestID == "" {
		run.ClientRequestID = uuid.NewString()
	}
	if run.Status == "" {
		run.Status = db.SessionReanalysisStatusQueued
	}
	if len(run.SourceContextSnapshot) == 0 {
		run.SourceContextSnapshot = db.JSONDocument(`{}`)
	}
	if len(run.OriginalAnalysisSnapshot) == 0 {
		run.OriginalAnalysisSnapshot = db.JSONDocument(`{}`)
	}
	if run.SessionScore == "" {
		run.SessionScore = `{}`
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}

	g.Expect(dbConn.Create(&run).Error).NotTo(g.HaveOccurred())
	return run
}

var stretchCounter uint64

func CreateStretch(dbConn *gorm.DB, stretchAttr *db.Stretch) db.Stretch {
	val := atomic.AddUint64(&stretchCounter, 1)
	s := db.Stretch{
		Name:         stretchAttr.Name,
		TargetArea:   stretchAttr.TargetArea,
		Description:  stretchAttr.Description,
		DurationHint: stretchAttr.DurationHint,
		Caution:      stretchAttr.Caution,
		ImageObject:  stretchAttr.ImageObject,
		VideoObject:  stretchAttr.VideoObject,
	}

	if s.Name == "" {
		s.Name = fmt.Sprintf("Test Stretch %d", val)
	}
	s.NormalizedKey = db.NormalizeStretchKey(s.Name)
	if s.TargetArea == "" {
		s.TargetArea = "Hips & Glutes"
	}

	g.Expect(dbConn.Create(&s).Error).NotTo(g.HaveOccurred())
	return s
}

var stretchAliasCounter uint64

func CreateStretchAlias(dbConn *gorm.DB, aliasAttr *db.StretchAlias) db.StretchAlias {
	val := atomic.AddUint64(&stretchAliasCounter, 1)
	a := db.StretchAlias{
		StretchID: aliasAttr.StretchID,
		Alias:     aliasAttr.Alias,
	}

	g.Expect(a.StretchID).NotTo(g.BeZero(), "StretchID is required for CreateStretchAlias")
	if a.Alias == "" {
		a.Alias = fmt.Sprintf("Test Alias %d", val)
	}
	a.NormalizedKey = db.NormalizeStretchKey(a.Alias)

	g.Expect(dbConn.Create(&a).Error).NotTo(g.HaveOccurred())
	return a
}

