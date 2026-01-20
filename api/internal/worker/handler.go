package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
)

const (
	TypeVideoAnalysis = "video:analysis"
)

type VideoAnalysisPayload struct {
	SessionID string
	FilePath  string
}

func NewVideoAnalysisTask(sessionID, filePath string) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID: sessionID,
		FilePath:  filePath,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeVideoAnalysis, data), nil
}

func HandleVideoAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	log.Printf("Processing video analysis for session: %s", p.SessionID)

	// Update status to PROCESSING (optional, if we tracked specific task IDs, but here we just append results)
	// For simplicity, we just create a new result when done.

	geminiClient, err := gemini.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gemini client: %w", err)
	}
	defer geminiClient.Close()

	prompt := "Analyze the movement and find strong point and weakness. Also it is intensive enough for the user's fitness level"
	analysis, geminiFile, err := geminiClient.AnalyzeVideo(ctx, p.FilePath, prompt)

	// Clean up local file
	defer func() {
		if err := os.Remove(p.FilePath); err != nil {
			log.Printf("Failed to remove temp file: %v", err)
		}
	}()

	// Clean up Gemini file if it was uploaded
	if geminiFile != "" {
		defer func() {
			if err := geminiClient.DeleteFile(ctx, geminiFile); err != nil {
				log.Printf("Failed to delete file from Gemini: %v", err)
			}
		}()
	}

	if err != nil {
		log.Printf("Analysis failed: %v", err)
		// Save failure to DB
		db.DB.Create(&db.AnalysisResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Output:    err.Error(),
		})
		return err
	}

	// Save success to DB
	db.DB.Create(&db.AnalysisResult{
		SessionID: p.SessionID,
		Status:    "COMPLETED",
		Output:    analysis,
	})

	log.Printf("Analysis completed for session: %s", p.SessionID)
	return nil
}
