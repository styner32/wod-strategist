package activity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultBackfillBatchSize = 25
	MaxBackfillBatchSize     = 100
)

// DecodeValidCachedSummary validates a stored sessions.activity_summary JSON document
// for direct reuse in GET /api/v1/history without loading chunk_analysis_results.
func DecodeValidCachedSummary(raw []byte) (Summary, bool) {
	if len(raw) == 0 {
		return Summary{}, false
	}
	var s Summary
	if err := json.Unmarshal(raw, &s); err != nil {
		return Summary{}, false
	}
	if s.Version != Version || s.ReviewVersion != Version || strings.TrimSpace(s.SourceVersion) == "" || s.CoverageScope != "recorded_chunks" {
		return Summary{}, false
	}
	switch s.ReviewState {
	case "provisional", "queued", "running", "completed", "partial", "failed", "disabled", "unavailable":
	default:
		return Summary{}, false
	}
	if s.Movements == nil || s.Unassessed == nil {
		return Summary{}, false
	}
	s.Reviews = []ReviewedChunk{}
	return s, true
}

type BackfillOptions struct {
	BatchSize int
	ProfileID uint
	SessionID string
	Apply     bool
}

type BackfillStats struct {
	ScannedSessions int
	AlreadyValid    int
	Updated         int
	WouldUpdate     int
	ChunksLoaded    int
}

// BackfillSummaries recomputes sessions.activity_summary and sets
// sessions.activity_summary_cache_version = SummaryCacheVersion in small
// sequential batches (concurrency = 1) using only DB rows and activity.Build.
// It NEVER touches analysis_results.workout_progress, performance_records, GCS, or Gemini.
func BackfillSummaries(ctx context.Context, conn *gorm.DB, opts BackfillOptions, log *zap.Logger) (BackfillStats, error) {
	var stats BackfillStats
	if conn == nil {
		return stats, nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBackfillBatchSize
	}
	if batchSize > MaxBackfillBatchSize {
		batchSize = MaxBackfillBatchSize
	}

	var lastID uint
	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var batch []db.Session
		q := conn.WithContext(ctx).
			Select("id", "session_id", "profile_id", "activity_summary", "activity_summary_cache_version").
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(batchSize)
		if opts.ProfileID > 0 {
			q = q.Where("profile_id = ?", opts.ProfileID)
		}
		if sid := strings.TrimSpace(opts.SessionID); sid != "" {
			q = q.Where("session_id = ?", sid)
		}
		if err := q.Find(&batch).Error; err != nil {
			return stats, err
		}
		if len(batch) == 0 {
			break
		}
		lastID = batch[len(batch)-1].ID

		candidates := make([]db.Session, 0, len(batch))
		for _, s := range batch {
			stats.ScannedSessions++
			if s.ActivitySummaryCacheVersion == SummaryCacheVersion {
				if _, ok := DecodeValidCachedSummary(s.ActivitySummary); ok {
					stats.AlreadyValid++
					continue
				}
			}
			candidates = append(candidates, s)
		}
		if len(candidates) == 0 {
			continue
		}

		if !opts.Apply {
			for _, cand := range candidates {
				var rows []db.ChunkAnalysisResult
				if err := conn.WithContext(ctx).
					Select("id", "profile_id", "session_id", "file_path", "exercise_type", "status", "start_secs", "end_secs", "media_start_secs", "media_end_secs", "movement_observations").
					Where("profile_id = ? AND session_id = ?", cand.ProfileID, cand.SessionID).
					Find(&rows).Error; err != nil {
					return stats, err
				}
				stats.ChunksLoaded += len(rows)
				var stored Summary
				if len(cand.ActivitySummary) > 0 {
					_ = json.Unmarshal(cand.ActivitySummary, &stored)
				}
				_ = Build(rows, &stored)
				stats.WouldUpdate++
			}
			continue
		}

		for _, cand := range candidates {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var locked db.Session
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Select("id", "session_id", "profile_id", "activity_summary", "activity_summary_cache_version").
					Where("id = ?", cand.ID).
					First(&locked).Error; err != nil {
					return err
				}
				if locked.ActivitySummaryCacheVersion == SummaryCacheVersion {
					if _, ok := DecodeValidCachedSummary(locked.ActivitySummary); ok {
						stats.AlreadyValid++
						return nil
					}
				}
				var rows []db.ChunkAnalysisResult
				if err := tx.
					Select("id", "profile_id", "session_id", "file_path", "exercise_type", "status", "start_secs", "end_secs", "media_start_secs", "media_end_secs", "movement_observations").
					Where("profile_id = ? AND session_id = ?", locked.ProfileID, locked.SessionID).
					Find(&rows).Error; err != nil {
					return err
				}
				stats.ChunksLoaded += len(rows)
				var stored Summary
				if len(locked.ActivitySummary) > 0 {
					_ = json.Unmarshal(locked.ActivitySummary, &stored)
				}
				built := Build(rows, &stored)
				raw, mErr := json.Marshal(built)
				if mErr != nil {
					return mErr
				}
				if err := tx.Model(&db.Session{}).Where("id = ?", locked.ID).Updates(map[string]any{
					"activity_summary":               db.JSONDocument(raw),
					"activity_summary_cache_version": SummaryCacheVersion,
				}).Error; err != nil {
					return err
				}
				stats.Updated++
				return nil
			})
			if err != nil {
				return stats, err
			}
		}
		log.Info("backfill activity summaries batch processed",
			zap.Uint("last_id", lastID),
			zap.Int("scanned", stats.ScannedSessions),
			zap.Int("updated", stats.Updated),
			zap.Int("already_valid", stats.AlreadyValid),
			zap.Int("chunks_loaded", stats.ChunksLoaded),
		)
	}
	return stats, nil
}
