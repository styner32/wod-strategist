package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

var errRepositoryNotConfigured = errors.New("analysis result repository is not configured")

type GormAnalysisResultRepository struct {
	db *gorm.DB
}

func NewGormAnalysisResultRepository(dbConn *gorm.DB) *GormAnalysisResultRepository {
	return &GormAnalysisResultRepository{db: dbConn}
}

func (r *GormAnalysisResultRepository) populateMissingWODDescriptions(ctx context.Context, results []db.AnalysisResult) {
	if r.db == nil || len(results) == 0 {
		return
	}
	missingSessionIDs := make([]string, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for i := range results {
		if strings.TrimSpace(results[i].WODDescription) != "" {
			continue
		}
		sid := strings.TrimSpace(results[i].SessionID)
		if sid == "" {
			continue
		}
		if _, exists := seen[sid]; !exists {
			seen[sid] = struct{}{}
			missingSessionIDs = append(missingSessionIDs, sid)
		}
	}
	if len(missingSessionIDs) == 0 {
		return
	}

	var sessions []db.Session
	if err := r.db.WithContext(ctx).
		Select("session_id", "profile_id", "wod_description").
		Where("session_id IN ? AND wod_description <> ''", missingSessionIDs).
		Find(&sessions).Error; err != nil {
		return
	}
	if len(sessions) == 0 {
		return
	}

	// A sessions row describes only its own profile's workout; legacy IDs
	// without a profile prefix can repeat across profiles.
	type sessionKey struct {
		sessionID string
		profileID uint
	}
	descriptions := make(map[sessionKey]string, len(sessions))
	for _, s := range sessions {
		if desc := strings.TrimSpace(s.WODDescription); desc != "" {
			descriptions[sessionKey{s.SessionID, s.ProfileID}] = desc
		}
	}

	for i := range results {
		if strings.TrimSpace(results[i].WODDescription) != "" {
			continue
		}
		if desc, ok := descriptions[sessionKey{strings.TrimSpace(results[i].SessionID), results[i].ProfileID}]; ok {
			results[i].WODDescription = desc
		}
	}
}

func (r *GormAnalysisResultRepository) FindBySessionID(ctx context.Context, sessionID string) ([]db.AnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.AnalysisResult
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&results).Error; err != nil {
		return nil, err
	}
	r.populateMissingWODDescriptions(ctx, results)

	return results, nil
}

func (r *GormAnalysisResultRepository) ListRecent(ctx context.Context, limit int, profileID uint, beforeID uint) ([]db.AnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.AnalysisResult
	q := r.db.WithContext(ctx).Where("archived_at IS NULL").Order("id desc").Limit(limit)
	if profileID > 0 {
		q = q.Where("profile_id = ?", profileID)
	}
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	if err := q.Find(&results).Error; err != nil {
		return nil, err
	}
	r.populateMissingWODDescriptions(ctx, results)

	return results, nil
}

func (r *GormAnalysisResultRepository) ListByDateRange(ctx context.Context, profileID uint, from, to time.Time) ([]db.AnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.AnalysisResult
	q := r.db.WithContext(ctx).Where("archived_at IS NULL").Where("profile_id = ?", profileID).Order("created_at desc")
	q = q.Where("created_at >= ?", from)
	q = q.Where("created_at < ?", to)
	if err := q.Find(&results).Error; err != nil {
		return nil, err
	}
	r.populateMissingWODDescriptions(ctx, results)

	return results, nil
}

func (r *GormAnalysisResultRepository) FindChunksBySessionID(ctx context.Context, sessionID string) ([]db.ChunkAnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.ChunkAnalysisResult
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at desc").Find(&results).Error; err != nil {
		return nil, err
	}

	return results, nil
}

func (r *GormAnalysisResultRepository) Archive(ctx context.Context, id uint) error {
	if r.db == nil {
		return errRepositoryNotConfigured
	}
	now := time.Now()
	return r.db.WithContext(ctx).Model(&db.AnalysisResult{}).Where("id = ?", id).Update("archived_at", now).Error
}

func (r *GormAnalysisResultRepository) Unarchive(ctx context.Context, id uint) error {
	if r.db == nil {
		return errRepositoryNotConfigured
	}
	return r.db.WithContext(ctx).Model(&db.AnalysisResult{}).Where("id = ?", id).Update("archived_at", nil).Error
}

// ==========================================
// Profile Repository
// ==========================================

var errProfileRepositoryNotConfigured = errors.New("profile repository is not configured")

type GormProfileRepository struct {
	db *gorm.DB
}

func NewGormProfileRepository(dbConn *gorm.DB) *GormProfileRepository {
	return &GormProfileRepository{db: dbConn}
}

func (r *GormProfileRepository) Create(ctx context.Context, profile *db.Profile) error {
	if r.db == nil {
		return errProfileRepositoryNotConfigured
	}
	return r.db.WithContext(ctx).Create(profile).Error
}

func (r *GormProfileRepository) FindByID(ctx context.Context, id uint) (*db.Profile, error) {
	if r.db == nil {
		return nil, errProfileRepositoryNotConfigured
	}
	var profile db.Profile
	if err := r.db.WithContext(ctx).First(&profile, id).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *GormProfileRepository) ListByUser(ctx context.Context, userID uint, includeArchived bool) ([]db.Profile, error) {
	if r.db == nil {
		return nil, errProfileRepositoryNotConfigured
	}
	var profiles []db.Profile
	q := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at asc")
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}
	if err := q.Find(&profiles).Error; err != nil {
		return nil, err
	}
	return profiles, nil
}

func (r *GormProfileRepository) Update(ctx context.Context, profile *db.Profile) error {
	if r.db == nil {
		return errProfileRepositoryNotConfigured
	}
	return r.db.WithContext(ctx).Save(profile).Error
}

func (r *GormProfileRepository) Archive(ctx context.Context, id uint) error {
	if r.db == nil {
		return errProfileRepositoryNotConfigured
	}
	now := time.Now()
	return r.db.WithContext(ctx).Model(&db.Profile{}).Where("id = ?", id).Update("archived_at", now).Error
}

func (r *GormProfileRepository) Unarchive(ctx context.Context, id uint) error {
	if r.db == nil {
		return errProfileRepositoryNotConfigured
	}
	return r.db.WithContext(ctx).Model(&db.Profile{}).Where("id = ?", id).Update("archived_at", nil).Error
}

// ==========================================
// Highlight Result Repository
// ==========================================

var errHighlightRepositoryNotConfigured = errors.New("highlight result repository is not configured")

type GormHighlightResultRepository struct {
	db *gorm.DB
}

func NewGormHighlightResultRepository(dbConn *gorm.DB) *GormHighlightResultRepository {
	return &GormHighlightResultRepository{db: dbConn}
}

func (r *GormHighlightResultRepository) FindBySessionID(ctx context.Context, sessionID string) ([]db.HighlightResult, error) {
	if r.db == nil {
		return nil, errHighlightRepositoryNotConfigured
	}

	var results []db.HighlightResult
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at desc").Find(&results).Error; err != nil {
		return nil, err
	}

	return results, nil
}

func (r *GormHighlightResultRepository) FindByID(ctx context.Context, id uint) (*db.HighlightResult, error) {
	if r.db == nil {
		return nil, errHighlightRepositoryNotConfigured
	}

	var result db.HighlightResult
	if err := r.db.WithContext(ctx).First(&result, id).Error; err != nil {
		return nil, err
	}

	return &result, nil
}
