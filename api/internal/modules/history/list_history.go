package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

// OmittedAnalysisColumns excludes unused JSON/TOAST columns from GET /history
// while retaining sensor_processing and sensor_summary for freshness verification.
var OmittedAnalysisColumns = []string{
	"sensor_timeline",
	"agentic_highlight_analysis",
	"injury_output_alt",
}

type HistoryFilter string

const (
	FilterActive            HistoryFilter = "active"
	FilterArchived          HistoryFilter = "archived"
	FilterInputNeeded       HistoryFilter = "input_needed"
	FilterEffortNeeded      HistoryFilter = "effort_needed"
	FilterPerformanceNeeded HistoryFilter = "performance_needed"
)

var ErrInvalidFilter = errors.New("invalid history filter")

type HistoryListQuery struct {
	ProfileID uint
	Filter    HistoryFilter
	Start     *time.Time
	End       *time.Time
	Limit     uint
	BeforeID  uint
}

func ListByDateRange(ctx context.Context, database *gorm.DB, q HistoryListQuery) ([]db.AnalysisResult, error) {
	query := database.WithContext(ctx).
		Omit(OmittedAnalysisColumns...).
		Where("profile_id = ?", q.ProfileID)

	switch q.Filter {
	case "", FilterActive:
		query = query.Where("archived_at IS NULL")
	case FilterArchived:
		query = query.Where("archived_at IS NOT NULL")
	case FilterInputNeeded, FilterEffortNeeded, FilterPerformanceNeeded:
		query = query.Where("archived_at IS NULL").Where("UPPER(status) = ?", "COMPLETED")
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidFilter, q.Filter)
	}

	results := []db.AnalysisResult{}
	scope := query.Order("created_at desc")
	if q.Start != nil && q.End != nil {
		scope = query.Where("created_at >= ? AND created_at < ?", q.Start, q.End.AddDate(0, 0, 1))
	}

	if q.Limit > 0 {
		scope = scope.Limit(int(q.Limit))
	}

	if q.BeforeID > 0 {
		scope = scope.Where("id < ?", q.BeforeID)
	}

	if err := scope.Find(&results).Error; err != nil {
		return nil, err
	}

	return results, nil
}
