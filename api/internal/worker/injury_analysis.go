package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

const InjuryAnalysisPrompt = `
# 부상 부위 집중 분석 (Injury-Focused Supplement)

당신은 운동 영상 관찰자입니다. 현재 영상에 직접 보이는 자세만 기록하고 내부 상태를 진단하지 마세요.
아래 타임스탬프 구간만 집중적으로 분석해 주세요. 전체 영상을 다시 분석할 필요 없습니다.

## 분석 요청 사항

1. **안정성 및 통제력 분석 (Stability & Control Analysis)**:
   - 해당 구간에서 직접 보이는 관절 위치와 현재 동작의 가동 범위를 시점과 함께 기록하세요. 통증 유무나 안전한 복귀 여부를 영상으로 판정하지 마세요.
   - 촬영 각도로 확인되는 척추·골반 정렬만 기록하고 코어 근력이나 복압을 추론하지 마세요.

2. **현재 자세와 위치 변화 (Posture & Position Changes)**:
   - 어깨 위치, 허리·목 정렬, 상체 궤적 등 직접 보이는 현상만 기록하세요. 부상 보호 의도나 보상 원인을 추론하지 마세요.
   - 통증 원인이나 부상 악화를 진단하지 마세요. 알려진 부상 정보 자체는 현재 영상의 관찰 증거가 아닙니다.

3. **현재 자세 큐 (Posture Cues)**:
   - 직접 보이는 정렬이나 균형 변화에 대한 짧은 자세 큐만 제시하고 근거가 없으면 팁을 만들지 마세요.
   - 가림이나 촬영 각도 때문에 확인할 수 없는 부위는 확인 불가로 남기세요.`

// InjuryAnalysisPayload is the payload for the injury:analysis task.
type InjuryAnalysisPayload struct {
	SessionID       string
	FilePath        string
	Injuries        []string
	ProfileID       uint
	FocusTimestamps string // JSON array [{"start":"0:32","end":"0:45","reason":"..."}]
	// Gemini file info — if set, reuse the uploaded file instead of re-uploading
	GeminiFileURI  string `json:"gemini_file_uri,omitempty"`
	GeminiFileName string `json:"gemini_file_name,omitempty"`
	GeminiMIMEType string `json:"gemini_mime_type,omitempty"`
	PipelineMode   string `json:"pipeline_mode,omitempty"`
}

func NewInjuryAnalysisTask(sessionID, filePath string, injuries []string, profileID uint, focusTimestamps string) (*asynq.Task, error) {
	payload := InjuryAnalysisPayload{
		SessionID:       sessionID,
		FilePath:        filePath,
		Injuries:        injuries,
		ProfileID:       profileID,
		FocusTimestamps: focusTimestamps,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeInjuryAnalysis, data), nil
}

func (w *Worker) HandleInjuryAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p InjuryAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing injury analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Strings("injuries", p.Injuries),
		zap.String("focus_timestamps", p.FocusTimestamps),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping injury analysis.")
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		w.logger.Error("Invalid file path: must be a GCS URI", zap.String("file_path", p.FilePath))
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	if err := validateSessionID(p.SessionID); err != nil {
		w.logger.Error("Invalid session ID", zap.String("session_id", p.SessionID))
		return err
	}

	// Use two-pass path when a Gemini file URI was passed from video analysis
	if p.GeminiFileURI != "" {
		return w.handleInjuryAnalysisWithFile(ctx, p)
	}
	return w.handleInjuryAnalysisLegacy(ctx, p)
}

// handleInjuryAnalysisWithFile reuses the Gemini file from video analysis.
// It analyzes each focus timestamp segment individually using AnalyzeSegment.
func (w *Worker) handleInjuryAnalysisWithFile(ctx context.Context, p InjuryAnalysisPayload) error {
	prompt := w.buildInjuryAnalysisPrompt(p)

	// Parse focus timestamps to get segment ranges
	var focusEntries []FocusEntry
	if p.FocusTimestamps != "" {
		_ = json.Unmarshal([]byte(p.FocusTimestamps), &focusEntries)
	}

	pMode := p.PipelineMode
	if pMode == "" {
		pMode = string(w.PipelineMode)
	}
	if pMode == "" && w.UseCache {
		pMode = string(PipelineModeOptimized)
	}

	var legacyIntervals []mergedInterval
	if PipelineMode(pMode) == PipelineModeLegacy || PipelineMode(pMode) == PipelineModeCompare {
		legacyIntervals = toIntervals(focusEntries)
	}

	var optIntervals []mergedInterval
	var skippedCalls int
	if PipelineMode(pMode) == PipelineModeOptimized || PipelineMode(pMode) == PipelineModeCompare {
		mergedIntervals := mergeFocusIntervals(focusEntries)
		optIntervals = capFocusIntervals(mergedIntervals, maxInjuryFocusSegments)

		if len(mergedIntervals) > 0 {
			skippedCalls = len(mergedIntervals) - len(optIntervals)
			var unionDuration float64
			for _, iv := range mergedIntervals {
				unionDuration += (iv.EndSecs - iv.StartSecs)
			}
			var keptDuration float64
			for _, iv := range optIntervals {
				keptDuration += (iv.EndSecs - iv.StartSecs)
			}
			var coverage float64 = 1.0
			if unionDuration > 0 {
				coverage = keptDuration / unionDuration
			}
			w.logger.Info("Injury coverage calculated",
				zap.Float64("coverage", coverage),
				zap.Int("merged_count", len(mergedIntervals)),
				zap.Int("capped_count", len(optIntervals)),
				zap.Int("skipped_calls", skippedCalls))
		}
	}

	var legacyOut, optOut string
	switch PipelineMode(pMode) {
	case PipelineModeLegacy:
		legacyOut = w.runInjuryVariant(ctx, p, prompt, "legacy", legacyIntervals, 0)
	case PipelineModeOptimized:
		optOut = w.runInjuryVariant(ctx, p, prompt, "optimized", optIntervals, skippedCalls)
	case PipelineModeCompare:
		if len(legacyIntervals) == 0 && len(optIntervals) == 0 {
			// With no focus entries, both variants run the identical full-video call.
			// Run once and share the output to save 2x Gemini API cost.
			legacyOut = w.runInjuryVariant(ctx, p, prompt, "legacy", legacyIntervals, 0)
			optOut = legacyOut
		} else {
			legacyOut = w.runInjuryVariant(ctx, p, prompt, "legacy", legacyIntervals, 0)
			optOut = w.runInjuryVariant(ctx, p, prompt, "optimized", optIntervals, skippedCalls)
		}
	default:
		legacyOut = w.runInjuryVariant(ctx, p, prompt, "legacy", legacyIntervals, 0)
	}

	// Determine what goes to main output vs alt output
	var mainOutput, altOutput string
	switch PipelineMode(pMode) {
	case PipelineModeLegacy:
		mainOutput = legacyOut
	case PipelineModeOptimized:
		mainOutput = optOut
	case PipelineModeCompare:
		mainOutput = legacyOut
		altOutput = optOut
	default:
		mainOutput = legacyOut
	}

	// Validate retry conditions: compare mode requires both outputs to be non-empty
	retry := false
	switch PipelineMode(pMode) {
	case PipelineModeLegacy:
		retry = mainOutput == ""
	case PipelineModeOptimized:
		retry = mainOutput == ""
	case PipelineModeCompare:
		retry = mainOutput == "" || altOutput == ""
	default:
		retry = mainOutput == ""
	}

	if retry {
		w.logger.Warn("Injury analysis variant returned empty. Retrying...",
			zap.String("mode", pMode),
			zap.Bool("legacy_empty", legacyOut == ""),
			zap.Bool("opt_empty", optOut == ""))
		return fmt.Errorf("injury analysis failed for one or more variants")
	}

	// Persist — append to existing result if present
	var existing db.AnalysisResult
	if findErr := w.DB.Where("session_id = ? AND analysis_type = ?", p.SessionID, db.AnalysisTypeWOD).First(&existing).Error; findErr == nil {
		updates := map[string]any{}
		if mainOutput != "" {
			newInjuryOutput := mainOutput
			if existing.InjuryOutput != "" {
				// Prevent duplicate appends if this is a retry/redelivery
				if !strings.Contains(existing.InjuryOutput, mainOutput) {
					newInjuryOutput = existing.InjuryOutput + "\n\n---\n\n" + mainOutput
				} else {
					newInjuryOutput = existing.InjuryOutput
				}
			}
			updates["injury_output"] = newInjuryOutput
		}
		if altOutput != "" {
			newInjuryOutputAlt := altOutput
			if existing.InjuryOutputAlt != "" {
				// Prevent duplicate appends if this is a retry/redelivery
				if !strings.Contains(existing.InjuryOutputAlt, altOutput) {
					newInjuryOutputAlt = existing.InjuryOutputAlt + "\n\n---\n\n" + altOutput
				} else {
					newInjuryOutputAlt = existing.InjuryOutputAlt
				}
			}
			updates["injury_output_alt"] = newInjuryOutputAlt
		}
		if len(updates) > 0 {
			if err := w.DB.Model(&existing).Updates(updates).Error; err != nil {
				return fmt.Errorf("failed to update existing injury result: %w", err)
			}
		}
	} else {
		result := &db.AnalysisResult{
			SessionID:       p.SessionID,
			AnalysisType:    db.AnalysisTypeInjurySupplement,
			Status:          "COMPLETED",
			Output:          mainOutput,
			InjuryOutputAlt: altOutput,
		}
		result.ProfileID = p.ProfileID
		if err := w.DB.Create(result).Error; err != nil {
			return fmt.Errorf("failed to create standalone injury result: %w", err)
		}
	}

	w.logger.Info("Injury analysis with file completed",
		zap.String("session_id", p.SessionID),
		zap.String("file_name", p.GeminiFileName))
	return nil
}

// runInjuryVariant analyzes the given intervals and returns concatenated output.
// variant is appended to the token-usage task type for cost comparison.
func (w *Worker) runInjuryVariant(ctx context.Context, p InjuryAnalysisPayload,
	basePrompt, variant string, intervals []mergedInterval, skippedCalls int) string {

	started := time.Now()
	var sb strings.Builder

	if len(intervals) == 0 {
		w.logger.Info("No specific injury segments, running full injury analysis",
			zap.String("variant", variant),
			zap.String("session_id", p.SessionID))

		analysis, usage, err := w.GeminiClient.AnalyzeSegment(
			ctx, p.GeminiFileURI, p.GeminiMIMEType, 0, 0, basePrompt)
		w.saveTokenUsage(p.SessionID, p.ProfileID, "injury:segment:"+variant, usage)
		if err != nil {
			w.logger.Error("Full injury analysis failed", zap.String("variant", variant), zap.Error(err))
			return ""
		}
		w.recordStageMetrics(p.SessionID, p.ProfileID, "injury_analysis", variant, 1, 0, 0, time.Since(started))
		return analysis
	}

	for i, iv := range intervals {
		startStr := formatDuration(time.Duration(iv.StartSecs) * time.Second)
		endStr := formatDuration(time.Duration(iv.EndSecs) * time.Second)
		segPrompt := fmt.Sprintf("%s\n\n## 분석 구간: %s ~ %s\n관찰된 사유:\n- %s",
			basePrompt, startStr, endStr,
			strings.Join(iv.Reasons, "\n- "))

		w.logger.Info("Analyzing injury segment",
			zap.String("variant", variant),
			zap.Int("segment", i+1),
			zap.String("start", startStr),
			zap.String("end", endStr))

		segAnalysis, usage, err := w.GeminiClient.AnalyzeSegment(
			ctx, p.GeminiFileURI, p.GeminiMIMEType,
			time.Duration(iv.StartSecs)*time.Second, time.Duration(iv.EndSecs)*time.Second, segPrompt)
		w.saveTokenUsage(p.SessionID, p.ProfileID, "injury:segment:"+variant, usage)
		if err != nil {
			w.logger.Error("Injury segment analysis failed",
				zap.String("variant", variant), zap.Error(err))
			continue
		}
		sb.WriteString(fmt.Sprintf("\n\n---\n## 부상 분석 구간 %d: %s ~ %s\n\n%s",
			i+1, startStr, endStr, segAnalysis))
	}

	w.recordStageMetrics(p.SessionID, p.ProfileID, "injury_analysis", variant,
		len(intervals), skippedCalls, 0, time.Since(started))
	return sb.String()
}

// handleInjuryAnalysisLegacy is the original file-upload based path.
func (w *Worker) handleInjuryAnalysisLegacy(ctx context.Context, p InjuryAnalysisPayload) error {
	localFilePath, err := createTempFile("injury", ".mp4")
	if err != nil {
		return err
	}

	w.logger.Info("Downloading file from GCS for injury analysis", zap.String("uri", p.FilePath), zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}

	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			w.logger.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	prompt := w.buildInjuryAnalysisPrompt(p)

	analysis, geminiFile, usage, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	if geminiFile != "" {
		defer func() {
			if delErr := w.GeminiClient.DeleteFile(ctx, geminiFile); delErr != nil {
				w.logger.Error("Failed to delete file from Gemini", zap.Error(delErr))
			}
		}()
	}

	// Save token usage regardless of analysis outcome
	w.saveTokenUsage(p.SessionID, p.ProfileID, "injury:legacy", usage)

	if err != nil {
		w.logger.Error("Injury analysis failed", zap.Error(err))
		// Update existing WOD row with failure note, or create standalone row
		var existing db.AnalysisResult
		if findErr := w.DB.Where("session_id = ? AND analysis_type = ?", p.SessionID, db.AnalysisTypeWOD).First(&existing).Error; findErr == nil {
			failureNote := "An internal error occurred during injury analysis."
			if existing.InjuryOutput != "" {
				failureNote = existing.InjuryOutput + "\n\n---\n\n" + failureNote
			}
			w.DB.Model(&existing).Update("injury_output", failureNote)
		} else {
			failedResult := &db.AnalysisResult{
				SessionID:    p.SessionID,
				AnalysisType: db.AnalysisTypeInjurySupplement,
				Status:       "FAILED",
				Output:       "An internal error occurred during injury analysis.",
			}
			failedResult.ProfileID = p.ProfileID
			w.DB.Create(failedResult)
		}
		return err
	}

	if analysis == "" {
		w.logger.Warn("Injury analysis returned empty. Retrying...")
		return fmt.Errorf("injury analysis is empty")
	}

	// Append injury output to existing WOD analysis row, or create standalone row as fallback
	var existing db.AnalysisResult
	if findErr := w.DB.Where("session_id = ? AND analysis_type = ?", p.SessionID, db.AnalysisTypeWOD).First(&existing).Error; findErr == nil {
		newInjuryOutput := analysis
		if existing.InjuryOutput != "" {
			newInjuryOutput = existing.InjuryOutput + "\n\n---\n\n" + analysis
		}
		w.DB.Model(&existing).Update("injury_output", newInjuryOutput)
	} else {
		result := &db.AnalysisResult{
			SessionID:    p.SessionID,
			AnalysisType: db.AnalysisTypeInjurySupplement,
			Status:       "COMPLETED",
			Output:       analysis,
		}
		result.ProfileID = p.ProfileID
		w.DB.Create(result)
	}

	w.logger.Info("Injury analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("analysis", analysis))
	return nil
}

func (w *Worker) buildInjuryAnalysisPrompt(p InjuryAnalysisPayload) string {
	prompt := InjuryAnalysisPrompt

	if p.FocusTimestamps != "" {
		prompt += fmt.Sprintf("\n\n## 집중 분석 구간 (Focus Timestamps)\n```json\n%s\n```", p.FocusTimestamps)
	} else {
		prompt += "\n\n## 집중 분석 구간\n타임스탬프가 제공되지 않았습니다. 전체 영상에서 부상 부위 관련 움직임을 분석해 주세요."
	}

	prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s", strings.Join(p.Injuries, ", "))

	personalProfile := w.lookupProfileString(p.ProfileID)
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", personalProfile)
	prompt += ObservationOnlyRulesPrompt
	prompt += SpinalObservationRulesPrompt
	prompt += ObservationOnlyOutputCheckPrompt

	return prompt
}
