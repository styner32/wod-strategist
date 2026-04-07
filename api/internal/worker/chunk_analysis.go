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

const ChunkAnalysisPrompt = `
# 운동 청크 영상 분석 요청 (실시간 피드백)

당신은 실시간 코칭을 제공하는 전문 코치입니다.
방금 수행된 약 10초 분량의 짧은 영상 클립이 주어집니다.

## 필수: 운동 종목 식별
먼저, 영상에서 수행 중인 운동 종목을 식별하세요.
- 운동이 보이면 → 첫 줄에 반드시 [EXERCISE: 영어 운동 이름] 태그를 출력하세요.
  (예: [EXERCISE: Snatch], [EXERCISE: Back Squat], [EXERCISE: Pull-up], [EXERCISE: Burpee])
- 운동이 보이지 않으면 (휴식, 걷기, 장비 세팅, 촬영 범위 밖 등) → 첫 줄에 [NO_EXERCISE] 태그만 출력하세요.

## 규칙
- 운동이 감지된 경우: [EXERCISE: ...] 태그 다음 줄에 **반드시 1~2문장**으로만 코칭 피드백을 답하세요.
- 운동이 감지되지 않은 경우: [NO_EXERCISE] 태그만 출력하고 추가 피드백을 작성하지 마세요.
- 아래 컨텍스트를 참고하되, 컨텍스트 자체를 반복하지 마세요.
- 자세 교정이 필요하면 → 구체적인 교정 큐(예: "팔꿈치를 더 높이 유지하세요")를 제시하세요.
- 자세가 좋으면 → 어떤 점이 좋은지 짧게 격려하세요.
- 부상 사항이 있으면 → 해당 부위에 위험한 움직임이 보일 때만 안전 경고를 우선하세요.`

// exerciseTagRegex matches [EXERCISE: <name>] tags in chunk analysis output.
var exerciseTagRegex = regexp.MustCompile(`(?i)\[EXERCISE:\s*(.+?)\]`)

// noExerciseTag is the tag indicating no exercise was detected in the chunk.
const noExerciseTag = "[NO_EXERCISE]"

// parseChunkExercise extracts the detected exercise name from chunk analysis output.
// Returns the exercise name (e.g. "Snatch") or empty string if no exercise detected.
func parseChunkExercise(analysis string) string {
	if strings.Contains(strings.ToUpper(analysis), noExerciseTag) {
		return ""
	}
	match := exerciseTagRegex.FindStringSubmatch(analysis)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// stripExerciseTag removes the [EXERCISE: ...] or [NO_EXERCISE] tag from the output,
// leaving just the coaching feedback.
func stripExerciseTag(analysis string) string {
	// Remove [EXERCISE: ...] tag
	result := exerciseTagRegex.ReplaceAllString(analysis, "")
	// Remove [NO_EXERCISE] tag (case insensitive)
	result = strings.ReplaceAll(result, noExerciseTag, "")
	result = strings.ReplaceAll(result, strings.ToLower(noExerciseTag), "")
	return strings.TrimSpace(result)
}

func NewChunkAnalysisTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint, startSecs, endSecs float64) (*asynq.Task, error) {
	payload := VideoAnalysisPayload{
		SessionID:   sessionID,
		FilePath:    filePath,
		WorkoutType: NormalizeWorkoutType(workoutType),
		Movements:   movements,
		Injuries:    injuries,
		ProfileID:   profileID,
		StartSecs:   startSecs,
		EndSecs:     endSecs,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeChunkAnalysis, data), nil
}

func (w *Worker) HandleChunkAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	w.logger.Info("Processing chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		w.logger.Error("Max retries reached. Skipping chunk analysis.")
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	if strings.ContainsRune(p.SessionID, filepath.Separator) {
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}
	safeSessionID := filepath.Base(p.SessionID)

	localFilePath := filepath.Join("/tmp", fmt.Sprintf("chunk_%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download chunk file from GCS: %w", err)
	}

	prompt := w.buildChunkAnalysisPrompt(p)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	defer func() {
		os.Remove(localFilePath)
	}()

	if geminiFile != "" {
		defer func() {
			w.GeminiClient.DeleteFile(ctx, geminiFile)
		}()
	}

	if analysis == "" {
		return fmt.Errorf("chunk analysis is empty")
	}

	if err != nil {
		w.logger.Error("Chunk analysis failed", zap.Error(err))
		chunkFailed := &db.ChunkAnalysisResult{
			SessionID: p.SessionID,
			FilePath:  p.FilePath,
			Status:    "FAILED",
			Output:    "An internal error occurred during chunk analysis.",
		}
		if p.ProfileID > 0 {
			chunkFailed.ProfileID = &p.ProfileID
		}
		if p.StartSecs > 0 || p.EndSecs > 0 {
			chunkFailed.StartSecs = &p.StartSecs
			chunkFailed.EndSecs = &p.EndSecs
		}
		w.DB.Create(chunkFailed)
		return err
	}

	// Extract exercise type detected by the model from the response
	detectedExercise := parseChunkExercise(analysis)
	// Strip the tag from the output, leaving just coaching feedback
	cleanOutput := stripExerciseTag(analysis)

	chunkResult := &db.ChunkAnalysisResult{
		SessionID:    p.SessionID,
		FilePath:     p.FilePath,
		ExerciseType: detectedExercise,
		Status:       "COMPLETED",
		Output:       cleanOutput,
	}
	if p.ProfileID > 0 {
		chunkResult.ProfileID = &p.ProfileID
	}
	if p.StartSecs > 0 || p.EndSecs > 0 {
		chunkResult.StartSecs = &p.StartSecs
		chunkResult.EndSecs = &p.EndSecs
	}
	w.DB.Create(chunkResult)

	w.logger.Info("Chunk analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("detected_exercise", detectedExercise))
	return nil
}

func (w *Worker) buildChunkAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := ChunkAnalysisPrompt

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("\n\n## 확인된 운동 종목 (사용자 입력)\n아래 운동들은 이 세션에서 **확실히 수행되는 운동**입니다. AI 감지가 불확실할 경우 이 목록을 우선 참고하세요.\n%s", strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s\n(이 부위에 위험한 자세가 보이면 반드시 경고하세요)", strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", personalProfile)

	return prompt
}
