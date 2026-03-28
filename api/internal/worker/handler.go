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
	WorkoutTypeWOD        = "wod"
	WorkoutTypeRehab      = "rehab"
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

	RehabilitationPrompt = `
# 운동 영상 분석 요청 (재활 및 안전 복귀)

## 분석 요청 사항
당신은 전문 스포츠 재활 치료사(Physical Therapist)이자 교정 운동 전문가입니다. 위 컨텍스트와 첨부된 영상을 바탕으로, 퍼포먼스 향상보다는 **부상 악화 방지, 통제력 확보, 그리고 안전한 기능 회복**에 초점을 맞추어 다음 항목을 분석해 주세요.
(※ 참고: 사용자는 현재 부상 회복 중이거나 좌식(Seated) 등 제한된 환경에서 운동 중일 수 있습니다. 해당 부위에 무리가 가지 않는지 철저히 관찰하세요.)

1. **안정성 및 통제력 분석 (Stability & Control Analysis)**:
   - 동작이 관절에 무리가 없는 안전한 가동 범위(Pain-free ROM) 내에서 부드럽게 수행되고 있는지 평가해 주세요.
   - 제한된 자세(예: 의자에 앉아 하체 고정)에서도 척추 중립(Neutral Spine)과 코어의 안정성이 흔들림 없이 유지되는지 분석해 주세요.

2. **보상 작용 및 불균형 (Compensations & Imbalances)**:
   - 통증을 피하거나 근력 저하를 메우기 위해 다른 관절을 무리하게 사용하는 **'보상 작용'**(예: 어깨 으쓱임, 허리 과신전, 목 빠짐, 상체 반동 등)이 관찰되는지 꼼꼼히 확인해 주세요.
   - 신체 좌우의 움직임 궤적, 가동 범위, 밸런스에 비대칭이 발생하는지 평가해 주세요.

3. **템포 및 긴장도 분석 (Tempo & Tension Analysis)**:
   - 동작의 속도, 특히 중량을 버티며 내리는 구간(신장성 수축)에서 템포가 차분하고 일정하게 통제되고 있는지 평가해 주세요.
   - 근육의 피로나 관절의 불안정성으로 인해 텐션이 풀리거나 반동을 쓰기 시작하여 부상 위험이 높아지는 **정확한 시점(분:초)**을 지목해 주세요.

4. **안전 복귀 솔루션 (Safe Return-to-Play Feedback)**:
   - 부상 부위에 스트레스를 주지 않으면서 현재 동작의 질을 높일 수 있는 즉각적인 자세/호흡 교정 팁 3가지를 제안해 주세요.
   - 무리한 동작이 관찰되었다면 가동 범위를 제한하거나 난이도를 낮춘 대체 운동(Regression)을 조언하고, 향후 본 훈련(Active WOD)으로 돌아가기 위한 점진적 과부하 가이드를 포함해 주세요.

5. **핵심 모니터링 구간 타임스탬프 (Key Timestamps)**:
   - 훌륭한 통제력으로 자세가 가장 안정적인 '모범 구간'과, 보상 작용이나 불안정성이 노출되어 '주의가 필요한 구간'(시작 시간 - 종료 시간)을 나열해 주세요.
   - 각 구간을 왜 주의 깊게 모니터링해야 하는지 부상 방지 관점에서 한 문장으로 요약해 주세요.
	`

	ChunkAnalysisPrompt = `
# 운동 청크 영상 분석 요청 (실시간 피드백)

당신은 전문 코치입니다. 방금 수행된 10초 분량의 짧은 영상이 주어집니다. 
오직 딱 1~2문장으로, 즉각적인 자세 교정 또는 격려 피드백만 짧게 제시하세요.
(예: "허리가 굽고 있습니다, 가슴을 펴고 복압을 유지하세요!" 또는 "아주 좋습니다!")`
)

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
	if strings.EqualFold(workoutType, WorkoutTypeRehab) {
		return WorkoutTypeRehab
	}
	return WorkoutTypeWOD
}

func IsValidWorkoutType(workoutType string) bool {
	return workoutType == "" ||
		strings.EqualFold(workoutType, WorkoutTypeWOD) ||
		strings.EqualFold(workoutType, WorkoutTypeRehab)
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
			Output:    err.Error(),
		}
		if p.ProfileID > 0 {
			failedResult.ProfileID = &p.ProfileID
		}
		w.DB.Create(failedResult)
		return err
	}

	// Save success to DB
	result := &db.AnalysisResult{
		SessionID: p.SessionID,
		Status:    "COMPLETED",
		Output:    analysis,
	}
	if p.ProfileID > 0 {
		result.ProfileID = &p.ProfileID
	}
	w.DB.Create(result)

	logger.Log.Info("Analysis completed", zap.String("session_id", p.SessionID), zap.String("analysis", analysis))
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

	prompt := ChunkAnalysisPrompt
	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("\n컨텍스트: 진행 중인 운동은 %s 입니다.", strings.Join(p.Movements, ", "))
	}

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
	if NormalizeWorkoutType(p.WorkoutType) == WorkoutTypeRehab {
		prompt = RehabilitationPrompt
	}

	if len(p.Movements) > 0 {
		prompt += fmt.Sprintf("%s\n## 운동 종목: %s", MovementPrompt, strings.Join(p.Movements, ", "))
	}

	if len(p.Injuries) > 0 {
		prompt += fmt.Sprintf("%s\n## 알려진 부상 사항: %s", KnownInjuriesPrompt, strings.Join(p.Injuries, ", "))
	}

	// Look up profile from DB; fall back to hardcoded default if not found
	personalProfile := "생년월일: 1984년 10월 17일, 성별: 남, 키: 164cm, 몸무게: 72kg"
	if p.ProfileID > 0 && w.DB != nil {
		var profile db.Profile
		if err := w.DB.First(&profile, p.ProfileID).Error; err == nil {
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
			logger.Log.Warn("Profile not found, using default", zap.Uint("profile_id", p.ProfileID), zap.Error(err))
		}
	}

	logger.Log.Warn("Personal Profile", zap.Uint("profile_id", p.ProfileID), zap.String("personal_profile", personalProfile))

	return prompt + fmt.Sprintf("%s\n## 개인 프로필: %s", PersonalProfilePrompt, personalProfile)
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

	if len(objects) == 0 {
		logger.Log.Warn("No chunk objects found for session", zap.String("prefix", prefix))
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

	hardSubPath := filepath.Join(tmpDir, fmt.Sprintf("hardsubbed_%s.mp4", p.SessionID))

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

	// Log output file size for comparison with input
	var hardSubSizeBytes int64
	if fi, err := os.Stat(hardSubPath); err == nil {
		hardSubSizeBytes = fi.Size()
	}

	logger.Log.Info("Hard-sub: FFmpeg completed",
		zap.String("session_id", p.SessionID),
		zap.Duration("duration", elapsed),
		zap.Int64("output_size_bytes", hardSubSizeBytes),
		zap.Uint64("mem_alloc_before_mb", memBefore.Alloc/1024/1024),
		zap.Uint64("mem_alloc_after_mb", memAfter.Alloc/1024/1024),
		zap.Uint64("mem_sys_mb", memAfter.Sys/1024/1024))

	hardSubObjectName := fmt.Sprintf("videos/%s_hardsubbed_%s.mp4", p.SessionID, randSuffix)
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
