package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/subtitle"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TypeVideoAnalysis     = "video:analysis"
	TypeChunkAnalysis     = "chunk:analysis"
	TypeMergeChunks       = "merge:chunks"
	TypeInjuryAnalysis    = "injury:analysis"
	WorkoutTypeWOD        = "wod"
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

	InjuryTimestampPrompt = `

6. **부상 관련 타임스탬프 (Injury-Relevant Timestamps)**:
   - 아래 부상 부위가 활발히 사용되거나 위험에 노출되는 구간을 JSON 배열로 출력하세요.
   - 반드시 아래 형식의 JSON 코드 블록으로 출력하세요:
` + "```json\n" + `[{"start": "0:32", "end": "0:45", "reason": "무릎 내전 관찰"}]` + "\n```"

	InjuryAnalysisPrompt = `
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

	ChunkAnalysisPrompt = `
# 운동 청크 영상 분석 요청 (실시간 피드백)

당신은 실시간 코칭을 제공하는 전문 코치입니다.
방금 수행된 약 10초 분량의 짧은 영상 클립이 주어집니다.

## 규칙
- **반드시 1~2문장**으로만 답하세요. 길게 쓰지 마세요.
- 아래 컨텍스트를 참고하되, 컨텍스트 자체를 반복하지 마세요.
- 자세 교정이 필요하면 → 구체적인 교정 큐(예: "팔꿈치를 더 높이 유지하세요")를 제시하세요.
- 자세가 좋으면 → 어떤 점이 좋은지 짧게 격려하세요.
- 부상 사항이 있으면 → 해당 부위에 위험한 움직임이 보일 때만 안전 경고를 우선하세요.`
)

// jsonBlockRegex matches fenced ```json ... ``` blocks in Gemini output.
var jsonBlockRegex = regexp.MustCompile("(?s)```json\\s*(\\[.*?\\])\\s*```")

type VideoAnalysisPayload struct {
	SessionID   string
	FilePath    string
	WorkoutType string
	Movements   []string
	Injuries    []string
	ProfileID   uint
	StartSecs   float64
	EndSecs     float64
}

type InjuryAnalysisPayload struct {
	SessionID       string
	FilePath        string
	Injuries        []string
	ProfileID       uint
	FocusTimestamps string // JSON array [{"start":"0:32","end":"0:45","reason":"..."}]
}

type QueueClient interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type Worker struct {
	DB            *gorm.DB
	StorageClient *storage.Client
	BucketName    string
	GeminiClient  *gemini.Client
	QueueClient   QueueClient
}

func NewWorker(db *gorm.DB, storageClient *storage.Client, bucketName string, geminiClient *gemini.Client, queueClient QueueClient) *Worker {
	return &Worker{
		DB:            db,
		StorageClient: storageClient,
		BucketName:    bucketName,
		GeminiClient:  geminiClient,
		QueueClient:   queueClient,
	}
}

func NormalizeWorkoutType(workoutType string) string {
	return WorkoutTypeWOD
}

func IsValidWorkoutType(workoutType string) bool {
	return true // All values normalize to "wod"
}

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

func (w *Worker) HandleVideoAnalysisTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok {
		retryCount = 0
	}

	logger.Log.Info("Processing video analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.String("workout_type", NormalizeWorkoutType(p.WorkoutType)),
		zap.Strings("movements", p.Movements),
		zap.Strings("injuries", p.Injuries),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		logger.Log.Error("Max retries reached. Skipping analysis.")
		return asynq.SkipRetry
	}

	// Determine file path (download from GCS if needed)
	if !strings.HasPrefix(p.FilePath, "gs://") {
		logger.Log.Error("Invalid file path: must be a GCS URI", zap.String("file_path", p.FilePath))
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	// Determine file path (download from GCS if needed)
	safeSessionID := filepath.Base(p.SessionID)

	// check if safeSessionID contains path separator
	if strings.ContainsRune(safeSessionID, filepath.Separator) {
		logger.Log.Error("Invalid session ID: contains path separator after sanitization", zap.String("session_id", p.SessionID))
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}

	// make sure ../ is not in the path
	localFilePath := filepath.Join("/tmp", fmt.Sprintf("%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

	logger.Log.Info("Downloading file from GCS", zap.String("uri", p.FilePath), zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}

	// Update status to PROCESSING (optional, if we tracked specific task IDs, but here we just append results)
	// For simplicity, we just create a new result when done.
	prompt := w.buildAnalysisPrompt(p)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	// retry if analysis is empty
	if analysis == "" {
		logger.Log.Warn("Analysis failed. Retrying...", zap.Error(err))
		return fmt.Errorf("analysis is empty")
	}

	// Clean up local file
	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			logger.Log.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	// Clean up Gemini file if it was uploaded
	if geminiFile != "" {
		defer func() {
			if err := w.GeminiClient.DeleteFile(ctx, geminiFile); err != nil {
				logger.Log.Error("Failed to delete file from Gemini", zap.Error(err))
			}
		}()
	}

	if err != nil {
		if os.IsNotExist(err) {
			logger.Log.Error("File not found. Skipping analysis.", zap.Error(err))
			return asynq.SkipRetry
		}

		logger.Log.Error("Analysis failed", zap.Error(err))

		// Save failure to DB
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

	// Save success to DB
	result := &db.AnalysisResult{
		SessionID:    p.SessionID,
		Status:       "COMPLETED",
		Output:       analysis,
		AnalysisType: db.AnalysisTypeWOD,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)

	logger.Log.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))

	// Agentic follow-up: if injuries are present, enqueue injury-focused re-analysis
	if len(p.Injuries) > 0 {
		focusTimestamps := parseInjuryTimestamps(analysis)
		injuryTask, taskErr := NewInjuryAnalysisTask(p.SessionID, p.FilePath, p.Injuries, p.ProfileID, focusTimestamps)
		if taskErr != nil {
			logger.Log.Error("Failed to create injury analysis task", zap.Error(taskErr))
		} else if _, enqErr := w.QueueClient.Enqueue(injuryTask); enqErr != nil {
			logger.Log.Error("Failed to enqueue injury analysis task", zap.Error(enqErr))
		} else {
			logger.Log.Info("Injury analysis enqueued",
				zap.String("session_id", p.SessionID),
				zap.Strings("injuries", p.Injuries),
				zap.String("focus_timestamps", focusTimestamps))
		}
	}

	return nil
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

	logger.Log.Info("Processing chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		logger.Log.Error("Max retries reached. Skipping chunk analysis.")
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	safeSessionID := filepath.Base(p.SessionID)
	if strings.ContainsRune(safeSessionID, filepath.Separator) {
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}

	localFilePath := filepath.Join("/tmp", fmt.Sprintf("chunk_%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download chunk file from GCS: %w", err)
	}

	prompt := w.buildChunkAnalysisPrompt(p)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	if analysis == "" {
		return fmt.Errorf("chunk analysis is empty")
	}

	defer func() {
		os.Remove(localFilePath)
	}()

	if geminiFile != "" {
		defer func() {
			w.GeminiClient.DeleteFile(ctx, geminiFile)
		}()
	}

	if err != nil {
		logger.Log.Error("Chunk analysis failed", zap.Error(err))
		chunkFailed := &db.ChunkAnalysisResult{
			SessionID: p.SessionID,
			Status:    "FAILED",
			Output:    err.Error(),
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

	chunkResult := &db.ChunkAnalysisResult{
		SessionID: p.SessionID,
		Status:    "COMPLETED",
		Output:    analysis,
	}
	if p.ProfileID > 0 {
		chunkResult.ProfileID = &p.ProfileID
	}
	if p.StartSecs > 0 || p.EndSecs > 0 {
		chunkResult.StartSecs = &p.StartSecs
		chunkResult.EndSecs = &p.EndSecs
	}
	w.DB.Create(chunkResult)

	logger.Log.Info("Chunk analysis completed", zap.String("session_id", p.SessionID))
	return nil
}

func (w *Worker) buildAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := AnalysisPrompt

	// When injuries are present, add a section requesting structured injury timestamps
	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n   - 부상 부위: %s", InjuryTimestampPrompt, strings.Join(p.Injuries, ", "))
	}

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("%s\n## 운동 종목: %s", MovementPrompt, strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)

	logger.Log.Info("Personal Profile", zap.Uint("profile_id", p.ProfileID), zap.String("personal_profile", personalProfile))

	return prompt + fmt.Sprintf("%s\n## 개인 프로필: %s", PersonalProfilePrompt, personalProfile)
}

func (w *Worker) buildChunkAnalysisPrompt(p VideoAnalysisPayload) string {
	prompt := ChunkAnalysisPrompt

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("\n\n## 운동 종목\n%s", strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("\n\n## 알려진 부상 사항\n%s\n(이 부위에 위험한 자세가 보이면 반드시 경고하세요)", strings.Join(p.Injuries, ", "))
	}

	personalProfile := w.lookupProfileString(p.ProfileID)
	prompt += fmt.Sprintf("\n\n## 개인 프로필\n%s", personalProfile)

	return prompt
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

// lookupProfileString returns a human-readable profile string for the given profile ID.
func (w *Worker) lookupProfileString(profileID uint) string {
	personalProfile := "생년월일: 1984년 10월 17일, 성별: 남, 키: 164cm, 몸무게: 72kg"
	if profileID > 0 && w.DB != nil {
		var profile db.Profile
		if err := w.DB.First(&profile, profileID).Error; err == nil {
			genderKo := "기타"
			switch profile.Gender {
			case "male":
				genderKo = "남"
			case "female":
				genderKo = "여"
			}
			personalProfile = fmt.Sprintf("생년월일: %d년 %d월 %d일, 성별: %s, 키: %dcm, 몸무게: %.1fkg",
				profile.BirthYear, profile.BirthMonth, profile.BirthDay,
				genderKo, profile.HeightCm, profile.WeightKg)
		} else {
			logger.Log.Warn("Profile not found, using default", zap.Uint("profile_id", profileID), zap.Error(err))
		}
	}
	return personalProfile
}

// parseInjuryTimestamps extracts the JSON array from the injury-relevant timestamp
// section of the WOD analysis output. Returns the raw JSON string, or empty on failure.
func parseInjuryTimestamps(analysisOutput string) string {
	matches := jsonBlockRegex.FindStringSubmatch(analysisOutput)
	if len(matches) < 2 {
		return ""
	}
	// Validate it's actually a JSON array
	var parsed []json.RawMessage
	if err := json.Unmarshal([]byte(matches[1]), &parsed); err != nil {
		return ""
	}
	return matches[1]
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

	logger.Log.Info("Processing injury analysis",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath),
		zap.Strings("injuries", p.Injuries),
		zap.String("focus_timestamps", p.FocusTimestamps),
		zap.Int("retry_count", int(retryCount)))

	if retryCount >= 3 {
		logger.Log.Error("Max retries reached. Skipping injury analysis.")
		return asynq.SkipRetry
	}

	if !strings.HasPrefix(p.FilePath, "gs://") {
		logger.Log.Error("Invalid file path: must be a GCS URI", zap.String("file_path", p.FilePath))
		return fmt.Errorf("invalid file path: %w", asynq.SkipRetry)
	}

	safeSessionID := filepath.Base(p.SessionID)
	if strings.ContainsRune(safeSessionID, filepath.Separator) {
		return fmt.Errorf("invalid session ID: %w", asynq.SkipRetry)
	}

	localFilePath := filepath.Join("/tmp", fmt.Sprintf("injury_%s_%s", strings.ReplaceAll(safeSessionID, ".", "_"), filepath.Base(p.FilePath)))

	logger.Log.Info("Downloading file from GCS for injury analysis", zap.String("uri", p.FilePath), zap.String("dest", localFilePath))
	if err := w.StorageClient.DownloadFile(ctx, p.FilePath, localFilePath); err != nil {
		return fmt.Errorf("failed to download file from GCS: %w", err)
	}

	defer func() {
		if err := os.Remove(localFilePath); err != nil {
			logger.Log.Error("Failed to remove temp file", zap.Error(err))
		}
	}()

	prompt := w.buildInjuryAnalysisPrompt(p)

	analysis, geminiFile, err := w.GeminiClient.AnalyzeVideo(ctx, localFilePath, prompt)

	if geminiFile != "" {
		defer func() {
			if delErr := w.GeminiClient.DeleteFile(ctx, geminiFile); delErr != nil {
				logger.Log.Error("Failed to delete file from Gemini", zap.Error(delErr))
			}
		}()
	}

	if analysis == "" {
		logger.Log.Warn("Injury analysis returned empty. Retrying...", zap.Error(err))
		return fmt.Errorf("injury analysis is empty")
	}

	if err != nil {
		logger.Log.Error("Injury analysis failed", zap.Error(err))
		failedResult := &db.AnalysisResult{
			SessionID:    p.SessionID,
			AnalysisType: db.AnalysisTypeInjurySupplement,
			Status:       "FAILED",
			Output:       "An internal error occurred during injury analysis.",
		}
		if p.ProfileID > 0 {
			failedResult.ProfileID = &p.ProfileID
		}
		w.DB.Create(failedResult)
		return err
	}

	result := &db.AnalysisResult{
		SessionID:    p.SessionID,
		AnalysisType: db.AnalysisTypeInjurySupplement,
		Status:       "COMPLETED",
		Output:       analysis,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)

	logger.Log.Info("Injury analysis completed",
		zap.String("session_id", p.SessionID),
		zap.String("analysis", analysis))
	return nil
}

func NewMergeChunksTask(sessionID, filePath, workoutType string, movements []string, injuries []string, profileID uint) (*asynq.Task, error) {
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
	return asynq.NewTask(TypeMergeChunks, data), nil
}

func (w *Worker) HandleMergeChunksTask(ctx context.Context, t *asynq.Task) error {
	var p VideoAnalysisPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	logger.Log.Info("Processing merge chunks",
		zap.String("session_id", p.SessionID),
		zap.String("file_path", p.FilePath))

	// Extract session prefix from the placeholder GCS URI (e.g. "videos/session_xxx")
	_, prefix, err := storage.ParseGCSURI(p.FilePath)
	if err != nil {
		return fmt.Errorf("invalid GCS URI for merge prefix: %w", asynq.SkipRetry)
	}

	// 1. List all chunk objects matching the session prefix
	objects, err := w.StorageClient.ListObjects(ctx, prefix)
	if err != nil {
		return fmt.Errorf("failed to list chunk objects: %w", err)
	}

	// Filter out previously merged/hardsubbed files that share the same prefix
	var chunkObjects []string
	for _, obj := range objects {
		base := filepath.Base(obj)
		if strings.Contains(base, "_merged_") || strings.Contains(base, "_hardsubbed_") {
			logger.Log.Info("Skipping non-chunk object", zap.String("object", obj))
			continue
		}
		chunkObjects = append(chunkObjects, obj)
	}
	objects = chunkObjects

	if len(objects) == 0 {
		logger.Log.Warn("No chunk objects found for session (after filtering)", zap.String("prefix", prefix))
		return fmt.Errorf("no chunks found: %w", asynq.SkipRetry)
	}

	// Sort objects to ensure chronological order (filenames contain timestamps)
	sortStrings(objects)

	logger.Log.Info("Found chunk objects", zap.Int("count", len(objects)), zap.Strings("objects", objects))

	// 2. Download all chunks to /tmp/
	tmpDir := filepath.Join("/tmp", fmt.Sprintf("merge_%s_%d", strings.ReplaceAll(filepath.Base(p.SessionID), ".", "_"), os.Getpid()))
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("failed to create merge temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var localChunkPaths []string
	for i, obj := range objects {
		localPath := filepath.Join(tmpDir, fmt.Sprintf("chunk_%03d_%s", i, filepath.Base(obj)))
		gcsURI := fmt.Sprintf("gs://%s/%s", w.BucketName, obj)
		if err := w.StorageClient.DownloadFile(ctx, gcsURI, localPath); err != nil {
			return fmt.Errorf("failed to download chunk %s: %w", obj, err)
		}
		localChunkPaths = append(localChunkPaths, localPath)
		logger.Log.Info("Downloaded chunk", zap.Int("index", i), zap.String("object", obj))
	}

	// 3. Create FFmpeg concat file list and merge in a single pass
	concatListPath := filepath.Join(tmpDir, "concat_list.txt")
	var concatEntries []string
	for _, lp := range localChunkPaths {
		concatEntries = append(concatEntries, fmt.Sprintf("file '%s'", lp))
	}
	if err := os.WriteFile(concatListPath, []byte(strings.Join(concatEntries, "\n")), 0o644); err != nil {
		return fmt.Errorf("failed to write concat list: %w", err)
	}

	mergedPath := filepath.Join(tmpDir, fmt.Sprintf("merged_%s.mp4", p.SessionID))
	if err := runFFmpegConcat(ctx, concatListPath, mergedPath); err != nil {
		return fmt.Errorf("ffmpeg merge failed: %w", err)
	}

	// 4. Upload merged file to GCS
	randSuffix := randomHex(4)
	mergedObjectName := fmt.Sprintf("videos/%s_merged_%s.mp4", p.SessionID, randSuffix)
	mergedGCSURI, err := w.StorageClient.UploadFromFile(ctx, mergedPath, mergedObjectName)
	if err != nil {
		return fmt.Errorf("failed to upload merged video: %w", err)
	}

	logger.Log.Info("Merged video uploaded", zap.String("gcs_uri", mergedGCSURI))

	// 5. Hard-sub: burn chunk analysis subtitles into the merged video.
	//
	// WARNING: Hard-subbing requires full decode → re-encode of every frame.
	// For a 5-min 720p video, expect ~200–400 MB RAM and ~2–5 min CPU time.
	// Uses -preset ultrafast to minimise CPU time at the cost of ~40% larger files.
	// Requires FFmpeg compiled with --enable-libass (subtitles filter).
	//
	// This step is best-effort: if it fails (e.g. missing libass, DB error,
	// insufficient resources), we log the error and proceed without hard-subs.
	// TODO: Consider moving to a separate, resource-heavy worker queue.
	hardSubGCSURI := w.tryHardSub(ctx, p, tmpDir, mergedPath, randSuffix)

	// 6. Enqueue full video analysis on the merged file
	analysisTask, err := NewVideoAnalysisTask(p.SessionID, mergedGCSURI, p.WorkoutType, p.Movements, p.Injuries, p.ProfileID)
	if err != nil {
		return fmt.Errorf("failed to create analysis task for merged video: %w", err)
	}

	if _, err := w.QueueClient.Enqueue(analysisTask); err != nil {
		return fmt.Errorf("failed to enqueue analysis task for merged video: %w", err)
	}

	logger.Log.Info("Analysis enqueued for merged video",
		zap.String("session_id", p.SessionID),
		zap.String("hardsub_uri", hardSubGCSURI))
	return nil
}

// tryHardSub attempts to burn chunk analysis subtitles into the merged video.
// Returns the GCS URI of the hard-subbed video on success, or empty string on
// failure. Errors are logged but never propagated — hard-sub is best-effort.
func (w *Worker) tryHardSub(ctx context.Context, p VideoAnalysisPayload, tmpDir, mergedPath, randSuffix string) string {
	logger.Log.Info("Hard-sub: starting",
		zap.String("session_id", p.SessionID),
		zap.String("tmp_dir", tmpDir),
		zap.String("merged_path", mergedPath),
		zap.String("rand_suffix", randSuffix))

	// Verify merged video exists and is non-empty
	if fi, err := os.Stat(mergedPath); err != nil {
		logger.Log.Warn("Hard-sub: merged video file not found, skipping",
			zap.String("merged_path", mergedPath), zap.Error(err))
		return ""
	} else {
		logger.Log.Info("Hard-sub: merged video file OK",
			zap.String("merged_path", mergedPath),
			zap.Int64("size_bytes", fi.Size()))
	}

	var chunks []db.ChunkAnalysisResult
	if err := w.DB.Where("session_id = ? AND status = ?", p.SessionID, "COMPLETED").
		Order("start_secs ASC").
		Find(&chunks).Error; err != nil {
		logger.Log.Warn("Hard-sub: failed to query chunk analysis, skipping",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return ""
	}

	logger.Log.Info("Hard-sub: queried chunk analysis",
		zap.String("session_id", p.SessionID),
		zap.Int("total_chunks", len(chunks)))

	// Log each chunk's details for debugging
	for i, ch := range chunks {
		var startSecs, endSecs float64
		if ch.StartSecs != nil {
			startSecs = *ch.StartSecs
		}
		if ch.EndSecs != nil {
			endSecs = *ch.EndSecs
		}
		outputPreview := ch.Output
		if len(outputPreview) > 80 {
			outputPreview = outputPreview[:80] + "..."
		}
		logger.Log.Info("Hard-sub: chunk detail",
			zap.Int("index", i),
			zap.String("status", ch.Status),
			zap.Float64("start_secs", startSecs),
			zap.Float64("end_secs", endSecs),
			zap.Bool("has_start", ch.StartSecs != nil),
			zap.Bool("has_end", ch.EndSecs != nil),
			zap.Int("output_len", len(ch.Output)),
			zap.String("output_preview", outputPreview))
	}

	srt := subtitle.FormatSRT(chunks)
	if srt == "" {
		logger.Log.Info("Hard-sub: no subtitle content, skipping",
			zap.String("session_id", p.SessionID),
			zap.Int("total_chunks", len(chunks)))
		return ""
	}

	// Log SRT preview for debugging (first 200 chars)
	srtPreview := srt
	if len(srtPreview) > 200 {
		srtPreview = srtPreview[:200] + "..."
	}
	logger.Log.Info("Hard-sub: generated SRT",
		zap.String("session_id", p.SessionID),
		zap.Int("srt_length", len(srt)),
		zap.String("srt_preview", srtPreview))

	srtPath := filepath.Join(tmpDir, "feedback.srt")
	if err := os.WriteFile(srtPath, []byte(srt), 0o644); err != nil {
		logger.Log.Warn("Hard-sub: failed to write SRT file, skipping", zap.Error(err))
		return ""
	}

	// Verify SRT file was written correctly
	if fi, err := os.Stat(srtPath); err != nil {
		logger.Log.Warn("Hard-sub: SRT file stat failed after write",
			zap.String("srt_path", srtPath), zap.Error(err))
		return ""
	} else {
		logger.Log.Info("Hard-sub: SRT file written OK",
			zap.String("srt_path", srtPath),
			zap.Int64("size_bytes", fi.Size()))
	}

	hardSubPath := filepath.Join(tmpDir, fmt.Sprintf("hardsubbed_%s.mp4", p.SessionID))

	logger.Log.Info("Hard-sub: starting FFmpeg",
		zap.String("input", mergedPath),
		zap.String("srt", srtPath),
		zap.String("output", hardSubPath))

	// Log resource usage before and after for observability
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	start := time.Now()

	if err := runFFmpegHardSub(ctx, mergedPath, srtPath, hardSubPath); err != nil {
		logger.Log.Warn("Hard-sub: FFmpeg failed, skipping",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return ""
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	elapsed := time.Since(start)

	// Verify output file exists and is non-empty
	var hardSubSizeBytes int64
	if fi, err := os.Stat(hardSubPath); err != nil {
		logger.Log.Warn("Hard-sub: output file not found after FFmpeg",
			zap.String("output_path", hardSubPath), zap.Error(err))
		return ""
	} else {
		hardSubSizeBytes = fi.Size()
		if hardSubSizeBytes == 0 {
			logger.Log.Warn("Hard-sub: output file is empty",
				zap.String("output_path", hardSubPath))
			return ""
		}
	}

	logger.Log.Info("Hard-sub: FFmpeg completed",
		zap.String("session_id", p.SessionID),
		zap.Duration("duration", elapsed),
		zap.Int64("output_size_bytes", hardSubSizeBytes),
		zap.Uint64("mem_alloc_before_mb", memBefore.Alloc/1024/1024),
		zap.Uint64("mem_alloc_after_mb", memAfter.Alloc/1024/1024),
		zap.Uint64("mem_sys_mb", memAfter.Sys/1024/1024))

	hardSubObjectName := fmt.Sprintf("videos/%s_hardsubbed_%s.mp4", p.SessionID, randSuffix)
	logger.Log.Info("Hard-sub: uploading to GCS",
		zap.String("object_name", hardSubObjectName),
		zap.Int64("file_size_bytes", hardSubSizeBytes))

	hardSubGCSURI, err := w.StorageClient.UploadFromFile(ctx, hardSubPath, hardSubObjectName)
	if err != nil {
		logger.Log.Warn("Hard-sub: failed to upload hard-subbed video, skipping",
			zap.String("session_id", p.SessionID), zap.Error(err))
		return ""
	}

	logger.Log.Info("Hard-sub: uploaded",
		zap.String("session_id", p.SessionID),
		zap.String("gcs_uri", hardSubGCSURI))
	return hardSubGCSURI
}

func runFFmpegConcat(ctx context.Context, concatListPath, outputPath string) error {
	args := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		"-c", "copy",
		"-y",
		outputPath,
	}

	logger.Log.Info("Running FFmpeg", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Error("FFmpeg failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg: %s: %w", string(output), err)
	}

	logger.Log.Info("FFmpeg merge completed", zap.String("output_path", outputPath))
	return nil
}

// runFFmpegHardSub burns subtitles from an SRT file into a video using FFmpeg.
//
// WARNING: This runs a full decode→filter→re-encode pipeline.
// CPU: ~100% single core for realtime-to-3x duration.
// Memory: ~200–400 MB for 720p.
// Uses -preset ultrafast to reduce CPU time at cost of ~40% larger output.
func runFFmpegHardSub(ctx context.Context, inputPath, srtPath, outputPath string) error {
	args := []string{
		"-i", inputPath,
		"-vf", fmt.Sprintf("subtitles=%s", srtPath),
		"-preset", "ultrafast",
		"-c:a", "copy",
		"-y",
		outputPath,
	}

	logger.Log.Info("Running FFmpeg hard-sub", zap.Strings("args", args))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Error("FFmpeg hard-sub failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("ffmpeg hardsub: %s: %w", string(output), err)
	}

	logger.Log.Info("FFmpeg hard-sub completed", zap.String("output_path", outputPath))
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// randomHex returns a cryptographically random hex string of n bytes (2n hex chars).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
