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

func (r *GormAnalysisResultRepository) ListRecent(ctx context.Context, limit int) ([]db.AnalysisResult, error) {
	if r.db == nil {
		return nil, errRepositoryNotConfigured
	}

	var results []db.AnalysisResult
	if err := r.db.WithContext(ctx).Order("created_at desc").Limit(limit).Find(&results).Error; err != nil {
		return nil, err
	}

	return results, nil
}
