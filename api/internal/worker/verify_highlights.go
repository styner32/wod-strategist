package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

// VerifyHighlightsPayload is the payload for the highlight:verify task.
type VerifyHighlightsPayload struct {
	SessionID string
}

// VerificationResult represents the model's verdict for one exact observation
// inside a parent highlight event.
type VerificationResult struct {
	EventIndex       int    `json:"event_index"`
	ObservationIndex int    `json:"observation_index"`
	Verified         bool   `json:"verified"`
	Reason           string `json:"reason"`
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
	started := time.Now()
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

	// Validate the stored array before doing Files API or storage work.
	decodedSegments, err := decodeHighlightSegmentArray(analysisResult.HighlightSegments)
	if err != nil {
		w.logger.Error("Failed to parse highlight segments",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return fmt.Errorf("invalid highlight segments JSON: %w", asynq.SkipRetry)
	}

	if len(decodedSegments) == 0 {
		w.logger.Warn("Empty highlight segments array",
			zap.String("session_id", p.SessionID))
		return fmt.Errorf("no highlight segments to verify: %w", asynq.SkipRetry)
	}

	// 3. Determine if we can reuse the existing Gemini file
	reused := analysisResult.GeminiFileURI != "" &&
		analysisResult.GeminiFileExpiresAt != nil &&
		time.Now().Before(*analysisResult.GeminiFileExpiresAt)

	var videoDuration time.Duration
	if reused {
		// Verify existence and recover the authoritative media duration from the
		// same Files object without making an extra model call.
		duration, exists, err := w.GeminiClient.FileVideoDuration(ctx, analysisResult.GeminiFileName)
		if err != nil {
			if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
				reused = false
			} else {
				return fmt.Errorf("failed to check Gemini file existence: %w", err)
			}
		} else if !exists {
			reused = false
		} else {
			videoDuration = duration
		}
	}

	var fileURI, mimeType string
	var uploadBytes int64
	var uploadFileNameToCleanup string

	if reused {
		fileURI = analysisResult.GeminiFileURI
		mimeType = analysisResult.GeminiMIMEType
		w.logger.Info("Reusing Gemini file for highlight verification",
			zap.String("session_id", p.SessionID),
			zap.String("file_uri", fileURI))
	} else {
		w.logger.Info("Gemini file not found, expired, or deleted; uploading source video",
			zap.String("session_id", p.SessionID))

		// Find the merged-media source on the profile-aware session timeline.
		videoURI, err := w.findSourceVideo(ctx, analysisResult.ProfileID, p.SessionID)
		if err != nil {
			w.logger.Error("Failed to find source video for verification",
				zap.String("session_id", p.SessionID), zap.Error(err))
			return fmt.Errorf("source video not found: %w", asynq.SkipRetry)
		}

		w.logger.Info("Source video found for verification",
			zap.String("session_id", p.SessionID),
			zap.String("video_uri", videoURI))

		localFilePath, err := createTempFile("verify", ".mp4")
		if err != nil {
			return err
		}
		defer os.Remove(localFilePath)

		if err := w.StorageClient.DownloadFile(ctx, videoURI, localFilePath); err != nil {
			return fmt.Errorf("failed to download video: %w", err)
		}

		if fi, err := os.Stat(localFilePath); err == nil {
			uploadBytes = fi.Size()
		}

		upload, err := w.GeminiClient.UploadVideo(ctx, localFilePath)
		if err != nil {
			return fmt.Errorf("failed to upload video to Gemini: %w", err)
		}
		fileURI = upload.FileURI
		mimeType = upload.MIMEType
		videoDuration = upload.VideoDuration
		uploadFileNameToCleanup = upload.FileName
	}

	defer func() {
		if uploadFileNameToCleanup != "" {
			if delErr := w.GeminiClient.DeleteFile(ctx, uploadFileNameToCleanup); delErr != nil {
				w.logger.Error("Failed to delete Gemini file after verification", zap.Error(delErr))
			}
		}
	}()

	// 4. Normalize exact observations with the processed video's media bound.
	segments, err := NormalizeHighlightSegmentsJSON(
		analysisResult.HighlightSegments,
		HighlightNormalizeOptions{VideoEndSeconds: videoDuration.Seconds()},
	)
	if err != nil {
		return fmt.Errorf("invalid highlight segments JSON: %w", asynq.SkipRetry)
	}
	if len(segments) == 0 {
		return fmt.Errorf("no highlight segments to verify: %w", asynq.SkipRetry)
	}

	// 5. Build a verification prompt over exact observations, not padded parent clips.
	prompt := buildVerificationPrompt(segments)
	observationCount := countHighlightObservations(segments)

	w.logger.Info("Sending verification query to Flash model",
		zap.String("session_id", p.SessionID),
		zap.Int("event_count", len(segments)),
		zap.Int("observation_count", observationCount))

	// 6. Query with Flash model (single call for all segments)
	output, verifyUsage, err := w.GeminiClient.QueryVideoFlash(ctx, fileURI, mimeType, prompt)
	if err != nil {
		return fmt.Errorf("flash verification query failed: %w", err)
	}

	profileID := analysisResult.ProfileID
	w.saveTokenUsage(p.SessionID, profileID, "highlight:verify", verifyUsage)

	// 7. Parse verification results
	results, parsed := parseVerificationResults(output, segments)
	if !parsed {
		w.logger.Warn("Could not parse verification results from model output",
			zap.String("session_id", p.SessionID),
			zap.String("raw_output", output))
		return fmt.Errorf("verification results unparseable")
	}
	if len(results) == 0 {
		w.logger.Warn("Verification returned empty or fully filtered results",
			zap.String("session_id", p.SessionID),
			zap.String("raw_output", output))
		return fmt.Errorf("verification results inconclusive: empty verdict set")
	}

	for _, r := range results {
		w.logger.Info("Verification result",
			zap.Int("event_index", r.EventIndex),
			zap.Int("observation_index", r.ObservationIndex),
			zap.Bool("verified", r.Verified),
			zap.String("reason", r.Reason))
	}

	verifiedSegments, allVerified := applyObservationVerification(
		segments,
		results,
		HighlightNormalizeOptions{},
	)

	// 8. Persist only verified observations and rebuild their parent events. The
	// legacy flag remains false if any original observation was rejected or omitted.
	if err := w.DB.Model(&db.AnalysisResult{}).
		Where("id = ?", analysisResult.ID).
		Updates(map[string]any{
			"highlight_segments": MarshalHighlightSegments(verifiedSegments),
			"verified":           allVerified,
		}).Error; err != nil {
		return fmt.Errorf("failed to update verified highlights: %w", err)
	}

	w.logger.Info("Highlight verification completed",
		zap.String("session_id", p.SessionID),
		zap.Bool("all_verified", allVerified),
		zap.Int("total_events", len(segments)),
		zap.Int("total_observations", observationCount),
		zap.Int("remaining_events", len(verifiedSegments)),
		zap.Int("results_parsed", len(results)))

	apiCalls := 1
	if !reused {
		apiCalls = 2
	}

	// Map pipeline mode to legacy/optimized variant
	pMode := string(w.PipelineMode)
	if pMode == "" && w.UseCache {
		pMode = string(PipelineModeOptimized)
	}
	variant := "legacy"
	if pMode == "optimized" || pMode == "compare" {
		variant = "optimized"
	}
	w.recordStageMetrics(p.SessionID, profileID, "verify_highlights", variant, apiCalls, 0, uploadBytes, time.Since(started))

	return nil
}

// findSourceVideo resolves the exact canonical merged media first. Legacy
// layouts are searched only after canonical absence and only known merged
// whole-session objects are accepted.
func (w *Worker) findSourceVideo(ctx context.Context, profileID uint, sessionID string) (string, error) {
	if strings.TrimSpace(w.BucketName) == "" {
		return "", fmt.Errorf("bucket name is empty")
	}

	canonicalObject := fmt.Sprintf("videos/%d/%s/merged.mp4", profileID, sessionID)
	objects, err := w.StorageClient.ListObjects(ctx, canonicalObject)
	if err != nil {
		return "", fmt.Errorf("failed to check canonical merged video: %w", err)
	}
	for _, obj := range objects {
		if obj == canonicalObject {
			return fmt.Sprintf("gs://%s/%s", w.BucketName, obj), nil
		}
	}

	prefixes := []string{fmt.Sprintf("videos/%d/%s/", profileID, sessionID)}
	if profileID != 0 {
		prefixes = append(prefixes, fmt.Sprintf("videos/0/%s/", sessionID))
	}
	prefixes = append(prefixes, fmt.Sprintf("videos/%s_", sessionID))

	for _, prefix := range prefixes {
		objects, listErr := w.StorageClient.ListObjects(ctx, prefix)
		if listErr != nil {
			continue
		}
		if objectName := selectLegacyMergedVideo(objects); objectName != "" {
			return fmt.Sprintf("gs://%s/%s", w.BucketName, objectName), nil
		}
	}

	return "", fmt.Errorf("no video files found for session %s", sessionID)
}

func selectLegacyMergedVideo(objects []string) string {
	selected := ""
	for _, object := range objects {
		base := strings.ToLower(filepath.Base(object))
		if base != "merged.mp4" && !(strings.Contains(base, "_merged_") && strings.HasSuffix(base, ".mp4")) {
			continue
		}
		derived := false
		for _, marker := range []string{
			"hardsub", "encoded", "chunk", "highlight", "_hl_", "tmp", "preview", "polished", "trimmed", "concat",
		} {
			if strings.Contains(base, marker) {
				derived = true
				break
			}
		}
		if derived || strings.HasPrefix(base, "hl_") {
			continue
		}
		selected = object
	}
	return selected
}

// buildVerificationPrompt asks the model to verify exact observations. Parent
// event ranges include playback padding and must not be treated as evidence.
func buildVerificationPrompt(segments []HighlightSegment) string {
	var segList strings.Builder
	for eventIndex, seg := range segments {
		for observationIndex, observation := range seg.Observations {
			segList.WriteString(fmt.Sprintf(
				"event=%d observation=%d [%s ~ %s] type=%s movement=%q reason=%q\n",
				eventIndex,
				observationIndex,
				observation.Start,
				observation.End,
				observation.Type,
				seg.Movement,
				observation.Reason,
			))
		}
	}

	return fmt.Sprintf(`## Task: Highlight Verification

You are a sports video analyst. The following exact observations were identified by AI analysis.
Your job is to watch the video and verify whether each claimed observation is ACTUALLY VISIBLE at its exact timestamp range.

## Claimed Observations
%s
## Instructions
1. Verify every event/observation pair independently at its exact observation range.
2. Do not judge the padded parent playback range before or after that observation.
3. Check whether the observation type and reason match visible evidence.
4. An observation is "verified" only when the claimed form, fatigue onset, or technique event is visible at that exact range.
5. An observation is NOT verified when the range shows rest/nothing, a different movement, or does not visibly support the claim.

## Output Format
Return your results as a JSON array inside a verification code block:

`+"```verification\n"+`[{"event_index": 0, "observation_index": 0, "verified": true, "reason": "Claim is visible at the exact range"}, {"event_index": 0, "observation_index": 1, "verified": false, "reason": "Range does not support the claimed form issue"}]`+"\n```\n",
		segList.String())
}

// parseVerificationResults extracts the verification results from the model output.
func parseVerificationResults(output string, segments []HighlightSegment) ([]VerificationResult, bool) {
	var rawJSON string
	match := verifyResultBlockRegex.FindStringSubmatch(output)
	if match == nil {
		// Try plain JSON as fallback
		re := regexp.MustCompile(`(?s)\[.*?\]`)
		rawJSON = re.FindString(output)
		if rawJSON == "" {
			return nil, false
		}
	} else {
		rawJSON = match[1]
	}

	type rawVerificationResult struct {
		EventIndex       *int   `json:"event_index"`
		ObservationIndex *int   `json:"observation_index"`
		Verified         *bool  `json:"verified"`
		Reason           string `json:"reason"`
	}
	var rawResults []rawVerificationResult
	if err := json.Unmarshal([]byte(rawJSON), &rawResults); err != nil {
		return nil, false
	}
	results := make([]VerificationResult, 0, len(rawResults))
	for _, raw := range rawResults {
		if raw.EventIndex == nil || raw.ObservationIndex == nil || raw.Verified == nil {
			continue
		}
		results = append(results, VerificationResult{
			EventIndex:       *raw.EventIndex,
			ObservationIndex: *raw.ObservationIndex,
			Verified:         *raw.Verified,
			Reason:           raw.Reason,
		})
	}
	return filterValidResults(results, segments), true
}

// filterValidResults removes results with out-of-range indices.
func filterValidResults(results []VerificationResult, segments []HighlightSegment) []VerificationResult {
	var valid []VerificationResult
	for _, r := range results {
		if r.EventIndex >= 0 && r.EventIndex < len(segments) &&
			r.ObservationIndex >= 0 && r.ObservationIndex < len(segments[r.EventIndex].Observations) {
			valid = append(valid, r)
		}
	}
	return valid
}

type observationVerificationKey struct {
	eventIndex       int
	observationIndex int
}

func applyObservationVerification(
	segments []HighlightSegment,
	results []VerificationResult,
	options HighlightNormalizeOptions,
) ([]HighlightSegment, bool) {
	verdicts := make(map[observationVerificationKey]bool, len(results))
	for _, result := range results {
		key := observationVerificationKey{
			eventIndex:       result.EventIndex,
			observationIndex: result.ObservationIndex,
		}
		if existing, ok := verdicts[key]; ok {
			// Conflicting duplicate verdicts fail closed.
			verdicts[key] = existing && result.Verified
			continue
		}
		verdicts[key] = result.Verified
	}

	allVerified := true
	verifiedCandidates := make([]highlightCandidate, 0)
	for eventIndex, segment := range segments {
		hadTechniqueObservation := HighlightSegmentHasObservationType(segment, HighlightObservationTechnique)
		preserveEvaluationKeyTag := HighlightSegmentHasTag(segment, HighlightTagKeyMoment) && !hadTechniqueObservation
		source := highlightSource{
			Index:           eventIndex * 2,
			Movement:        segment.Movement,
			HardGapBoundary: true,
		}
		parentStart, startErr := parseTimestampToSeconds(segment.Start)
		parentEnd, endErr := parseTimestampToSeconds(segment.End)
		if startErr == nil && endErr == nil && parentEnd > parentStart {
			source.Start = parentStart
			source.End = parentEnd
			source.HasBounds = true
		}
		for observationIndex, observation := range segment.Observations {
			verified, found := verdicts[observationVerificationKey{
				eventIndex:       eventIndex,
				observationIndex: observationIndex,
			}]
			if !found || !verified {
				allVerified = false
				continue
			}
			observation.Verified = boolPointer(true)
			start, startErr := parseTimestampToSeconds(observation.Start)
			end, endErr := parseTimestampToSeconds(observation.End)
			if startErr != nil || endErr != nil || end <= start || !validObservationType(observation.Type) || !validConfidence(observation.Confidence) {
				allVerified = false
				continue
			}
			tags := []string(nil)
			if observation.Type == HighlightObservationTechnique || preserveEvaluationKeyTag {
				tags = append(tags, HighlightTagKeyMoment)
			}
			verifiedCandidates = append(verifiedCandidates, highlightCandidate{
				Observation: observation,
				Movement:    segment.Movement,
				Tags:        tags,
				Start:       start,
				End:         end,
				Source:      source,
			})
		}
	}
	return consolidateHighlightCandidates(verifiedCandidates, options), allVerified
}

func countHighlightObservations(segments []HighlightSegment) int {
	count := 0
	for _, segment := range segments {
		count += len(segment.Observations)
	}
	return count
}

func boolPointer(value bool) *bool {
	return &value
}
