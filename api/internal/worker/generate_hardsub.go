package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

// HardSubPayload is the payload for the hardsub:generate task.
type HardSubPayload struct {
	SessionID string `json:"session_id"`
	ProfileID uint   `json:"profile_id"`
}

// NewGenerateHardSubTask creates a new hardsub generation task.
func NewGenerateHardSubTask(sessionID string, profileID uint) (*asynq.Task, error) {
	payload := HardSubPayload{
		SessionID: sessionID,
		ProfileID: profileID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeGenerateHardSub, data), nil
}

// HandleGenerateHardSubTask downloads merged.mp4 from GCS and burns chunk
// analysis subtitles into it, producing hardsubbed.mp4.
func (w *Worker) HandleGenerateHardSubTask(ctx context.Context, t *asynq.Task) error {
	var p HardSubPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID for hardsub", zap.String("session_id", p.SessionID))
		return err
	}

	w.logger.Info("Processing hardsub generation",
		zap.String("session_id", p.SessionID),
		zap.Uint("profile_id", p.ProfileID))

	// 1. Create temp directory
	tmpDir, err := createTempDir("hardsub")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// 2. Download merged.mp4 from GCS
	mergedGCSURI := fmt.Sprintf("gs://%s/videos/%d/%s/merged.mp4", w.BucketName, p.ProfileID, p.SessionID)
	mergedPath := filepath.Join(tmpDir, "merged.mp4")

	if err := w.StorageClient.DownloadFile(ctx, mergedGCSURI, mergedPath); err != nil {
		w.logger.Error("Failed to download merged.mp4 for hardsub",
			zap.String("session_id", p.SessionID),
			zap.String("gcs_uri", mergedGCSURI),
			zap.Error(err))
		return fmt.Errorf("download merged.mp4: %w", err)
	}

	// 3. Reuse tryHardSub which queries DB for chunk analysis, generates SRT,
	//    burns subs via FFmpeg, and uploads hardsubbed.mp4 to GCS.
	analysisPayload := VideoAnalysisPayload{
		SessionID: p.SessionID,
		ProfileID: p.ProfileID,
	}

	hardSubURI := w.tryHardSub(ctx, analysisPayload, tmpDir, mergedPath)
	if hardSubURI == "" {
		w.logger.Error("Hardsub generation produced no output",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("hardsub generation failed: %w", asynq.SkipRetry)
	}

	w.logger.Info("Hardsub generation completed",
		zap.String("session_id", p.SessionID),
		zap.String("gcs_uri", hardSubURI))

	return nil
}
