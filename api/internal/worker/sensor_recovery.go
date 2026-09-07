package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SensorRecoveryManager struct {
	db          *gorm.DB
	queueClient QueueClient
	logger      *zap.Logger
}

func NewSensorRecoveryManager(db *gorm.DB, queueClient QueueClient, logger *zap.Logger) *SensorRecoveryManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SensorRecoveryManager{
		db:          db,
		queueClient: queueClient,
		logger:      logger,
	}
}

// Start runs the recovery loop every interval (e.g. 10s), running once immediately on start.
func (m *SensorRecoveryManager) Start(ctx context.Context, interval time.Duration) {
	m.RunRecovery(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RunRecovery(ctx)
		}
	}
}

// RunRecovery executes one pass of scanning and recovering pending/running/expired sensor tasks.
func (m *SensorRecoveryManager) RunRecovery(ctx context.Context) {
	// 1. Expire overdue UPLOADING rows
	m.expireOverdueUploads(ctx)

	// 2. Scan and requeue overdue PENDING or expired RUNNING tasks
	m.requeueOverdueTasks(ctx)
}

func (m *SensorRecoveryManager) expireOverdueUploads(ctx context.Context) {
	now := time.Now().UTC()
	var rows []db.AnalysisResult

	err := m.db.WithContext(ctx).
		Where("sensor_state = ? AND sensor_next_attempt_at IS NOT NULL AND sensor_next_attempt_at <= ? AND archived_at IS NULL",
			db.SensorStateUploading, now).
		Limit(100).
		Find(&rows).Error
	if err != nil {
		m.logger.Error("failed to query overdue uploading rows", zap.Error(err))
		return
	}

	for _, r := range rows {
		tx := m.db.WithContext(ctx).Begin()
		var locked db.AnalysisResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("id = ? AND sensor_state = ?", r.ID, db.SensorStateUploading).
			First(&locked).Error; err != nil {
			tx.Rollback()
			continue
		}

		_ = tx.Model(&locked).Updates(map[string]any{
			"sensor_state":           db.SensorStateExpired,
			"sensor_next_attempt_at": nil,
			"updated_at":             now,
		}).Error
		_ = tx.Commit().Error
		m.logger.Info("marked expired sensor upload", zap.Uint("id", r.ID), zap.String("session_id", r.SessionID))
	}
}

func (m *SensorRecoveryManager) requeueOverdueTasks(ctx context.Context) {
	now := time.Now().UTC()

	var candidateIDs []uint
	// Find candidate IDs (PENDING overdue, or RUNNING expired)
	// Respect retry_not_before: if retry_not_before exists, it must be <= now.
	err := m.db.WithContext(ctx).Raw(`
		SELECT id FROM analysis_results
		WHERE archived_at IS NULL
		  AND (
		    (sensor_state = 'PENDING'
		     AND sensor_next_attempt_at IS NOT NULL
		     AND sensor_next_attempt_at <= ?
		     AND (sensor_processing->>'retry_not_before' IS NULL
		          OR (sensor_processing->>'retry_not_before')::timestamptz <= ?))
		    OR
		    (sensor_state = 'RUNNING'
		     AND sensor_next_attempt_at IS NOT NULL
		     AND sensor_next_attempt_at <= ?)
		  )
		ORDER BY id ASC
		LIMIT 100
	`, now, now, now).Scan(&candidateIDs).Error

	if err != nil {
		m.logger.Error("failed to query candidate recovery tasks", zap.Error(err))
		return
	}

	for _, id := range candidateIDs {
		m.recoverSingleTask(ctx, id)
	}
}

func (m *SensorRecoveryManager) recoverSingleTask(ctx context.Context, id uint) {
	now := time.Now().UTC()
	tx := m.db.WithContext(ctx).Begin()
	defer tx.Rollback()

	var row db.AnalysisResult
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("id = ? AND archived_at IS NULL", id).
		First(&row).Error; err != nil {
		return // locked by another worker or not found
	}

	if row.SensorState != db.SensorStatePending && row.SensorState != db.SensorStateRunning {
		return
	}

	var proc SensorProcessingRecord
	if err := json.Unmarshal([]byte(row.SensorProcessing), &proc); err != nil {
		m.logger.Error("corrupt processing record during recovery", zap.Uint("id", row.ID), zap.Error(err))
		return
	}

	if proc.RetryNotBefore != nil && now.Before(*proc.RetryNotBefore) {
		return // backoff has not elapsed yet
	}

	// Invalidate any stale lease token
	proc.LeaseToken = nil
	nextAttempt := now.Add(60 * time.Second)

	procJSON, _ := json.Marshal(proc)
	if err := tx.Model(&row).Updates(map[string]any{
		"sensor_state":           db.SensorStatePending,
		"sensor_processing":      db.JSONDocument(procJSON),
		"sensor_next_attempt_at": &nextAttempt,
		"updated_at":             now,
	}).Error; err != nil {
		return
	}

	if err := tx.Commit().Error; err != nil {
		return
	}

	// Enqueue to asynq sensor queue
	task, err := NewSensorTelemetryTask(row.ID, row.ProfileID, proc.RequestID, row.SensorVersion)
	if err != nil {
		m.logger.Error("failed to create sensor task during recovery", zap.Error(err))
		return
	}

	if _, err := m.queueClient.Enqueue(task, asynq.Queue(SensorQueueName), asynq.MaxRetry(0)); err != nil {
		m.logger.Warn("failed to enqueue sensor task during recovery (will be retried on next scan)", zap.Error(err))
		return
	}

	m.logger.Info("recovered and reenqueued sensor task",
		zap.Uint("id", row.ID),
		zap.String("session_id", row.SessionID),
		zap.Int64("version", row.SensorVersion))
}
