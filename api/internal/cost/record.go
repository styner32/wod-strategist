package cost

import (
	"encoding/json"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RecordUsage appends actual calls. A deterministic request key is used only
// where a durable run/step identity exists; real reruns must use a different key.
func RecordUsage(conn *gorm.DB, sessionID string, profileID, userID uint, taskType, requestKey string, usage *gemini.TokenUsage) error {
	if conn == nil || usage == nil {
		return nil
	}
	record := db.TokenUsage{SessionID: sessionID, ProfileID: profileID, TaskType: taskType, Model: usage.Model,
		PromptTokens: int64(usage.PromptTokens), CandidateTokens: int64(usage.CandidateTokens), TotalTokens: int64(usage.TotalTokens)}
	details := usage.Details
	var thinkingTokens int64
	if details != nil {
		if details.Input != nil {
			record.PromptTokens = *details.Input
		}
		if details.Output != nil {
			record.CandidateTokens = *details.Output
		}
		if details.Total != nil {
			record.TotalTokens = *details.Total
		}
		if details.Thinking != nil {
			thinkingTokens = *details.Thinking
		}
	}
	if details != nil {
		raw, err := json.Marshal(details)
		if err != nil {
			return err
		}
		record.UsageMetadata = raw
	} else {
		record.UsageMetadata = []byte(`{}`)
	}
	if userID != 0 {
		record.UserID = &userID
	}
	if requestKey != "" {
		record.RequestKey = &requestKey
	}
	if err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_key"}}, DoNothing: true}).Create(&record).Error; err != nil {
		return err
	}
	if summary := strings.TrimSpace(usage.ThoughtSummary); summary != "" {
		thought := db.GeminiThoughtLog{
			ProfileID:      profileID,
			SessionID:      sessionID,
			TaskType:       taskType,
			Model:          usage.Model,
			ThinkingTokens: thinkingTokens,
			ThoughtSummary: summary,
		}
		if record.ID != 0 {
			id := record.ID
			thought.TokenUsageID = &id
		} else if requestKey != "" {
			var existing db.TokenUsage
			if conn.Select("id").Where("request_key = ?", requestKey).First(&existing).Error == nil && existing.ID != 0 {
				thought.TokenUsageID = &existing.ID
			}
		}
		if userID != 0 {
			u := userID
			thought.UserID = &u
		}
		if requestKey != "" {
			rk := requestKey
			thought.RequestKey = &rk
		}
		_ = conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_key"}}, DoNothing: true}).Create(&thought).Error
	}
	return nil
}
