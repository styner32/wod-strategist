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

const (
	PersonalProfilePrompt = `
## 개인 프로필
분석의 정확도를 높이기 위해 개인 정보를 참고해주세요.`

	MovementPrompt = `
## 운동 컨텍스트
분석의 정확도를 높이기 위해 아래 운동 정보를 참고해주세요.`

	KnownInjuriesPrompt = `
## 알려진 부상 사항
분석의 정확도를 높이기 위해 아래 부상 사항을 참고해주세요.`

	AnalysisPrompt = `
# 운동 영상 분석 요청

## 분석 요청 사항
당신은 전문 스포츠 생체역학 전문가이자 코치입니다. 위 컨텍스트와 첨부된 영상을 바탕으로 다음 항목을 분석해 주세요.

1. **동작 분석 및 체형 평가 (Movement & Posture Analysis)**:
   - 전반적인 자세의 정확도와 가동 범위를 평가해주세요.
   - 평소 체형의 불균형이나 의심되는 증상(예: 거북목, 라운드 숄더, 일자허리, 골반 비대칭 등)이 관찰된다면 함께 짚어주세요.
   - (입력된 운동 종목이 있다면) 해당 종목의 표준 기술(Standard)과 비교해 주세요.

2. **강점 및 약점 (Strengths & Weaknesses)**:
   - 동작 수행 중 잘 유지되고 있는 부분(Core 안정성, 리듬 등)은 무엇인가요?
   - 자세가 무너지거나 힘의 누수가 발생하는 약점은 무엇인가요?

3. **피로도 및 페이스 분석 (Fatigue Analysis)**:
   - 수행 속도가 눈에 띄게 느려지거나 자세가 흐트러지기 시작하는 **정확한 시점(분:초)**을 지목해주세요.
   - 피로가 자세에 어떤 영향을 미쳤는지(예: 등이 굽음, 무릎이 모임) 설명해 주세요.

4. **개선 솔루션 (Actionable Feedback)**:
   - 다음에 이 운동을 할 때 즉시 적용할 수 있는 구체적인 팁을 3가지 제안해주세요.
   - 입력된 **운동 목표**가 있다면, 그 목표 달성을 위한 전략적 조언을 포함해 주세요.

5. **핵심 구간 타임스탬프 (Key Timestamps)**:
   - 피드백과 관련된 비디오의 중요 구간(시작 시간 - 종료 시간)을 나열하고, 해당 구간을 주목해야 하는 이유를 한 문장으로 요약해 주세요.`

	HighlightSelectionPrompt = `

7. **하이라이트 구간 (Highlight Segments)**:
   - 소셜 미디어 공유나 퍼포먼스 비교에 적합한 핵심 구간을 선별하세요.
   - 카테고리: best_form (가장 좋은 자세), worst_form (가장 나쁜 자세), fatigue_point (피로 시작 지점), key_moment (핵심 순간)
   - 영상 길이를 고려하여 **각 카테고리당 가능한 한 2개 이상의 구간**을 찾아주세요.
   - 각 구간은 3~15초 권장, 전체 시간 합계 제한은 없습니다. 자유롭게 유의미한 구간을 모두 추출하세요.
   - 반드시 아래 형식의 **highlights** JSON 코드 블록으로 출력하세요 (json이 아닌 highlights 태그 사용):
` + "```highlights\n" + `[{"start":"0:15","end":"0:28","type":"best_form","reason":"완벽한 스내치 풀 익스텐션"},{"start":"1:10","end":"1:20","type":"best_form","reason":"코어가 매우 안정적인 두번째 움직임"},{"start":"2:30","end":"2:45","type":"worst_form","reason":"무릎 내전과 등 굽음 관찰"}]` + "\n```"

	InjuryTimestampPrompt = `

6. **부상 관련 타임스탬프 (Injury-Relevant Timestamps)**:
   - 아래 부상 부위가 활발히 사용되거나 위험에 노출되는 구간을 JSON 배열로 출력하세요.
   - 반드시 아래 형식의 **injury_timestamps** JSON 코드 블록으로 출력하세요 (json이 아닌 injury_timestamps 태그 사용):
` + "```injury_timestamps\n" + `[{"start": "0:32", "end": "0:45", "reason": "무릎 내전 관찰"}]` + "\n```"
)

// highlightBlockRegex matches fenced ```highlights ... ``` blocks in Gemini output.
var highlightBlockRegex = regexp.MustCompile("(?is)```highlights\\s*(\\[.*?\\])\\s*```")

// injuryTimestampBlockRegex matches fenced ```injury_timestamps ... ``` blocks in Gemini output.
var injuryTimestampBlockRegex = regexp.MustCompile("(?is)```injury_timestamps\\s*(\\[.*?\\])\\s*```")

func NewVideoAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:   sessionID,
		FilePath:    filePath,
		WorkoutType: NormalizeWorkoutType(workoutType),
		Movements:   movements,
		Injuries:    injuries,
		ProfileID:   profileID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeVideoAnalysis, data), nil
}

func (w *Worker) HandleVideoAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing video analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.String("workout_type", NormalizeWorkoutType(p.WorkoutType)),
		zap.Strings("movements", p.Movements),
		zap.Strings("injuries", p.Injuries),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping analysis.")
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		w.logger.Error("Invalid file path: must be a GCS URI", zap.String("file_path", p.FilePath))
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	safeSessionID := filepath.Base(p.SessionID)
	if strings.ContainsRune(safeSessionID, filepath.Separator) {
		w.logger.Error("Invalid session ID: contains path separator after sanitization", zap.String("session_id", p.SessionID))
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}

	localFilePath := filepath.Join("/tmp", fmt.Sprintf("%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

	w.logger.Info("Downloading file from GCS", zap.String("uri", p.FilePath), zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}

	prompt := w.buildAnalysisPrompt(p)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	// Clean up local file
	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			w.logger.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	// Clean up Gemini file if it was uploaded
	if geminiFile != "" {
		defer func() {
			if err := w.GeminiClient.DeleteFile(ctx, geminiFile); err != nil {
				w.logger.Error("Failed to delete file from Gemini", zap.Error(err))
			}
		}()
	}

	// retry if analysis is empty
	if analysis == "" {
		w.logger.Warn("Analysis failed. Retrying...", zap.Error(err))
		return fmt.Errorf("analysis is empty")
	}

	if err != nil {
		if os.IsNotExist(err) {
			w.logger.Error("File not found. Skipping analysis.", zap.Error(err))
			return asynq.SkipRetry
		}

		w.logger.Error("Analysis failed", zap.Error(err))

		failedResult := &db.AnalysisResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Output:    "An internal error occurred during analysis.",
		}
		if p.ProfileID > 0 {
			failedResult.ProfileID = &p.ProfileID
		}
		w.DB.Create(failedResult)

		return err
	}

	// Parse highlight segments from analysis output
	highlightSegments := parseHighlightSegments(analysis)

	result := &db.AnalysisResult{
		SessionID:         p.SessionID,
		Status:            "COMPLETED",
		Output:            analysis,
		AnalysisType:      db.AnalysisTypeWOD,
		HighlightSegments: highlightSegments,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)

	w.logger.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))

	// Agentic follow-up: if injuries are present, enqueue injury-focused re-analysis
	if len(p.Injuries) > 0 {
		focusTimestamps := parseInjuryTimestamps(analysis)
		injuryTask, taskErr := NewInjuryAnalysisTask(p.SessionID, p.FilePath, p.Injuries, p.ProfileID, focusTimestamps)
		if taskErr != nil {
			w.logger.Error("Failed to create injury analysis task", zap.Error(taskErr))
		} else if _, enqErr := w.QueueClient.Enqueue(injuryTask); enqErr != nil {
			w.logger.Error("Failed to enqueue injury analysis task", zap.Error(enqErr))
		} else {
			w.logger.Info("Injury analysis enqueued",
				zap.String("session_id", p.SessionID),
				zap.Strings("injuries", p.Injuries),
				zap.String("focus_timestamps", focusTimestamps))
		}
	}

	return nil
}

func (w *Worker) buildAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := AnalysisPrompt

	// When injuries are present, add a section requesting structured injury timestamps
	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n   - 부상 부위: %s", InjuryTimestampPrompt, strings.Join(p.Injuries, ", "))
	}

	// Always request highlight segments for short-form video generation
	prompt += HighlightSelectionPrompt

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("%s\n## 운동 종목: %s", MovementPrompt, strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)

	w.logger.Info("Personal Profile", zap.Uint("profile_id", p.ProfileID), zap.String("personal_profile", personalProfile))

	return prompt + fmt.Sprintf("%s\n## 개인 프로필: %s", PersonalProfilePrompt, personalProfile)
}

// parseHighlightSegments extracts the JSON array from the ```highlights``` code block
// in the WOD analysis output. Returns the raw JSON string, or empty on failure.
func parseHighlightSegments(analysisOutput string) string {
	match := highlightBlockRegex.FindStringSubmatch(analysisOutput)
	if match == nil {
		return ""
	}
	// Sanity-check: must be a non-empty array of valid HighlightSegments.
	var parsed []HighlightSegment
	if err := json.Unmarshal([]byte(match[1]), &parsed); err != nil || len(parsed) == 0 {
		return ""
	}
	return match[1]
}

// parseInjuryTimestamps extracts the JSON array from the ```injury_timestamps``` code block
// in the WOD analysis output. Returns the raw JSON string, or empty on failure.
func parseInjuryTimestamps(analysisOutput string) string {
	match := injuryTimestampBlockRegex.FindStringSubmatch(analysisOutput)
	if match == nil {
		return ""
	}
	// Sanity-check: must be a non-empty JSON array.
	var parsed []json.RawMessage
	if err := json.Unmarshal([]byte(match[1]), &parsed); err != nil || len(parsed) == 0 {
		return ""
	}
	return match[1]
}
