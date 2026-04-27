package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

// VerifyHighlightsPayload is the payload for the highlight:verify task.
type VerifyHighlightsPayload struct {
	SessionID string
}

// VerificationResult represents the model's verdict for a single highlight segment.
type VerificationResult struct {
	Index    int    `json:"index"`
	Verified bool   `json:"verified"`
	Reason   string `json:"reason"`
}

// verifyResultBlockRegex matches fenced ```verification ... ``` blocks in model output.
var verifyResultBlockRegex = regexp.MustCompile("(?is)```verification\\s*(\\[.*?\\])\\s*```")

func NewVerifyHighlightsTask(sessionID string) (*asynq.Task, error) {
	payload := VerifyHighlightsPayload{
		SessionID: sessionID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeVerifyHighlights, data), nil
}

func (w *Worker) HandleVerifyHighlightsTask(ctx context.Context, t *asynq.Task) error {
	var p VerifyHighlightsPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing highlight verification",
		zap.String("session_id", p.SessionID),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping highlight verification.")
		return asynq.SkipRetry
	}

	// 1. Find the latest completed WOD analysis for this session
	var analysisResult db.AnalysisResult
	if err := w.DB.Where("session_id = ? AND analysis_type = ? AND status = ?",
		p.SessionID, db.AnalysisTypeWOD, "COMPLETED").
		Order("created_at DESC").
		First(&analysisResult).Error; err != nil {
		w.logger.Error("No completed WOD analysis found for verification",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("no completed WOD analysis found: %w", asynq.SkipRetry)
	}

	if analysisResult.HighlightSegments == "" {
		w.logger.Warn("No highlight segments to verify",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("no highlight segments available: %w", asynq.SkipRetry)
	}

	// 2. Parse highlight segments
	var segments []HighlightSegment
	if err := json.Unmarshal([]byte(analysisResult.HighlightSegments), &segments); err != nil {
		w.logger.Error("Failed to parse highlight segments",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("invalid highlight segments JSON: %w", asynq.SkipRetry)
	}

	if len(segments) == 0 {
		w.logger.Warn("Empty highlight segments array",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("no highlight segments to verify: %w", asynq.SkipRetry)
	}

	// 3. Find the source video in GCS (prefer _merged_, fall back to _encoded)
	videoURI, err := w.findSourceVideo(ctx, p.SessionID)
	if err != nil {
		w.logger.Error("Failed to find source video for verification",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("source video not found: %w", asynq.SkipRetry)
	}

	w.logger.Info("Source video found for verification",
		zap.String("session_id", p.SessionID),
		zap.String("video_uri", videoURI))

	// 4. Download video and upload to Gemini Files API
	localFilePath, err := createTempFile("verify", ".mp4")
	if err != nil {
		return err
	}

	if err := w.StorageClient.DownloadFile(ctx, videoURI, localFilePath); err != nil {
		return fmt.Errorf("failed to download video: %w", err)
	}
	defer os.Remove(localFilePath)

	upload, err := w.GeminiClient.UploadVideo(ctx, localFilePath)
	if err != nil {
		return fmt.Errorf("failed to upload video to Gemini: %w", err)
	}
	defer func() {
		if delErr := w.GeminiClient.DeleteFile(ctx, upload.FileName); delErr != nil {
			w.logger.Error("Failed to delete Gemini file after verification", zap.Error(delErr))
		}
	}()

	// 5. Build verification prompt listing all highlights
	prompt := buildVerificationPrompt(segments)

	w.logger.Info("Sending verification query to Flash model",
		zap.String("session_id", p.SessionID),
		zap.Int("segment_count", len(segments)))

	// 6. Query with Flash model (single call for all segments)
	output, verifyUsage, err := w.GeminiClient.QueryVideoFlash(ctx, upload.FileURI, upload.MIMEType, prompt)
	if err != nil {
		return fmt.Errorf("flash verification query failed: %w", err)
	}

	w.saveTokenUsage(p.SessionID, 0, "highlight:verify", verifyUsage)

	// 7. Parse verification results
	results := parseVerificationResults(output, len(segments))
	allVerified := true
	for _, r := range results {
		w.logger.Info("Verification result",
			zap.Int("index", r.Index),
			zap.Bool("verified", r.Verified),
			zap.String("reason", r.Reason))
		if !r.Verified {
			allVerified = false
		}
	}

	// If we couldn't parse any results, treat as inconclusive (don't update)
	if len(results) == 0 {
		w.logger.Warn("Could not parse verification results from model output",
			zap.String("session_id", p.SessionID),
			zap.String("raw_output", output))
		return fmt.Errorf("verification results unparseable")
	}

	// 8. Update the analysis result with the verified flag
	if err := w.DB.Model(&db.AnalysisResult{}).
		Where("id = ?", analysisResult.ID).
		Update("verified", allVerified).Error; err != nil {
		return fmt.Errorf("failed to update verified flag: %w", err)
	}

	w.logger.Info("Highlight verification completed",
		zap.String("session_id", p.SessionID),
		zap.Bool("all_verified", allVerified),
		zap.Int("total_segments", len(segments)),
		zap.Int("results_parsed", len(results)))

	return nil
}

// findSourceVideo lists GCS objects for the session and returns the best source video URI.
// Priority: _merged_ > _encoded > any other video file.
func (w *Worker) findSourceVideo(ctx context.Context, sessionID string) (string, error) {
	prefix := fmt.Sprintf("videos/%s", sessionID)
	objects, err := w.StorageClient.ListObjects(ctx, prefix)
	if err != nil {
		return "", fmt.Errorf("failed to list objects: %w", err)
	}

	var mergedURI, encodedURI, fallbackURI string
	for _, obj := range objects {
		base := filepath.Base(obj)
		switch {
		case strings.Contains(base, "_hardsubbed_"):
			continue // skip subtitle-burned copies
		case strings.Contains(base, "_merged_"):
			mergedURI = obj
		case strings.Contains(base, "_encoded"):
			encodedURI = obj
		default:
			if fallbackURI == "" {
				fallbackURI = obj
			}
		}
	}

	if mergedURI != "" {
		return mergedURI, nil
	}
	if encodedURI != "" {
		return encodedURI, nil
	}
	if fallbackURI != "" {
		return fallbackURI, nil
	}

	return "", fmt.Errorf("no video files found for session %s", sessionID)
}

// buildVerificationPrompt creates a Flash model prompt that asks the model to verify
// each highlight segment in a single pass.
func buildVerificationPrompt(segments []HighlightSegment) string {
	var segList strings.Builder
	for i, seg := range segments {
		segList.WriteString(fmt.Sprintf("%d. [%s ~ %s] type=%s, reason=\"%s\"\n",
			i, seg.Start, seg.End, seg.Type, seg.Reason))
	}

	return fmt.Sprintf(`## Task: Highlight Verification

You are a sports video analyst. The following highlight segments were identified by AI analysis.
Your job is to watch the video and verify whether each claimed observation is ACTUALLY VISIBLE at the specified timestamps.

## Claimed Highlights
%s
## Instructions
1. For each highlight, go to the specified timestamp range and watch carefully.
2. Check if the claimed type/reason matches what you actually see.
3. A segment is "verified" if there IS meaningful exercise/movement activity at that timestamp AND the description reasonably matches.
4. A segment is NOT verified if: the timestamp shows rest/nothing, or the described movement is completely different from what's visible.

## Output Format
Return your results as a JSON array inside a verification code block:

`+"```verification\n"+`[{"index": 0, "verified": true, "reason": "Snatch movement clearly visible"}, {"index": 1, "verified": false, "reason": "Timestamp shows rest period, not squat"}]`+"\n```\n",
		segList.String())
}

// parseVerificationResults extracts the verification results from the model output.
func parseVerificationResults(output string, expectedCount int) []VerificationResult {
	match := verifyResultBlockRegex.FindStringSubmatch(output)
	if match == nil {
		// Try plain JSON as fallback
		re := regexp.MustCompile(`(?s)\[.*?\]`)
		jsonMatch := re.FindString(output)
		if jsonMatch == "" {
			return nil
		}
		var results []VerificationResult
		if err := json.Unmarshal([]byte(jsonMatch), &results); err != nil {
			return nil
		}
		return filterValidResults(results, expectedCount)
	}

	var results []VerificationResult
	if err := json.Unmarshal([]byte(match[1]), &results); err != nil {
		return nil
	}
	return filterValidResults(results, expectedCount)
}

// filterValidResults removes results with out-of-range indices.
func filterValidResults(results []VerificationResult, maxIndex int) []VerificationResult {
	var valid []VerificationResult
	for _, r := range results {
		if r.Index >= 0 && r.Index < maxIndex {
			valid = append(valid, r)
		}
	}
	return valid
}
