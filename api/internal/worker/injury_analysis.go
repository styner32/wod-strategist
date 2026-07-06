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

당신은 전문 스포츠 재활 치료사(Physical Therapist)이자 교정 운동 전문가입니다.
아래 타임스탬프 구간만 집중적으로 분석해 주세요. 전체 영상을 다시 분석할 필요 없습니다.

## 분석 요청 사항

1. **안정성 및 통제력 분석 (Stability & Control Analysis)**:
   - 해당 구간에서 부상 부위 관절에 무리가 없는 안전한 가동 범위(Pain-free ROM) 내에서 동작이 수행되고 있는지 평가해 주세요.
   - 척추 중립(Neutral Spine)과 코어의 안정성이 유지되는지 확인해 주세요.

2. **보상 작용 및 위험 패턴 (Compensations & Risk Patterns)**:
   - 부상 부위를 보호하기 위한 보상 작용(어깨 으쓱임, 허리 과신전, 목 빠짐, 상체 반동 등)이 관찰되는지 확인해 주세요.
   - 부상 악화 위험이 있는 움직임 패턴을 구체적으로 지적해 주세요.

3. **안전 복귀 솔루션 (Safe Return-to-Play Feedback)**:
   - 부상 부위에 스트레스를 주지 않으면서 동작의 질을 높일 수 있는 즉각적인 자세/호흡 교정 팁 3가지를 제안해 주세요.
   - 무리한 동작이 관찰되었다면 가동 범위를 제한하거나 난이도를 낮춘 대체 운동(Regression)을 조언해 주세요.`

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
		if err != nil {
			w.logger.Error("Full injury analysis failed", zap.String("variant", variant), zap.Error(err))
			return ""
		}
		w.saveTokenUsage(p.SessionID, p.ProfileID, "injury:segment:"+variant, usage)
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
		if err != nil {
			w.logger.Error("Injury segment analysis failed",
				zap.String("variant", variant), zap.Error(err))
			continue
		}
		w.saveTokenUsage(p.SessionID, p.ProfileID, "injury:segment:"+variant, usage)
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

	if analysis == "" {
		w.logger.Warn("Injury analysis returned empty. Retrying...", zap.Error(err))
		return fmt.Errorf("injury analysis is empty")
	}

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

	return prompt
}
