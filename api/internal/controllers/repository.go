package controllers

import (
	"context"
	"errors"

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

func (r *GormAnalysisResultRepository) FindBySessionID(ctx context.Context, sessionID string) ([]db.AnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.AnalysisResult
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&results).Error; err != nil {
		return nil, err
	}

	return results, nil
}

func (r *GormAnalysisResultRepository) ListRecent(ctx context.Context, limit int, profileID uint) ([]db.AnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.AnalysisResult
	q := r.db.WithContext(ctx).Order("created_at desc").Limit(limit)
	if profileID > 0 {
		q = q.Where("profile_id = ?", profileID)
	}
	if err := q.Find(&results).Error; err != nil {
		return nil, err
	}

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

